package jira

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"strings"

	"github.com/techreloaded-ar/ARchetipo/cli/internal/domain"
	"github.com/techreloaded-ar/ARchetipo/cli/internal/iox"
)

// This file mirrors the github connector's board_resolver: it resolves the
// Jira project (detect by name, create when missing) and the canonical->Jira
// status mapping, persisting both back to .archetipo/config.yaml so later runs
// take the fast path. It is the only place in the package calling Config.Save.

// maxKeyAttempts bounds the create retries when the derived project key is
// already taken by a project with a different name.
const maxKeyAttempts = 3

// resolveProject ensures c.jira.ProjectKey points at an existing Jira project
// and the status map covers every canonical status. myAccountID (from
// /rest/api/3/myself) becomes the lead of an auto-created project.
func (c *Connector) resolveProject(ctx context.Context, myAccountID string) error {
	if c.jira.ProjectKey == "" {
		name := filepath.Base(c.cfg.ProjectRoot)
		key, err := c.findProjectByName(ctx, name)
		if err != nil {
			return err
		}
		if key == "" {
			key, err = c.createProject(ctx, name, myAccountID)
			if err != nil {
				return err
			}
		}
		// Persist the key before status resolution: a freshly created project
		// may lack a REVIEW-like status, and the user must be able to fix the
		// Jira workflow and re-run without triggering a second create.
		c.jira.ProjectKey = key
		c.cfg.Jira.ProjectKey = key
		_ = c.cfg.Save()
	}
	return c.resolveStatusMap(ctx)
}

// findProjectByName scans the projects visible to the token for one whose name
// matches (case-insensitively). Returns "" when none matches.
func (c *Connector) findProjectByName(ctx context.Context, name string) (string, error) {
	startAt := 0
	for {
		var page struct {
			Values []struct {
				Key  string `json:"key"`
				Name string `json:"name"`
			} `json:"values"`
			IsLast bool `json:"isLast"`
		}
		path := fmt.Sprintf("/rest/api/3/project/search?query=%s&startAt=%d&maxResults=50",
			url.QueryEscape(name), startAt)
		if err := c.do(ctx, "GET", path, nil, &page); err != nil {
			return "", err
		}
		for _, p := range page.Values {
			if strings.EqualFold(p.Name, name) {
				return p.Key, nil
			}
		}
		if page.IsLast || len(page.Values) == 0 {
			return "", nil
		}
		startAt += len(page.Values)
	}
}

// createProject creates a company-managed Kanban software project named after
// the project directory. Requires the "Administer Jira" global permission;
// without it the API answers 401/403 and we re-hint the error accordingly.
func (c *Connector) createProject(ctx context.Context, name, leadAccountID string) (string, error) {
	base, err := deriveProjectKey(name)
	if err != nil {
		return "", err
	}
	key := base
	for attempt := 1; ; attempt++ {
		payload := map[string]any{
			"key":                key,
			"name":               name,
			"projectTypeKey":     "software",
			"projectTemplateKey": "com.pyxis.greenhopper.jira:gh-simplified-kanban-classic",
			"leadAccountId":      leadAccountID,
			"assigneeType":       "UNASSIGNED",
		}
		var created struct {
			Key string `json:"key"`
		}
		err := c.do(ctx, "POST", "/rest/api/3/project", payload, &created)
		if err == nil {
			if created.Key != "" {
				return created.Key, nil
			}
			return key, nil
		}
		var ce *iox.CodedError
		if errors.As(err, &ce) {
			if ce.Code == iox.CodeConnectorAuth {
				return "", iox.NewConnector(iox.CodeConnectorAuth,
					fmt.Sprintf("jira project auto-creation failed: %s", ce.Message),
					"creating Jira projects requires the Administer Jira permission; ask an admin, or create the project manually and set jira.project_key in .archetipo/config.yaml", err)
			}
			// A taken/invalid key surfaces as a 400 whose errors map names the
			// projectKey field. Retry with a numeric-suffix variant.
			if attempt < maxKeyAttempts && strings.Contains(ce.Message, "projectKey") {
				key = keyVariant(base, attempt+1)
				continue
			}
		}
		return "", iox.NewConnector(iox.CodeConnectorBackend,
			fmt.Sprintf("jira project auto-creation failed: %v", err),
			"create the project manually in Jira and set jira.project_key in .archetipo/config.yaml", err)
	}
}

// deriveProjectKey turns a directory name into a Jira project key: uppercase,
// alphanumeric only, no leading digits, at most 10 characters.
func deriveProjectKey(name string) (string, error) {
	var b strings.Builder
	for _, r := range strings.ToUpper(name) {
		switch {
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			if b.Len() > 0 { // Jira keys must start with a letter
				b.WriteRune(r)
			}
		}
	}
	key := b.String()
	if len(key) > 10 {
		key = key[:10]
	}
	if key == "" {
		return "", iox.NewInvalidInput(
			fmt.Sprintf("cannot derive a Jira project key from directory name %q", name),
			"set jira.project_key in .archetipo/config.yaml", nil)
	}
	return key, nil
}

// keyVariant builds the n-th collision fallback for a derived key (BASE2,
// BASE3, ...), keeping the result within Jira's 10-character limit.
func keyVariant(base string, n int) string {
	suffix := fmt.Sprintf("%d", n)
	if len(base)+len(suffix) > 10 {
		base = base[:10-len(suffix)]
	}
	return base + suffix
}

// statusSynonyms lists, per canonical status, the normalized Jira status names
// commonly used for the same concept. Checked only after exact and normalized
// matching fail.
var statusSynonyms = map[domain.Status][]string{
	domain.StatusTodo:       {"todo", "backlog", "open"},
	domain.StatusPlanned:    {"selectedfordevelopment", "ready", "readyfordevelopment", "planned"},
	domain.StatusInProgress: {"inprogress", "indevelopment"},
	domain.StatusReview:     {"inreview", "review", "codereview", "qa", "testing"},
	domain.StatusDone:       {"done", "closed", "complete", "resolved"},
}

// resolveStatusMap discovers the canonical->Jira status mapping from the
// project's real workflow and persists it to config.yaml when it changed.
// User-provided status_map entries win but are validated against the statuses
// that actually exist. Canonical statuses with no counterpart are first
// provisioned into the Jira workflow (standard templates lack a REVIEW-like
// status, so a freshly created board can never match otherwise); only when
// provisioning fails too are they an error, because they would silently break
// transitions later.
func (c *Connector) resolveStatusMap(ctx context.Context) error {
	res, err := c.matchProjectStatuses(ctx)
	if err != nil {
		return err
	}
	var provErr error
	if len(res.unmatched) > 0 {
		if provErr = c.provisionStatuses(ctx, res.unmatched); provErr == nil {
			if res, err = c.matchProjectStatuses(ctx); err != nil {
				return err
			}
		}
	}
	if len(res.unmatched) > 0 {
		msg := fmt.Sprintf("cannot map workflow status(es) %s to the statuses of jira project %s (available for %s: %s)",
			strings.Join(res.unmatched, ", "), c.jira.ProjectKey, c.storyType(), strings.Join(res.storyStatuses, ", "))
		if provErr != nil {
			msg += fmt.Sprintf("; auto-creating them in the Jira workflow failed: %v", provErr)
		}
		return iox.NewPrecondition(msg,
			"add matching statuses to the Jira workflow, or set jira.status_map.<STATUS> in .archetipo/config.yaml", provErr)
	}

	if !mapsEqual(res.resolved, c.jira.StatusMap) {
		c.jira.StatusMap = res.resolved
		c.cfg.Jira.StatusMap = res.resolved
		_ = c.cfg.Save()
	}
	return nil
}

// statusResolution is the outcome of one matching pass over the project's
// live workflow statuses.
type statusResolution struct {
	resolved      map[string]string
	unmatched     []string // canonical statuses with no user entry and no match
	storyStatuses []string // statuses of the story issue type, for error messages
}

// jiraStatusName carries both names Jira exposes for a project status: name is
// translated to the account's language for statuses with translations (the
// Jira defaults all have them: "In review" reads "In revisione" on an Italian
// account), untranslatedName is the stored one. Matching considers both, but
// always resolves to the translated name, because that is what the
// issue-facing endpoints (issue fields, transitions) return.
type jiraStatusName struct {
	name         string
	untranslated string
}

// matchProjectStatuses fetches the project's live statuses and matches every
// canonical status against them: user-provided status_map entries win (and are
// validated against the union across issue types), the rest is auto-matched
// against the story issue type's statuses.
func (c *Connector) matchProjectStatuses(ctx context.Context) (statusResolution, error) {
	var issueTypes []struct {
		Name     string `json:"name"`
		Subtask  bool   `json:"subtask"`
		Statuses []struct {
			Name             string `json:"name"`
			UntranslatedName string `json:"untranslatedName"`
		} `json:"statuses"`
	}
	err := c.do(ctx, "GET", "/rest/api/3/project/"+c.jira.ProjectKey+"/statuses", nil, &issueTypes)
	if err != nil {
		var ce *iox.CodedError
		if errors.As(err, &ce) && ce.Code == iox.CodeNotFound {
			return statusResolution{}, iox.NewPrecondition(
				fmt.Sprintf("jira project %s (from .archetipo/config.yaml) does not exist on %s", c.jira.ProjectKey, c.jira.BaseURL),
				"fix jira.project_key, or remove it to let the CLI auto-detect the project", err)
		}
		return statusResolution{}, err
	}

	// Statuses of the story issue type drive auto-matching; the union across
	// all issue types validates user overrides (sub-tasks may follow a
	// different workflow). Both lookups accept the untranslated name too, so a
	// localized account still matches the Jira default statuses.
	var storyStatuses []jiraStatusName
	union := map[string]string{} // lowercased name or untranslated name -> translated name
	for _, it := range issueTypes {
		for _, s := range it.Statuses {
			union[strings.ToLower(s.Name)] = s.Name
			if s.UntranslatedName != "" {
				if _, taken := union[strings.ToLower(s.UntranslatedName)]; !taken {
					union[strings.ToLower(s.UntranslatedName)] = s.Name
				}
			}
			if strings.EqualFold(it.Name, c.storyType()) {
				storyStatuses = append(storyStatuses, jiraStatusName{name: s.Name, untranslated: s.UntranslatedName})
			}
		}
	}
	if len(storyStatuses) == 0 {
		available := make([]string, 0, len(issueTypes))
		for _, it := range issueTypes {
			available = append(available, it.Name)
		}
		return statusResolution{}, iox.NewPrecondition(
			fmt.Sprintf("issue type %q not found in jira project %s (available: %s)",
				c.storyType(), c.jira.ProjectKey, strings.Join(available, ", ")),
			"set jira.story_type in .archetipo/config.yaml to one of the project's issue types", nil)
	}

	canonical := []domain.Status{
		domain.StatusTodo, domain.StatusPlanned, domain.StatusInProgress,
		domain.StatusReview, domain.StatusDone,
	}
	res := statusResolution{resolved: map[string]string{}}
	for _, s := range storyStatuses {
		label := s.name
		if s.untranslated != "" && !strings.EqualFold(s.untranslated, s.name) {
			label = fmt.Sprintf("%s (%s)", s.name, s.untranslated)
		}
		res.storyStatuses = append(res.storyStatuses, label)
	}
	for _, st := range canonical {
		if user, ok := c.jira.StatusMap[string(st)]; ok && user != "" {
			exact, found := union[strings.ToLower(user)]
			if !found {
				return statusResolution{}, iox.NewPrecondition(
					fmt.Sprintf("jira.status_map maps %s to %q, which does not exist in project %s (available: %s)",
						st, user, c.jira.ProjectKey, strings.Join(sortedValues(union), ", ")),
					"fix jira.status_map in .archetipo/config.yaml or add the status to the Jira workflow", nil)
			}
			res.resolved[string(st)] = exact
			continue
		}
		if name, ok := matchStatus(st, storyStatuses); ok {
			res.resolved[string(st)] = name
			continue
		}
		res.unmatched = append(res.unmatched, string(st))
	}
	return res, nil
}

// matchStatus finds the Jira status for a canonical one: exact
// case-insensitive first, then normalized (alphanumeric-only), then synonyms.
// Every tier checks the translated and the untranslated name; the returned
// name is always the translated one (see jiraStatusName).
func matchStatus(st domain.Status, jiraStatuses []jiraStatusName) (string, bool) {
	for _, s := range jiraStatuses {
		if strings.EqualFold(s.name, string(st)) || strings.EqualFold(s.untranslated, string(st)) {
			return s.name, true
		}
	}
	want := normalizeStatus(string(st))
	for _, s := range jiraStatuses {
		if normalizeStatus(s.name) == want || (s.untranslated != "" && normalizeStatus(s.untranslated) == want) {
			return s.name, true
		}
	}
	for _, syn := range statusSynonyms[st] {
		for _, s := range jiraStatuses {
			if normalizeStatus(s.name) == syn || (s.untranslated != "" && normalizeStatus(s.untranslated) == syn) {
				return s.name, true
			}
		}
	}
	return "", false
}

// normalizeStatus lowercases and strips every non-alphanumeric rune, so that
// "In-Progress", "in progress" and "IN PROGRESS" compare equal.
func normalizeStatus(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// sortedValues lists the distinct values of m in sorted order (the union maps
// both names of a status to the same value, so duplicates are expected).
func sortedValues(m map[string]string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(m))
	for _, v := range m {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
