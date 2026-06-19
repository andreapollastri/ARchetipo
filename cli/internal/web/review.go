package web

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/techreloaded-ar/ARchetipo/cli/internal/connector"
	"github.com/techreloaded-ar/ARchetipo/cli/internal/domain"
	"github.com/techreloaded-ar/ARchetipo/cli/internal/gitwt"
	"github.com/techreloaded-ar/ARchetipo/cli/internal/iox"
)

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
	rs, ok := s.conn.(connector.ReviewStore)
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
	rs, ok := s.conn.(connector.ReviewStore)
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

// handleRequestChanges moves the saved inline comments into the spec body as a
// "## Rework Feedback" section, flags the spec as in rework, transitions it back
// to TODO, and clears the review. The feedback now lives inside the spec: the
// next archetipo-plan run reads it (inside the spec's worktree) and turns each
// item into a Fix task before re-planning.
func (s *Server) handleRequestChanges(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	if code == "" {
		writeError(w, iox.NewInvalidInput("missing spec code", "", nil))
		return
	}
	ctx := r.Context()
	rs, ok := s.conn.(connector.ReviewStore)
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
	spec, err := s.conn.ReadSpecDetail(ctx, code)
	if err != nil {
		writeError(w, err)
		return
	}
	body := domain.AppendReworkFeedback(spec.Body, review.Comments)
	rework := true
	if _, err := s.conn.UpdateSpec(ctx, code, domain.SpecUpdate{Body: &body, Rework: &rework}); err != nil {
		writeError(w, err)
		return
	}
	if _, err := s.conn.TransitionStatus(ctx, code, domain.StatusTodo); err != nil {
		writeError(w, err)
		return
	}
	// Clear the review: the feedback now lives in the spec body.
	if err := rs.SaveReview(ctx, code, domain.Review{Comments: []domain.ReviewComment{}}); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "comments_moved": len(review.Comments)})
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
	blockers, err := gitwt.UnintegratedBlockers(ctx, s.cfg.ProjectRoot, s.cfg.Worktree, spec, allSpecs)
	if err != nil {
		writeError(w, err)
		return
	}
	if len(blockers) > 0 {
		writeError(w, iox.NewConflict(fmt.Sprintf("unintegrated blockers: %s", strings.Join(blockers, ", ")), "integrate the blockers first", nil))
		return
	}
	if err := gitwt.Integrate(ctx, s.cfg.ProjectRoot, s.cfg.Worktree, spec.Branch, spec.Worktree); err != nil {
		writeError(w, err)
		return
	}
	// Clear persisted worktree metadata after a successful integrate.
	emptyStr := ""
	_, _ = s.conn.UpdateSpec(ctx, code, domain.SpecUpdate{
		Branch:   &emptyStr,
		Worktree: &emptyStr,
		ForkBase: &emptyStr,
	}) // best-effort: ignore error, merge succeeded.
	if _, err := s.conn.TransitionStatus(ctx, code, domain.StatusDone); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "merged_at": time.Now().UTC().Format(time.RFC3339)})
}
