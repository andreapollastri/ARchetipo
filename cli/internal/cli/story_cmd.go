package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/techreloaded-ar/ARchetipo/cli/internal/connector"
	"github.com/techreloaded-ar/ARchetipo/cli/internal/domain"
	"github.com/techreloaded-ar/ARchetipo/cli/internal/iox"
)

// newStoryCmd builds `archetipo story ...` with five leaves:
//
//	story add    -> idempotent backlog create-or-append (stdin: {"stories":[...]})
//	story show   -> read story body + tasks (positional code or --status auto-select)
//	story plan   -> save plan + transition TODO → PLANNED (stdin: {"plan_body","tasks"})
//	story start  -> transition PLANNED → IN PROGRESS (idempotent)
//	story review -> transition IN PROGRESS → REVIEW; stdin (optional) is a closing comment
func newStoryCmd(s streams) *cobra.Command {
	root := &cobra.Command{Use: "story", Short: "Story operations"}
	root.AddCommand(
		newStoryAddCmd(s),
		newStoryShowCmd(s),
		newStoryPlanCmd(s),
		newStoryStartCmd(s),
		newStoryReviewCmd(s),
	)
	return root
}

// storiesPayload is the canonical stdin shape for `story add`.
type storiesPayload struct {
	Schema  string         `json:"schema,omitempty"`
	Kind    string         `json:"kind,omitempty"`
	Stories []domain.Story `json:"stories"`
}

func newStoryAddCmd(s streams) *cobra.Command {
	return &cobra.Command{
		Use:   "add",
		Short: "Add stories to the backlog (idempotent: skips duplicate codes)",
		Long: "Reads {\"stories\":[...]} from stdin and writes them to the backlog. " +
			"Creates the backlog when missing, appends otherwise. Stories whose code is " +
			"already present are skipped and reported in data.skipped.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			payload, err := readStoriesPayload(s.in)
			if err != nil {
				return err
			}
			return withConnector(cmd, s, "write_result", func(ctx context.Context, c connector.Connector) (any, error) {
				return idempotentBacklogWrite(ctx, c, payload.Stories)
			})
		},
	}
}

func readStoriesPayload(r io.Reader) (storiesPayload, error) {
	var p storiesPayload
	if err := iox.ReadJSON(r, &p); err != nil {
		return storiesPayload{}, err
	}
	if len(p.Stories) == 0 {
		return storiesPayload{}, iox.NewInvalidInput("no stories on stdin", "expected {\"stories\":[...]}", nil)
	}
	return p, nil
}

// idempotentBacklogWrite implements the create-or-append semantics of
// `story add`: a fresh backlog is initialized, an existing backlog is
// extended skipping codes already present.
func idempotentBacklogWrite(ctx context.Context, c connector.Connector, stories []domain.Story) (domain.WriteResult, error) {
	summary, err := c.ReadExistingBacklog(ctx)
	backlogEmpty := false
	if err != nil {
		if ce, ok := err.(*iox.CodedError); ok && ce.Code == iox.CodePreconditionMissing {
			backlogEmpty = true
		} else {
			return domain.WriteResult{}, err
		}
	} else if len(summary.Codes) == 0 {
		backlogEmpty = true
	}

	if backlogEmpty {
		return c.SaveInitialBacklog(ctx, stories)
	}

	existing := make(map[string]struct{}, len(summary.Codes))
	for _, code := range summary.Codes {
		existing[code] = struct{}{}
	}
	fresh := make([]domain.Story, 0, len(stories))
	skipped := make([]string, 0)
	for _, st := range stories {
		if _, ok := existing[st.Code]; ok {
			skipped = append(skipped, st.Code)
			continue
		}
		fresh = append(fresh, st)
	}
	if len(fresh) == 0 {
		return domain.WriteResult{OK: true, Skipped: skipped}, nil
	}
	res, err := c.AppendStories(ctx, fresh)
	if err != nil {
		return domain.WriteResult{}, err
	}
	if len(skipped) > 0 {
		res.Skipped = skipped
	}
	return res, nil
}

func newStoryShowCmd(s streams) *cobra.Command {
	var status string
	cmd := &cobra.Command{
		Use:   "show [US-XXX]",
		Short: "Read a story's body and tasks. Pass a code or use --status to auto-select.",
		Long: "Two mutually exclusive forms:\n" +
			"  archetipo story show US-005           (lookup by code)\n" +
			"  archetipo story show --status TODO    (auto-pick first eligible by priority+code)",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := ""
			if len(args) == 1 {
				ref = strings.TrimSpace(args[0])
			}
			switch {
			case ref != "" && status != "":
				return errInvalidUsage("story show accepts a code OR --status, not both", "drop one of the two")
			case ref == "" && status == "":
				return errInvalidUsage("story show requires a code or --status", "pass `US-XXX` or `--status TODO`")
			}
			return withConnector(cmd, s, "story", func(ctx context.Context, c connector.Connector) (any, error) {
				var st domain.Story
				var err error
				if ref != "" {
					st, err = c.ReadStoryDetail(ctx, ref)
				} else {
					st, err = c.SelectStory(ctx, domain.SelectQuery{EligibleStatuses: []domain.Status{domain.Status(status)}})
				}
				if err != nil {
					return nil, err
				}
				tasks, err := c.ReadStoryTasks(ctx, st.Code)
				if err != nil {
					if ce, ok := err.(*iox.CodedError); ok && ce.Code == iox.CodePreconditionMissing {
						// No plan yet: empty task list, not an error.
						tasks = []domain.Task{}
					} else {
						return nil, err
					}
				}
				return map[string]any{"story": st, "tasks": tasks}, nil
			})
		},
	}
	cmd.Flags().StringVar(&status, "status", "", "auto-select the first eligible story with this workflow status")
	return cmd
}

func newStoryPlanCmd(s streams) *cobra.Command {
	return &cobra.Command{
		Use:   "plan US-XXX",
		Short: "Save the implementation plan for a story and transition to PLANNED",
		Long: "Reads {\"plan_body\":\"...\",\"tasks\":[...]} from stdin. " +
			"Idempotent: re-running on a PLANNED story upserts the plan body without erroring. " +
			"Errors with E_CONFLICT when the story is past PLANNED.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := strings.TrimSpace(args[0])
			if ref == "" {
				return errInvalidUsage("missing story code", "pass US-XXX as positional argument")
			}
			var input domain.PlanInput
			if err := iox.ReadJSON(s.in, &input); err != nil {
				return err
			}
			return withConnector(cmd, s, "write_result", func(ctx context.Context, c connector.Connector) (any, error) {
				story, err := c.ReadStoryDetail(ctx, ref)
				if err != nil {
					return nil, err
				}
				switch story.Status {
				case domain.StatusTodo:
					res, err := c.SavePlan(ctx, ref, input)
					if err != nil {
						return nil, err
					}
					if _, err := c.TransitionStatus(ctx, ref, domain.StatusPlanned); err != nil {
						return nil, err
					}
					return res, nil
				case domain.StatusPlanned:
					return c.SavePlan(ctx, ref, input)
				default:
					return nil, iox.NewConflict(
						fmt.Sprintf("cannot plan story %s: status is %s, expected TODO or PLANNED", ref, story.Status),
						"inspect the current status with `archetipo story show "+ref+"`", nil)
				}
			})
		},
	}
}

func newStoryStartCmd(s streams) *cobra.Command {
	return &cobra.Command{
		Use:   "start US-XXX",
		Short: "Transition a planned story to IN PROGRESS (idempotent)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := strings.TrimSpace(args[0])
			if ref == "" {
				return errInvalidUsage("missing story code", "pass US-XXX as positional argument")
			}
			return withConnector(cmd, s, "write_result", func(ctx context.Context, c connector.Connector) (any, error) {
				return transitionWithValidation(ctx, c, ref, "start", domain.StatusPlanned, domain.StatusInProgress)
			})
		},
	}
}

func newStoryReviewCmd(s streams) *cobra.Command {
	return &cobra.Command{
		Use:   "review US-XXX",
		Short: "Transition a story to REVIEW; stdin (optional) is posted as a closing comment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := strings.TrimSpace(args[0])
			if ref == "" {
				return errInvalidUsage("missing story code", "pass US-XXX as positional argument")
			}
			comment, err := io.ReadAll(s.in)
			if err != nil {
				return iox.NewInvalidInput("reading stdin", "", err)
			}
			return withConnector(cmd, s, "write_result", func(ctx context.Context, c connector.Connector) (any, error) {
				res, err := transitionWithValidation(ctx, c, ref, "review", domain.StatusInProgress, domain.StatusReview)
				if err != nil {
					return nil, err
				}
				if len(strings.TrimSpace(string(comment))) > 0 {
					if _, err := c.PostComment(ctx, ref, string(comment)); err != nil {
						return nil, err
					}
				}
				return res, nil
			})
		},
	}
}

// transitionWithValidation enforces the idempotent + validated transition rules
// shared by `story start` and `story review`. Calling the verb when the story
// is already at the target state is a no-op success; calling it from any
// status other than the expected source returns E_CONFLICT.
func transitionWithValidation(ctx context.Context, c connector.Connector, ref, verb string, source, target domain.Status) (domain.WriteResult, error) {
	story, err := c.ReadStoryDetail(ctx, ref)
	if err != nil {
		return domain.WriteResult{}, err
	}
	if story.Status == target {
		return domain.WriteResult{OK: true, Refs: []domain.Ref{{Code: story.Code}}}, nil
	}
	if story.Status != source {
		return domain.WriteResult{}, iox.NewConflict(
			fmt.Sprintf("cannot %s story %s: status is %s, expected %s", verb, ref, story.Status, source),
			fmt.Sprintf("transition the story to %s before running `archetipo story %s`", source, verb),
			nil)
	}
	return c.TransitionStatus(ctx, ref, target)
}
