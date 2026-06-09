// Package gitwt implements the per-spec git worktree workflow used by the
// review feature: each spec is developed on a dedicated branch + worktree so
// its review diff can be isolated (`git diff <fork_base>...<branch>`) and
// integrated back into the base branch with a single merge.
//
// All operations shell out to the `git` binary with the project root as the
// working directory, mirroring how the github connector shells out to `gh`.
package gitwt

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/techreloaded-ar/ARchetipo/cli/internal/domain"
	"github.com/techreloaded-ar/ARchetipo/cli/internal/iox"
)

// runGit executes `git <args...>` in repoRoot and returns trimmed stdout. On
// failure the error message includes stderr so callers can surface it.
func runGit(ctx context.Context, repoRoot string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repoRoot
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

func runGitInDir(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// gitOK runs git and reports whether it exited zero, discarding output. Used
// for boolean probes (ref existence, ancestry) where a non-zero exit is a
// normal "false" answer rather than an error.
func gitOK(ctx context.Context, repoRoot string, args ...string) bool {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repoRoot
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run() == nil
}

// EnsureRepo returns a typed precondition error when repoRoot is not a git
// work tree or the configured base branch does not exist.
func EnsureRepo(ctx context.Context, repoRoot, base string) error {
	if !gitOK(ctx, repoRoot, "rev-parse", "--is-inside-work-tree") {
		return iox.NewPrecondition(
			fmt.Sprintf("%s is not a git repository", repoRoot),
			"the worktree workflow requires a git repository", nil)
	}
	if !refExists(ctx, repoRoot, base) {
		return iox.NewPrecondition(
			fmt.Sprintf("base branch %q not found", base),
			"set worktree.base in .archetipo/config.yaml to an existing branch", nil)
	}
	return nil
}

func refExists(ctx context.Context, repoRoot, ref string) bool {
	return gitOK(ctx, repoRoot, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
}

func isAncestor(ctx context.Context, repoRoot, ref, descendant string) bool {
	return gitOK(ctx, repoRoot, "merge-base", "--is-ancestor", ref, descendant)
}

// BranchName returns the git branch name for a spec code under the configured
// prefix (e.g. "archetipo/US-002").
func BranchName(cfg domain.WorktreeConfig, code string) string {
	return cfg.BranchPrefix + code
}

// ForkRef determines the ref a spec's branch should fork from, honoring
// dependencies (BlockedBy):
//   - blockers that have no branch, or whose branch is already merged into base,
//     are considered integrated and ignored;
//   - 0 unmerged blockers  -> fork from base;
//   - 1 unmerged blocker   -> fork from that blocker's branch (stacking);
//   - >=2 unmerged blockers -> E_CONFLICT (the caller must integrate/resolve first).
func ForkRef(ctx context.Context, repoRoot string, cfg domain.WorktreeConfig, spec domain.Spec, allSpecs []domain.Spec) (string, error) {
	unmerged := UnintegratedBlockers(ctx, repoRoot, cfg, spec, allSpecs)
	switch len(unmerged) {
	case 0:
		return cfg.Base, nil
	case 1:
		return BranchName(cfg, unmerged[0]), nil
	default:
		return "", iox.NewConflict(
			fmt.Sprintf("multiple unintegrated blockers: %s", strings.Join(unmerged, ", ")),
			"integrate or merge the blockers before starting this spec", nil)
	}
}

// UnintegratedBlockers returns the codes of the spec's blockers whose branch
// exists and is not yet merged into the base branch. A blocker with no recorded
// branch is treated as already integrated.
func UnintegratedBlockers(ctx context.Context, repoRoot string, cfg domain.WorktreeConfig, spec domain.Spec, allSpecs []domain.Spec) []string {
	byCode := make(map[string]domain.Spec, len(allSpecs))
	for _, s := range allSpecs {
		byCode[s.Code] = s
	}
	var unmerged []string
	for _, code := range spec.BlockedBy {
		b, ok := byCode[code]
		branch := ""
		if ok {
			branch = b.Branch
		}
		if branch == "" {
			continue
		}
		if isAncestor(ctx, repoRoot, branch, cfg.Base) {
			continue
		}
		unmerged = append(unmerged, code)
	}
	return unmerged
}

// WorktreeRel returns the conventional worktree path (relative to repoRoot) for
// a spec code: <cfg.Dir>/<code>. It is the single source of truth for where a
// spec's worktree lives, used both to create it (Ensure) and to resolve it
// later (Resolve), so the two never disagree.
func WorktreeRel(cfg domain.WorktreeConfig, code string) string {
	return filepath.Join(cfg.Dir, code)
}

// Resolve returns the conventional worktree path (relative to repoRoot) for a
// spec and whether that worktree currently exists on disk. It depends only on
// the configured convention and the filesystem — never on persisted spec
// fields — so it stays correct even when those fields drift out of sync with
// the actual git worktree.
func Resolve(repoRoot string, cfg domain.WorktreeConfig, code string) (rel string, exists bool) {
	rel = WorktreeRel(cfg, code)
	if fi, err := os.Stat(filepath.Join(repoRoot, rel)); err == nil && fi.IsDir() {
		return rel, true
	}
	return rel, false
}

// Ensure creates (idempotently) the branch and worktree for a spec forked from
// forkRef. It returns the branch name, the worktree path (relative to
// repoRoot), and the resolved fork-base SHA used as the diff parent.
func Ensure(ctx context.Context, repoRoot string, cfg domain.WorktreeConfig, code, forkRef string) (branch, worktreeRel, forkBaseSHA string, err error) {
	branch = BranchName(cfg, code)
	worktreeRel = WorktreeRel(cfg, code)
	worktreeAbs := worktreeRel
	if !filepath.IsAbs(worktreeAbs) {
		worktreeAbs = filepath.Join(repoRoot, worktreeRel)
	}

	forkBaseSHA, err = runGit(ctx, repoRoot, "rev-parse", forkRef)
	if err != nil {
		return "", "", "", err
	}

	if !refExists(ctx, repoRoot, branch) {
		if _, err := runGit(ctx, repoRoot, "branch", branch, forkRef); err != nil {
			return "", "", "", err
		}
	}

	if _, statErr := os.Stat(worktreeAbs); statErr != nil {
		if err := os.MkdirAll(filepath.Dir(worktreeAbs), 0o755); err != nil {
			return "", "", "", fmt.Errorf("creating worktree dir: %w", err)
		}
		if _, err := runGit(ctx, repoRoot, "worktree", "add", worktreeAbs, branch); err != nil {
			return "", "", "", err
		}
	}
	return branch, worktreeRel, forkBaseSHA, nil
}

// CommitWorktreeChanges stages and commits any dirty or untracked changes in a
// spec worktree so the review diff, which is branch-based, includes all files.
func CommitWorktreeChanges(ctx context.Context, repoRoot, worktreeRel, code string) error {
	if strings.TrimSpace(worktreeRel) == "" {
		return iox.NewPrecondition(
			fmt.Sprintf("spec %s has no worktree path", code),
			"run `archetipo spec start` with worktree enabled first", nil)
	}
	worktreeAbs := worktreeRel
	if !filepath.IsAbs(worktreeAbs) {
		worktreeAbs = filepath.Join(repoRoot, worktreeRel)
	}
	if fi, err := os.Stat(worktreeAbs); err != nil || !fi.IsDir() {
		return iox.NewPrecondition(
			fmt.Sprintf("worktree for spec %s not found at %s", code, worktreeAbs),
			"run `archetipo spec start` with worktree enabled first", err)
	}
	status, err := runGitInDir(ctx, worktreeAbs, "status", "--porcelain")
	if err != nil {
		return iox.NewConflict(
			fmt.Sprintf("could not inspect worktree changes for %s", code),
			"ensure the spec worktree is a valid git checkout", err)
	}
	if strings.TrimSpace(status) == "" {
		return nil
	}
	if _, err := runGitInDir(ctx, worktreeAbs, "add", "-A"); err != nil {
		return iox.NewConflict(
			fmt.Sprintf("could not stage worktree changes for %s", code),
			"inspect the worktree and retry `archetipo spec review`", err)
	}
	if _, err := runGitInDir(ctx, worktreeAbs, "commit", "-m", fmt.Sprintf("chore(%s): prepare review", code)); err != nil {
		return iox.NewConflict(
			fmt.Sprintf("could not commit worktree changes for %s", code),
			"inspect the worktree and retry `archetipo spec review`", err)
	}
	return nil
}

// AheadBehind reports how many commits branch is ahead of and behind base.
func AheadBehind(ctx context.Context, repoRoot, base, branch string) (ahead, behind int, err error) {
	out, err := runGit(ctx, repoRoot, "rev-list", "--left-right", "--count", base+"..."+branch)
	if err != nil {
		return 0, 0, err
	}
	fields := strings.Fields(out)
	if len(fields) != 2 {
		return 0, 0, fmt.Errorf("unexpected rev-list output: %q", out)
	}
	// left = commits in base not in branch (behind); right = commits in branch
	// not in base (ahead).
	behind, _ = strconv.Atoi(fields[0])
	ahead, _ = strconv.Atoi(fields[1])
	return ahead, behind, nil
}

// Integrate merges branch into base with --no-ff, then removes the worktree and
// deletes the branch. On merge conflict it aborts the merge and returns an
// E_CONFLICT error listing the conflicting files; the working tree is left
// clean so the caller can resolve manually and retry.
func Integrate(ctx context.Context, repoRoot string, cfg domain.WorktreeConfig, branch, worktreeRel string) error {
	if _, err := runGit(ctx, repoRoot, "checkout", cfg.Base); err != nil {
		return err
	}
	if _, err := runGit(ctx, repoRoot, "merge", "--no-ff", branch); err != nil {
		conflicts, _ := runGit(ctx, repoRoot, "diff", "--name-only", "--diff-filter=U")
		_, _ = runGit(ctx, repoRoot, "merge", "--abort")
		hint := "resolve the conflicts manually, then retry"
		msg := fmt.Sprintf("merge of %s into %s has conflicts", branch, cfg.Base)
		if files := strings.TrimSpace(conflicts); files != "" {
			msg = fmt.Sprintf("%s: %s", msg, strings.Join(strings.Fields(files), ", "))
		}
		return iox.NewConflict(msg, hint, nil)
	}
	if worktreeRel != "" {
		worktreeAbs := filepath.Join(repoRoot, worktreeRel)
		if _, err := runGit(ctx, repoRoot, "worktree", "remove", "--force", worktreeAbs); err != nil {
			// Directory may already be gone; prune stale registrations below.
			_, _ = runGit(ctx, repoRoot, "worktree", "prune")
		}
	}
	if refExists(ctx, repoRoot, branch) {
		if _, err := runGit(ctx, repoRoot, "branch", "-D", branch); err != nil {
			return err
		}
	}
	return nil
}
