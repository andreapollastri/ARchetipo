package jira

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/techreloaded-ar/ARchetipo/cli/internal/config"
	"github.com/techreloaded-ar/ARchetipo/cli/internal/domain"
)

// fakeJira is a minimal in-memory Jira Cloud backend exercising exactly the
// REST endpoints the connector calls. It keeps enough state (issues, statuses,
// subtasks) to drive the connector through a full backlog -> plan -> transition
// lifecycle without a network.
type fakeJira struct {
	t        *testing.T
	issues   map[string]*fakeIssue
	order    []string // creation order, so search results are deterministic
	nextID   int
	calls    []string
	comments []string

	// Project resolution backend. projects is what /project/search returns;
	// projectCreates records every POST /project payload. createProjectStatus
	// forces a non-201 answer (e.g. 403); projectKeyCollisions makes the first
	// N creates fail with a 400 naming the projectKey field. projectStatuses,
	// when nil, defaults to the five canonical statuses on Story and Sub-task
	// so pre-existing tests keep their identity mapping.
	projects             []fakeProject
	projectCreates       []map[string]any
	createProjectStatus  int
	projectKeyCollisions int
	projectStatuses      []map[string]any

	// Workflow provisioning backend. globalStatuses backs /statuses/search;
	// statusCreates and workflowUpdates record the write payloads. A
	// successful workflow update rewrites projectStatuses so the connector's
	// re-match sees the new workflow. provisionStatus forces a non-2xx on the
	// write endpoints (e.g. 403 when the token lacks Administer Jira).
	globalStatuses  []map[string]any
	statusCreates   []map[string]any
	workflowUpdates []map[string]any
	provisionStatus int
}

type fakeProject struct {
	Key  string
	Name string
}

type fakeIssue struct {
	Key       string
	Fields    map[string]any
	StatusNm  string
	ParentKey string
}

func newFakeJira(t *testing.T) *fakeJira {
	return &fakeJira{t: t, issues: map[string]*fakeIssue{}, nextID: 1}
}

func (f *fakeJira) Do(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		body, _ = io.ReadAll(req.Body)
	}
	path := req.URL.Path
	f.calls = append(f.calls, req.Method+" "+path)
	switch {
	case req.Method == "GET" && path == "/rest/api/3/myself":
		return jsonResp(200, map[string]any{"accountId": "acc-1"}), nil
	case req.Method == "GET" && path == "/rest/api/3/project/search":
		return f.projectSearch(req), nil
	case req.Method == "POST" && path == "/rest/api/3/project":
		return f.createProject(body), nil
	case req.Method == "GET" && strings.HasPrefix(path, "/rest/api/3/project/") && strings.HasSuffix(path, "/statuses"):
		return f.projectStatusesResp(), nil
	case req.Method == "GET" && strings.HasPrefix(path, "/rest/api/3/project/"):
		return f.projectDetails(path), nil
	case req.Method == "GET" && path == "/rest/api/3/workflowscheme/project":
		return jsonResp(200, map[string]any{"values": []map[string]any{{
			"workflowScheme": map[string]any{
				"defaultWorkflow":   "Software Simplified Workflow",
				"issueTypeMappings": map[string]any{},
			},
		}}}), nil
	case req.Method == "GET" && path == "/rest/api/3/statuses/search":
		return f.statusSearch(req), nil
	case req.Method == "POST" && path == "/rest/api/3/statuses":
		return f.createStatuses(body), nil
	case req.Method == "POST" && path == "/rest/api/3/workflows":
		return f.workflowsRead(), nil
	case req.Method == "POST" && path == "/rest/api/3/workflows/update":
		return f.workflowsUpdate(body), nil
	case req.Method == "POST" && path == "/rest/api/3/issue":
		return f.createIssue(body), nil
	case req.Method == "POST" && path == "/rest/api/3/search/jql":
		return f.search(body), nil
	case req.Method == "GET" && strings.HasPrefix(path, "/rest/api/3/issue/") && strings.HasSuffix(path, "/transitions"):
		return f.transitionsList(), nil
	case req.Method == "POST" && strings.HasPrefix(path, "/rest/api/3/issue/") && strings.HasSuffix(path, "/transitions"):
		return f.applyTransition(path, body), nil
	case req.Method == "POST" && strings.HasPrefix(path, "/rest/api/3/issue/") && strings.HasSuffix(path, "/comment"):
		return f.addComment(body), nil
	case req.Method == "GET" && strings.HasPrefix(path, "/rest/api/3/issue/"):
		return f.getIssue(path), nil
	case req.Method == "PUT" && strings.HasPrefix(path, "/rest/api/3/issue/"):
		return f.updateIssue(path, body), nil
	}
	f.t.Fatalf("unexpected jira call: %s %s", req.Method, path)
	return nil, nil
}

func (f *fakeJira) projectSearch(req *http.Request) *http.Response {
	query := req.URL.Query().Get("query")
	var values []map[string]any
	for _, p := range f.projects {
		if query != "" && !strings.Contains(strings.ToLower(p.Name), strings.ToLower(query)) {
			continue
		}
		values = append(values, map[string]any{"key": p.Key, "name": p.Name})
	}
	return jsonResp(200, map[string]any{"values": values, "isLast": true})
}

func (f *fakeJira) createProject(body []byte) *http.Response {
	var in map[string]any
	_ = json.Unmarshal(body, &in)
	f.projectCreates = append(f.projectCreates, in)
	if f.createProjectStatus != 0 {
		return jsonResp(f.createProjectStatus, map[string]any{"errorMessages": []string{"forbidden"}})
	}
	if f.projectKeyCollisions > 0 {
		f.projectKeyCollisions--
		return jsonResp(400, map[string]any{"errors": map[string]string{"projectKey": "Project key already exists"}})
	}
	key, _ := in["key"].(string)
	name, _ := in["name"].(string)
	f.projects = append(f.projects, fakeProject{Key: key, Name: name})
	return jsonResp(201, map[string]any{"key": key})
}

func (f *fakeJira) projectStatusesResp() *http.Response {
	if f.projectStatuses != nil {
		return jsonResp(200, f.projectStatuses)
	}
	canonical := []map[string]any{
		{"name": "TODO"}, {"name": "PLANNED"}, {"name": "IN PROGRESS"},
		{"name": "REVIEW"}, {"name": "DONE"},
	}
	return jsonResp(200, []map[string]any{
		{"name": "Story", "subtask": false, "statuses": canonical},
		{"name": "Sub-task", "subtask": true, "statuses": canonical},
	})
}

// storyStatusNames returns the status names the fake currently exposes on the
// first non-subtask issue type (the connector's story type).
func (f *fakeJira) storyStatusNames() []string {
	if f.projectStatuses == nil {
		return []string{"TODO", "PLANNED", "IN PROGRESS", "REVIEW", "DONE"}
	}
	for _, it := range f.projectStatuses {
		if sub, _ := it["subtask"].(bool); sub {
			continue
		}
		var names []string
		for _, s := range it["statuses"].([]map[string]any) {
			names = append(names, s["name"].(string))
		}
		return names
	}
	return nil
}

func (f *fakeJira) projectDetails(path string) *http.Response {
	key := strings.TrimPrefix(path, "/rest/api/3/project/")
	return jsonResp(200, map[string]any{
		"id": "10001", "key": key,
		"issueTypes": []map[string]any{
			{"id": "it-story", "name": "Story"},
			{"id": "it-sub", "name": "Sub-task"},
		},
	})
}

func (f *fakeJira) statusSearch(req *http.Request) *http.Response {
	q := strings.ToLower(req.URL.Query().Get("searchString"))
	var values []map[string]any
	for _, s := range f.globalStatuses {
		name, _ := s["name"].(string)
		if q == "" || strings.Contains(strings.ToLower(name), q) {
			values = append(values, s)
		}
	}
	return jsonResp(200, map[string]any{"values": values})
}

func (f *fakeJira) createStatuses(body []byte) *http.Response {
	if f.provisionStatus != 0 {
		return jsonResp(f.provisionStatus, map[string]any{"errorMessages": []string{"forbidden"}})
	}
	var in struct {
		Statuses []map[string]any `json:"statuses"`
	}
	_ = json.Unmarshal(body, &in)
	var out []map[string]any
	for _, s := range in.Statuses {
		id := "st-" + strconv.Itoa(f.nextID)
		f.nextID++
		s["id"] = id
		f.statusCreates = append(f.statusCreates, s)
		f.globalStatuses = append(f.globalStatuses, s)
		out = append(out, map[string]any{"id": id, "name": s["name"]})
	}
	return jsonResp(200, out)
}

// workflowsRead serves the bulk read of the story workflow: every status the
// fake currently exposes, referenced as "ref-<name>", plus one global
// transition per status carrying an action that must survive the round trip.
func (f *fakeJira) workflowsRead() *http.Response {
	var defs, wfStatuses, transitions []map[string]any
	for i, n := range f.storyStatusNames() {
		ref := "ref-" + n
		defs = append(defs, map[string]any{
			"id": ref, "statusReference": ref, "name": n, "statusCategory": "TODO",
		})
		wfStatuses = append(wfStatuses, map[string]any{
			"statusReference": ref, "properties": map[string]any{}, "deprecated": false,
		})
		transitions = append(transitions, map[string]any{
			"id": strconv.Itoa((i+1)*10 + 1), "type": "GLOBAL", "toStatusReference": ref,
			"name": n, "links": []any{},
			"actions": []map[string]any{{"ruleKey": "system:update-field"}},
		})
	}
	return jsonResp(200, map[string]any{
		"statuses": defs,
		"workflows": []map[string]any{{
			"id":          "wf-1",
			"name":        "Software Simplified Workflow",
			"version":     map[string]any{"id": "v1", "versionNumber": 0},
			"statuses":    wfStatuses,
			"transitions": transitions,
		}},
	})
}

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// globalStatusExists reports whether a global status with the given id exists.
func (f *fakeJira) globalStatusExists(id string) bool {
	for _, s := range f.globalStatuses {
		if sid, _ := s["id"].(string); sid == id {
			return true
		}
	}
	return false
}

// workflowsUpdate applies a bulk workflow update: it records the payload and
// rebuilds projectStatuses from the statuses the workflow references, so the
// connector's re-fetch observes the new workflow. Referencing a status absent
// from the request's statuses list is a 400, like in the real API.
func (f *fakeJira) workflowsUpdate(body []byte) *http.Response {
	if f.provisionStatus != 0 {
		return jsonResp(f.provisionStatus, map[string]any{"errorMessages": []string{"forbidden"}})
	}
	var raw map[string]any
	_ = json.Unmarshal(body, &raw)
	f.workflowUpdates = append(f.workflowUpdates, raw)
	var in struct {
		Statuses []struct {
			ID              string `json:"id"`
			Name            string `json:"name"`
			StatusReference string `json:"statusReference"`
		} `json:"statuses"`
		Workflows []struct {
			Statuses []struct {
				StatusReference string `json:"statusReference"`
			} `json:"statuses"`
		} `json:"workflows"`
	}
	_ = json.Unmarshal(body, &in)
	if len(in.Workflows) == 0 {
		return jsonResp(400, map[string]any{"errorMessages": []string{"no workflows in update"}})
	}
	// Real Jira validates references strictly: a status echoed from the read
	// keeps its server-issued reference, anything else must be a
	// client-generated UUID carrying the numeric id of an existing status.
	served := map[string]bool{}
	for _, n := range f.storyStatusNames() {
		served["ref-"+n] = true
	}
	for _, s := range in.Statuses {
		if served[s.StatusReference] {
			continue
		}
		if !uuidRe.MatchString(s.StatusReference) {
			return jsonResp(400, map[string]any{"errorMessages": []string{
				"The reference " + s.StatusReference + " is not a UUID."}})
		}
		if !f.globalStatusExists(s.ID) {
			return jsonResp(400, map[string]any{"errorMessages": []string{
				"Status name \"" + s.Name + "\" already in use."}})
		}
	}
	names := map[string]string{}
	for _, s := range in.Statuses {
		names[s.StatusReference] = s.Name
	}
	var statuses []map[string]any
	for _, ref := range in.Workflows[0].Statuses {
		n, ok := names[ref.StatusReference]
		if !ok || n == "" {
			return jsonResp(400, map[string]any{
				"errorMessages": []string{"workflow references a status missing from the statuses list: " + ref.StatusReference},
			})
		}
		statuses = append(statuses, map[string]any{"name": n})
	}
	f.projectStatuses = []map[string]any{
		{"name": "Story", "subtask": false, "statuses": statuses},
		{"name": "Sub-task", "subtask": true, "statuses": statuses},
	}
	return jsonResp(200, nil)
}

func (f *fakeJira) createIssue(body []byte) *http.Response {
	var in struct {
		Fields map[string]any `json:"fields"`
	}
	_ = json.Unmarshal(body, &in)
	key := "ARCH-" + strconv.Itoa(f.nextID)
	f.nextID++
	iss := &fakeIssue{Key: key, Fields: in.Fields, StatusNm: "TODO"}
	if p, ok := in.Fields["parent"].(map[string]any); ok {
		iss.ParentKey, _ = p["key"].(string)
	}
	f.issues[key] = iss
	f.order = append(f.order, key)
	return jsonResp(201, map[string]any{"key": key})
}

func (f *fakeJira) keyFromPath(path string) string {
	rest := strings.TrimPrefix(path, "/rest/api/3/issue/")
	return strings.SplitN(rest, "/", 2)[0]
}

func (f *fakeJira) getIssue(path string) *http.Response {
	key := f.keyFromPath(path)
	// path may carry "?fields=..." already stripped by net/url into RawQuery,
	// but here keyFromPath split on '/', so trim a stray '?'.
	if i := strings.IndexByte(key, '?'); i >= 0 {
		key = key[:i]
	}
	iss, ok := f.issues[key]
	if !ok {
		return jsonResp(404, map[string]any{"errorMessages": []string{"not found"}})
	}
	return jsonResp(200, map[string]any{"key": key, "fields": f.fieldsOf(iss)})
}

func (f *fakeJira) fieldsOf(iss *fakeIssue) map[string]any {
	out := map[string]any{}
	for k, v := range iss.Fields {
		out[k] = v
	}
	out["status"] = map[string]any{"name": iss.StatusNm}
	return out
}

func (f *fakeJira) updateIssue(path string, body []byte) *http.Response {
	key := f.keyFromPath(path)
	iss, ok := f.issues[key]
	if !ok {
		return jsonResp(404, map[string]any{"errorMessages": []string{"not found"}})
	}
	var in struct {
		Fields map[string]any `json:"fields"`
	}
	_ = json.Unmarshal(body, &in)
	for k, v := range in.Fields {
		iss.Fields[k] = v
	}
	return jsonResp(204, nil)
}

func (f *fakeJira) transitionsList() *http.Response {
	// Offer a transition to every canonical status name used in tests.
	names := []string{"TODO", "PLANNED", "IN PROGRESS", "REVIEW", "DONE"}
	var ts []map[string]any
	for i, n := range names {
		ts = append(ts, map[string]any{"id": strconv.Itoa(i + 1), "to": map[string]any{"name": n}})
	}
	return jsonResp(200, map[string]any{"transitions": ts})
}

func (f *fakeJira) applyTransition(path string, body []byte) *http.Response {
	key := f.keyFromPath(path)
	iss, ok := f.issues[key]
	if !ok {
		return jsonResp(404, map[string]any{"errorMessages": []string{"not found"}})
	}
	var in struct {
		Transition struct {
			ID string `json:"id"`
		} `json:"transition"`
	}
	_ = json.Unmarshal(body, &in)
	names := map[string]string{"1": "TODO", "2": "PLANNED", "3": "IN PROGRESS", "4": "REVIEW", "5": "DONE"}
	if n, ok := names[in.Transition.ID]; ok {
		iss.StatusNm = n
	}
	return jsonResp(204, nil)
}

func (f *fakeJira) addComment(body []byte) *http.Response {
	var in struct {
		Body json.RawMessage `json:"body"`
	}
	_ = json.Unmarshal(body, &in)
	f.comments = append(f.comments, textFromADF(in.Body))
	return jsonResp(201, map[string]any{"id": "c1"})
}

func (f *fakeJira) search(body []byte) *http.Response {
	var in struct {
		JQL           string `json:"jql"`
		NextPageToken string `json:"nextPageToken"`
	}
	_ = json.Unmarshal(body, &in)
	var matched []map[string]any
	for _, key := range f.order {
		iss := f.issues[key]
		if !f.matchesJQL(iss, in.JQL) {
			continue
		}
		matched = append(matched, map[string]any{"key": iss.Key, "fields": f.fieldsOf(iss)})
	}
	pageSize := 1
	start := 0
	if in.NextPageToken != "" {
		var err error
		start, err = strconv.Atoi(in.NextPageToken)
		if err != nil {
			return jsonResp(400, map[string]any{"errorMessages": []string{"bad nextPageToken"}})
		}
	}
	end := start + pageSize
	if end > len(matched) {
		end = len(matched)
	}
	isLast := end >= len(matched)
	next := ""
	if !isLast {
		next = strconv.Itoa(end)
	}
	return jsonResp(200, map[string]any{
		"isLast": isLast, "nextPageToken": next, "issues": matched[start:end],
	})
}

func (f *fakeJira) matchesJQL(iss *fakeIssue, jql string) bool {
	if strings.Contains(jql, "labels = "+backlogLabel) {
		labels, _ := iss.Fields["labels"].([]any)
		for _, l := range labels {
			if s, _ := l.(string); s == backlogLabel {
				return true
			}
		}
		return false
	}
	if strings.Contains(jql, "parent = ") {
		// extract the key after "parent = "
		idx := strings.Index(jql, "parent = ")
		rest := jql[idx+len("parent = "):]
		want := strings.Fields(rest)[0]
		return iss.ParentKey == want
	}
	return false
}

func jsonResp(status int, payload any) *http.Response {
	var buf []byte
	if payload != nil {
		buf, _ = json.Marshal(payload)
	}
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(bytes.NewReader(buf)),
		Header:     make(http.Header),
	}
}

func testConfig() config.Config {
	cfg := config.Default()
	cfg.Connector = config.ConnectorJira
	cfg.Jira = config.JiraConfig{BaseURL: "https://acme.atlassian.net", ProjectKey: "ARCH"}
	return cfg
}

func newTestConnector(t *testing.T) (*Connector, *fakeJira) {
	t.Setenv("JIRA_EMAIL", "bot@acme.com")
	t.Setenv("JIRA_API_TOKEN", "tok")
	f := newFakeJira(t)
	return NewWithDoer(testConfig(), f), f
}

func sampleSpecs() []domain.Spec {
	return []domain.Spec{
		{Code: "US-001", Title: "Setup", Epic: domain.Epic{Code: "EP-001", Title: "Foundations"},
			Priority: domain.PriorityHigh, Points: 3, Status: domain.StatusTodo, Body: "## Spec\n\nAs a user, I want X."},
		{Code: "US-002", Title: "Auth", Epic: domain.Epic{Code: "EP-001", Title: "Foundations"},
			Priority: domain.PriorityMedium, Points: 5, Status: domain.StatusTodo, Body: "## Spec\n\nLogin."},
	}
}

func TestInitialize_VerifiesAuth(t *testing.T) {
	c, _ := newTestConnector(t)
	info, err := c.InitializeConnector(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Connector != config.ConnectorJira {
		t.Errorf("connector name = %q", info.Connector)
	}
	if info.Workflow.Statuses.Todo == "" {
		t.Errorf("workflow statuses not populated")
	}
}

func TestInitialize_MissingCredentials(t *testing.T) {
	t.Setenv("JIRA_EMAIL", "")
	t.Setenv("JIRA_API_TOKEN", "")
	c := NewWithDoer(testConfig(), newFakeJira(t))
	if _, err := c.InitializeConnector(context.Background()); err == nil {
		t.Fatal("expected auth error when credentials are missing")
	}
}

// TestSpecRoundTrip covers the Jira-specific encoding the shared conformance
// suite does not assert: the epic title is carried in a description marker, the
// marker is stripped from the returned body, and the priority is mapped through
// the default High/Medium/Low names. Idempotency (a second SaveInitialBacklog
// must conflict) is also Jira-path specific.
func TestSpecRoundTrip(t *testing.T) {
	c, f := newTestConnector(t)
	ctx := context.Background()
	specs := sampleSpecs()
	if _, err := c.SaveInitialBacklog(ctx, specs); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SaveInitialBacklog(ctx, specs); err == nil {
		t.Fatal("expected conflict on second SaveInitialBacklog")
	}
	det, err := c.ReadSpecDetail(ctx, "US-001")
	if err != nil {
		t.Fatal(err)
	}
	if det.Epic.Code != "EP-001" || det.Epic.Title != "Foundations" {
		t.Errorf("epic lost: %+v", det.Epic)
	}
	if strings.Contains(det.Body, "archetipo:epic") {
		t.Errorf("epic marker leaked into body: %q", det.Body)
	}
	if det.Priority != domain.PriorityHigh {
		t.Errorf("priority lost: %s", det.Priority)
	}
	listed, err := c.FetchBacklogItems(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != len(specs) {
		t.Fatalf("expected paginated list to return %d specs, got %d", len(specs), len(listed))
	}
	if !f.called("POST /rest/api/3/search/jql") {
		t.Fatalf("search did not use Jira v3 /search/jql, calls: %v", f.calls)
	}
	if f.calledPrefix("/rest/api/2/") {
		t.Fatalf("unexpected Jira v2 call: %v", f.calls)
	}
}

// TestReadPlanBody verifies the PlanBodyReader optional capability: the plan
// body SavePlan appends to the story description is returned separately.
func TestReadPlanBody(t *testing.T) {
	c, _ := newTestConnector(t)
	ctx := context.Background()
	if _, err := c.SaveInitialBacklog(ctx, sampleSpecs()); err != nil {
		t.Fatal(err)
	}
	plan := domain.PlanInput{PlanBody: "## Soluzione Tecnica\n\nSpiegazione."}
	if _, err := c.SavePlan(ctx, "US-001", plan); err != nil {
		t.Fatal(err)
	}
	body, err := c.ReadPlanBody(ctx, "US-001")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "Soluzione Tecnica") {
		t.Errorf("plan body not returned: %q", body)
	}
}

func TestReadSpecTasksRoundTrip(t *testing.T) {
	c, _ := newTestConnector(t)
	ctx := context.Background()
	if _, err := c.SaveInitialBacklog(ctx, sampleSpecs()); err != nil {
		t.Fatal(err)
	}
	plan := domain.PlanInput{
		PlanBody: "## Piano",
		Tasks: []domain.Task{{
			ID:           "TASK-001",
			Title:        "Preparare schema",
			Type:         domain.TaskImpl,
			Dependencies: []string{"TASK-000"},
			Body:         "Implementare il primo pezzo.",
		}},
	}
	if _, err := c.SavePlan(ctx, "US-001", plan); err != nil {
		t.Fatal(err)
	}
	tasks, err := c.ReadSpecTasks(ctx, "US-001")
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].ID != "TASK-001" || tasks[0].Type != domain.TaskImpl {
		t.Fatalf("task metadata lost: %+v", tasks[0])
	}
	if len(tasks[0].Dependencies) != 1 || tasks[0].Dependencies[0] != "TASK-000" {
		t.Fatalf("task dependencies lost: %+v", tasks[0].Dependencies)
	}
	if strings.Contains(tasks[0].Body, "archetipo:task") {
		t.Fatalf("task marker leaked into body: %q", tasks[0].Body)
	}
}

func TestPostCommentUsesADF(t *testing.T) {
	c, f := newTestConnector(t)
	ctx := context.Background()
	if _, err := c.SaveInitialBacklog(ctx, sampleSpecs()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PostComment(ctx, "US-001", "Prima riga\nSeconda riga"); err != nil {
		t.Fatal(err)
	}
	if len(f.comments) != 1 || f.comments[0] != "Prima riga\nSeconda riga" {
		t.Fatalf("comment body did not round-trip through ADF: %#v", f.comments)
	}
}

func TestTransitionStatusUsesV3(t *testing.T) {
	c, f := newTestConnector(t)
	ctx := context.Background()
	if _, err := c.SaveInitialBacklog(ctx, sampleSpecs()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.TransitionStatus(ctx, "US-001", domain.StatusInProgress); err != nil {
		t.Fatal(err)
	}
	det, err := c.ReadSpecDetail(ctx, "US-001")
	if err != nil {
		t.Fatal(err)
	}
	if det.Status != domain.StatusInProgress {
		t.Fatalf("status not transitioned: %s", det.Status)
	}
	if !f.called("GET /rest/api/3/issue/ARCH-1/transitions") ||
		!f.called("POST /rest/api/3/issue/ARCH-1/transitions") {
		t.Fatalf("transition did not use Jira v3 calls: %v", f.calls)
	}
}

func TestUpdateSpec(t *testing.T) {
	c, _ := newTestConnector(t)
	ctx := context.Background()
	if _, err := c.SaveInitialBacklog(ctx, sampleSpecs()); err != nil {
		t.Fatal(err)
	}
	newTitle := "Setup rinominato"
	if _, err := c.UpdateSpec(ctx, "US-001", domain.SpecUpdate{Title: &newTitle}); err != nil {
		t.Fatal(err)
	}
	det, err := c.ReadSpecDetail(ctx, "US-001")
	if err != nil {
		t.Fatal(err)
	}
	if det.Title != newTitle {
		t.Errorf("title not updated: %q", det.Title)
	}
	if det.Code != "US-001" {
		t.Errorf("code prefix lost after rename: %q", det.Code)
	}
}

func (f *fakeJira) called(want string) bool {
	for _, call := range f.calls {
		if call == want {
			return true
		}
	}
	return false
}

func (f *fakeJira) calledPrefix(prefix string) bool {
	for _, call := range f.calls {
		if strings.Contains(call, prefix) {
			return true
		}
	}
	return false
}
