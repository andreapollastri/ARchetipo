package cli

import (
	"context"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/techreloaded-ar/ARchetipo/cli/internal/config"
	"github.com/techreloaded-ar/ARchetipo/cli/internal/connector"
	"github.com/techreloaded-ar/ARchetipo/cli/internal/iox"
)

// streams bundles the I/O streams a sub-command operates on. cli.Execute
// passes the same streams down to every sub-command so tests can swap them.
type streams struct {
	in  io.Reader
	out io.Writer
	err io.Writer
}

// withConnector wires the standard plumbing (config load, connector build,
// JSON envelope on success or error) around a per-operation callback.
//
// The callback returns the kind tag and the data payload for the success
// envelope. On error, the failure is encoded to the error envelope on stderr
// and translated to an exit code by cli.Execute.
func withConnector(cmd *cobra.Command, s streams, kind string, fn func(ctx context.Context, c connector.Connector) (any, error)) error {
	cwd, err := os.Getwd()
	if err != nil {
		return iox.NewInternal("cwd unavailable", err)
	}
	cfg, err := config.Load(cwd)
	if err != nil {
		return iox.NewInvalidInput(err.Error(), "fix the file or remove it to fall back to defaults", err)
	}
	conn, err := connector.New(cfg)
	if err != nil {
		return iox.NewInvalidInput("connector unavailable", "check `connector:` in .archetipo/config.yaml", err)
	}
	data, err := fn(cmd.Context(), conn)
	if err != nil {
		return err
	}
	if err := iox.WriteOK(s.out, kind, data); err != nil {
		return iox.NewInternal("encoding output", err)
	}
	return nil
}

// withConnectorCfg is like withConnector but also passes the loaded config to
// the callback. Used by operations that need config beyond the connector (e.g.
// the worktree workflow needs ProjectRoot and the worktree settings).
func withConnectorCfg(cmd *cobra.Command, s streams, kind string, fn func(ctx context.Context, cfg config.Config, c connector.Connector) (any, error)) error {
	cwd, err := os.Getwd()
	if err != nil {
		return iox.NewInternal("cwd unavailable", err)
	}
	cfg, err := config.Load(cwd)
	if err != nil {
		return iox.NewInvalidInput(err.Error(), "fix the file or remove it to fall back to defaults", err)
	}
	conn, err := connector.New(cfg)
	if err != nil {
		return iox.NewInvalidInput("connector unavailable", "check `connector:` in .archetipo/config.yaml", err)
	}
	data, err := fn(cmd.Context(), cfg, conn)
	if err != nil {
		return err
	}
	if err := iox.WriteOK(s.out, kind, data); err != nil {
		return iox.NewInternal("encoding output", err)
	}
	return nil
}
