package e2e

import (
	"fmt"
	"strings"
	"testing"
)

// The CLI never supplies the opt-in progress writer: an up run still returns
// only its JSON result and the Compose invocation keeps the capture path.
func TestCLIUpKeepsCapturedOutputWithoutProgressFlags(t *testing.T) {
	fake := lifecycleFake(t)
	project := projectNamed(t, "cli-up-log")
	env := fake.envExtra(t, t.TempDir(), map[string]string{
		"DOCKER_FAKE_UP_LINES": "dcraft-progress: building web",
	})
	res := runProcess(t, project, env, strings.NewReader(""), "up", "--path", project, "--json")
	if res.code != 0 || res.stderr != "" {
		t.Fatalf("up exit=%d stderr=%q stdout=%q", res.code, res.stderr, res.stdout)
	}
	parsed := parseLifecycle(t, res.stdout)
	if parsed.Outcome != "success" {
		t.Fatalf("outcome = %q", parsed.Outcome)
	}
	if strings.Contains(res.stdout, "dcraft-progress: building web") {
		t.Fatalf("CLI output mixed in progress text:\n%s", res.stdout)
	}
	upLine := findCall(t, fake.calls(t), " up --detach")
	for _, forbidden := range []string{"--progress", "--ansi"} {
		if strings.Contains(upLine, forbidden) {
			t.Fatalf("CLI up used a TUI-only flag %q: %s", forbidden, upLine)
		}
	}
}

// A real pseudo-terminal exercises the split running screen: the compose
// progress lines appear inside the TUI, the overflow footer advertises the
// log keys, and the result replaces the pane when the run completes.
func TestPTYTUIUpShowsComposeProgressInTheLogPane(t *testing.T) {
	fake := lifecycleFake(t)
	project := projectNamed(t, "tui-up-log")
	lines := make([]string, 0, 60)
	for i := 0; i < 60; i++ {
		lines = append(lines, fmt.Sprintf("dcraft-progress line-%02d", i))
	}
	env := fake.envExtra(t, t.TempDir(), map[string]string{
		"DOCKER_FAKE_UP_LINES": strings.Join(lines, "\n"),
		"DOCKER_FAKE_SLEEP_UP": "1.5",
	})
	output, code := runLifecyclePTYSteps(t, project, env, []lifecyclePTYStep{
		{wait: "마지막 조회", walk: "스택 시작", send: "\r"},
		{wait: "서비스 선택", send: "\r"},
		{wait: "작업 실행 확인", send: "\r"},
		{wait: "dcraft-progress line-59", send: ""},
		{wait: "PgUp/PgDn", send: ""},
		{wait: "현재 Compose 설정을 적용해", send: ""},
	})
	if code != 0 {
		t.Fatalf("TUI exit = %d\n%s", code, output)
	}
	for _, want := range []string{"dcraft-progress line-59", "PgUp/PgDn", "현재 Compose 설정을 적용해"} {
		if !screenContains(output, want) {
			t.Fatalf("TUI output missing %q:\n%s", want, visibleText(output))
		}
	}
	upLine := findCall(t, fake.calls(t), " up --detach")
	for _, want := range []string{"--progress plain", "--ansi never"} {
		if !strings.Contains(upLine, want) {
			t.Fatalf("up call missing %q: %s", want, upLine)
		}
	}
}
