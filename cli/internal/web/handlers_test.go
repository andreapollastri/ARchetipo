package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/techreloaded-ar/ARchetipo/cli/internal/config"
	"github.com/techreloaded-ar/ARchetipo/cli/internal/connector/inmemory"
	"github.com/techreloaded-ar/ARchetipo/cli/internal/domain"
)

func newTestServer(t *testing.T) (*Server, *inmemory.Connector) {
	t.Helper()
	cfg := config.Default()
	conn := inmemory.New(cfg)
	srv, err := NewServer(conn, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return srv, conn
}

func seedStories(t *testing.T, c *inmemory.Connector) {
	t.Helper()
	stories := []domain.Story{
		{Code: "US-001", Title: "Setup", Epic: domain.Epic{Code: "EP-001", Title: "F"}, Priority: domain.PriorityHigh, StoryPoints: 3, Status: domain.StatusTodo},
		{Code: "US-002", Title: "Auth", Epic: domain.Epic{Code: "EP-001", Title: "F"}, Priority: domain.PriorityMedium, StoryPoints: 5, Status: domain.StatusPlanned},
	}
	if _, err := c.SaveInitialBacklog(context.Background(), stories); err != nil {
		t.Fatal(err)
	}
}

func TestGetBoard(t *testing.T) {
	srv, conn := newTestServer(t)
	seedStories(t, conn)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/board", nil)
	srv.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, body=%s", w.Code, w.Body.String())
	}
	var view boardView
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Columns) != 5 {
		t.Fatalf("expected 5 columns, got %d", len(view.Columns))
	}
	var todoCount, plannedCount int
	for _, c := range view.Columns {
		if c.ID == "todo" {
			todoCount = len(c.Stories)
		}
		if c.ID == "planned" {
			plannedCount = len(c.Stories)
		}
	}
	if todoCount != 1 || plannedCount != 1 {
		t.Errorf("expected 1+1 stories in todo+planned, got %d+%d", todoCount, plannedCount)
	}
}

func TestUpdateStoryEndpoint(t *testing.T) {
	srv, conn := newTestServer(t)
	seedStories(t, conn)

	patch := map[string]any{"title": "Setup renamed", "priority": "LOW", "story_points": 8}
	body, _ := json.Marshal(patch)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/story/US-001", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	srv.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, body=%s", w.Code, w.Body.String())
	}
	got, err := conn.ReadStoryDetail(context.Background(), "US-001")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Setup renamed" || got.Priority != domain.PriorityLow || got.StoryPoints != 8 {
		t.Errorf("update not applied: %+v", got)
	}
}

func TestUpdateStoryNotFound(t *testing.T) {
	srv, conn := newTestServer(t)
	seedStories(t, conn)

	body := []byte(`{"title":"x"}`)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/story/US-404", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	srv.mux.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d (body=%s)", w.Code, w.Body.String())
	}
}

func TestMoveCard(t *testing.T) {
	srv, conn := newTestServer(t)
	seedStories(t, conn)

	body := []byte(`{"code":"US-001","to":"in_progress"}`)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/board/move", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	srv.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, body=%s", w.Code, w.Body.String())
	}
	got, err := conn.ReadStoryDetail(context.Background(), "US-001")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.StatusInProgress {
		t.Errorf("status not updated: %q", got.Status)
	}
}

func TestSavePlanEndpoint(t *testing.T) {
	srv, conn := newTestServer(t)
	seedStories(t, conn)

	plan := map[string]any{
		"plan_body": "## Plan\n\nbody",
		"tasks": []map[string]any{
			{"id": "TASK-01", "title": "do x", "type": "Impl", "status": "TODO"},
		},
	}
	body, _ := json.Marshal(plan)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/story/US-001/plan", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	srv.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, body=%s", w.Code, w.Body.String())
	}
	tasks, err := conn.ReadStoryTasks(context.Background(), "US-001")
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].ID != "TASK-01" {
		t.Errorf("plan not saved: %+v", tasks)
	}
}

func TestGetStory(t *testing.T) {
	srv, conn := newTestServer(t)
	seedStories(t, conn)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/story/US-001", nil)
	srv.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, body=%s", w.Code, w.Body.String())
	}
	var out storyDetailView
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Story.Code != "US-001" {
		t.Errorf("expected US-001, got %+v", out.Story)
	}
}
