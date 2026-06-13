package jira

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/techreloaded-ar/ARchetipo/cli/internal/domain"
)

// codeFromSummary returns the US-NNN code from a summary like
// "US-001: Login utente". Empty when no code is present.
var codeRegexp = regexp.MustCompile(`^(US-\d+):`)

func codeFromSummary(summary string) string {
	m := codeRegexp.FindStringSubmatch(summary)
	if m == nil {
		return ""
	}
	return m[1]
}

var taskRegexp = regexp.MustCompile(`^(TASK-\d+):`)

func taskIDFromSummary(summary string) string {
	m := taskRegexp.FindStringSubmatch(summary)
	if m == nil {
		return ""
	}
	return m[1]
}

func titleAfterCode(summary string) string {
	idx := strings.Index(summary, ":")
	if idx == -1 {
		return summary
	}
	return strings.TrimSpace(summary[idx+1:])
}

// epicLabel encodes an epic code as a Jira label. Jira labels cannot contain
// spaces, so only the code (EP-001) lives in the label; the title is carried in
// the description marker (see renderDescription).
func epicLabel(code string) string { return code }

func epicCodeFromLabel(label string) string {
	if epicCodeRegexp.MatchString(label) {
		return label
	}
	return ""
}

var epicCodeRegexp = regexp.MustCompile(`^EP-\d+$`)

// Markers are HTML comments appended to issue/sub-task descriptions so the
// connector can round-trip metadata Jira has no native field for (epic title,
// task type, task dependencies). They are stripped before a body is returned.
var (
	epicMarkerRegexp = regexp.MustCompile(`(?m)^<!-- archetipo:epic (.*?) -->\s*$`)
	taskMarkerRegexp = regexp.MustCompile(`(?m)^<!-- archetipo:task (.*?) -->\s*$`)
)

// renderDescription appends the epic marker to the spec body when an epic is set.
func renderDescription(body string, epic domain.Epic) string {
	out := strings.TrimRight(body, "\n")
	if epic.Code != "" {
		marker := fmt.Sprintf("<!-- archetipo:epic %s|%s -->", epic.Code, epic.Title)
		if out != "" {
			out += "\n\n"
		}
		out += marker
	}
	return out
}

// parseDescription splits a stored description into the clean body and the epic
// it carries (empty when absent).
func parseDescription(raw string) (body string, epic domain.Epic) {
	m := epicMarkerRegexp.FindStringSubmatch(raw)
	if m != nil {
		parts := strings.SplitN(m[1], "|", 2)
		epic.Code = strings.TrimSpace(parts[0])
		if len(parts) == 2 {
			epic.Title = strings.TrimSpace(parts[1])
		}
	}
	body = strings.TrimSpace(epicMarkerRegexp.ReplaceAllString(raw, ""))
	return body, epic
}

// renderTaskDescription appends the task marker (type + dependencies) to a
// sub-task body.
func renderTaskDescription(t domain.Task) string {
	out := strings.TrimRight(firstNonEmpty(t.Description, t.Body), "\n")
	marker := "type=" + string(t.Type)
	if len(t.Dependencies) > 0 {
		marker += " deps=" + strings.Join(t.Dependencies, ",")
	}
	if out != "" {
		out += "\n\n"
	}
	return out + "<!-- archetipo:task " + marker + " -->"
}

// parseTaskDescription splits a sub-task description into its clean body, type
// and dependency list.
func parseTaskDescription(raw string) (body string, typ domain.TaskType, deps []string) {
	m := taskMarkerRegexp.FindStringSubmatch(raw)
	if m != nil {
		for _, field := range strings.Fields(m[1]) {
			switch {
			case strings.HasPrefix(field, "type="):
				typ = domain.TaskType(strings.TrimPrefix(field, "type="))
			case strings.HasPrefix(field, "deps="):
				csv := strings.TrimPrefix(field, "deps=")
				if csv != "" {
					deps = strings.Split(csv, ",")
				}
			}
		}
	}
	body = strings.TrimSpace(taskMarkerRegexp.ReplaceAllString(raw, ""))
	return body, typ, deps
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

// adfFromText converts ARchetipo markdown/plain text into the smallest ADF
// shape Jira v3 accepts, preserving the original text paragraph by paragraph.
func adfFromText(s string) map[string]any {
	lines := strings.Split(s, "\n")
	content := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		paragraph := map[string]any{"type": "paragraph"}
		if line != "" {
			paragraph["content"] = []map[string]any{{
				"type": "text",
				"text": line,
			}}
		}
		content = append(content, paragraph)
	}
	if len(content) == 0 {
		content = append(content, map[string]any{"type": "paragraph"})
	}
	return map[string]any{
		"type":    "doc",
		"version": 1,
		"content": content,
	}
}

// textFromADF accepts both Jira v3 ADF documents and legacy plain strings. It
// extracts text leaves in document order and keeps paragraph boundaries as
// newline separators so ARchetipo markers still round-trip.
func textFromADF(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var legacy string
	if err := json.Unmarshal(raw, &legacy); err == nil {
		return legacy
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return ""
	}
	var paragraphs []string
	collectADFParagraphs(doc, &paragraphs)
	return strings.TrimSpace(strings.Join(paragraphs, "\n"))
}

func collectADFParagraphs(v any, out *[]string) {
	m, ok := v.(map[string]any)
	if !ok {
		return
	}
	if typ, _ := m["type"].(string); typ == "paragraph" {
		var b strings.Builder
		collectADFText(m["content"], &b)
		*out = append(*out, b.String())
		return
	}
	if children, ok := m["content"].([]any); ok {
		for _, child := range children {
			collectADFParagraphs(child, out)
		}
	}
}

func collectADFText(v any, b *strings.Builder) {
	switch x := v.(type) {
	case []any:
		for _, child := range x {
			collectADFText(child, b)
		}
	case map[string]any:
		if text, ok := x["text"].(string); ok {
			b.WriteString(text)
		}
		if children, ok := x["content"].([]any); ok {
			collectADFText(children, b)
		}
	}
}

// writeFile mirrors filefs.writeFile (the jira connector still persists the PRD
// as a local markdown file).
func writeFile(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating dir: %w", err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}
