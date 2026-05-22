// Package conformance defines a behavioural test suite shared by every
// concrete connector. Each implementation must pass it identically; this
// is what guarantees that a skill written against the contract works the
// same regardless of whether the project uses the file or github backend.
//
// Concrete connector packages provide a Factory and call Run from a *_test.go
// file. The suite touches every method of the Connector interface in
// sequence, mirroring a realistic skill workflow:
//
//	init -> save_initial_backlog -> list -> select -> save_plan -> read_tasks ->
//	transition_status -> complete_task -> append_specs -> read_existing -> post_comment
package conformance

import (
	"context"
	"sort"
	"testing"

	"github.com/techreloaded-ar/ARchetipo/cli/internal/connector"
	"github.com/techreloaded-ar/ARchetipo/cli/internal/domain"
)

// Factory builds a fresh connector for one sub-test. Implementations are
// expected to isolate state (filefs uses a temp dir, inmemory uses a fresh
// instance, etc.).
type Factory func(t *testing.T) connector.Connector

// Run executes the full suite against newConn.
func Run(t *testing.T, newConn Factory) {
	t.Helper()
	t.Run("InitializeConnector", func(t *testing.T) { testInitialize(t, newConn(t)) })
	t.Run("BacklogLifecycle", func(t *testing.T) { testBacklogLifecycle(t, newConn(t)) })
	t.Run("PlanLifecycle", func(t *testing.T) { testPlanLifecycle(t, newConn(t)) })
	t.Run("AppendSpecs", func(t *testing.T) { testAppendSpecs(t, newConn(t)) })
	t.Run("PostCommentNoOpAllowed", func(t *testing.T) { testPostComment(t, newConn(t)) })
}

func testInitialize(t *testing.T, c connector.Connector) {
	info, err := c.InitializeConnector(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Connector == "" {
		t.Errorf("connector name not populated")
	}
	if info.Workflow.Statuses.Todo == "" {
		t.Errorf("workflow statuses not populated")
	}
}

func testBacklogLifecycle(t *testing.T, c connector.Connector) {
	ctx := context.Background()
	specs := sampleSpecs()
	if _, err := c.SaveInitialBacklog(ctx, specs); err != nil {
		t.Fatal(err)
	}
	all, err := c.FetchBacklogItems(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != len(specs) {
		t.Fatalf("expected %d specs, got %d", len(specs), len(all))
	}
	// Filter by status: only TODO are present at this stage.
	todos, err := c.FetchBacklogItems(ctx, domain.StatusTodo)
	if err != nil {
		t.Fatal(err)
	}
	if len(todos) != len(specs) {
		t.Errorf("expected all specs TODO, got %d", len(todos))
	}
	// Auto-select picks the highest priority (US-001 HIGH).
	selected, err := c.SelectSpec(ctx, domain.SelectQuery{
		EligibleStatuses: []domain.Status{domain.StatusTodo},
	})
	if err != nil {
		t.Fatal(err)
	}
	if selected.Code != "US-001" {
		t.Errorf("auto-select expected US-001, got %s", selected.Code)
	}
	// Targeted select.
	got, err := c.SelectSpec(ctx, domain.SelectQuery{SpecCode: "US-002"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Code != "US-002" {
		t.Errorf("expected US-002, got %s", got.Code)
	}
	// Detail.
	det, err := c.ReadSpecDetail(ctx, "US-001")
	if err != nil {
		t.Fatal(err)
	}
	if det.Title == "" {
		t.Errorf("detail title empty")
	}
	// Transition.
	if _, err := c.TransitionStatus(ctx, "US-001", domain.StatusPlanned); err != nil {
		t.Fatal(err)
	}
	planned, err := c.FetchBacklogItems(ctx, domain.StatusPlanned)
	if err != nil {
		t.Fatal(err)
	}
	if len(planned) != 1 || planned[0].Code != "US-001" {
		t.Errorf("expected US-001 PLANNED, got %+v", planned)
	}
}

func testPlanLifecycle(t *testing.T, c connector.Connector) {
	ctx := context.Background()
	if _, err := c.SaveInitialBacklog(ctx, sampleSpecs()); err != nil {
		t.Fatal(err)
	}
	plan := domain.PlanInput{
		PlanBody: "## Soluzione Tecnica\n\nSpiegazione.",
		Tasks: []domain.Task{
			{ID: "TASK-01", Title: "Schema", Description: "Create schema", Type: domain.TaskImpl, Status: domain.StatusTodo},
			{ID: "TASK-02", Title: "Test schema", Description: "Verify", Type: domain.TaskTest, Status: domain.StatusTodo, Dependencies: []string{"TASK-01"}},
		},
	}
	if _, err := c.SavePlan(ctx, "US-001", plan); err != nil {
		t.Fatal(err)
	}
	tasks, err := c.ReadSpecTasks(ctx, "US-001")
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(tasks))
	}
	if tasks[1].ID != "TASK-02" || len(tasks[1].Dependencies) != 1 {
		t.Errorf("dependency lost: %+v", tasks[1])
	}
	if _, err := c.CompleteTask(ctx, "US-001", "TASK-01"); err != nil {
		t.Fatal(err)
	}
	tasks, _ = c.ReadSpecTasks(ctx, "US-001")
	if tasks[0].Status != domain.StatusDone {
		t.Errorf("expected TASK-01 DONE, got %s", tasks[0].Status)
	}
}

func testAppendSpecs(t *testing.T, c connector.Connector) {
	ctx := context.Background()
	if _, err := c.SaveInitialBacklog(ctx, sampleSpecs()); err != nil {
		t.Fatal(err)
	}
	extra := []domain.Spec{{
		Code: "US-100", Title: "New",
		Epic: domain.Epic{Code: "EP-002", Title: "Other"}, Priority: domain.PriorityLow, Points: 1, Status: domain.StatusTodo,
		Body: "## Spec\n\nLater.",
	}}
	if _, err := c.AppendSpecs(ctx, extra); err != nil {
		t.Fatal(err)
	}
	all, _ := c.FetchBacklogItems(ctx, "")
	codes := specCodes(all)
	sort.Strings(codes)
	if !contains(codes, "US-100") {
		t.Errorf("US-100 not appended: %v", codes)
	}
	sum, err := c.ReadExistingBacklog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(sum.Codes, "US-100") {
		t.Errorf("summary missing US-100: %v", sum.Codes)
	}
	if sum.LastCode != "US-100" {
		t.Errorf("last_code expected US-100, got %s", sum.LastCode)
	}
}

func testPostComment(t *testing.T, c connector.Connector) {
	ctx := context.Background()
	if _, err := c.SaveInitialBacklog(ctx, sampleSpecs()); err != nil {
		t.Fatal(err)
	}
	res, err := c.PostComment(ctx, "US-001", "smoke")
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Errorf("post_comment must return ok=true even when the connector is no-op")
	}
}

// helpers

func sampleSpecs() []domain.Spec {
	return []domain.Spec{
		{
			Code: "US-001", Title: "Setup",
			Epic: domain.Epic{Code: "EP-001", Title: "Foundations"}, Priority: domain.PriorityHigh, Points: 3, Status: domain.StatusTodo, Scope: "MVP",
			Body: "## Spec\n\nAs a user, I want X.",
		},
		{
			Code: "US-002", Title: "Auth",
			Epic: domain.Epic{Code: "EP-001", Title: "Foundations"}, Priority: domain.PriorityMedium, Points: 5, Status: domain.StatusTodo, BlockedBy: []string{"US-001"},
			Body: "## Spec\n\nLogin.",
		},
	}
}

func specCodes(s []domain.Spec) []string {
	out := make([]string, len(s))
	for i, x := range s {
		out[i] = x.Code
	}
	return out
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
