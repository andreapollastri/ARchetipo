// Package connector defines the abstract interface implemented by every
// ARchetipo connector. Methods mirror the public workflow operations
// exposed by the CLI.
package connector

import (
	"context"

	"github.com/techreloaded-ar/ARchetipo/cli/internal/domain"
)

// Connector is the abstract backend behind the CLI. Implementations:
//
//   - filefs.Connector: stores PRD, backlog and plans as YAML files.
//   - github.Connector: stores backlog and plans as GitHub issues + Projects v2.
//
// Operations match the CLI surface. Methods take a context.Context so
// long-running github calls can be cancelled and tested with timeouts.
type Connector interface {
	// SETUP

	// InitializeConnector authenticates, detects the repository (if any) and
	// loads metadata. Idempotent.
	InitializeConnector(ctx context.Context) (domain.SetupInfo, error)

	// READ

	// FetchBacklogItems returns all backlog items, optionally filtered by status.
	// statusFilter == "" returns all items.
	FetchBacklogItems(ctx context.Context, statusFilter domain.Status) ([]domain.Story, error)

	// SelectStory returns a single story by code, or auto-selects the highest-
	// priority story among eligible statuses. Q.StoryCode == "" => auto.
	SelectStory(ctx context.Context, q domain.SelectQuery) (domain.Story, error)

	// ReadStoryDetail returns the full body/content of a story.
	ReadStoryDetail(ctx context.Context, ref string) (domain.Story, error)

	// ReadStoryTasks returns the ordered task list for a story.
	ReadStoryTasks(ctx context.Context, parentRef string) ([]domain.Task, error)

	// ReadExistingBacklog returns idempotency metadata about the current backlog.
	ReadExistingBacklog(ctx context.Context) (domain.BacklogSummary, error)

	// WRITE

	// SavePRD writes the PRD markdown to the configured path.
	SavePRD(ctx context.Context, content string) (domain.WriteResult, error)

	// SaveInitialBacklog creates the initial backlog from the given stories.
	// Connector-specific side effects (label creation, project board setup)
	// happen as part of this call.
	SaveInitialBacklog(ctx context.Context, stories []domain.Story) (domain.WriteResult, error)

	// AppendStories adds new stories to an existing backlog without rewriting
	// existing content.
	AppendStories(ctx context.Context, stories []domain.Story) (domain.WriteResult, error)

	// SavePlan attaches an implementation plan to a story. The plan body goes
	// into the parent artifact (file or issue body); each task becomes a
	// trackable item (file row or sub-issue).
	SavePlan(ctx context.Context, storyRef string, plan domain.PlanInput) (domain.WriteResult, error)

	// TransitionStatus changes the workflow status of a story.
	TransitionStatus(ctx context.Context, storyRef string, newStatus domain.Status) (domain.WriteResult, error)

	// CompleteTask marks a single task as completed.
	CompleteTask(ctx context.Context, parentRef, taskRef string) (domain.WriteResult, error)

	// MoveBoardCard repositions a story inside the board, optionally changing
	// its status when the target column maps to a different workflow step.
	MoveBoardCard(ctx context.Context, storyRef, targetColumn string, anchor domain.ReorderAnchor) (domain.WriteResult, error)

	// UpdateStory applies a partial patch to an existing story. Only fields whose
	// pointer in patch is non-nil are modified; the rest of the story keeps its
	// current value. Returns a precondition error when the story is unknown.
	UpdateStory(ctx context.Context, storyRef string, patch domain.StoryUpdate) (domain.WriteResult, error)

	// PostComment posts a comment on a story. No-op for connectors without
	// comment support (e.g. filefs); the implementation must return ok=true.
	PostComment(ctx context.Context, storyRef, body string) (domain.WriteResult, error)
}
