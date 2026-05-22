// Package filefs implements the Connector interface against the local
// filesystem. Markdown files are the persistence layer; HTML-comment markers
// are the machine-readable source of truth for structured fields.
//
// Marker grammar (one line per marker):
//
//	<!-- archetipo:KIND k1=v1 k2=v2 ... -->
//
// Values are URL-escaped when they contain whitespace, '=', or comment
// terminators. Multi-value fields use commas as separator.
//
// Recognized kinds:
//
//	spec     fields of a backlog spec (code, epic, priority, points, status, blocked_by, scope)
//	backlog  preamble of BACKLOG.md (version)
//	plan     preamble of planning/{US-CODE}.md (spec)
//	tasks    sentinel marking the start of the Implementation Tasks table
package filefs

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/techreloaded-ar/ARchetipo/cli/internal/domain"
)

// markerLine matches a single archetipo marker line and captures the kind and
// the raw key=value attribute string.
var markerLine = regexp.MustCompile(`^\s*<!--\s*archetipo:(\w+)\s*(.*?)\s*-->\s*$`)

// attribute matches a single key=value pair inside a marker. Values may be
// percent-encoded for whitespace and special characters.
var attribute = regexp.MustCompile(`(\w+)=(\S*)`)

// marker is the parsed in-memory representation of a marker line.
type marker struct {
	Kind  string
	Attrs map[string]string
}

// parseMarker reads a single marker line. Returns ok=false when the line
// is not a marker.
func parseMarker(line string) (marker, bool) {
	m := markerLine.FindStringSubmatch(line)
	if m == nil {
		return marker{}, false
	}
	mk := marker{Kind: m[1], Attrs: map[string]string{}}
	for _, pair := range attribute.FindAllStringSubmatch(m[2], -1) {
		k, v := pair[1], pair[2]
		decoded, err := url.QueryUnescape(v)
		if err == nil {
			v = decoded
		}
		mk.Attrs[k] = v
	}
	return mk, true
}

// formatMarker renders a marker line. Keys are emitted in deterministic
// alphabetical order so the output is byte-stable across runs.
func formatMarker(kind string, attrs map[string]string) string {
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("<!-- archetipo:")
	b.WriteString(kind)
	for _, k := range keys {
		b.WriteByte(' ')
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(escapeAttr(attrs[k]))
	}
	b.WriteString(" -->")
	return b.String()
}

// escapeAttr percent-encodes characters that would break the marker grammar.
func escapeAttr(v string) string {
	if v == "" {
		return ""
	}
	if !strings.ContainsAny(v, " \t\r\n=<->") {
		return v
	}
	return url.QueryEscape(v)
}

// specMarker builds the marker line for a spec.
func specMarker(s domain.Spec) string {
	attrs := map[string]string{
		"code":     s.Code,
		"epic":     s.Epic.Code,
		"priority": string(s.Priority),
		"points":   strconv.Itoa(s.Points),
		"status":   string(s.Status),
	}
	if len(s.BlockedBy) > 0 {
		attrs["blocked_by"] = strings.Join(s.BlockedBy, ",")
	}
	if s.Epic.Title != "" {
		attrs["epic_title"] = s.Epic.Title
	}
	if s.Scope != "" {
		attrs["scope"] = string(s.Scope)
	}
	return formatMarker("spec", attrs)
}

// specFromMarker reconstructs the structured fields of a spec from a
// marker. The Title and Body are filled by the parser separately.
func specFromMarker(m marker) (domain.Spec, error) {
	if m.Kind != "spec" {
		return domain.Spec{}, fmt.Errorf("expected kind=spec, got %q", m.Kind)
	}
	s := domain.Spec{
		Code: m.Attrs["code"],
		Epic: domain.Epic{
			Code:  m.Attrs["epic"],
			Title: m.Attrs["epic_title"],
		},
		Priority: domain.Priority(m.Attrs["priority"]),
		Status:   domain.Status(m.Attrs["status"]),
		Scope:    domain.Scope(m.Attrs["scope"]),
	}
	if v := m.Attrs["points"]; v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return domain.Spec{}, fmt.Errorf("invalid points=%q: %w", v, err)
		}
		s.Points = n
	}
	if v := m.Attrs["blocked_by"]; v != "" {
		s.BlockedBy = splitCSV(v)
	}
	return s, nil
}

// planMarker builds the preamble marker for a planning file.
func planMarker(specCode string) string {
	return formatMarker("plan", map[string]string{"spec": specCode})
}

// backlogMarker builds the preamble marker for the backlog file.
func backlogMarker() string {
	return formatMarker("backlog", map[string]string{"version": "1"})
}

// tasksMarker is the sentinel that introduces the Implementation Tasks table.
func tasksMarker() string {
	return formatMarker("tasks", map[string]string{"version": "1"})
}

func splitCSV(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" && p != "-" {
			out = append(out, p)
		}
	}
	return out
}
