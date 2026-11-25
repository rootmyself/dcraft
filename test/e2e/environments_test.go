package e2e

import (
	"context"
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
)

func writeEnvironmentProject(t *testing.T, dir string) (configPath string, hashes map[string]string) {
	t.Helper()
	writeProject(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "compose.dev.yaml"),
		[]byte("services:\n  debug:\n    image: nginx:alpine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env.dev"), []byte("TAG=dev\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env.prod"), []byte("TAG=prod\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	config := `environments:
  dev:
    compose_files: [compose.yaml, compose.dev.yaml]
    env_files: [.env.dev]
    profiles: [dev]
  prod:
    compose_files: [compose.yaml]
    env_files: [.env.prod]
    profiles: [prod]
`
	configPath = filepath.Join(dir, ".dcraft.yaml")
	if err := os.WriteFile(configPath, []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	hashes = map[string]string{
		filepath.Join(dir, "compose.yaml"):     sha256File(t, filepath.Join(dir, "compose.yaml")),
		filepath.Join(dir, "compose.dev.yaml"): sha256File(t, filepath.Join(dir, "compose.dev.yaml")),
		configPath:                             sha256File(t, configPath),
	}
	return configPath, hashes
}

func assertFilesUnchanged(t *testing.T, hashes map[string]string) {
	t.Helper()
	for path, want := range hashes {
		if got := sha256File(t, path); got != want {
			t.Fatalf("%s was modified", path)
		}
	}
}

func TestEnvironmentMissingSelectionFailsWithExit2AndKoreanGuidance(t *testing.T) {
	fake := newFakeDocker(t)
	project := t.TempDir()
	writeEnvironmentProject(t, project)
	state := t.TempDir()

	res := runProcess(t, project, fake.env(t, state), strings.NewReader(""), "status")
	if res.code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr=%q)", res.code, res.stderr)
	}
	for _, want := range []string{"실행환경", "dev", "prod", "--env", "다음 행동"} {
		if !strings.Contains(res.stderr, want) {
			t.Fatalf("stderr missing %q:\n%s", want, res.stderr)
		}
	}
	if strings.Contains(res.stderr, "?") || strings.Contains(res.stdout, "\x1b[") {
		t.Fatalf("non-interactive run prompted or opened a TUI: stdout=%q stderr=%q", res.stdout, res.stderr)
	}
	if calls := fake.calls(t); len(calls) != 0 {
		t.Fatalf("docker was called without a selection: %v", calls)
	}
}

func TestEnvironmentMissingSelectionInJSONIsMachineReadable(t *testing.T) {
	fake := newFakeDocker(t)
	project := t.TempDir()
	writeEnvironmentProject(t, project)

	res := runProcess(t, project, fake.env(t, t.TempDir()), strings.NewReader(""), "status", "--json")
	if res.code != 2 {
		t.Fatalf("exit = %d, want 2", res.code)
	}
	if res.stderr != "" {
		t.Fatalf("JSON mode wrote stderr: %q", res.stderr)
	}
	parsed := parseStatus(t, res.stdout)
	if parsed.Outcome != core.OutcomeFailure {
		t.Fatalf("outcome = %q", parsed.Outcome)
	}
	if parsed.Target.Environment != "" {
		t.Fatalf("target.environment was chosen implicitly: %q", parsed.Target.Environment)
	}
	if !hasCategory(parsed, core.CategoryEnvironmentRequired) {
		t.Fatalf("diagnostics = %+v", parsed.Diagnostics)
	}
	if calls := fake.calls(t); len(calls) != 0 {
		t.Fatalf("docker was called without a selection: %v", calls)
	}
}

func TestEnvironmentSelectionWorksInStatusJSON(t *testing.T) {
	fake := newFakeDocker(t)
	project := t.TempDir()
	_, hashes := writeEnvironmentProject(t, project)
	state := t.TempDir()

	res := runProcess(t, project, fake.env(t, state), strings.NewReader(""), "status", "--env", "dev", "--json")
	if res.code != 0 {
		t.Fatalf("exit = %d, stderr=%q", res.code, res.stderr)
	}
	parsed := parseStatus(t, res.stdout)
	if parsed.Outcome != core.OutcomeSuccess {
		t.Fatalf("outcome = %q diagnostics=%+v", parsed.Outcome, parsed.Diagnostics)
	}
	if parsed.Target.Environment != "dev" {
		t.Fatalf("target.environment = %q, want dev", parsed.Target.Environment)
	}
	wantFiles := []string{filepath.Join(project, "compose.yaml"), filepath.Join(project, "compose.dev.yaml")}
	if len(parsed.Target.Files) != len(wantFiles) || !samePath(t, parsed.Target.Files[0], wantFiles[0]) || !samePath(t, parsed.Target.Files[1], wantFiles[1]) {
		t.Fatalf("target.files = %v, want %v", parsed.Target.Files, wantFiles)
	}
	if len(parsed.Target.EnvFiles) != 1 || !samePath(t, parsed.Target.EnvFiles[0], filepath.Join(project, ".env.dev")) {
		t.Fatalf("target.env_files = %v", parsed.Target.EnvFiles)
	}
	if strings.Join(parsed.Target.Profiles, ",") != "dev" {
		t.Fatalf("target.profiles = %v", parsed.Target.Profiles)
	}

	var configCall, psCall string
	for _, line := range fake.calls(t) {
		if strings.Contains(line, " config ") {
			configCall = line
		}
		if strings.Contains(line, " ps ") {
			psCall = line
		}
	}
	for name, call := range map[string]string{"config": configCall, "ps": psCall} {
		if call == "" {
			t.Fatalf("no compose %s call logged: %v", name, fake.calls(t))
		}
		for _, want := range []string{"-f " + parsed.Target.Files[0], "-f " + parsed.Target.Files[1], "--env-file " + parsed.Target.EnvFiles[0], "--profile dev"} {
			if !strings.Contains(call, want) {
				t.Fatalf("%s call %q missing %q", name, call, want)
			}
		}
	}
	assertFilesUnchanged(t, hashes)
}

func TestEnvironmentUnknownAndConflictingInputsAreUsageErrors(t *testing.T) {
	fake := newFakeDocker(t)
	project := t.TempDir()
	writeEnvironmentProject(t, project)

	unknown := runProcess(t, project, fake.env(t, t.TempDir()), strings.NewReader(""), "status", "--env", "staging")
	if unknown.code != 2 {
		t.Fatalf("unknown exit = %d, want 2 (stderr=%q)", unknown.code, unknown.stderr)
	}
	if !strings.Contains(unknown.stderr, "staging") || !strings.Contains(unknown.stderr, "dev") {
		t.Fatalf("unknown environment guidance = %q", unknown.stderr)
	}

	conflict := runProcess(t, project, fake.env(t, t.TempDir()), strings.NewReader(""), "status", "--env", "dev", "-f", "compose.yaml")
	if conflict.code != 2 {
		t.Fatalf("conflict exit = %d, want 2 (stderr=%q)", conflict.code, conflict.stderr)
	}
	if !strings.Contains(conflict.stderr, "--env") || !strings.Contains(conflict.stderr, "-f") {
		t.Fatalf("conflict guidance = %q", conflict.stderr)
	}
	if calls := fake.calls(t); len(calls) != 0 {
		t.Fatalf("docker was called for usage errors: %v", calls)
	}
}

func TestEnvironmentAliasWithoutConfigKeepsComposeDefaults(t *testing.T) {
	fake := newFakeDocker(t)
	project := t.TempDir()
	writeProject(t, project)
	res := runProcess(t, project, fake.env(t, t.TempDir()), strings.NewReader(""), "status", "--json")
	if res.code != 0 {
		t.Fatalf("exit = %d, stderr=%q", res.code, res.stderr)
	}
	parsed := parseStatus(t, res.stdout)
	if parsed.Target.Environment != "" {
		t.Fatalf("environment = %q, want empty", parsed.Target.Environment)
	}
	if len(parsed.Target.Files) != 1 || !samePath(t, parsed.Target.Files[0], filepath.Join(project, "compose.yaml")) {
		t.Fatalf("files = %v", parsed.Target.Files)
	}
}

type ptyStep struct {
	waitText string
	send     string
	// walk is a menu entry label to move the cursor onto before send. The
	// arrow count depends on how many presses the opening run swallowed, so a
	// step that walks reads the drawn frame instead of counting keys.
	walk string
}

func runPTYSteps(t *testing.T, dir string, env []string, steps []ptyStep) (string, int) {
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
	go func() { _, _ = io.Copy(out, f) }()

	waitFor := func(text string) {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for !screenContains(out.String(), text) {
			if time.Now().After(deadline) {
				t.Fatalf("TUI did not render %q within timeout; output:\n%s", text, out.String())
			}
			time.Sleep(25 * time.Millisecond)
		}
	}
	for _, step := range steps {
		if step.waitText != "" {
			waitFor(step.waitText)
		}
		if step.walk != "" {
			walkMenuCursor(t, f, out, step.walk, waitFor)
		}
		if step.send != "" {
			if _, err := f.Write([]byte(step.send)); err != nil {
				t.Fatalf("send %q: %v", step.send, err)
			}
		}
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
		t.Fatalf("TUI did not exit; output:\n%s", out.String())
		return "", -1
	}
}

// Picking the session's environment records it as the active alias, so the next
// start opens on it. Nothing else about the project changes: no stack call runs
// and the aliases themselves are untouched.
func TestTUIEnvironmentSelectionRecordsTheActiveAlias(t *testing.T) {
	fake := newFakeDocker(t)
	project := t.TempDir()
	_, hashes := writeEnvironmentProject(t, project)
	state := t.TempDir()
	env := fake.env(t, state)

	output, code := runPTYSteps(t, project, env, []ptyStep{
		{waitText: "실행환경 선택"},
		{waitText: "dev", send: "\x1b[B"},
		{waitText: "> dev", send: "\r"},
		{waitText: "fake-project"},
		{send: "q"},
	})
	if code != 0 {
		t.Fatalf("TUI exit = %d\n%s", code, output)
	}
	for _, want := range []string{"실행환경 선택", "dev", "prod", "> dev", "fake-project"} {
		if !screenContains(output, want) {
			t.Fatalf("TUI output missing %q:\n%s", want, output)
		}
	}
	assertNoMutatingCalls(t, fake.calls(t))
	var configCall string
	for _, line := range fake.calls(t) {
		if strings.Contains(line, " config ") {
			configCall = line
		}
	}
	realProject, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(configCall, "--env-file "+filepath.Join(realProject, ".env.dev")) || !strings.Contains(configCall, "--profile dev") {
		t.Fatalf("TUI selection did not reach Compose with the alias inputs: %q", configCall)
	}
	// Only the record changed: the aliases are the same and the Compose files
	// were not touched.
	config, err := os.ReadFile(filepath.Join(project, ".dcraft.yaml"))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(config), "active_environment: dev") {
		t.Fatalf("the chosen alias was not recorded:\n%s", config)
	}
	for path, want := range hashes {
		if filepath.Base(path) == ".dcraft.yaml" {
			continue
		}
		got := sha256File(t, path)
		if got != want {
			t.Fatalf("%s changed", path)
		}
	}
}

func TestTUIQuitFromEnvironmentSelectionDoesNotRunAnything(t *testing.T) {
	fake := newFakeDocker(t)
	project := t.TempDir()
	_, hashes := writeEnvironmentProject(t, project)
	output, code := runPTYSteps(t, project, fake.env(t, t.TempDir()), []ptyStep{
		{waitText: "실행환경 선택"},
		{send: "q"},
	})
	if code != 0 {
		t.Fatalf("TUI exit = %d\n%s", code, output)
	}
	if calls := fake.calls(t); len(calls) != 0 {
		t.Fatalf("quitting from selection contacted Docker: %v", calls)
	}
	assertFilesUnchanged(t, hashes)
}

// walkMenuCursor presses the down arrow until the drawn frame marks the wanted
// menu entry, then stops. A press that arrives while the opening status run
// still owns the keyboard scrolls the result instead of moving the cursor, so a
// fixed count can land elsewhere; the drawn frame is the only proof.
func walkMenuCursor(t *testing.T, f *os.File, out *syncBuffer, label string, waitFor func(string)) {
	t.Helper()
	marker := "▸ " + label
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if screenContains(currentFrame(out.String()), marker) {
			time.Sleep(50 * time.Millisecond)
			return
		}
		if _, err := f.Write([]byte("\x1b[B")); err != nil {
			t.Fatalf("send down: %v", err)
		}
		time.Sleep(40 * time.Millisecond)
	}
	_ = waitFor
	t.Fatalf("menu never marked %q:\n%s", label, out.String())
}
