package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/techreloaded-ar/ARchetipo/cli/internal/version"
)

func newVersionCmd(s streams) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the archetipo CLI version",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			fmt.Fprint(s.out, versionLine())
			return nil
		},
	}
}

func versionLine() string {
	return fmt.Sprintf("archetipo %s\n", version.Version)
}
