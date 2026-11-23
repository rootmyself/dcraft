package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/stack"
)

const lifecycleConfig = `{"services":{"web":{"image":"nginx:alpine"},"api":{"image":"api:local"}}}`

const lifecyclePS = `{"Name":"hello-world-web-1","Service":"web","State":"running","Health":"","Image":"nginx:alpine","ExitCode":0}`

type lifecycleResult struct {
	SchemaVersion int               `json:"schema_version"`
	OperationID   string            `json:"operation_id"`
	Action        string            `json:"action"`
	Outcome       core.Outcome      `json:"outcome"`
	Target        core.Target       `json:"target"`
	Stages        []core.Stage      `json:"stages"`
	Diagnostics   []core.Diagnostic `json:"diagnostics"`
	Data          struct {
		Lifecycle struct {
			Action            string          `json:"action"`
			ProjectName       string          `json:"project_name"`
			ProjectNameSource string          `json:"project_name_source"`
			Scope             string          `json:"scope"`
			Services          []string        `json:"services"`
			VolumesRemoved    bool            `json:"volumes_removed"`
			ConfigApplied     bool            `json:"config_applied"`
			State             *stack.Snapshot `json:"state"`
			Notices           []string        `json:"notices"`
		} `json:"lifecycle"`
	} `json:"data"`
}

func parseLifecycle(t *testing.T, out string) lifecycleResult {
	t.Helper()
	var res lifecycleResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("parse lifecycle JSON: %v\n%s", err, out)
	}
	return res
}

func lifecycleFake(t *testing.T) *fakeDocker {
	t.Helper()
	f := newFakeDocker(t)
	f.writeConfig(t, lifecycleConfig)
	f.writePS(t, lifecyclePS)
	return f
}

func projectNamed(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeProject(t, dir)
	return dir
}

func lifecycleCategories(res lifecycleResult) []core.Category {
	var out []core.Category
	for _, d := range res.Diagnostics {
		out = append(out, d.Category)
	}
	return out
}

func findCall(t *testing.T, calls []string, contains string) string {
	t.Helper()
	for _, line := range calls {
		if strings.Contains(line, contains) {
			return line
		}
	}
	t.Fatalf("no call contains %q:\n%s", contains, strings.Join(calls, "\n"))
	return ""
}

func countCalls(calls []string, contains string) int {
	n := 0
	for _, line := range calls {
		if strings.Contains(line, contains) {
			n++
		}
	}
	return n
}

func TestLifecycleUpAllocatesReadableNameAndReusesIt(t *testing.T) {
	fake := lifecycleFake(t)
	project := projectNamed(t, "hello-world")
	stateDir := t.TempDir()
	env := fake.env(t, stateDir)

	res := runProcess(t, project, env, strings.NewReader(""), "up", "--path", project, "--json")
	if res.code != 0 || res.stderr != "" {
		t.Fatalf("up exit=%d stderr=%q stdout=%q", res.code, res.stderr, res.stdout)
	}
	parsed := parseLifecycle(t, res.stdout)
	if parsed.Outcome != core.OutcomeSuccess || parsed.Action != "up" {
		t.Fatalf("result = %+v", parsed)
	}
	var stageNames []string
	for _, st := range parsed.Stages {
		stageNames = append(stageNames, st.Name)
	}
	if want := "resolve_target,preflight,resolve_project,coordinate,allocate_name,up,observe,history"; strings.Join(stageNames, ",") != want {
		t.Fatalf("stages = %v, want %s", stageNames, want)
	}
	report := parsed.Data.Lifecycle
	if report.ProjectName != "hello-world" {
		t.Fatalf("project name = %q, want hello-world", report.ProjectName)
	}
	if report.ProjectNameSource != "allocated" || report.Scope != "stack" {
		t.Fatalf("report = %+v", report)
	}
	if report.Services == nil || report.Notices == nil {
		t.Fatalf("arrays must be [] not null: %+v", report)
	}
	if !report.ConfigApplied || report.VolumesRemoved {
		t.Fatalf("effect flags = %+v", report)
	}
	if report.State == nil || report.State.Summary.Running != 1 {
		t.Fatalf("observed state = %+v", report.State)
	}
	if parsed.Target.ProjectDir != project {
		t.Fatalf("target dir = %q", parsed.Target.ProjectDir)
	}

	calls := fake.calls(t)
	upLine := findCall(t, calls, " up --detach")
	if !strings.Contains(upLine, "-p hello-world ") {
		t.Fatalf("up call does not pin the allocated name: %q", upLine)
	}
	if countCalls(calls, " down") != 0 || countCalls(calls, " -v") != 0 {
		t.Fatalf("up removed resources: %v", calls)
	}
	if countCalls(calls, "compose ls -a --format json") != 1 {
		t.Fatalf("existing projects were not checked once: %v", calls)
	}

	// Repeating up reuses the same readable name and does not duplicate it.
	again := runProcess(t, project, env, strings.NewReader(""), "up", "--path", project, "--json")
	if again.code != 0 {
		t.Fatalf("second up exit=%d stderr=%q", again.code, again.stderr)
	}
	againParsed := parseLifecycle(t, again.stdout)
	if got := againParsed.Data.Lifecycle.ProjectName; got != "hello-world" {
		t.Fatalf("second up name = %q", got)
	}
	if n := countCalls(fake.calls(t), " up --detach"); n != 2 {
		t.Fatalf("up calls = %d, want 2", n)
	}
	if claims := nameClaims(t, stateDir); len(claims) != 1 {
		t.Fatalf("name claims = %d, want 1", len(claims))
	}

	// Stopping and resuming keeps the same name.
	stop := runProcess(t, project, env, strings.NewReader(""), "stop", "--path", project, "--json")
	if stop.code != 0 || stop.stderr != "" {
		t.Fatalf("stop exit=%d stderr=%q", stop.code, stop.stderr)
	}
	stopParsed := parseLifecycle(t, stop.stdout)
	if stopParsed.Data.Lifecycle.ProjectName != "hello-world" || stopParsed.Data.Lifecycle.ConfigApplied {
		t.Fatalf("stop report = %+v", stopParsed.Data.Lifecycle)
	}
	if line := findCall(t, fake.calls(t), " stop"); !strings.Contains(line, "-p hello-world stop") {
		t.Fatalf("stop call = %q", line)
	}
	resume := runProcess(t, project, env, strings.NewReader(""), "up", "--path", project, "--json")
	if resume.code != 0 {
		t.Fatalf("resume exit=%d stderr=%q", resume.code, resume.stderr)
	}
	if got := parseLifecycle(t, resume.stdout).Data.Lifecycle.ProjectName; got != "hello-world" {
		t.Fatalf("resume name = %q", got)
	}
	history := historyFor(t, stateDir)
	if strings.Count(history, "\n") < 3 {
		t.Fatalf("history was not written:\n%s", history)
	}
}

func TestLifecycleSameNamedFoldersGetSmallestSuffix(t *testing.T) {
	fake := lifecycleFake(t)
	root := t.TempDir()
	dirA := filepath.Join(root, "a", "hello-world")
	dirB := filepath.Join(root, "b", "hello-world")
	for _, dir := range []string{dirA, dirB} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		writeProject(t, dir)
	}
	stateDir := t.TempDir()
	env := fake.env(t, stateDir)

	first := runProcess(t, dirA, env, strings.NewReader(""), "up", "--path", dirA, "--json")
	if first.code != 0 {
		t.Fatalf("first up exit=%d stderr=%q", first.code, first.stderr)
	}
	second := runProcess(t, dirB, env, strings.NewReader(""), "up", "--path", dirB, "--json")
	if second.code != 0 {
		t.Fatalf("second up exit=%d stderr=%q", second.code, second.stderr)
	}
	nameA := parseLifecycle(t, first.stdout).Data.Lifecycle.ProjectName
	nameB := parseLifecycle(t, second.stdout).Data.Lifecycle.ProjectName
	if nameA != "hello-world" || nameB != "hello-world-1" {
		t.Fatalf("names = %q, %q; want hello-world, hello-world-1", nameA, nameB)
	}
	if line := findCall(t, fake.calls(t), "up --detach"); !strings.Contains(line, "-p hello-world ") {
		t.Fatalf("first up = %q", line)
	}
	upLines := fake.calls(t)
	var bLine string
	for _, line := range upLines {
		if strings.Contains(line, " up --detach") && strings.Contains(line, "-p hello-world-1 ") {
			bLine = line
		}
	}
	if bLine == "" {
		t.Fatalf("second up did not pin hello-world-1:\n%s", strings.Join(upLines, "\n"))
	}
	// The first folder still owns hello-world.
	again := runProcess(t, dirA, env, strings.NewReader(""), "up", "--path", dirA, "--json")
	if again.code != 0 {
		t.Fatal(again.stderr)
	}
	if got := parseLifecycle(t, again.stdout).Data.Lifecycle.ProjectName; got != "hello-world" {
		t.Fatalf("first folder was renumbered to %q", got)
	}
}

func TestLifecycleExplicitNameCollisionExits2WithoutMutation(t *testing.T) {
	fake := lifecycleFake(t)
	dirA := projectNamed(t, "hello-world")
	dirB := projectNamed(t, "hello-world")
	stateDir := t.TempDir()
	env := fake.env(t, stateDir)

	if res := runProcess(t, dirA, env, strings.NewReader(""), "up", "--path", dirA, "--json"); res.code != 0 {
		t.Fatalf("baseline up: %d %s", res.code, res.stderr)
	}
	before := fake.calls(t)
	res := runProcess(t, dirB, env, strings.NewReader(""),
		"up", "--path", dirB, "-p", "hello-world", "--json")
	if res.code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr=%q stdout=%q)", res.code, res.stderr, res.stdout)
	}
	if res.stderr != "" {
		t.Fatalf("JSON mode wrote stderr: %q", res.stderr)
	}
	parsed := parseLifecycle(t, res.stdout)
	if parsed.Outcome != core.OutcomeFailure {
		t.Fatalf("outcome = %q", parsed.Outcome)
	}
	if cats := lifecycleCategories(parsed); len(cats) != 1 || cats[0] != core.CategoryProjectNameConflict {
		t.Fatalf("diagnostics = %+v", parsed.Diagnostics)
	}
	if !strings.Contains(parsed.Diagnostics[0].Message, "hello-world") {
		t.Fatalf("conflict message = %q", parsed.Diagnostics[0].Message)
	}
	target := strings.Join(parsed.Diagnostics[0].NextActions, " ")
	if !strings.Contains(target, "project-name") || !strings.Contains(target, "조작하지 않음") {
		t.Fatalf("conflict guidance = %v", parsed.Diagnostics[0].NextActions)
	}
	after := fake.calls(t)
	if n := countCalls(after, " up --detach") - countCalls(before, " up --detach"); n != 0 {
		t.Fatalf("collision still ran up: %v", after)
	}

	// Human mode fails with Korean guidance and never prompts.
	human := runProcess(t, dirB, env, strings.NewReader(""),
		"up", "--path", dirB, "-p", "hello-world")
	if human.code != 2 {
		t.Fatalf("human exit = %d, want 2", human.code)
	}
	if human.stdout != "" {
		t.Fatalf("failure wrote stdout: %q", human.stdout)
	}
	for _, want := range []string{"작업 실패", "hello-world", "다음 행동", "project-name"} {
		if !strings.Contains(human.stderr, want) {
			t.Fatalf("stderr missing %q:\n%s", want, human.stderr)
		}
	}
}

func TestLifecycleUnknownServiceExits2BeforeAllocation(t *testing.T) {
	fake := lifecycleFake(t)
	project := projectNamed(t, "hello-world")
	stateDir := t.TempDir()
	res := runProcess(t, project, fake.env(t, stateDir), strings.NewReader(""),
		"up", "--path", project, "-s", "worker", "--json")
	if res.code != 2 {
		t.Fatalf("exit = %d, want 2", res.code)
	}
	parsed := parseLifecycle(t, res.stdout)
	if cats := lifecycleCategories(parsed); len(cats) != 1 || cats[0] != core.CategoryServiceUnknown {
		t.Fatalf("diagnostics = %+v", parsed.Diagnostics)
	}
	if detail := parsed.Diagnostics[0].Detail; !strings.Contains(detail, "api") || !strings.Contains(detail, "web") {
		t.Fatalf("detail = %q", detail)
	}
	if countCalls(fake.calls(t), " up --detach") != 0 {
		t.Fatalf("unknown service ran up: %v", fake.calls(t))
	}
	if _, err := os.Stat(filepath.Join(stateDir, "names.json")); !os.IsNotExist(err) {
		t.Fatalf("unknown service allocated a name: %v", err)
	}
}

func TestLifecycleCommandFlagsStayInTheDocumentedOrder(t *testing.T) {
	fake := lifecycleFake(t)
	project := projectNamed(t, "hello-world")
	composePath := filepath.Join(project, "compose.yaml")
	envFile := filepath.Join(project, "custom.env")
	if err := os.WriteFile(envFile, []byte("TAG=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fake.writeConfig(t, `{"name":"chosen-name","services":{"web":{}}}`)
	stateDir := t.TempDir()
	env := fake.env(t, stateDir)

	res := runProcess(t, project, env, strings.NewReader(""),
		"up", "--path", project, "-p", "chosen-name", "-f", composePath,
		"--env-file", envFile, "--profile", "dev", "--json")
	if res.code != 0 {
		t.Fatalf("up exit=%d stderr=%q", res.code, res.stderr)
	}
	line := findCall(t, fake.calls(t), " up --detach")
	for _, part := range []string{"-f " + composePath, "--env-file " + envFile, "--profile dev", "-p chosen-name"} {
		if !strings.Contains(line, part) {
			t.Fatalf("up line missing %q: %q", part, line)
		}
		if strings.Index(line, part) > strings.Index(line, " up ") {
			t.Fatalf("global input %q came after the subcommand: %q", part, line)
		}
	}

	if res := runProcess(t, project, env, strings.NewReader(""), "stop", "--path", project, "-p", "chosen-name", "-s", "web", "--json"); res.code != 0 {
		t.Fatalf("stop exit=%d stderr=%q", res.code, res.stderr)
	}
	if line := findCall(t, fake.calls(t), " stop"); !strings.HasSuffix(line, "-p chosen-name stop web") {
		t.Fatalf("stop line = %q", line)
	}

	if res := runProcess(t, project, env, strings.NewReader(""), "restart", "--path", project, "-p", "chosen-name", "-s", "web", "--json"); res.code != 0 {
		t.Fatalf("restart exit=%d stderr=%q", res.code, res.stderr)
	}
	if line := findCall(t, fake.calls(t), " restart"); !strings.HasSuffix(line, "-p chosen-name restart --no-deps web") {
		t.Fatalf("restart line = %q", line)
	}

	if res := runProcess(t, project, env, strings.NewReader(""), "down", "--path", project, "-p", "chosen-name", "--json"); res.code != 0 {
		t.Fatalf("down exit=%d stderr=%q", res.code, res.stderr)
	}
	downLine := findCall(t, fake.calls(t), " down")
	if !strings.HasSuffix(downLine, "-p chosen-name down") {
		t.Fatalf("down line = %q", downLine)
	}
	for _, forbidden := range []string{" -v", "--volumes", "--rmi", "--remove-orphans"} {
		if strings.Contains(downLine, forbidden) {
			t.Fatalf("down used %q: %q", forbidden, downLine)
		}
	}
}

func TestLifecycleDoesNotPrintOrStoreSecretValues(t *testing.T) {
	fake := lifecycleFake(t)
	project := projectNamed(t, "hello-world")
	fake.writeConfig(t, `{"services":{"web":{"environment":{"SECRET_TOKEN":"s3cr3t-value"}}}}`)
	stateDir := t.TempDir()
	env := fake.env(t, stateDir)

	jsonRes := runProcess(t, project, env, strings.NewReader(""), "up", "--path", project, "--json")
	humanRes := runProcess(t, project, env, strings.NewReader(""), "up", "--path", project)
	for _, res := range []processResult{jsonRes, humanRes} {
		if res.code != 0 {
			t.Fatalf("exit=%d stderr=%q", res.code, res.stderr)
		}
		if strings.Contains(res.stdout, "s3cr3t-value") || strings.Contains(res.stderr, "s3cr3t-value") {
			t.Fatalf("secret leaked to output:\n%s\n%s", res.stdout, res.stderr)
		}
	}
	if strings.Contains(historyFor(t, stateDir), "s3cr3t-value") {
		t.Fatal("secret leaked into operation history")
	}
	if strings.Contains(strings.Join(fake.calls(t), "\n"), "s3cr3t-value") {
		t.Fatal("secret leaked into command arguments")
	}
}

func TestLifecycleCancellationExits130AndStatesPartialEffects(t *testing.T) {
	fake := lifecycleFake(t)
	project := projectNamed(t, "hello-world")
	env := fake.envExtra(t, t.TempDir(), map[string]string{"DOCKER_FAKE_SLEEP_UP": "10"})
	cmd := exec.Command(binaryPath, "up", "--path", project, "--json")
	cmd.Dir = project
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1 * time.Second)
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	err := cmd.Wait()
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("wait: %v", err)
		}
		code = exitErr.ExitCode()
	}
	if code != 130 {
		t.Fatalf("exit = %d, want 130 (stderr=%q)", code, stderr.String())
	}
	parsed := parseLifecycle(t, stdout.String())
	if parsed.Outcome != core.OutcomeCanceled {
		t.Fatalf("outcome = %q", parsed.Outcome)
	}
	if cats := lifecycleCategories(parsed); len(cats) == 0 || cats[0] != core.CategoryCanceled {
		t.Fatalf("diagnostics = %+v", parsed.Diagnostics)
	}
	joined := strings.Join(parsed.Diagnostics[0].NextActions, " ")
	if !strings.Contains(parsed.Diagnostics[0].Message, "남아 있을 수") && !strings.Contains(joined, "남아 있을 수") {
		t.Fatalf("cancellation does not state partial effects: %+v", parsed.Diagnostics[0])
	}
	upCanceled := false
	for _, st := range parsed.Stages {
		if st.Name == "up" && st.Status == core.StageCanceled {
			upCanceled = true
		}
	}
	if !upCanceled {
		t.Fatalf("up stage was not canceled: %+v", parsed.Stages)
	}
}

func nameClaims(t *testing.T, stateDir string) map[string]json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(stateDir, "names.json"))
	if err != nil {
		t.Fatalf("read names.json: %v", err)
	}
	var doc struct {
		Claims map[string]json.RawMessage `json:"claims"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("names.json: %v", err)
	}
	return doc.Claims
}

func historyFor(t *testing.T, stateDir string) string {
	t.Helper()
	var out strings.Builder
	stacksDir := filepath.Join(stateDir, "stacks")
	entries, err := os.ReadDir(stacksDir)
	if err != nil {
		t.Fatalf("read stacks dir: %v", err)
	}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(stacksDir, entry.Name(), "history.jsonl"))
		if err != nil {
			continue
		}
		out.Write(data)
	}
	return out.String()
}

type lifecyclePTYStep struct {
	wait string
	send string
	// action runs after the wait text appears and before send. It is used to
	// simulate an external change between a preview and the next keypress.
	action func()
}

// runPTYSteps drives the real binary through a pty, waiting for each screen
// before sending the next key sequence, then quits with q.
func runLifecyclePTYSteps(t *testing.T, dir string, env []string, steps []lifecyclePTYStep) (string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binaryPath)
	cmd.Dir = dir
	cmd.Env = append(env, "TERM=xterm-256color")
	f, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("start pty: %v", err)
	}
	defer f.Close()
	_ = pty.Setsize(f, &pty.Winsize{Rows: 50, Cols: 140})

	out := &syncBuffer{}
	go func() { _, _ = io.Copy(out, f) }()

	waitFor := func(text string) {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for !screenContains(out.String(), text) {
			if time.Now().After(deadline) {
				t.Fatalf("TUI did not render %q; output:\n%s", text, out.String())
			}
			time.Sleep(25 * time.Millisecond)
		}
	}
	for _, step := range steps {
		waitFor(step.wait)
		if step.action != nil {
			step.action()
		}
		if step.send == "" {
			continue
		}
		if _, err := f.Write([]byte(step.send)); err != nil {
			t.Fatalf("send %q: %v", step.send, err)
		}
		// Bubble Tea reads the pty in chunks; keep multi-key steps from
		// being coalesced into one read.
		time.Sleep(80 * time.Millisecond)
	}
	waitFor("[q] 종료")
	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()
	for attempt := 0; attempt < 4; attempt++ {
		if _, err := f.Write([]byte("q")); err != nil {
			break
		}
		select {
		case err := <-waitDone:
			if err != nil {
				var exitErr *exec.ExitError
				if errors.As(err, &exitErr) {
					return out.String(), exitErr.ExitCode()
				}
				t.Fatalf("wait: %v", err)
			}
			return out.String(), 0
		case <-time.After(3 * time.Second):
		}
	}
	_ = cmd.Process.Kill()
	t.Fatalf("TUI did not exit after q; output:\n%s", out.String())
	return "", -1
}

func TestLifecycleTUIMenuSelectsStopAndQuitsWithoutChangingServices(t *testing.T) {
	fake := lifecycleFake(t)
	project := projectNamed(t, "hello-world")
	env := fake.env(t, t.TempDir())
	output, code := runLifecyclePTYSteps(t, project, env, []lifecyclePTYStep{
		{wait: "상태 새로 고침", send: ""},
		{wait: "[↑↓] 이동", send: menuDown(t, "stop")},
		{wait: "▸ 스택 중지", send: "\r"},
		{wait: "서비스 선택", send: "\r"},
		{wait: "작업 실행 확인", send: ""},
	})
	if code != 0 {
		t.Fatalf("TUI exit = %d\n%s", code, output)
	}
	for _, want := range []string{"스택 중지", "대상", "효과", "[esc] 취소"} {
		if !screenContains(output, want) {
			t.Fatalf("confirmation missing %q:\n%s", want, output)
		}
	}
	assertNoMutatingCalls(t, fake.calls(t))
}

func TestLifecycleTUIRunsUpThroughTheSameEngine(t *testing.T) {
	fake := lifecycleFake(t)
	project := projectNamed(t, "hello-world")
	env := fake.env(t, t.TempDir())
	output, code := runLifecyclePTYSteps(t, project, env, []lifecyclePTYStep{
		{wait: "상태 새로 고침", send: ""},
		{wait: "[↑↓] 이동", send: menuDown(t, "up")},
		{wait: "▸ 스택 시작", send: "\r"},
		{wait: "서비스 선택", send: "\r"},
		{wait: "작업 실행 확인", send: "\r"},
		{wait: "스택 시작.", send: ""},
	})
	if code != 0 {
		t.Fatalf("TUI exit = %d\n%s", code, output)
	}
	for _, want := range []string{"스택 시작.", "자동 배정", "hello-world"} {
		if !screenContains(output, want) {
			t.Fatalf("result missing %q:\n%s", want, output)
		}
	}
	if countCalls(fake.calls(t), " up --detach") != 1 {
		t.Fatalf("TUI up calls = %v", fake.calls(t))
	}
}

func TestLifecycleHonorsConfiguredProjectName(t *testing.T) {
	fake := lifecycleFake(t)
	fake.writeConfig(t, `{"name":"declared-name","services":{"web":{}}}`)
	dirA := projectNamed(t, "hello-world")
	dirB := projectNamed(t, "hello-world")
	stateDir := t.TempDir()
	env := fake.env(t, stateDir)

	first := runProcess(t, dirA, env, strings.NewReader(""), "up", "--path", dirA, "--json")
	if first.code != 0 {
		t.Fatalf("configured name up exit=%d stderr=%q", first.code, first.stderr)
	}
	report := parseLifecycle(t, first.stdout).Data.Lifecycle
	if report.ProjectName != "declared-name" || report.ProjectNameSource != "configured" {
		t.Fatalf("report = %+v", report)
	}
	if line := findCall(t, fake.calls(t), " up --detach"); !strings.Contains(line, "-p declared-name ") {
		t.Fatalf("up line = %q", line)
	}
	// A second folder declaring the same name must stop, not rename it.
	second := runProcess(t, dirB, env, strings.NewReader(""), "up", "--path", dirB, "--json")
	if second.code != 2 {
		t.Fatalf("second configured name exit=%d, want 2", second.code)
	}
	if cats := lifecycleCategories(parseLifecycle(t, second.stdout)); len(cats) != 1 || cats[0] != core.CategoryProjectNameConflict {
		t.Fatalf("diagnostics = %+v", parseLifecycle(t, second.stdout).Diagnostics)
	}
	if countCalls(fake.calls(t), " up --detach") != 1 {
		t.Fatalf("conflict still ran up: %v", fake.calls(t))
	}
}

func TestLifecycleDownRejectsServiceSelection(t *testing.T) {
	fake := lifecycleFake(t)
	project := projectNamed(t, "hello-world")
	res := runProcess(t, project, fake.env(t, t.TempDir()), strings.NewReader(""), "down", "--path", project, "-s", "web")
	if res.code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr=%q)", res.code, res.stderr)
	}
	if !strings.Contains(res.stderr, "s") && !strings.Contains(res.stderr, "service") {
		t.Fatalf("stderr = %q", res.stderr)
	}
	if countCalls(fake.calls(t), " down") != 0 {
		t.Fatalf("down ran with an invalid selection: %v", fake.calls(t))
	}
}

func TestLifecycleHelpExplainsEffectsInKorean(t *testing.T) {
	fake := lifecycleFake(t)
	env := fake.env(t, t.TempDir())
	cases := map[string][]string{
		"up":      {"스택 컨테이너 시작", "--service", "중단"},
		"stop":    {"중지", "dcraft up", "데이터"},
		"down":    {"데이터 볼륨은 삭제하지 않", "프로젝트 전체"},
		"restart": {"새 설정이나 새 이미지를 적용하지 않음", "dcraft up"},
	}
	for command, wants := range cases {
		res := runProcess(t, t.TempDir(), env, strings.NewReader(""), command, "--help")
		if res.code != 0 {
			t.Fatalf("%s --help exit=%d", command, res.code)
		}
		for _, want := range wants {
			if !strings.Contains(res.stdout, want) {
				t.Fatalf("%s help missing %q:\n%s", command, want, res.stdout)
			}
		}
	}
}

// stateTree records every file and its content hash under root. It is used to
// prove that read-only commands do not create or change per-computer state.
func stateTree(t *testing.T, root string) map[string]string {
	t.Helper()
	tree := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			tree[rel+"/"] = "dir"
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		tree[rel] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("walk state dir %q: %v", root, err)
	}
	return tree
}

func equalStateTrees(before, after map[string]string) bool {
	if len(before) != len(after) {
		return false
	}
	for key, value := range before {
		if after[key] != value {
			return false
		}
	}
	return true
}

func TestStatusDoesNotCreateStateInAFreshDirectory(t *testing.T) {
	fake := lifecycleFake(t)
	project := projectNamed(t, "hello-world")
	stateDir := filepath.Join(t.TempDir(), "fresh-state")
	env := fake.env(t, stateDir)

	for i := 0; i < 2; i++ {
		res := runProcess(t, project, env, strings.NewReader(""), "status", "--path", project, "--json")
		if res.code != 0 || res.stderr != "" {
			t.Fatalf("status %d exit=%d stderr=%q", i, res.code, res.stderr)
		}
		parsed := parseStatus(t, res.stdout)
		if parsed.Target.ProjectName != "" {
			t.Fatalf("status invented a project name without state: %q", parsed.Target.ProjectName)
		}
		if parsed.Data.Stack.ProjectName != "hello-world" {
			t.Fatalf("payload project name = %q", parsed.Data.Stack.ProjectName)
		}
		if got := len(parsed.Stages); got != 3 {
			t.Fatalf("stages = %+v", parsed.Stages)
		}
	}
	if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
		t.Fatalf("status created state in a fresh DCRAFT_STATE_DIR: %v", err)
	}
}

func TestStatusUsesTheAllocatedNameAndWritesNothing(t *testing.T) {
	fake := lifecycleFake(t)
	dirA := projectNamed(t, "hello-world")
	dirB := projectNamed(t, "hello-world")
	stateDir := t.TempDir()
	env := fake.env(t, stateDir)

	upA := runProcess(t, dirA, env, strings.NewReader(""), "up", "--path", dirA, "--json")
	if upA.code != 0 {
		t.Fatalf("up A exit=%d stderr=%q", upA.code, upA.stderr)
	}
	if got := parseLifecycle(t, upA.stdout).Data.Lifecycle.ProjectName; got != "hello-world" {
		t.Fatalf("A name = %q", got)
	}
	upB := runProcess(t, dirB, env, strings.NewReader(""), "up", "--path", dirB, "--json")
	if upB.code != 0 {
		t.Fatalf("up B exit=%d stderr=%q", upB.code, upB.stderr)
	}
	if got := parseLifecycle(t, upB.stdout).Data.Lifecycle.ProjectName; got != "hello-world-1" {
		t.Fatalf("B name = %q", got)
	}

	before := stateTree(t, stateDir)
	statusB := runProcess(t, dirB, env, strings.NewReader(""), "status", "--path", dirB, "--json")
	if statusB.code != 0 || statusB.stderr != "" {
		t.Fatalf("status B exit=%d stderr=%q", statusB.code, statusB.stderr)
	}
	parsedB := parseStatus(t, statusB.stdout)
	if parsedB.Target.ProjectName != "hello-world-1" {
		t.Fatalf("target project name = %q, want hello-world-1", parsedB.Target.ProjectName)
	}
	if parsedB.Data.Stack.ProjectName != "hello-world-1" {
		t.Fatalf("payload project name = %q, want hello-world-1", parsedB.Data.Stack.ProjectName)
	}
	if parsedB.Data.Stack.Summary.Running != 1 {
		t.Fatalf("stack summary = %+v", parsedB.Data.Stack.Summary)
	}
	if len(parsedB.Stages) != 3 {
		t.Fatalf("stages = %+v", parsedB.Stages)
	}
	if !equalStateTrees(before, stateTree(t, stateDir)) {
		t.Fatal("status changed per-computer state")
	}

	// A declared name wins over the recorded allocation.
	fake.writeConfig(t, `{"name":"declared-name","services":{"web":{}}}`)
	before = stateTree(t, stateDir)
	declared := runProcess(t, dirB, env, strings.NewReader(""), "status", "--path", dirB, "--json")
	if declared.code != 0 || declared.stderr != "" {
		t.Fatalf("declared status exit=%d stderr=%q", declared.code, declared.stderr)
	}
	declaredParsed := parseStatus(t, declared.stdout)
	if declaredParsed.Target.ProjectName != "declared-name" || declaredParsed.Data.Stack.ProjectName != "declared-name" {
		t.Fatalf("declared status = %+v / %q", declaredParsed.Target, declaredParsed.Data.Stack.ProjectName)
	}
	if !equalStateTrees(before, stateTree(t, stateDir)) {
		t.Fatal("declared status changed per-computer state")
	}

	// An explicit -p wins over both the allocation and Compose's default.
	fake.writeConfig(t, lifecycleConfig)
	if got := parseLifecycleStatus(t, dirB, env, "explicit-name"); got.Target.ProjectName != "explicit-name" || got.Data.Stack.ProjectName != "explicit-name" {
		t.Fatalf("explicit status = %+v / %q", got.Target, got.Data.Stack.ProjectName)
	}

	// The first folder still identifies its own allocated name.
	statusA := runProcess(t, dirA, env, strings.NewReader(""), "status", "--path", dirA, "--json")
	if statusA.code != 0 {
		t.Fatalf("status A exit=%d stderr=%q", statusA.code, statusA.stderr)
	}
	parsedA := parseStatus(t, statusA.stdout)
	if parsedA.Target.ProjectName != "hello-world" || parsedA.Data.Stack.ProjectName != "hello-world" {
		t.Fatalf("A status = %+v / %q", parsedA.Target, parsedA.Data.Stack.ProjectName)
	}
}

func parseLifecycleStatus(t *testing.T, dir string, env []string, explicit string) statusJSON {
	t.Helper()
	res := runProcess(t, dir, env, strings.NewReader(""), "status", "--path", dir, "-p", explicit, "--json")
	if res.code != 0 || res.stderr != "" {
		t.Fatalf("explicit status exit=%d stderr=%q", res.code, res.stderr)
	}
	return parseStatus(t, res.stdout)
}
