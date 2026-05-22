package cli

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/techreloaded-ar/ARchetipo/cli/internal/config"
	"github.com/techreloaded-ar/ARchetipo/cli/internal/connector"
	"github.com/techreloaded-ar/ARchetipo/cli/internal/iox"
	"github.com/techreloaded-ar/ARchetipo/cli/internal/web"
)

func newViewCmd(s streams) *cobra.Command {
	var port int
	var host string
	var noOpen bool
	cmd := &cobra.Command{
		Use:   "view",
		Short: "Open the local Kanban view for the backlog",
		Long: `Start a local HTTP server that serves a Kanban board for the project
backlog and, by default, opens it in the system browser.

The view reads and writes the same files used by the file connector
(.archetipo/backlog.yaml, .archetipo/specs/, .archetipo/plans/), so any
edits made in the browser persist immediately. The server binds to the
loopback interface only; no authentication is performed.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return iox.NewInternal("cwd unavailable", err)
			}
			cfg, err := config.Load(cwd)
			if err != nil {
				return iox.NewInvalidInput("invalid .archetipo/config.yaml", "fix the file or remove it to fall back to defaults", err)
			}
			conn, err := connector.New(cfg)
			if err != nil {
				return iox.NewInvalidInput("connector unavailable", "check `connector:` in .archetipo/config.yaml", err)
			}
			addr := net.JoinHostPort(host, strconv.Itoa(port))
			srv, err := web.NewServer(conn, cfg, addr)
			if err != nil {
				return iox.NewInternal("creating server", err)
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			onReady := func(url string) {
				fmt.Fprintf(s.err, "ARchetipo view ready at %s\n", url)
				fmt.Fprintln(s.err, "Press Ctrl+C to stop.")
				if !noOpen {
					if err := web.OpenBrowser(url); err != nil {
						fmt.Fprintf(s.err, "(could not open browser: %v)\n", err)
					}
				}
			}
			if err := srv.Run(ctx, onReady); err != nil {
				return iox.NewInternal("view server stopped with error", err)
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&port, "port", 8080, "TCP port to listen on")
	cmd.Flags().StringVar(&host, "host", "127.0.0.1", "host address to bind to (loopback only by default)")
	cmd.Flags().BoolVar(&noOpen, "no-open", false, "do not open the browser automatically")
	return cmd
}
