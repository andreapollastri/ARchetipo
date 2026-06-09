package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/techreloaded-ar/ARchetipo/cli/internal/config"
	"github.com/techreloaded-ar/ARchetipo/cli/internal/connector"
	"github.com/techreloaded-ar/ARchetipo/cli/internal/domain"
	"github.com/techreloaded-ar/ARchetipo/cli/internal/gitwt"
	"github.com/techreloaded-ar/ARchetipo/cli/internal/iox"
)

// newSpecCmd builds `archetipo spec ...` with eight leaves:
//
//	spec add    -> idempotent backlog create-or-append (stdin: {"specs":[...]})
//	spec show   -> read spec body + tasks by code
//	spec next   -> auto-pick first eligible spec by --status (priority+code)
//	spec list   -> aggregated read: items (optionally filtered) + summary metadata
//	spec plan   -> save plan + transition TODO → PLANNED (stdin: {"plan_body","tasks"})
//	spec start  -> transition PLANNED → IN PROGRESS (idempotent)
//	spec review -> transition IN PROGRESS → REVIEW; --file (optional) is a closing comment
//	spec move   -> reposition a spec within the board or across workflow columns
func newSpecCmd(s streams) *cobra.Command {
	root := &cobra.Command{Use: "spec", Short: "Spec operations (user story is the spec body)"}
	root.AddCommand(
		newSpecAddCmd(s),
		newSpecShowCmd(s),
		newSpecNextCmd(s),
		newSpecListCmd(s),
		newSpecPlanCmd(s),
		newSpecStartCmd(s),
		newSpecReviewCmd(s),
		newSpecIntegrateCmd(s),
		newSpecMoveCmd(s),
	)
	return root
}

// specsPayload is the canonical stdin shape for `spec add`.
type specsPayload struct {
	Schema string        `json:"schema,omitempty"`
	Kind   string        `json:"kind,omitempty"`
	Specs  []domain.Spec `json:"specs"`
}

func newSpecAddCmd(s streams) *cobra.Command {
	var filePath string
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add specs to the backlog (idempotent: skips duplicate codes)",
		Long: "Reads a YAML or JSON payload from --file and writes the specs to the backlog. " +
			"Creates the backlog when missing, appends otherwise. Specs whose code is " +
			"already present are skipped and reported in data.skipped.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if filePath == "" {
				return errInvalidUsage("missing input file", "pass --file path/to/specs.yaml or --file -")
			}
			payload, err := readSpecsPayload(s.in, filePath)
			if err != nil {
				return err
			}
			return withConnector(cmd, s, "write_result", func(ctx context.Context, c connector.Connector) (any, error) {
				return idempotentBacklogWrite(ctx, c, payload.Specs)
			})
		},
	}
	cmd.Flags().StringVar(&filePath, "file", "", "path to a YAML or JSON payload file, or - for stdin")
	return cmd
}

func readSpecsPayload(stdin io.Reader, path string) (specsPayload, error) {
	var p specsPayload
	if err := readStructuredInput(stdin, path, &p); err != nil {
		return specsPayload{}, err
	}
	if len(p.Specs) == 0 {
		return specsPayload{}, iox.NewInvalidInput("no specs in input payload", "expected {specs:[...]}", nil)
	}
	return p, nil
}

// idempotentBacklogWrite implements the create-or-append semantics of
// `spec add`: a fresh backlog is initialized, an existing backlog is
// extended skipping codes already present.
func idempotentBacklogWrite(ctx context.Context, c connector.Connector, specs []domain.Spec) (domain.WriteResult, error) {
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
		return c.SaveInitialBacklog(ctx, specs)
	}

	existing := make(map[string]struct{}, len(summary.Codes))
	for _, code := range summary.Codes {
		existing[code] = struct{}{}
	}
	fresh := make([]domain.Spec, 0, len(specs))
	skipped := make([]string, 0)
	for _, st := range specs {
		if _, ok := existing[st.Code]; ok {
			skipped = append(skipped, st.Code)
			continue
		}
		fresh = append(fresh, st)
	}
	if len(fresh) == 0 {
		return domain.WriteResult{OK: true, Skipped: skipped}, nil
	}
	res, err := c.AppendSpecs(ctx, fresh)
	if err != nil {
		return domain.WriteResult{}, err
	}
	if len(skipped) > 0 {
		res.Skipped = skipped
	}
	return res, nil
}

func newSpecShowCmd(s streams) *cobra.Command {
	return &cobra.Command{
		Use:   "show US-XXX",
		Short: "Read a spec's body and tasks by code",
		Long:  "Looks up the spec by code and returns its body and current task list.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return errInvalidUsage("missing spec code", "pass US-XXX as positional argument")
			}
			ref := strings.TrimSpace(args[0])
			if ref == "" {
				return errInvalidUsage("missing spec code", "pass US-XXX as positional argument")
			}
			return withConnectorCfg(cmd, s, "spec", func(ctx context.Context, cfg config.Config, c connector.Connector) (any, error) {
				st, err := c.ReadSpecDetail(ctx, ref)
				if err != nil {
					return nil, err
				}
				return loadSpecWithTasks(ctx, cfg, c, st)
			})
		},
	}
}

func newSpecNextCmd(s streams) *cobra.Command {
	var status string
	cmd := &cobra.Command{
		Use:   "next",
		Short: "Auto-pick the first eligible spec by --status (priority+code)",
		Long: "Selects the first eligible spec whose workflow status matches --status, " +
			"ordered by priority and code, and returns its body and tasks.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(status) == "" {
				return errInvalidUsage("missing --status", "pass --status TODO|PLANNED|IN PROGRESS|REVIEW|DONE")
			}
			return withConnectorCfg(cmd, s, "spec", func(ctx context.Context, cfg config.Config, c connector.Connector) (any, error) {
				st, err := c.SelectSpec(ctx, domain.SelectQuery{EligibleStatuses: []domain.Status{domain.Status(status)}})
				if err != nil {
					return nil, err
				}
				return loadSpecWithTasks(ctx, cfg, c, st)
			})
		},
	}
	cmd.Flags().StringVar(&status, "status", "", "workflow status to auto-pick from")
	return cmd
}

// loadSpecWithTasks builds the `spec` envelope payload shared by spec show
// and spec next. A spec without a plan reports an empty task list rather than
// an error.
//
// The payload also carries `workdir`: the ABSOLUTE directory a skill must use
// as the single root for all of the spec's file and git work — the spec's git
// worktree when one exists, the project root otherwise. It is always populated
// so skills never branch on worktree presence. See resolveWorkdir for how it is
// derived (filesystem state first, persisted field only as a fallback).
func loadSpecWithTasks(ctx context.Context, cfg config.Config, c connector.Connector, st domain.Spec) (map[string]any, error) {
	tasks, err := c.ReadSpecTasks(ctx, st.Code)
	if err != nil {
		if ce, ok := err.(*iox.CodedError); ok && ce.Code == iox.CodePreconditionMissing {
			tasks = []domain.Task{}
		} else {
			return nil, err
		}
	}
	return map[string]any{"spec": st, "tasks": tasks, "workdir": resolveWorkdir(cfg, st)}, nil
}

// resolveWorkdir returns the absolute working directory for a spec. The worktree
// workflow can leave the persisted spec.Worktree field out of sync with the real
// git worktree (e.g. the link is dropped while the worktree still exists on
// disk), so the actual filesystem state is the authoritative source: when the
// worktree workflow is enabled and the conventional worktree directory exists,
// that directory wins. Only when it is absent do we honor a recorded
// spec.Worktree (e.g. a connector that tracks it out of band), falling back to
// the project root. The result is always absolute and never empty.
func resolveWorkdir(cfg config.Config, st domain.Spec) string {
	if cfg.Worktree.Enabled {
		if rel, exists := gitwt.Resolve(cfg.ProjectRoot, cfg.Worktree, st.Code); exists {
			return filepath.Join(cfg.ProjectRoot, rel)
		}
	}
	if st.Worktree != "" {
		return filepath.Join(cfg.ProjectRoot, st.Worktree)
	}
	return cfg.ProjectRoot
}

func newSpecListCmd(s streams) *cobra.Command {
	var status string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List backlog specs (optionally filtered by status) with summary metadata",
		Long: "Returns {items, summary} in a single envelope. items is filtered by --status when provided; " +
			"summary is always the full backlog metadata (codes, last_code, epics, titles).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withConnector(cmd, s, "backlog", func(ctx context.Context, c connector.Connector) (any, error) {
				items, err := c.FetchBacklogItems(ctx, domain.Status(status))
				if err != nil {
					return nil, err
				}
				summary, err := c.ReadExistingBacklog(ctx)
				if err != nil {
					if ce, ok := err.(*iox.CodedError); ok && ce.Code == iox.CodePreconditionMissing {
						summary = domain.BacklogSummary{}
					} else {
						return nil, err
					}
				}
				return map[string]any{
					"items":   items,
					"summary": summary,
				}, nil
			})
		},
	}
	cmd.Flags().StringVar(&status, "status", "", "filter items by workflow status (e.g. TODO)")
	return cmd
}

func newSpecPlanCmd(s streams) *cobra.Command {
	var filePath string
	cmd := &cobra.Command{
		Use:   "plan US-XXX",
		Short: "Save the implementation plan for a spec and transition to PLANNED",
		Long: "Reads a YAML or JSON payload from --file. " +
			"Idempotent: re-running on a PLANNED spec upserts the plan body without erroring. " +
			"Errors with E_CONFLICT when the spec is past PLANNED.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := strings.TrimSpace(args[0])
			if ref == "" {
				return errInvalidUsage("missing spec code", "pass US-XXX as positional argument")
			}
			if filePath == "" {
				return errInvalidUsage("missing input file", "pass --file path/to/plan.yaml or --file -")
			}
			var input domain.PlanInput
			if err := readStructuredInput(s.in, filePath, &input); err != nil {
				return err
			}
			return withConnector(cmd, s, "write_result", func(ctx context.Context, c connector.Connector) (any, error) {
				spec, err := c.ReadSpecDetail(ctx, ref)
				if err != nil {
					return nil, err
				}
				switch spec.Status {
				case domain.StatusTodo:
					res, err := c.SavePlan(ctx, ref, input)
					if err != nil {
						return nil, err
					}
					if _, err := c.TransitionStatus(ctx, ref, domain.StatusPlanned); err != nil {
						return nil, err
					}
					// Re-planning clears the rework marker: the review feedback has
					// now been turned into tasks. No-op when the spec was not in rework.
					rework := false
					if _, err := c.UpdateSpec(ctx, ref, domain.SpecUpdate{Rework: &rework}); err != nil {
						return nil, err
					}
					return res, nil
				case domain.StatusPlanned:
					return c.SavePlan(ctx, ref, input)
				default:
					return nil, iox.NewConflict(
						fmt.Sprintf("cannot plan spec %s: status is %s, expected TODO or PLANNED", ref, spec.Status),
						"inspect the current status with `archetipo spec show "+ref+"`", nil)
				}
			})
		},
	}
	cmd.Flags().StringVar(&filePath, "file", "", "path to a YAML or JSON payload file, or - for stdin")
	return cmd
}

func newSpecStartCmd(s streams) *cobra.Command {
	return &cobra.Command{
		Use:   "start US-XXX",
		Short: "Transition a planned spec to IN PROGRESS (idempotent)",
		Long: "Transitions the spec from PLANNED to IN PROGRESS. When the worktree " +
			"workflow is enabled (worktree.enabled in config.yaml), it also creates a " +
			"dedicated git branch + worktree forked from the right base (dependency-aware) " +
			"and records branch/worktree/fork_base on the spec. Worktree setup is " +
			"non-fatal: outside a git repository it is skipped with a warning.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := strings.TrimSpace(args[0])
			if ref == "" {
				return errInvalidUsage("missing spec code", "pass US-XXX as positional argument")
			}
			return withConnectorCfg(cmd, s, "write_result", func(ctx context.Context, cfg config.Config, c connector.Connector) (any, error) {
				res, err := transitionWithValidation(ctx, c, ref, "start", domain.StatusPlanned, domain.StatusInProgress)
				if err != nil {
					return nil, err
				}
				if cfg.Worktree.Enabled {
					if wt, werr := setupWorktree(ctx, cfg, c, ref); werr != nil {
						fmt.Fprintf(s.err, "warning: worktree setup skipped: %v\n", werr)
					} else {
						res.Refs = append(res.Refs, domain.Ref{Code: ref, Path: wt})
					}
				}
				return res, nil
			})
		},
	}
}

// setupWorktree creates (idempotently) the branch + worktree for a spec and
// persists branch/worktree/fork_base on it. Returns the worktree path. Any git
// failure is returned to the caller, which treats worktree setup as non-fatal.
func setupWorktree(ctx context.Context, cfg config.Config, c connector.Connector, ref string) (string, error) {
	if err := gitwt.EnsureRepo(ctx, cfg.ProjectRoot, cfg.Worktree.Base); err != nil {
		return "", err
	}
	spec, err := c.ReadSpecDetail(ctx, ref)
	if err != nil {
		return "", err
	}
	if spec.Branch != "" {
		// Already set up on a previous start.
		return spec.Worktree, nil
	}
	allSpecs, err := c.FetchBacklogItems(ctx, "")
	if err != nil {
		return "", err
	}
	forkRef, err := gitwt.ForkRef(ctx, cfg.ProjectRoot, cfg.Worktree, spec, allSpecs)
	if err != nil {
		return "", err
	}
	branch, worktree, forkBase, err := gitwt.Ensure(ctx, cfg.ProjectRoot, cfg.Worktree, ref, forkRef)
	if err != nil {
		return "", err
	}
	if _, err := c.UpdateSpec(ctx, ref, domain.SpecUpdate{
		Branch:   &branch,
		Worktree: &worktree,
		ForkBase: &forkBase,
	}); err != nil {
		return "", err
	}
	return worktree, nil
}

func newSpecIntegrateCmd(s streams) *cobra.Command {
	return &cobra.Command{
		Use:   "integrate US-XXX",
		Short: "Merge a spec's worktree branch into base, clean up, and mark it DONE",
		Long: "Integrates a reviewed spec: merges its branch into the base branch " +
			"(--no-ff), removes the worktree, deletes the branch and transitions the " +
			"spec to DONE. Requires the worktree workflow and that all blockers are " +
			"already integrated. On merge conflict the merge is aborted and the " +
			"conflicting files are reported (E_CONFLICT); resolve manually and retry.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := strings.TrimSpace(args[0])
			if ref == "" {
				return errInvalidUsage("missing spec code", "pass US-XXX as positional argument")
			}
			return withConnectorCfg(cmd, s, "write_result", func(ctx context.Context, cfg config.Config, c connector.Connector) (any, error) {
				if !cfg.Worktree.Enabled {
					return nil, iox.NewConflict("worktree workflow is disabled", "enable worktree.enabled in config.yaml to use integrate", nil)
				}
				if err := gitwt.EnsureRepo(ctx, cfg.ProjectRoot, cfg.Worktree.Base); err != nil {
					return nil, err
				}
				spec, err := c.ReadSpecDetail(ctx, ref)
				if err != nil {
					return nil, err
				}
				if spec.Branch == "" {
					return nil, iox.NewPrecondition(fmt.Sprintf("spec %s has no worktree branch", ref), "run `archetipo spec start` with worktree enabled first", nil)
				}
				allSpecs, err := c.FetchBacklogItems(ctx, "")
				if err != nil {
					return nil, err
				}
				if blockers := gitwt.UnintegratedBlockers(ctx, cfg.ProjectRoot, cfg.Worktree, spec, allSpecs); len(blockers) > 0 {
					return nil, iox.NewConflict(
						fmt.Sprintf("unintegrated blockers: %s", strings.Join(blockers, ", ")),
						"integrate the blockers before this spec", nil)
				}
				if err := gitwt.Integrate(ctx, cfg.ProjectRoot, cfg.Worktree, spec.Branch, spec.Worktree); err != nil {
					return nil, err
				}
				return c.TransitionStatus(ctx, ref, domain.StatusDone)
			})
		},
	}
}

func newSpecReviewCmd(s streams) *cobra.Command {
	var filePath string
	cmd := &cobra.Command{
		Use:   "review US-XXX",
		Short: "Transition a spec to REVIEW; --file (or stdin) is posted as a closing comment",
		Long: "Transitions the spec from IN PROGRESS to REVIEW and, when a non-empty body is provided " +
			"via --file or stdin, posts it as a closing comment on the parent issue. Connectors " +
			"without comment support silently ignore the body.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := strings.TrimSpace(args[0])
			if ref == "" {
				return errInvalidUsage("missing spec code", "pass US-XXX as positional argument")
			}
			comment, err := readRawInput(s.in, filePath)
			if err != nil {
				return err
			}
			return withConnectorCfg(cmd, s, "write_result", func(ctx context.Context, cfg config.Config, c connector.Connector) (any, error) {
				spec, err := c.ReadSpecDetail(ctx, ref)
				if err != nil {
					return nil, err
				}
				if spec.Status != domain.StatusReview && cfg.Worktree.Enabled && spec.Branch != "" && spec.Worktree != "" {
					if err := gitwt.CommitWorktreeChanges(ctx, cfg.ProjectRoot, spec.Worktree, spec.Code); err != nil {
						return nil, err
					}
				}
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
	cmd.Flags().StringVar(&filePath, "file", "", "path to the closing comment, or - for stdin (default: stdin)")
	return cmd
}

// validMoveTargets lists the board columns accepted by `spec move --to`.
// The list mirrors the mapping in the connector implementations.
var validMoveTargets = []string{"todo", "planned", "in_progress", "review", "done"}

func newSpecMoveCmd(s streams) *cobra.Command {
	var before string
	var after string
	var target string
	cmd := &cobra.Command{
		Use:   "move US-XXX",
		Short: "Move a spec within the board or across workflow columns",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if target == "" {
				return errInvalidUsage("missing target column", "pass --to "+strings.Join(validMoveTargets, "|"))
			}
			if !isValidMoveTarget(target) {
				return errInvalidUsage(
					fmt.Sprintf("invalid --to value %q", target),
					"valid columns: "+strings.Join(validMoveTargets, "|"),
				)
			}
			if before != "" && after != "" {
				return errInvalidUsage("before and after are mutually exclusive", "pass only one anchor")
			}
			ref := args[0]
			return withConnector(cmd, s, "write_result", func(ctx context.Context, c connector.Connector) (any, error) {
				return c.MoveBoardCard(ctx, ref, target, domain.ReorderAnchor{Before: before, After: after})
			})
		},
	}
	cmd.Flags().StringVar(&target, "to", "", "target board column: "+strings.Join(validMoveTargets, "|"))
	cmd.Flags().StringVar(&before, "before", "", "insert before the given spec code in the target column")
	cmd.Flags().StringVar(&after, "after", "", "insert after the given spec code in the target column")
	return cmd
}

func isValidMoveTarget(t string) bool {
	for _, v := range validMoveTargets {
		if v == t {
			return true
		}
	}
	return false
}

// transitionWithValidation enforces the idempotent + validated transition rules
// shared by `spec start` and `spec review`. Calling the verb when the spec
// is already at the target state is a no-op success; calling it from any
// status other than the expected source returns E_CONFLICT.
func transitionWithValidation(ctx context.Context, c connector.Connector, ref, verb string, source, target domain.Status) (domain.WriteResult, error) {
	spec, err := c.ReadSpecDetail(ctx, ref)
	if err != nil {
		return domain.WriteResult{}, err
	}
	if spec.Status == target {
		return domain.WriteResult{OK: true, Refs: []domain.Ref{{Code: spec.Code}}}, nil
	}
	if spec.Status != source {
		return domain.WriteResult{}, iox.NewConflict(
			fmt.Sprintf("cannot %s spec %s: status is %s, expected %s", verb, ref, spec.Status, source),
			fmt.Sprintf("transition the spec to %s before running `archetipo spec %s`", source, verb),
			nil)
	}
	return c.TransitionStatus(ctx, ref, target)
}

func readStructuredInput(stdin io.Reader, path string, v any) error {
	raw, err := readRawInput(stdin, path)
	if err != nil {
		return err
	}
	if err := yaml.Unmarshal(raw, v); err != nil {
		return iox.NewInvalidInput("invalid structured input", "expected YAML or JSON payload", err)
	}
	return nil
}

// readRawInput reads raw bytes from a file path or stdin. When path is empty
// or "-", input is taken from stdin. Returns iox-typed errors on read failure.
func readRawInput(stdin io.Reader, path string) ([]byte, error) {
	if path == "" || path == "-" {
		raw, err := io.ReadAll(stdin)
		if err != nil {
			return nil, iox.NewInvalidInput("reading stdin", "", err)
		}
		return raw, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, iox.NewInvalidInput(fmt.Sprintf("reading input file %s", path), "", err)
	}
	return raw, nil
}
