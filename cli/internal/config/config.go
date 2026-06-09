// Package config loads and validates .archetipo/config.yaml.
//
// The config file lives in the *target project* (the project where the user
// runs the CLI), not in the CLI repo. It selects which connector implements
// the contract, where artifacts live, and how workflow statuses are labelled.
//
// Defaults: when config.yaml does not exist, the file connector is selected
// with the canonical paths and statuses built into the CLI.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"gopkg.in/yaml.v3"

	"github.com/techreloaded-ar/ARchetipo/cli/internal/domain"
)

// Path of the config file relative to the project root.
const RelativePath = ".archetipo/config.yaml"

// Connector identifiers recognized by the registry.
const (
	ConnectorFile   = "file"
	ConnectorGitHub = "github"
)

// Config is the parsed shape of .archetipo/config.yaml.
type Config struct {
	Connector string                `yaml:"connector" json:"connector"`
	Paths     domain.ConfigPaths    `yaml:"paths" json:"paths"`
	Workflow  domain.WorkflowConfig `yaml:"workflow" json:"workflow"`
	File      domain.FileConfig     `yaml:"file" json:"file,omitempty"`
	GitHub    GitHubConfig          `yaml:"github" json:"github,omitempty"`
	// Worktree is the optional per-spec git worktree workflow. Disabled by
	// default; when enabled, `archetipo spec start` creates a branch + worktree
	// per spec so the review diff can be isolated and integrated with one merge.
	Worktree domain.WorktreeConfig `yaml:"worktree" json:"worktree,omitempty"`
	// ProjectRoot is the absolute path of the directory that contains
	// .archetipo/. Set by Load; not present in the YAML file.
	ProjectRoot string `yaml:"-" json:"project_root"`
}

// GitHubConfig holds connector-specific overrides. Owner and project number
// are auto-detected from `gh` when empty.
type GitHubConfig struct {
	Owner         string               `yaml:"owner,omitempty" json:"owner,omitempty"`
	ProjectNumber int                  `yaml:"project_number,omitempty" json:"project_number,omitempty"`
	ProjectNodeID string               `yaml:"project_node_id,omitempty" json:"project_node_id,omitempty"`
	ProjectURL    string               `yaml:"project_url,omitempty" json:"project_url,omitempty"`
	Fields        domain.ProjectFields `yaml:"fields,omitempty" json:"fields,omitempty"`
}

// Default returns the canonical default config (file connector, English status
// labels). Used when the project has no config.yaml.
func Default() Config {
	return Config{
		Connector: ConnectorFile,
		Paths: domain.ConfigPaths{
			PRD:         "docs/PRD.md",
			Mockups:     "docs/mockups/",
			TestResults: "docs/test-results/",
		},
		File: domain.FileConfig{
			Backlog:  ".archetipo/backlog.yaml",
			Planning: ".archetipo/plans/",
		},
		Workflow: domain.WorkflowConfig{
			Statuses: domain.StatusLabels{
				Todo:       string(domain.StatusTodo),
				Planned:    string(domain.StatusPlanned),
				InProgress: string(domain.StatusInProgress),
				Review:     string(domain.StatusReview),
				Done:       string(domain.StatusDone),
			},
		},
		Worktree: domain.WorktreeConfig{
			Enabled:      false,
			Base:         "main",
			Dir:          ".archetipo/worktrees",
			BranchPrefix: "archetipo/",
		},
	}
}

// Load locates `.archetipo/config.yaml` starting from startDir, walking up
// the directory tree until found or the filesystem root is reached. When
// not found, the default config rooted at startDir is returned.
func Load(startDir string) (Config, error) {
	root, cfgPath, err := find(startDir)
	if err != nil {
		return Config{}, err
	}
	if cfgPath == "" {
		// No config: use default rooted at startDir.
		c := Default()
		abs, _ := filepath.Abs(startDir)
		c.ProjectRoot = abs
		return c, nil
	}
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		return Config{}, fmt.Errorf("reading %s: %w", cfgPath, err)
	}
	if err := rejectLegacyKeys(raw, cfgPath); err != nil {
		return Config{}, err
	}
	c := Default()
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return Config{}, fmt.Errorf("parsing %s: %w", cfgPath, err)
	}
	c.applyDefaults()
	c.ProjectRoot = root
	if err := c.validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// rejectLegacyKeys scans the raw YAML for top-level `paths.backlog` /
// `paths.planning` and refuses the config with an explicit migration error.
// Those keys moved to a dedicated `file:` section; no automatic migration is
// performed.
func rejectLegacyKeys(raw []byte, cfgPath string) error {
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		// Defer the real parse error to the main Unmarshal below.
		return nil
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil
	}
	paths := childMapping(root, "paths")
	if paths == nil {
		return nil
	}
	legacy := []string{}
	if childMapping(paths, "backlog") != nil || childScalar(paths, "backlog") != nil {
		legacy = append(legacy, "paths.backlog -> file.backlog")
	}
	if childMapping(paths, "planning") != nil || childScalar(paths, "planning") != nil {
		legacy = append(legacy, "paths.planning -> file.planning")
	}
	if len(legacy) == 0 {
		return nil
	}
	return fmt.Errorf(
		"%s: legacy key(s) %v belong to the file connector and must move to a top-level `file:` section. "+
			"No automatic migration is performed — update your config manually",
		cfgPath, legacy,
	)
}

func childMapping(m *yaml.Node, key string) *yaml.Node {
	n := childNode(m, key)
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	return n
}

func childScalar(m *yaml.Node, key string) *yaml.Node {
	n := childNode(m, key)
	if n == nil || n.Kind != yaml.ScalarNode || n.Value == "" {
		return nil
	}
	return n
}

func childNode(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// applyDefaults fills empty fields with canonical defaults. Lets the user
// omit unchanged keys from config.yaml.
func (c *Config) applyDefaults() {
	d := Default()
	if c.Connector == "" {
		c.Connector = d.Connector
	}
	if c.Paths.PRD == "" {
		c.Paths.PRD = d.Paths.PRD
	}
	if c.Paths.Mockups == "" {
		c.Paths.Mockups = d.Paths.Mockups
	}
	if c.Paths.TestResults == "" {
		c.Paths.TestResults = d.Paths.TestResults
	}
	if c.File.Backlog == "" {
		c.File.Backlog = d.File.Backlog
	}
	if c.File.Planning == "" {
		c.File.Planning = d.File.Planning
	}
	if c.Workflow.Statuses.Todo == "" {
		c.Workflow.Statuses.Todo = d.Workflow.Statuses.Todo
	}
	if c.Workflow.Statuses.Planned == "" {
		c.Workflow.Statuses.Planned = d.Workflow.Statuses.Planned
	}
	if c.Workflow.Statuses.InProgress == "" {
		c.Workflow.Statuses.InProgress = d.Workflow.Statuses.InProgress
	}
	if c.Workflow.Statuses.Review == "" {
		c.Workflow.Statuses.Review = d.Workflow.Statuses.Review
	}
	if c.Workflow.Statuses.Done == "" {
		c.Workflow.Statuses.Done = d.Workflow.Statuses.Done
	}
	if c.Worktree.Base == "" {
		c.Worktree.Base = d.Worktree.Base
	}
	if c.Worktree.Dir == "" {
		c.Worktree.Dir = d.Worktree.Dir
	}
	if c.Worktree.BranchPrefix == "" {
		c.Worktree.BranchPrefix = d.Worktree.BranchPrefix
	}
}

// validate performs config-level checks. Connector name validation is
// intentionally deferred to connector.New, which already rejects unknown
// names and can list the registered set dynamically.
//
// Path validation verifies that the parent directory of each configured path
// exists (or is creatable) and that the location is writable. A missing leaf
// file (e.g. paths.prd before the first write) is acceptable. Paths used only
// by the active connector are checked; paths from other connectors are not.
func (c *Config) validate() error {
	checks := []struct {
		key  string
		path string
	}{
		{"paths.prd", c.Paths.PRD},
		{"paths.mockups", c.Paths.Mockups},
		{"paths.test_results", c.Paths.TestResults},
	}
	if c.Connector == ConnectorFile {
		checks = append(checks,
			struct{ key, path string }{"file.backlog", c.File.Backlog},
			struct{ key, path string }{"file.planning", c.File.Planning},
		)
	}
	for _, ck := range checks {
		if ck.path == "" {
			continue
		}
		if err := checkPathWritable(c.AbsPath(ck.path)); err != nil {
			return fmt.Errorf("config %s (%s): %w", ck.key, ck.path, err)
		}
	}
	return nil
}

// checkPathWritable ensures that the directory containing target exists (or
// can be created) and is writable. Used by validate() to surface bad config
// at Load time rather than at first write.
func checkPathWritable(target string) error {
	dir := target
	if filepath.Ext(target) != "" || !endsWithSep(target) {
		dir = filepath.Dir(target)
	}
	info, err := os.Stat(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
				return fmt.Errorf("parent directory %s is not creatable: %w", dir, mkErr)
			}
			info, err = os.Stat(dir)
			if err != nil {
				return err
			}
		} else {
			return err
		}
	}
	if !info.IsDir() {
		return fmt.Errorf("parent %s is not a directory", dir)
	}
	probe, err := os.CreateTemp(dir, ".archetipo-write-probe-*")
	if err != nil {
		return fmt.Errorf("directory %s is not writable: %w", dir, err)
	}
	probeName := probe.Name()
	_ = probe.Close()
	_ = os.Remove(probeName)
	return nil
}

func endsWithSep(s string) bool {
	if s == "" {
		return false
	}
	return s[len(s)-1] == filepath.Separator || s[len(s)-1] == '/'
}

// AbsPath joins p against the project root if p is relative.
func (c Config) AbsPath(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(c.ProjectRoot, p)
}

// Save patches the `github.owner` and `github.project_number` keys in the
// existing config file, preserving comments and the order of unrelated keys
// via yaml.Node. If the file does not yet exist a fresh one is written from
// the in-memory Config. When ProjectRoot is empty (e.g. tests using
// Default()) Save is a no-op.
func (c Config) Save() error {
	if c.ProjectRoot == "" {
		return nil
	}
	path := filepath.Join(c.ProjectRoot, RelativePath)
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("creating .archetipo dir: %w", err)
		}
		// Bootstrap: emit only the keys that matter for the github connector.
		// applyDefaults() will fill the rest at next Load. Avoids marshalling
		// the whole Config, whose nested types (domain.ConfigPaths /
		// domain.StatusLabels) lack yaml tags and would emit broken keys.
		gh := &yaml.Node{Kind: yaml.MappingNode}
		upsertGitHubMapping(gh, c.GitHub)
		doc := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
		root := doc.Content[0]
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "connector"},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: c.Connector},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "github"},
			gh,
		)
		out, err := yaml.Marshal(doc)
		if err != nil {
			return fmt.Errorf("encoding config: %w", err)
		}
		return os.WriteFile(path, []byte(out), 0o644)
	}
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("parsing %s: %w", path, err)
	}
	if err := upsertGitHubSection(&doc, c.GitHub); err != nil {
		return err
	}
	out, err := yaml.Marshal(&doc)
	if err != nil {
		return fmt.Errorf("encoding config: %w", err)
	}
	return os.WriteFile(path, out, 0o644)
}

// upsertGitHubSection finds (or creates) a top-level `github:` mapping inside
// the YAML document and ensures `owner` and `project_number` keys reflect g.
// Other keys under `github:` and elsewhere in the document are left untouched.
func upsertGitHubSection(doc *yaml.Node, g GitHubConfig) error {
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		// Empty or malformed document: rebuild a minimal mapping.
		doc.Kind = yaml.DocumentNode
		doc.Content = []*yaml.Node{{Kind: yaml.MappingNode}}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return fmt.Errorf("config root is not a mapping")
	}
	gh := findOrCreateChildMapping(root, "github")
	if gh == nil {
		key := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "github"}
		gh = &yaml.Node{Kind: yaml.MappingNode}
		root.Content = append(root.Content, key, gh)
	}
	upsertGitHubMapping(gh, g)
	return nil
}

func upsertGitHubMapping(gh *yaml.Node, g GitHubConfig) {
	setScalarChild(gh, "owner", g.Owner, "!!str")
	setScalarChild(gh, "project_number", strconv.Itoa(g.ProjectNumber), "!!int")
	setOptionalScalarChild(gh, "project_node_id", g.ProjectNodeID)
	setOptionalScalarChild(gh, "project_url", g.ProjectURL)
	if !projectFieldsEmpty(g.Fields) {
		setMappingChild(gh, "fields", projectFieldsNode(g.Fields))
	}
}

// findOrCreateChildMapping returns the value node for a given mapping key.
// If the key already exists but has an empty/null/scalar value (for example
// `github:` in the shipped template), the existing node is converted in place
// to a mapping so Save() patches it instead of appending a duplicate key.
func findOrCreateChildMapping(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value != key {
			continue
		}
		if m.Content[i+1].Kind != yaml.MappingNode {
			m.Content[i+1].Kind = yaml.MappingNode
			m.Content[i+1].Tag = "!!map"
			m.Content[i+1].Value = ""
			m.Content[i+1].Content = nil
			m.Content[i+1].Style = 0
		}
		return m.Content[i+1]
	}
	return nil
}

// setScalarChild sets a scalar key on a mapping, preserving any leading
// comment already attached to the existing key. Adds the key when missing.
// Resets Style so the new value is emitted plain regardless of how the
// previous value was quoted.
func setScalarChild(m *yaml.Node, key, value, tag string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1].Kind = yaml.ScalarNode
			m.Content[i+1].Tag = tag
			m.Content[i+1].Value = value
			m.Content[i+1].Style = 0
			return
		}
	}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: value},
	)
}

func setOptionalScalarChild(m *yaml.Node, key, value string) {
	if value == "" {
		return
	}
	setScalarChild(m, key, value, "!!str")
}

func setMappingChild(m *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = value
			return
		}
	}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		value,
	)
}

func projectFieldsEmpty(f domain.ProjectFields) bool {
	return f.StatusFieldID == "" &&
		f.PriorityFieldID == "" &&
		f.PointsFieldID == "" &&
		f.EpicFieldID == "" &&
		len(f.StatusOptions) == 0 &&
		len(f.PriorityOptions) == 0 &&
		len(f.EpicOptions) == 0
}

func projectFieldsNode(f domain.ProjectFields) *yaml.Node {
	n := &yaml.Node{Kind: yaml.MappingNode}
	setOptionalScalarChild(n, "status_field_id", f.StatusFieldID)
	setStringMapChild(n, "status_options", f.StatusOptions)
	setOptionalScalarChild(n, "priority_field_id", f.PriorityFieldID)
	setStringMapChild(n, "priority_options", f.PriorityOptions)
	setOptionalScalarChild(n, "points_field_id", f.PointsFieldID)
	setOptionalScalarChild(n, "epic_field_id", f.EpicFieldID)
	setStringMapChild(n, "epic_options", f.EpicOptions)
	return n
}

func setStringMapChild(m *yaml.Node, key string, values map[string]string) {
	if len(values) == 0 {
		return
	}
	child := &yaml.Node{Kind: yaml.MappingNode}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := values[k]
		child.Content = append(child.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v},
		)
	}
	setMappingChild(m, key, child)
}

// find walks up from start looking for .archetipo/config.yaml. Returns the
// project root (the directory that contains .archetipo/) and the absolute
// path of the config file. If neither is found, returns ("", "", nil).
func find(start string) (root, cfg string, err error) {
	abs, err := filepath.Abs(start)
	if err != nil {
		return "", "", err
	}
	dir := abs
	for {
		candidate := filepath.Join(dir, RelativePath)
		info, statErr := os.Stat(candidate)
		if statErr == nil && !info.IsDir() {
			return dir, candidate, nil
		}
		if statErr != nil && !errors.Is(statErr, fs.ErrNotExist) {
			return "", "", statErr
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", "", nil
		}
		dir = parent
	}
}
