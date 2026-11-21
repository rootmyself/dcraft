package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const tuiServicesCompose = `services:
  api:
    image: busybox:${DCRAFT_TEST_TAG:-1.36}
    command: ["sh", "-c", "echo api-boot; sleep 600"]
  web:
    image: busybox:${DCRAFT_TEST_TAG:-1.36}
    command: ["sh", "-c", "echo web-boot; sleep 600"]
`

func containerStartedAt(t *testing.T, id string) string {
	t.Helper()
	return dockerMust(t, "inspect", "-f", "{{.State.StartedAt}}", id)
}

// selectWebScope drives the generic scope screen: whole -> api -> web, space,
// enter. It proves the TUI reaches the existing plan/confirm path with only
// the chosen service.
func selectWebScope(s *ptySession) {
	s.t.Helper()
	s.waitFor("서비스 선택", 30*time.Second)
	s.press("\x1b[B", "\x1b[B")
	s.press(" ")
	s.waitFor("[x] web", 15*time.Second)
	s.press("\r")
}

func TestIntegrationTUIServiceScopeSelectsOnlyOneService(t *testing.T) {
	env := requireIntegration(t)
	base := "dcraft-tui-" + randomHex(t, 6)
	dir := filepath.Join(t.TempDir(), base)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	composePath := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(composePath, []byte(tuiServicesCompose), 0o644); err != nil {
		t.Fatal(err)
	}
	dcraftEnv := buildEnv(map[string]string{"DCRAFT_STATE_DIR": t.TempDir()})

	up := runProcess(t, dir, dcraftEnv, strings.NewReader(""), "up", "--json")
	if up.code != 0 {
		t.Fatalf("up exit=%d stderr=%q stdout=%q", up.code, up.stderr, up.stdout)
	}
	projectName := parseLifecycle(t, up.stdout).Data.Lifecycle.ProjectName
	t.Cleanup(func() { cleanupLifecycleCompose(t, env, dir, projectName, composePath, false) })

	apiID := composeInspect(t, env, projectName, composePath, "api")
	webID := composeInspect(t, env, projectName, composePath, "web")
	if apiID == "" || webID == "" {
		t.Fatalf("stack did not start: api=%q web=%q", apiID, webID)
	}
	apiBefore := containerIDCreated(t, apiID)
	webBefore := containerIDCreated(t, webID)
	if !containerRunning(t, apiID) || !containerRunning(t, webID) {
		t.Fatal("both services must be running before the TUI selection")
	}

	// stop: choose web only through the TUI scope screen.
	s := startPTY(t, dir, dcraftEnv)
	s.waitFor("[↑↓] 이동", 30*time.Second)
	s.pressMenuDown(menuDownPresses(t, "stop"))
	selectWebScope(s)
	s.waitFor("다음 작업을 실행할까요?", 15*time.Second)
	s.waitFor("범위:", 15*time.Second)
	if row, ok := screenRowWith(s.out.String(), "범위:"); !ok || !strings.Contains(row, "web") {
		t.Fatalf("confirmation did not echo the chosen scope:\n%s", s.out.String())
	}
	s.press("\r")
	s.waitFor("스택을 중지했습니다.", 60*time.Second)
	s.send("q")
	if code := s.wait(); code != 0 {
		t.Fatalf("stop TUI exit = %d\n%s", code, s.out.String())
	}
	if containerRunning(t, webID) {
		t.Fatal("selected web service is still running after stop")
	}
	if !containerRunning(t, apiID) {
		t.Fatal("stop touched the unselected api service")
	}
	if got := containerIDCreated(t, apiID); got != apiBefore {
		t.Fatalf("unselected api container changed on stop: %s -> %s", apiBefore, got)
	}

	// resume web without recreating it.
	if res := runProcess(t, dir, dcraftEnv, strings.NewReader(""), "up", "-s", "web", "--json"); res.code != 0 {
		t.Fatalf("resume web exit=%d stderr=%q", res.code, res.stderr)
	}
	if got := composeInspect(t, env, projectName, composePath, "web"); got != webID {
		t.Fatalf("resume recreated web: %s -> %s", webID, got)
	}
	apiStarted := containerStartedAt(t, apiID)
	webStarted := containerStartedAt(t, webID)

	// restart: choose web only; api must keep its ID, Created and StartedAt.
	s = startPTY(t, dir, dcraftEnv)
	s.waitFor("[↑↓] 이동", 30*time.Second)
	s.pressMenuDown(menuDownPresses(t, "restart"))
	selectWebScope(s)
	s.waitFor("다음 작업을 실행할까요?", 15*time.Second)
	s.waitFor("범위:", 15*time.Second)
	if row, ok := screenRowWith(s.out.String(), "범위:"); !ok || !strings.Contains(row, "web") {
		t.Fatalf("restart confirmation did not echo the chosen scope:\n%s", s.out.String())
	}
	s.press("\r")
	s.waitFor("스택을 재시작했습니다.", 60*time.Second)
	s.send("q")
	if code := s.wait(); code != 0 {
		t.Fatalf("restart TUI exit = %d\n%s", code, s.out.String())
	}
	if got := composeInspect(t, env, projectName, composePath, "web"); got != webID {
		t.Fatalf("restart recreated web: %s -> %s", webID, got)
	}
	if got := containerIDCreated(t, webID); got != webBefore {
		t.Fatalf("restart recreated the web container: %s -> %s", webBefore, got)
	}
	if got := containerStartedAt(t, webID); got == webStarted {
		t.Fatalf("restart did not restart web (StartedAt still %s)", webStarted)
	}
	if got := containerIDCreated(t, apiID); got != apiBefore {
		t.Fatalf("restart touched api: %s -> %s", apiBefore, got)
	}
	if got := containerStartedAt(t, apiID); got != apiStarted {
		t.Fatalf("restart touched api StartedAt: %s -> %s", apiStarted, got)
	}

	// update: the plan/confirm screen carries the selected web scope, and the
	// unselected api container is not recreated.
	s = startPTY(t, dir, dcraftEnv)
	s.waitFor("[↑↓] 이동", 30*time.Second)
	s.pressMenuDown(menuDownPresses(t, "update"))
	selectWebScope(s)
	s.waitFor("다음 작업을 실행할까요?", 30*time.Second)
	s.waitFor("범위:", 15*time.Second)
	if !strings.Contains(s.out.String(), "서비스: web") {
		t.Fatalf("update plan does not carry the selected service:\n%s", s.out.String())
	}
	s.press("\r")
	s.waitFor("갱신 완료", 120*time.Second)
	if !strings.Contains(s.out.String(), "서비스: web") {
		t.Fatalf("update result does not report the selected service:\n%s", s.out.String())
	}
	s.send("q")
	if code := s.wait(); code != 0 {
		t.Fatalf("update TUI exit = %d\n%s", code, s.out.String())
	}
	if got := composeInspect(t, env, projectName, composePath, "api"); got != apiID {
		t.Fatalf("update recreated the unselected api service: %s -> %s", apiID, got)
	}
	if got := containerIDCreated(t, apiID); got != apiBefore {
		t.Fatalf("update changed api: %s -> %s", apiBefore, got)
	}
	if got := composeInspect(t, env, projectName, composePath, "web"); got == "" {
		t.Fatal("update left the selected web service without a container")
	}
	if !containerRunning(t, apiID) || !containerRunning(t, webID) {
		t.Fatal("update left a service stopped")
	}
}
