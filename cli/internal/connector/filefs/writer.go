package filefs

import (
	"fmt"
	"strings"

	"github.com/techreloaded-ar/ARchetipo/cli/internal/domain"
)

// renderBacklog produces the canonical BACKLOG.md content for the given
// specs. Output is byte-deterministic: same input => same bytes.
//
// Layout:
//
//	<!-- archetipo:backlog version=1 -->
//
//	# Backlog
//
//	#### US-001: <Title>
//	<!-- archetipo:spec ... -->
//	<body provided by the skill>
//
//	#### US-002: <Title>
//	...
//
// The skill is responsible for body content (user story / Demonstrates /
// Acceptance Criteria / Epic prose / etc.) and for any human-language summary
// at the top — this writer only emits the structural skeleton.
func renderBacklog(specs []domain.Spec) string {
	var b strings.Builder
	b.WriteString(backlogMarker())
	b.WriteString("\n\n# Backlog\n\n")
	for i, s := range specs {
		b.WriteString(renderSpecBlock(s))
		if i != len(specs)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// renderSpecBlock writes a single spec block ending with a single trailing
// newline.
func renderSpecBlock(s domain.Spec) string {
	var b strings.Builder
	fmt.Fprintf(&b, "#### %s: %s\n", s.Code, s.Title)
	b.WriteString(specMarker(s))
	b.WriteByte('\n')
	if body := strings.TrimSpace(s.Body); body != "" {
		b.WriteByte('\n')
		b.WriteString(body)
		b.WriteByte('\n')
	}
	return b.String()
}

// renderPlan produces the canonical planning/{US-CODE}.md content.
//
// Layout:
//
//	<!-- archetipo:plan spec=US-001 -->
//
//	<plan body provided by the skill — Soluzione Tecnica, Strategia di Test, ...>
//
//	<!-- archetipo:tasks version=1 -->
//	| status | id | title | description | type | dependencies |
//	|---|---|---|---|---|---|
//	| TODO | TASK-01 | ... | ... | Impl | - |
//	| TODO | TASK-02 | ... | ... | Test | TASK-01 |
func renderPlan(specCode string, plan domain.PlanInput) string {
	var b strings.Builder
	b.WriteString(planMarker(specCode))
	b.WriteString("\n\n")
	if body := strings.TrimSpace(plan.PlanBody); body != "" {
		b.WriteString(body)
		b.WriteString("\n\n")
	}
	b.WriteString(tasksMarker())
	b.WriteByte('\n')
	b.WriteString(renderTaskTable(plan.Tasks))
	return b.String()
}

// renderTaskTable renders the canonical Implementation Tasks GFM table.
func renderTaskTable(tasks []domain.Task) string {
	var b strings.Builder
	b.WriteString("| status | id | title | description | type | dependencies |\n")
	b.WriteString("|---|---|---|---|---|---|\n")
	for _, t := range tasks {
		status := string(t.Status)
		if status == "" {
			status = string(domain.StatusTodo)
		}
		typ := string(t.Type)
		if typ == "" {
			typ = string(domain.TaskImpl)
		}
		deps := "-"
		if len(t.Dependencies) > 0 {
			deps = strings.Join(t.Dependencies, ",")
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n",
			status,
			t.ID,
			escapeCell(t.Title),
			escapeCell(t.Description),
			typ,
			deps,
		)
	}
	return b.String()
}

// escapeCell sanitizes a markdown table cell. Pipes break the table; newlines
// break the row.
func escapeCell(v string) string {
	v = strings.ReplaceAll(v, "\n", " ")
	v = strings.ReplaceAll(v, "\r", " ")
	v = strings.ReplaceAll(v, "|", "\\|")
	return v
}
