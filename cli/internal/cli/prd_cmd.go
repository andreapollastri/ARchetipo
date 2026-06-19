package cli

import (
	"context"
	"strings"

	"github.com/spf13/cobra"

	"github.com/techreloaded-ar/ARchetipo/cli/internal/connector"
	"github.com/techreloaded-ar/ARchetipo/cli/internal/iox"
)

// newPRDCmd builds `archetipo prd write` -> save_prd. The markdown body is
// read from --file (or stdin when the flag is omitted or set to "-").
func newPRDCmd(s streams) *cobra.Command {
	root := &cobra.Command{Use: "prd", Short: "PRD operations"}
	root.AddCommand(newPRDWriteCmd(s))
	return root
}

func newPRDWriteCmd(s streams) *cobra.Command {
	var filePath string
	cmd := &cobra.Command{
		Use:   "write",
		Short: "Persist the PRD markdown read from --file or stdin",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			body, err := readRawInput(s.in, filePath)
			if err != nil {
				return err
			}
			if strings.TrimSpace(string(body)) == "" {
				return iox.NewInvalidInput("prd write requires non-empty markdown input", "provide PRD content via --file or stdin", nil)
			}
			return withConnector(cmd, s, "write_result", func(ctx context.Context, c connector.Connector) (any, error) {
				return c.SavePRD(ctx, string(body))
			})
		},
	}
	cmd.Flags().StringVar(&filePath, "file", "", "path to the PRD markdown, or - for stdin (default: stdin)")
	return cmd
}
