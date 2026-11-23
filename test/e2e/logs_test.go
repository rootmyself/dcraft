package e2e

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rootmyself/dcraft/internal/core"
)

// fakeLiveDocker is a scripted `docker` used only by the logs/exec process
// tests. It records every call and emulates exactly the preflight, config, ps,
// logs and exec invocations those features need.
type fakeLiveDocker struct {
	dir     string
	logPath string
}

func newFakeLiveDocker(t *testing.T) *fakeLiveDocker {
	t.Helper()
	dir := t.TempDir()
	f := &fakeLiveDocker{dir: dir, logPath: filepath.Join(dir, "calls.log")}
	config := `{"name":"live-project","services":{"web":{"image":"busybox:1.36"},"db":{"image":"busybox:1.36"},"job":{"image":"busybox:1.36"}}}`
	ps := strings.Join([]string{
		`{"Name":"live-project-web-1","Service":"web","State":"running","Health":"","Image":"busybox:1.36","ExitCode":0}`,
		`{"Name":"live-project-web-2","Service":"web","State":"running","Health":"","Image":"busybox:1.36","ExitCode":0}`,
		`{"Name":"live-project-db-1","Service":"db","State":"running","Health":"","Image":"busybox:1.36","ExitCode":0}`,
		`{"Name":"live-project-job-1","Service":"job","State":"exited","Health":"","Image":"busybox:1.36","ExitCode":1}`,
	}, "\n")
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ps.ndjson"), []byte(ps), 0o644); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
LOG="${DOCKER_FAKE_LOG:?}"
printf '%s\n' "$*" >> "$LOG"
case "$*" in
  "--version") echo "Docker version 29.10.0, build fake"; exit 0 ;;
  "compose version --short") echo "5.7.0"; exit 0 ;;
  "context ls --format json") printf '%s\n' '{"Current":true,"Description":"Docker Desktop","DockerEndpoint":"unix:///Users/me/.docker/run/docker.sock","Error":"","Name":"desktop-linux"}'; exit 0 ;;
  "info --format {{.ServerVersion}}") echo "29.9.3"; exit 0 ;;
esac
case "$*" in
  *" -f - config --format json") echo '{"services":{"probe":{"image":"scratch"}}}'; exit 0 ;;
  *" -f - ps --all --format json") exit 0 ;;
  *" config --format json") cat "$DOCKER_FAKE_CONFIG"; exit 0 ;;
  *" ps --all --format json") cat "$DOCKER_FAKE_PS"; exit 0 ;;
esac
sub=""
for a in "$@"; do
  case "$a" in
    logs|exec) sub="$a"; break ;;
  esac
done
case "$sub" in
  logs)
    echo "live-project-web-1  | boot line"
    case "$*" in
      *"--follow"*)
        if [ -n "$DOCKER_FAKE_LOG_FOLLOW_LINE" ]; then
          sleep 0.3
          echo "live-project-web-1  | $DOCKER_FAKE_LOG_FOLLOW_LINE"
        fi
        while :; do sleep 1; done ;;
    esac
    exit 0 ;;
  exec)
    if [ -n "$DOCKER_FAKE_EXEC_BLOCK" ]; then sleep 30; fi
    if [ -n "$DOCKER_FAKE_EXEC_ECHO" ]; then
      echo "ready"
      IFS= read -r line
      echo "got:$line"
      if [ -n "$DOCKER_FAKE_EXEC_EXIT" ]; then exit "$DOCKER_FAKE_EXEC_EXIT"; fi
      exit 0
    fi
    if [ -n "$DOCKER_FAKE_EXEC_STDERR" ]; then echo "$DOCKER_FAKE_EXEC_STDERR" >&2; fi
    if [ -n "$DOCKER_FAKE_EXEC_STDOUT" ]; then echo "$DOCKER_FAKE_EXEC_STDOUT"; fi
    if [ -n "$DOCKER_FAKE_EXEC_EXIT" ]; then exit "$DOCKER_FAKE_EXEC_EXIT"; fi
    exit 0 ;;
esac
echo "fake docker: unexpected invocation: $*" >&2
exit 9
`
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fakeLiveDocker) env(t *testing.T, stateDir string, extra map[string]string) []string {
	t.Helper()
	values := map[string]string{
		"PATH":               f.dir + ":" + os.Getenv("PATH"),
		"DOCKER_FAKE_LOG":    f.logPath,
		"DOCKER_FAKE_CONFIG": filepath.Join(f.dir, "config.json"),
		"DOCKER_FAKE_PS":     filepath.Join(f.dir, "ps.ndjson"),
		"DCRAFT_STATE_DIR":   stateDir,
	}
	for k, v := range extra {
		values[k] = v
	}
	return buildEnv(values)
}

func (f *fakeLiveDocker) calls(t *testing.T) []string {
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

func (f *fakeLiveDocker) assertNoSubcommand(t *testing.T, name string) {
	t.Helper()
	for _, line := range f.calls(t) {
		if strings.HasSuffix(line, " "+name) || strings.Contains(line, " "+name+" ") {
			t.Fatalf("docker %s was invoked: %s", name, line)
		}
	}
}

func (f *fakeLiveDocker) findCall(t *testing.T, contains string) string {
	t.Helper()
	for _, line := range f.calls(t) {
		if strings.Contains(line, contains) {
			return line
		}
	}
	t.Fatalf("no docker call contains %q: %v", contains, f.calls(t))
	return ""
}

func liveProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("services:\n  web:\n    image: busybox:1.36\n  db:\n    image: busybox:1.36\n  job:\n    image: busybox:1.36\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

type liveResult struct {
	SchemaVersion int               `json:"schema_version"`
	Action        string            `json:"action"`
	Outcome       core.Outcome      `json:"outcome"`
	Target        core.Target       `json:"target"`
	Stages        []core.Stage      `json:"stages"`
	Diagnostics   []core.Diagnostic `json:"diagnostics"`
	Data          struct {
		Logs json.RawMessage `json:"logs"`
		Exec struct {
			Service  string   `json:"service"`
			Instance string   `json:"instance"`
			Index    int      `json:"index"`
			Command  []string `json:"command"`
			TTY      bool     `json:"tty"`
			ExitCode int      `json:"exit_code"`
			Stdout   string   `json:"stdout"`
			Stderr   string   `json:"stderr"`
		} `json:"exec"`
	} `json:"data"`
}

func parseLive(t *testing.T, out string) liveResult {
	t.Helper()
	var res liveResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("parse JSON: %v\n%s", err, out)
	}
	return res
}

func liveCategories(res liveResult) []core.Category {
	var out []core.Category
	for _, d := range res.Diagnostics {
		out = append(out, d.Category)
	}
	return out
}

func TestLogsRefusesJSONWithoutContactingDocker(t *testing.T) {
	fake := newFakeLiveDocker(t)
	project := liveProject(t)
	res := runProcess(t, project, fake.env(t, t.TempDir(), nil), strings.NewReader(""), "logs", "--json")
	if res.code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr=%q)", res.code, res.stderr)
	}
	for _, want := range []string{"실시간 로그", "--json", "ctrl+c"} {
		if !strings.Contains(res.stderr, want) {
			t.Fatalf("stderr missing %q:\n%s", want, res.stderr)
		}
	}
	if calls := fake.calls(t); len(calls) != 0 {
		t.Fatalf("docker was called for a usage error: %v", calls)
	}
}

func TestLogsNoFollowStreamsExistingOutputAndMapsFlags(t *testing.T) {
	fake := newFakeLiveDocker(t)
	project := liveProject(t)
	res := runProcess(t, project, fake.env(t, t.TempDir(), nil), strings.NewReader(""),
		"logs", "--no-follow", "--tail", "5", "--since", "10m", "--timestamps", "-s", "web")
	if res.code != 0 {
		t.Fatalf("exit = %d, stderr=%q", res.code, res.stderr)
	}
	if !strings.Contains(res.stdout, "boot line") {
		t.Fatalf("live output missing:\n%s", res.stdout)
	}
	call := fake.findCall(t, " logs ")
	for _, want := range []string{"--no-color", "--tail 5", "--since 10m", "--timestamps", "logs"} {
		if !strings.Contains(call, want) {
			t.Fatalf("logs call missing %q: %s", want, call)
		}
	}
	if strings.Contains(call, "--follow") {
		t.Fatalf("--no-follow still followed: %s", call)
	}
	for _, line := range fake.calls(t) {
		for _, forbidden := range []string{" stop", " down", " kill", " rm"} {
			if strings.Contains(line, forbidden) {
				t.Fatalf("logs changed services: %q", line)
			}
		}
	}
}

func TestLogsIndexMapsToComposeOneBasedIndex(t *testing.T) {
	fake := newFakeLiveDocker(t)
	project := liveProject(t)
	res := runProcess(t, project, fake.env(t, t.TempDir(), nil), strings.NewReader(""),
		"logs", "--no-follow", "-s", "web", "-i", "1")
	if res.code != 0 {
		t.Fatalf("exit = %d, stderr=%q", res.code, res.stderr)
	}
	call := fake.findCall(t, " logs ")
	if !strings.Contains(call, "--index 2") || !strings.HasSuffix(call, " web") {
		t.Fatalf("logs call = %s", call)
	}
}

func TestLogsIndexWithoutASingleServiceFailsWithExitTwo(t *testing.T) {
	fake := newFakeLiveDocker(t)
	project := liveProject(t)
	res := runProcess(t, project, fake.env(t, t.TempDir(), nil), strings.NewReader(""), "logs", "-i", "0")
	if res.code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr=%q stdout=%q)", res.code, res.stderr, res.stdout)
	}
	for _, want := range []string{"서비스를 하나만", "-s"} {
		if !strings.Contains(res.stderr, want) {
			t.Fatalf("stderr missing %q:\n%s", want, res.stderr)
		}
	}
	fake.assertNoSubcommand(t, "logs")
}

func TestLogsFollowCancellationKeepsServicesAndExits130(t *testing.T) {
	fake := newFakeLiveDocker(t)
	project := liveProject(t)
	env := fake.env(t, t.TempDir(), map[string]string{"DOCKER_FAKE_LOG_FOLLOW_LINE": "followed line"})
	cmd := exec.Command(binaryPath, "logs", "-s", "web")
	cmd.Dir = project
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(stdout.String(), "followed line") {
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			t.Fatalf("follow did not stream the new line; output=%q stderr=%q", stdout.String(), stderr.String())
		}
		time.Sleep(25 * time.Millisecond)
	}
	// ctrl+c in a terminal is SIGINT; cancellation must not stop services.
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
	if !strings.Contains(stderr.String(), "계속 실행") {
		t.Fatalf("canceled output does not explain the detached stream:\n%s", stderr.String())
	}
	for _, line := range fake.calls(t) {
		for _, forbidden := range []string{" stop", " down", " kill", " rm"} {
			if strings.Contains(line, forbidden) {
				t.Fatalf("canceling logs changed services: %q", line)
			}
		}
	}
}

func TestExecRequiresServiceAndCommand(t *testing.T) {
	fake := newFakeLiveDocker(t)
	project := liveProject(t)
	cases := [][]string{
		{"exec", "--", "date"},
		{"exec", "-s", "db"},
	}
	for _, args := range cases {
		res := runProcess(t, project, fake.env(t, t.TempDir(), nil), strings.NewReader(""), args...)
		if res.code != 2 {
			t.Fatalf("%v exit = %d, want 2 (stderr=%q)", args, res.code, res.stderr)
		}
		if !strings.Contains(res.stderr, "다음 행동") && !strings.Contains(res.stderr, "명령") && !strings.Contains(res.stderr, "서비스") {
			t.Fatalf("%v stderr = %q", args, res.stderr)
		}
	}
	fake.assertNoSubcommand(t, "exec")
}

func TestExecPassesExactArgvAndPropagatesNonZeroExitAsFailure(t *testing.T) {
	fake := newFakeLiveDocker(t)
	project := liveProject(t)
	env := fake.env(t, t.TempDir(), map[string]string{
		"DOCKER_FAKE_EXEC_EXIT":   "7",
		"DOCKER_FAKE_EXEC_STDERR": "remote failed",
		"DOCKER_FAKE_EXEC_STDOUT": "ignored on failure",
	})
	res := runProcess(t, project, env, strings.NewReader(""),
		"exec", "-s", "db", "--json", "--", "printf", "%s", "hello world", "&&", "echo", "$HOME")
	if res.code != 1 {
		t.Fatalf("dcraft exit = %d, want 1 (stderr=%q)", res.code, res.stderr)
	}
	if res.stderr != "" {
		t.Fatalf("JSON mode wrote stderr: %q", res.stderr)
	}
	parsed := parseLive(t, res.stdout)
	if parsed.Outcome != core.OutcomeFailure {
		t.Fatalf("outcome = %q", parsed.Outcome)
	}
	var execDiag *core.Diagnostic
	for i := range parsed.Diagnostics {
		if parsed.Diagnostics[i].Category == core.CategoryExecFailed {
			execDiag = &parsed.Diagnostics[i]
		}
	}
	if execDiag == nil {
		t.Fatalf("diagnostics = %+v", parsed.Diagnostics)
	}
	if execDiag.ExitCode == nil || *execDiag.ExitCode != 7 {
		t.Fatalf("remote exit code = %v", execDiag.ExitCode)
	}
	if !strings.Contains(execDiag.Detail, "remote failed") {
		t.Fatalf("detail = %q", execDiag.Detail)
	}
	call := fake.findCall(t, " exec ")
	want := "exec -T db printf %s hello world && echo $HOME"
	if !strings.HasSuffix(call, want) {
		t.Fatalf("exec call = %s\nwant suffix %q", call, want)
	}
	if strings.Contains(call, "--index") {
		t.Fatalf("single-instance exec passed an index: %s", call)
	}
	for _, forbidden := range []string{"sh -c", " -lc "} {
		if strings.Contains(call, forbidden) {
			t.Fatalf("exec inserted a shell: %s", call)
		}
	}
}

func TestExecJSONCapturesOutputWithoutLiveStdout(t *testing.T) {
	fake := newFakeLiveDocker(t)
	project := liveProject(t)
	env := fake.env(t, t.TempDir(), map[string]string{
		"DOCKER_FAKE_EXEC_STDOUT": "captured line",
		"DOCKER_FAKE_EXEC_STDERR": "captured err",
	})
	res := runProcess(t, project, env, strings.NewReader(""), "exec", "-s", "db", "--json", "--", "date")
	if res.code != 0 {
		t.Fatalf("exit = %d, stderr=%q", res.code, res.stderr)
	}
	if res.stderr != "" {
		t.Fatalf("JSON mode wrote stderr: %q", res.stderr)
	}
	parsed := parseLive(t, res.stdout)
	if parsed.Outcome != core.OutcomeSuccess || parsed.Action != "exec" {
		t.Fatalf("result = %+v", parsed)
	}
	if parsed.Data.Exec.Stdout != "captured line\n" || parsed.Data.Exec.Stderr != "captured err\n" {
		t.Fatalf("payload = %+v", parsed.Data.Exec)
	}
	if parsed.Data.Exec.TTY || parsed.Data.Exec.Service != "db" || parsed.Data.Exec.Instance != "live-project-db-1" {
		t.Fatalf("payload = %+v", parsed.Data.Exec)
	}
	if strings.Contains(res.stdout, "\x1b[") {
		t.Fatal("JSON output contains terminal escapes")
	}
}

func TestExecScaledServiceRequiresAnInstance(t *testing.T) {
	fake := newFakeLiveDocker(t)
	project := liveProject(t)
	res := runProcess(t, project, fake.env(t, t.TempDir(), nil), strings.NewReader(""), "exec", "-s", "web", "--", "date")
	if res.code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr=%q)", res.code, res.stderr)
	}
	for _, want := range []string{"live-project-web-1", "live-project-web-2", "-i"} {
		if !strings.Contains(res.stderr, want) {
			t.Fatalf("stderr missing %q:\n%s", want, res.stderr)
		}
	}
	fake.assertNoSubcommand(t, "exec")
}

func TestExecStoppedServiceFailsWithExitOne(t *testing.T) {
	fake := newFakeLiveDocker(t)
	project := liveProject(t)
	res := runProcess(t, project, fake.env(t, t.TempDir(), nil), strings.NewReader(""), "exec", "-s", "job", "--", "date")
	if res.code != 1 {
		t.Fatalf("exit = %d, want 1 (stderr=%q)", res.code, res.stderr)
	}
	if !strings.Contains(res.stderr, "실행 중이 아님") {
		t.Fatalf("stderr = %q", res.stderr)
	}
}
