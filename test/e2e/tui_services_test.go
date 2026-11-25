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
		{wait: "상태 새로 고침", send: ""},
		{wait: "마지막 조회", walk: "스택 중지", send: "\r"},
		{wait: "서비스 선택", send: "down"}, // 전체 → api (Compose order)
		{wait: "▸[ ] api", send: "down"},
		{wait: "▸[ ] web", send: " "},
		{wait: "[x] web", send: "\r"},
		{wait: "작업 실행 확인", send: ""},
		{wait: "범위:", send: "\r"},
		{wait: "스택 중지.", send: ""},
	})
	// The chosen scope is echoed on the confirmation panel.
	if row, ok := screenRowWith(output, "범위:"); !ok || !strings.Contains(row, "web") {
		t.Fatalf("confirmation did not echo the chosen scope:\n%s", output)
	}
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
		{wait: "상태 새로 고침", send: ""},
		{wait: "마지막 조회", walk: "스택 중지", send: "\r"},
		{wait: "서비스 선택", send: "\r"},
		{wait: "작업 실행 확인", send: ""},
		{wait: "범위:", send: "\r"},
		{wait: "스택 중지.", send: ""},
	})
	if code != 0 {
		t.Fatalf("TUI exit = %d\n%s", code, output)
	}
	if row, ok := screenRowWith(output, "범위:"); !ok || !strings.Contains(row, "스택 전체") {
		t.Fatalf("confirmation did not echo the whole-stack scope:\n%s", output)
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
		{wait: "상태 새로 고침", send: ""},
		{wait: "마지막 조회", walk: "스택 중지", send: "\r"},
		{wait: "서비스 선택", send: "down"},
		{wait: "▸[ ] api", send: "\x1b"}, // esc
		{wait: "아무것도 실행하지 않았음", send: ""},
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
	s.waitFor("마지막 조회", 15*time.Second)
	s.openMenuFeature(t, "logs")
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
