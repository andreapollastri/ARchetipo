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
	"time"

	"github.com/techreloaded-ar/ARchetipo/cli/internal/connector"
)

// Server wires the connector backend to HTTP handlers and the embedded UI.
type Server struct {
	conn    connector.Connector
	mux     *http.ServeMux
	httpSrv *http.Server
}

// NewServer constructs a Server bound to addr (e.g. "127.0.0.1:8080").
// The returned server has all routes registered but is not listening yet:
// call Run to start serving.
func NewServer(conn connector.Connector, addr string) (*Server, error) {
	mux := http.NewServeMux()
	s := &Server{conn: conn, mux: mux}
	s.registerRoutes()
	s.httpSrv = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return s, nil
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
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.httpSrv.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("GET /api/board", s.handleGetBoard)
	s.mux.HandleFunc("GET /api/story/{code}", s.handleGetStory)
	s.mux.HandleFunc("PUT /api/story/{code}", s.handleUpdateStory)
	s.mux.HandleFunc("PUT /api/story/{code}/plan", s.handleSavePlan)
	s.mux.HandleFunc("POST /api/board/move", s.handleMoveCard)
	s.mux.HandleFunc("POST /api/backlog/reorder", s.handleReorderBacklog)

	// Static assets (HTML/CSS/JS + vendor). Served from the embedded FS.
	assets, err := fs.Sub(assetsFS, "assets")
	if err == nil {
		s.mux.Handle("/", http.FileServer(http.FS(assets)))
	}
}
