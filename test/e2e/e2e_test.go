package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/stack"
)

var binaryPath string

func TestMain(m *testing.M) {
	moduleRoot, err := filepath.Abs("../..")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	dir, err := os.MkdirTemp("", "dcraft-e2e-bin-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	binaryPath = filepath.Join(dir, "dcraft")
	build := exec.Command("go", "build", "-o", binaryPath, "./cmd/dcraft")
	build.Dir = moduleRoot
	out, err := build.CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "build dcraft: %v\n%s", err, out)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

type fakeDocker struct {
	dir       string
	logPath   string
	config    string
	ps        string
	consumers string
	configOut string
}

func newFakeDocker(t *testing.T) *fakeDocker {
	t.Helper()
	dir := t.TempDir()
	f := &fakeDocker{
		dir:       dir,
		logPath:   filepath.Join(dir, "calls.log"),
		config:    filepath.Join(dir, "config.json"),
		ps:        filepath.Join(dir, "ps.ndjson"),
		consumers: filepath.Join(dir, "consumers.tsv"),
		configOut: `{"name":"fake-project","services":{"web":{"image":"nginx:alpine","healthcheck":{"test":["CMD","true"]},"ports":[{"mode":"ingress","target":80,"published":"8080","protocol":"tcp"}]},"api":{"image":"api:local","build":{"context":"."}}}}`,
	}
	if err := os.WriteFile(f.config, []byte(f.configOut), 0o644); err != nil {
		t.Fatal(err)
	}
	ps := strings.Join([]string{
		`{"Name":"fake-project-web-1","Service":"web","State":"running","Health":"healthy","Image":"nginx:alpine","ExitCode":0,"Publishers":[{"URL":"0.0.0.0","TargetPort":80,"PublishedPort":8080,"Protocol":"tcp"}]}`,
		`{"Name":"fake-project-api-1","Service":"api","State":"running","Health":"","Image":"api:local","ExitCode":0}`,
	}, "\n")
	if err := os.WriteFile(f.ps, []byte(ps), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.consumers, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
LOG="${DOCKER_FAKE_LOG:?}"
printf '%s\n' "$*" >> "$LOG"
case "$*" in
  "--version") echo "Docker version 29.10.0, build fake"; exit 0 ;;
  "compose version --short") echo "5.7.0"; exit 0 ;;
  "context ls --format json") printf '%s\n' '{"Current":true,"Description":"Docker Desktop","DockerEndpoint":"unix:///Users/me/.docker/run/docker.sock","Error":"","Name":"desktop-linux"}'; exit 0 ;;
  "info --format {{.ServerVersion}}") if [ -n "$DOCKER_FAKE_SLEEP" ]; then exec sleep "$DOCKER_FAKE_SLEEP"; fi; echo "29.9.3"; exit 0 ;;
esac
case "$*" in
  *" -f - --project-directory "*)
    dir=""; prev=""
    for a in "$@"; do
      if [ "$prev" = "--project-directory" ]; then dir="$a"; fi
      prev="$a"
    done
    name=$(basename "$dir" | tr '[:upper:]' '[:lower:]' | tr -cd 'a-z0-9_-')
    echo "{\"name\":\"$name\"}"
    exit 0 ;;
  "compose ls -a --format json")
    if [ -n "$DOCKER_FAKE_PROJECTS" ] && [ -f "$DOCKER_FAKE_PROJECTS" ]; then
      cat "$DOCKER_FAKE_PROJECTS"
    else
      echo "[]"
    fi
    exit 0 ;;
  *" config --format json") cat "$DOCKER_FAKE_CONFIG"; exit 0 ;;
  *" ps --all --format json") cat "$DOCKER_FAKE_PS"; exit 0 ;;
  *"ps -a --filter volume="*)
    if [ -n "$DOCKER_FAKE_CONSUMERS" ] && [ -f "$DOCKER_FAKE_CONSUMERS" ]; then
      cat "$DOCKER_FAKE_CONSUMERS"
    fi
    exit 0 ;;
  "run "*)
    hostdir=""
    prev=""
    for a in "$@"; do
      case "$a" in
        *:/backup) hostdir="${a%:/backup}" ;;
      esac
      prev="$a"
    done
    if [ -z "$hostdir" ]; then
      echo "fake docker run: no /backup mount" >&2
      exit 9
    fi
    if [ -n "$DOCKER_FAKE_SLEEP_RUN" ]; then exec sleep "$DOCKER_FAKE_SLEEP_RUN"; fi
    if [ -n "$DOCKER_FAKE_ARCHIVE_FAIL" ]; then
      printf 'partial' > "$hostdir/data.tar.gz"
      echo "tar: can't open './secret': $DOCKER_FAKE_ARCHIVE_FAIL" >&2
      exit 1
    fi
    tmp=$(mktemp -d)
    echo "hello from volume" > "$tmp/file.txt"
    mkdir -p "$tmp/nested"
    echo "nested" > "$tmp/nested/dotless.txt"
    echo "dot" > "$tmp/.hidden"
    ln -s file.txt "$tmp/link"
    ( cd "$tmp" && tar -czf "$hostdir/data.tar.gz" . )
    rm -rf "$tmp"
    exit 0 ;;
esac
sub=""
for a in "$@"; do
  case "$a" in
    up|stop|down|restart|start) sub="$a"; break ;;
  esac
done
case "$sub" in
  up)
    if [ -n "$DOCKER_FAKE_SLEEP_UP" ]; then exec sleep "$DOCKER_FAKE_SLEEP_UP"; fi
    echo "Container fake-web-1 Started"; exit 0 ;;
  stop) echo "Container fake-web-1 Stopped"; exit 0 ;;
  down) echo "Container fake-web-1 Removed"; exit 0 ;;
  restart) echo "Container fake-web-1 Restarted"; exit 0 ;;
  start) echo "Container fake-web-1 Started"; exit 0 ;;
esac
echo "fake docker: unexpected invocation: $*" >&2
exit 9
`
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fakeDocker) env(t *testing.T, stateDir string) []string {
	t.Helper()
	return f.envExtra(t, stateDir, nil)
}

// envExtra adds test-specific environment values (for example a project list
// file for `docker compose ls`).
func (f *fakeDocker) envExtra(t *testing.T, stateDir string, extra map[string]string) []string {
	t.Helper()
	values := map[string]string{
		"PATH":                  f.dir + ":" + os.Getenv("PATH"),
		"DOCKER_FAKE_LOG":       f.logPath,
		"DOCKER_FAKE_CONFIG":    f.config,
		"DOCKER_FAKE_PS":        f.ps,
		"DOCKER_FAKE_CONSUMERS": f.consumers,
		"DCRAFT_STATE_DIR":      stateDir,
	}
	for k, v := range extra {
		values[k] = v
	}
	return buildEnv(values)
}

func (f *fakeDocker) writeConfig(t *testing.T, text string) {
	t.Helper()
	if err := os.WriteFile(f.config, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f *fakeDocker) writePS(t *testing.T, text string) {
	t.Helper()
	if err := os.WriteFile(f.ps, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f *fakeDocker) writeConsumers(t *testing.T, text string) {
	t.Helper()
	if err := os.WriteFile(f.consumers, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f *fakeDocker) calls(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(f.logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func buildEnv(overrides map[string]string) []string {
	var out []string
	for _, kv := range os.Environ() {
		key := strings.SplitN(kv, "=", 2)[0]
		if _, replaced := overrides[key]; replaced {
			continue
		}
		if key == "DOCKER_HOST" || key == "DOCKER_CONTEXT" || key == "TERM" {
			continue
		}
		out = append(out, kv)
	}
	for k, v := range overrides {
		out = append(out, k+"="+v)
	}
	return out
}

type processResult struct {
	stdout string
	stderr string
	code   int
}

func runProcess(t *testing.T, dir string, env []string, stdin io.Reader, args ...string) processResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binaryPath, args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdin = stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		} else {
			t.Fatalf("run %v: %v", args, err)
		}
	}
	return processResult{stdout: stdout.String(), stderr: stderr.String(), code: code}
}

type statusJSON struct {
	SchemaVersion int               `json:"schema_version"`
	OperationID   string            `json:"operation_id"`
	Action        string            `json:"action"`
	Outcome       core.Outcome      `json:"outcome"`
	Target        core.Target       `json:"target"`
	Tools         core.ToolInfo     `json:"tools"`
	Stages        []core.Stage      `json:"stages"`
	Diagnostics   []core.Diagnostic `json:"diagnostics"`
	Data          struct {
		Stack stack.Snapshot `json:"stack"`
	} `json:"data"`
}

func parseStatus(t *testing.T, out string) statusJSON {
	t.Helper()
	var res statusJSON
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("parse status JSON: %v\n%s", err, out)
	}
	return res
}

func hasCategory(res statusJSON, cat core.Category) bool {
	for _, d := range res.Diagnostics {
		if d.Category == cat {
			return true
		}
	}
	return false
}

func mutatingComposeWords() map[string]bool {
	return map[string]bool{
		"up": true, "down": true, "stop": true, "start": true, "restart": true,
		"kill": true, "rm": true, "create": true, "run": true, "exec": true,
		"pause": true, "unpause": true, "pull": true, "push": true, "build": true,
		"publish": true, "scale": true, "commit": true, "cp": true, "export": true,
	}
}

func assertNoMutatingCalls(t *testing.T, calls []string) {
	t.Helper()
	mutating := mutatingComposeWords()
	for _, line := range calls {
		for _, token := range strings.Fields(line) {
			if mutating[token] {
				t.Fatalf("mutating Docker command invoked: %q", line)
			}
		}
	}
}

func writeProject(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(path, []byte("services:\n  web:\n    image: nginx:alpine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestStatusJSONFromProcess(t *testing.T) {
	fake := newFakeDocker(t)
	project := t.TempDir()
	composePath := writeProject(t, project)
	state := t.TempDir()

	res := runProcess(t, project, fake.env(t, state), strings.NewReader(""), "status", "--path", project, "--json")
	if res.code != 0 {
		t.Fatalf("exit = %d, stderr=%q", res.code, res.stderr)
	}
	if res.stderr != "" {
		t.Fatalf("JSON mode wrote stderr: %q", res.stderr)
	}
	parsed := parseStatus(t, res.stdout)
	if parsed.Outcome != core.OutcomeSuccess || parsed.Action != "status" {
		t.Fatalf("result = %+v", parsed)
	}
	if parsed.Target.ProjectDir != project {
		t.Fatalf("target dir = %q, want %q", parsed.Target.ProjectDir, project)
	}
	if len(parsed.Target.Files) != 1 || parsed.Target.Files[0] != composePath {
		t.Fatalf("target files = %v", parsed.Target.Files)
	}
	if parsed.Tools.ComposeVersion != "5.7.0" || parsed.Tools.DockerVersion != "29.10.0" {
		t.Fatalf("tools = %+v", parsed.Tools)
	}
	if len(parsed.Stages) != 3 {
		t.Fatalf("stages = %+v", parsed.Stages)
	}
	for i, want := range []string{"resolve_target", "preflight", "inspect_stack"} {
		if parsed.Stages[i].Name != want || parsed.Stages[i].Status != core.StageSucceeded {
			t.Fatalf("stage %d = %+v", i, parsed.Stages[i])
		}
	}
	if parsed.Data.Stack.ProjectName != "fake-project" {
		t.Fatalf("project name = %q", parsed.Data.Stack.ProjectName)
	}
	if len(parsed.Data.Stack.Services) != 2 {
		t.Fatalf("services = %+v", parsed.Data.Stack.Services)
	}
	var web, api stack.Service
	for _, s := range parsed.Data.Stack.Services {
		switch s.Name {
		case "web":
			web = s
		case "api":
			api = s
		}
	}
	if !web.AppVerified || web.Health != stack.HealthHealthy {
		t.Fatalf("web = %+v", web)
	}
	if api.AppVerified || api.Health != stack.HealthNone {
		t.Fatalf("api = %+v", api)
	}
	if len(web.Connections) == 0 || web.Connections[0].PublishedPort != 8080 {
		t.Fatalf("web connections = %+v", web.Connections)
	}
	assertNoMutatingCalls(t, fake.calls(t))
}

func TestStatusHumanOutputAndNoServiceChanges(t *testing.T) {
	fake := newFakeDocker(t)
	project := t.TempDir()
	writeProject(t, project)
	state := t.TempDir()

	res := runProcess(t, project, fake.env(t, state), strings.NewReader(""), "status")
	if res.code != 0 {
		t.Fatalf("exit = %d, stderr=%q", res.code, res.stderr)
	}
	for _, want := range []string{"fake-project", "web", "api", "8080", "앱 확인 안 됨", "healthy", "dcraft status", "dcraft secrets", "자동 제거되지 않습니다"} {
		if !strings.Contains(res.stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, res.stdout)
		}
	}
	assertNoMutatingCalls(t, fake.calls(t))
}

func TestStatusNoConfigurationFailsWithoutDocker(t *testing.T) {
	fake := newFakeDocker(t)
	empty := t.TempDir()
	res := runProcess(t, empty, fake.env(t, t.TempDir()), strings.NewReader(""), "status", "--json")
	if res.code != 1 {
		t.Fatalf("exit = %d, want 1 (stderr=%q)", res.code, res.stderr)
	}
	parsed := parseStatus(t, res.stdout)
	if parsed.Outcome != core.OutcomeFailure {
		t.Fatalf("outcome = %q", parsed.Outcome)
	}
	if !hasCategory(parsed, core.CategoryNoConfiguration) {
		t.Fatalf("diagnostics = %+v", parsed.Diagnostics)
	}
	if len(parsed.Diagnostics[0].NextActions) == 0 {
		t.Fatal("no Korean next actions")
	}
	if calls := fake.calls(t); len(calls) != 0 {
		t.Fatalf("docker called despite missing configuration: %v", calls)
	}
}

func TestStatusDockerMissing(t *testing.T) {
	project := t.TempDir()
	writeProject(t, project)
	env := buildEnv(map[string]string{
		"PATH":             "/usr/bin:/bin",
		"DCRAFT_STATE_DIR": t.TempDir(),
	})
	res := runProcess(t, project, env, strings.NewReader(""), "status", "--json")
	if res.code != 1 {
		t.Fatalf("exit = %d, want 1", res.code)
	}
	parsed := parseStatus(t, res.stdout)
	if !hasCategory(parsed, core.CategoryDockerMissing) {
		t.Fatalf("diagnostics = %+v", parsed.Diagnostics)
	}
}

func TestStatusRemoteEngineIsRefusedWithoutContactingIt(t *testing.T) {
	fake := newFakeDocker(t)
	project := t.TempDir()
	writeProject(t, project)
	env := buildEnv(map[string]string{
		"PATH":               fake.dir + ":" + os.Getenv("PATH"),
		"DOCKER_FAKE_LOG":    fake.logPath,
		"DOCKER_FAKE_CONFIG": fake.config,
		"DOCKER_FAKE_PS":     fake.ps,
		"DCRAFT_STATE_DIR":   t.TempDir(),
		"DOCKER_HOST":        "tcp://127.0.0.1:2375",
	})
	res := runProcess(t, project, env, strings.NewReader(""), "status", "--json")
	if res.code != 1 {
		t.Fatalf("exit = %d, want 1", res.code)
	}
	parsed := parseStatus(t, res.stdout)
	if !hasCategory(parsed, core.CategoryRemoteEngine) {
		t.Fatalf("diagnostics = %+v", parsed.Diagnostics)
	}
	for _, line := range fake.calls(t) {
		if strings.Contains(line, "info") || strings.Contains(line, " config ") || strings.Contains(line, " ps ") {
			t.Fatalf("remote endpoint was contacted: %q", line)
		}
	}
}

func TestNonInteractiveNoArgsFailsWithoutTUI(t *testing.T) {
	fake := newFakeDocker(t)
	project := t.TempDir()
	writeProject(t, project)
	res := runProcess(t, project, fake.env(t, t.TempDir()), strings.NewReader(""))
	if res.code != 2 {
		t.Fatalf("exit = %d, want 2", res.code)
	}
	if strings.Contains(res.stdout, "\x1b[") {
		t.Fatalf("stdout contains TUI escapes: %q", res.stdout)
	}
	for _, want := range []string{"대화형 터미널이 아니어서", "dcraft status", "--json", "도움말"} {
		if !strings.Contains(res.stderr, want) {
			t.Fatalf("stderr missing %q:\n%s", want, res.stderr)
		}
	}
	if calls := fake.calls(t); len(calls) != 0 {
		t.Fatalf("docker invoked for usage error: %v", calls)
	}
}

func TestHelpIsKorean(t *testing.T) {
	fake := newFakeDocker(t)
	res := runProcess(t, t.TempDir(), fake.env(t, t.TempDir()), strings.NewReader(""), "--help")
	if res.code != 0 {
		t.Fatalf("exit = %d", res.code)
	}
	for _, want := range []string{"사용법:", "사용 가능한 명령:", "status", "도움말:"} {
		if !strings.Contains(res.stdout, want) {
			t.Fatalf("help missing %q:\n%s", want, res.stdout)
		}
	}
}

func TestUnknownCommandFails(t *testing.T) {
	fake := newFakeDocker(t)
	res := runProcess(t, t.TempDir(), fake.env(t, t.TempDir()), strings.NewReader(""), "definitely-not-a-command")
	if res.code != 2 {
		t.Fatalf("exit = %d, want 2", res.code)
	}
	if !strings.Contains(res.stderr, "오류:") {
		t.Fatalf("stderr = %q", res.stderr)
	}
}

func TestJSONWithoutCommandFails(t *testing.T) {
	fake := newFakeDocker(t)
	res := runProcess(t, t.TempDir(), fake.env(t, t.TempDir()), strings.NewReader(""), "--json")
	if res.code != 2 {
		t.Fatalf("exit = %d, want 2", res.code)
	}
	if !strings.Contains(res.stderr, "명령을 지정") {
		t.Fatalf("stderr = %q", res.stderr)
	}
}

func TestSIGINTCancelsReadOnlyOperationWithExit130(t *testing.T) {
	fake := newFakeDocker(t)
	project := t.TempDir()
	writeProject(t, project)
	env := buildEnv(map[string]string{
		"PATH":               fake.dir + ":" + os.Getenv("PATH"),
		"DOCKER_FAKE_LOG":    fake.logPath,
		"DOCKER_FAKE_CONFIG": fake.config,
		"DOCKER_FAKE_PS":     fake.ps,
		"DOCKER_FAKE_SLEEP":  "10",
		"DCRAFT_STATE_DIR":   t.TempDir(),
	})
	cmd := exec.Command(binaryPath, "status", "--json")
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
		t.Fatalf("signal: %v", err)
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
	parsed := parseStatus(t, stdout.String())
	if parsed.Outcome != core.OutcomeCanceled {
		t.Fatalf("outcome = %q", parsed.Outcome)
	}
	if !hasCategory(parsed, core.CategoryCanceled) {
		t.Fatalf("diagnostics = %+v", parsed.Diagnostics)
	}
}

func TestParentComposeFileIsNotDiscovered(t *testing.T) {
	fake := newFakeDocker(t)
	parent := t.TempDir()
	writeProject(t, parent)
	child := filepath.Join(parent, "nested")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}
	res := runProcess(t, child, fake.env(t, t.TempDir()), strings.NewReader(""), "status", "--json")
	if res.code != 1 {
		t.Fatalf("exit = %d, want 1", res.code)
	}
	parsed := parseStatus(t, res.stdout)
	if !hasCategory(parsed, core.CategoryNoConfiguration) {
		t.Fatalf("diagnostics = %+v", parsed.Diagnostics)
	}
	if len(fake.calls(t)) != 0 {
		t.Fatalf("docker called: %v", fake.calls(t))
	}
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func runPTY(t *testing.T, dir string, env []string, waitText string) (string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binaryPath)
	cmd.Dir = dir
	cmd.Env = append(env, "TERM=xterm-256color")
	f, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("start pty: %v", err)
	}
	defer f.Close()
	_ = pty.Setsize(f, &pty.Winsize{Rows: 40, Cols: 120})

	out := &syncBuffer{}
	readDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(out, f)
		close(readDone)
	}()

	deadline := time.Now().Add(15 * time.Second)
	for !strings.Contains(out.String(), waitText) {
		if time.Now().After(deadline) {
			t.Fatalf("TUI did not render %q within timeout; output:\n%s", waitText, out.String())
		}
		time.Sleep(25 * time.Millisecond)
	}
	if _, err := f.Write([]byte("q")); err != nil {
		t.Fatalf("send quit: %v", err)
	}
	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()
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
	case <-time.After(15 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("TUI did not exit after q; output:\n%s", out.String())
		return "", -1
	}
}

func TestTUIEnterAndQuitDoesNotChangeServices(t *testing.T) {
	fake := newFakeDocker(t)
	project := t.TempDir()
	writeProject(t, project)
	state := t.TempDir()

	output, code := runPTY(t, project, fake.env(t, state), "fake-project")
	if code != 0 {
		t.Fatalf("TUI exit = %d\n%s", code, output)
	}
	for _, want := range []string{"dcraft", "스택 상태", "새로고침", "종료"} {
		if !strings.Contains(output, want) {
			t.Fatalf("TUI output missing %q:\n%s", want, output)
		}
	}
	assertNoMutatingCalls(t, fake.calls(t))
}

func TestTUIShowsDiagnosticsAndQuitsInEmptyFolder(t *testing.T) {
	fake := newFakeDocker(t)
	empty := t.TempDir()
	output, code := runPTY(t, empty, fake.env(t, t.TempDir()), "Compose 설정을 찾지 못했습니다")
	if code != 0 {
		t.Fatalf("TUI exit = %d\n%s", code, output)
	}
	for _, want := range []string{"작업 실패", "다음 행동", "q 종료"} {
		if !strings.Contains(output, want) {
			t.Fatalf("failure screen missing %q:\n%s", want, output)
		}
	}
	if calls := fake.calls(t); len(calls) != 0 {
		t.Fatalf("docker called for missing configuration: %v", calls)
	}
}

func TestTUIHasNoUnimplementedActions(t *testing.T) {
	fake := newFakeDocker(t)
	project := t.TempDir()
	writeProject(t, project)
	output, code := runPTY(t, project, fake.env(t, t.TempDir()), "fake-project")
	if code != 0 {
		t.Fatalf("TUI exit = %d\n%s", code, output)
	}
	for _, forbidden := range []string{"dcraft update", "dcraft logs", "dcraft backup"} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("TUI exposes unimplemented action %q:\n%s", forbidden, output)
		}
	}
}

func sha256File(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
