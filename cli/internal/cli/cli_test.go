package cli_test

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
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

const storyJSON = `{"stories":[
	{"code":"US-001","title":"First","priority":"HIGH","story_points":3,"status":"TODO","epic":{"code":"EP-001","title":"Epic"}},
	{"code":"US-002","title":"Second","priority":"MEDIUM","story_points":2,"status":"TODO","epic":{"code":"EP-001","title":"Epic"}}
]}`

const planJSON = `{"plan_body":"## Plan\nDo the work","tasks":[
	{"id":"TASK-01","title":"Implement","type":"Impl","status":"TODO"},
	{"id":"TASK-02","title":"Test","type":"Test","status":"TODO"}
]}`

func TestConfig(t *testing.T) {
	newProject(t)
	res := runCLI(t, "", "config")
	kind, _ := decodeOK(t, res)
	if kind != "setup" {
		t.Fatalf("expected kind=setup, got %s", kind)
	}
}

func TestStoryAdd_EmptyBacklog(t *testing.T) {
	newProject(t)
	storiesFile := writeInputFile(t, "stories.json", storyJSON)
	res := runCLI(t, "", "story", "add", "--file", storiesFile)
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

func TestStoryAdd_Idempotent(t *testing.T) {
	newProject(t)
	storiesFile := writeInputFile(t, "stories.json", storyJSON)
	first := runCLI(t, "", "story", "add", "--file", storiesFile)
	if first.exit != 0 {
		t.Fatalf("first add failed: %s", first.stderr.String())
	}
	second := runCLI(t, "", "story", "add", "--file", storiesFile)
	_, data := decodeOK(t, second)
	skipped, _ := data["skipped"].([]any)
	if len(skipped) != 2 {
		t.Fatalf("expected 2 skipped codes, got %v", skipped)
	}
	if refs, ok := data["refs"].([]any); ok && len(refs) != 0 {
		t.Fatalf("expected no refs on full skip, got %v", refs)
	}
}

func TestStoryAdd_MixedSkipAndAppend(t *testing.T) {
	newProject(t)
	storiesFile := writeInputFile(t, "stories.json", storyJSON)
	if res := runCLI(t, "", "story", "add", "--file", storiesFile); res.exit != 0 {
		t.Fatalf("seed add failed: %s", res.stderr.String())
	}
	mixed := `{"stories":[
		{"code":"US-001","title":"dup","priority":"HIGH","story_points":1,"status":"TODO","epic":{"code":"EP-001","title":"Epic"}},
		{"code":"US-003","title":"new","priority":"LOW","story_points":1,"status":"TODO","epic":{"code":"EP-001","title":"Epic"}}
	]}`
	mixedFile := writeInputFile(t, "mixed.json", mixed)
	res := runCLI(t, "", "story", "add", "--file", mixedFile)
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

func TestBacklogShow(t *testing.T) {
	newProject(t)
	storiesFile := writeInputFile(t, "stories.json", storyJSON)
	if res := runCLI(t, "", "story", "add", "--file", storiesFile); res.exit != 0 {
		t.Fatalf("seed add failed: %s", res.stderr.String())
	}
	res := runCLI(t, "", "backlog", "show")
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

func TestStoryShow_ByCode(t *testing.T) {
	newProject(t)
	storiesFile := writeInputFile(t, "stories.json", storyJSON)
	if res := runCLI(t, "", "story", "add", "--file", storiesFile); res.exit != 0 {
		t.Fatalf("seed add failed: %s", res.stderr.String())
	}
	res := runCLI(t, "", "story", "show", "US-001")
	kind, data := decodeOK(t, res)
	if kind != "story" {
		t.Fatalf("expected kind=story, got %s", kind)
	}
	story, _ := data["story"].(map[string]any)
	if story["code"] != "US-001" {
		t.Fatalf("expected US-001, got %v", story["code"])
	}
	tasks, _ := data["tasks"].([]any)
	if len(tasks) != 0 {
		t.Fatalf("expected 0 tasks before plan, got %d", len(tasks))
	}
}

func TestStoryShow_ByStatus(t *testing.T) {
	newProject(t)
	storiesFile := writeInputFile(t, "stories.json", storyJSON)
	if res := runCLI(t, "", "story", "add", "--file", storiesFile); res.exit != 0 {
		t.Fatalf("seed add failed: %s", res.stderr.String())
	}
	res := runCLI(t, "", "story", "show", "--status", "TODO")
	_, data := decodeOK(t, res)
	story, _ := data["story"].(map[string]any)
	// Auto-pick: priority HIGH first → US-001 (HIGH) before US-002 (MEDIUM).
	if story["code"] != "US-001" {
		t.Fatalf("expected auto-pick US-001 (HIGH), got %v", story["code"])
	}
}

func TestStoryShow_BothFormsRejected(t *testing.T) {
	newProject(t)
	res := runCLI(t, "", "story", "show", "US-001", "--status", "TODO")
	_, code := decodeError(t, res)
	if code != iox.CodeInvalidInput {
		t.Fatalf("expected E_INVALID_INPUT, got %s", code)
	}
}

func TestStoryShow_NeitherFormGiven(t *testing.T) {
	newProject(t)
	res := runCLI(t, "", "story", "show")
	_, code := decodeError(t, res)
	if code != iox.CodeInvalidInput {
		t.Fatalf("expected E_INVALID_INPUT, got %s", code)
	}
}

func TestStoryPlan_TODOToPlanned(t *testing.T) {
	newProject(t)
	storiesFile := writeInputFile(t, "stories.json", storyJSON)
	planFile := writeInputFile(t, "plan.json", planJSON)
	if res := runCLI(t, "", "story", "add", "--file", storiesFile); res.exit != 0 {
		t.Fatalf("seed add failed: %s", res.stderr.String())
	}
	res := runCLI(t, "", "story", "plan", "US-001", "--file", planFile)
	if res.exit != 0 {
		t.Fatalf("plan failed: %s", res.stderr.String())
	}
	// Verify status moved by reading it back.
	show := runCLI(t, "", "story", "show", "US-001")
	_, data := decodeOK(t, show)
	story, _ := data["story"].(map[string]any)
	if story["status"] != "PLANNED" {
		t.Fatalf("expected status PLANNED, got %v", story["status"])
	}
	tasks, _ := data["tasks"].([]any)
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks after plan, got %d", len(tasks))
	}
	if _, err := os.Stat(expectedPlanPath("US-001")); err != nil {
		t.Fatalf("expected plan file at %s: %v", expectedPlanPath("US-001"), err)
	}
}

func TestStoryPlan_IdempotentOnPlanned(t *testing.T) {
	newProject(t)
	storiesFile := writeInputFile(t, "stories.json", storyJSON)
	planFile := writeInputFile(t, "plan.json", planJSON)
	if res := runCLI(t, "", "story", "add", "--file", storiesFile); res.exit != 0 {
		t.Fatalf("seed add failed: %s", res.stderr.String())
	}
	if res := runCLI(t, "", "story", "plan", "US-001", "--file", planFile); res.exit != 0 {
		t.Fatalf("first plan failed: %s", res.stderr.String())
	}
	res := runCLI(t, "", "story", "plan", "US-001", "--file", planFile)
	if res.exit != 0 {
		t.Fatalf("re-plan should be idempotent, got exit %d, stderr=%s", res.exit, res.stderr.String())
	}
}

func TestStoryPlan_FromStdin(t *testing.T) {
	newProject(t)
	storiesFile := writeInputFile(t, "stories.json", storyJSON)
	if res := runCLI(t, "", "story", "add", "--file", storiesFile); res.exit != 0 {
		t.Fatalf("seed add failed: %s", res.stderr.String())
	}
	res := runCLI(t, planJSON, "story", "plan", "US-001", "--file", "-")
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

func TestStoryStart_ConflictFromTodo(t *testing.T) {
	newProject(t)
	storiesFile := writeInputFile(t, "stories.json", storyJSON)
	if res := runCLI(t, "", "story", "add", "--file", storiesFile); res.exit != 0 {
		t.Fatalf("seed add failed: %s", res.stderr.String())
	}
	res := runCLI(t, "", "story", "start", "US-001")
	_, code := decodeError(t, res)
	if code != iox.CodeConflict {
		t.Fatalf("expected E_CONFLICT, got %s", code)
	}
}

func TestStoryStart_HappyPath(t *testing.T) {
	newProject(t)
	storiesFile := writeInputFile(t, "stories.json", storyJSON)
	planFile := writeInputFile(t, "plan.json", planJSON)
	if res := runCLI(t, "", "story", "add", "--file", storiesFile); res.exit != 0 {
		t.Fatalf("seed add failed: %s", res.stderr.String())
	}
	if res := runCLI(t, "", "story", "plan", "US-001", "--file", planFile); res.exit != 0 {
		t.Fatalf("plan failed: %s", res.stderr.String())
	}
	res := runCLI(t, "", "story", "start", "US-001")
	if res.exit != 0 {
		t.Fatalf("start failed: %s", res.stderr.String())
	}
	// Re-running is idempotent.
	again := runCLI(t, "", "story", "start", "US-001")
	if again.exit != 0 {
		t.Fatalf("idempotent start failed: %s", again.stderr.String())
	}
}

func TestStoryReview_HappyPathWithComment(t *testing.T) {
	newProject(t)
	storiesFile := writeInputFile(t, "stories.json", storyJSON)
	planFile := writeInputFile(t, "plan.json", planJSON)
	if res := runCLI(t, "", "story", "add", "--file", storiesFile); res.exit != 0 {
		t.Fatalf("seed add failed: %s", res.stderr.String())
	}
	if res := runCLI(t, "", "story", "plan", "US-001", "--file", planFile); res.exit != 0 {
		t.Fatalf("plan failed: %s", res.stderr.String())
	}
	if res := runCLI(t, "", "story", "start", "US-001"); res.exit != 0 {
		t.Fatalf("start failed: %s", res.stderr.String())
	}
	res := runCLI(t, "Closing notes for the story", "story", "review", "US-001")
	if res.exit != 0 {
		t.Fatalf("review failed: %s", res.stderr.String())
	}
	show := runCLI(t, "", "story", "show", "US-001")
	_, data := decodeOK(t, show)
	story, _ := data["story"].(map[string]any)
	if story["status"] != "REVIEW" {
		t.Fatalf("expected REVIEW, got %v", story["status"])
	}
}

func TestTaskDone_Positional(t *testing.T) {
	newProject(t)
	storiesFile := writeInputFile(t, "stories.json", storyJSON)
	planFile := writeInputFile(t, "plan.json", planJSON)
	if res := runCLI(t, "", "story", "add", "--file", storiesFile); res.exit != 0 {
		t.Fatalf("seed add failed: %s", res.stderr.String())
	}
	if res := runCLI(t, "", "story", "plan", "US-001", "--file", planFile); res.exit != 0 {
		t.Fatalf("plan failed: %s", res.stderr.String())
	}
	res := runCLI(t, "", "task", "done", "US-001", "TASK-01")
	if res.exit != 0 {
		t.Fatalf("task done failed: %s", res.stderr.String())
	}
}

func TestBoardMove_ChangesStatusAndOrder(t *testing.T) {
	newProject(t)
	storiesFile := writeInputFile(t, "stories.json", storyJSON)
	if res := runCLI(t, "", "story", "add", "--file", storiesFile); res.exit != 0 {
		t.Fatalf("seed add failed: %s", res.stderr.String())
	}
	if res := runCLI(t, "", "board", "move", "US-002", "--to", "review"); res.exit != 0 {
		t.Fatalf("board move failed: %s", res.stderr.String())
	}
	show := runCLI(t, "", "story", "show", "US-002")
	_, data := decodeOK(t, show)
	story, _ := data["story"].(map[string]any)
	if story["status"] != "REVIEW" {
		t.Fatalf("expected REVIEW after board move, got %v", story["status"])
	}
}

func TestPRDWrite(t *testing.T) {
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
