package e2e

import (
	"strings"
	"testing"
	"time"
)

// The service scope screen is generic: it appears for any SupportsServices
// feature that does not collect its own input, and the chosen services travel
// through the same plan/confirm/run path as the CLI's -s/--service.
func TestTUIServiceScopeStopsOnlyTheSelectedService(t *testing.T) {
	fake := lifecycleFake(t)
	project := projectNamed(t, "hello-world")
	env := fake.env(t, t.TempDir())
	output, code := runLifecyclePTYSteps(t, project, env, []lifecyclePTYStep{
		{wait: "상태를 새로 고치려면", send: ""},
		{wait: "[↑↓] 이동", send: menuDown(t, "stop")},
		{wait: "▸ 스택 중지", send: "\r"},
		{wait: "서비스 선택", send: "down"}, // 전체 → api (Compose order)
		{wait: "> [ ] api", send: "down"},
		{wait: "> [ ] web", send: " "},
		{wait: "[x] web", send: "\r"},
		{wait: "다음 작업을 실행할까요?", send: ""},
		{wait: "범위: web", send: "\r"},
		{wait: "스택을 중지했습니다.", send: ""},
	})
	if code != 0 {
		t.Fatalf("TUI exit = %d\n%s", code, output)
	}
	calls := fake.calls(t)
	stopLine := findCall(t, calls, " stop")
	if !strings.HasSuffix(stopLine, " stop web") {
		t.Fatalf("stop did not target only web: %q", stopLine)
	}
	for _, line := range calls {
		if strings.Contains(line, " stop ") && strings.Contains(line, " api") {
			t.Fatalf("unselected service was targeted: %q", line)
		}
	}
}

func TestTUIServiceScopeWholeStackKeepsThePreviousBehavior(t *testing.T) {
	fake := lifecycleFake(t)
	project := projectNamed(t, "hello-world")
	env := fake.env(t, t.TempDir())
	output, code := runLifecyclePTYSteps(t, project, env, []lifecyclePTYStep{
		{wait: "상태를 새로 고치려면", send: ""},
		{wait: "[↑↓] 이동", send: menuDown(t, "stop")},
		{wait: "▸ 스택 중지", send: "\r"},
		{wait: "서비스 선택", send: "\r"},
		{wait: "다음 작업을 실행할까요?", send: ""},
		{wait: "범위: 스택 전체", send: "\r"},
		{wait: "스택을 중지했습니다.", send: ""},
	})
	if code != 0 {
		t.Fatalf("TUI exit = %d\n%s", code, output)
	}
	stopLine := findCall(t, fake.calls(t), " stop")
	if !strings.HasSuffix(stopLine, " stop") {
		t.Fatalf("whole-stack stop carried a service list: %q", stopLine)
	}
}

func TestTUIServiceScopeCancelChangesNothing(t *testing.T) {
	fake := lifecycleFake(t)
	project := projectNamed(t, "hello-world")
	env := fake.env(t, t.TempDir())
	output, code := runLifecyclePTYSteps(t, project, env, []lifecyclePTYStep{
		{wait: "상태를 새로 고치려면", send: ""},
		{wait: "[↑↓] 이동", send: menuDown(t, "stop")},
		{wait: "▸ 스택 중지", send: "\r"},
		{wait: "서비스 선택", send: "down"},
		{wait: "> [ ] api", send: "\x1b"}, // esc
		{wait: "아무것도 실행하지 않았습니다", send: ""},
	})
	if code != 0 {
		t.Fatalf("TUI exit = %d\n%s", code, output)
	}
	assertNoMutatingCalls(t, fake.calls(t))
}

// The logs screen uses the same generic scope selection. Choosing web must
// pass only web to the Compose logs command, and the live screen must not stop
// anything when it detaches.
func TestTUILogsUsesTheSelectedServiceAndKeepsServicesRunning(t *testing.T) {
	fake := newFakeLiveDocker(t)
	project := liveProject(t)
	env := fake.env(t, t.TempDir(), nil)
	s := startPTY(t, project, env)
	s.waitFor("[↑↓] 이동", 15*time.Second)
	s.pressMenuDown(menuDownPresses(t, "logs"))
	s.waitFor("서비스 선택", 15*time.Second)
	// 전체 → db → job → web
	s.press("\x1b[B", "\x1b[B", "\x1b[B")
	s.press(" ")
	s.waitFor("[x] web", 15*time.Second)
	s.press("\r")
	s.waitFor("boot line", 15*time.Second)
	s.send("q")
	s.waitFor("마지막 실시간 출력", 15*time.Second)
	s.send("q")
	code := s.wait()
	if code != 0 {
		t.Fatalf("TUI exit = %d\n%s", code, s.out.String())
	}
	line := fake.findCall(t, " logs")
	if !strings.HasSuffix(line, " web") {
		t.Fatalf("logs did not target only web: %q", line)
	}
	fake.assertNoSubcommand(t, "stop")
	fake.assertNoSubcommand(t, "down")
}
