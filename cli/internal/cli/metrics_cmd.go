package cli

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/techreloaded-ar/ARchetipo/cli/internal/connector"
	"github.com/techreloaded-ar/ARchetipo/cli/internal/metrics"
)

func newMetricsCmd(s streams) *cobra.Command {
	return &cobra.Command{
		Use:   "metrics",
		Short: "Report backlog progress metrics (totals, per epic, WIP, cycle and lead time)",
		Long: "Aggregates the backlog into delivery metrics: spec and point totals, completion " +
			"percentage, per-status and per-epic breakdown, specs in rework, blocked specs, and — " +
			"for specs whose status history is recorded — average cycle time (first IN PROGRESS to " +
			"DONE) and lead time (creation to DONE).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withConnector(cmd, s, "metrics", func(ctx context.Context, c connector.Connector) (any, error) {
				specs, err := c.FetchBacklogItems(ctx, "")
				if err != nil {
					return nil, err
				}
				return metrics.Compute(specs), nil
			})
		},
	}
}
