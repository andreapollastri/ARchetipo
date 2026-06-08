package web

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/techreloaded-ar/ARchetipo/cli/internal/config"
	"github.com/techreloaded-ar/ARchetipo/cli/internal/connector/filefs"
	"github.com/techreloaded-ar/ARchetipo/cli/internal/connector/inmemory"
	"github.com/techreloaded-ar/ARchetipo/cli/internal/domain"
)

func newTestServer(t *testing.T) (*Server, *inmemory.Connector) {
	t.Helper()
	cfg := config.Default()
	conn := inmemory.New(cfg)
	srv, err := NewServer(conn, cfg, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return srv, conn
}

func seedSpecs(t *testing.T, c *inmemory.Connector) {
	t.Helper()
	specs := []domain.Spec{
		{Code: "US-001", Title: "Setup", Epic: domain.Epic{Code: "EP-001", Title: "F"}, Priority: domain.PriorityHigh, Points: 3, Status: domain.StatusTodo},
		{Code: "US-002", Title: "Auth", Epic: domain.Epic{Code: "EP-001", Title: "F"}, Priority: domain.PriorityMedium, Points: 5, Status: domain.StatusPlanned},
	}
	if _, err := c.SaveInitialBacklog(context.Background(), specs); err != nil {
		t.Fatal(err)
	}
}

func TestGetBoard(t *testing.T) {
	srv, conn := newTestServer(t)
	seedSpecs(t, conn)

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
			todoCount = len(c.Specs)
		}
		if c.ID == "planned" {
			plannedCount = len(c.Specs)
		}
	}
	if todoCount != 1 || plannedCount != 1 {
		t.Errorf("expected 1+1 specs in todo+planned, got %d+%d", todoCount, plannedCount)
	}
}

func TestUpdateSpecEndpoint(t *testing.T) {
	srv, conn := newTestServer(t)
	seedSpecs(t, conn)

	patch := map[string]any{"title": "Setup renamed", "priority": "LOW", "points": 8}
	body, _ := json.Marshal(patch)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/spec/US-001", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	srv.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, body=%s", w.Code, w.Body.String())
	}
	got, err := conn.ReadSpecDetail(context.Background(), "US-001")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Setup renamed" || got.Priority != domain.PriorityLow || got.Points != 8 {
		t.Errorf("update not applied: %+v", got)
	}
}

func TestUpdateSpecNotFound(t *testing.T) {
	srv, conn := newTestServer(t)
	seedSpecs(t, conn)

	body := []byte(`{"title":"x"}`)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/spec/US-404", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	srv.mux.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d (body=%s)", w.Code, w.Body.String())
	}
}

func TestMoveCard(t *testing.T) {
	srv, conn := newTestServer(t)
	seedSpecs(t, conn)

	body := []byte(`{"code":"US-001","to":"in_progress"}`)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/board/move", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	srv.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, body=%s", w.Code, w.Body.String())
	}
	got, err := conn.ReadSpecDetail(context.Background(), "US-001")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.StatusInProgress {
		t.Errorf("status not updated: %q", got.Status)
	}
}

func TestSavePlanEndpoint(t *testing.T) {
	srv, conn := newTestServer(t)
	seedSpecs(t, conn)

	plan := map[string]any{
		"plan_body": "## Plan\n\nbody",
		"tasks": []map[string]any{
			{"id": "TASK-01", "title": "do x", "type": "Impl", "status": "TODO"},
		},
	}
	body, _ := json.Marshal(plan)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/spec/US-001/plan", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	srv.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, body=%s", w.Code, w.Body.String())
	}
	tasks, err := conn.ReadSpecTasks(context.Background(), "US-001")
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].ID != "TASK-01" {
		t.Errorf("plan not saved: %+v", tasks)
	}
}

func newFileServer(t *testing.T) (*Server, config.Config) {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Default()
	cfg.ProjectRoot = dir
	conn := filefs.New(cfg)
	srv, err := NewServer(conn, cfg, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return srv, cfg
}

func TestGetPRDMissing(t *testing.T) {
	srv, _ := newFileServer(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/prd", nil)
	srv.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, body=%s", w.Code, w.Body.String())
	}
	var got prdView
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Body != "" {
		t.Errorf("expected empty body, got %q", got.Body)
	}
}

func TestSaveAndGetPRD(t *testing.T) {
	srv, cfg := newFileServer(t)
	body, _ := json.Marshal(prdView{Body: "# PRD\nhello"})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/prd", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	srv.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT status: got %d, body=%s", w.Code, w.Body.String())
	}
	raw, err := os.ReadFile(cfg.AbsPath(cfg.Paths.PRD))
	if err != nil {
		t.Fatalf("PRD file missing: %v", err)
	}
	if string(raw) != "# PRD\nhello" {
		t.Errorf("file content mismatch: %q", string(raw))
	}

	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodGet, "/api/prd", nil)
	srv.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("GET status: got %d", w.Code)
	}
	var got prdView
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Body != "# PRD\nhello" {
		t.Errorf("body mismatch: %q", got.Body)
	}
}

func TestPRDUnsupportedConnector(t *testing.T) {
	srv, _ := newTestServer(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/prd", nil)
	srv.mux.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestListMockups(t *testing.T) {
	srv, cfg := newFileServer(t)
	root := cfg.AbsPath(cfg.Paths.Mockups)
	for _, name := range []string{"app-home", "US-001", "broken"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"app-home", "US-001"} {
		if err := os.WriteFile(filepath.Join(root, name, "index.html"), []byte("<h1>"+name+"</h1>"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/mockups", nil)
	srv.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, body=%s", w.Code, w.Body.String())
	}
	var got mockupsView
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Mockups) != 2 {
		t.Fatalf("expected 2 mockups, got %d: %+v", len(got.Mockups), got.Mockups)
	}
	byName := map[string]domain.MockupEntry{}
	for _, m := range got.Mockups {
		byName[m.Name] = m
	}
	if byName["US-001"].SpecCode != "US-001" {
		t.Errorf("US-001 should be tagged with spec code, got %q", byName["US-001"].SpecCode)
	}
	if byName["app-home"].SpecCode != "" {
		t.Errorf("app-home should not be tagged with a spec code, got %q", byName["app-home"].SpecCode)
	}
	if byName["app-home"].URL != "/mockups/app-home/index.html" {
		t.Errorf("unexpected URL: %q", byName["app-home"].URL)
	}
}

func TestListMockupsUnsupportedConnectorReturnsEmpty(t *testing.T) {
	srv, _ := newTestServer(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/mockups", nil)
	srv.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var got mockupsView
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Mockups) != 0 {
		t.Errorf("expected empty list, got %+v", got.Mockups)
	}
}

func TestStreamBoardSendsEventOnPublish(t *testing.T) {
	srv, _ := newTestServer(t)
	ts := httptest.NewServer(srv.mux)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/board/stream", nil)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("expected SSE content type, got %q", ct)
	}

	reader := bufio.NewReader(resp.Body)
	// Read the initial ": connected" comment so the stream is established
	// before we publish, otherwise the publish could race the subscribe.
	if _, err := reader.ReadString('\n'); err != nil {
		t.Fatalf("read initial comment: %v", err)
	}

	// Give the SSE handler a moment to register its subscription on the broker.
	time.Sleep(50 * time.Millisecond)
	srv.broker.Publish()

	gotEvent := make(chan string, 1)
	go func() {
		var buf strings.Builder
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			buf.WriteString(line)
			if strings.Contains(buf.String(), "event: board_changed") {
				gotEvent <- buf.String()
				return
			}
		}
	}()

	select {
	case <-gotEvent:
	case <-time.After(2 * time.Second):
		t.Fatal("did not receive board_changed event within 2s")
	}
}

func TestGetSpec(t *testing.T) {
	srv, conn := newTestServer(t)
	seedSpecs(t, conn)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/spec/US-001", nil)
	srv.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, body=%s", w.Code, w.Body.String())
	}
	var out specDetailView
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Spec.Code != "US-001" {
		t.Errorf("expected US-001, got %+v", out.Spec)
	}
}

func TestRequestChangesMovesCommentsIntoSpec(t *testing.T) {
	srv, _ := newFileServer(t)
	ctx := context.Background()
	if _, err := srv.conn.SaveInitialBacklog(ctx, []domain.Spec{
		{Code: "US-001", Title: "Greeting", Epic: domain.Epic{Code: "EP-001", Title: "F"}, Status: domain.StatusReview, Body: "## User Story\nas a user"},
	}); err != nil {
		t.Fatal(err)
	}
	rs := srv.conn.(reviewStore)
	if err := rs.SaveReview(ctx, "US-001", domain.Review{Comments: []domain.ReviewComment{
		{File: "hello.txt", Line: 3, Side: "new", Body: "localize this greeting"},
	}}); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/spec/US-001/request-changes", nil)
	srv.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, body=%s", w.Code, w.Body.String())
	}

	spec, err := srv.conn.ReadSpecDetail(ctx, "US-001")
	if err != nil {
		t.Fatal(err)
	}
	if spec.Status != domain.StatusTodo {
		t.Errorf("expected status TODO, got %s", spec.Status)
	}
	if !spec.Rework {
		t.Error("expected rework flag to be set")
	}
	for _, want := range []string{"## Rework Feedback", "hello.txt:3", "localize this greeting"} {
		if !strings.Contains(spec.Body, want) {
			t.Errorf("spec body missing %q; body=%q", want, spec.Body)
		}
	}
	// Original body is preserved.
	if !strings.Contains(spec.Body, "## User Story") {
		t.Error("original body was discarded")
	}
	// Review is cleared after the comments move into the spec.
	rev, err := rs.ReadReview(ctx, "US-001")
	if err != nil {
		t.Fatal(err)
	}
	if len(rev.Comments) != 0 {
		t.Errorf("expected review cleared, got %d comments", len(rev.Comments))
	}
}

func TestRequestChangesNoCommentsErrors(t *testing.T) {
	srv, _ := newFileServer(t)
	ctx := context.Background()
	if _, err := srv.conn.SaveInitialBacklog(ctx, []domain.Spec{
		{Code: "US-001", Title: "Greeting", Status: domain.StatusReview},
	}); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/spec/US-001/request-changes", nil)
	srv.mux.ServeHTTP(w, r)
	if w.Code == http.StatusOK {
		t.Fatalf("expected error without comments, got 200: %s", w.Body.String())
	}
}
