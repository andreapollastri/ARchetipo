package filefs

import (
	"bufio"
	"fmt"
	"regexp"
	"strings"

	"github.com/techreloaded-ar/ARchetipo/cli/internal/domain"
	"github.com/techreloaded-ar/ARchetipo/cli/internal/iox"
)

// specHeader matches `#### US-001: Title` (canonical) or any heading level
// 2-6 ending in `US-XXX:` so the parser is forgiving on heading depth chosen
// by the skill. Also captures the title.
var specHeader = regexp.MustCompile(`^(#{2,6})\s+(US-\d+):\s+(.+?)\s*$`)

// taskTableHeader is the canonical machine-readable table header. It MUST be
// the first row of the Implementation Tasks table. Skills that want to
// localize for human readers can use a second header row immediately below
// (a separator row); this parser ignores rows after the first header and
// alignment row.
var taskTableHeader = regexp.MustCompile(`^\|\s*status\s*\|\s*id\s*\|\s*title\s*\|\s*description\s*\|\s*type\s*\|\s*dependencies\s*\|\s*$`)

// taskTableSeparator matches a GFM alignment row.
var taskTableSeparator = regexp.MustCompile(`^\|\s*:?-+:?\s*(\|\s*:?-+:?\s*)+\|\s*$`)

// parseBacklog reads a BACKLOG.md content and extracts every spec block.
// The spec body is everything between the heading and the next spec heading
// (or EOF), excluding the marker line itself.
func parseBacklog(content string) ([]domain.Spec, error) {
	lines := strings.Split(content, "\n")
	var specs []domain.Spec
	i := 0
	for i < len(lines) {
		m := specHeader.FindStringSubmatch(lines[i])
		if m == nil {
			i++
			continue
		}
		title := m[3]
		// Look ahead a few lines for the marker. Skills MUST emit the
		// marker on the first non-blank line after the heading.
		var st domain.Spec
		var foundMarker bool
		j := i + 1
		for ; j < len(lines) && j-i <= 4; j++ {
			if strings.TrimSpace(lines[j]) == "" {
				continue
			}
			mk, ok := parseMarker(lines[j])
			if !ok {
				break
			}
			if mk.Kind != "spec" {
				break
			}
			s, err := specFromMarker(mk)
			if err != nil {
				return nil, iox.NewInvalidInput(
					fmt.Sprintf("malformed spec marker on line %d", j+1),
					"every spec must have a `<!-- archetipo:spec ... -->` marker as its first non-blank line",
					err)
			}
			st = s
			foundMarker = true
			j++ // consume marker line
			break
		}
		if !foundMarker {
			return nil, iox.NewInvalidInput(
				fmt.Sprintf("spec %q on line %d has no archetipo:spec marker", title, i+1),
				"add `<!-- archetipo:spec code=US-XXX epic=EP-XXX priority=... -->` after the heading",
				nil)
		}
		st.Title = title
		st.Ref = st.Code
		// Body extends until the next specHeader match or EOF.
		bodyStart := j
		k := bodyStart
		for ; k < len(lines); k++ {
			if specHeader.MatchString(lines[k]) {
				break
			}
		}
		body := strings.TrimSpace(strings.Join(lines[bodyStart:k], "\n"))
		st.Body = body
		specs = append(specs, st)
		i = k
	}
	return specs, nil
}

// parsePlan extracts task rows from a planning file. Returns the plan body
// (everything before the tasks marker) and the parsed tasks.
func parsePlan(content string) (planBody string, tasks []domain.Task, err error) {
	scanner := bufio.NewScanner(strings.NewReader(content))
	scanner.Buffer(make([]byte, 1024*1024), 8*1024*1024)
	var bodyB strings.Builder
	var tableLines []string
	state := stateBody
	for scanner.Scan() {
		line := scanner.Text()
		switch state {
		case stateBody:
			if mk, ok := parseMarker(line); ok {
				if mk.Kind == "tasks" {
					state = stateTable
					continue
				}
				// Drop preamble markers (plan, backlog, etc.) — they
				// are written by the writer and must not bleed into the
				// body, otherwise round-trip is not byte-stable.
				continue
			}
			bodyB.WriteString(line)
			bodyB.WriteByte('\n')
		case stateTable:
			tableLines = append(tableLines, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return "", nil, fmt.Errorf("reading plan: %w", err)
	}
	planBody = strings.TrimSpace(bodyB.String())
	if state == stateBody {
		return planBody, nil, nil // no tasks marker -> empty task list
	}
	parsed, perr := parseTaskTable(tableLines)
	if perr != nil {
		return "", nil, perr
	}
	return planBody, parsed, nil
}

const (
	stateBody = iota
	stateTable
)

// parseTaskTable reads a GFM table where the first row is the canonical
// machine-readable header (status|id|title|description|type|dependencies).
// Tolerates blank lines and additional content after the table ends (a blank
// line or a non-pipe-prefixed line).
func parseTaskTable(lines []string) ([]domain.Task, error) {
	// Skip leading blanks
	i := 0
	for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
		i++
	}
	if i >= len(lines) {
		return nil, nil
	}
	if !taskTableHeader.MatchString(lines[i]) {
		return nil, iox.NewInvalidInput(
			"missing canonical task table header",
			"first row after the `<!-- archetipo:tasks ... -->` marker must be `| status | id | title | description | type | dependencies |`",
			nil)
	}
	i++
	if i >= len(lines) || !taskTableSeparator.MatchString(lines[i]) {
		return nil, iox.NewInvalidInput(
			"missing GFM alignment row after task table header",
			"insert `|---|---|---|---|---|---|` immediately after the header",
			nil)
	}
	i++
	var tasks []domain.Task
	for ; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			break
		}
		if !strings.HasPrefix(strings.TrimLeft(line, " \t"), "|") {
			break
		}
		cells := splitTableRow(line)
		if len(cells) < 6 {
			return nil, iox.NewInvalidInput(
				fmt.Sprintf("task row has %d columns, expected 6", len(cells)),
				"columns: status | id | title | description | type | dependencies",
				nil)
		}
		t := domain.Task{
			Status:       domain.Status(strings.TrimSpace(cells[0])),
			ID:           strings.TrimSpace(cells[1]),
			Title:        strings.TrimSpace(cells[2]),
			Description:  strings.TrimSpace(cells[3]),
			Type:         domain.TaskType(strings.TrimSpace(cells[4])),
			Dependencies: splitCSV(strings.TrimSpace(cells[5])),
		}
		t.Ref = t.ID
		tasks = append(tasks, t)
	}
	return tasks, nil
}

// splitTableRow returns the cells of a GFM row, trimming the leading and
// trailing pipes. Does not handle escaped pipes (`\|`) — skills should
// avoid them in task content.
func splitTableRow(row string) []string {
	row = strings.TrimSpace(row)
	row = strings.TrimPrefix(row, "|")
	row = strings.TrimSuffix(row, "|")
	parts := strings.Split(row, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}
