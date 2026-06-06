package web

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/techreloaded-ar/ARchetipo/cli/internal/domain"
	"github.com/techreloaded-ar/ARchetipo/cli/internal/gitwt"
	"github.com/techreloaded-ar/ARchetipo/cli/internal/iox"
)

// reviewStore is an optional capability connectors can implement to persist the
// inline review comments left on a spec's diff. The filefs connector stores
// them under .archetipo/reviews/{code}.yaml; connectors without it simply
// expose no saved comments.
type reviewStore interface {
	ReadReview(ctx context.Context, code string) (domain.Review, error)
	SaveReview(ctx context.Context, code string, r domain.Review) error
}

type diffView struct {
	Base   string           `json:"base"`
	Branch string           `json:"branch"`
	Ahead  int              `json:"ahead"`
	Behind int              `json:"behind"`
	Files  []gitwt.FileDiff `json:"files"`
}

// handleGetDiff returns the structured diff for a spec under review. When the
// spec has a recorded branch (worktree workflow) the diff is
// `git diff <fork_base>...<branch>`; otherwise it falls back to `git diff
// <base>` against the working tree, where base comes from ?base= or the
// configured worktree base.
func (s *Server) handleGetDiff(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	if code == "" {
		writeError(w, iox.NewInvalidInput("missing spec code", "use /api/spec/US-XXX/diff", nil))
		return
	}
	ctx := r.Context()
	spec, err := s.conn.ReadSpecDetail(ctx, code)
	if err != nil {
		writeError(w, err)
		return
	}
	root := s.cfg.ProjectRoot
	if spec.Branch != "" {
		forkBase := spec.ForkBase
		if forkBase == "" {
			forkBase = s.cfg.Worktree.Base
		}
		files, err := gitwt.Diff(ctx, root, forkBase, spec.Branch)
		if err != nil {
			writeError(w, err)
			return
		}
		ahead, behind, _ := gitwt.AheadBehind(ctx, root, s.cfg.Worktree.Base, spec.Branch)
		writeJSON(w, http.StatusOK, diffView{Base: forkBase, Branch: spec.Branch, Ahead: ahead, Behind: behind, Files: files})
		return
	}
	base := strings.TrimSpace(r.URL.Query().Get("base"))
	if base == "" {
		base = s.cfg.Worktree.Base
	}
	files, err := gitwt.DiffWorkingTree(ctx, root, base)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, diffView{Base: base, Files: files})
}

func (s *Server) handleGetReview(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	if code == "" {
		writeError(w, iox.NewInvalidInput("missing spec code", "", nil))
		return
	}
	rs, ok := s.conn.(reviewStore)
	if !ok {
		writeJSON(w, http.StatusOK, domain.Review{Comments: []domain.ReviewComment{}})
		return
	}
	review, err := rs.ReadReview(r.Context(), code)
	if err != nil {
		writeError(w, err)
		return
	}
	if review.Comments == nil {
		review.Comments = []domain.ReviewComment{}
	}
	writeJSON(w, http.StatusOK, review)
}

func (s *Server) handleSaveReview(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	if code == "" {
		writeError(w, iox.NewInvalidInput("missing spec code", "", nil))
		return
	}
	rs, ok := s.conn.(reviewStore)
	if !ok {
		writeError(w, iox.NewConnector(iox.CodePreconditionMissing, "this connector does not persist review comments", "use the file connector", nil))
		return
	}
	var review domain.Review
	if err := decodeJSON(r, &review); err != nil {
		writeError(w, err)
		return
	}
	if err := rs.SaveReview(r.Context(), code, review); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, review)
}

// handleRequestChanges converts the saved inline comments into Fix tasks
// appended to the spec's plan, transitions the spec back to IN PROGRESS, and
// clears the review (comments are ephemeral: they now live as tasks).
func (s *Server) handleRequestChanges(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	if code == "" {
		writeError(w, iox.NewInvalidInput("missing spec code", "", nil))
		return
	}
	ctx := r.Context()
	rs, ok := s.conn.(reviewStore)
	if !ok {
		writeError(w, iox.NewConnector(iox.CodePreconditionMissing, "this connector does not persist review comments", "use the file connector", nil))
		return
	}
	review, err := rs.ReadReview(ctx, code)
	if err != nil {
		writeError(w, err)
		return
	}
	if len(review.Comments) == 0 {
		writeError(w, iox.NewInvalidInput("no review comments to convert", "add inline comments before requesting changes", nil))
		return
	}
	tasks, planBody, err := s.readPlanForSpec(ctx, code)
	if err != nil {
		writeError(w, err)
		return
	}
	tasks = appendFixTasks(tasks, review.Comments)
	if _, err := s.conn.SavePlan(ctx, code, domain.PlanInput{PlanBody: planBody, Tasks: tasks}); err != nil {
		writeError(w, err)
		return
	}
	if _, err := s.conn.TransitionStatus(ctx, code, domain.StatusInProgress); err != nil {
		writeError(w, err)
		return
	}
	// Clear the review: feedback now lives as Fix tasks.
	if err := rs.SaveReview(ctx, code, domain.Review{Comments: []domain.ReviewComment{}}); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "tasks_added": len(review.Comments)})
}

// appendFixTasks turns review comments into Fix tasks appended after the
// existing tasks, continuing the TASK-NN numbering.
func appendFixTasks(tasks []domain.Task, comments []domain.ReviewComment) []domain.Task {
	next := nextTaskNumber(tasks)
	for _, c := range comments {
		id := fmt.Sprintf("TASK-%02d", next)
		next++
		anchor := c.File
		if c.Line > 0 {
			anchor = fmt.Sprintf("%s:%d", c.File, c.Line)
		}
		tasks = append(tasks, domain.Task{
			ID:     id,
			Title:  summarize(c.Body),
			Type:   domain.TaskFix,
			Status: domain.StatusTodo,
			Body:   fmt.Sprintf("%s\n\n%s", anchor, c.Body),
		})
	}
	return tasks
}

func nextTaskNumber(tasks []domain.Task) int {
	max := 0
	for _, t := range tasks {
		if i := strings.LastIndexByte(t.ID, '-'); i >= 0 {
			if n, err := strconv.Atoi(t.ID[i+1:]); err == nil && n > max {
				max = n
			}
		}
	}
	return max + 1
}

func summarize(body string) string {
	line := strings.TrimSpace(body)
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	const limit = 80
	if len(line) > limit {
		line = strings.TrimSpace(line[:limit]) + "…"
	}
	if line == "" {
		line = "Review fix"
	}
	return line
}

// handleIntegrate merges the spec's branch into base, removes the worktree and
// branch, and transitions the spec to DONE. Mirrors `archetipo spec integrate`.
func (s *Server) handleIntegrate(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	if code == "" {
		writeError(w, iox.NewInvalidInput("missing spec code", "", nil))
		return
	}
	ctx := r.Context()
	if !s.cfg.Worktree.Enabled {
		writeError(w, iox.NewConflict("worktree workflow is disabled", "enable worktree.enabled in config.yaml", nil))
		return
	}
	if err := gitwt.EnsureRepo(ctx, s.cfg.ProjectRoot, s.cfg.Worktree.Base); err != nil {
		writeError(w, err)
		return
	}
	spec, err := s.conn.ReadSpecDetail(ctx, code)
	if err != nil {
		writeError(w, err)
		return
	}
	if spec.Branch == "" {
		writeError(w, iox.NewPrecondition(fmt.Sprintf("spec %s has no worktree branch", code), "", nil))
		return
	}
	allSpecs, err := s.conn.FetchBacklogItems(ctx, "")
	if err != nil {
		writeError(w, err)
		return
	}
	if blockers := gitwt.UnintegratedBlockers(ctx, s.cfg.ProjectRoot, s.cfg.Worktree, spec, allSpecs); len(blockers) > 0 {
		writeError(w, iox.NewConflict(fmt.Sprintf("unintegrated blockers: %s", strings.Join(blockers, ", ")), "integrate the blockers first", nil))
		return
	}
	if err := gitwt.Integrate(ctx, s.cfg.ProjectRoot, s.cfg.Worktree, spec.Branch, spec.Worktree); err != nil {
		writeError(w, err)
		return
	}
	if _, err := s.conn.TransitionStatus(ctx, code, domain.StatusDone); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "merged_at": time.Now().UTC().Format(time.RFC3339)})
}
