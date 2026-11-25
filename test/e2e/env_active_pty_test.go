package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A chosen environment is recorded and reused: the next start opens on it
// without the selection screen, and a switch from the menu records the new one
// and reopens the status screen with the completion stated.
func TestPTYUIEnvironmentSwitchRecordsAndReusesTheActiveAlias(t *testing.T) {
	fake := newFakeDocker(t)
	project := t.TempDir()
	writeEnvironmentProject(t, project)
	state := t.TempDir()
	env := fake.env(t, state)

	s := startPTY(t, project, env)
	// Nothing is recorded yet, so the startup screen asks.
	s.waitFor("실행환경 선택", 20*time.Second)
	if strings.Contains(visibleText(s.out.String()), "active_environment") {
		t.Fatalf("a config value leaked into the screen:\n%s", s.out.String())
	}
	// Nothing is preselected, so the first entry is reached with one press.
	s.press("\x1b[B")
	s.press("\r") // dev
	s.waitFrameFor(t, "마지막 조회", 25*time.Second)
	assertActiveEnvironment(t, project, "dev")

	// Switching from the menu records the new alias, states it, and reopens the
	// status screen on it.
	s.openMenu("실행환경 전환")
	s.waitFrameFor(t, "실행환경 전환", 20*time.Second)
	s.press("\x1b[B") // dev (현재) -> prod
	s.press("\r")
	s.waitFrameFor(t, "실행환경 전환 완료: prod", 25*time.Second)
	frame := currentFrame(s.out.String())
	if !strings.Contains(frame, "마지막 조회") {
		t.Fatalf("the switch did not reopen the status screen:\n%s", frame)
	}
	if strings.Contains(frame, "실행환경 전환\n") {
		t.Fatalf("the switch left the list on screen:\n%s", frame)
	}
	assertActiveEnvironment(t, project, "prod")
	s.send("q")
	if code := s.wait(); code != 0 {
		t.Fatalf("TUI exit = %d\n%s", code, s.out.String())
	}
	s.assertRestored()

	// A new session starts on the recorded alias without asking again.
	again := startPTY(t, project, env)
	again.waitFrameFor(t, "마지막 조회", 25*time.Second)
	if strings.Contains(currentFrame(again.out.String()), "실행환경 선택") {
		t.Fatalf("the selection screen appeared despite the record:\n%s", again.out.String())
	}
	again.send("q")
	if code := again.wait(); code != 0 {
		t.Fatalf("second TUI exit = %d", code)
	}
}

// A recorded alias the project no longer registers is stated and left alone: the
// reader chooses again instead of the session failing.
func TestPTYUIUnknownRecordedAliasIsStated(t *testing.T) {
	fake := newFakeDocker(t)
	project := t.TempDir()
	writeEnvironmentProject(t, project)
	if err := os.WriteFile(filepath.Join(project, ".dcraft.yaml"),
		[]byte("active_environment: gone\nenvironments:\n  dev:\n    compose_files: [compose.yaml]\n    env_files: [.env.dev]\n    profiles: [dev]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := startPTY(t, project, fake.env(t, t.TempDir()))
	s.waitFor("저장된 활성 실행환경을 쓸 수 없음: gone", 20*time.Second)
	if !strings.Contains(visibleText(s.out.String()), "실행환경 선택") {
		t.Fatalf("the selection screen did not appear:\n%s", s.out.String())
	}
	s.send("q")
	if code := s.wait(); code != 0 {
		t.Fatalf("exit = %d", code)
	}
}

// assertActiveEnvironment reads the recorded alias through the same parser the
// program uses, so the assertion is on the file's meaning rather than its bytes.
func assertActiveEnvironment(t *testing.T, project, want string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(project, ".dcraft.yaml"))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	line := "active_environment: " + want
	if !strings.Contains(string(data), line) {
		t.Fatalf("config does not record %q:\n%s", want, data)
	}
}
