package filefs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/techreloaded-ar/ARchetipo/cli/internal/config"
	"github.com/techreloaded-ar/ARchetipo/cli/internal/domain"
)

func newTestConnector(t *testing.T) *Connector {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Default()
	cfg.ProjectRoot = dir
	cfg.Paths.Backlog = filepath.Join(dir, ".archetipo", "backlog.yaml")
	cfg.Paths.Planning = filepath.Join(dir, ".archetipo", "plans")
	cfg.Paths.PRD = filepath.Join(dir, "PRD.md")
	return New(cfg)
}

func TestSpecMarkerRoundTrip(t *testing.T) {
	s := domain.Spec{
		Code:      "US-007",
		Title:     "Login utente",
		Epic:      domain.Epic{Code: "EP-002", Title: "Auth Foundations"},
		Priority:  domain.PriorityHigh,
		Points:    5,
		Status:    domain.StatusPlanned,
		BlockedBy: []string{"US-002", "US-003"},
		Scope:     "MVP",
	}
	line := specMarker(s)
	mk, ok := parseMarker(line)
	if !ok {
		t.Fatalf("failed to parse generated marker: %s", line)
	}
	got, err := specFromMarker(mk)
	if err != nil {
		t.Fatal(err)
	}
	got.Title = s.Title // marker doesn't carry title
	if got.Code != s.Code || got.Priority != s.Priority || got.Points != s.Points || got.Status != s.Status || got.Scope != s.Scope {
		t.Errorf("structured fields differ: got=%+v want=%+v", got, s)
	}
	if got.Epic.Code != s.Epic.Code || got.Epic.Title != s.Epic.Title {
		t.Errorf("epic differs: got=%+v want=%+v", got.Epic, s.Epic)
	}
	if len(got.BlockedBy) != 2 || got.BlockedBy[0] != "US-002" || got.BlockedBy[1] != "US-003" {
		t.Errorf("blocked_by differs: %v", got.BlockedBy)
	}
}

func TestRenderBacklogIsDeterministic(t *testing.T) {
	specs := []domain.Spec{
		{
			Code: "US-001", Title: "Setup",
			Epic:     domain.Epic{Code: "EP-001", Title: "Foundations"},
			Priority: domain.PriorityHigh,
			Points:   3,
			Status:   domain.StatusTodo,
			Scope:    "MVP",
			Body:     "## Spec\n\nAs a user, I want X.\n",
		},
		{
			Code: "US-002", Title: "Auth",
			Epic:      domain.Epic{Code: "EP-001", Title: "Foundations"},
			Priority:  domain.PriorityMedium,
			Points:    5,
			Status:    domain.StatusTodo,
			BlockedBy: []string{"US-001"},
			Body:      "## Spec\n\nLogin.\n",
		},
	}
	a := renderBacklog(specs)
	b := renderBacklog(specs)
	if a != b {
		t.Fatalf("non-deterministic rendering")
	}
}

func TestRoundTripBacklog(t *testing.T) {
	specs := []domain.Spec{
		{
			Code: "US-001", Title: "Setup",
			Epic:     domain.Epic{Code: "EP-001", Title: "Foundations"},
			Priority: domain.PriorityHigh,
			Points:   3,
			Status:   domain.StatusTodo,
			Scope:    "MVP",
			Body:     "## Spec\n\nAs a user, I want X.",
		},
		{
			Code: "US-002", Title: "Auth",
			Epic:      domain.Epic{Code: "EP-001", Title: "Foundations"},
			Priority:  domain.PriorityMedium,
			Points:    5,
			Status:    domain.StatusTodo,
			BlockedBy: []string{"US-001"},
			Body:      "## Spec\n\nLogin.",
		},
	}
	rendered := renderBacklog(specs)
	parsed, err := parseBacklog(rendered)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed) != 2 {
		t.Fatalf("expected 2 specs, got %d", len(parsed))
	}
	for i, want := range specs {
		got := parsed[i]
		if got.Code != want.Code || got.Title != want.Title {
			t.Errorf("spec[%d] head: got %s/%q want %s/%q", i, got.Code, got.Title, want.Code, want.Title)
		}
		if got.Priority != want.Priority || got.Points != want.Points || got.Status != want.Status {
			t.Errorf("spec[%d] fields: got=%+v want=%+v", i, got, want)
		}
		if strings.TrimSpace(got.Body) != strings.TrimSpace(want.Body) {
			t.Errorf("spec[%d] body mismatch: got=%q want=%q", i, got.Body, want.Body)
		}
	}
	// Round-trip: render again should produce the same bytes.
	again := renderBacklog(parsed)
	if again != rendered {
		t.Errorf("round-trip not byte-stable\n--- first ---\n%s\n--- second ---\n%s", rendered, again)
	}
}

func TestParseBacklogMissingMarkerFails(t *testing.T) {
	content := "# Backlog\n\n#### US-001: Setup\n\nbody only, no marker\n"
	_, err := parseBacklog(content)
	if err == nil {
		t.Fatal("expected error for missing marker")
	}
}

func TestPlanRoundTrip(t *testing.T) {
	tasks := []domain.Task{
		{ID: "TASK-01", Title: "Schema DB", Description: "Create schema", Type: domain.TaskImpl, Status: domain.StatusTodo},
		{ID: "TASK-02", Title: "Test schema", Description: "Verify migration", Type: domain.TaskTest, Status: domain.StatusTodo, Dependencies: []string{"TASK-01"}},
	}
	plan := domain.PlanInput{
		PlanBody: "## Soluzione Tecnica\n\nSpiegazione.",
		Tasks:    tasks,
	}
	rendered := renderPlan("US-001", plan)
	body, parsedTasks, err := parsePlan(rendered)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "Soluzione Tecnica") {
		t.Errorf("plan body lost: %q", body)
	}
	if len(parsedTasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(parsedTasks))
	}
	if parsedTasks[1].ID != "TASK-02" || len(parsedTasks[1].Dependencies) != 1 || parsedTasks[1].Dependencies[0] != "TASK-01" {
		t.Errorf("dependency lost: %+v", parsedTasks[1])
	}
	again := renderPlan("US-001", domain.PlanInput{PlanBody: body, Tasks: parsedTasks})
	if again != rendered {
		t.Errorf("plan round-trip not byte-stable")
	}
}

func TestUpdateSpec(t *testing.T) {
	c := newTestConnector(t)
	ctx := context.Background()
	_, err := c.SaveInitialBacklog(ctx, []domain.Spec{{
		Code:     "US-001",
		Title:    "Setup",
		Epic:     domain.Epic{Code: "EP-001", Title: "Foundations"},
		Priority: domain.PriorityMedium,
		Points:   3,
		Status:   domain.StatusTodo,
		Scope:    "MVP",
		Body:     "## Spec\n\nOriginal.",
	}})
	if err != nil {
		t.Fatal(err)
	}
	newTitle := "Setup project"
	newPriority := domain.PriorityHigh
	newPoints := 5
	newBody := "## Spec\n\nUpdated."
	patch := domain.SpecUpdate{
		Title:    &newTitle,
		Priority: &newPriority,
		Points:   &newPoints,
		Body:     &newBody,
	}
	if _, err := c.UpdateSpec(ctx, "US-001", patch); err != nil {
		t.Fatal(err)
	}
	got, err := c.ReadSpecDetail(ctx, "US-001")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != newTitle {
		t.Errorf("title not updated: %q", got.Title)
	}
	if got.Priority != newPriority {
		t.Errorf("priority not updated: %q", got.Priority)
	}
	if got.Points != newPoints {
		t.Errorf("points not updated: %d", got.Points)
	}
	if got.Body != newBody {
		t.Errorf("body not updated: %q", got.Body)
	}
	// untouched fields preserved
	if got.Scope != "MVP" {
		t.Errorf("scope unexpectedly changed: %q", got.Scope)
	}
	if got.Epic.Code != "EP-001" {
		t.Errorf("epic unexpectedly changed: %+v", got.Epic)
	}
}

func TestUpdateSpecUnknownReturnsPrecondition(t *testing.T) {
	c := newTestConnector(t)
	_, err := c.SaveInitialBacklog(context.Background(), []domain.Spec{{
		Code: "US-001", Title: "Setup",
		Epic: domain.Epic{Code: "EP-001", Title: "F"}, Priority: domain.PriorityHigh, Points: 1, Status: domain.StatusTodo,
	}})
	if err != nil {
		t.Fatal(err)
	}
	title := "ghost"
	_, err = c.UpdateSpec(context.Background(), "US-404", domain.SpecUpdate{Title: &title})
	if err == nil {
		t.Fatal("expected error for unknown spec")
	}
}

func TestSpecFilesStoreEpicWithCodeAndTitle(t *testing.T) {
	c := newTestConnector(t)
	_, err := c.SaveInitialBacklog(context.Background(), []domain.Spec{{
		Code:     "US-001",
		Title:    "Setup",
		Epic:     domain.Epic{Code: "EP-001", Title: "Foundations"},
		Priority: domain.PriorityHigh,
		Points:   3,
		Status:   domain.StatusTodo,
		Body:     "## Spec\n\nAs a user, I want X.",
	}})
	if err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Join(c.cfg.ProjectRoot, ".archetipo", "specs", "US-001.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, "code: EP-001") {
		t.Fatalf("expected epic code in spec file, got:\n%s", text)
	}
	if !strings.Contains(text, "title: Foundations") {
		t.Fatalf("expected epic title in spec file, got:\n%s", text)
	}

	store, err := c.loadStore()
	if err != nil {
		t.Fatal(err)
	}
	if got := store.Specs["US-001"].Epic.Title; got != "Foundations" {
		t.Fatalf("expected epic title preserved, got %q", got)
	}
}

func TestSpecFilesReadLegacyScalarEpic(t *testing.T) {
	c := newTestConnector(t)
	specsDir := filepath.Join(c.cfg.ProjectRoot, ".archetipo", "specs")
	if err := os.MkdirAll(specsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	backlog := "schema: archetipo/backlog/v2\nversion: 2\nepics:\n  - code: EP-001\n    title: Foundations\norders:\n  board: {}\n"
	if err := os.WriteFile(filepath.Join(c.cfg.ProjectRoot, ".archetipo", "backlog.yaml"), []byte(backlog), 0o644); err != nil {
		t.Fatal(err)
	}
	legacySpec := "schema: archetipo/spec/v2\ncode: US-001\ntitle: Setup\nepic: EP-001\npriority: HIGH\npoints: 3\nstatus: TODO\n"
	if err := os.WriteFile(filepath.Join(specsDir, "US-001.yaml"), []byte(legacySpec), 0o644); err != nil {
		t.Fatal(err)
	}

	store, err := c.loadStore()
	if err != nil {
		t.Fatal(err)
	}
	st, ok := store.Specs["US-001"]
	if !ok {
		t.Fatalf("spec US-001 not loaded; got %+v", store.Specs)
	}
	if st.Epic.Code != "EP-001" {
		t.Errorf("epic code lost from legacy scalar: %q", st.Epic.Code)
	}
	if st.Epic.Title != "Foundations" {
		t.Errorf("epic title fallback failed; got %q want %q", st.Epic.Title, "Foundations")
	}
}
