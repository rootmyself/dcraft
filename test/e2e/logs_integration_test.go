package e2e

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rootmyself/dcraft/internal/core"
)

const logsExecCompose = `services:
  web:
    image: busybox:${DCRAFT_TEST_TAG:-1.36}
    command: ["sh", "-c", "i=0; while :; do i=$((i+1)); echo line-$i; sleep 1; done"]
  job:
    image: busybox:${DCRAFT_TEST_TAG:-1.36}
    command: ["sh", "-c", "echo job-done; sleep 600"]
`

func waitUntil(t *testing.T, timeout time.Duration, what string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !fn() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func composePS(t *testing.T, env []string, project, file, service string) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "compose", "-p", project, "-f", file, "ps", "-q", service)
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("compose ps %s: %v", service, err)
	}
	var ids []string
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			ids = append(ids, line)
		}
	}
	return ids
}

func TestIntegrationLogsExecScaledService(t *testing.T) {
	env := requireIntegration(t)
	name := "dcraft-test-logs-" + randomHex(t, 6)
	dir := t.TempDir()
	composePath := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(composePath, []byte(logsExecCompose), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "docker", "compose", "-p", name, "-f", composePath, "down", "-v", "--remove-orphans")
		cmd.Dir = dir
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Logf("cleanup %s: %v\n%s", name, err, out)
		}
	})

	upCtx, upCancel := context.WithTimeout(context.Background(), 180*time.Second)
	up := exec.CommandContext(upCtx, "docker", "compose", "-p", name, "-f", composePath, "up", "-d", "--scale", "web=2")
	up.Dir = dir
	up.Env = env
	if out, err := up.CombinedOutput(); err != nil {
		upCancel()
		t.Fatalf("start scaled stack: %v\n%s", err, out)
	}
	upCancel()
	waitUntil(t, 60*time.Second, "two web containers", func() bool {
		return len(composePS(t, env, name, composePath, "web")) == 2
	})

	dcraftEnv := buildEnv(map[string]string{"DCRAFT_STATE_DIR": t.TempDir()})
	run := func(args ...string) processResult {
		t.Helper()
		full := append([]string{"--path", dir, "-p", name}, args...)
		return runProcess(t, dir, dcraftEnv, strings.NewReader(""), full...)
	}

	// status shows two distinct instances of the scaled service.
	status := run("status", "--json")
	if status.code != 0 {
		t.Fatalf("status exit=%d stderr=%q", status.code, status.stderr)
	}
	web := findService(t, parseStatus(t, status.stdout).Data.Stack, "web")
	if len(web.Instances) != 2 || web.Instances[0].Name == web.Instances[1].Name {
		t.Fatalf("scaled instances = %+v", web.Instances)
	}
	instanceNames := []string{web.Instances[0].Name, web.Instances[1].Name}

	// logs reads the service without following.
	plain := run("logs", "-s", "web", "--no-follow", "--tail", "20")
	if plain.code != 0 {
		t.Fatalf("logs exit=%d stderr=%q", plain.code, plain.stderr)
	}
	if !strings.Contains(plain.stdout, "line-") {
		t.Fatalf("logs output missing service lines:\n%s", plain.stdout)
	}

	// exec runs the exact argv in instance 0 and 1; a shell would have split
	// the literal into two commands.
	literal := run("exec", "-s", "web", "-i", "0", "--", "printf", "%s\n", "a && b")
	if literal.code != 0 {
		t.Fatalf("exec literal exit=%d stderr=%q", literal.code, literal.stderr)
	}
	if !strings.Contains(literal.stdout, "a && b") {
		t.Fatalf("exec did not pass the literal argument through: %q", literal.stdout)
	}
	hostJSON := func(index string) string {
		res := run("exec", "-s", "web", "-i", index, "--json", "--", "hostname")
		if res.code != 0 {
			t.Fatalf("hostname exec exit=%d stderr=%q", res.code, res.stderr)
		}
		return strings.TrimSpace(parseLive(t, res.stdout).Data.Exec.Stdout)
	}
	host0 := hostJSON("0")
	host1 := hostJSON("1")
	if host0 == "" || host1 == "" || host0 == host1 {
		t.Fatalf("instances are not distinct: %q %q", host0, host1)
	}
	for i, host := range []string{host0, host1} {
		id := strings.TrimSpace(runShell(t, "docker", "inspect", "-f", "{{.Id}}", instanceNames[i]))
		if !strings.HasPrefix(id, host) {
			t.Fatalf("index %d reached %s, want %s (%s)", i, host, instanceNames[i], id)
		}
	}

	// A remote non-zero exit is a dcraft failure, and the tool's code stays
	// distinguishable in JSON.
	nonZero := run("exec", "-s", "web", "-i", "0", "--json", "--", "sh", "-c", "echo out; exit 7")
	if nonZero.code != 1 {
		t.Fatalf("non-zero exec exit=%d, want 1", nonZero.code)
	}
	parsedNonZero := parseLive(t, nonZero.stdout)
	assertStageStatus(t, parsedNonZero.Stages, "attach", core.StageFailed)
	var exitDiag *core.Diagnostic
	for i := range parsedNonZero.Diagnostics {
		if parsedNonZero.Diagnostics[i].Category == core.CategoryExecFailed {
			exitDiag = &parsedNonZero.Diagnostics[i]
		}
	}
	if exitDiag == nil || exitDiag.ExitCode == nil || *exitDiag.ExitCode != 7 {
		t.Fatalf("non-zero diagnostics = %+v", parsedNonZero.Diagnostics)
	}

	// A command that is not in the image fails with an explanation and never
	// falls back to a shell.
	missing := run("exec", "-s", "web", "-i", "0", "--json", "--", "definitely-not-in-the-image")
	if missing.code != 1 {
		t.Fatalf("missing command exit=%d, want 1", missing.code)
	}
	parsedMissing := parseLive(t, missing.stdout)
	assertStageStatus(t, parsedMissing.Stages, "attach", core.StageFailed)
	if !strings.Contains(parsedMissing.Diagnostics[0].Message, "명령이 없습니다") {
		t.Fatalf("missing command message = %q", parsedMissing.Diagnostics[0].Message)
	}

	// A stopped service fails before attaching.
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 60*time.Second)
	stopJob := exec.CommandContext(stopCtx, "docker", "compose", "-p", name, "-f", composePath, "stop", "job")
	stopJob.Dir = dir
	stopJob.Env = env
	if out, err := stopJob.CombinedOutput(); err != nil {
		stopCancel()
		t.Fatalf("stop job: %v\n%s", err, out)
	}
	stopCancel()
	stopped := run("exec", "-s", "job", "--json", "--", "date")
	if stopped.code != 1 {
		t.Fatalf("stopped exec exit=%d, want 1", stopped.code)
	}
	parsedStopped := parseLive(t, stopped.stdout)
	if !strings.Contains(parsedStopped.Diagnostics[0].Message, "실행 중이 아닙니다") {
		t.Fatalf("stopped message = %q", parsedStopped.Diagnostics[0].Message)
	}

	// Following streams a newly written line; ctrl+c detaches without
	// stopping anything.
	cmd := exec.Command(binaryPath, "logs", "--tail", "0", "-s", "web", "-i", "0", "--path", dir, "-p", name)
	cmd.Dir = dir
	cmd.Env = dcraftEnv
	var followOut, followErr strings.Builder
	cmd.Stdout = &followOut
	cmd.Stderr = &followErr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	followDeadline := time.Now().Add(30 * time.Second)
	for !strings.Contains(followOut.String(), "line-") {
		if time.Now().After(followDeadline) {
			_ = cmd.Process.Kill()
			t.Fatalf("follow streamed nothing:\n%s\n%s", followOut.String(), followErr.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("interrupt follow: %v", err)
	}
	waitErr := cmd.Wait()
	followCode := 0
	if waitErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(waitErr, &exitErr) {
			t.Fatalf("follow wait: %v", waitErr)
		}
		followCode = exitErr.ExitCode()
	}
	if followCode != 130 {
		t.Fatalf("follow cancel exit=%d, want 130\n%s", followCode, followErr.String())
	}
	if !strings.Contains(followErr.String(), "계속 실행") {
		t.Fatalf("follow cancel guidance = %q", followErr.String())
	}
	if ids := composePS(t, env, name, composePath, "web"); len(ids) != 2 || !containerRunning(t, ids[0]) || !containerRunning(t, ids[1]) {
		t.Fatalf("canceling logs changed the service: %v", ids)
	}

	// Quitting the TUI logs screen returns to the menu and leaves both
	// instances running.
	ptyEnv := append([]string{}, dcraftEnv...)
	s := startPTY(t, dir, ptyEnv, "--path", dir, "-p", name)
	s.waitFor("스택 상태 (status)", 30*time.Second)
	s.pressMenuDown(menuDownPresses(t, "logs"))
	s.waitFor("line-", 30*time.Second)
	s.press("q")
	s.waitFor("마지막 실시간 출력", 30*time.Second)
	s.press("q")
	if code := s.wait(); code != 0 {
		t.Fatalf("TUI exit=%d\n%s", code, s.out.String())
	}
	s.assertRestored()
	if ids := composePS(t, env, name, composePath, "web"); len(ids) != 2 || !containerRunning(t, ids[0]) || !containerRunning(t, ids[1]) {
		t.Fatalf("TUI logs exit changed the service: %v", ids)
	}
}

func runShell(t *testing.T, name string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		t.Fatalf("%s %v: %v", name, args, err)
	}
	return string(out)
}

func assertStageStatus(t *testing.T, stages []core.Stage, name string, want core.StageStatus) {
	t.Helper()
	for _, st := range stages {
		if st.Name == name {
			if st.Status != want {
				t.Fatalf("stage %s = %s, want %s", name, st.Status, want)
			}
			return
		}
	}
	t.Fatalf("stage %q missing: %+v", name, stages)
}
