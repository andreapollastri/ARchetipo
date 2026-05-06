package github

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/techreloaded-ar/ARchetipo/cli/internal/domain"
	"github.com/techreloaded-ar/ARchetipo/cli/internal/iox"
)

// resolveBoard is the single entry point for obtaining the GitHub repo and
// project metadata used by every other operation. It centralises:
//
//   - reading github.owner / github.project_number from .archetipo/config.yaml,
//   - auto-detecting them via `gh` when the config does not have them,
//   - creating the project board on GitHub when none exists yet,
//   - persisting the resolved values back into the config.
//
// It is the only function in the package that calls Config.Save. All other
// methods on Connector go through ensureSetup (which calls
// InitializeConnector, which in turn calls resolveBoard).
func (c *Connector) resolveBoard(ctx context.Context) (*domain.RepoInfo, *domain.ProjectInfo, error) {
	cameFromConfig := c.cfg.GitHub.Owner != "" && c.cfg.GitHub.ProjectNumber > 0

	repo, err := c.detectRepo(ctx)
	if err != nil {
		return nil, nil, err
	}

	var project *domain.ProjectInfo
	if cameFromConfig {
		project, err = c.lookupProjectByNumber(ctx, c.cfg.GitHub.Owner, c.cfg.GitHub.ProjectNumber, repo)
		if err != nil {
			return nil, nil, err
		}
	} else {
		project, err = c.findProjectByTitlePipeline(ctx, repo)
		if err != nil {
			return nil, nil, err
		}
		if project == nil {
			project, err = c.createProject(ctx, repo)
			if err != nil {
				return nil, nil, err
			}
		}
	}

	if !cameFromConfig {
		c.cfg.GitHub.Owner = repo.Owner
		c.cfg.GitHub.ProjectNumber = project.Number
		// Saving config is best-effort: a filesystem permission error must
		// not block read/write operations against the (already resolved) board.
		_ = c.cfg.Save()
	}

	return repo, project, nil
}

// lookupProjectByNumber resolves the project by the exact number recorded in
// config.yaml. If the number does not match any project owned by owner the
// config is considered stale and an explicit precondition error is returned —
// no fallback to title-based discovery.
func (c *Connector) lookupProjectByNumber(ctx context.Context, owner string, number int, repo *domain.RepoInfo) (*domain.ProjectInfo, error) {
	var raw struct {
		Projects []struct {
			Number int    `json:"number"`
			ID     string `json:"id"`
			Title  string `json:"title"`
			URL    string `json:"url"`
		} `json:"projects"`
	}
	if err := runJSON(ctx, c.runner, &raw,
		"project", "list", "--owner", owner, "--format", "json",
	); err != nil {
		return nil, err
	}
	for _, p := range raw.Projects {
		if p.Number == number {
			return c.loadProjectFields(ctx, repo, p.Number, p.ID, p.URL)
		}
	}
	return nil, iox.NewPrecondition(
		fmt.Sprintf("project_number %d in .archetipo/config.yaml does not exist for owner %s", number, owner),
		"remove or correct github.project_number in .archetipo/config.yaml", nil)
}

// findProjectByTitlePipeline looks up the project owned by repo.Owner whose
// title matches "<repo> Backlog" exactly. Returns (nil, nil) when nothing
// matches so the caller can create a fresh board. Partial-title or
// lowest-numbered fallbacks are intentionally absent: they previously caused
// init to attach a repo to a project owned by a different repo (e.g. "Artly"
// reusing "Tela Backlog" because both share the owner and the title contains
// "Backlog").
func (c *Connector) findProjectByTitlePipeline(ctx context.Context, repo *domain.RepoInfo) (*domain.ProjectInfo, error) {
	var raw struct {
		Projects []struct {
			Number int    `json:"number"`
			ID     string `json:"id"`
			Title  string `json:"title"`
			URL    string `json:"url"`
		} `json:"projects"`
	}
	if err := runJSON(ctx, c.runner, &raw,
		"project", "list", "--owner", repo.Owner, "--format", "json",
	); err != nil {
		return nil, err
	}
	exactTitle := repo.Name + " Backlog"
	for _, p := range raw.Projects {
		if p.Title == exactTitle {
			return c.loadProjectFields(ctx, repo, p.Number, p.ID, p.URL)
		}
	}
	return nil, nil
}

// createProject creates a new GitHub Projects v2 board titled "<repo> Backlog",
// adds the Priority and Story Points custom fields, and aligns the Status
// field options to the workflow.statuses values from config. The Epic field
// is intentionally not pre-created: GitHub requires at least one option for
// SINGLE_SELECT, and the connector populates Epic options on demand when
// stories with Epic codes are added.
func (c *Connector) createProject(ctx context.Context, repo *domain.RepoInfo) (*domain.ProjectInfo, error) {
	title := repo.Name + " Backlog"
	var created struct {
		Number int    `json:"number"`
		ID     string `json:"id"`
		URL    string `json:"url"`
	}
	if err := runJSON(ctx, c.runner, &created,
		"project", "create", "--owner", repo.Owner, "--title", title, "--format", "json",
	); err != nil {
		return nil, err
	}
	if _, stderr, err := c.runner.Run(ctx, nil,
		"project", "field-create", strconv.Itoa(created.Number),
		"--owner", repo.Owner,
		"--name", "Priority",
		"--data-type", "SINGLE_SELECT",
		"--single-select-options", string(domain.PriorityHigh)+","+string(domain.PriorityMedium)+","+string(domain.PriorityLow),
	); err != nil {
		return nil, classify(err, stderr)
	}
	if _, stderr, err := c.runner.Run(ctx, nil,
		"project", "field-create", strconv.Itoa(created.Number),
		"--owner", repo.Owner,
		"--name", "Story Points",
		"--data-type", "NUMBER",
	); err != nil {
		return nil, classify(err, stderr)
	}
	pi, err := c.loadProjectFields(ctx, repo, created.Number, created.ID, created.URL)
	if err != nil {
		return nil, err
	}
	if err := c.alignStatusOptions(ctx, pi); err != nil {
		return nil, err
	}
	// Reload to capture the new Status options.
	return c.loadProjectFields(ctx, repo, created.Number, created.ID, created.URL)
}

// alignStatusOptions overwrites the options of the project's Status
// single-select field so they match c.cfg.Workflow.Statuses. Required because
// GitHub creates new boards with a default Status of "Todo / In Progress /
// Done" which usually does not line up with the connector's canonical labels
// (TODO / PLANNED / IN PROGRESS / REVIEW / DONE).
func (c *Connector) alignStatusOptions(ctx context.Context, pi *domain.ProjectInfo) error {
	if pi.Fields.StatusFieldID == "" {
		return nil
	}
	wanted := []string{
		c.cfg.Workflow.Statuses.Todo,
		c.cfg.Workflow.Statuses.Planned,
		c.cfg.Workflow.Statuses.InProgress,
		c.cfg.Workflow.Statuses.Review,
		c.cfg.Workflow.Statuses.Done,
	}
	parts := make([]string, 0, len(wanted))
	for _, name := range wanted {
		if name == "" {
			continue
		}
		escaped := strings.ReplaceAll(name, `"`, `\"`)
		parts = append(parts, fmt.Sprintf(`{name: "%s", color: GRAY, description: ""}`, escaped))
	}
	// gh api graphql -F encodes scalars only; arrays of objects must be
	// inlined into the query text.
	query := fmt.Sprintf(`mutation {
  updateProjectV2Field(input: {
    fieldId: "%s"
    singleSelectOptions: [%s]
  }) { projectV2Field { ... on ProjectV2SingleSelectField { id } } }
}`, pi.Fields.StatusFieldID, strings.Join(parts, ", "))
	if _, stderr, err := c.runner.Run(ctx, nil, "api", "graphql", "-f", "query="+query); err != nil {
		return classify(err, stderr)
	}
	return nil
}
