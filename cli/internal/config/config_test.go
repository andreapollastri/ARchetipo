package config

import (
	"os"
	"path/filepath"
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
	if c.Paths.Backlog != ".archetipo/backlog.yaml" {
		t.Errorf("default backlog path: %q", c.Paths.Backlog)
	}
}

func TestLoadFromConfigFile(t *testing.T) {
	root := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(root, ".archetipo"), 0o755))
	must(t, os.WriteFile(filepath.Join(root, RelativePath), []byte(`connector: github
paths:
  backlog: my/BL.md
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
	if c.Connector != ConnectorGitHub {
		t.Errorf("connector: %q", c.Connector)
	}
	if c.Paths.Backlog != "my/BL.md" {
		t.Errorf("backlog: %q", c.Paths.Backlog)
	}
	// Defaults preserved for unspecified path keys.
	if c.Paths.PRD != "docs/PRD.md" {
		t.Errorf("PRD default lost: %q", c.Paths.PRD)
	}
	if c.Paths.Planning != ".archetipo/plans/" {
		t.Errorf("planning default lost: %q", c.Paths.Planning)
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

func TestUnknownConnectorRejected(t *testing.T) {
	root := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(root, ".archetipo"), 0o755))
	must(t, os.WriteFile(filepath.Join(root, RelativePath), []byte(`connector: gitlab
`), 0o644))
	_, err := Load(root)
	if err == nil {
		t.Fatal("expected error for unknown connector")
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
# only valid for file connector
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
		"# only valid for file connector",
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
