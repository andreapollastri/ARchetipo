package filefs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"github.com/techreloaded-ar/ARchetipo/cli/internal/config"
	"github.com/techreloaded-ar/ARchetipo/cli/internal/connector"
	"github.com/techreloaded-ar/ARchetipo/cli/internal/domain"
	"github.com/techreloaded-ar/ARchetipo/cli/internal/iox"
)

// Connector is the file-system implementation of connector.Connector.
type Connector struct {
	cfg config.Config
}

// New constructs a Connector. Always succeeds — config validation happens at
// load time.
func New(cfg config.Config) *Connector { return &Connector{cfg: cfg} }

// Register hooks the file connector into the registry under the canonical
// name "file".
func Register() {
	connector.Register(config.ConnectorFile, func(cfg config.Config) (connector.Connector, error) {
		return New(cfg), nil
	})
}

var errBacklogMissing = errors.New("backlog missing")

// mockupStoryCodeRE matches mockup folder names that map 1:1 to a story or
// epic code so the viewer can render a per-story link.
var mockupStoryCodeRE = regexp.MustCompile(`^(US|EP)-\d+$`)

func (c *Connector) InitializeConnector(ctx context.Context) (domain.SetupInfo, error) {
	return domain.SetupInfo{
		Connector: config.ConnectorFile,
		Paths:     c.cfg.Paths,
		Workflow:  c.cfg.Workflow,
	}, nil
}

func (c *Connector) FetchBacklogItems(ctx context.Context, statusFilter domain.Status) ([]domain.Story, error) {
	store, err := c.loadStore()
	if err != nil {
		return nil, err
	}
	out := make([]domain.Story, 0, len(store.Stories))
	for _, col := range c.boardColumns() {
		for _, code := range store.Backlog.Orders.Board[col.ID] {
			story, ok := store.Stories[code]
			if !ok {
				continue
			}
			if statusFilter != "" && story.Status != statusFilter {
				continue
			}
			out = append(out, story)
		}
	}
	return out, nil
}

func (c *Connector) SelectStory(ctx context.Context, q domain.SelectQuery) (domain.Story, error) {
	stories, err := c.FetchBacklogItems(ctx, "")
	if err != nil {
		return domain.Story{}, err
	}
	if q.StoryCode != "" {
		for _, story := range stories {
			if story.Code == q.StoryCode {
				return story, nil
			}
		}
		return domain.Story{}, iox.NewPrecondition(
			fmt.Sprintf("story %s not found in backlog", q.StoryCode),
			"check the backlog or run `archetipo backlog show`", nil,
		)
	}
	eligible := map[domain.Status]struct{}{}
	for _, status := range q.EligibleStatuses {
		eligible[status] = struct{}{}
	}
	candidates := make([]domain.Story, 0, len(stories))
	for _, story := range stories {
		if _, ok := eligible[story.Status]; ok {
			candidates = append(candidates, story)
		}
	}
	if len(candidates) == 0 {
		return domain.Story{}, iox.NewPrecondition(
			"no eligible stories in backlog",
			"check the backlog status distribution", nil,
		)
	}
	domain.SortByPriorityThenCode(candidates)
	return candidates[0], nil
}

func (c *Connector) ReadStoryDetail(ctx context.Context, ref string) (domain.Story, error) {
	store, err := c.loadStore()
	if err != nil {
		return domain.Story{}, err
	}
	story, ok := store.Stories[ref]
	if !ok {
		return domain.Story{}, iox.NewPrecondition(fmt.Sprintf("story %s not found in backlog", ref), "", nil)
	}
	return story, nil
}

func (c *Connector) ReadStoryTasks(ctx context.Context, parentRef string) ([]domain.Task, error) {
	plan, err := c.readPlan(parentRef)
	if err != nil {
		return nil, err
	}
	return append([]domain.Task(nil), plan.Tasks...), nil
}

func (c *Connector) ReadExistingBacklog(ctx context.Context) (domain.BacklogSummary, error) {
	store, err := c.loadStore()
	if err != nil {
		return domain.BacklogSummary{}, err
	}
	out := domain.BacklogSummary{}
	seenEpics := map[string]domain.Epic{}
	for _, col := range c.boardColumns() {
		for _, code := range store.Backlog.Orders.Board[col.ID] {
			story, ok := store.Stories[code]
			if !ok {
				continue
			}
			out.Codes = append(out.Codes, story.Code)
			out.Titles = append(out.Titles, story.Title)
			if story.Epic.Code != "" {
				seenEpics[story.Epic.Code] = story.Epic
			}
		}
	}
	sortedCodes := append([]string(nil), out.Codes...)
	sort.Strings(sortedCodes)
	out.Codes = sortedCodes
	if len(out.Codes) > 0 {
		out.LastCode = highestCode(out.Codes)
	}
	for _, epic := range seenEpics {
		out.Epics = append(out.Epics, epic)
	}
	sort.Slice(out.Epics, func(i, j int) bool { return out.Epics[i].Code < out.Epics[j].Code })
	return out, nil
}

func (c *Connector) SavePRD(ctx context.Context, content string) (domain.WriteResult, error) {
	path := c.cfg.AbsPath(c.cfg.Paths.PRD)
	if err := writeFile(path, []byte(content)); err != nil {
		return domain.WriteResult{}, err
	}
	return domain.WriteResult{OK: true, Refs: []domain.Ref{{Path: path}}}, nil
}

// ReadBoardOrder returns the per-column ordering of story codes as persisted
// by MoveBoardCard. The web viewer uses it to render the Kanban in the order
// the user assigned via drag-and-drop.
func (c *Connector) ReadBoardOrder(ctx context.Context) (map[string][]string, error) {
	store, err := c.loadStore()
	if err != nil {
		return nil, err
	}
	out := make(map[string][]string, len(store.Backlog.Orders.Board))
	for id, codes := range store.Backlog.Orders.Board {
		cp := make([]string, len(codes))
		copy(cp, codes)
		out[id] = cp
	}
	return out, nil
}

// ReadPRD returns the contents of the configured PRD file. A missing file is
// not an error: callers (the viewer) should treat it as an empty PRD so the
// edit flow can create it on first save.
func (c *Connector) ReadPRD(ctx context.Context) (string, error) {
	path := c.cfg.AbsPath(c.cfg.Paths.PRD)
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", iox.NewInternal(fmt.Sprintf("reading %s", path), err)
	}
	return string(b), nil
}

// ListMockups enumerates subfolders of paths.mockups that contain an
// index.html and returns them as MockupEntry records. A missing mockups
// directory yields an empty slice (not an error). Folder names matching the
// US-NNN or EP-NNN pattern are tagged with the corresponding StoryCode.
func (c *Connector) ListMockups(ctx context.Context) ([]domain.MockupEntry, error) {
	root := c.cfg.AbsPath(c.cfg.Paths.Mockups)
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []domain.MockupEntry{}, nil
		}
		return nil, iox.NewInternal(fmt.Sprintf("reading %s", root), err)
	}
	out := []domain.MockupEntry{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		indexPath := filepath.Join(root, e.Name(), "index.html")
		if _, err := os.Stat(indexPath); err != nil {
			continue
		}
		name := e.Name()
		entry := domain.MockupEntry{
			Name: name,
			URL:  "/mockups/" + name + "/index.html",
		}
		if mockupStoryCodeRE.MatchString(name) {
			entry.StoryCode = name
		}
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (c *Connector) SaveInitialBacklog(ctx context.Context, stories []domain.Story) (domain.WriteResult, error) {
	if len(stories) == 0 {
		return domain.WriteResult{}, iox.NewInvalidInput("no stories to write", "stdin must contain a non-empty stories array", nil)
	}
	if store, err := c.loadStore(); err == nil {
		if len(store.Stories) > 0 {
			return domain.WriteResult{}, iox.NewConnector(
				iox.CodeConflict,
				"backlog already exists with stories",
				"use `archetipo story add` to extend it",
				nil,
			)
		}
	} else {
		var ce *iox.CodedError
		if !errors.As(err, &ce) || ce.Code != iox.CodePreconditionMissing {
			return domain.WriteResult{}, err
		}
	}

	store := yamlStore{
		Backlog: c.normalizeBacklog(backlogDoc{
			Schema:  backlogSchema,
			Version: 2,
			Orders:  ordersDoc{Board: map[string][]string{}},
		}, map[string]domain.Story{}),
		Stories: map[string]domain.Story{},
	}
	for _, story := range stories {
		story.Ref = story.Code
		store.Stories[story.Code] = story
	}
	if err := c.writeStore(store); err != nil {
		return domain.WriteResult{}, err
	}
	return domain.WriteResult{OK: true, Refs: refsFromStories(stories, c.backlogPath())}, nil
}

func (c *Connector) AppendStories(ctx context.Context, stories []domain.Story) (domain.WriteResult, error) {
	if len(stories) == 0 {
		return domain.WriteResult{}, iox.NewInvalidInput("no stories to append", "stdin must contain a non-empty stories array", nil)
	}
	store, err := c.loadStore()
	if err != nil {
		var ce *iox.CodedError
		if errors.As(err, &ce) && ce.Code == iox.CodePreconditionMissing {
			return c.SaveInitialBacklog(ctx, stories)
		}
		return domain.WriteResult{}, err
	}
	added := make([]domain.Story, 0, len(stories))
	for _, story := range stories {
		if _, exists := store.Stories[story.Code]; exists {
			continue
		}
		story.Ref = story.Code
		store.Stories[story.Code] = story
		added = append(added, story)
	}
	if err := c.writeStore(store); err != nil {
		return domain.WriteResult{}, err
	}
	return domain.WriteResult{OK: true, Refs: refsFromStories(added, c.backlogPath())}, nil
}

func (c *Connector) SavePlan(ctx context.Context, storyRef string, plan domain.PlanInput) (domain.WriteResult, error) {
	if storyRef == "" {
		return domain.WriteResult{}, iox.NewInvalidInput("missing story ref", "pass US-XXX as positional argument", nil)
	}
	if _, err := c.ReadStoryDetail(ctx, storyRef); err != nil {
		return domain.WriteResult{}, err
	}
	if err := c.writePlan(storyRef, plan); err != nil {
		return domain.WriteResult{}, err
	}
	refs := []domain.Ref{{Code: storyRef, Path: c.planPath(storyRef)}}
	for _, task := range plan.Tasks {
		refs = append(refs, domain.Ref{Code: task.ID, Path: c.planPath(storyRef)})
	}
	return domain.WriteResult{OK: true, Refs: refs}, nil
}

func (c *Connector) TransitionStatus(ctx context.Context, storyRef string, newStatus domain.Status) (domain.WriteResult, error) {
	store, err := c.loadStore()
	if err != nil {
		return domain.WriteResult{}, err
	}
	story, ok := store.Stories[storyRef]
	if !ok {
		return domain.WriteResult{}, iox.NewPrecondition(fmt.Sprintf("story %s not found", storyRef), "", nil)
	}
	colID, ok := columnIDForStatus(c.boardColumns(), newStatus)
	if !ok {
		return domain.WriteResult{}, iox.NewConflict(fmt.Sprintf("status %s is not mapped to a board column", newStatus), "", nil)
	}
	story.Status = newStatus
	store.Stories[storyRef] = story
	for id, order := range store.Backlog.Orders.Board {
		store.Backlog.Orders.Board[id] = removeCode(order, storyRef)
	}
	store.Backlog.Orders.Board[colID] = append(store.Backlog.Orders.Board[colID], storyRef)
	if err := c.writeStore(store); err != nil {
		return domain.WriteResult{}, err
	}
	return domain.WriteResult{
		OK: true,
		Refs: []domain.Ref{
			{Code: storyRef, Path: c.backlogPath()},
			{Code: storyRef, Path: c.storyPath(storyRef)},
		},
	}, nil
}

func (c *Connector) CompleteTask(ctx context.Context, parentRef, taskRef string) (domain.WriteResult, error) {
	if parentRef == "" || taskRef == "" {
		return domain.WriteResult{}, iox.NewInvalidInput("missing parent or task ref", "usage: archetipo task done US-XXX TASK-NN", nil)
	}
	plan, err := c.readPlan(parentRef)
	if err != nil {
		return domain.WriteResult{}, err
	}
	hit := false
	for i := range plan.Tasks {
		if plan.Tasks[i].ID == taskRef {
			plan.Tasks[i].Status = domain.StatusDone
			hit = true
			break
		}
	}
	if !hit {
		return domain.WriteResult{}, iox.NewPrecondition(
			fmt.Sprintf("task %s not found in plan %s", taskRef, parentRef),
			"", nil,
		)
	}
	if err := writeYAML(c.planPath(parentRef), plan); err != nil {
		return domain.WriteResult{}, err
	}
	return domain.WriteResult{OK: true, Refs: []domain.Ref{{Code: taskRef, Path: c.planPath(parentRef)}}}, nil
}

func (c *Connector) MoveBoardCard(ctx context.Context, storyRef, targetColumn string, anchor domain.ReorderAnchor) (domain.WriteResult, error) {
	store, err := c.loadStore()
	if err != nil {
		return domain.WriteResult{}, err
	}
	story, ok := store.Stories[storyRef]
	if !ok {
		return domain.WriteResult{}, iox.NewPrecondition(fmt.Sprintf("story %s not found", storyRef), "", nil)
	}
	targetStatus, ok := columnStatus(c.boardColumns(), targetColumn)
	if !ok {
		return domain.WriteResult{}, iox.NewInvalidInput(
			fmt.Sprintf("unknown board column %q", targetColumn),
			"allowed: todo, planned, in_progress, review, done",
			nil,
		)
	}
	for id, order := range store.Backlog.Orders.Board {
		store.Backlog.Orders.Board[id] = removeCode(order, storyRef)
	}
	newOrder, err := insertRelative(store.Backlog.Orders.Board[targetColumn], storyRef, anchor)
	if err != nil {
		return domain.WriteResult{}, err
	}
	store.Backlog.Orders.Board[targetColumn] = newOrder
	refs := []domain.Ref{{Code: storyRef, Path: c.backlogPath()}}
	if story.Status != targetStatus {
		story.Status = targetStatus
		store.Stories[storyRef] = story
		refs = append(refs, domain.Ref{Code: storyRef, Path: c.storyPath(storyRef)})
	}
	if err := c.writeStore(store); err != nil {
		return domain.WriteResult{}, err
	}
	return domain.WriteResult{OK: true, Refs: refs}, nil
}

func (c *Connector) PostComment(ctx context.Context, storyRef, body string) (domain.WriteResult, error) {
	return domain.WriteResult{OK: true}, nil
}

// ReadPlanBody returns the prose body of a story's plan, if any. It is not on
// the Connector interface because not every backend keeps a separate body:
// the github connector mixes it into the parent issue body. The web viewer
// discovers this method at runtime via a type assertion.
func (c *Connector) ReadPlanBody(ctx context.Context, storyCode string) (string, error) {
	plan, err := c.readPlan(storyCode)
	if err != nil {
		return "", err
	}
	return plan.Body, nil
}

func (c *Connector) UpdateStory(ctx context.Context, storyRef string, patch domain.StoryUpdate) (domain.WriteResult, error) {
	store, err := c.loadStore()
	if err != nil {
		return domain.WriteResult{}, err
	}
	story, ok := store.Stories[storyRef]
	if !ok {
		return domain.WriteResult{}, iox.NewPrecondition(fmt.Sprintf("story %s not found", storyRef), "", nil)
	}
	if patch.Title != nil {
		story.Title = *patch.Title
	}
	if patch.Priority != nil {
		story.Priority = *patch.Priority
	}
	if patch.StoryPoints != nil {
		story.StoryPoints = *patch.StoryPoints
	}
	if patch.Scope != nil {
		story.Scope = *patch.Scope
	}
	if patch.BlockedBy != nil {
		story.BlockedBy = append([]string(nil), (*patch.BlockedBy)...)
	}
	if patch.Body != nil {
		story.Body = *patch.Body
	}
	if patch.Epic != nil {
		story.Epic = *patch.Epic
	}
	store.Stories[storyRef] = story
	if err := c.writeStore(store); err != nil {
		return domain.WriteResult{}, err
	}
	return domain.WriteResult{
		OK: true,
		Refs: []domain.Ref{
			{Code: storyRef, Path: c.storyPath(storyRef)},
			{Code: storyRef, Path: c.backlogPath()},
		},
	}, nil
}

func refsFromStories(stories []domain.Story, path string) []domain.Ref {
	out := make([]domain.Ref, 0, len(stories))
	for _, story := range stories {
		out = append(out, domain.Ref{Code: story.Code, Path: path})
	}
	return out
}

func highestCode(codes []string) string {
	best := ""
	bestN := -1
	for _, code := range codes {
		if n := numericTail(code); n > bestN {
			best, bestN = code, n
		}
	}
	return best
}

func numericTail(code string) int {
	value := 0
	multiplier := 1
	for i := len(code) - 1; i >= 0; i-- {
		if code[i] < '0' || code[i] > '9' {
			break
		}
		value += int(code[i]-'0') * multiplier
		multiplier *= 10
	}
	return value
}

func writeFile(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating dir: %w", err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}
