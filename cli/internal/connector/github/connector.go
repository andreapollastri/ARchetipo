package github

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/techreloaded-ar/ARchetipo/cli/internal/config"
	"github.com/techreloaded-ar/ARchetipo/cli/internal/connector"
	"github.com/techreloaded-ar/ARchetipo/cli/internal/domain"
	"github.com/techreloaded-ar/ARchetipo/cli/internal/iox"
)

// Connector is the GitHub Issues + Projects v2 implementation.
//
// All side-effecting operations go through Runner so tests can swap in a fake
// without touching the network. The connector caches metadata fetched during
// InitializeConnector for the lifetime of the instance — skills are expected
// to use a single CLI invocation per logical operation, so this cache rarely
// outlives a single run.
type Connector struct {
	cfg    config.Config
	runner Runner
	state  state
}

// state holds metadata learned at init time. Reset between init calls.
type state struct {
	repo    *domain.RepoInfo
	project *domain.ProjectInfo
	// items maps issue number to project item id (needed by transition_status).
	items map[int]string
	// specs caches project board rows for the lifetime of one CLI process.
	specs       []domain.Spec
	itemsLoaded bool
	// labels caches known label names so create_labels can avoid duplicate
	// gh label create calls.
	labels map[string]struct{}
}

// New constructs a Connector. The realRunner forks `gh`; tests can pass a
// stub via NewWithRunner.
func New(cfg config.Config) *Connector {
	return NewWithRunner(cfg, NewRealRunner())
}

// NewWithRunner exposes the runner injection point so tests can record/replay
// gh invocations.
func NewWithRunner(cfg config.Config, r Runner) *Connector {
	return &Connector{cfg: cfg, runner: r, state: state{items: map[int]string{}, labels: map[string]struct{}{}}}
}

// Register hooks the github connector into the registry under "github".
func Register() {
	connector.Register(config.ConnectorGitHub, func(cfg config.Config) (connector.Connector, error) {
		return New(cfg), nil
	})
}

// SETUP

func (c *Connector) InitializeConnector(ctx context.Context) (domain.SetupInfo, error) {
	repo, project, err := c.resolveBoard(ctx)
	if err != nil {
		return domain.SetupInfo{}, err
	}
	c.state.repo = repo
	c.state.project = project
	return domain.SetupInfo{
		Connector:   config.ConnectorGitHub,
		ProjectRoot: c.cfg.ProjectRoot,
		Paths:       c.cfg.Paths,
		Workflow:    c.cfg.Workflow,
		Repo:        repo,
		Project:     project,
	}, nil
}

// READ

func (c *Connector) FetchBacklogItems(ctx context.Context, statusFilter domain.Status) ([]domain.Spec, error) {
	if err := c.ensureSetup(ctx); err != nil {
		return nil, err
	}
	items, err := c.listProjectItems(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Spec, 0, len(items))
	for _, it := range items {
		if statusFilter != "" && it.Status != statusFilter {
			continue
		}
		out = append(out, it)
	}
	return out, nil
}

func (c *Connector) SelectSpec(ctx context.Context, q domain.SelectQuery) (domain.Spec, error) {
	if err := c.ensureSetup(ctx); err != nil {
		return domain.Spec{}, err
	}
	items, err := c.listProjectItems(ctx)
	if err != nil {
		return domain.Spec{}, err
	}
	if q.SpecCode != "" {
		for _, s := range items {
			if s.Code == q.SpecCode {
				return c.fillSpecDetail(ctx, s)
			}
		}
		return domain.Spec{}, iox.NewPrecondition(
			fmt.Sprintf("spec %s not found in project board", q.SpecCode), "", nil)
	}
	eligible := map[domain.Status]struct{}{}
	for _, st := range q.EligibleStatuses {
		eligible[st] = struct{}{}
	}
	candidates := make([]domain.Spec, 0, len(items))
	for _, s := range items {
		if _, ok := eligible[s.Status]; ok {
			candidates = append(candidates, s)
		}
	}
	if len(candidates) == 0 {
		return domain.Spec{}, iox.NewPrecondition(
			"no eligible specs in project board", "", nil)
	}
	domain.SortByPriorityThenCode(candidates)
	return c.fillSpecDetail(ctx, candidates[0])
}

func (c *Connector) ReadSpecDetail(ctx context.Context, ref string) (domain.Spec, error) {
	if err := c.ensureSetup(ctx); err != nil {
		return domain.Spec{}, err
	}
	num, err := c.resolveIssueNumber(ctx, ref)
	if err != nil {
		return domain.Spec{}, err
	}
	spec, err := c.viewIssueAsSpec(ctx, num)
	if err != nil {
		return domain.Spec{}, err
	}
	// Enrich with project board data (status, priority, points, epic).
	items, err := c.listProjectItems(ctx)
	if err != nil {
		return domain.Spec{}, err
	}
	for _, item := range items {
		if item.Ref == spec.Ref {
			spec.Status = item.Status
			spec.Priority = item.Priority
			spec.Points = item.Points
			if spec.Epic.Code == "" {
				spec.Epic = item.Epic
			}
			break
		}
	}
	return spec, nil
}

func (c *Connector) ReadSpecTasks(ctx context.Context, parentRef string) ([]domain.Task, error) {
	if err := c.ensureSetup(ctx); err != nil {
		return nil, err
	}
	num, err := c.resolveIssueNumber(ctx, parentRef)
	if err != nil {
		return nil, err
	}
	subs, err := c.listSubIssues(ctx, num)
	if err != nil {
		return nil, err
	}
	return subs, nil
}

func (c *Connector) ReadExistingBacklog(ctx context.Context) (domain.BacklogSummary, error) {
	if err := c.ensureSetup(ctx); err != nil {
		return domain.BacklogSummary{}, err
	}
	if c.state.itemsLoaded {
		return summarizeSpecs(c.state.specs), nil
	}
	var raw []struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
	}
	if err := runJSON(ctx, c.runner, &raw,
		"api", fmt.Sprintf("repos/%s/issues?state=all&labels=archetipo-backlog&per_page=100", c.state.repo.Slug),
	); err != nil {
		return domain.BacklogSummary{}, err
	}
	out := domain.BacklogSummary{}
	seen := map[string]domain.Epic{}
	for _, r := range raw {
		code := codeFromTitle(r.Title)
		title := titleAfterCode(r.Title)
		out.Codes = append(out.Codes, code)
		out.Titles = append(out.Titles, title)
		for _, l := range r.Labels {
			if strings.HasPrefix(l.Name, "EP-") {
				if _, ok := seen[l.Name]; !ok {
					seen[l.Name] = domain.Epic{Code: epicCodeFromLabel(l.Name), Title: epicTitleFromLabel(l.Name)}
				}
			}
		}
	}
	sort.Strings(out.Codes)
	if len(out.Codes) > 0 {
		out.LastCode = out.Codes[len(out.Codes)-1]
	}
	for _, e := range seen {
		out.Epics = append(out.Epics, e)
	}
	sort.Slice(out.Epics, func(i, j int) bool { return out.Epics[i].Code < out.Epics[j].Code })
	return out, nil
}

func summarizeSpecs(specs []domain.Spec) domain.BacklogSummary {
	out := domain.BacklogSummary{}
	seen := map[string]domain.Epic{}
	for _, s := range specs {
		out.Codes = append(out.Codes, s.Code)
		out.Titles = append(out.Titles, s.Title)
		if s.Epic.Code != "" {
			seen[s.Epic.Code] = s.Epic
		}
	}
	sort.Strings(out.Codes)
	if len(out.Codes) > 0 {
		out.LastCode = out.Codes[len(out.Codes)-1]
	}
	for _, e := range seen {
		out.Epics = append(out.Epics, e)
	}
	sort.Slice(out.Epics, func(i, j int) bool { return out.Epics[i].Code < out.Epics[j].Code })
	return out
}

// WRITE

func (c *Connector) SavePRD(ctx context.Context, content string) (domain.WriteResult, error) {
	// PRD lives as a local markdown file even with the github connector,
	// matching the contract documented in github.md.
	path := c.cfg.AbsPath(c.cfg.Paths.PRD)
	if err := writeFile(path, []byte(content)); err != nil {
		return domain.WriteResult{}, err
	}
	return domain.WriteResult{OK: true, Refs: []domain.Ref{{Path: path}}}, nil
}

func (c *Connector) SaveInitialBacklog(ctx context.Context, specs []domain.Spec) (domain.WriteResult, error) {
	if err := c.ensureSetup(ctx); err != nil {
		return domain.WriteResult{}, err
	}
	if err := c.idempotencyCheck(ctx); err != nil {
		return domain.WriteResult{}, err
	}
	return c.createSpecsAndAttach(ctx, specs)
}

func (c *Connector) AppendSpecs(ctx context.Context, specs []domain.Spec) (domain.WriteResult, error) {
	if err := c.ensureSetup(ctx); err != nil {
		return domain.WriteResult{}, err
	}
	return c.createSpecsAndAttach(ctx, specs)
}

func (c *Connector) SavePlan(ctx context.Context, specRef string, plan domain.PlanInput) (domain.WriteResult, error) {
	if err := c.ensureSetup(ctx); err != nil {
		return domain.WriteResult{}, err
	}
	parentNum, err := c.resolveIssueNumber(ctx, specRef)
	if err != nil {
		return domain.WriteResult{}, err
	}
	parent, err := c.viewIssueRaw(ctx, parentNum)
	if err != nil {
		return domain.WriteResult{}, err
	}
	// Append the strategic plan body to the parent issue.
	updatedBody := parent.Body + "\n\n---\n\n" + strings.TrimSpace(plan.PlanBody)
	if _, err := c.editIssueBody(ctx, parentNum, updatedBody); err != nil {
		return domain.WriteResult{}, err
	}
	// Find the EP- label on the parent so sub-issues inherit it.
	epicLabel := ""
	for _, l := range parent.Labels {
		if strings.HasPrefix(l.Name, "EP-") {
			epicLabel = l.Name
			break
		}
	}
	refs := []domain.Ref{{Code: specRef, Number: parentNum, URL: parent.URL}}
	subNumbers := make([]int, 0, len(plan.Tasks))
	subIDs := make([]int64, 0, len(plan.Tasks))
	for _, t := range plan.Tasks {
		labels := []string{}
		if epicLabel != "" {
			labels = append(labels, epicLabel)
		}
		created, err := c.createIssue(ctx, fmt.Sprintf("%s: %s", t.ID, t.Title), t.Body, labels)
		if err != nil {
			return domain.WriteResult{}, err
		}
		if created.ID == 0 {
			return domain.WriteResult{}, iox.NewConnector(iox.CodeConnectorBackend,
				"GitHub issue create response missing REST id",
				"check gh CLI and GitHub REST API compatibility", nil)
		}
		subNumbers = append(subNumbers, created.Number)
		subIDs = append(subIDs, created.ID)
		refs = append(refs, domain.Ref{Code: t.ID, Number: created.Number, URL: created.URL})
	}
	// Link sub-issues to parent via REST API.
	for i := range subNumbers {
		if _, stderr, err := c.runner.Run(ctx, nil,
			"api", "-X", "POST",
			fmt.Sprintf("repos/%s/issues/%d/sub_issues", c.state.repo.Slug, parentNum),
			"-F", fmt.Sprintf("sub_issue_id=%d", subIDs[i]),
			"-H", "X-GitHub-Api-Version: 2026-03-10",
		); err != nil {
			return domain.WriteResult{}, classify(err, stderr)
		}
	}
	return domain.WriteResult{OK: true, Refs: refs}, nil
}

func (c *Connector) TransitionStatus(ctx context.Context, specRef string, newStatus domain.Status) (domain.WriteResult, error) {
	if err := c.ensureSetup(ctx); err != nil {
		return domain.WriteResult{}, err
	}
	num, err := c.resolveIssueNumber(ctx, specRef)
	if err != nil {
		return domain.WriteResult{}, err
	}
	itemID, ok := c.state.items[num]
	if !ok {
		// Refresh items in case the issue was added since init.
		if _, lerr := c.listProjectItems(ctx); lerr != nil {
			return domain.WriteResult{}, lerr
		}
		itemID = c.state.items[num]
	}
	if itemID == "" {
		return domain.WriteResult{}, iox.NewPrecondition(
			"issue not on project board", "add it via `gh project item-add`", nil)
	}
	optionID := c.state.project.Fields.StatusOptions[string(newStatus)]
	if optionID == "" {
		return domain.WriteResult{}, iox.NewInvalidInput(
			fmt.Sprintf("status %q has no option id in project", newStatus),
			"check workflow.statuses in config.yaml matches the project Status options", nil)
	}
	if err := runGraphQL(ctx, c.runner, updateSingleSelectFieldMutation, map[string]string{
		"projectId": c.state.project.NodeID,
		"itemId":    itemID,
		"fieldId":   c.state.project.Fields.StatusFieldID,
		"optionId":  optionID,
	}, nil); err != nil {
		return domain.WriteResult{}, err
	}
	c.updateCachedSpecStatus(num, newStatus)
	return domain.WriteResult{OK: true, Refs: []domain.Ref{{Code: specRef, Number: num}}}, nil
}

func (c *Connector) CompleteTask(ctx context.Context, parentRef, taskRef string) (domain.WriteResult, error) {
	if err := c.ensureSetup(ctx); err != nil {
		return domain.WriteResult{}, err
	}
	num, err := c.resolveSubIssueNumber(ctx, parentRef, taskRef)
	if err != nil {
		return domain.WriteResult{}, err
	}
	if _, err := c.closeIssue(ctx, num); err != nil {
		return domain.WriteResult{}, err
	}
	return domain.WriteResult{OK: true, Refs: []domain.Ref{{Code: taskRef, Number: num}}}, nil
}

func (c *Connector) PostComment(ctx context.Context, specRef, body string) (domain.WriteResult, error) {
	if err := c.ensureSetup(ctx); err != nil {
		return domain.WriteResult{}, err
	}
	num, err := c.resolveIssueNumber(ctx, specRef)
	if err != nil {
		return domain.WriteResult{}, err
	}
	if _, err := c.postIssueComment(ctx, num, body); err != nil {
		return domain.WriteResult{}, err
	}
	return domain.WriteResult{OK: true, Refs: []domain.Ref{{Code: specRef, Number: num}}}, nil
}

func (c *Connector) UpdateSpec(ctx context.Context, specRef string, patch domain.SpecUpdate) (domain.WriteResult, error) {
	return domain.WriteResult{}, iox.NewConnector(
		iox.CodeConnectorBackend,
		"spec metadata update is not supported by the github connector yet",
		"edit the issue directly on GitHub for now",
		nil,
	)
}

func (c *Connector) MoveBoardCard(ctx context.Context, specRef, targetColumn string, anchor domain.ReorderAnchor) (domain.WriteResult, error) {
	statusByColumn := map[string]domain.Status{
		"todo":        domain.StatusTodo,
		"planned":     domain.StatusPlanned,
		"in_progress": domain.StatusInProgress,
		"review":      domain.StatusReview,
		"done":        domain.StatusDone,
	}
	status, ok := statusByColumn[targetColumn]
	if !ok {
		return domain.WriteResult{}, iox.NewInvalidInput(
			fmt.Sprintf("unknown board column %q", targetColumn),
			"allowed: todo, planned, in_progress, review, done",
			nil,
		)
	}
	return c.TransitionStatus(ctx, specRef, status)
}

// internal helpers below

func (c *Connector) ensureSetup(ctx context.Context) error {
	if c.state.repo != nil && c.state.project != nil {
		return nil
	}
	_, err := c.InitializeConnector(ctx)
	return err
}

// detectRepo runs `gh repo view` and decodes the result.
func (c *Connector) detectRepo(ctx context.Context) (*domain.RepoInfo, error) {
	var raw struct {
		ID    string `json:"id"`
		Owner struct {
			Login string `json:"login"`
		} `json:"owner"`
		Name          string `json:"name"`
		NameWithOwner string `json:"nameWithOwner"`
	}
	if err := runJSON(ctx, c.runner, &raw,
		"repo", "view", "--json", "id,owner,name,nameWithOwner",
	); err != nil {
		return nil, err
	}
	return &domain.RepoInfo{
		Owner:  raw.Owner.Login,
		Name:   raw.Name,
		Slug:   raw.NameWithOwner,
		NodeID: raw.ID,
	}, nil
}

// loadProjectFields fetches only the field metadata needed to resolve names
// to ids. It intentionally avoids `gh project field-list`, which also pulls
// project items and is very expensive in GraphQL credits.
//
// The custom field named "Story Points" on GitHub Projects keeps that exact
// user-visible label — only the Go-side identifier (PointsFieldID) was renamed
// as part of the spec/story refactoring.
func (c *Connector) loadProjectFields(ctx context.Context, _ *domain.RepoInfo, number int, id, url string) (*domain.ProjectInfo, error) {
	var fl struct {
		Node struct {
			Fields struct {
				Nodes []struct {
					TypeName string `json:"__typename"`
					ID       string `json:"id"`
					Name     string `json:"name"`
					DataType string `json:"dataType"`
					Options  []struct {
						ID   string `json:"id"`
						Name string `json:"name"`
					} `json:"options,omitempty"`
				} `json:"nodes"`
			} `json:"fields"`
		} `json:"node"`
	}
	if err := runGraphQL(ctx, c.runner, projectFieldsQuery, map[string]string{
		"projectId": id,
	}, &fl); err != nil {
		return nil, err
	}
	pi := &domain.ProjectInfo{
		Number: number,
		NodeID: id,
		URL:    url,
		Fields: domain.ProjectFields{
			StatusOptions:   map[string]string{},
			PriorityOptions: map[string]string{},
			EpicOptions:     map[string]string{},
		},
	}
	for _, f := range fl.Node.Fields.Nodes {
		switch f.Name {
		case "Status":
			pi.Fields.StatusFieldID = f.ID
			for _, o := range f.Options {
				pi.Fields.StatusOptions[o.Name] = o.ID
			}
		case "Priority":
			pi.Fields.PriorityFieldID = f.ID
			for _, o := range f.Options {
				pi.Fields.PriorityOptions[o.Name] = o.ID
			}
		case "Story Points":
			pi.Fields.PointsFieldID = f.ID
		case "Epic":
			pi.Fields.EpicFieldID = f.ID
			for _, o := range f.Options {
				pi.Fields.EpicOptions[o.Name] = o.ID
			}
		}
	}
	return pi, nil
}

// listProjectItems pulls all items on the board (capped at 200) and converts
// them to []domain.Spec. Caches issue->itemID mapping in c.state.items.
func (c *Connector) listProjectItems(ctx context.Context) ([]domain.Spec, error) {
	if c.state.itemsLoaded {
		return append([]domain.Spec(nil), c.state.specs...), nil
	}
	out := []domain.Spec{}
	after := ""
	for {
		var raw struct {
			Node struct {
				Items struct {
					PageInfo struct {
						EndCursor   string `json:"endCursor"`
						HasNextPage bool   `json:"hasNextPage"`
					} `json:"pageInfo"`
					Nodes []projectItemNode `json:"nodes"`
				} `json:"items"`
			} `json:"node"`
		}
		vars := map[string]string{"projectId": c.state.project.NodeID}
		if after != "" {
			vars["after"] = after
		}
		if err := runGraphQL(ctx, c.runner, projectItemsQuery, vars, &raw); err != nil {
			return nil, err
		}
		for _, it := range raw.Node.Items.Nodes {
			spec, ok := it.spec()
			if !ok {
				continue
			}
			c.state.items[it.Content.Number] = it.ID
			out = append(out, spec)
		}
		if !raw.Node.Items.PageInfo.HasNextPage {
			break
		}
		after = raw.Node.Items.PageInfo.EndCursor
		if after == "" {
			break
		}
	}
	c.state.specs = append([]domain.Spec(nil), out...)
	c.state.itemsLoaded = true
	return out, nil
}

type projectItemNode struct {
	ID      string `json:"id"`
	Content struct {
		TypeName string `json:"__typename"`
		Number   int    `json:"number"`
		Title    string `json:"title"`
		URL      string `json:"url"`
		Labels   struct {
			Nodes []struct {
				Name string `json:"name"`
			} `json:"nodes"`
		} `json:"labels"`
	} `json:"content"`
	Status   *projectFieldValue `json:"status"`
	Priority *projectFieldValue `json:"priority"`
	Points   *projectFieldValue `json:"points"`
	Epic     *projectFieldValue `json:"epic"`
}

type projectFieldValue struct {
	TypeName string  `json:"__typename"`
	Name     string  `json:"name"`
	Text     string  `json:"text"`
	Number   float64 `json:"number"`
}

func (it projectItemNode) spec() (domain.Spec, bool) {
	if it.Content.TypeName != "Issue" || it.Content.Number == 0 {
		return domain.Spec{}, false
	}
	code := codeFromTitle(it.Content.Title)
	if code == "" || !it.hasLabel("archetipo-backlog") {
		return domain.Spec{}, false
	}
	status := domain.StatusTodo
	if it.Status != nil && it.Status.Name != "" {
		status = domain.Status(it.Status.Name)
	}
	epicLabel := it.firstLabelWithPrefix("EP-")
	if it.Epic != nil {
		switch {
		case it.Epic.Name != "":
			epicLabel = it.Epic.Name
		case it.Epic.Text != "":
			epicLabel = it.Epic.Text
		}
	}
	points := 0
	if it.Points != nil {
		points = int(it.Points.Number)
	}
	priority := domain.Priority("")
	if it.Priority != nil {
		priority = domain.Priority(it.Priority.Name)
	}
	return domain.Spec{
		Code:     code,
		Title:    titleAfterCode(it.Content.Title),
		Status:   status,
		Priority: priority,
		Points:   points,
		Epic:     domain.Epic{Code: epicCodeFromLabel(epicLabel), Title: epicTitleFromLabel(epicLabel)},
		Ref:      strconv.Itoa(it.Content.Number),
		URL:      it.Content.URL,
	}, true
}

func (it projectItemNode) hasLabel(name string) bool {
	for _, l := range it.Content.Labels.Nodes {
		if l.Name == name {
			return true
		}
	}
	return false
}

func (it projectItemNode) firstLabelWithPrefix(prefix string) string {
	for _, l := range it.Content.Labels.Nodes {
		if strings.HasPrefix(l.Name, prefix) {
			return l.Name
		}
	}
	return ""
}

// fillSpecDetail enriches a spec with its issue body.
func (c *Connector) fillSpecDetail(ctx context.Context, s domain.Spec) (domain.Spec, error) {
	if s.Ref == "" {
		return s, nil
	}
	num, err := strconv.Atoi(s.Ref)
	if err != nil {
		return s, nil
	}
	det, err := c.viewIssueAsSpec(ctx, num)
	if err != nil {
		return s, err
	}
	det.Status = s.Status
	det.Priority = s.Priority
	det.Points = s.Points
	if det.Epic.Code == "" {
		det.Epic = s.Epic
	}
	return det, nil
}

func (c *Connector) viewIssueAsSpec(ctx context.Context, num int) (domain.Spec, error) {
	raw, err := c.viewIssueRaw(ctx, num)
	if err != nil {
		return domain.Spec{}, err
	}
	return domain.Spec{
		Code:  codeFromTitle(raw.Title),
		Title: titleAfterCode(raw.Title),
		Body:  raw.Body,
		Ref:   strconv.Itoa(num),
		URL:   raw.URL,
	}, nil
}

type rawIssue struct {
	Number  int    `json:"number"`
	ID      int64  `json:"id"`
	NodeID  string `json:"node_id"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	URL     string `json:"url"`
	HTMLURL string `json:"html_url"`
	Labels  []struct {
		Name string `json:"name"`
	} `json:"labels"`
}

func (c *Connector) viewIssueRaw(ctx context.Context, num int) (rawIssue, error) {
	var raw rawIssue
	if err := runJSON(ctx, c.runner, &raw,
		"api", fmt.Sprintf("repos/%s/issues/%d", c.state.repo.Slug, num),
	); err != nil {
		return rawIssue{}, err
	}
	if raw.URL == "" {
		raw.URL = raw.HTMLURL
	}
	return raw, nil
}

func (c *Connector) createIssue(ctx context.Context, title, body string, labels []string) (rawIssue, error) {
	args := []string{
		"api", "-X", "POST", fmt.Sprintf("repos/%s/issues", c.state.repo.Slug),
		"-f", "title=" + title,
		"-f", "body=" + body,
	}
	for _, label := range labels {
		args = append(args, "-f", "labels[]="+label)
	}
	var raw rawIssue
	if err := runJSON(ctx, c.runner, &raw, args...); err != nil {
		return rawIssue{}, err
	}
	if raw.URL == "" {
		raw.URL = raw.HTMLURL
	}
	if raw.Number == 0 || raw.NodeID == "" {
		return rawIssue{}, iox.NewConnector(iox.CodeConnectorBackend,
			"GitHub issue create response missing number or node_id",
			"check gh CLI and GitHub REST API compatibility", nil)
	}
	return raw, nil
}

func (c *Connector) editIssueBody(ctx context.Context, num int, body string) (rawIssue, error) {
	var raw rawIssue
	if err := runJSON(ctx, c.runner, &raw,
		"api", "-X", "PATCH", fmt.Sprintf("repos/%s/issues/%d", c.state.repo.Slug, num),
		"-f", "body="+body,
	); err != nil {
		return rawIssue{}, err
	}
	if raw.URL == "" {
		raw.URL = raw.HTMLURL
	}
	return raw, nil
}

func (c *Connector) closeIssue(ctx context.Context, num int) (rawIssue, error) {
	var raw rawIssue
	if err := runJSON(ctx, c.runner, &raw,
		"api", "-X", "PATCH", fmt.Sprintf("repos/%s/issues/%d", c.state.repo.Slug, num),
		"-f", "state=closed",
	); err != nil {
		return rawIssue{}, err
	}
	if raw.URL == "" {
		raw.URL = raw.HTMLURL
	}
	return raw, nil
}

func (c *Connector) postIssueComment(ctx context.Context, num int, body string) (rawIssue, error) {
	var raw rawIssue
	if err := runJSON(ctx, c.runner, &raw,
		"api", "-X", "POST", fmt.Sprintf("repos/%s/issues/%d/comments", c.state.repo.Slug, num),
		"-f", "body="+body,
	); err != nil {
		return rawIssue{}, err
	}
	if raw.URL == "" {
		raw.URL = raw.HTMLURL
	}
	return raw, nil
}

func (c *Connector) listSubIssues(ctx context.Context, parentNum int) ([]domain.Task, error) {
	var raw []struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		Body   string `json:"body"`
		State  string `json:"state"`
	}
	if err := runJSON(ctx, c.runner, &raw,
		"api",
		fmt.Sprintf("repos/%s/issues/%d/sub_issues", c.state.repo.Slug, parentNum),
		"-H", "X-GitHub-Api-Version: 2026-03-10",
	); err != nil {
		return nil, err
	}
	out := make([]domain.Task, 0, len(raw))
	for _, s := range raw {
		t := domain.Task{
			ID:    taskIDFromTitle(s.Title),
			Title: titleAfterTaskID(s.Title),
			Body:  s.Body,
			Ref:   strconv.Itoa(s.Number),
		}
		if strings.EqualFold(s.State, "closed") {
			t.Status = domain.StatusDone
		} else {
			t.Status = domain.StatusTodo
		}
		out = append(out, t)
	}
	return out, nil
}

func (c *Connector) resolveIssueNumber(ctx context.Context, ref string) (int, error) {
	if n, err := strconv.Atoi(ref); err == nil {
		return n, nil
	}
	// Treat ref as a US-XXX code: search project items.
	items, err := c.listProjectItems(ctx)
	if err != nil {
		return 0, err
	}
	for _, s := range items {
		if s.Code == ref {
			return strconv.Atoi(s.Ref)
		}
	}
	return 0, iox.NewPrecondition(
		fmt.Sprintf("spec %s not found", ref), "", nil)
}

func (c *Connector) resolveSubIssueNumber(ctx context.Context, parentRef, taskRef string) (int, error) {
	if n, err := strconv.Atoi(taskRef); err == nil {
		return n, nil
	}
	parentNum, err := c.resolveIssueNumber(ctx, parentRef)
	if err != nil {
		return 0, err
	}
	subs, err := c.listSubIssues(ctx, parentNum)
	if err != nil {
		return 0, err
	}
	for _, t := range subs {
		if t.ID == taskRef {
			return strconv.Atoi(t.Ref)
		}
	}
	return 0, iox.NewPrecondition(
		fmt.Sprintf("task %s not found under %s", taskRef, parentRef), "", nil)
}

// idempotencyCheck refuses to re-create the initial backlog when issues
// labelled archetipo-backlog already exist. Maps to step 1 of the original
// `save_initial_backlog`.
func (c *Connector) idempotencyCheck(ctx context.Context) error {
	var raw []struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
	}
	if err := runJSON(ctx, c.runner, &raw,
		"api", fmt.Sprintf("repos/%s/issues?state=all&labels=archetipo-backlog&per_page=100", c.state.repo.Slug),
	); err != nil {
		return err
	}
	if len(raw) > 0 {
		return iox.NewConnector(iox.CodeConflict,
			fmt.Sprintf("backlog already has %d archetipo-backlog issues", len(raw)),
			"use `archetipo spec add` to add to it, or close existing issues to recreate", nil)
	}
	return nil
}

// createSpecsAndAttach creates one issue per spec, ensures the archetipo
// labels exist, adds each issue to the project board and fills the field
// values. Returns the WriteResult with refs for every created issue.
func (c *Connector) createSpecsAndAttach(ctx context.Context, specs []domain.Spec) (domain.WriteResult, error) {
	if err := c.ensureLabel(ctx, "archetipo-backlog", "Spec generated by ARchetipo backlog", "1D76DB"); err != nil {
		return domain.WriteResult{}, err
	}
	epicLabels := map[string]struct{}{}
	for _, s := range specs {
		if s.Epic.Code != "" {
			epicLabels[s.Epic.Code+": ["+s.Epic.Title+"]"] = struct{}{}
		}
	}
	for label := range epicLabels {
		if err := c.ensureLabel(ctx, label, "Epic", "5319E7"); err != nil {
			return domain.WriteResult{}, err
		}
	}
	refs := make([]domain.Ref, 0, len(specs))
	for _, s := range specs {
		title := s.Code + ": " + s.Title
		labels := []string{"archetipo-backlog"}
		if s.Epic.Code != "" {
			labels = append(labels, s.Epic.Code+": ["+s.Epic.Title+"]")
		}
		created, err := c.createIssue(ctx, title, s.Body, labels)
		if err != nil {
			return domain.WriteResult{}, err
		}
		num := created.Number
		refs = append(refs, domain.Ref{Code: s.Code, Number: num, URL: created.URL})
		// Add to project + set fields. Status field is the only one always
		// present; priority/points/epic depend on whether the project
		// declares those fields (loadProjectFields populated them or not).
		if c.state.project == nil {
			continue
		}
		var addResp struct {
			AddProjectV2ItemById struct {
				Item struct {
					ID string `json:"id"`
				} `json:"item"`
			} `json:"addProjectV2ItemById"`
		}
		if err := runGraphQL(ctx, c.runner, addProjectItemMutation, map[string]string{
			"projectId": c.state.project.NodeID,
			"contentId": created.NodeID,
		}, &addResp); err != nil {
			return domain.WriteResult{}, err
		}
		itemID := addResp.AddProjectV2ItemById.Item.ID
		c.state.items[num] = itemID
		if optID := c.state.project.Fields.StatusOptions[string(s.Status)]; optID != "" {
			_ = runGraphQL(ctx, c.runner, updateSingleSelectFieldMutation, map[string]string{
				"projectId": c.state.project.NodeID,
				"itemId":    itemID,
				"fieldId":   c.state.project.Fields.StatusFieldID,
				"optionId":  optID,
			}, nil)
		}
		if c.state.project.Fields.PriorityFieldID != "" {
			if optID := c.state.project.Fields.PriorityOptions[string(s.Priority)]; optID != "" {
				_ = runGraphQL(ctx, c.runner, updateSingleSelectFieldMutation, map[string]string{
					"projectId": c.state.project.NodeID,
					"itemId":    itemID,
					"fieldId":   c.state.project.Fields.PriorityFieldID,
					"optionId":  optID,
				}, nil)
			}
		}
		if c.state.project.Fields.PointsFieldID != "" && s.Points > 0 {
			_ = runGraphQL(ctx, c.runner, updateNumberFieldMutation, map[string]string{
				"projectId": c.state.project.NodeID,
				"itemId":    itemID,
				"fieldId":   c.state.project.Fields.PointsFieldID,
				"value":     strconv.Itoa(s.Points),
			}, nil)
		}
	}
	c.invalidateItemsCache()
	return domain.WriteResult{OK: true, Refs: refs}, nil
}

// ensureLabel creates a label on the repo if not already known. `gh label
// create --force` is idempotent.
func (c *Connector) ensureLabel(ctx context.Context, name, description, color string) error {
	if _, ok := c.state.labels[name]; ok {
		return nil
	}
	_, stderr, err := c.runner.Run(ctx, nil,
		"label", "create", name,
		"--repo", c.state.repo.Slug,
		"--description", description,
		"--color", color,
		"--force",
	)
	if err != nil {
		return classify(err, stderr)
	}
	c.state.labels[name] = struct{}{}
	return nil
}

func (c *Connector) invalidateItemsCache() {
	c.state.specs = nil
	c.state.itemsLoaded = false
}

func (c *Connector) updateCachedSpecStatus(num int, status domain.Status) {
	if !c.state.itemsLoaded {
		return
	}
	for i := range c.state.specs {
		if c.state.specs[i].Ref == strconv.Itoa(num) {
			c.state.specs[i].Status = status
			return
		}
	}
}
