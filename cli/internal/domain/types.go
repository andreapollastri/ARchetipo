// Package domain defines the canonical data types exchanged between the CLI
// surface, the connector interface, and the two connector implementations
// (filefs, github). Types are connector-agnostic: a Spec is a Spec whether
// it lives in BACKLOG.md or as a GitHub issue.
package domain

// Priority of a spec. Stable string set so the JSON output is deterministic.
type Priority string

const (
	PriorityHigh   Priority = "HIGH"
	PriorityMedium Priority = "MEDIUM"
	PriorityLow    Priority = "LOW"
)

// Status is the workflow status of a spec or task. Strings come from the
// `workflow.statuses` map in .archetipo/config.yaml; the canonical set is the
// one built into the CLI defaults.
type Status string

const (
	StatusTodo       Status = "TODO"
	StatusPlanned    Status = "PLANNED"
	StatusInProgress Status = "IN PROGRESS"
	StatusReview     Status = "REVIEW"
	StatusDone       Status = "DONE"
)

// Scope of a spec (MVP, post-MVP, etc.). Free-form string.
type Scope string

// TaskType distinguishes implementation tasks from test tasks.
type TaskType string

const (
	TaskImpl TaskType = "Impl"
	TaskTest TaskType = "Test"
)

// Epic identifies a group of specs. Code looks like "EP-001"; Title is
// the human-readable name.
type Epic struct {
	Code  string `json:"code" yaml:"code"`
	Title string `json:"title" yaml:"title"`
}

// Spec is the unit of work in the backlog. Its body follows the user-story
// agile format ("As [persona] I want [action] so that [benefit]"), but the
// container itself is a Spec.
//
// Code, Title and Epic are always populated. Status defaults to TODO when
// the connector cannot determine it.
type Spec struct {
	Code      string   `json:"code" yaml:"code"`
	Title     string   `json:"title" yaml:"title"`
	Epic      Epic     `json:"epic" yaml:"epic"`
	Priority  Priority `json:"priority" yaml:"priority"`
	Points    int      `json:"points" yaml:"points"`
	Status    Status   `json:"status" yaml:"status"`
	BlockedBy []string `json:"blocked_by,omitempty" yaml:"blocked_by,omitempty"`
	Scope     Scope    `json:"scope,omitempty" yaml:"scope,omitempty"`
	// Body is the full markdown body of the spec (acceptance criteria,
	// description, demonstrates, scope). Connectors fill it for read_spec_detail.
	Body string `json:"body,omitempty" yaml:"body,omitempty"`
	// Ref is a connector-local identifier (issue number for github, spec
	// code for filefs). Always set together with Code.
	Ref string `json:"ref,omitempty" yaml:"ref,omitempty"`
	// URL is set by connectors that have a web location (github).
	URL string `json:"url,omitempty" yaml:"url,omitempty"`
}

// Task is a unit of work inside a Spec's implementation plan.
type Task struct {
	ID           string   `json:"id" yaml:"id"`
	Title        string   `json:"title" yaml:"title"`
	Description  string   `json:"description,omitempty" yaml:"description,omitempty"`
	Type         TaskType `json:"type" yaml:"type"`
	Status       Status   `json:"status" yaml:"status"`
	Dependencies []string `json:"dependencies,omitempty" yaml:"dependencies,omitempty"`
	// Body is the full markdown body of the task (filled by read_spec_tasks
	// when the connector exposes one). May be empty for the file connector.
	Body string `json:"body,omitempty" yaml:"body,omitempty"`
	// Ref is a connector-local identifier (sub-issue number for github,
	// task ID for filefs). Always set together with ID.
	Ref string `json:"ref,omitempty" yaml:"ref,omitempty"`
}

// SetupInfo is the output of initialize_connector. Fields populated depend on
// the connector: filefs only fills Paths; github fills Repo + Project.
type SetupInfo struct {
	Connector string         `json:"connector" yaml:"connector"`
	Paths     ConfigPaths    `json:"paths" yaml:"paths"`
	Workflow  WorkflowConfig `json:"workflow" yaml:"workflow"`
	Repo      *RepoInfo      `json:"repo,omitempty" yaml:"repo,omitempty"`
	Project   *ProjectInfo   `json:"project,omitempty" yaml:"project,omitempty"`
}

// ConfigPaths mirrors the paths section of .archetipo/config.yaml.
type ConfigPaths struct {
	PRD         string `json:"prd" yaml:"prd"`
	Backlog     string `json:"backlog" yaml:"backlog"`
	Planning    string `json:"planning" yaml:"planning"`
	Mockups     string `json:"mockups" yaml:"mockups"`
	TestResults string `json:"test_results" yaml:"test_results"`
}

// WorkflowConfig mirrors workflow.statuses from .archetipo/config.yaml.
type WorkflowConfig struct {
	Statuses StatusLabels `json:"statuses" yaml:"statuses"`
}

// StatusLabels maps the canonical workflow steps to project-specific labels.
type StatusLabels struct {
	Todo       string `json:"todo" yaml:"todo"`
	Planned    string `json:"planned" yaml:"planned"`
	InProgress string `json:"in_progress" yaml:"in_progress"`
	Review     string `json:"review" yaml:"review"`
	Done       string `json:"done" yaml:"done"`
}

// RepoInfo is populated by the github connector.
type RepoInfo struct {
	Owner  string `json:"owner" yaml:"owner"`
	Name   string `json:"name" yaml:"name"`
	Slug   string `json:"slug" yaml:"slug"`
	NodeID string `json:"node_id" yaml:"node_id"`
}

// ProjectInfo is populated by the github connector with the GitHub Projects v2
// metadata needed by downstream operations.
type ProjectInfo struct {
	Number int           `json:"number" yaml:"number"`
	NodeID string        `json:"node_id" yaml:"node_id"`
	URL    string        `json:"url,omitempty" yaml:"url,omitempty"`
	Fields ProjectFields `json:"fields" yaml:"fields"`
}

// ProjectFields holds the IDs of project custom fields and their option IDs.
// PointsFieldID stores the GitHub Projects custom field whose user-visible
// label remains "Story Points" — only the Go-side identifier is renamed.
type ProjectFields struct {
	StatusFieldID   string            `json:"status_field_id,omitempty" yaml:"status_field_id,omitempty"`
	StatusOptions   map[string]string `json:"status_options,omitempty" yaml:"status_options,omitempty"`
	PriorityFieldID string            `json:"priority_field_id,omitempty" yaml:"priority_field_id,omitempty"`
	PriorityOptions map[string]string `json:"priority_options,omitempty" yaml:"priority_options,omitempty"`
	PointsFieldID   string            `json:"points_field_id,omitempty" yaml:"points_field_id,omitempty"`
	EpicFieldID     string            `json:"epic_field_id,omitempty" yaml:"epic_field_id,omitempty"`
	EpicOptions     map[string]string `json:"epic_options,omitempty" yaml:"epic_options,omitempty"`
}

// BacklogSummary is the output of read_existing_backlog: the data a skill
// needs to extend a backlog idempotently.
type BacklogSummary struct {
	Codes    []string `json:"codes" yaml:"codes"`
	LastCode string   `json:"last_code,omitempty" yaml:"last_code,omitempty"`
	Epics    []Epic   `json:"epics" yaml:"epics"`
	Titles   []string `json:"titles" yaml:"titles"`
}

// Ref is a back-reference returned by write operations so the caller can
// point users at the artifact (URL when connector is github, file path when
// connector is filefs).
type Ref struct {
	Code   string `json:"code,omitempty" yaml:"code,omitempty"`
	Number int    `json:"number,omitempty" yaml:"number,omitempty"`
	Path   string `json:"path,omitempty" yaml:"path,omitempty"`
	URL    string `json:"url,omitempty" yaml:"url,omitempty"`
}

// WriteResult is the canonical envelope-level data for write operations.
//
// Skipped lists the codes that the CLI intentionally did not write because
// they would conflict with existing artifacts (e.g. `archetipo spec add`
// idempotently skips specs whose code is already present in the backlog).
type WriteResult struct {
	OK      bool     `json:"ok" yaml:"ok"`
	Refs    []Ref    `json:"refs,omitempty" yaml:"refs,omitempty"`
	Skipped []string `json:"skipped,omitempty" yaml:"skipped,omitempty"`
}

// PlanInput is the stdin payload of `archetipo spec plan`.
type PlanInput struct {
	PlanBody string `json:"plan_body" yaml:"plan_body"`
	Tasks    []Task `json:"tasks" yaml:"tasks"`
}

// SelectQuery captures the inputs of select_spec.
type SelectQuery struct {
	SpecCode         string   // empty => auto-select
	EligibleStatuses []Status // required for auto-select; ignored when SpecCode is set
}

// ReorderAnchor captures a relative move request. Exactly one of Before/After
// may be set; both empty means append to the end.
type ReorderAnchor struct {
	Before string
	After  string
}

// MockupEntry describes a single mockup folder (one per design artifact)
// served by the viewer. Name is the folder name under paths.mockups; URL is
// the path the SPA can link to (served by the viewer's static handler).
// SpecCode is non-empty when Name matches a spec/epic code (US-NNN, EP-NNN).
type MockupEntry struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	SpecCode string `json:"spec_code,omitempty"`
}

// SpecUpdate is a partial patch over an existing Spec. Pointer fields
// distinguish "not provided" (nil) from "set to zero value" (non-nil pointing
// at the zero value). Connectors must only touch fields whose pointer is
// non-nil and leave the rest untouched.
type SpecUpdate struct {
	Title     *string   `json:"title,omitempty"`
	Priority  *Priority `json:"priority,omitempty"`
	Points    *int      `json:"points,omitempty"`
	Scope     *Scope    `json:"scope,omitempty"`
	BlockedBy *[]string `json:"blocked_by,omitempty"`
	Body      *string   `json:"body,omitempty"`
	Epic      *Epic     `json:"epic,omitempty"`
}
