// Package web serves the local Kanban viewer for the ARchetipo backlog.
//
// The server exposes a small JSON API on top of the existing connector and
// serves a single-page UI from assets embedded in the binary. It is intended
// for local single-user use: it binds to 127.0.0.1 by default and ships no
// authentication.
package web

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"path/filepath"
	"time"

	"github.com/techreloaded-ar/ARchetipo/cli/internal/config"
	"github.com/techreloaded-ar/ARchetipo/cli/internal/connector"
)

// Server wires the connector backend to HTTP handlers and the embedded UI.
type Server struct {
	conn       connector.Connector
	cfg        config.Config
	mux        *http.ServeMux
	httpSrv    *http.Server
	mockupsDir string
	broker     *Broker
	watchRoot  string
}

// NewServer constructs a Server bound to addr (e.g. "127.0.0.1:8080").
// The returned server has all routes registered but is not listening yet:
// call Run to start serving. cfg is used to resolve the on-disk location of
// design mockups served under /mockups/.
func NewServer(conn connector.Connector, cfg config.Config, addr string) (*Server, error) {
	mux := http.NewServeMux()
	s := &Server{
		conn:       conn,
		cfg:        cfg,
		mux:        mux,
		mockupsDir: cfg.AbsPath(cfg.Paths.Mockups),
		broker:     NewBroker(),
		watchRoot:  resolveWatchRoot(cfg),
	}
	s.registerRoutes()
	s.httpSrv = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return s, nil
}

// resolveWatchRoot picks the directory the filesystem watcher should observe.
// The viewer cares about anything that affects the rendered board, so we watch
// the parent of the backlog file (typically .archetipo/), which also contains
// stories/ and plans/.
func resolveWatchRoot(cfg config.Config) string {
	if cfg.Paths.Backlog == "" {
		return ""
	}
	return cfg.AbsPath(filepath.Dir(cfg.Paths.Backlog))
}

// Addr returns the address the server listens on.
func (s *Server) Addr() string { return s.httpSrv.Addr }

// Run starts listening and blocks until ctx is done or the server errors.
// When ctx is cancelled the server is shut down with a 5s grace period.
func (s *Server) Run(ctx context.Context, onReady func(url string)) error {
	ln, err := net.Listen("tcp", s.httpSrv.Addr)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", s.httpSrv.Addr, err)
	}
	// Capture the resolved port (in case Addr was ":0").
	s.httpSrv.Addr = ln.Addr().String()
	if onReady != nil {
		onReady("http://" + s.httpSrv.Addr)
	}

	// Real-time refresh: start the filesystem watcher if a watch root is set.
	// A watcher failure is non-fatal — the viewer keeps working, just without
	// live updates (clients fall back to the manual refresh button).
	if s.watchRoot != "" {
		if w, werr := NewWatcher(s.watchRoot, s.broker); werr == nil {
			go func() { _ = w.Run(ctx) }()
		}
	}

	errCh := make(chan error, 1)
	go func() {
		if err := s.httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()
	select {
	case <-ctx.Done():
		s.broker.Close()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.httpSrv.Shutdown(shutdownCtx)
	case err := <-errCh:
		s.broker.Close()
		return err
	}
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("GET /api/board", s.handleGetBoard)
	s.mux.HandleFunc("GET /api/board/stream", s.handleStreamBoard)
	s.mux.HandleFunc("GET /api/spec/{code}", s.handleGetSpec)
	s.mux.HandleFunc("PUT /api/spec/{code}", s.handleUpdateSpec)
	s.mux.HandleFunc("PUT /api/spec/{code}/plan", s.handleSavePlan)
	s.mux.HandleFunc("POST /api/board/move", s.handleMoveCard)
	s.mux.HandleFunc("GET /api/prd", s.handleGetPRD)
	s.mux.HandleFunc("PUT /api/prd", s.handleSavePRD)
	s.mux.HandleFunc("GET /api/mockups", s.handleListMockups)

	// Serve design mockups from the configured paths.mockups directory.
	// The handler is registered unconditionally; a missing directory just
	// produces 404s, which the frontend already tolerates.
	if s.mockupsDir != "" {
		s.mux.Handle("/mockups/", http.StripPrefix("/mockups/", http.FileServer(http.Dir(s.mockupsDir))))
	}

	// Static assets (HTML/CSS/JS + vendor). Served from the embedded FS.
	assets, err := fs.Sub(assetsFS, "assets")
	if err == nil {
		s.mux.Handle("/", http.FileServer(http.FS(assets)))
	}
}
