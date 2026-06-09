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

// mockupSpecCodeRE matches mockup folder names that map 1:1 to a spec or
// epic code so the viewer can render a per-spec link.
var mockupSpecCodeRE = regexp.MustCompile(`^(US|EP)-\d+$`)

func (c *Connector) InitializeConnector(ctx context.Context) (domain.SetupInfo, error) {
	file := c.cfg.File
	return domain.SetupInfo{
		Connector:   config.ConnectorFile,
		ProjectRoot: c.cfg.ProjectRoot,
		Paths:       c.cfg.Paths,
		Workflow:    c.cfg.Workflow,
		File:        &file,
	}, nil
}

func (c *Connector) FetchBacklogItems(ctx context.Context, statusFilter domain.Status) ([]domain.Spec, error) {
	store, err := c.loadStore()
	if err != nil {
		return nil, err
	}
	out := make([]domain.Spec, 0, len(store.Specs))
	for _, code := range store.Backlog.Order {
		spec, ok := store.Specs[code]
		if !ok {
			continue
		}
		if statusFilter != "" && spec.Status != statusFilter {
			continue
		}
		out = append(out, spec)
	}
	return out, nil
}

func (c *Connector) SelectSpec(ctx context.Context, q domain.SelectQuery) (domain.Spec, error) {
	specs, err := c.FetchBacklogItems(ctx, "")
	if err != nil {
		return domain.Spec{}, err
	}
	if q.SpecCode != "" {
		for _, spec := range specs {
			if spec.Code == q.SpecCode {
				return spec, nil
			}
		}
		return domain.Spec{}, iox.NewPrecondition(
			fmt.Sprintf("spec %s not found in backlog", q.SpecCode),
			"check the backlog or run `archetipo spec list`", nil,
		)
	}
	eligible := map[domain.Status]struct{}{}
	for _, status := range q.EligibleStatuses {
		eligible[status] = struct{}{}
	}
	candidates := make([]domain.Spec, 0, len(specs))
	for _, spec := range specs {
		if _, ok := eligible[spec.Status]; ok {
			candidates = append(candidates, spec)
		}
	}
	if len(candidates) == 0 {
		return domain.Spec{}, iox.NewPrecondition(
			"no eligible specs in backlog",
			"check the backlog status distribution", nil,
		)
	}
	domain.SortByPriorityThenCode(candidates)
	return candidates[0], nil
}

func (c *Connector) ReadSpecDetail(ctx context.Context, ref string) (domain.Spec, error) {
	store, err := c.loadStore()
	if err != nil {
		return domain.Spec{}, err
	}
	spec, ok := store.Specs[ref]
	if !ok {
		return domain.Spec{}, iox.NewPrecondition(fmt.Sprintf("spec %s not found in backlog", ref), "", nil)
	}
	return spec, nil
}

func (c *Connector) ReadSpecTasks(ctx context.Context, parentRef string) ([]domain.Task, error) {
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
	for _, code := range store.Backlog.Order {
		spec, ok := store.Specs[code]
		if !ok {
			continue
		}
		out.Codes = append(out.Codes, spec.Code)
		out.Titles = append(out.Titles, spec.Title)
		if spec.Epic.Code != "" {
			seenEpics[spec.Epic.Code] = spec.Epic
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

// ReadBoardOrder returns the global ordering of spec codes as persisted by
// MoveBoardCard. The web viewer projects this list onto Kanban columns by
// filtering on each spec's Status, so a single ordering is enough.
func (c *Connector) ReadBoardOrder(ctx context.Context) ([]string, error) {
	store, err := c.loadStore()
	if err != nil {
		return nil, err
	}
	out := make([]string, len(store.Backlog.Order))
	copy(out, store.Backlog.Order)
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
// US-NNN or EP-NNN pattern are tagged with the corresponding SpecCode.
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
		if mockupSpecCodeRE.MatchString(name) {
			entry.SpecCode = name
		}
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (c *Connector) SaveInitialBacklog(ctx context.Context, specs []domain.Spec) (domain.WriteResult, error) {
	if len(specs) == 0 {
		return domain.WriteResult{}, iox.NewInvalidInput("no specs to write", "stdin must contain a non-empty specs array", nil)
	}
	if store, err := c.loadStore(); err == nil {
		if len(store.Specs) > 0 {
			return domain.WriteResult{}, iox.NewConnector(
				iox.CodeConflict,
				"backlog already exists with specs",
				"use `archetipo spec add` to extend it",
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
			Order:   []string{},
		}, map[string]domain.Spec{}),
		Specs: map[string]domain.Spec{},
	}
	for _, spec := range specs {
		spec.Ref = spec.Code
		store.Specs[spec.Code] = spec
	}
	if err := c.writeStore(store); err != nil {
		return domain.WriteResult{}, err
	}
	return domain.WriteResult{OK: true, Refs: refsFromSpecs(specs, c.backlogPath())}, nil
}

func (c *Connector) AppendSpecs(ctx context.Context, specs []domain.Spec) (domain.WriteResult, error) {
	if len(specs) == 0 {
		return domain.WriteResult{}, iox.NewInvalidInput("no specs to append", "stdin must contain a non-empty specs array", nil)
	}
	store, err := c.loadStore()
	if err != nil {
		var ce *iox.CodedError
		if errors.As(err, &ce) && ce.Code == iox.CodePreconditionMissing {
			return c.SaveInitialBacklog(ctx, specs)
		}
		return domain.WriteResult{}, err
	}
	added := make([]domain.Spec, 0, len(specs))
	for _, spec := range specs {
		if _, exists := store.Specs[spec.Code]; exists {
			continue
		}
		spec.Ref = spec.Code
		store.Specs[spec.Code] = spec
		added = append(added, spec)
	}
	if err := c.writeStore(store); err != nil {
		return domain.WriteResult{}, err
	}
	return domain.WriteResult{OK: true, Refs: refsFromSpecs(added, c.backlogPath())}, nil
}

func (c *Connector) SavePlan(ctx context.Context, specRef string, plan domain.PlanInput) (domain.WriteResult, error) {
	if specRef == "" {
		return domain.WriteResult{}, iox.NewInvalidInput("missing spec ref", "pass US-XXX as positional argument", nil)
	}
	if _, err := c.ReadSpecDetail(ctx, specRef); err != nil {
		return domain.WriteResult{}, err
	}
	if err := c.writePlan(specRef, plan); err != nil {
		return domain.WriteResult{}, err
	}
	refs := []domain.Ref{{Code: specRef, Path: c.planPath(specRef)}}
	for _, task := range plan.Tasks {
		refs = append(refs, domain.Ref{Code: task.ID, Path: c.planPath(specRef)})
	}
	return domain.WriteResult{OK: true, Refs: refs}, nil
}

func (c *Connector) TransitionStatus(ctx context.Context, specRef string, newStatus domain.Status) (domain.WriteResult, error) {
	store, err := c.loadStore()
	if err != nil {
		return domain.WriteResult{}, err
	}
	spec, ok := store.Specs[specRef]
	if !ok {
		return domain.WriteResult{}, iox.NewPrecondition(fmt.Sprintf("spec %s not found", specRef), "", nil)
	}
	if _, ok := columnIDForStatus(c.boardColumns(), newStatus); !ok {
		return domain.WriteResult{}, iox.NewConflict(fmt.Sprintf("status %s is not mapped to a board column", newStatus), "", nil)
	}
	spec.Status = newStatus
	store.Specs[specRef] = spec
	if err := c.writeStore(store); err != nil {
		return domain.WriteResult{}, err
	}
	return domain.WriteResult{
		OK: true,
		Refs: []domain.Ref{
			{Code: specRef, Path: c.backlogPath()},
			{Code: specRef, Path: c.specPath(specRef)},
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

func (c *Connector) MoveBoardCard(ctx context.Context, specRef, targetColumn string, anchor domain.ReorderAnchor) (domain.WriteResult, error) {
	store, err := c.loadStore()
	if err != nil {
		return domain.WriteResult{}, err
	}
	spec, ok := store.Specs[specRef]
	if !ok {
		return domain.WriteResult{}, iox.NewPrecondition(fmt.Sprintf("spec %s not found", specRef), "", nil)
	}
	targetStatus, ok := columnStatus(c.boardColumns(), targetColumn)
	if !ok {
		return domain.WriteResult{}, iox.NewInvalidInput(
			fmt.Sprintf("unknown board column %q", targetColumn),
			"allowed: todo, planned, in_progress, review, done",
			nil,
		)
	}
	newOrder, err := insertRelative(store.Backlog.Order, specRef, anchor)
	if err != nil {
		return domain.WriteResult{}, err
	}
	store.Backlog.Order = newOrder
	refs := []domain.Ref{{Code: specRef, Path: c.backlogPath()}}
	if spec.Status != targetStatus {
		spec.Status = targetStatus
		store.Specs[specRef] = spec
		refs = append(refs, domain.Ref{Code: specRef, Path: c.specPath(specRef)})
	}
	if err := c.writeStore(store); err != nil {
		return domain.WriteResult{}, err
	}
	return domain.WriteResult{OK: true, Refs: refs}, nil
}

func (c *Connector) PostComment(ctx context.Context, specRef, body string) (domain.WriteResult, error) {
	return domain.WriteResult{OK: true}, nil
}

// ReadPlanBody returns the prose body of a spec's plan, if any. It is not on
// the Connector interface because not every backend keeps a separate body:
// the github connector mixes it into the parent issue body. The web viewer
// discovers this method at runtime via a type assertion.
func (c *Connector) ReadPlanBody(ctx context.Context, specCode string) (string, error) {
	plan, err := c.readPlan(specCode)
	if err != nil {
		return "", err
	}
	return plan.Body, nil
}

func (c *Connector) UpdateSpec(ctx context.Context, specRef string, patch domain.SpecUpdate) (domain.WriteResult, error) {
	store, err := c.loadStore()
	if err != nil {
		return domain.WriteResult{}, err
	}
	spec, ok := store.Specs[specRef]
	if !ok {
		return domain.WriteResult{}, iox.NewPrecondition(fmt.Sprintf("spec %s not found", specRef), "", nil)
	}
	if patch.Title != nil {
		spec.Title = *patch.Title
	}
	if patch.Priority != nil {
		spec.Priority = *patch.Priority
	}
	if patch.Points != nil {
		spec.Points = *patch.Points
	}
	if patch.Scope != nil {
		spec.Scope = *patch.Scope
	}
	if patch.BlockedBy != nil {
		spec.BlockedBy = append([]string(nil), (*patch.BlockedBy)...)
	}
	if patch.Body != nil {
		spec.Body = *patch.Body
	}
	if patch.Epic != nil {
		spec.Epic = *patch.Epic
	}
	if patch.Branch != nil {
		spec.Branch = *patch.Branch
	}
	if patch.Worktree != nil {
		spec.Worktree = *patch.Worktree
	}
	if patch.ForkBase != nil {
		spec.ForkBase = *patch.ForkBase
	}
	if patch.Rework != nil {
		spec.Rework = *patch.Rework
	}
	store.Specs[specRef] = spec
	if err := c.writeStore(store); err != nil {
		return domain.WriteResult{}, err
	}
	return domain.WriteResult{
		OK: true,
		Refs: []domain.Ref{
			{Code: specRef, Path: c.specPath(specRef)},
			{Code: specRef, Path: c.backlogPath()},
		},
	}, nil
}

func refsFromSpecs(specs []domain.Spec, path string) []domain.Ref {
	out := make([]domain.Ref, 0, len(specs))
	for _, spec := range specs {
		out = append(out, domain.Ref{Code: spec.Code, Path: path})
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
