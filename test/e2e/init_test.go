package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
	initimpl "github.com/rootmyself/dcraft/internal/initconfig"
)

type initJSON struct {
	SchemaVersion    int               `json:"schema_version"`
	Action           string            `json:"action"`
	Outcome          core.Outcome      `json:"outcome"`
	RequiredApproval string            `json:"required_approval"`
	Target           core.Target       `json:"target"`
	Stages           []core.Stage      `json:"stages"`
	Diagnostics      []core.Diagnostic `json:"diagnostics"`
	Data             struct {
		Init initimpl.Report `json:"init"`
	} `json:"data"`
}

func parseInit(t *testing.T, stdout string) initJSON {
	t.Helper()
	var parsed initJSON
	if err := json.Unmarshal([]byte(stdout), &parsed); err != nil {
		t.Fatalf("parse init JSON: %v\n%s", err, stdout)
	}
	return parsed
}

func initCategory(parsed initJSON, category core.Category) bool {
	for _, d := range parsed.Diagnostics {
		if d.Category == category {
			return true
		}
	}
	return false
}

func TestInitJSONCreatesAnOrdinaryComposeFileAndNeverRunsIt(t *testing.T) {
	fake := newFakeDocker(t)
	project := t.TempDir()
	state := t.TempDir()
	res := runProcess(t, project, fake.env(t, state), strings.NewReader(""),
		"init", "--from", "image", "--service-name", "web", "--image", "busybox:1.36",
		"--port", "127.0.0.1:18080:80", "--volume", "./data:/data", "--env-var", "FOO=bar",
		"--restart", "unless-stopped", "--json")
	if res.code != 0 {
		t.Fatalf("exit = %d stderr=%q\n%s", res.code, res.stderr, res.stdout)
	}
	if strings.TrimSpace(res.stderr) != "" {
		t.Fatalf("JSON mode wrote to stderr: %q", res.stderr)
	}
	parsed := parseInit(t, res.stdout)
	if parsed.Action != "init" || parsed.Outcome != core.OutcomeSuccess {
		t.Fatalf("result = %+v", parsed)
	}
	if !samePath(t, parsed.Target.ProjectDir, project) {
		t.Fatalf("target dir = %q, want %q", parsed.Target.ProjectDir, project)
	}
	report := parsed.Data.Init
	if !samePath(t, report.Path, filepath.Join(project, "compose.yaml")) {
		t.Fatalf("path = %q", report.Path)
	}
	if report.ProjectName != "fake-project" {
		t.Fatalf("project name = %q (Compose authority)", report.ProjectName)
	}
	if report.Mode != "image" || report.Service != "web" || !report.Changed || report.Overwritten {
		t.Fatalf("report = %+v", report)
	}
	if !report.Validation.Checked || report.Validation.Command == "" {
		t.Fatalf("validation = %+v", report.Validation)
	}
	if report.NextCommand != "dcraft up" {
		t.Fatalf("next command = %q", report.NextCommand)
	}
	for _, want := range []string{"image: busybox:1.36", "18080:80", "./data:/data", "FOO: bar", "restart: unless-stopped"} {
		if !strings.Contains(report.Content, want) {
			t.Fatalf("content missing %q:\n%s", want, report.Content)
		}
	}
	written, err := os.ReadFile(filepath.Join(project, "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(written) != report.Content {
		t.Fatalf("saved content differs from the JSON content:\n%s", written)
	}
	if strings.Contains(string(written), "dcraft") {
		t.Fatalf("generated file contains a dcraft-only key:\n%s", written)
	}
	calls := strings.Join(fake.calls(t), "\n")
	if !strings.Contains(calls, "compose -f - config --format json") {
		t.Fatalf("Compose validation did not run: %s", calls)
	}
	for _, forbidden := range []string{" up", "create ", "down"} {
		if strings.Contains(calls, forbidden) {
			t.Fatalf("init ran a stack operation (%q):\n%s", forbidden, calls)
		}
	}
}

func TestInitNonInteractiveMissingInputFailsWithoutPromptingOrWriting(t *testing.T) {
	fake := newFakeDocker(t)
	project := t.TempDir()
	res := runProcess(t, project, fake.env(t, t.TempDir()), strings.NewReader(""), "init", "--json")
	if res.code != 2 {
		t.Fatalf("exit = %d, want 2\nstderr=%q\n%s", res.code, res.stderr, res.stdout)
	}
	parsed := parseInit(t, res.stdout)
	if parsed.Outcome != core.OutcomeFailure || !initCategory(parsed, core.CategoryInitInputInvalid) {
		t.Fatalf("result = %+v", parsed)
	}
	if _, err := os.Stat(filepath.Join(project, "compose.yaml")); !os.IsNotExist(err) {
		t.Fatal("missing input created a file")
	}
	for _, line := range fake.calls(t) {
		if strings.Contains(line, " config ") || strings.Contains(line, " up ") {
			t.Fatalf("missing input still ran Docker: %v", fake.calls(t))
		}
	}
}

func TestInitRefusesExistingComposeAndOverwritesOnlyWithOutputAndApproval(t *testing.T) {
	fake := newFakeDocker(t)
	project := t.TempDir()
	existing := filepath.Join(project, "compose.yaml")
	if err := os.WriteFile(existing, []byte("services:\n  old:\n    image: busybox\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	beforeHash := sha256File(t, existing)
	env := fake.env(t, t.TempDir())

	// The default destination is not an explicit destination: both the file
	// path and the independent approval are required.
	res := runProcess(t, project, env, strings.NewReader(""),
		"init", "--from", "image", "--service-name", "web", "--image", "busybox:1.36", "--json")
	if res.code != 2 {
		t.Fatalf("default existing exit = %d, want 2\n%s", res.code, res.stdout)
	}
	parsed := parseInit(t, res.stdout)
	if !initCategory(parsed, core.CategoryInitInputInvalid) {
		t.Fatalf("default existing diagnostics = %+v", parsed.Diagnostics)
	}
	foundPath := false
	for _, d := range parsed.Diagnostics {
		if strings.Contains(d.Detail, existing) || strings.Contains(d.Message, existing) {
			foundPath = true
		}
	}
	if !foundPath {
		t.Fatalf("refusal does not point to %s: %+v", existing, parsed.Diagnostics)
	}
	if sha256File(t, existing) != beforeHash {
		t.Fatal("the existing file changed")
	}

	res = runProcess(t, project, env, strings.NewReader(""),
		"init", "--from", "image", "--service-name", "web", "--image", "busybox:1.36",
		"--output", "compose.yaml", "--json")
	if res.code != 2 {
		t.Fatalf("unapproved overwrite exit = %d, want 2\n%s", res.code, res.stdout)
	}
	parsed = parseInit(t, res.stdout)
	if parsed.RequiredApproval != "file_overwrite" || !initCategory(parsed, core.CategoryApprovalRequired) {
		t.Fatalf("unapproved overwrite = %+v", parsed)
	}
	if sha256File(t, existing) != beforeHash {
		t.Fatal("the existing file changed without approval")
	}

	res = runProcess(t, project, env, strings.NewReader(""),
		"init", "--from", "image", "--service-name", "web", "--image", "busybox:1.36",
		"--output", "compose.yaml", "--approve", "file_overwrite", "--json")
	if res.code != 0 {
		t.Fatalf("approved overwrite exit = %d\n%s\n%s", res.code, res.stdout, res.stderr)
	}
	parsed = parseInit(t, res.stdout)
	if parsed.Outcome != core.OutcomeSuccess || !parsed.Data.Init.Overwritten {
		t.Fatalf("approved overwrite = %+v", parsed)
	}
	if sha256File(t, existing) == beforeHash {
		t.Fatal("approved overwrite did not replace the file")
	}
}

func TestInitRefusesToCreateASecondComposeFileBesideAnExistingOne(t *testing.T) {
	fake := newFakeDocker(t)
	project := t.TempDir()
	existing := filepath.Join(project, "compose.yml")
	if err := os.WriteFile(existing, []byte("services:\n  old:\n    image: busybox\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := sha256File(t, existing)
	res := runProcess(t, project, fake.env(t, t.TempDir()), strings.NewReader(""),
		"init", "--from", "image", "--service-name", "web", "--image", "busybox:1.36",
		"--output", "compose.yaml", "--approve", "file_overwrite", "--json")
	if res.code != 1 {
		t.Fatalf("exit = %d, want 1\n%s", res.code, res.stdout)
	}
	parsed := parseInit(t, res.stdout)
	if !initCategory(parsed, core.CategoryInitExistingConfiguration) {
		t.Fatalf("diagnostics = %+v", parsed.Diagnostics)
	}
	if !strings.Contains(parsed.Diagnostics[0].Detail, existing) {
		t.Fatalf("refusal does not point to %s: %+v", existing, parsed.Diagnostics[0])
	}
	if sha256File(t, existing) != before {
		t.Fatal("compose.yml changed")
	}
	if _, err := os.Stat(filepath.Join(project, "compose.yaml")); !os.IsNotExist(err) {
		t.Fatal("a second Compose file was created")
	}
	for _, line := range fake.calls(t) {
		if strings.Contains(line, " config ") || strings.Contains(line, " up ") {
			t.Fatalf("refused init still ran Docker: %v", fake.calls(t))
		}
	}
}

func TestInitHumanOutputShowsTheExactFileAndTheNextCommand(t *testing.T) {
	fake := newFakeDocker(t)
	project := t.TempDir()
	res := runProcess(t, project, fake.env(t, t.TempDir()), strings.NewReader(""),
		"init", "--from", "image", "--service-name", "web", "--image", "busybox:1.36",
		"--port", "127.0.0.1:18081:80")
	if res.code != 0 {
		t.Fatalf("exit = %d\nstderr=%s\nstdout=%s", res.code, res.stderr, res.stdout)
	}
	combined := res.stdout + res.stderr
	for _, want := range []string{
		"Compose 파일 미리보기",
		"services:",
		"image: busybox:1.36",
		"127.0.0.1:18081:80",
		"fake-project",
		"dcraft up",
		"저장은 실행이 아님",
		"Compose 파일 저장.",
	} {
		if !strings.Contains(combined, want) {
			t.Fatalf("human output missing %q:\n%s", want, combined)
		}
	}
}

func TestInitMissingDockerfileFailsBeforeComposeValidation(t *testing.T) {
	fake := newFakeDocker(t)
	project := t.TempDir()
	res := runProcess(t, project, fake.env(t, t.TempDir()), strings.NewReader(""),
		"init", "--from", "dockerfile", "--service-name", "web", "--json")
	if res.code != 2 {
		t.Fatalf("exit = %d, want 2\n%s", res.code, res.stdout)
	}
	parsed := parseInit(t, res.stdout)
	if !initCategory(parsed, core.CategoryInitInputInvalid) {
		t.Fatalf("diagnostics = %+v", parsed.Diagnostics)
	}
	if _, err := os.Stat(filepath.Join(project, "compose.yaml")); !os.IsNotExist(err) {
		t.Fatal("a file was created without a Dockerfile")
	}
	for _, line := range fake.calls(t) {
		if strings.Contains(line, " config ") {
			t.Fatalf("missing Dockerfile still ran Compose validation: %v", fake.calls(t))
		}
	}
}

func TestInitValidatesTheGeneratedFileWithoutAReachableEngine(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker CLI not available")
	}
	if out, err := exec.Command("docker", "compose", "version").CombinedOutput(); err != nil {
		t.Skipf("docker compose not available: %v\n%s", err, out)
	}
	project := uniqueInitProject(t)
	env := buildEnv(map[string]string{
		"DOCKER_HOST":      "unix:///nonexistent",
		"DCRAFT_STATE_DIR": t.TempDir(),
	})
	res := runProcess(t, project, env, strings.NewReader(""),
		"init", "--from", "image", "--service-name", "web", "--image", "busybox:1.36", "--json")
	if res.code != 0 {
		t.Fatalf("init needs an engine unexpectedly: exit=%d\n%s\n%s", res.code, res.stdout, res.stderr)
	}
	parsed := parseInit(t, res.stdout)
	if parsed.Outcome != core.OutcomeSuccess || !parsed.Data.Init.Validation.Checked {
		t.Fatalf("result = %+v", parsed)
	}
	if _, err := os.Stat(filepath.Join(project, "compose.yaml")); err != nil {
		t.Fatalf("compose.yaml missing: %v", err)
	}
}
