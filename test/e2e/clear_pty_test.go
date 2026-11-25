package e2e

import (
	"strings"
	"testing"
	"time"
)

// A shell's `clear` starts from an empty screen: the rows the command removed
// must not stay reachable above the prompt. The child here prints a banner that
// scrolls off the screen, then sends the exact sequence a shell's `clear` sends,
// then a prompt with no newline.
func TestPTYTUIShellClearLeavesNothingBehind(t *testing.T) {
	fake := newFakeLiveDocker(t)
	project := liveProject(t)
	env := fake.env(t, t.TempDir(), map[string]string{"DOCKER_FAKE_EXEC_SHELL": "60"})
	s := startPTY(t, project, env)
	s.waitFor("마지막 조회", 20*time.Second)
	s.openMenuFeature(t, "exec")
	s.waitFrameFor(t, "실행 전 확인", 20*time.Second)

	// The pinned shell is the default: confirm the instance choice, confirm the
	// command choice (the last visible field, so this submits), and attach.
	s.press("\r")
	s.waitFrameFor(t, "명령 선택:", 15*time.Second)
	s.press("\r")
	s.waitFrameFor(t, "probe:/ #", 25*time.Second)

	// The banner scrolled off the screen and the clear removed it: the visible
	// grid is the prompt row only, with no banner line above it.
	frame := currentFrame(s.out.String())
	if strings.Contains(frame, "banner line") {
		t.Fatalf("clear left the removed rows on screen:\n%s", frame)
	}
	// The attach header states the detach key in its fixed line.
	if !screenContains(s.out.String(), "종료: ctrl+]") {
		t.Fatalf("the attach header does not state the detach key:\n%s", s.out.String())
	}
	s.send("\x1d")
	s.waitFor("마지막 실시간 출력", 20*time.Second)
	s.send("q")
	if code := s.wait(); code != 0 {
		t.Fatalf("TUI exit = %d\n%s", code, s.out.String())
	}
	s.assertRestored()
}
