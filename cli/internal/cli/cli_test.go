package cli_test

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/techreloaded-ar/ARchetipo/cli/internal/cli"
	"github.com/techreloaded-ar/ARchetipo/cli/internal/iox"
)

// Each test uses t.Chdir(t.TempDir()) so the file connector picks up an empty
// project root with default paths. Tests are sequential because t.Chdir is
// process-wide; parallelism would race on the cwd.

type result struct {
	exit   int
	stdout bytes.Buffer
	stderr bytes.Buffer
}

func runCLI(t *testing.T, stdin string, args ...string) result {
	t.Helper()
	r := result{}
	in := io.Reader(strings.NewReader(stdin))
	r.exit = cli.Execute(args, in, &r.stdout, &r.stderr)
	return r
}

func newProject(t *testing.T) {
	t.Helper()
	t.Chdir(t.TempDir())
}

func mustRun(t *testing.T, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s failed: %v\n%s", name, strings.Join(args, " "), err, string(out))
	}
}

func mustOutput(t *testing.T, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s failed: %v\n%s", name, strings.Join(args, " "), err, string(out))
	}
	return strings.TrimSpace(string(out))
}

func writeWorktreeConfig(t *testing.T) {
	t.Helper()
	if err := os.MkdirAll(".archetipo", 0o755); err != nil {
		t.Fatal(err)
	}
	body := `connector: file
worktree:
  enabled: true
  base: main
  dir: .archetipo/worktrees
  branch_prefix: archetipo/
`
	if err := os.WriteFile(filepath.Join(".archetipo", "config.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func initGitMain(t *testing.T) {
	t.Helper()
	mustRun(t, "git", "init", "-b", "main")
	mustRun(t, "git", "config", "user.email", "archetipo-test@example.com")
	mustRun(t, "git", "config", "user.name", "ARchetipo Test")
	mustRun(t, "git", "commit", "--allow-empty", "-m", "base")
}

func seedStartedWorktreeSpec(t *testing.T) {
	t.Helper()
	writeWorktreeConfig(t)
	initGitMain(t)
	specsFile := writeInputFile(t, "specs.json", specJSON)
	planFile := writeInputFile(t, "plan.json", planJSON)
	if res := runCLI(t, "", "spec", "add", "--file", specsFile); res.exit != 0 {
		t.Fatalf("seed add failed: %s", res.stderr.String())
	}
	if res := runCLI(t, "", "spec", "plan", "US-001", "--file", planFile); res.exit != 0 {
		t.Fatalf("plan failed: %s", res.stderr.String())
	}
	if res := runCLI(t, "", "spec", "start", "US-001"); res.exit != 0 {
		t.Fatalf("start failed: %s", res.stderr.String())
	}
}

func writeInputFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(".", name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func expectedPlanPath(code string) string {
	return filepath.Join(".", ".archetipo", "plans", code+"-plan.yaml")
}

func decodeOK(t *testing.T, res result) (string, map[string]any) {
	t.Helper()
	if res.exit != 0 {
		t.Fatalf("expected exit 0, got %d. stderr=%s", res.exit, res.stderr.String())
	}
	var env struct {
		Schema string         `json:"schema"`
		Kind   string         `json:"kind"`
		Data   map[string]any `json:"data"`
	}
	if err := json.Unmarshal(res.stdout.Bytes(), &env); err != nil {
		t.Fatalf("decoding stdout: %v\nraw=%s", err, res.stdout.String())
	}
	return env.Kind, env.Data
}

func decodeError(t *testing.T, res result) (int, string) {
	t.Helper()
	if res.exit == 0 {
		t.Fatalf("expected non-zero exit, got 0. stdout=%s", res.stdout.String())
	}
	var env struct {
		Schema string `json:"schema"`
		Kind   string `json:"kind"`
		Error  struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Hint    string `json:"hint"`
		} `json:"error"`
	}
	if err := json.Unmarshal(res.stderr.Bytes(), &env); err != nil {
		t.Fatalf("decoding stderr: %v\nraw=%s", err, res.stderr.String())
	}
	return res.exit, env.Error.Code
}

const specJSON = `{"specs":[
	{"code":"US-001","title":"First","priority":"HIGH","points":3,"status":"TODO","epic":{"code":"EP-001","title":"Epic"}},
	{"code":"US-002","title":"Second","priority":"MEDIUM","points":2,"status":"TODO","epic":{"code":"EP-001","title":"Epic"}}
]}`

const planJSON = `{"plan_body":"## Plan\nDo the work","tasks":[
	{"id":"TASK-01","title":"Implement","type":"Impl","status":"TODO"},
	{"id":"TASK-02","title":"Test","type":"Test","status":"TODO"}
]}`

func TestConfigShow(t *testing.T) {
	newProject(t)
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	res := runCLI(t, "", "config", "show")
	kind, data := decodeOK(t, res)
	if kind != "setup" {
		t.Fatalf("expected kind=setup, got %s", kind)
	}
	if data["project_root"] != root {
		t.Fatalf("expected project_root=%s, got %v", root, data["project_root"])
	}
}

func TestSpecAdd_EmptyBacklog(t *testing.T) {
	newProject(t)
	specsFile := writeInputFile(t, "specs.json", specJSON)
	res := runCLI(t, "", "spec", "add", "--file", specsFile)
	kind, data := decodeOK(t, res)
	if kind != "write_result" {
		t.Fatalf("expected kind=write_result, got %s", kind)
	}
	if ok, _ := data["ok"].(bool); !ok {
		t.Fatalf("expected ok=true, got %v", data["ok"])
	}
	if _, present := data["skipped"]; present {
		t.Fatalf("expected no skipped on initial save, got %v", data["skipped"])
	}
}

func TestSpecAdd_Idempotent(t *testing.T) {
	newProject(t)
	specsFile := writeInputFile(t, "specs.json", specJSON)
	first := runCLI(t, "", "spec", "add", "--file", specsFile)
	if first.exit != 0 {
		t.Fatalf("first add failed: %s", first.stderr.String())
	}
	second := runCLI(t, "", "spec", "add", "--file", specsFile)
	_, data := decodeOK(t, second)
	skipped, _ := data["skipped"].([]any)
	if len(skipped) != 2 {
		t.Fatalf("expected 2 skipped codes, got %v", skipped)
	}
	if refs, ok := data["refs"].([]any); ok && len(refs) != 0 {
		t.Fatalf("expected no refs on full skip, got %v", refs)
	}
}

func TestSpecAdd_MixedSkipAndAppend(t *testing.T) {
	newProject(t)
	specsFile := writeInputFile(t, "specs.json", specJSON)
	if res := runCLI(t, "", "spec", "add", "--file", specsFile); res.exit != 0 {
		t.Fatalf("seed add failed: %s", res.stderr.String())
	}
	mixed := `{"specs":[
		{"code":"US-001","title":"dup","priority":"HIGH","points":1,"status":"TODO","epic":{"code":"EP-001","title":"Epic"}},
		{"code":"US-003","title":"new","priority":"LOW","points":1,"status":"TODO","epic":{"code":"EP-001","title":"Epic"}}
	]}`
	mixedFile := writeInputFile(t, "mixed.json", mixed)
	res := runCLI(t, "", "spec", "add", "--file", mixedFile)
	_, data := decodeOK(t, res)
	skipped, _ := data["skipped"].([]any)
	if len(skipped) != 1 || skipped[0] != "US-001" {
		t.Fatalf("expected skipped=[US-001], got %v", skipped)
	}
	refs, _ := data["refs"].([]any)
	if len(refs) == 0 {
		t.Fatalf("expected refs for US-003, got empty")
	}
}

func TestSpecList(t *testing.T) {
	newProject(t)
	specsFile := writeInputFile(t, "specs.json", specJSON)
	if res := runCLI(t, "", "spec", "add", "--file", specsFile); res.exit != 0 {
		t.Fatalf("seed add failed: %s", res.stderr.String())
	}
	res := runCLI(t, "", "spec", "list")
	kind, data := decodeOK(t, res)
	if kind != "backlog" {
		t.Fatalf("expected kind=backlog, got %s", kind)
	}
	items, _ := data["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	summary, _ := data["summary"].(map[string]any)
	codes, _ := summary["codes"].([]any)
	if len(codes) != 2 {
		t.Fatalf("expected 2 codes in summary, got %d", len(codes))
	}
}

func TestSpecShow_ByCode(t *testing.T) {
	newProject(t)
	specsFile := writeInputFile(t, "specs.json", specJSON)
	if res := runCLI(t, "", "spec", "add", "--file", specsFile); res.exit != 0 {
		t.Fatalf("seed add failed: %s", res.stderr.String())
	}
	res := runCLI(t, "", "spec", "show", "US-001")
	kind, data := decodeOK(t, res)
	if kind != "spec" {
		t.Fatalf("expected kind=spec, got %s", kind)
	}
	spec, _ := data["spec"].(map[string]any)
	if spec["code"] != "US-001" {
		t.Fatalf("expected US-001, got %v", spec["code"])
	}
	tasks, _ := data["tasks"].([]any)
	if len(tasks) != 0 {
		t.Fatalf("expected 0 tasks before plan, got %d", len(tasks))
	}
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if data["workdir"] != root {
		t.Fatalf("expected workdir=%s, got %v", root, data["workdir"])
	}
}

func TestSpecShow_MissingCodeRejected(t *testing.T) {
	newProject(t)
	res := runCLI(t, "", "spec", "show")
	_, code := decodeError(t, res)
	if code != iox.CodeInvalidInput {
		t.Fatalf("expected E_INVALID_INPUT, got %s", code)
	}
}

func TestSpecNext_AutoPickByStatus(t *testing.T) {
	newProject(t)
	specsFile := writeInputFile(t, "specs.json", specJSON)
	if res := runCLI(t, "", "spec", "add", "--file", specsFile); res.exit != 0 {
		t.Fatalf("seed add failed: %s", res.stderr.String())
	}
	res := runCLI(t, "", "spec", "next", "--status", "TODO")
	kind, data := decodeOK(t, res)
	if kind != "spec" {
		t.Fatalf("expected kind=spec, got %s", kind)
	}
	spec, _ := data["spec"].(map[string]any)
	// Auto-pick: priority HIGH first → US-001 (HIGH) before US-002 (MEDIUM).
	if spec["code"] != "US-001" {
		t.Fatalf("expected auto-pick US-001 (HIGH), got %v", spec["code"])
	}
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if data["workdir"] != root {
		t.Fatalf("expected workdir=%s, got %v", root, data["workdir"])
	}
}

func TestSpecShow_WorkdirBeforeAndAfterWorktreeStart(t *testing.T) {
	newProject(t)
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	writeWorktreeConfig(t)
	initGitMain(t)

	specsFile := writeInputFile(t, "specs.json", specJSON)
	planFile := writeInputFile(t, "plan.json", planJSON)
	if res := runCLI(t, "", "spec", "add", "--file", specsFile); res.exit != 0 {
		t.Fatalf("seed add failed: %s", res.stderr.String())
	}
	if res := runCLI(t, "", "spec", "plan", "US-001", "--file", planFile); res.exit != 0 {
		t.Fatalf("plan failed: %s", res.stderr.String())
	}

	before := runCLI(t, "", "spec", "show", "US-001")
	_, beforeData := decodeOK(t, before)
	if beforeData["workdir"] != root {
		t.Fatalf("expected workdir before start=%s, got %v", root, beforeData["workdir"])
	}

	if res := runCLI(t, "", "spec", "start", "US-001"); res.exit != 0 {
		t.Fatalf("start failed: stdout=%s stderr=%s", res.stdout.String(), res.stderr.String())
	}

	after := runCLI(t, "", "spec", "show", "US-001")
	_, afterData := decodeOK(t, after)
	wantWorkdir := filepath.Join(root, ".archetipo", "worktrees", "US-001")
	if afterData["workdir"] != wantWorkdir {
		t.Fatalf("expected workdir after start=%s, got %v", wantWorkdir, afterData["workdir"])
	}
	spec, _ := afterData["spec"].(map[string]any)
	if spec["worktree"] != filepath.Join(".archetipo", "worktrees", "US-001") {
		t.Fatalf("expected persisted spec.worktree, got %v", spec["worktree"])
	}
}

func TestSpecNext_MissingStatusRejected(t *testing.T) {
	newProject(t)
	res := runCLI(t, "", "spec", "next")
	_, code := decodeError(t, res)
	if code != iox.CodeInvalidInput {
		t.Fatalf("expected E_INVALID_INPUT, got %s", code)
	}
}

func TestSpecPlan_TODOToPlanned(t *testing.T) {
	newProject(t)
	specsFile := writeInputFile(t, "specs.json", specJSON)
	planFile := writeInputFile(t, "plan.json", planJSON)
	if res := runCLI(t, "", "spec", "add", "--file", specsFile); res.exit != 0 {
		t.Fatalf("seed add failed: %s", res.stderr.String())
	}
	res := runCLI(t, "", "spec", "plan", "US-001", "--file", planFile)
	if res.exit != 0 {
		t.Fatalf("plan failed: %s", res.stderr.String())
	}
	// Verify status moved by reading it back.
	show := runCLI(t, "", "spec", "show", "US-001")
	_, data := decodeOK(t, show)
	spec, _ := data["spec"].(map[string]any)
	if spec["status"] != "PLANNED" {
		t.Fatalf("expected status PLANNED, got %v", spec["status"])
	}
	tasks, _ := data["tasks"].([]any)
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks after plan, got %d", len(tasks))
	}
	if _, err := os.Stat(expectedPlanPath("US-001")); err != nil {
		t.Fatalf("expected plan file at %s: %v", expectedPlanPath("US-001"), err)
	}
}

func TestSpecPlan_IdempotentOnPlanned(t *testing.T) {
	newProject(t)
	specsFile := writeInputFile(t, "specs.json", specJSON)
	planFile := writeInputFile(t, "plan.json", planJSON)
	if res := runCLI(t, "", "spec", "add", "--file", specsFile); res.exit != 0 {
		t.Fatalf("seed add failed: %s", res.stderr.String())
	}
	if res := runCLI(t, "", "spec", "plan", "US-001", "--file", planFile); res.exit != 0 {
		t.Fatalf("first plan failed: %s", res.stderr.String())
	}
	res := runCLI(t, "", "spec", "plan", "US-001", "--file", planFile)
	if res.exit != 0 {
		t.Fatalf("re-plan should be idempotent, got exit %d, stderr=%s", res.exit, res.stderr.String())
	}
}

func TestSpecPlan_FromStdin(t *testing.T) {
	newProject(t)
	specsFile := writeInputFile(t, "specs.json", specJSON)
	if res := runCLI(t, "", "spec", "add", "--file", specsFile); res.exit != 0 {
		t.Fatalf("seed add failed: %s", res.stderr.String())
	}
	res := runCLI(t, planJSON, "spec", "plan", "US-001", "--file", "-")
	_, data := decodeOK(t, res)
	refs, _ := data["refs"].([]any)
	if len(refs) == 0 {
		t.Fatalf("expected refs after stdin plan save")
	}
	firstRef, _ := refs[0].(map[string]any)
	gotPath, _ := firstRef["path"].(string)
	wantPath, err := filepath.Abs(expectedPlanPath("US-001"))
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != wantPath {
		t.Fatalf("expected first ref path %s, got %s", wantPath, gotPath)
	}
}

func TestSpecStart_ConflictFromTodo(t *testing.T) {
	newProject(t)
	specsFile := writeInputFile(t, "specs.json", specJSON)
	if res := runCLI(t, "", "spec", "add", "--file", specsFile); res.exit != 0 {
		t.Fatalf("seed add failed: %s", res.stderr.String())
	}
	res := runCLI(t, "", "spec", "start", "US-001")
	_, code := decodeError(t, res)
	if code != iox.CodeConflict {
		t.Fatalf("expected E_CONFLICT, got %s", code)
	}
}

func TestSpecStart_HappyPath(t *testing.T) {
	newProject(t)
	specsFile := writeInputFile(t, "specs.json", specJSON)
	planFile := writeInputFile(t, "plan.json", planJSON)
	if res := runCLI(t, "", "spec", "add", "--file", specsFile); res.exit != 0 {
		t.Fatalf("seed add failed: %s", res.stderr.String())
	}
	if res := runCLI(t, "", "spec", "plan", "US-001", "--file", planFile); res.exit != 0 {
		t.Fatalf("plan failed: %s", res.stderr.String())
	}
	res := runCLI(t, "", "spec", "start", "US-001")
	if res.exit != 0 {
		t.Fatalf("start failed: %s", res.stderr.String())
	}
	// Re-running is idempotent.
	again := runCLI(t, "", "spec", "start", "US-001")
	if again.exit != 0 {
		t.Fatalf("idempotent start failed: %s", again.stderr.String())
	}
}

func TestSpecReview_HappyPathWithComment(t *testing.T) {
	newProject(t)
	specsFile := writeInputFile(t, "specs.json", specJSON)
	planFile := writeInputFile(t, "plan.json", planJSON)
	if res := runCLI(t, "", "spec", "add", "--file", specsFile); res.exit != 0 {
		t.Fatalf("seed add failed: %s", res.stderr.String())
	}
	if res := runCLI(t, "", "spec", "plan", "US-001", "--file", planFile); res.exit != 0 {
		t.Fatalf("plan failed: %s", res.stderr.String())
	}
	if res := runCLI(t, "", "spec", "start", "US-001"); res.exit != 0 {
		t.Fatalf("start failed: %s", res.stderr.String())
	}
	res := runCLI(t, "Closing notes for the spec", "spec", "review", "US-001")
	if res.exit != 0 {
		t.Fatalf("review failed: %s", res.stderr.String())
	}
	show := runCLI(t, "", "spec", "show", "US-001")
	_, data := decodeOK(t, show)
	spec, _ := data["spec"].(map[string]any)
	if spec["status"] != "REVIEW" {
		t.Fatalf("expected REVIEW, got %v", spec["status"])
	}
}

func TestSpecReview_CommentFromFile(t *testing.T) {
	newProject(t)
	specsFile := writeInputFile(t, "specs.json", specJSON)
	planFile := writeInputFile(t, "plan.json", planJSON)
	if res := runCLI(t, "", "spec", "add", "--file", specsFile); res.exit != 0 {
		t.Fatalf("seed add failed: %s", res.stderr.String())
	}
	if res := runCLI(t, "", "spec", "plan", "US-001", "--file", planFile); res.exit != 0 {
		t.Fatalf("plan failed: %s", res.stderr.String())
	}
	if res := runCLI(t, "", "spec", "start", "US-001"); res.exit != 0 {
		t.Fatalf("start failed: %s", res.stderr.String())
	}
	commentFile := writeInputFile(t, "comment.md", "## Done\nShipping it.")
	res := runCLI(t, "", "spec", "review", "US-001", "--file", commentFile)
	if res.exit != 0 {
		t.Fatalf("review failed: %s", res.stderr.String())
	}
}

func TestSpecReview_CommitsDirtyWorktreeBeforeReview(t *testing.T) {
	newProject(t)
	writeWorktreeConfig(t)
	initGitMain(t)
	if err := os.WriteFile("hello.txt", []byte("Hello from ARchetipo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "git", "add", "hello.txt")
	mustRun(t, "git", "commit", "-m", "seed hello")

	specsFile := writeInputFile(t, "specs.json", specJSON)
	planFile := writeInputFile(t, "plan.json", planJSON)
	if res := runCLI(t, "", "spec", "add", "--file", specsFile); res.exit != 0 {
		t.Fatalf("seed add failed: %s", res.stderr.String())
	}
	if res := runCLI(t, "", "spec", "plan", "US-001", "--file", planFile); res.exit != 0 {
		t.Fatalf("plan failed: %s", res.stderr.String())
	}
	if res := runCLI(t, "", "spec", "start", "US-001"); res.exit != 0 {
		t.Fatalf("start failed: %s", res.stderr.String())
	}

	worktree := filepath.Join(".archetipo", "worktrees", "US-001")
	if err := os.WriteFile(filepath.Join(worktree, "hello.txt"), []byte("Hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree, "archetipo.txt"), []byte("from ARchetipo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := runCLI(t, "Closing notes", "spec", "review", "US-001")
	if res.exit != 0 {
		t.Fatalf("review failed: stdout=%s stderr=%s", res.stdout.String(), res.stderr.String())
	}
	show := runCLI(t, "", "spec", "show", "US-001")
	_, data := decodeOK(t, show)
	spec, _ := data["spec"].(map[string]any)
	if spec["status"] != "REVIEW" {
		t.Fatalf("expected REVIEW, got %v", spec["status"])
	}
	if status := mustOutput(t, "git", "-C", worktree, "status", "--porcelain"); status != "" {
		t.Fatalf("expected clean worktree after review commit, got %q", status)
	}
	diff := mustOutput(t, "git", "diff", "--name-status", "main...archetipo/US-001")
	if !strings.Contains(diff, "M\thello.txt") {
		t.Fatalf("expected review diff to include modified hello.txt, got:\n%s", diff)
	}
	if !strings.Contains(diff, "A\tarchetipo.txt") {
		t.Fatalf("expected review diff to include new archetipo.txt, got:\n%s", diff)
	}
}

func TestSpecReview_CleanWorktreeDoesNotCreateCommit(t *testing.T) {
	newProject(t)
	seedStartedWorktreeSpec(t)
	before := mustOutput(t, "git", "rev-parse", "archetipo/US-001")

	res := runCLI(t, "", "spec", "review", "US-001")
	if res.exit != 0 {
		t.Fatalf("review failed: %s", res.stderr.String())
	}
	after := mustOutput(t, "git", "rev-parse", "archetipo/US-001")
	if after != before {
		t.Fatalf("expected no new commit for clean worktree, before=%s after=%s", before, after)
	}
	show := runCLI(t, "", "spec", "show", "US-001")
	_, data := decodeOK(t, show)
	spec, _ := data["spec"].(map[string]any)
	if spec["status"] != "REVIEW" {
		t.Fatalf("expected REVIEW, got %v", spec["status"])
	}
}

func TestTaskDone_Positional(t *testing.T) {
	newProject(t)
	specsFile := writeInputFile(t, "specs.json", specJSON)
	planFile := writeInputFile(t, "plan.json", planJSON)
	if res := runCLI(t, "", "spec", "add", "--file", specsFile); res.exit != 0 {
		t.Fatalf("seed add failed: %s", res.stderr.String())
	}
	if res := runCLI(t, "", "spec", "plan", "US-001", "--file", planFile); res.exit != 0 {
		t.Fatalf("plan failed: %s", res.stderr.String())
	}
	res := runCLI(t, "", "task", "done", "US-001", "TASK-01")
	if res.exit != 0 {
		t.Fatalf("task done failed: %s", res.stderr.String())
	}
}

func TestSpecMove_ChangesStatusAndOrder(t *testing.T) {
	newProject(t)
	specsFile := writeInputFile(t, "specs.json", specJSON)
	if res := runCLI(t, "", "spec", "add", "--file", specsFile); res.exit != 0 {
		t.Fatalf("seed add failed: %s", res.stderr.String())
	}
	if res := runCLI(t, "", "spec", "move", "US-002", "--to", "review"); res.exit != 0 {
		t.Fatalf("spec move failed: %s", res.stderr.String())
	}
	show := runCLI(t, "", "spec", "show", "US-002")
	_, data := decodeOK(t, show)
	spec, _ := data["spec"].(map[string]any)
	if spec["status"] != "REVIEW" {
		t.Fatalf("expected REVIEW after spec move, got %v", spec["status"])
	}
}

func TestSpecMove_InvalidToReturnsInvalidInput(t *testing.T) {
	newProject(t)
	specsFile := writeInputFile(t, "specs.json", specJSON)
	if res := runCLI(t, "", "spec", "add", "--file", specsFile); res.exit != 0 {
		t.Fatalf("seed add failed: %s", res.stderr.String())
	}
	res := runCLI(t, "", "spec", "move", "US-001", "--to", "BOGUS")
	_, code := decodeError(t, res)
	if code != iox.CodeInvalidInput {
		t.Fatalf("expected E_INVALID_INPUT, got %s", code)
	}
}

func TestPRDWrite_FromStdin(t *testing.T) {
	newProject(t)
	res := runCLI(t, "# Product Vision\n\nMVP for early adopters.", "prd", "write")
	kind, data := decodeOK(t, res)
	if kind != "write_result" {
		t.Fatalf("expected kind=write_result, got %s", kind)
	}
	if ok, _ := data["ok"].(bool); !ok {
		t.Fatalf("expected ok=true, got %v", data["ok"])
	}
}

func TestPRDWrite_FromFileFlag(t *testing.T) {
	newProject(t)
	prdFile := writeInputFile(t, "PRD.md", "# Product Vision\n\nFrom file.")
	res := runCLI(t, "", "prd", "write", "--file", prdFile)
	kind, data := decodeOK(t, res)
	if kind != "write_result" {
		t.Fatalf("expected kind=write_result, got %s", kind)
	}
	if ok, _ := data["ok"].(bool); !ok {
		t.Fatalf("expected ok=true, got %v", data["ok"])
	}
}

func TestVersionCommand(t *testing.T) {
	res := runCLI(t, "", "version")
	if res.exit != 0 {
		t.Fatalf("exit=%d stderr=%s", res.exit, res.stderr.String())
	}
	if !strings.HasPrefix(res.stdout.String(), "archetipo ") {
		t.Fatalf("unexpected stdout: %q", res.stdout.String())
	}
}

func TestVersionFlagMatchesCommand(t *testing.T) {
	cmd := runCLI(t, "", "version")
	flag := runCLI(t, "", "--version")
	if cmd.stdout.String() != flag.stdout.String() {
		t.Fatalf("mismatch: cmd=%q flag=%q", cmd.stdout.String(), flag.stdout.String())
	}
}
