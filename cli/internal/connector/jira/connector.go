package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/techreloaded-ar/ARchetipo/cli/internal/config"
	"github.com/techreloaded-ar/ARchetipo/cli/internal/connector"
	"github.com/techreloaded-ar/ARchetipo/cli/internal/domain"
	"github.com/techreloaded-ar/ARchetipo/cli/internal/iox"
)

// backlogLabel marks the issues created by ARchetipo so list/idempotency
// queries can scope to them.
const backlogLabel = "archetipo-backlog"

// Connector is the Jira Cloud implementation of the connector contract. It maps
// specs to Story issues, plan tasks to Sub-tasks, and the workflow statuses to
// Jira workflow transitions. Credentials come from the environment so secrets
// stay out of config.yaml.
type Connector struct {
	cfg   config.Config
	jira  config.JiraConfig
	doer  Doer
	email string
	token string

	// statusToJira maps a canonical status to the Jira status name; jiraToStatus
	// is its inverse. priorityToJira maps a canonical priority to a Jira
	// priority name. Built in InitializeConnector.
	statusToJira   map[string]string
	jiraToStatus   map[string]domain.Status
	priorityToJira map[domain.Priority]string
	jiraToPriority map[string]domain.Priority

	// keyByCode caches the US-NNN -> Jira issue key mapping for the process.
	keyByCode map[string]string
	ready     bool
}

// New builds a Jira connector with the real HTTP transport.
func New(cfg config.Config) *Connector {
	return NewWithDoer(cfg, NewRealDoer())
}

// NewWithDoer exposes the transport injection point used by tests.
func NewWithDoer(cfg config.Config, d Doer) *Connector {
	return &Connector{cfg: cfg, jira: cfg.Jira, doer: d, keyByCode: map[string]string{}}
}

// Register hooks the jira connector into the registry under "jira".
func Register() {
	connector.Register(config.ConnectorJira, func(cfg config.Config) (connector.Connector, error) {
		return New(cfg), nil
	})
}

// Optional capabilities (see connector.capabilities). Jira exposes the PRD —
// which it still persists as a local file — and the plan body it appends to the
// story description. It deliberately does NOT implement MockupLister,
// BoardOrderReader or ReviewStore: those have no Jira-native home today, so the
// viewer falls back gracefully. The compile-time assertions document the choice.
var (
	_ connector.PRDReader      = (*Connector)(nil)
	_ connector.PlanBodyReader = (*Connector)(nil)
)

// ReadPRD returns the PRD markdown. Like SavePRD, the jira connector keeps the
// PRD as a local file; a missing file is treated as an empty PRD.
func (c *Connector) ReadPRD(ctx context.Context) (string, error) {
	path := c.cfg.AbsPath(c.cfg.Paths.PRD)
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", iox.NewInternal(fmt.Sprintf("reading %s", path), err)
	}
	return string(b), nil
}

// ReadPlanBody returns the strategic plan body SavePlan appended to the story
// description (the text after the "---" separator). Empty when no plan section
// is present.
func (c *Connector) ReadPlanBody(ctx context.Context, specCode string) (string, error) {
	if err := c.ensureSetup(ctx); err != nil {
		return "", err
	}
	key, err := c.resolveKey(ctx, specCode)
	if err != nil {
		return "", err
	}
	var issue jiraIssue
	if err := c.do(ctx, "GET", "/rest/api/3/issue/"+key+"?fields=description", nil, &issue); err != nil {
		return "", err
	}
	desc, _ := parseDescription(c.decodeFields(issue).Description)
	idx := strings.Index(desc, "\n---\n")
	if idx == -1 {
		return "", nil
	}
	return strings.TrimSpace(desc[idx+len("\n---\n"):]), nil
}

// SETUP

func (c *Connector) InitializeConnector(ctx context.Context) (domain.SetupInfo, error) {
	if err := c.loadCredentials(); err != nil {
		return domain.SetupInfo{}, err
	}
	// Verify auth and base URL with a cheap, side-effect-free call. The
	// account id becomes the lead of an auto-created project.
	var me struct {
		AccountID string `json:"accountId"`
	}
	if err := c.do(ctx, "GET", "/rest/api/3/myself", nil, &me); err != nil {
		return domain.SetupInfo{}, err
	}
	// Resolve project + status map BEFORE buildMaps so the maps consume the
	// discovered status_map instead of the identity defaults.
	if err := c.resolveProject(ctx, me.AccountID); err != nil {
		return domain.SetupInfo{}, err
	}
	c.buildMaps()
	c.ready = true
	return domain.SetupInfo{
		Connector:   config.ConnectorJira,
		ProjectRoot: c.cfg.ProjectRoot,
		Paths:       c.cfg.Paths,
		Workflow:    c.cfg.Workflow,
	}, nil
}

// loadCredentials resolves email + token. The token is environment-only.
// project_key is intentionally NOT required here: resolveProject detects or
// creates the Jira project when the key is missing.
func (c *Connector) loadCredentials() error {
	c.email = strings.TrimSpace(c.jira.Email)
	if c.email == "" {
		c.email = strings.TrimSpace(os.Getenv("JIRA_EMAIL"))
	}
	c.token = strings.TrimSpace(os.Getenv("JIRA_API_TOKEN"))
	if c.jira.BaseURL == "" {
		// In-memory only: an env-sourced base_url must not be written back to
		// config.yaml by Save(), so c.cfg.Jira.BaseURL stays empty.
		c.jira.BaseURL = strings.TrimSpace(os.Getenv("JIRA_BASE_URL"))
	}
	if c.jira.BaseURL == "" {
		return iox.NewInvalidInput("jira.base_url is not set",
			"set jira.base_url in .archetipo/config.yaml or export JIRA_BASE_URL", nil)
	}
	if c.email == "" || c.token == "" {
		return iox.NewConnector(iox.CodeConnectorAuth,
			"jira credentials are missing",
			"export JIRA_EMAIL and JIRA_API_TOKEN (create a token at id.atlassian.com)", nil)
	}
	return nil
}

// buildMaps wires the canonical<->Jira status and priority mappings, applying
// defaults when the config omits them.
func (c *Connector) buildMaps() {
	canonicalStatuses := map[string]string{
		string(domain.StatusTodo):       string(domain.StatusTodo),
		string(domain.StatusPlanned):    string(domain.StatusPlanned),
		string(domain.StatusInProgress): string(domain.StatusInProgress),
		string(domain.StatusReview):     string(domain.StatusReview),
		string(domain.StatusDone):       string(domain.StatusDone),
	}
	for k, v := range c.jira.StatusMap {
		canonicalStatuses[k] = v
	}
	c.statusToJira = canonicalStatuses
	c.jiraToStatus = map[string]domain.Status{}
	for canonical, jiraName := range canonicalStatuses {
		c.jiraToStatus[strings.ToLower(jiraName)] = domain.Status(canonical)
	}

	priorities := map[domain.Priority]string{
		domain.PriorityHigh:   "High",
		domain.PriorityMedium: "Medium",
		domain.PriorityLow:    "Low",
	}
	for k, v := range c.jira.PriorityMap {
		priorities[domain.Priority(k)] = v
	}
	c.priorityToJira = priorities
	c.jiraToPriority = map[string]domain.Priority{}
	for canonical, jiraName := range priorities {
		c.jiraToPriority[strings.ToLower(jiraName)] = canonical
	}
}

func (c *Connector) ensureSetup(ctx context.Context) error {
	if c.ready {
		return nil
	}
	_, err := c.InitializeConnector(ctx)
	return err
}

func (c *Connector) storyType() string {
	if c.jira.StoryType != "" {
		return c.jira.StoryType
	}
	return "Story"
}

func (c *Connector) subtaskType() string {
	if c.jira.SubtaskType != "" {
		return c.jira.SubtaskType
	}
	return "Sub-task"
}

func (c *Connector) jiraStatus(s domain.Status) string {
	if name, ok := c.statusToJira[string(s)]; ok {
		return name
	}
	return string(s)
}

func (c *Connector) statusFromJira(name string) domain.Status {
	if s, ok := c.jiraToStatus[strings.ToLower(name)]; ok {
		return s
	}
	return domain.Status(name)
}

func (c *Connector) priorityName(p domain.Priority) string {
	if p == "" {
		return ""
	}
	if name, ok := c.priorityToJira[p]; ok {
		return name
	}
	return string(p)
}

func (c *Connector) priorityFromJira(name string) domain.Priority {
	if p, ok := c.jiraToPriority[strings.ToLower(name)]; ok {
		return p
	}
	return domain.Priority(name)
}

// READ

func (c *Connector) FetchBacklogItems(ctx context.Context, statusFilter domain.Status) ([]domain.Spec, error) {
	if err := c.ensureSetup(ctx); err != nil {
		return nil, err
	}
	specs, err := c.searchSpecs(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Spec, 0, len(specs))
	for _, s := range specs {
		if statusFilter != "" && s.Status != statusFilter {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}

func (c *Connector) SelectSpec(ctx context.Context, q domain.SelectQuery) (domain.Spec, error) {
	if err := c.ensureSetup(ctx); err != nil {
		return domain.Spec{}, err
	}
	specs, err := c.searchSpecs(ctx)
	if err != nil {
		return domain.Spec{}, err
	}
	if q.SpecCode != "" {
		for _, s := range specs {
			if s.Code == q.SpecCode {
				return c.ReadSpecDetail(ctx, s.Ref)
			}
		}
		return domain.Spec{}, iox.NewPrecondition(
			fmt.Sprintf("spec %s not found in project %s", q.SpecCode, c.jira.ProjectKey), "", nil)
	}
	eligible := map[domain.Status]struct{}{}
	for _, st := range q.EligibleStatuses {
		eligible[st] = struct{}{}
	}
	candidates := make([]domain.Spec, 0, len(specs))
	for _, s := range specs {
		if _, ok := eligible[s.Status]; ok {
			candidates = append(candidates, s)
		}
	}
	if len(candidates) == 0 {
		return domain.Spec{}, iox.NewPrecondition("no eligible specs in jira project", "", nil)
	}
	domain.SortByPriorityThenCode(candidates)
	return c.ReadSpecDetail(ctx, candidates[0].Ref)
}

func (c *Connector) ReadSpecDetail(ctx context.Context, ref string) (domain.Spec, error) {
	if err := c.ensureSetup(ctx); err != nil {
		return domain.Spec{}, err
	}
	key, err := c.resolveKey(ctx, ref)
	if err != nil {
		return domain.Spec{}, err
	}
	var issue jiraIssue
	if err := c.do(ctx, "GET", "/rest/api/3/issue/"+key+"?fields="+c.specFields(), nil, &issue); err != nil {
		return domain.Spec{}, err
	}
	return c.specFromIssue(issue), nil
}

func (c *Connector) ReadSpecTasks(ctx context.Context, parentRef string) ([]domain.Task, error) {
	if err := c.ensureSetup(ctx); err != nil {
		return nil, err
	}
	key, err := c.resolveKey(ctx, parentRef)
	if err != nil {
		return nil, err
	}
	issues, err := c.search(ctx, fmt.Sprintf("parent = %s ORDER BY created ASC", key),
		[]string{"summary", "status", "description"})
	if err != nil {
		return nil, err
	}
	out := make([]domain.Task, 0, len(issues))
	for _, it := range issues {
		f := c.decodeFields(it)
		id := taskIDFromSummary(f.Summary)
		if id == "" {
			continue
		}
		body, typ, deps := parseTaskDescription(f.Description)
		status := domain.StatusTodo
		if f.Status != nil {
			status = c.statusFromJira(f.Status.Name)
		}
		out = append(out, domain.Task{
			ID:           id,
			Title:        titleAfterCode(f.Summary),
			Type:         typ,
			Status:       status,
			Dependencies: deps,
			Body:         body,
			Ref:          it.Key,
		})
	}
	return out, nil
}

func (c *Connector) ReadExistingBacklog(ctx context.Context) (domain.BacklogSummary, error) {
	if err := c.ensureSetup(ctx); err != nil {
		return domain.BacklogSummary{}, err
	}
	specs, err := c.searchSpecs(ctx)
	if err != nil {
		return domain.BacklogSummary{}, err
	}
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
	return out, nil
}

// WRITE

func (c *Connector) SavePRD(ctx context.Context, content string) (domain.WriteResult, error) {
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
	existing, err := c.searchSpecs(ctx)
	if err != nil {
		return domain.WriteResult{}, err
	}
	if len(existing) > 0 {
		return domain.WriteResult{}, iox.NewConnector(iox.CodeConflict,
			fmt.Sprintf("backlog already has %d %s issues", len(existing), backlogLabel),
			"use `archetipo spec add` to add to it, or remove existing issues to recreate", nil)
	}
	return c.createSpecs(ctx, specs)
}

func (c *Connector) AppendSpecs(ctx context.Context, specs []domain.Spec) (domain.WriteResult, error) {
	if err := c.ensureSetup(ctx); err != nil {
		return domain.WriteResult{}, err
	}
	return c.createSpecs(ctx, specs)
}

func (c *Connector) createSpecs(ctx context.Context, specs []domain.Spec) (domain.WriteResult, error) {
	refs := make([]domain.Ref, 0, len(specs))
	for _, s := range specs {
		fields := map[string]any{
			"project":   map[string]string{"key": c.jira.ProjectKey},
			"summary":   s.Code + ": " + s.Title,
			"issuetype": map[string]string{"name": c.storyType()},
			"labels":    c.specLabels(s),
		}
		if desc := renderDescription(s.Body, s.Epic); desc != "" {
			fields["description"] = adfFromText(desc)
		}
		if pr := c.priorityName(s.Priority); pr != "" {
			fields["priority"] = map[string]string{"name": pr}
		}
		if c.jira.PointsField != "" && s.Points > 0 {
			fields[c.jira.PointsField] = s.Points
		}
		var created struct {
			Key string `json:"key"`
		}
		if err := c.do(ctx, "POST", "/rest/api/3/issue", map[string]any{"fields": fields}, &created); err != nil {
			return domain.WriteResult{}, err
		}
		c.keyByCode[s.Code] = created.Key
		refs = append(refs, domain.Ref{Code: s.Code, URL: c.browseURL(created.Key)})
	}
	return domain.WriteResult{OK: true, Refs: refs}, nil
}

func (c *Connector) specLabels(s domain.Spec) []string {
	labels := []string{backlogLabel}
	if s.Epic.Code != "" {
		labels = append(labels, epicLabel(s.Epic.Code))
	}
	return labels
}

func (c *Connector) SavePlan(ctx context.Context, specRef string, plan domain.PlanInput) (domain.WriteResult, error) {
	if err := c.ensureSetup(ctx); err != nil {
		return domain.WriteResult{}, err
	}
	key, err := c.resolveKey(ctx, specRef)
	if err != nil {
		return domain.WriteResult{}, err
	}
	// Append the strategic plan body to the story description, preserving the
	// epic marker.
	var issue jiraIssue
	if err := c.do(ctx, "GET", "/rest/api/3/issue/"+key+"?fields=description", nil, &issue); err != nil {
		return domain.WriteResult{}, err
	}
	current := c.decodeFields(issue).Description
	updated := strings.TrimRight(current, "\n") + "\n\n---\n\n" + strings.TrimSpace(plan.PlanBody)
	if err := c.do(ctx, "PUT", "/rest/api/3/issue/"+key,
		map[string]any{"fields": map[string]any{"description": adfFromText(updated)}}, nil); err != nil {
		return domain.WriteResult{}, err
	}
	refs := []domain.Ref{{Code: specRef, URL: c.browseURL(key)}}
	for _, t := range plan.Tasks {
		fields := map[string]any{
			"project":     map[string]string{"key": c.jira.ProjectKey},
			"parent":      map[string]string{"key": key},
			"summary":     t.ID + ": " + t.Title,
			"issuetype":   map[string]string{"name": c.subtaskType()},
			"description": adfFromText(renderTaskDescription(t)),
		}
		var created struct {
			Key string `json:"key"`
		}
		if err := c.do(ctx, "POST", "/rest/api/3/issue", map[string]any{"fields": fields}, &created); err != nil {
			return domain.WriteResult{}, err
		}
		refs = append(refs, domain.Ref{Code: t.ID, URL: c.browseURL(created.Key)})
	}
	return domain.WriteResult{OK: true, Refs: refs}, nil
}

func (c *Connector) TransitionStatus(ctx context.Context, specRef string, newStatus domain.Status) (domain.WriteResult, error) {
	if err := c.ensureSetup(ctx); err != nil {
		return domain.WriteResult{}, err
	}
	key, err := c.resolveKey(ctx, specRef)
	if err != nil {
		return domain.WriteResult{}, err
	}
	if err := c.transition(ctx, key, newStatus); err != nil {
		return domain.WriteResult{}, err
	}
	return domain.WriteResult{OK: true, Refs: []domain.Ref{{Code: specRef, URL: c.browseURL(key)}}}, nil
}

// transition moves an issue to the workflow status mapped from newStatus by
// finding the matching transition id. A no-op (already in the target status,
// no transition offered) returns nil.
func (c *Connector) transition(ctx context.Context, key string, newStatus domain.Status) error {
	target := c.jiraStatus(newStatus)
	var resp struct {
		Transitions []struct {
			ID string `json:"id"`
			To struct {
				Name string `json:"name"`
			} `json:"to"`
		} `json:"transitions"`
	}
	if err := c.do(ctx, "GET", "/rest/api/3/issue/"+key+"/transitions", nil, &resp); err != nil {
		return err
	}
	for _, t := range resp.Transitions {
		if strings.EqualFold(t.To.Name, target) {
			return c.do(ctx, "POST", "/rest/api/3/issue/"+key+"/transitions",
				map[string]any{"transition": map[string]string{"id": t.ID}}, nil)
		}
	}
	return iox.NewInvalidInput(
		fmt.Sprintf("no jira transition to status %q (mapped from %q) available on %s", target, newStatus, key),
		"check the project workflow and jira.status_map in .archetipo/config.yaml", nil)
}

func (c *Connector) CompleteTask(ctx context.Context, parentRef, taskRef string) (domain.WriteResult, error) {
	if err := c.ensureSetup(ctx); err != nil {
		return domain.WriteResult{}, err
	}
	key, err := c.resolveSubtaskKey(ctx, parentRef, taskRef)
	if err != nil {
		return domain.WriteResult{}, err
	}
	if err := c.transition(ctx, key, domain.StatusDone); err != nil {
		return domain.WriteResult{}, err
	}
	return domain.WriteResult{OK: true, Refs: []domain.Ref{{Code: taskRef, URL: c.browseURL(key)}}}, nil
}

func (c *Connector) PostComment(ctx context.Context, specRef, body string) (domain.WriteResult, error) {
	if err := c.ensureSetup(ctx); err != nil {
		return domain.WriteResult{}, err
	}
	key, err := c.resolveKey(ctx, specRef)
	if err != nil {
		return domain.WriteResult{}, err
	}
	if err := c.do(ctx, "POST", "/rest/api/3/issue/"+key+"/comment",
		map[string]any{"body": adfFromText(body)}, nil); err != nil {
		return domain.WriteResult{}, err
	}
	return domain.WriteResult{OK: true, Refs: []domain.Ref{{Code: specRef, URL: c.browseURL(key)}}}, nil
}

func (c *Connector) UpdateSpec(ctx context.Context, specRef string, patch domain.SpecUpdate) (domain.WriteResult, error) {
	if err := c.ensureSetup(ctx); err != nil {
		return domain.WriteResult{}, err
	}
	key, err := c.resolveKey(ctx, specRef)
	if err != nil {
		return domain.WriteResult{}, err
	}
	// Read the current issue so partial body/epic edits can be merged into the
	// stored description (which carries the epic marker).
	var issue jiraIssue
	if err := c.do(ctx, "GET", "/rest/api/3/issue/"+key+"?fields="+c.specFields(), nil, &issue); err != nil {
		return domain.WriteResult{}, err
	}
	cur := c.specFromIssue(issue)
	fields := map[string]any{}
	if patch.Title != nil {
		fields["summary"] = cur.Code + ": " + *patch.Title
	}
	if patch.Priority != nil {
		if pr := c.priorityName(*patch.Priority); pr != "" {
			fields["priority"] = map[string]string{"name": pr}
		}
	}
	if patch.Points != nil && c.jira.PointsField != "" {
		fields[c.jira.PointsField] = *patch.Points
	}
	body := cur.Body
	epic := cur.Epic
	if patch.Body != nil {
		body = *patch.Body
	}
	if patch.Epic != nil {
		epic = *patch.Epic
		fields["labels"] = c.specLabels(domain.Spec{Epic: epic})
	}
	if patch.Body != nil || patch.Epic != nil {
		fields["description"] = adfFromText(renderDescription(body, epic))
	}
	if len(fields) == 0 {
		return domain.WriteResult{OK: true, Refs: []domain.Ref{{Code: cur.Code, URL: c.browseURL(key)}}}, nil
	}
	if err := c.do(ctx, "PUT", "/rest/api/3/issue/"+key, map[string]any{"fields": fields}, nil); err != nil {
		return domain.WriteResult{}, err
	}
	return domain.WriteResult{OK: true, Refs: []domain.Ref{{Code: cur.Code, URL: c.browseURL(key)}}}, nil
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
			"allowed: todo, planned, in_progress, review, done", nil)
	}
	return c.TransitionStatus(ctx, specRef, status)
}

// internal helpers

// specFields is the comma-separated field list requested when reading a spec.
func (c *Connector) specFields() string {
	fields := []string{"summary", "status", "priority", "labels", "description", "issuetype"}
	if c.jira.PointsField != "" {
		fields = append(fields, c.jira.PointsField)
	}
	return strings.Join(fields, ",")
}

// searchSpecs returns every ARchetipo backlog story in the project as a Spec.
func (c *Connector) searchSpecs(ctx context.Context) ([]domain.Spec, error) {
	jql := fmt.Sprintf("project = %s AND labels = %s ORDER BY created ASC", c.jira.ProjectKey, backlogLabel)
	issues, err := c.search(ctx, jql, strings.Split(c.specFields(), ","))
	if err != nil {
		return nil, err
	}
	out := make([]domain.Spec, 0, len(issues))
	for _, it := range issues {
		spec := c.specFromIssue(it)
		if spec.Code == "" {
			continue
		}
		c.keyByCode[spec.Code] = it.Key
		out = append(out, spec)
	}
	return out, nil
}

// search runs a paginated JQL query and returns all matching issues.
func (c *Connector) search(ctx context.Context, jql string, fields []string) ([]jiraIssue, error) {
	var out []jiraIssue
	nextPageToken := ""
	for {
		body := map[string]any{
			"jql":        jql,
			"fields":     fields,
			"maxResults": 100,
		}
		if nextPageToken != "" {
			body["nextPageToken"] = nextPageToken
		}
		var resp struct {
			Issues        []jiraIssue `json:"issues"`
			IsLast        bool        `json:"isLast"`
			NextPageToken string      `json:"nextPageToken"`
		}
		if err := c.do(ctx, "POST", "/rest/api/3/search/jql", body, &resp); err != nil {
			return nil, err
		}
		out = append(out, resp.Issues...)
		if resp.IsLast || resp.NextPageToken == "" || len(resp.Issues) == 0 {
			break
		}
		nextPageToken = resp.NextPageToken
	}
	return out, nil
}

// jiraIssue keeps the fields object raw so the dynamic story-points custom
// field can be read without a fixed struct tag.
type jiraIssue struct {
	Key    string          `json:"key"`
	Fields json.RawMessage `json:"fields"`
}

type knownFields struct {
	Summary        string          `json:"summary"`
	DescriptionRaw json.RawMessage `json:"description"`
	Description    string          `json:"-"`
	Labels         []string        `json:"labels"`
	Status         *jiraNamed      `json:"status"`
	Priority       *jiraNamed      `json:"priority"`
	IssueType      *jiraNamed      `json:"issuetype"`
}

type jiraNamed struct {
	Name string `json:"name"`
}

func (c *Connector) decodeFields(it jiraIssue) knownFields {
	var f knownFields
	_ = json.Unmarshal(it.Fields, &f)
	f.Description = textFromADF(f.DescriptionRaw)
	return f
}

func (c *Connector) specFromIssue(it jiraIssue) domain.Spec {
	f := c.decodeFields(it)
	body, epic := parseDescription(f.Description)
	status := domain.StatusTodo
	if f.Status != nil {
		status = c.statusFromJira(f.Status.Name)
	}
	priority := domain.Priority("")
	if f.Priority != nil {
		priority = c.priorityFromJira(f.Priority.Name)
	}
	if epic.Code == "" {
		for _, l := range f.Labels {
			if code := epicCodeFromLabel(l); code != "" {
				epic.Code = code
				break
			}
		}
	}
	return domain.Spec{
		Code:     codeFromSummary(f.Summary),
		Title:    titleAfterCode(f.Summary),
		Epic:     epic,
		Priority: priority,
		Points:   c.pointsFromFields(it.Fields),
		Status:   status,
		Body:     body,
		Ref:      it.Key,
		URL:      c.browseURL(it.Key),
	}
}

func (c *Connector) pointsFromFields(raw json.RawMessage) int {
	if c.jira.PointsField == "" {
		return 0
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return 0
	}
	v, ok := m[c.jira.PointsField]
	if !ok {
		return 0
	}
	var f float64
	if err := json.Unmarshal(v, &f); err != nil {
		return 0
	}
	return int(f)
}

func (c *Connector) browseURL(key string) string {
	if key == "" {
		return ""
	}
	return strings.TrimRight(c.jira.BaseURL, "/") + "/browse/" + key
}

// resolveKey turns a ref (a US-NNN code or a Jira key) into a Jira issue key.
func (c *Connector) resolveKey(ctx context.Context, ref string) (string, error) {
	if codeRegexp.MatchString(ref + ":") {
		// ref is a US-NNN code.
		if key, ok := c.keyByCode[ref]; ok {
			return key, nil
		}
		if _, err := c.searchSpecs(ctx); err != nil {
			return "", err
		}
		if key, ok := c.keyByCode[ref]; ok {
			return key, nil
		}
		return "", iox.NewPrecondition(fmt.Sprintf("spec %s not found in jira", ref), "", nil)
	}
	// Assume it is already a Jira key.
	return ref, nil
}

func (c *Connector) resolveSubtaskKey(ctx context.Context, parentRef, taskRef string) (string, error) {
	if taskIDFromSummary(taskRef+":") == "" {
		// Not a TASK-NNN code: assume it is already a Jira key.
		return taskRef, nil
	}
	tasks, err := c.ReadSpecTasks(ctx, parentRef)
	if err != nil {
		return "", err
	}
	for _, t := range tasks {
		if t.ID == taskRef {
			return t.Ref, nil
		}
	}
	return "", iox.NewPrecondition(
		fmt.Sprintf("task %s not found under %s", taskRef, parentRef), "", nil)
}
