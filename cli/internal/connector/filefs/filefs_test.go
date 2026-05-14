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

func TestStoryMarkerRoundTrip(t *testing.T) {
	s := domain.Story{
		Code:        "US-007",
		Title:       "Login utente",
		Epic:        domain.Epic{Code: "EP-002", Title: "Auth Foundations"},
		Priority:    domain.PriorityHigh,
		StoryPoints: 5,
		Status:      domain.StatusPlanned,
		BlockedBy:   []string{"US-002", "US-003"},
		Scope:       "MVP",
	}
	line := storyMarker(s)
	mk, ok := parseMarker(line)
	if !ok {
		t.Fatalf("failed to parse generated marker: %s", line)
	}
	got, err := storyFromMarker(mk)
	if err != nil {
		t.Fatal(err)
	}
	got.Title = s.Title // marker doesn't carry title
	if got.Code != s.Code || got.Priority != s.Priority || got.StoryPoints != s.StoryPoints || got.Status != s.Status || got.Scope != s.Scope {
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
	stories := []domain.Story{
		{
			Code: "US-001", Title: "Setup",
			Epic:        domain.Epic{Code: "EP-001", Title: "Foundations"},
			Priority:    domain.PriorityHigh,
			StoryPoints: 3,
			Status:      domain.StatusTodo,
			Scope:       "MVP",
			Body:        "## Story\n\nAs a user, I want X.\n",
		},
		{
			Code: "US-002", Title: "Auth",
			Epic:        domain.Epic{Code: "EP-001", Title: "Foundations"},
			Priority:    domain.PriorityMedium,
			StoryPoints: 5,
			Status:      domain.StatusTodo,
			BlockedBy:   []string{"US-001"},
			Body:        "## Story\n\nLogin.\n",
		},
	}
	a := renderBacklog(stories)
	b := renderBacklog(stories)
	if a != b {
		t.Fatalf("non-deterministic rendering")
	}
}

func TestRoundTripBacklog(t *testing.T) {
	stories := []domain.Story{
		{
			Code: "US-001", Title: "Setup",
			Epic:        domain.Epic{Code: "EP-001", Title: "Foundations"},
			Priority:    domain.PriorityHigh,
			StoryPoints: 3,
			Status:      domain.StatusTodo,
			Scope:       "MVP",
			Body:        "## Story\n\nAs a user, I want X.",
		},
		{
			Code: "US-002", Title: "Auth",
			Epic:        domain.Epic{Code: "EP-001", Title: "Foundations"},
			Priority:    domain.PriorityMedium,
			StoryPoints: 5,
			Status:      domain.StatusTodo,
			BlockedBy:   []string{"US-001"},
			Body:        "## Story\n\nLogin.",
		},
	}
	rendered := renderBacklog(stories)
	parsed, err := parseBacklog(rendered)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed) != 2 {
		t.Fatalf("expected 2 stories, got %d", len(parsed))
	}
	for i, want := range stories {
		got := parsed[i]
		if got.Code != want.Code || got.Title != want.Title {
			t.Errorf("story[%d] head: got %s/%q want %s/%q", i, got.Code, got.Title, want.Code, want.Title)
		}
		if got.Priority != want.Priority || got.StoryPoints != want.StoryPoints || got.Status != want.Status {
			t.Errorf("story[%d] fields: got=%+v want=%+v", i, got, want)
		}
		if strings.TrimSpace(got.Body) != strings.TrimSpace(want.Body) {
			t.Errorf("story[%d] body mismatch: got=%q want=%q", i, got.Body, want.Body)
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

func TestStoryFilesStoreEpicAsCodeOnly(t *testing.T) {
	c := newTestConnector(t)
	_, err := c.SaveInitialBacklog(context.Background(), []domain.Story{{
		Code:        "US-001",
		Title:       "Setup",
		Epic:        domain.Epic{Code: "EP-001", Title: "Foundations"},
		Priority:    domain.PriorityHigh,
		StoryPoints: 3,
		Status:      domain.StatusTodo,
		Body:        "## Story\n\nAs a user, I want X.",
	}})
	if err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Join(c.cfg.ProjectRoot, ".archetipo", "stories", "US-001.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, "epic: EP-001\n") {
		t.Fatalf("expected scalar epic code in story file, got:\n%s", text)
	}
	if strings.Contains(text, "title: Foundations") {
		t.Fatalf("story file should not duplicate epic title, got:\n%s", text)
	}

	store, err := c.loadStore()
	if err != nil {
		t.Fatal(err)
	}
	if got := store.Stories["US-001"].Epic.Title; got != "Foundations" {
		t.Fatalf("expected epic title restored from backlog metadata, got %q", got)
	}
}
