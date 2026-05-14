package filefs

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/techreloaded-ar/ARchetipo/cli/internal/domain"
	"github.com/techreloaded-ar/ARchetipo/cli/internal/iox"
)

const (
	backlogSchema = "archetipo/backlog/v2"
	storySchema   = "archetipo/story/v2"
	planSchema    = "archetipo/plan/v2"
)

type backlogDoc struct {
	Schema   string                `yaml:"schema"`
	Version  int                   `yaml:"version"`
	Workflow domain.WorkflowConfig `yaml:"workflow"`
	Epics    []domain.Epic         `yaml:"epics,omitempty"`
	Board    boardDoc              `yaml:"board"`
	Orders   ordersDoc             `yaml:"orders"`
}

type boardDoc struct {
	Columns []boardColumnDoc `yaml:"columns"`
}

type boardColumnDoc struct {
	ID     string        `yaml:"id"`
	Title  string        `yaml:"title"`
	Status domain.Status `yaml:"status"`
}

type ordersDoc struct {
	Backlog []string            `yaml:"backlog"`
	Board   map[string][]string `yaml:"board"`
}

type storyDoc struct {
	Schema      string          `yaml:"schema"`
	Code        string          `yaml:"code"`
	Title       string          `yaml:"title"`
	Epic        string          `yaml:"epic"`
	Priority    domain.Priority `yaml:"priority"`
	StoryPoints int             `yaml:"story_points"`
	Status      domain.Status   `yaml:"status"`
	BlockedBy   []string        `yaml:"blocked_by,omitempty"`
	Scope       domain.Scope    `yaml:"scope,omitempty"`
	Body        string          `yaml:"body,omitempty"`
	Ref         string          `yaml:"ref,omitempty"`
	URL         string          `yaml:"url,omitempty"`
}

type planDoc struct {
	Schema    string        `yaml:"schema"`
	StoryCode string        `yaml:"story_code"`
	Body      string        `yaml:"body"`
	Tasks     []domain.Task `yaml:"tasks"`
}

type yamlStore struct {
	Backlog backlogDoc
	Stories map[string]domain.Story
}

func (c *Connector) backlogPath() string {
	return c.cfg.AbsPath(c.cfg.Paths.Backlog)
}

func (c *Connector) storiesDir() string {
	return filepath.Join(filepath.Dir(c.backlogPath()), "stories")
}

func (c *Connector) planPath(storyRef string) string {
	return filepath.Join(c.cfg.AbsPath(c.cfg.Paths.Planning), storyRef+"-plan.yaml")
}

func (c *Connector) storyPath(storyCode string) string {
	return filepath.Join(c.storiesDir(), storyCode+".yaml")
}

func (c *Connector) loadStore() (yamlStore, error) {
	path := c.backlogPath()
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return c.loadLegacyStore()
		}
		return yamlStore{}, fmt.Errorf("reading backlog store: %w", err)
	}
	var backlog backlogDoc
	if err := yaml.Unmarshal(raw, &backlog); err != nil {
		return yamlStore{}, iox.NewInvalidInput("invalid backlog YAML", "check .archetipo/backlog.yaml", err)
	}
	stories, err := c.readStoryDocs(backlog.Epics)
	if err != nil {
		return yamlStore{}, err
	}
	backlog = c.normalizeBacklog(backlog, stories)
	return yamlStore{Backlog: backlog, Stories: stories}, nil
}

func (c *Connector) readStoryDocs(epics []domain.Epic) (map[string]domain.Story, error) {
	dir := c.storiesDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string]domain.Story{}, nil
		}
		return nil, fmt.Errorf("reading stories dir: %w", err)
	}
	epicTitles := make(map[string]string, len(epics))
	for _, epic := range epics {
		if epic.Code == "" {
			continue
		}
		epicTitles[epic.Code] = epic.Title
	}
	out := make(map[string]domain.Story, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading story file %s: %w", path, err)
		}
		var doc storyDoc
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			return nil, iox.NewInvalidInput(fmt.Sprintf("invalid story YAML at %s", path), "", err)
		}
		st := doc.toStory()
		if st.Code == "" {
			st.Code = strings.TrimSuffix(entry.Name(), ".yaml")
		}
		if st.Epic.Code != "" && st.Epic.Title == "" {
			st.Epic.Title = epicTitles[st.Epic.Code]
		}
		if st.Ref == "" {
			st.Ref = st.Code
		}
		out[st.Code] = st
	}
	return out, nil
}

func (c *Connector) readPlan(storyCode string) (planDoc, error) {
	path := c.planPath(storyCode)
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return c.readLegacyPlan(storyCode)
		}
		return planDoc{}, fmt.Errorf("reading plan file: %w", err)
	}
	var doc planDoc
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return planDoc{}, iox.NewInvalidInput(fmt.Sprintf("invalid plan YAML at %s", path), "", err)
	}
	if doc.StoryCode == "" {
		doc.StoryCode = storyCode
	}
	for i := range doc.Tasks {
		if doc.Tasks[i].Ref == "" {
			doc.Tasks[i].Ref = doc.Tasks[i].ID
		}
	}
	return doc, nil
}

func (c *Connector) readLegacyPlan(storyCode string) (planDoc, error) {
	legacyPath := filepath.Join(c.legacyPlanningDir(), storyCode+".md")
	raw, err := os.ReadFile(legacyPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return planDoc{}, iox.NewPrecondition(
				fmt.Sprintf("planning file for %s not found", storyCode),
				"run `archetipo story plan` first", err,
			)
		}
		return planDoc{}, fmt.Errorf("reading legacy plan: %w", err)
	}
	body, tasks, err := parsePlan(string(raw))
	if err != nil {
		return planDoc{}, err
	}
	for i := range tasks {
		tasks[i].Ref = tasks[i].ID
	}
	return planDoc{
		Schema:    planSchema,
		StoryCode: storyCode,
		Body:      body,
		Tasks:     tasks,
	}, nil
}

func (c *Connector) loadLegacyStore() (yamlStore, error) {
	legacyPath := c.legacyBacklogPath()
	raw, err := os.ReadFile(legacyPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return yamlStore{}, iox.NewPrecondition(
				fmt.Sprintf("backlog not found at %s", c.backlogPath()),
				"run `archetipo story add` or `archetipo-spec` first", errBacklogMissing,
			)
		}
		return yamlStore{}, fmt.Errorf("reading legacy backlog: %w", err)
	}
	stories, err := parseBacklog(string(raw))
	if err != nil {
		return yamlStore{}, err
	}
	out := make(map[string]domain.Story, len(stories))
	order := make([]string, 0, len(stories))
	for _, story := range stories {
		story.Ref = story.Code
		out[story.Code] = story
		order = append(order, story.Code)
	}
	backlog := c.normalizeBacklog(backlogDoc{
		Schema:   backlogSchema,
		Version:  2,
		Workflow: c.cfg.Workflow,
		Orders: ordersDoc{
			Backlog: order,
			Board:   map[string][]string{},
		},
	}, out)
	return yamlStore{Backlog: backlog, Stories: out}, nil
}

func (c *Connector) legacyBacklogPath() string {
	if strings.HasSuffix(strings.ToLower(c.cfg.Paths.Backlog), ".md") {
		return c.cfg.AbsPath(c.cfg.Paths.Backlog)
	}
	return filepath.Join(c.cfg.ProjectRoot, "docs", "BACKLOG.md")
}

func (c *Connector) legacyPlanningDir() string {
	if strings.HasSuffix(strings.ToLower(c.cfg.Paths.Planning), ".md") {
		return filepath.Dir(c.cfg.AbsPath(c.cfg.Paths.Planning))
	}
	if strings.HasSuffix(strings.ToLower(c.cfg.Paths.Planning), "/") || strings.Contains(filepath.Base(c.cfg.Paths.Planning), ".") == false {
		path := c.cfg.AbsPath(c.cfg.Paths.Planning)
		if strings.HasSuffix(strings.ToLower(path), ".yaml") {
			return filepath.Join(c.cfg.ProjectRoot, "docs", "planning")
		}
		return path
	}
	return filepath.Join(c.cfg.ProjectRoot, "docs", "planning")
}

func (c *Connector) writeStore(store yamlStore) error {
	store.Backlog = c.normalizeBacklog(store.Backlog, store.Stories)
	if err := writeYAML(c.backlogPath(), store.Backlog); err != nil {
		return err
	}
	for code, story := range store.Stories {
		doc := storyDocFromStory(story)
		doc.Schema = storySchema
		doc.Ref = ""
		doc.URL = ""
		if err := writeYAML(c.storyPath(code), doc); err != nil {
			return err
		}
	}
	return nil
}

func storyDocFromStory(story domain.Story) storyDoc {
	return storyDoc{
		Code:        story.Code,
		Title:       story.Title,
		Epic:        story.Epic.Code,
		Priority:    story.Priority,
		StoryPoints: story.StoryPoints,
		Status:      story.Status,
		BlockedBy:   story.BlockedBy,
		Scope:       story.Scope,
		Body:        story.Body,
		Ref:         story.Ref,
		URL:         story.URL,
	}
}

func (d storyDoc) toStory() domain.Story {
	return domain.Story{
		Code:        d.Code,
		Title:       d.Title,
		Epic:        domain.Epic{Code: d.Epic},
		Priority:    d.Priority,
		StoryPoints: d.StoryPoints,
		Status:      d.Status,
		BlockedBy:   d.BlockedBy,
		Scope:       d.Scope,
		Body:        d.Body,
		Ref:         d.Ref,
		URL:         d.URL,
	}
}

func (c *Connector) writePlan(storyCode string, plan domain.PlanInput) error {
	doc := planDoc{
		Schema:    planSchema,
		StoryCode: storyCode,
		Body:      plan.PlanBody,
		Tasks:     append([]domain.Task(nil), plan.Tasks...),
	}
	for i := range doc.Tasks {
		doc.Tasks[i].Ref = ""
	}
	return writeYAML(c.planPath(storyCode), doc)
}

func writeYAML(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating dir: %w", err)
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("encoding YAML: %w", err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("closing YAML encoder: %w", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

func (c *Connector) normalizeBacklog(doc backlogDoc, stories map[string]domain.Story) backlogDoc {
	doc.Schema = backlogSchema
	doc.Version = 2
	doc.Workflow = c.cfg.Workflow
	if len(doc.Board.Columns) == 0 {
		doc.Board.Columns = defaultBoardColumns(c.cfg.Workflow.Statuses)
	}
	if doc.Orders.Board == nil {
		doc.Orders.Board = map[string][]string{}
	}
	epics := map[string]domain.Epic{}
	for _, story := range stories {
		if story.Ref == "" {
			story.Ref = story.Code
		}
		if story.Epic.Code != "" {
			epics[story.Epic.Code] = story.Epic
		}
	}
	doc.Epics = doc.Epics[:0]
	for _, epic := range epics {
		doc.Epics = append(doc.Epics, epic)
	}
	sort.Slice(doc.Epics, func(i, j int) bool { return doc.Epics[i].Code < doc.Epics[j].Code })

	ordered := dedupeKnown(doc.Orders.Backlog, stories)
	missing := missingCodes(stories, ordered)
	sort.Strings(missing)
	doc.Orders.Backlog = append(ordered, missing...)

	boardSeen := map[string]struct{}{}
	normalizedBoard := make(map[string][]string, len(doc.Board.Columns))
	for _, col := range doc.Board.Columns {
		normalizedBoard[col.ID] = []string{}
	}
	for _, col := range doc.Board.Columns {
		for _, code := range doc.Orders.Board[col.ID] {
			story, ok := stories[code]
			if !ok {
				continue
			}
			if expected, ok := columnIDForStatus(doc.Board.Columns, story.Status); !ok || expected != col.ID {
				continue
			}
			if _, dup := boardSeen[code]; dup {
				continue
			}
			normalizedBoard[col.ID] = append(normalizedBoard[col.ID], code)
			boardSeen[code] = struct{}{}
		}
	}
	for _, code := range doc.Orders.Backlog {
		if _, ok := boardSeen[code]; ok {
			continue
		}
		story, ok := stories[code]
		if !ok {
			continue
		}
		colID, ok := columnIDForStatus(doc.Board.Columns, story.Status)
		if !ok {
			colID = doc.Board.Columns[0].ID
		}
		normalizedBoard[colID] = append(normalizedBoard[colID], code)
	}
	doc.Orders.Board = normalizedBoard
	return doc
}

func defaultBoardColumns(labels domain.StatusLabels) []boardColumnDoc {
	return []boardColumnDoc{
		{ID: "todo", Title: labels.Todo, Status: domain.Status(labels.Todo)},
		{ID: "planned", Title: labels.Planned, Status: domain.Status(labels.Planned)},
		{ID: "in_progress", Title: labels.InProgress, Status: domain.Status(labels.InProgress)},
		{ID: "review", Title: labels.Review, Status: domain.Status(labels.Review)},
		{ID: "done", Title: labels.Done, Status: domain.Status(labels.Done)},
	}
}

func columnIDForStatus(columns []boardColumnDoc, status domain.Status) (string, bool) {
	for _, col := range columns {
		if col.Status == status {
			return col.ID, true
		}
	}
	return "", false
}

func columnStatus(columns []boardColumnDoc, id string) (domain.Status, bool) {
	for _, col := range columns {
		if col.ID == id {
			return col.Status, true
		}
	}
	return "", false
}

func dedupeKnown(order []string, stories map[string]domain.Story) []string {
	out := make([]string, 0, len(order))
	seen := map[string]struct{}{}
	for _, code := range order {
		if _, ok := stories[code]; !ok {
			continue
		}
		if _, dup := seen[code]; dup {
			continue
		}
		out = append(out, code)
		seen[code] = struct{}{}
	}
	return out
}

func missingCodes(stories map[string]domain.Story, have []string) []string {
	seen := map[string]struct{}{}
	for _, code := range have {
		seen[code] = struct{}{}
	}
	out := make([]string, 0)
	for code := range stories {
		if _, ok := seen[code]; !ok {
			out = append(out, code)
		}
	}
	return out
}

func insertRelative(list []string, code string, anchor domain.ReorderAnchor) ([]string, error) {
	list = removeCode(list, code)
	switch {
	case anchor.Before != "" && anchor.After != "":
		return nil, iox.NewInvalidInput("before and after are mutually exclusive", "pass only one anchor", nil)
	case anchor.Before != "":
		idx := indexOf(list, anchor.Before)
		if idx == -1 {
			return nil, iox.NewPrecondition(fmt.Sprintf("story %s not found in target order", anchor.Before), "", nil)
		}
		list = append(list[:idx], append([]string{code}, list[idx:]...)...)
	case anchor.After != "":
		idx := indexOf(list, anchor.After)
		if idx == -1 {
			return nil, iox.NewPrecondition(fmt.Sprintf("story %s not found in target order", anchor.After), "", nil)
		}
		idx++
		if idx >= len(list) {
			list = append(list, code)
		} else {
			list = append(list[:idx], append([]string{code}, list[idx:]...)...)
		}
	default:
		list = append(list, code)
	}
	return list, nil
}

func removeCode(list []string, code string) []string {
	out := make([]string, 0, len(list))
	for _, item := range list {
		if item != code {
			out = append(out, item)
		}
	}
	return out
}

func indexOf(list []string, code string) int {
	for i, item := range list {
		if item == code {
			return i
		}
	}
	return -1
}
