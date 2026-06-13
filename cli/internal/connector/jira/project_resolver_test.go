package jira

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/techreloaded-ar/ARchetipo/cli/internal/config"
	"github.com/techreloaded-ar/ARchetipo/cli/internal/domain"
	"github.com/techreloaded-ar/ARchetipo/cli/internal/iox"
)

// newRootedConnector builds a connector whose config is Loaded from a real
// temp directory, so ProjectRoot is set and Config.Save() writes the file.
// The jira section deliberately omits project_key to exercise auto-detection.
func newRootedConnector(t *testing.T, extraJiraYAML string) (*Connector, *fakeJira, string) {
	t.Helper()
	t.Setenv("JIRA_EMAIL", "bot@acme.com")
	t.Setenv("JIRA_API_TOKEN", "tok")
	// t.TempDir() ends in an all-digits component ("001") from which no Jira
	// key can be derived; nest a realistically named project directory.
	root := filepath.Join(t.TempDir(), "myapp")
	if err := os.MkdirAll(filepath.Join(root, ".archetipo"), 0o755); err != nil {
		t.Fatal(err)
	}
	raw := "connector: jira\njira:\n  base_url: https://acme.atlassian.net\n" + extraJiraYAML
	if err := os.WriteFile(filepath.Join(root, config.RelativePath), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	f := newFakeJira(t)
	return NewWithDoer(cfg, f), f, root
}

func savedConfig(t *testing.T, root string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, config.RelativePath))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestInitialize_ConfiguredKeySkipsDiscovery(t *testing.T) {
	c, f := newTestConnector(t)
	if _, err := c.InitializeConnector(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, call := range f.calls {
		if strings.Contains(call, "/project/search") || call == "POST /rest/api/3/project" {
			t.Fatalf("configured project_key must skip discovery, calls: %v", f.calls)
		}
	}
	if !f.called("GET /rest/api/3/project/ARCH/statuses") {
		t.Fatalf("init should validate the project via /statuses, calls: %v", f.calls)
	}
}

func TestInitialize_FindsProjectByName(t *testing.T) {
	c, f, root := newRootedConnector(t, "")
	f.projects = []fakeProject{
		{Key: "OTHER", Name: "Something Else"},
		{Key: "MYAPP", Name: filepath.Base(root)},
	}
	if _, err := c.InitializeConnector(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.projectCreates) != 0 {
		t.Fatalf("matching project exists, nothing should be created: %v", f.projectCreates)
	}
	// Both config copies must see the resolved key: c.jira drives the REST
	// calls, c.cfg.Jira drives Save().
	if c.jira.ProjectKey != "MYAPP" || c.cfg.Jira.ProjectKey != "MYAPP" {
		t.Fatalf("project key not propagated: jira=%q cfg=%q", c.jira.ProjectKey, c.cfg.Jira.ProjectKey)
	}
	if s := savedConfig(t, root); !strings.Contains(s, "project_key: MYAPP") {
		t.Errorf("project_key not persisted:\n%s", s)
	}
}

func TestInitialize_CreatesProject(t *testing.T) {
	c, f, root := newRootedConnector(t, "")
	if _, err := c.InitializeConnector(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.projectCreates) != 1 {
		t.Fatalf("expected exactly one create, got %d", len(f.projectCreates))
	}
	name := filepath.Base(root)
	wantKey, err := deriveProjectKey(name)
	if err != nil {
		t.Fatal(err)
	}
	created := f.projectCreates[0]
	if created["key"] != wantKey || created["name"] != name {
		t.Errorf("create payload key/name: %v", created)
	}
	if created["projectTypeKey"] != "software" || created["leadAccountId"] != "acc-1" {
		t.Errorf("create payload type/lead: %v", created)
	}
	if s := savedConfig(t, root); !strings.Contains(s, "project_key: "+wantKey) {
		t.Errorf("created project_key not persisted:\n%s", s)
	}
}

func TestInitialize_CreatePermissionDenied(t *testing.T) {
	c, f, _ := newRootedConnector(t, "")
	f.createProjectStatus = 403
	_, err := c.InitializeConnector(context.Background())
	if err == nil {
		t.Fatal("expected error when project creation is forbidden")
	}
	var ce *iox.CodedError
	if !errors.As(err, &ce) {
		t.Fatalf("expected *iox.CodedError, got %T", err)
	}
	if !strings.Contains(ce.Hint, "Administer Jira") || !strings.Contains(ce.Hint, "jira.project_key") {
		t.Errorf("hint should point at permissions and manual project_key: %q", ce.Hint)
	}
}

func TestInitialize_KeyCollisionRetries(t *testing.T) {
	c, f, root := newRootedConnector(t, "")
	f.projectKeyCollisions = 1
	if _, err := c.InitializeConnector(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.projectCreates) != 2 {
		t.Fatalf("expected a retry after key collision, got %d creates", len(f.projectCreates))
	}
	base, err := deriveProjectKey(filepath.Base(root))
	if err != nil {
		t.Fatal(err)
	}
	want := keyVariant(base, 2)
	if f.projectCreates[1]["key"] != want {
		t.Errorf("retry key = %v, want %s", f.projectCreates[1]["key"], want)
	}
	if c.jira.ProjectKey != want {
		t.Errorf("resolved key = %q, want %s", c.jira.ProjectKey, want)
	}
}

func TestStatusMap_AutoDiscovery(t *testing.T) {
	c, f, root := newRootedConnector(t, "  project_key: ARCH\n")
	f.projectStatuses = []map[string]any{
		{"name": "Story", "subtask": false, "statuses": []map[string]any{
			{"name": "To Do"}, {"name": "Selected for Development"},
			{"name": "In Progress"}, {"name": "In Review"}, {"name": "Done"},
		}},
	}
	if _, err := c.InitializeConnector(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"TODO":        "To Do",
		"PLANNED":     "Selected for Development",
		"IN PROGRESS": "In Progress",
		"REVIEW":      "In Review",
		"DONE":        "Done",
	}
	for canonical, jiraName := range want {
		if got := c.jiraStatus(domain.Status(canonical)); got != jiraName {
			t.Errorf("status %s -> %q, want %q", canonical, got, jiraName)
		}
	}
	s := savedConfig(t, root)
	for _, line := range []string{"status_map:", "TODO: To Do", "PLANNED: Selected for Development"} {
		if !strings.Contains(s, line) {
			t.Errorf("missing %q in persisted status_map:\n%s", line, s)
		}
	}
}

func TestStatusMap_MatchesUntranslatedNames(t *testing.T) {
	// A non-English account sees the Jira default statuses translated ("In
	// revisione" for "In review"); the API pairs every translated name with
	// untranslatedName. Matching must go through the untranslated name and
	// resolve to the translated one, which is what the issue-facing endpoints
	// (transitions) speak — and nothing must be provisioned, the statuses are
	// all there.
	c, f, _ := newRootedConnector(t, "  project_key: ARCH\n")
	f.projectStatuses = []map[string]any{
		{"name": "Story", "subtask": false, "statuses": []map[string]any{
			{"name": "Backlog", "untranslatedName": "Backlog"},
			{"name": "Selected for Development", "untranslatedName": "Selected for Development"},
			{"name": "In corso", "untranslatedName": "In Progress"},
			{"name": "In revisione", "untranslatedName": "In review"},
			{"name": "Completata", "untranslatedName": "Done"},
		}},
	}
	if _, err := c.InitializeConnector(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"TODO":        "Backlog",
		"PLANNED":     "Selected for Development",
		"IN PROGRESS": "In corso",
		"REVIEW":      "In revisione",
		"DONE":        "Completata",
	}
	for canonical, jiraName := range want {
		if got := c.jiraStatus(domain.Status(canonical)); got != jiraName {
			t.Errorf("status %s -> %q, want %q", canonical, got, jiraName)
		}
	}
	if len(f.statusCreates) != 0 || len(f.workflowUpdates) != 0 {
		t.Errorf("all statuses exist, nothing should be provisioned: %v %v", f.statusCreates, f.workflowUpdates)
	}
}

func TestStatusMap_UserEntryMatchesUntranslatedName(t *testing.T) {
	// A status_map entry written with the untranslated name must be accepted
	// and resolved to the translated one.
	c, f, _ := newRootedConnector(t, "  project_key: ARCH\n  status_map:\n    REVIEW: In review\n")
	f.projectStatuses = []map[string]any{
		{"name": "Story", "subtask": false, "statuses": []map[string]any{
			{"name": "Backlog", "untranslatedName": "Backlog"},
			{"name": "Selected for Development", "untranslatedName": "Selected for Development"},
			{"name": "In corso", "untranslatedName": "In Progress"},
			{"name": "In revisione", "untranslatedName": "In review"},
			{"name": "Completata", "untranslatedName": "Done"},
		}},
	}
	if _, err := c.InitializeConnector(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := c.jiraStatus(domain.StatusReview); got != "In revisione" {
		t.Errorf("REVIEW -> %q, want the translated In revisione", got)
	}
}

func TestStatusMap_RespectsUserEntries(t *testing.T) {
	// REVIEW is mapped by the user to a status that only exists on the
	// sub-task workflow: it must be accepted (validated against the union of
	// issue types), while the other statuses are auto-matched.
	c, f, _ := newRootedConnector(t, "  project_key: ARCH\n  status_map:\n    REVIEW: QA Check\n")
	f.projectStatuses = []map[string]any{
		{"name": "Story", "subtask": false, "statuses": []map[string]any{
			{"name": "To Do"}, {"name": "Planned"}, {"name": "In Progress"}, {"name": "Done"},
		}},
		{"name": "Sub-task", "subtask": true, "statuses": []map[string]any{
			{"name": "QA Check"},
		}},
	}
	if _, err := c.InitializeConnector(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := c.jiraStatus(domain.StatusReview); got != "QA Check" {
		t.Errorf("REVIEW -> %q, want user-provided QA Check", got)
	}
}

func TestStatusMap_RejectsUnknownUserEntry(t *testing.T) {
	c, _, _ := newRootedConnector(t, "  project_key: ARCH\n  status_map:\n    REVIEW: Nonexistent\n")
	_, err := c.InitializeConnector(context.Background())
	if err == nil || !strings.Contains(err.Error(), "Nonexistent") {
		t.Fatalf("expected error naming the bogus status, got %v", err)
	}
}

// freshKanbanStatuses is what a project created from Jira's standard Kanban
// template exposes: no REVIEW-like status.
func freshKanbanStatuses() []map[string]any {
	return []map[string]any{
		{"name": "Story", "subtask": false, "statuses": []map[string]any{
			{"name": "Backlog"}, {"name": "Selected for Development"},
			{"name": "In Progress"}, {"name": "Done"},
		}},
	}
}

func TestStatusMap_ProvisionsMissingStatuses(t *testing.T) {
	c, f, root := newRootedConnector(t, "")
	f.projectStatuses = freshKanbanStatuses()
	if _, err := c.InitializeConnector(context.Background()); err != nil {
		t.Fatal(err)
	}
	// REVIEW had no match: a global "In review" status must be created and
	// wired into the workflow, then picked up by the re-match.
	if len(f.statusCreates) != 1 || f.statusCreates[0]["name"] != "In review" {
		t.Fatalf("expected exactly one created status (In review), got %v", f.statusCreates)
	}
	if len(f.workflowUpdates) != 1 {
		t.Fatalf("expected exactly one workflow update, got %d", len(f.workflowUpdates))
	}
	if got := c.jiraStatus(domain.StatusReview); got != "In review" {
		t.Errorf("REVIEW -> %q, want In review", got)
	}
	// The other canonical statuses keep their template matches.
	if got := c.jiraStatus(domain.StatusTodo); got != "Backlog" {
		t.Errorf("TODO -> %q, want Backlog", got)
	}
	if s := savedConfig(t, root); !strings.Contains(s, "REVIEW: In review") {
		t.Errorf("provisioned REVIEW mapping not persisted:\n%s", s)
	}
}

func TestStatusMap_ProvisionEchoesExistingTransitions(t *testing.T) {
	c, f, _ := newRootedConnector(t, "")
	f.projectStatuses = freshKanbanStatuses()
	if _, err := c.InitializeConnector(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The bulk update replaces transitions wholesale: the 4 template
	// transitions (with their actions) must be echoed back alongside the new
	// global transition for the provisioned status.
	wf := f.workflowUpdates[0]["workflows"].([]any)[0].(map[string]any)
	transitions := wf["transitions"].([]any)
	if len(transitions) != 5 {
		t.Fatalf("expected 4 echoed + 1 new transitions, got %d", len(transitions))
	}
	first := transitions[0].(map[string]any)
	if _, ok := first["actions"]; !ok {
		t.Errorf("existing transition lost its actions in the round trip: %v", first)
	}
	if wf["version"] == nil {
		t.Errorf("workflow update must echo the version for optimistic locking")
	}
}

func TestStatusMap_ProvisionReusesExistingGlobalStatus(t *testing.T) {
	c, f, _ := newRootedConnector(t, "")
	f.projectStatuses = freshKanbanStatuses()
	f.globalStatuses = []map[string]any{
		{"id": "10042", "name": "In Review", "statusCategory": "IN_PROGRESS"},
	}
	if _, err := c.InitializeConnector(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.statusCreates) != 0 {
		t.Fatalf("global status exists, none should be created: %v", f.statusCreates)
	}
	if got := c.jiraStatus(domain.StatusReview); got != "In Review" {
		t.Errorf("REVIEW -> %q, want the reused In Review", got)
	}
}

func TestStatusMap_ProvisionPermissionDeniedFallsBack(t *testing.T) {
	c, f, root := newRootedConnector(t, "")
	f.projectStatuses = freshKanbanStatuses()
	f.provisionStatus = 403
	_, err := c.InitializeConnector(context.Background())
	if err == nil || !strings.Contains(err.Error(), "REVIEW") {
		t.Fatalf("expected unmatched REVIEW error when provisioning is forbidden, got %v", err)
	}
	var ce *iox.CodedError
	if !errors.As(err, &ce) || ce.Code != iox.CodePreconditionMissing {
		t.Fatalf("expected precondition error, got %v", err)
	}
	if !strings.Contains(ce.Hint, "jira.status_map") {
		t.Errorf("hint should keep pointing at the manual fix: %q", ce.Hint)
	}
	// The auto-created project key must already be persisted so the user can
	// fix the Jira workflow and re-run without a second create.
	if s := savedConfig(t, root); !strings.Contains(s, "project_key:") {
		t.Errorf("project_key should be persisted before status resolution fails:\n%s", s)
	}
}

func TestInitialize_IdempotentSecondRun(t *testing.T) {
	c, _, root := newRootedConnector(t, "")
	if _, err := c.InitializeConnector(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := savedConfig(t, root)

	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	f2 := newFakeJira(t)
	c2 := NewWithDoer(cfg, f2)
	if _, err := c2.InitializeConnector(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, call := range f2.calls {
		if strings.Contains(call, "/project/search") || call == "POST /rest/api/3/project" {
			t.Fatalf("second run must reuse the persisted key, calls: %v", f2.calls)
		}
	}
	if second := savedConfig(t, root); second != first {
		t.Errorf("config rewritten on idempotent re-run:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
}

func TestDeriveProjectKey(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
		err  bool
	}{
		{"simple", "archetipo", "ARCHETIPO", false},
		{"hyphenated", "my-cool-app", "MYCOOLAPP", false},
		{"leading digits stripped", "123abc", "ABC", false},
		{"digits kept after letter", "app2x", "APP2X", false},
		{"truncated to 10", "averyverylongprojectname", "AVERYVERYL", false},
		{"symbols only", "@@@", "", true},
		{"digits only", "2024", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := deriveProjectKey(tc.in)
			if tc.err {
				if err == nil {
					t.Fatalf("expected error for %q, got %q", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("deriveProjectKey(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
