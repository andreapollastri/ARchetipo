package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDefaultWhenConfigMissing(t *testing.T) {
	dir := t.TempDir()
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.Connector != ConnectorFile {
		t.Errorf("expected default connector %q, got %q", ConnectorFile, c.Connector)
	}
	if c.File.Backlog != ".archetipo/backlog.yaml" {
		t.Errorf("default backlog path: %q", c.File.Backlog)
	}
	if c.File.Planning != ".archetipo/plans/" {
		t.Errorf("default planning path: %q", c.File.Planning)
	}
	if c.Paths.PRD != "docs/PRD.md" {
		t.Errorf("default PRD path: %q", c.Paths.PRD)
	}
}

func TestLoadFromConfigFile(t *testing.T) {
	root := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(root, ".archetipo"), 0o755))
	must(t, os.WriteFile(filepath.Join(root, RelativePath), []byte(`connector: file
file:
  backlog: my/BL.yaml
workflow:
  statuses:
    todo: A_FARE
    planned: PIANIFICATO
    in_progress: IN CORSO
    review: REVISIONE
    done: FATTO
`), 0o644))

	c, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if c.Connector != ConnectorFile {
		t.Errorf("connector: %q", c.Connector)
	}
	if c.File.Backlog != "my/BL.yaml" {
		t.Errorf("backlog: %q", c.File.Backlog)
	}
	// Defaults preserved for unspecified keys.
	if c.Paths.PRD != "docs/PRD.md" {
		t.Errorf("PRD default lost: %q", c.Paths.PRD)
	}
	if c.File.Planning != ".archetipo/plans/" {
		t.Errorf("planning default lost: %q", c.File.Planning)
	}
	if c.Workflow.Statuses.Todo != "A_FARE" {
		t.Errorf("status override lost: %q", c.Workflow.Statuses.Todo)
	}
	if c.ProjectRoot != root {
		t.Errorf("project root: %q want %q", c.ProjectRoot, root)
	}
}

func TestLoadFromSubdirectoryWalksUp(t *testing.T) {
	root := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(root, ".archetipo"), 0o755))
	must(t, os.WriteFile(filepath.Join(root, RelativePath), []byte(`connector: file
`), 0o644))
	sub := filepath.Join(root, "src", "deep")
	must(t, os.MkdirAll(sub, 0o755))

	c, err := Load(sub)
	if err != nil {
		t.Fatal(err)
	}
	if c.ProjectRoot != root {
		t.Errorf("project root: %q want %q", c.ProjectRoot, root)
	}
}

func TestUnknownConnectorPassesThroughConfig(t *testing.T) {
	// Config intentionally does NOT validate connector names;
	// connector.New rejects unknown names with a dynamic list
	// of registered connectors. This avoids a circular import
	// (config → connector) and keeps config connector-agnostic.
	root := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(root, ".archetipo"), 0o755))
	must(t, os.WriteFile(filepath.Join(root, RelativePath), []byte(`connector: gitlab
`), 0o644))
	c, err := Load(root)
	if err != nil {
		t.Fatalf("config should load regardless of connector name: %v", err)
	}
	if c.Connector != "gitlab" {
		t.Errorf("expected connector 'gitlab', got %q", c.Connector)
	}
}

func TestLegacyPathsBacklogIsRejected(t *testing.T) {
	root := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(root, ".archetipo"), 0o755))
	must(t, os.WriteFile(filepath.Join(root, RelativePath), []byte(`connector: file
paths:
  backlog: .archetipo/backlog.yaml
`), 0o644))

	_, err := Load(root)
	if err == nil {
		t.Fatal("expected error for legacy paths.backlog key")
	}
	msg := err.Error()
	if !strings.Contains(msg, "paths.backlog -> file.backlog") {
		t.Errorf("error should mention migration path; got: %v", err)
	}
}

func TestLegacyPathsPlanningIsRejected(t *testing.T) {
	root := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(root, ".archetipo"), 0o755))
	must(t, os.WriteFile(filepath.Join(root, RelativePath), []byte(`connector: file
paths:
  planning: .archetipo/plans/
`), 0o644))

	_, err := Load(root)
	if err == nil {
		t.Fatal("expected error for legacy paths.planning key")
	}
	if !strings.Contains(err.Error(), "paths.planning -> file.planning") {
		t.Errorf("error should mention migration path; got: %v", err)
	}
}

func TestPathValidationRejectsUnwritableSharedPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod-based unwritable check not portable on Windows")
	}
	if os.Getuid() == 0 {
		t.Skip("running as root bypasses directory permission checks")
	}
	root := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(root, ".archetipo"), 0o755))
	readonly := filepath.Join(root, "readonly")
	must(t, os.MkdirAll(readonly, 0o755))
	must(t, os.Chmod(readonly, 0o555))
	defer func() { _ = os.Chmod(readonly, 0o755) }()

	must(t, os.WriteFile(filepath.Join(root, RelativePath), []byte(`connector: github
paths:
  mockups: readonly/inside/
`), 0o644))

	_, err := Load(root)
	if err == nil {
		t.Fatal("expected error for unwritable paths.mockups")
	}
	if !strings.Contains(err.Error(), "paths.mockups") {
		t.Errorf("error should mention paths.mockups; got: %v", err)
	}
}

func TestPathValidationSkipsFilePathsForGitHubConnector(t *testing.T) {
	root := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(root, ".archetipo"), 0o755))
	// file.planning points to a non-existent unrelated absolute path. The
	// github connector should not validate file.* paths.
	must(t, os.WriteFile(filepath.Join(root, RelativePath), []byte(`connector: github
file:
  planning: /nonexistent/never/touched/by/github/
`), 0o644))

	if _, err := Load(root); err != nil {
		t.Fatalf("github connector should not validate file.* paths: %v", err)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestSave_PatchesGitHubKeysPreservingComments(t *testing.T) {
	root := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(root, ".archetipo"), 0o755))
	initial := `connector: github
paths:
  prd: docs/PRD.md

github:
  # auto-detected on first run
  owner: ""
`
	must(t, os.WriteFile(filepath.Join(root, RelativePath), []byte(initial), 0o644))

	c, err := Load(root)
	must(t, err)
	c.GitHub.Owner = "acme"
	c.GitHub.ProjectNumber = 42
	must(t, c.Save())

	out, err := os.ReadFile(filepath.Join(root, RelativePath))
	must(t, err)
	s := string(out)
	for _, want := range []string{
		"# auto-detected on first run",
		"owner: acme",
		"project_number: 42",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in saved file:\n%s", want, s)
		}
	}
}

func TestSave_AddsGitHubSectionWhenMissing(t *testing.T) {
	root := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(root, ".archetipo"), 0o755))
	initial := `connector: github
paths:
  prd: docs/PRD.md
`
	must(t, os.WriteFile(filepath.Join(root, RelativePath), []byte(initial), 0o644))

	c, err := Load(root)
	must(t, err)
	c.GitHub.Owner = "x"
	c.GitHub.ProjectNumber = 7
	must(t, c.Save())

	raw, err := os.ReadFile(filepath.Join(root, RelativePath))
	must(t, err)
	s := string(raw)
	for _, want := range []string{"github:", "owner: x", "project_number: 7"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in saved file:\n%s", want, s)
		}
	}
}

func TestSave_ReusesEmptyGitHubSectionFromTemplate(t *testing.T) {
	root := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(root, ".archetipo"), 0o755))
	initial := `connector: github
paths:
  prd: docs/PRD.md
#only valid for github connector
github:

# owner: auto-detected from repo
# project_number: auto-detected from repo
`
	must(t, os.WriteFile(filepath.Join(root, RelativePath), []byte(initial), 0o644))

	c, err := Load(root)
	must(t, err)
	c.GitHub.Owner = "sleli"
	c.GitHub.ProjectNumber = 23
	must(t, c.Save())

	raw, err := os.ReadFile(filepath.Join(root, RelativePath))
	must(t, err)
	s := string(raw)
	if strings.Count(s, "\ngithub:") != 1 {
		t.Fatalf("expected a single github section, got:\n%s", s)
	}
	for _, want := range []string{"owner: sleli", "project_number: 23"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in saved file:\n%s", want, s)
		}
	}
}

func TestSave_NoOpWhenProjectRootEmpty(t *testing.T) {
	c := Default()
	c.GitHub.Owner = "x"
	c.GitHub.ProjectNumber = 1
	if err := c.Save(); err != nil {
		t.Fatalf("Save with empty ProjectRoot should be a no-op, got %v", err)
	}
}

func TestSave_CreatesFileWhenMissing(t *testing.T) {
	root := t.TempDir()
	c := Default()
	c.ProjectRoot = root
	c.Connector = ConnectorGitHub
	c.GitHub.Owner = "y"
	c.GitHub.ProjectNumber = 1
	must(t, c.Save())

	raw, err := os.ReadFile(filepath.Join(root, RelativePath))
	must(t, err)
	s := string(raw)
	if !strings.Contains(s, "owner: y") || !strings.Contains(s, "project_number: 1") {
		t.Errorf("fresh config missing github keys:\n%s", s)
	}
}

func TestLoad_JiraProjectKeyOptional(t *testing.T) {
	root := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(root, ".archetipo"), 0o755))
	must(t, os.WriteFile(filepath.Join(root, RelativePath), []byte(`connector: jira
jira:
  base_url: https://acme.atlassian.net
`), 0o644))

	c, err := Load(root)
	if err != nil {
		t.Fatalf("project_key should be optional (auto-detected on first run): %v", err)
	}
	if c.Jira.ProjectKey != "" {
		t.Errorf("project_key: %q", c.Jira.ProjectKey)
	}
}

func TestLoad_JiraBaseURLRequiredWithoutEnv(t *testing.T) {
	t.Setenv("JIRA_BASE_URL", "")
	root := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(root, ".archetipo"), 0o755))
	must(t, os.WriteFile(filepath.Join(root, RelativePath), []byte(`connector: jira
`), 0o644))

	_, err := Load(root)
	if err == nil || !strings.Contains(err.Error(), "jira.base_url") {
		t.Fatalf("expected jira.base_url error, got %v", err)
	}
}

func TestLoad_JiraBaseURLEnvFallback(t *testing.T) {
	t.Setenv("JIRA_BASE_URL", "https://acme.atlassian.net")
	root := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(root, ".archetipo"), 0o755))
	must(t, os.WriteFile(filepath.Join(root, RelativePath), []byte(`connector: jira
`), 0o644))

	if _, err := Load(root); err != nil {
		t.Fatalf("JIRA_BASE_URL should satisfy base_url requirement: %v", err)
	}
}

func TestSave_PatchesJiraKeysPreservingComments(t *testing.T) {
	root := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(root, ".archetipo"), 0o755))
	initial := `connector: jira
paths:
  prd: docs/PRD.md

jira:
  # project_key is auto-detected on first run
  base_url: https://acme.atlassian.net
`
	must(t, os.WriteFile(filepath.Join(root, RelativePath), []byte(initial), 0o644))

	c, err := Load(root)
	must(t, err)
	c.Jira.ProjectKey = "ARCH"
	c.Jira.StatusMap = map[string]string{"TODO": "To Do", "DONE": "Done"}
	must(t, c.Save())

	out, err := os.ReadFile(filepath.Join(root, RelativePath))
	must(t, err)
	s := string(out)
	for _, want := range []string{
		"# project_key is auto-detected on first run",
		"base_url: https://acme.atlassian.net",
		"project_key: ARCH",
		"status_map:",
		"TODO: To Do",
		"DONE: Done",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in saved file:\n%s", want, s)
		}
	}
	if strings.Contains(s, "github:") {
		t.Errorf("jira connector save must not inject a github section:\n%s", s)
	}
}

func TestSave_ReusesEmptyJiraSectionFromTemplate(t *testing.T) {
	root := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(root, ".archetipo"), 0o755))
	// Mirrors the shipped template: `jira:` followed only by commented keys,
	// i.e. a null value node that Save must convert in place.
	initial := `connector: jira
jira:
# base_url: https://example.atlassian.net
# project_key: ARCH
`
	must(t, os.WriteFile(filepath.Join(root, RelativePath), []byte(initial), 0o644))

	t.Setenv("JIRA_BASE_URL", "https://acme.atlassian.net")
	c, err := Load(root)
	must(t, err)
	c.Jira.ProjectKey = "AID"
	must(t, c.Save())

	raw, err := os.ReadFile(filepath.Join(root, RelativePath))
	must(t, err)
	s := string(raw)
	if strings.Count(s, "jira:") != 1 {
		t.Fatalf("expected a single jira section, got:\n%s", s)
	}
	if !strings.Contains(s, "project_key: AID") {
		t.Errorf("missing project_key in saved file:\n%s", s)
	}
	// base_url came from env only: it must NOT be written to the file.
	if strings.Contains(s, "base_url: https://acme.atlassian.net") {
		t.Errorf("env-sourced base_url must not be persisted:\n%s", s)
	}
}

func TestSave_CreatesFileWithJiraSection(t *testing.T) {
	root := t.TempDir()
	c := Default()
	c.ProjectRoot = root
	c.Connector = ConnectorJira
	c.Jira.BaseURL = "https://acme.atlassian.net"
	c.Jira.ProjectKey = "ARCH"
	must(t, c.Save())

	raw, err := os.ReadFile(filepath.Join(root, RelativePath))
	must(t, err)
	s := string(raw)
	for _, want := range []string{"connector: jira", "jira:", "project_key: ARCH"} {
		if !strings.Contains(s, want) {
			t.Errorf("fresh config missing %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "github:") {
		t.Errorf("jira bootstrap must not emit a github section:\n%s", s)
	}
}
