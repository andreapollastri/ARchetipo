package cli

import (
	"context"
	"strings"

	"github.com/spf13/cobra"

	"github.com/techreloaded-ar/ARchetipo/cli/internal/connector"
)

// newTaskCmd builds `archetipo task done <US-XXX> <TASK-NN>`.
//
// The parent story code is the first positional, the task code is the second.
// No flags: positional-friendly form is easier for LLM agents to emit.
func newTaskCmd(s streams) *cobra.Command {
	root := &cobra.Command{Use: "task", Short: "Task operations"}
	root.AddCommand(newTaskDoneCmd(s))
	return root
}

func newTaskDoneCmd(s streams) *cobra.Command {
	return &cobra.Command{
		Use:   "done US-XXX TASK-NN",
		Short: "Mark a task as completed",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			parent := strings.TrimSpace(args[0])
			ref := strings.TrimSpace(args[1])
			if parent == "" || ref == "" {
				return errInvalidUsage("missing parent or task code", "usage: archetipo task done US-XXX TASK-NN")
			}
			return withConnector(cmd, s, "write_result", func(ctx context.Context, c connector.Connector) (any, error) {
				return c.CompleteTask(ctx, parent, ref)
			})
		},
	}
}
