package cli

import (
	"context"
	"io"

	"github.com/spf13/cobra"

	"github.com/techreloaded-ar/ARchetipo/cli/internal/connector"
	"github.com/techreloaded-ar/ARchetipo/cli/internal/iox"
)

// newPRDCmd builds `archetipo prd write` -> save_prd. Stdin is the raw markdown.
func newPRDCmd(s streams) *cobra.Command {
	root := &cobra.Command{Use: "prd", Short: "PRD operations"}
	root.AddCommand(newPRDWriteCmd(s))
	return root
}

func newPRDWriteCmd(s streams) *cobra.Command {
	return &cobra.Command{
		Use:   "write",
		Short: "Persist the PRD markdown read from stdin",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			body, err := io.ReadAll(s.in)
			if err != nil {
				return iox.NewInvalidInput("reading stdin", "", err)
			}
			return withConnector(cmd, s, "write_result", func(ctx context.Context, c connector.Connector) (any, error) {
				return c.SavePRD(ctx, string(body))
			})
		},
	}
}
