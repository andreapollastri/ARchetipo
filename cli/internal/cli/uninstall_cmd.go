package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/techreloaded-ar/ARchetipo/cli/internal/iox"
)

func newUninstallCmd(s streams) *cobra.Command {
	var toolFlags []string

	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove ARchetipo skills from the selected tools",
		Long:  "Removes the archetipo-* skill directories from the chosen tools in the current project.",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runUninstall(s, toolFlags)
		},
	}
	cmd.Flags().StringSliceVar(&toolFlags, "tool", nil, "Tool key(s) to clean up: claude, codex, gemini, opencode, copilot, pi. Repeat or comma-separate.")
	return cmd
}

func runUninstall(s streams, toolFlags []string) error {
	tools, err := resolveToolFlags(toolFlags)
	if err != nil {
		return err
	}
	if len(tools) == 0 {
		tools, err = pickToolsInteractive(s)
		if err != nil {
			return err
		}
	}
	if len(tools) == 0 {
		fmt.Fprintln(s.out, "No tools selected.")
		return nil
	}

	fmt.Fprintln(s.out, "Removing...")
	for _, t := range tools {
		target := t.ProjectPath
		removed := 0
		for _, sk := range allSkills {
			dst := filepath.Join(target, sk)
			if _, err := os.Stat(dst); err != nil {
				continue
			}
			if err := os.RemoveAll(dst); err != nil {
				return iox.NewInternal("cannot remove "+dst, err)
			}
			removed++
		}
		if removed == 0 {
			fmt.Fprintf(s.out, "  – %s: nothing to remove (%s)\n", t.Name, target)
		} else {
			fmt.Fprintf(s.out, "  ✓ %s: removed %d skill(s) from %s\n", t.Name, removed, target)
		}
	}
	fmt.Fprintln(s.out, "Done.")
	return nil
}
