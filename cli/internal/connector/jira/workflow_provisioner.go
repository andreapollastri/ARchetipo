package jira

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/techreloaded-ar/ARchetipo/cli/internal/domain"
	"github.com/techreloaded-ar/ARchetipo/cli/internal/iox"
)

// This file closes the gap left by Jira's standard project templates: they
// ship a fixed workflow (Backlog, Selected for Development, In Progress,
// Done) that can never satisfy the canonical REVIEW status, so a board
// auto-created by resolveProject would always bounce the user to the Jira
// admin UI. provisionStatuses instead creates the missing statuses and wires
// them into the workflow of the project's story issue type through the bulk
// workflows API, keeping board setup zero-touch. Like project auto-creation
// this requires the Administer Jira permission; resolveStatusMap treats any
// failure here as "fix the workflow by hand" and falls back to its
// precondition error.

// statusProvisionDefaults is the Jira status (name + category) created for a
// canonical status the workflow lacks. Names are chosen so matchStatus
// resolves them on the re-match that follows provisioning.
var statusProvisionDefaults = map[string]struct{ Name, Category string }{
	string(domain.StatusTodo):       {"To Do", "TODO"},
	string(domain.StatusPlanned):    {"Selected for Development", "TODO"},
	string(domain.StatusInProgress): {"In Progress", "IN_PROGRESS"},
	string(domain.StatusReview):     {"In review", "IN_PROGRESS"},
	string(domain.StatusDone):       {"Done", "DONE"},
}

// provisionedStatus is a global Jira status to be wired into a workflow.
type provisionedStatus struct {
	ID       string
	Name     string
	Category string
}

// provisionStatuses adds a Jira status for every unmatched canonical status to
// the workflow governing the project's story issue type.
func (c *Connector) provisionStatuses(ctx context.Context, missing []string) error {
	workflowName, err := c.storyWorkflowName(ctx)
	if err != nil {
		return err
	}
	add := make([]provisionedStatus, 0, len(missing))
	for _, canonical := range missing {
		def, ok := statusProvisionDefaults[canonical]
		if !ok {
			return iox.NewInternal(fmt.Sprintf("no provisioning default for canonical status %s", canonical), nil)
		}
		st, err := c.ensureGlobalStatus(ctx, def.Name, def.Category)
		if err != nil {
			return err
		}
		add = append(add, st)
	}
	return c.addStatusesToWorkflow(ctx, workflowName, add)
}

// storyWorkflowName resolves which workflow governs the story issue type: the
// scheme's explicit issue-type mapping when present, the default workflow
// otherwise (the only case for template-created projects).
func (c *Connector) storyWorkflowName(ctx context.Context) (string, error) {
	var proj struct {
		ID         string `json:"id"`
		IssueTypes []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"issueTypes"`
	}
	if err := c.do(ctx, "GET", "/rest/api/3/project/"+c.jira.ProjectKey, nil, &proj); err != nil {
		return "", err
	}
	var scheme struct {
		Values []struct {
			WorkflowScheme struct {
				DefaultWorkflow   string            `json:"defaultWorkflow"`
				IssueTypeMappings map[string]string `json:"issueTypeMappings"`
			} `json:"workflowScheme"`
		} `json:"values"`
	}
	path := "/rest/api/3/workflowscheme/project?projectId=" + url.QueryEscape(proj.ID)
	if err := c.do(ctx, "GET", path, nil, &scheme); err != nil {
		return "", err
	}
	if len(scheme.Values) == 0 {
		return "", iox.NewConnector(iox.CodeConnectorBackend,
			fmt.Sprintf("jira returned no workflow scheme for project %s", c.jira.ProjectKey), "", nil)
	}
	ws := scheme.Values[0].WorkflowScheme
	for _, it := range proj.IssueTypes {
		if strings.EqualFold(it.Name, c.storyType()) {
			if name, ok := ws.IssueTypeMappings[it.ID]; ok {
				return name, nil
			}
			break
		}
	}
	return ws.DefaultWorkflow, nil
}

// ensureGlobalStatus finds the globally scoped Jira status with the given name
// (statuses are an instance-wide resource), creating it when absent.
func (c *Connector) ensureGlobalStatus(ctx context.Context, name, category string) (provisionedStatus, error) {
	var page struct {
		Values []struct {
			ID             string `json:"id"`
			Name           string `json:"name"`
			StatusCategory string `json:"statusCategory"`
			Scope          *struct {
				Type string `json:"type"`
			} `json:"scope"`
		} `json:"values"`
	}
	path := "/rest/api/3/statuses/search?searchString=" + url.QueryEscape(name) + "&maxResults=200"
	if err := c.do(ctx, "GET", path, nil, &page); err != nil {
		return provisionedStatus{}, err
	}
	for _, v := range page.Values {
		// Project-scoped statuses (team-managed projects) cannot be reused in
		// another project's workflow; only a global one counts as a match.
		if strings.EqualFold(v.Name, name) && (v.Scope == nil || v.Scope.Type == "GLOBAL") {
			return provisionedStatus{ID: v.ID, Name: v.Name, Category: v.StatusCategory}, nil
		}
	}
	payload := map[string]any{
		"scope":    map[string]any{"type": "GLOBAL"},
		"statuses": []map[string]any{{"name": name, "statusCategory": category, "description": ""}},
	}
	var created []struct {
		ID string `json:"id"`
	}
	if err := c.do(ctx, "POST", "/rest/api/3/statuses", payload, &created); err != nil {
		return provisionedStatus{}, err
	}
	if len(created) == 0 || created[0].ID == "" {
		return provisionedStatus{}, iox.NewConnector(iox.CodeConnectorBackend,
			fmt.Sprintf("jira created status %q but returned no id", name), "", nil)
	}
	return provisionedStatus{ID: created[0].ID, Name: name, Category: category}, nil
}

// addStatusesToWorkflow appends the given statuses to the workflow, each with
// a global ("any status -> this one") transition, mirroring how the standard
// templates wire their own statuses. The bulk update API replaces statuses and
// transitions wholesale, so the existing ones are read first and echoed back
// verbatim (raw JSON: transitions carry actions/validators/properties that
// must survive the round trip untouched). Within the payload statuses are
// keyed by statusReference — a UUID, not the numeric status id, which travels
// separately in the "id" field; statuses already in the workflow keep the
// reference the read returned, new ones get a client-generated UUID.
func (c *Connector) addStatusesToWorkflow(ctx context.Context, workflowName string, add []provisionedStatus) error {
	var read struct {
		Statuses  []json.RawMessage `json:"statuses"`
		Workflows []struct {
			ID          string            `json:"id"`
			Version     json.RawMessage   `json:"version"`
			Statuses    []json.RawMessage `json:"statuses"`
			Transitions []json.RawMessage `json:"transitions"`
		} `json:"workflows"`
	}
	if err := c.do(ctx, "POST", "/rest/api/3/workflows",
		map[string]any{"workflowNames": []string{workflowName}}, &read); err != nil {
		return err
	}
	if len(read.Workflows) == 0 {
		return iox.NewConnector(iox.CodeConnectorBackend,
			fmt.Sprintf("jira workflow %q not found", workflowName), "", nil)
	}
	wf := read.Workflows[0]

	present := map[string]bool{}
	for _, raw := range wf.Statuses {
		var s struct {
			StatusReference string `json:"statusReference"`
		}
		_ = json.Unmarshal(raw, &s)
		present[s.StatusReference] = true
	}
	refByID := map[string]string{}
	for _, raw := range read.Statuses {
		var s struct {
			ID              string `json:"id"`
			StatusReference string `json:"statusReference"`
		}
		_ = json.Unmarshal(raw, &s)
		if s.ID != "" && s.StatusReference != "" {
			refByID[s.ID] = s.StatusReference
		}
	}
	nextTransitionID := 1
	for _, raw := range wf.Transitions {
		var t struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(raw, &t)
		if n, err := strconv.Atoi(t.ID); err == nil && n >= nextTransitionID {
			nextTransitionID = n + 1
		}
	}

	statuses := make([]any, 0, len(read.Statuses)+len(add))
	for _, raw := range read.Statuses {
		statuses = append(statuses, raw)
	}
	wfStatuses := make([]any, 0, len(wf.Statuses)+len(add))
	for _, raw := range wf.Statuses {
		wfStatuses = append(wfStatuses, raw)
	}
	transitions := make([]any, 0, len(wf.Transitions)+len(add))
	for _, raw := range wf.Transitions {
		transitions = append(transitions, raw)
	}
	changed := false
	for _, st := range add {
		ref, ok := refByID[st.ID]
		if !ok {
			var err error
			if ref, err = newStatusReference(); err != nil {
				return err
			}
		}
		if present[ref] {
			continue
		}
		changed = true
		statuses = append(statuses, map[string]any{
			"id": st.ID, "statusReference": ref, "name": st.Name, "statusCategory": st.Category,
		})
		wfStatuses = append(wfStatuses, map[string]any{
			"statusReference": ref, "properties": map[string]any{},
		})
		transitions = append(transitions, map[string]any{
			"id": strconv.Itoa(nextTransitionID), "type": "GLOBAL",
			"toStatusReference": ref, "name": st.Name, "links": []any{},
		})
		nextTransitionID++
	}
	if !changed {
		return nil
	}
	payload := map[string]any{
		"statuses": statuses,
		"workflows": []map[string]any{{
			"id":          wf.ID,
			"version":     wf.Version,
			"statuses":    wfStatuses,
			"transitions": transitions,
		}},
	}
	return c.do(ctx, "POST", "/rest/api/3/workflows/update", payload, nil)
}

// newStatusReference returns a fresh UUIDv4 to key a status within a workflow
// create/update payload.
func newStatusReference() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", iox.NewInternal("generating workflow status reference", err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
