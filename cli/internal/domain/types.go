// Package domain defines the canonical data types exchanged between the CLI
// surface, the connector interface, and the two connector implementations
// (filefs, github). Types are connector-agnostic: a Story is a Story whether
// it lives in BACKLOG.md or as a GitHub issue.
package domain

// Priority of a story. Stable string set so the JSON output is deterministic.
type Priority string

const (
	PriorityHigh   Priority = "HIGH"
	PriorityMedium Priority = "MEDIUM"
	PriorityLow    Priority = "LOW"
)

// Status is the workflow status of a story or task. Strings come from the
// `workflow.statuses` map in .archetipo/config.yaml; the canonical set is the
// one documented in contracts.md.
type Status string

const (
	StatusTodo       Status = "TODO"
	StatusPlanned    Status = "PLANNED"
	StatusInProgress Status = "IN PROGRESS"
	StatusReview     Status = "REVIEW"
	StatusDone       Status = "DONE"
)

// Scope of a story (MVP, post-MVP, etc.). Free-form string.
type Scope string

// TaskType distinguishes implementation tasks from test tasks.
type TaskType string

const (
	TaskImpl TaskType = "Impl"
	TaskTest TaskType = "Test"
)

// Epic identifies a group of stories. Code looks like "EP-001"; Title is
// the human-readable name.
type Epic struct {
	Code  string `json:"code"`
	Title string `json:"title"`
}

// Story is the unit of work in the backlog.
//
// Code, Title and Epic are always populated. Status defaults to TODO when
// the connector cannot determine it.
type Story struct {
	Code           string   `json:"code"`
	Title          string   `json:"title"`
	Epic           Epic     `json:"epic"`
	Priority       Priority `json:"priority"`
	StoryPoints    int      `json:"story_points"`
	Status         Status   `json:"status"`
	BlockedBy      []string `json:"blocked_by,omitempty"`
	Scope          Scope    `json:"scope,omitempty"`
	// Body is the full markdown body of the story (acceptance criteria,
	// description, demonstrates, scope). Connectors fill it for read_story_detail.
	Body string `json:"body,omitempty"`
	// Ref is a connector-local identifier (issue number for github, story
	// code for filefs). Always set together with Code.
	Ref string `json:"ref,omitempty"`
	// URL is set by connectors that have a web location (github).
	URL string `json:"url,omitempty"`
}

// Task is a unit of work inside a Story's implementation plan.
type Task struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Description  string   `json:"description,omitempty"`
	Type         TaskType `json:"type"`
	Status       Status   `json:"status"`
	Dependencies []string `json:"dependencies,omitempty"`
	// Body is the full markdown body of the task (filled by read_story_tasks
	// when the connector exposes one). May be empty for the file connector.
	Body string `json:"body,omitempty"`
	// Ref is a connector-local identifier (sub-issue number for github,
	// task ID for filefs). Always set together with ID.
	Ref string `json:"ref,omitempty"`
}

// SetupInfo is the output of initialize_connector. Fields populated depend on
// the connector: filefs only fills Paths; github fills Repo + Project.
type SetupInfo struct {
	Connector string         `json:"connector"`
	Paths     ConfigPaths    `json:"paths"`
	Workflow  WorkflowConfig `json:"workflow"`
	Repo      *RepoInfo      `json:"repo,omitempty"`
	Project   *ProjectInfo   `json:"project,omitempty"`
}

// ConfigPaths mirrors the paths section of .archetipo/config.yaml.
type ConfigPaths struct {
	PRD         string `json:"prd"`
	Backlog     string `json:"backlog"`
	Planning    string `json:"planning"`
	Mockups     string `json:"mockups"`
	TestResults string `json:"test_results"`
}

// WorkflowConfig mirrors workflow.statuses from .archetipo/config.yaml.
type WorkflowConfig struct {
	Statuses StatusLabels `json:"statuses"`
}

// StatusLabels maps the canonical workflow steps to project-specific labels.
type StatusLabels struct {
	Todo       string `json:"todo"`
	Planned    string `json:"planned"`
	InProgress string `json:"in_progress"`
	Review     string `json:"review"`
	Done       string `json:"done"`
}

// RepoInfo is populated by the github connector.
type RepoInfo struct {
	Owner    string `json:"owner"`
	Name     string `json:"name"`
	Slug     string `json:"slug"`
	NodeID   string `json:"node_id"`
}

// ProjectInfo is populated by the github connector with the GitHub Projects v2
// metadata needed by downstream operations.
type ProjectInfo struct {
	Number   int               `json:"number"`
	NodeID   string            `json:"node_id"`
	URL      string            `json:"url,omitempty"`
	Fields   ProjectFields     `json:"fields"`
}

// ProjectFields holds the IDs of project custom fields and their option IDs.
type ProjectFields struct {
	StatusFieldID  string            `json:"status_field_id,omitempty"`
	StatusOptions  map[string]string `json:"status_options,omitempty"`
	PriorityFieldID string           `json:"priority_field_id,omitempty"`
	PriorityOptions map[string]string `json:"priority_options,omitempty"`
	StoryPointsFieldID string         `json:"story_points_field_id,omitempty"`
	EpicFieldID    string             `json:"epic_field_id,omitempty"`
	EpicOptions    map[string]string  `json:"epic_options,omitempty"`
}

// BacklogSummary is the output of read_existing_backlog: the data a skill
// needs to extend a backlog idempotently.
type BacklogSummary struct {
	Codes    []string `json:"codes"`
	LastCode string   `json:"last_code,omitempty"`
	Epics    []Epic   `json:"epics"`
	Titles   []string `json:"titles"`
}

// Ref is a back-reference returned by write operations so the caller can
// point users at the artifact (URL when connector is github, file path when
// connector is filefs).
type Ref struct {
	Code   string `json:"code,omitempty"`
	Number int    `json:"number,omitempty"`
	Path   string `json:"path,omitempty"`
	URL    string `json:"url,omitempty"`
}

// WriteResult is the canonical envelope-level data for write operations.
//
// Skipped lists the codes that the CLI intentionally did not write because
// they would conflict with existing artifacts (e.g. `archetipo story add`
// idempotently skips stories whose code is already present in the backlog).
type WriteResult struct {
	OK      bool     `json:"ok"`
	Refs    []Ref    `json:"refs,omitempty"`
	Skipped []string `json:"skipped,omitempty"`
}

// PlanInput is the stdin payload of `archetipo plan save`.
type PlanInput struct {
	PlanBody string `json:"plan_body"`
	Tasks    []Task `json:"tasks"`
}

// SelectQuery captures the inputs of select_story.
type SelectQuery struct {
	StoryCode        string   // empty => auto-select
	EligibleStatuses []Status // required for auto-select; ignored when StoryCode is set
}
