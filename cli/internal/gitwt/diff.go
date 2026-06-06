package gitwt

import (
	"context"
	"strconv"
	"strings"
)

// FileDiff is the structured diff of a single file. Status is one of
// "modified", "added", "deleted", "renamed".
type FileDiff struct {
	OldPath string `json:"old_path"`
	NewPath string `json:"new_path"`
	Status  string `json:"status"`
	Hunks   []Hunk `json:"hunks"`
}

// Hunk is a contiguous block of changes within a file.
type Hunk struct {
	Header string `json:"header"`
	Lines  []Line `json:"lines"`
}

// Line is one line of a hunk. Kind is "context", "add" or "del". OldLine and
// NewLine are 1-based line numbers in the pre/post image (0 when not present on
// that side), used to anchor inline review comments.
type Line struct {
	Kind    string `json:"kind"`
	OldLine int    `json:"old_line"`
	NewLine int    `json:"new_line"`
	Text    string `json:"text"`
}

// Diff returns the structured diff between forkBase and branch, restricted to
// the changes introduced on branch since it diverged from forkBase
// (`git diff <forkBase>...<branch>`).
func Diff(ctx context.Context, repoRoot, forkBase, branch string) ([]FileDiff, error) {
	out, err := runGit(ctx, repoRoot, "diff", "--no-color", "--find-renames", forkBase+"..."+branch, "--")
	if err != nil {
		return nil, err
	}
	return parseUnifiedDiff(out), nil
}

// DiffWorkingTree returns the structured diff between base and the working tree
// (`git diff <base>`). Used as a fallback when a spec has no recorded branch.
func DiffWorkingTree(ctx context.Context, repoRoot, base string) ([]FileDiff, error) {
	out, err := runGit(ctx, repoRoot, "diff", "--no-color", "--find-renames", base, "--")
	if err != nil {
		return nil, err
	}
	return parseUnifiedDiff(out), nil
}

// parseUnifiedDiff converts the textual unified diff produced by `git diff`
// into structured FileDiffs. It is tolerant of the metadata lines git emits
// (index, mode, rename from/to, new/deleted file) and tracks old/new line
// numbers across hunks.
func parseUnifiedDiff(text string) []FileDiff {
	var files []FileDiff
	var cur *FileDiff
	var oldLine, newLine int

	flush := func() {
		if cur != nil {
			files = append(files, *cur)
			cur = nil
		}
	}

	lines := strings.Split(text, "\n")
	for _, ln := range lines {
		switch {
		case strings.HasPrefix(ln, "diff --git "):
			flush()
			old, new := parseDiffGitHeader(ln)
			cur = &FileDiff{OldPath: old, NewPath: new, Status: "modified"}
		case cur == nil:
			// Skip anything before the first file header.
			continue
		case strings.HasPrefix(ln, "new file mode"):
			cur.Status = "added"
		case strings.HasPrefix(ln, "deleted file mode"):
			cur.Status = "deleted"
		case strings.HasPrefix(ln, "rename from "):
			cur.Status = "renamed"
			cur.OldPath = strings.TrimPrefix(ln, "rename from ")
		case strings.HasPrefix(ln, "rename to "):
			cur.Status = "renamed"
			cur.NewPath = strings.TrimPrefix(ln, "rename to ")
		case strings.HasPrefix(ln, "--- "):
			if p := trimDiffPath(strings.TrimPrefix(ln, "--- ")); p != "" {
				cur.OldPath = p
			}
		case strings.HasPrefix(ln, "+++ "):
			if p := trimDiffPath(strings.TrimPrefix(ln, "+++ ")); p != "" {
				cur.NewPath = p
			}
		case strings.HasPrefix(ln, "@@"):
			oldLine, newLine = parseHunkHeader(ln)
			cur.Hunks = append(cur.Hunks, Hunk{Header: ln})
		case strings.HasPrefix(ln, "index ") || strings.HasPrefix(ln, "old mode ") ||
			strings.HasPrefix(ln, "new mode ") || strings.HasPrefix(ln, "similarity index ") ||
			strings.HasPrefix(ln, "dissimilarity index ") || strings.HasPrefix(ln, "Binary files "):
			continue
		case strings.HasPrefix(ln, "\\"):
			// "\ No newline at end of file" — attach to nothing, just skip.
			continue
		default:
			if len(cur.Hunks) == 0 {
				continue
			}
			h := &cur.Hunks[len(cur.Hunks)-1]
			if ln == "" {
				continue
			}
			switch ln[0] {
			case '+':
				h.Lines = append(h.Lines, Line{Kind: "add", NewLine: newLine, Text: ln[1:]})
				newLine++
			case '-':
				h.Lines = append(h.Lines, Line{Kind: "del", OldLine: oldLine, Text: ln[1:]})
				oldLine++
			case ' ':
				h.Lines = append(h.Lines, Line{Kind: "context", OldLine: oldLine, NewLine: newLine, Text: ln[1:]})
				oldLine++
				newLine++
			}
		}
	}
	flush()
	return files
}

// parseDiffGitHeader extracts the a/ and b/ paths from a "diff --git a/x b/y"
// line. Paths with spaces are not quoted by git in this position for the common
// case; we split on " b/" which is unambiguous for the prefixes git uses.
func parseDiffGitHeader(ln string) (old, new string) {
	rest := strings.TrimPrefix(ln, "diff --git ")
	if i := strings.Index(rest, " b/"); i >= 0 {
		old = strings.TrimPrefix(rest[:i], "a/")
		new = strings.TrimPrefix(rest[i+1:], "b/")
		return old, new
	}
	return "", ""
}

// trimDiffPath strips the a/ or b/ prefix from a --- / +++ path and maps
// /dev/null to empty.
func trimDiffPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "/dev/null" {
		return ""
	}
	p = strings.TrimPrefix(p, "a/")
	p = strings.TrimPrefix(p, "b/")
	return p
}

// parseHunkHeader reads the starting old/new line numbers from a hunk header
// like "@@ -12,7 +13,8 @@ optional section heading".
func parseHunkHeader(ln string) (oldStart, newStart int) {
	// Format: @@ -oldStart[,oldCount] +newStart[,newCount] @@
	body := ln
	if i := strings.Index(body, "@@"); i >= 0 {
		body = body[i+2:]
	}
	if i := strings.Index(body, "@@"); i >= 0 {
		body = body[:i]
	}
	for _, tok := range strings.Fields(body) {
		if strings.HasPrefix(tok, "-") {
			oldStart = leadingInt(tok[1:])
		} else if strings.HasPrefix(tok, "+") {
			newStart = leadingInt(tok[1:])
		}
	}
	return oldStart, newStart
}

func leadingInt(s string) int {
	if i := strings.IndexByte(s, ','); i >= 0 {
		s = s[:i]
	}
	n, _ := strconv.Atoi(s)
	return n
}
