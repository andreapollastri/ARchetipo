// Package inmemory is a reference Connector implementation backed by Go maps.
//
// Two roles:
//
//  1. shared conformance test target: pins the expected behaviour that every
//     real connector must reproduce.
//  2. test fixture for cli sub-commands when wiring stdout JSON: avoids
//     touching the filesystem or the gh CLI.
package inmemory

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/techreloaded-ar/ARchetipo/cli/internal/config"
	"github.com/techreloaded-ar/ARchetipo/cli/internal/connector"
	"github.com/techreloaded-ar/ARchetipo/cli/internal/domain"
	"github.com/techreloaded-ar/ARchetipo/cli/internal/iox"
)

// Connector is an in-memory store of specs, tasks, plan bodies and the PRD.
type Connector struct {
	mu sync.Mutex

	cfg   config.Config
	prd   string
	specs []domain.Spec
	tasks map[string][]domain.Task
	plans map[string]string // specCode -> plan body
}

// New returns an empty in-memory connector backed by cfg.
func New(cfg config.Config) *Connector {
	return &Connector{cfg: cfg, tasks: map[string][]domain.Task{}, plans: map[string]string{}}
}

func (c *Connector) InitializeConnector(ctx context.Context) (domain.SetupInfo, error) {
	return domain.SetupInfo{
		Connector:   "inmemory",
		ProjectRoot: c.cfg.ProjectRoot,
		Paths:       c.cfg.Paths,
		Workflow:    c.cfg.Workflow,
	}, nil
}

func (c *Connector) FetchBacklogItems(ctx context.Context, statusFilter domain.Status) ([]domain.Spec, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]domain.Spec, 0, len(c.specs))
	for _, s := range c.specs {
		if statusFilter != "" && s.Status != statusFilter {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}

func (c *Connector) SelectSpec(ctx context.Context, q domain.SelectQuery) (domain.Spec, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if q.SpecCode != "" {
		for _, s := range c.specs {
			if s.Code == q.SpecCode {
				return s, nil
			}
		}
		return domain.Spec{}, iox.NewPrecondition(
			fmt.Sprintf("spec %s not found", q.SpecCode),
			"check the backlog or run `archetipo spec list`", nil)
	}
	eligible := make(map[domain.Status]struct{}, len(q.EligibleStatuses))
	for _, st := range q.EligibleStatuses {
		eligible[st] = struct{}{}
	}
	candidates := make([]domain.Spec, 0, len(c.specs))
	for _, s := range c.specs {
		if _, ok := eligible[s.Status]; ok {
			candidates = append(candidates, s)
		}
	}
	if len(candidates) == 0 {
		return domain.Spec{}, iox.NewPrecondition("no eligible specs",
			"adjust --eligible or run `archetipo spec list`", nil)
	}
	domain.SortByPriorityThenCode(candidates)
	return candidates[0], nil
}

func (c *Connector) ReadSpecDetail(ctx context.Context, ref string) (domain.Spec, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range c.specs {
		if s.Code == ref || s.Ref == ref {
			return s, nil
		}
	}
	return domain.Spec{}, iox.NewPrecondition(
		fmt.Sprintf("spec %s not found", ref), "", nil)
}

func (c *Connector) ReadSpecTasks(ctx context.Context, parentRef string) ([]domain.Task, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	tasks, ok := c.tasks[parentRef]
	if !ok {
		// fall back: maybe parentRef is a code, look it up
		for _, s := range c.specs {
			if s.Code == parentRef || s.Ref == parentRef {
				return append([]domain.Task(nil), c.tasks[s.Code]...), nil
			}
		}
		return nil, iox.NewPrecondition(
			fmt.Sprintf("no plan for spec %s", parentRef),
			"run `archetipo plan save` first", nil)
	}
	return append([]domain.Task(nil), tasks...), nil
}

func (c *Connector) ReadExistingBacklog(ctx context.Context) (domain.BacklogSummary, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := domain.BacklogSummary{}
	seenEpics := map[string]domain.Epic{}
	for _, s := range c.specs {
		out.Codes = append(out.Codes, s.Code)
		out.Titles = append(out.Titles, s.Title)
		if _, ok := seenEpics[s.Epic.Code]; !ok && s.Epic.Code != "" {
			seenEpics[s.Epic.Code] = s.Epic
		}
	}
	sort.Strings(out.Codes)
	if len(out.Codes) > 0 {
		out.LastCode = out.Codes[len(out.Codes)-1]
	}
	for _, e := range seenEpics {
		out.Epics = append(out.Epics, e)
	}
	sort.Slice(out.Epics, func(i, j int) bool { return out.Epics[i].Code < out.Epics[j].Code })
	return out, nil
}

func (c *Connector) SavePRD(ctx context.Context, content string) (domain.WriteResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prd = content
	return domain.WriteResult{OK: true, Refs: []domain.Ref{{Path: c.cfg.Paths.PRD}}}, nil
}

func (c *Connector) SaveInitialBacklog(ctx context.Context, specs []domain.Spec) (domain.WriteResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.specs) > 0 {
		return domain.WriteResult{}, iox.NewConnector(iox.CodeConflict,
			"backlog is not empty", "use `archetipo spec add` to add specs", nil)
	}
	c.specs = append([]domain.Spec(nil), specs...)
	refs := make([]domain.Ref, 0, len(specs))
	for _, s := range specs {
		refs = append(refs, domain.Ref{Code: s.Code})
	}
	return domain.WriteResult{OK: true, Refs: refs}, nil
}

func (c *Connector) AppendSpecs(ctx context.Context, specs []domain.Spec) (domain.WriteResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.specs = append(c.specs, specs...)
	refs := make([]domain.Ref, 0, len(specs))
	for _, s := range specs {
		refs = append(refs, domain.Ref{Code: s.Code})
	}
	return domain.WriteResult{OK: true, Refs: refs}, nil
}

func (c *Connector) SavePlan(ctx context.Context, specRef string, plan domain.PlanInput) (domain.WriteResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	code := c.codeFor(specRef)
	if code == "" {
		return domain.WriteResult{}, iox.NewPrecondition(
			fmt.Sprintf("spec %s not found", specRef), "", nil)
	}
	c.plans[code] = plan.PlanBody
	tasks := append([]domain.Task(nil), plan.Tasks...)
	c.tasks[code] = tasks
	refs := make([]domain.Ref, 0, len(tasks)+1)
	refs = append(refs, domain.Ref{Code: code})
	for _, t := range tasks {
		refs = append(refs, domain.Ref{Code: t.ID})
	}
	return domain.WriteResult{OK: true, Refs: refs}, nil
}

func (c *Connector) TransitionStatus(ctx context.Context, specRef string, newStatus domain.Status) (domain.WriteResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.specs {
		if c.specs[i].Code == specRef || c.specs[i].Ref == specRef {
			c.specs[i].Status = newStatus
			return domain.WriteResult{OK: true, Refs: []domain.Ref{{Code: c.specs[i].Code}}}, nil
		}
	}
	return domain.WriteResult{}, iox.NewPrecondition(
		fmt.Sprintf("spec %s not found", specRef), "", nil)
}

func (c *Connector) CompleteTask(ctx context.Context, parentRef, taskRef string) (domain.WriteResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	code := c.codeFor(parentRef)
	tasks, ok := c.tasks[code]
	if !ok {
		return domain.WriteResult{}, iox.NewPrecondition(
			fmt.Sprintf("no plan for %s", parentRef), "", nil)
	}
	for i := range tasks {
		if tasks[i].ID == taskRef || tasks[i].Ref == taskRef {
			tasks[i].Status = domain.StatusDone
			c.tasks[code] = tasks
			return domain.WriteResult{OK: true, Refs: []domain.Ref{{Code: tasks[i].ID}}}, nil
		}
	}
	return domain.WriteResult{}, iox.NewPrecondition(
		fmt.Sprintf("task %s not found in %s", taskRef, parentRef), "", nil)
}

func (c *Connector) MoveBoardCard(ctx context.Context, specRef, targetColumn string, anchor domain.ReorderAnchor) (domain.WriteResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	targetStatus, ok := map[string]domain.Status{
		"todo":        domain.StatusTodo,
		"planned":     domain.StatusPlanned,
		"in_progress": domain.StatusInProgress,
		"review":      domain.StatusReview,
		"done":        domain.StatusDone,
	}[targetColumn]
	if !ok {
		return domain.WriteResult{}, iox.NewInvalidInput(fmt.Sprintf("unknown board column %q", targetColumn), "", nil)
	}
	for i := range c.specs {
		if c.specs[i].Code == specRef || c.specs[i].Ref == specRef {
			c.specs[i].Status = targetStatus
			spec := c.specs[i]
			c.specs = append(c.specs[:i], c.specs[i+1:]...)
			insertAt := len(c.specs)
			switch {
			case anchor.Before != "" && anchor.After != "":
				return domain.WriteResult{}, iox.NewInvalidInput("before and after are mutually exclusive", "", nil)
			case anchor.Before != "":
				insertAt = -1
				for j := range c.specs {
					if c.specs[j].Code == anchor.Before {
						insertAt = j
						break
					}
				}
				if insertAt == -1 {
					return domain.WriteResult{}, iox.NewPrecondition(fmt.Sprintf("spec %s not found", anchor.Before), "", nil)
				}
			case anchor.After != "":
				insertAt = -1
				for j := range c.specs {
					if c.specs[j].Code == anchor.After {
						insertAt = j + 1
						break
					}
				}
				if insertAt == -1 {
					return domain.WriteResult{}, iox.NewPrecondition(fmt.Sprintf("spec %s not found", anchor.After), "", nil)
				}
			}
			c.specs = append(c.specs, domain.Spec{})
			copy(c.specs[insertAt+1:], c.specs[insertAt:])
			c.specs[insertAt] = spec
			return domain.WriteResult{OK: true, Refs: []domain.Ref{{Code: spec.Code}}}, nil
		}
	}
	return domain.WriteResult{}, iox.NewPrecondition(fmt.Sprintf("spec %s not found", specRef), "", nil)
}

func (c *Connector) PostComment(ctx context.Context, specRef, body string) (domain.WriteResult, error) {
	// In-memory connector: silent ok, like filefs no-op.
	return domain.WriteResult{OK: true}, nil
}

func (c *Connector) UpdateSpec(ctx context.Context, specRef string, patch domain.SpecUpdate) (domain.WriteResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.specs {
		if c.specs[i].Code == specRef || c.specs[i].Ref == specRef {
			if patch.Title != nil {
				c.specs[i].Title = *patch.Title
			}
			if patch.Priority != nil {
				c.specs[i].Priority = *patch.Priority
			}
			if patch.Points != nil {
				c.specs[i].Points = *patch.Points
			}
			if patch.Scope != nil {
				c.specs[i].Scope = *patch.Scope
			}
			if patch.BlockedBy != nil {
				c.specs[i].BlockedBy = append([]string(nil), (*patch.BlockedBy)...)
			}
			if patch.Body != nil {
				c.specs[i].Body = *patch.Body
			}
			if patch.Epic != nil {
				c.specs[i].Epic = *patch.Epic
			}
			return domain.WriteResult{OK: true, Refs: []domain.Ref{{Code: c.specs[i].Code}}}, nil
		}
	}
	return domain.WriteResult{}, iox.NewPrecondition(
		fmt.Sprintf("spec %s not found", specRef), "", nil)
}

// codeFor resolves a ref (code or numeric ref) into the spec code. Empty
// when the ref is unknown.
func (c *Connector) codeFor(ref string) string {
	for _, s := range c.specs {
		if s.Code == ref || s.Ref == ref {
			return s.Code
		}
	}
	return ""
}

// Register integrates the inmemory connector with the registry under the name
// "inmemory". Skills do not select it via config.yaml; tests do.
func Register() {
	connector.Register("inmemory", func(cfg config.Config) (connector.Connector, error) {
		return New(cfg), nil
	})
}
