package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/techreloaded-ar/ARchetipo/cli/internal/domain"
	"github.com/techreloaded-ar/ARchetipo/cli/internal/iox"
)

// boardColumnView is the JSON shape of one Kanban column in GET /api/board.
type boardColumnView struct {
	ID      string         `json:"id"`
	Title   string         `json:"title"`
	Status  domain.Status  `json:"status"`
	Stories []domain.Story `json:"stories"`
}

type boardView struct {
	Columns []boardColumnView `json:"columns"`
	Epics   []domain.Epic     `json:"epics"`
}

// canonical board layout: keeps the order TODO → PLANNED → IN PROGRESS → REVIEW → DONE.
var boardLayout = []struct {
	ID     string
	Status domain.Status
}{
	{"todo", domain.StatusTodo},
	{"planned", domain.StatusPlanned},
	{"in_progress", domain.StatusInProgress},
	{"review", domain.StatusReview},
	{"done", domain.StatusDone},
}

func (s *Server) handleGetBoard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	info, err := s.conn.InitializeConnector(ctx)
	if err != nil {
		writeError(w, err)
		return
	}
	labels := info.Workflow.Statuses
	stories, err := s.conn.FetchBacklogItems(ctx, "")
	if err != nil {
		writeError(w, err)
		return
	}
	summary, err := s.conn.ReadExistingBacklog(ctx)
	if err != nil {
		writeError(w, err)
		return
	}
	view := boardView{Epics: summary.Epics}
	titleFor := func(id string) string {
		switch id {
		case "todo":
			return labels.Todo
		case "planned":
			return labels.Planned
		case "in_progress":
			return labels.InProgress
		case "review":
			return labels.Review
		case "done":
			return labels.Done
		}
		return id
	}
	for _, col := range boardLayout {
		c := boardColumnView{ID: col.ID, Title: titleFor(col.ID), Status: col.Status}
		for _, st := range stories {
			if st.Status == col.Status {
				c.Stories = append(c.Stories, st)
			}
		}
		view.Columns = append(view.Columns, c)
	}
	writeJSON(w, http.StatusOK, view)
}

type storyDetailView struct {
	Story    domain.Story  `json:"story"`
	PlanBody string        `json:"plan_body"`
	Tasks    []domain.Task `json:"tasks"`
}

func (s *Server) handleGetStory(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	if code == "" {
		writeError(w, iox.NewInvalidInput("missing story code", "use /api/story/US-XXX", nil))
		return
	}
	ctx := r.Context()
	story, err := s.conn.ReadStoryDetail(ctx, code)
	if err != nil {
		writeError(w, err)
		return
	}
	tasks, planBody, err := s.readPlanForStory(ctx, code)
	if err != nil {
		writeError(w, err)
		return
	}
	if tasks == nil {
		tasks = []domain.Task{}
	}
	writeJSON(w, http.StatusOK, storyDetailView{Story: story, PlanBody: planBody, Tasks: tasks})
}

// readPlanForStory returns the tasks and (when readable) the plan body for a
// story. The connector interface only exposes ReadStoryTasks, so for connectors
// that also store a plan body (filefs) we look it up via the optional
// planBodyReader. A missing plan is not an error: the viewer should still be
// able to display the story with an empty plan.
func (s *Server) readPlanForStory(ctx context.Context, code string) ([]domain.Task, string, error) {
	tasks, err := s.conn.ReadStoryTasks(ctx, code)
	if err != nil {
		var ce *iox.CodedError
		if errors.As(err, &ce) && ce.Code == iox.CodePreconditionMissing {
			return nil, "", nil
		}
		return nil, "", err
	}
	body := ""
	if pr, ok := s.conn.(planBodyReader); ok {
		if b, err := pr.ReadPlanBody(ctx, code); err == nil {
			body = b
		}
	}
	return tasks, body, nil
}

// planBodyReader is an optional capability connectors can implement to expose
// the plan body text alongside the tasks. The viewer probes for it at runtime
// via a type assertion, so connectors that do not implement it (e.g. github)
// simply return tasks with an empty body.
type planBodyReader interface {
	ReadPlanBody(ctx context.Context, code string) (string, error)
}

// prdReader is an optional capability connectors can implement to expose the
// raw PRD markdown so the viewer can render it next to stories and plans.
type prdReader interface {
	ReadPRD(ctx context.Context) (string, error)
}

// mockupLister is an optional capability connectors can implement to list the
// design mockups produced by archetipo-design (HTML folders under paths.mockups).
type mockupLister interface {
	ListMockups(ctx context.Context) ([]domain.MockupEntry, error)
}

type prdView struct {
	Body string `json:"body"`
}

func (s *Server) handleGetPRD(w http.ResponseWriter, r *http.Request) {
	pr, ok := s.conn.(prdReader)
	if !ok {
		writeError(w, iox.NewConnector(iox.CodePreconditionMissing, "this connector does not expose a PRD", "use the file connector to read the PRD", nil))
		return
	}
	body, err := pr.ReadPRD(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, prdView{Body: body})
}

func (s *Server) handleSavePRD(w http.ResponseWriter, r *http.Request) {
	var req prdView
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	if _, err := s.conn.SavePRD(r.Context(), req.Body); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, prdView{Body: req.Body})
}

type mockupsView struct {
	Mockups []domain.MockupEntry `json:"mockups"`
}

func (s *Server) handleListMockups(w http.ResponseWriter, r *http.Request) {
	ml, ok := s.conn.(mockupLister)
	if !ok {
		writeJSON(w, http.StatusOK, mockupsView{Mockups: []domain.MockupEntry{}})
		return
	}
	list, err := ml.ListMockups(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	if list == nil {
		list = []domain.MockupEntry{}
	}
	writeJSON(w, http.StatusOK, mockupsView{Mockups: list})
}

func (s *Server) handleUpdateStory(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	if code == "" {
		writeError(w, iox.NewInvalidInput("missing story code", "", nil))
		return
	}
	var patch domain.StoryUpdate
	if err := decodeJSON(r, &patch); err != nil {
		writeError(w, err)
		return
	}
	if _, err := s.conn.UpdateStory(r.Context(), code, patch); err != nil {
		writeError(w, err)
		return
	}
	story, err := s.conn.ReadStoryDetail(r.Context(), code)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"story": story})
}

type savePlanReq struct {
	PlanBody string        `json:"plan_body"`
	Tasks    []domain.Task `json:"tasks"`
}

func (s *Server) handleSavePlan(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	if code == "" {
		writeError(w, iox.NewInvalidInput("missing story code", "", nil))
		return
	}
	var req savePlanReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	res, err := s.conn.SavePlan(r.Context(), code, domain.PlanInput{PlanBody: req.PlanBody, Tasks: req.Tasks})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

type moveReq struct {
	Code   string  `json:"code"`
	To     string  `json:"to"`
	Before *string `json:"before,omitempty"`
	After  *string `json:"after,omitempty"`
}

func (s *Server) handleMoveCard(w http.ResponseWriter, r *http.Request) {
	var req moveReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	if req.Code == "" || req.To == "" {
		writeError(w, iox.NewInvalidInput("code and to are required", "", nil))
		return
	}
	anchor := domain.ReorderAnchor{}
	if req.Before != nil {
		anchor.Before = *req.Before
	}
	if req.After != nil {
		anchor.After = *req.After
	}
	res, err := s.conn.MoveBoardCard(r.Context(), req.Code, req.To, anchor)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

type reorderReq struct {
	Code   string  `json:"code"`
	Before *string `json:"before,omitempty"`
	After  *string `json:"after,omitempty"`
}

func (s *Server) handleReorderBacklog(w http.ResponseWriter, r *http.Request) {
	var req reorderReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	if req.Code == "" {
		writeError(w, iox.NewInvalidInput("code is required", "", nil))
		return
	}
	anchor := domain.ReorderAnchor{}
	if req.Before != nil {
		anchor.Before = *req.Before
	}
	if req.After != nil {
		anchor.After = *req.After
	}
	res, err := s.conn.ReorderBacklog(r.Context(), req.Code, anchor)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// helpers

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(payload)
}

func decodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return iox.NewInvalidInput("invalid JSON body", "", err)
	}
	return nil
}

func writeError(w http.ResponseWriter, err error) {
	var ce *iox.CodedError
	if !errors.As(err, &ce) {
		ce = iox.NewInternal(err.Error(), err)
	}
	status := http.StatusInternalServerError
	switch ce.Code {
	case iox.CodeInvalidInput:
		status = http.StatusBadRequest
	case iox.CodeNotFound, iox.CodePreconditionMissing:
		status = http.StatusNotFound
	case iox.CodeConflict:
		status = http.StatusConflict
	case iox.CodeConnectorAuth, iox.CodeConnectorNetwork, iox.CodeConnectorBackend:
		status = http.StatusBadGateway
	}
	writeJSON(w, status, map[string]any{
		"error":   ce.Message,
		"code":    ce.Code,
		"hint":    ce.Hint,
	})
}
