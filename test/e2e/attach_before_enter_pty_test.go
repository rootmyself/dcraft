package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The reported defect: an attached shell showed nothing until Enter, so the
// prompt arrived late and a typed command was invisible. Both halves are asserted
// here with no Enter pressed at all, on a real terminal with the real binary.
//
// The child is a fake `docker exec` that behaves like an interactive shell: it
// writes a prompt with no newline and then reads a line. That is exactly the
// shape the old line-buffered screen could not show.
func TestPTYTUIAttachShowsPromptAndTypingBeforeEnter(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(dir, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "compose.yaml"), []byte("services:\n  probe:\n    image: busybox:1.36\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(dir, "state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(dir, "config.json")
	if err := os.WriteFile(config, []byte(`{"name":"probe","services":{"probe":{"image":"busybox:1.36"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	ps := filepath.Join(dir, "ps.ndjson")
	if err := os.WriteFile(ps, []byte(`{"Name":"probe-probe-1","Service":"probe","State":"running","Health":"","Image":"busybox:1.36","ExitCode":0}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Order matters: the capability probe uses `-f -` and must be matched before
	// the project config, exactly as the other exec fixtures do.
	script := `#!/bin/sh
case "$*" in
  "--version") echo "Docker version 29.10.0, build fake"; exit 0 ;;
  "compose version --short") echo "5.7.0"; exit 0 ;;
  "context ls --format json") printf '%s\n' '{"Current":true,"Description":"Docker Desktop","DockerEndpoint":"unix:///tmp/d.sock","Error":"","Name":"desktop-linux"}'; exit 0 ;;
  "info --format {{.ServerVersion}}") echo "29.9.3"; exit 0 ;;
esac
case "$*" in
  *" -f - config --format json") echo '{"services":{"probe":{"image":"scratch"}}}'; exit 0 ;;
  *" -f - ps --all --format json") exit 0 ;;
  *" config --format json") cat "$FAKE_CONFIG"; exit 0 ;;
  *" ps --all --format json") cat "$FAKE_PS"; exit 0 ;;
esac
sub=""
for a in "$@"; do
  case "$a" in
    logs|exec) sub="$a"; break ;;
  esac
done
if [ "$sub" = "exec" ]; then
  printf 'probe:/ # '
  while IFS= read -r line; do
    printf '\r\n'
    if [ "$line" = "exit" ]; then exit 0; fi
    printf 'ran:%s\r\n' "$line"
    printf 'probe:/ # '
  done
  exit 0
fi
exit 0
`
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	env := buildEnv(map[string]string{
		"PATH":             bin + ":" + os.Getenv("PATH"),
		"DCRAFT_STATE_DIR": state,
		"FAKE_CONFIG":      config,
		"FAKE_PS":          ps,
	})
	s := startPTY(t, project, env)
	s.waitFor("[↑↓] 이동", 30*time.Second)
	s.pressMenuDown(menuDownPresses(t, "exec"))
	s.waitFor("실행 전 확인", 30*time.Second)
	s.press("\r")     // the default instance
	s.press("\x1b[A") // wrap to the new-command row
	s.press("\r")
	for _, key := range strings.Split("/bin/sh", "") {
		s.press(key)
	}
	s.press("\r")
	// The attach screen itself must appear; from here nothing else is pressed.
	s.waitFor("명령: /bin/sh", 30*time.Second)

	// 1) The prompt arrives with no Enter. A shell writes it without a newline.
	s.waitFor("probe:/ # ", 30*time.Second)

	// 2) Typing appears as it is typed, again with no Enter.
	for _, key := range strings.Split("echo hi", "") {
		s.press(key)
	}
	s.waitFor("probe:/ # echo hi", 30*time.Second)

	// 3) The command runs and its output arrives.
	s.press("\r")
	s.waitFor("ran:echo hi", 30*time.Second)

	// 4) ctrl+] detaches, the last screen stays visible, and the service keeps
	// running.
	s.send("\x1d")
	s.waitFor("마지막 실시간 출력", 30*time.Second)
	s.send("q")
	if code := s.wait(); code != 0 {
		t.Fatalf("TUI exit = %d\n%s", code, s.out.String())
	}
	s.assertRestored()
}
