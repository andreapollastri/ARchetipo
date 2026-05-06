package github

import (
	"context"
	"strings"
	"testing"

	"github.com/techreloaded-ar/ARchetipo/cli/internal/config"
)

// TestInitializeConnector_HappyPath verifies the gh call sequence used by
// initialize_connector resolves repo + project metadata into a SetupInfo.
func TestInitializeConnector_HappyPath(t *testing.T) {
	m := newMock(t).
		on("repo view --json", `{
			"id":"R_abc","owner":{"login":"acme"},"name":"web","nameWithOwner":"acme/web"
		}`).
		on("project list --owner acme", `{
			"projects":[{"number":4,"id":"PVT_kw","title":"web Backlog","url":"https://gh/p/4"}]
		}`).
		on("project field-list 4", `{
			"fields":[
				{"id":"FID_status","name":"Status","type":"SINGLE_SELECT","options":[
					{"id":"OPT_todo","name":"TODO"},{"id":"OPT_planned","name":"PLANNED"}
				]},
				{"id":"FID_pri","name":"Priority","type":"SINGLE_SELECT","options":[
					{"id":"OPT_high","name":"HIGH"}
				]},
				{"id":"FID_sp","name":"Story Points","type":"NUMBER"}
			]
		}`)

	cfg := config.Default()
	cfg.Connector = config.ConnectorGitHub
	c := NewWithRunner(cfg, m)

	info, err := c.InitializeConnector(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Repo == nil || info.Repo.Slug != "acme/web" {
		t.Errorf("repo not resolved: %+v", info.Repo)
	}
	if info.Project == nil || info.Project.Number != 4 {
		t.Errorf("project not resolved: %+v", info.Project)
	}
	if info.Project.Fields.StatusOptions["TODO"] != "OPT_todo" {
		t.Errorf("status option lost: %+v", info.Project.Fields.StatusOptions)
	}
	if !m.calledWithPrefix("repo view") {
		t.Errorf("expected gh repo view to be called")
	}
}

// TestProjectPreference_NoExactMatchCreatesNew is the regression test for the
// Artly/Tela bug: when the owner already has projects whose titles contain
// "Backlog" (e.g. "Tela Backlog") but none matches the current repo exactly
// ("Artly Backlog"), the resolver must NOT reuse the unrelated board — it
// must create a fresh one for the current repo.
func TestProjectPreference_NoExactMatchCreatesNew(t *testing.T) {
	m := newMock(t).
		on("repo view --json", `{"id":"R","owner":{"login":"sleli"},"name":"Artly","nameWithOwner":"sleli/Artly"}`).
		on("project list --owner sleli", `{"projects":[
			{"number":11,"id":"PVT11","title":"Tela Backlog","url":""},
			{"number":7,"id":"PVT7","title":"FoodCost Backlog","url":""}
		]}`).
		on("project create --owner sleli --title Artly Backlog --format json",
			`{"number":12,"id":"PVT12","url":"https://gh/p/12"}`).
		on("project field-create 12 --owner sleli --name Priority", "ok").
		on("project field-create 12 --owner sleli --name Story Points", "ok").
		on("project field-list 12", `{"fields":[
			{"id":"FID_status","name":"Status","type":"SINGLE_SELECT","options":[]}
		]}`).
		on("api graphql", `{"data":{"updateProjectV2Field":{"projectV2Field":{"id":"FID_status"}}}}`)

	cfg := config.Default()
	cfg.Connector = config.ConnectorGitHub
	c := NewWithRunner(cfg, m)

	info, err := c.InitializeConnector(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Project.Number != 12 {
		t.Errorf("expected freshly created project 12, got %d (cross-repo reuse regression)", info.Project.Number)
	}
	if m.calledWithPrefix("project field-list 11") {
		t.Errorf("must NOT load fields of unrelated project 11 (Tela Backlog)")
	}
}

// TestResolveBoard_ConfigPinsProjectNumber verifies that when the config
// already carries owner + project_number the resolver picks that exact
// project, regardless of title-based preferences.
func TestResolveBoard_ConfigPinsProjectNumber(t *testing.T) {
	m := newMock(t).
		on("repo view --json", `{"id":"R","owner":{"login":"acme"},"name":"web","nameWithOwner":"acme/web"}`).
		on("project list --owner acme", `{"projects":[
			{"number":4,"id":"PVT4","title":"web Backlog","url":""},
			{"number":9,"id":"PVT9","title":"Other","url":""}
		]}`).
		on("project field-list 9", `{"fields":[]}`)

	cfg := config.Default()
	cfg.Connector = config.ConnectorGitHub
	cfg.GitHub.Owner = "acme"
	cfg.GitHub.ProjectNumber = 9
	c := NewWithRunner(cfg, m)

	info, err := c.InitializeConnector(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Project.Number != 9 {
		t.Errorf("expected pinned project 9, got %d", info.Project.Number)
	}
}

// TestResolveBoard_StaleConfigNumberFails verifies that an outdated
// project_number in the config produces an explicit precondition error and
// does NOT silently fall back to title-based discovery.
func TestResolveBoard_StaleConfigNumberFails(t *testing.T) {
	m := newMock(t).
		on("repo view --json", `{"id":"R","owner":{"login":"acme"},"name":"web","nameWithOwner":"acme/web"}`).
		on("project list --owner acme", `{"projects":[
			{"number":4,"id":"PVT4","title":"web Backlog","url":""}
		]}`)

	cfg := config.Default()
	cfg.Connector = config.ConnectorGitHub
	cfg.GitHub.Owner = "acme"
	cfg.GitHub.ProjectNumber = 99 // not present
	c := NewWithRunner(cfg, m)

	_, err := c.InitializeConnector(context.Background())
	if err == nil {
		t.Fatal("expected precondition error for stale project_number")
	}
	if !strings.Contains(err.Error(), "project_number 99") {
		t.Errorf("error message should mention stale number, got: %v", err)
	}
	if m.calledWithPrefix("project field-list 4") {
		t.Errorf("must NOT fall back to title pipeline when config is stale")
	}
}

// TestResolveBoard_NoProjectTriggersCreate verifies that when the user has
// no project at all the resolver creates one, configures the canonical
// custom fields, and aligns the Status options to workflow.statuses.
func TestResolveBoard_NoProjectTriggersCreate(t *testing.T) {
	m := newMock(t).
		on("repo view --json", `{"id":"R","owner":{"login":"acme"},"name":"web","nameWithOwner":"acme/web"}`).
		on("project list --owner acme", `{"projects":[]}`).
		on("project create --owner acme --title web Backlog --format json",
			`{"number":11,"id":"PVT11","url":"https://gh/p/11"}`).
		on("project field-create 11 --owner acme --name Priority", "ok").
		on("project field-create 11 --owner acme --name Story Points", "ok").
		on("project field-list 11", `{"fields":[
			{"id":"FID_status","name":"Status","type":"SINGLE_SELECT","options":[]}
		]}`).
		on("api graphql", `{"data":{"updateProjectV2Field":{"projectV2Field":{"id":"FID_status"}}}}`)

	cfg := config.Default()
	cfg.Connector = config.ConnectorGitHub
	c := NewWithRunner(cfg, m)

	info, err := c.InitializeConnector(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Project.Number != 11 {
		t.Errorf("expected created project 11, got %d", info.Project.Number)
	}
	for _, want := range []string{
		"project create --owner acme",
		"project field-create 11 --owner acme --name Priority",
		"project field-create 11 --owner acme --name Story Points",
		"api graphql",
	} {
		if !m.calledWithPrefix(want) {
			t.Errorf("expected gh call with prefix %q", want)
		}
	}
}
