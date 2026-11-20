package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHooksTUIShowsPlanTrustApprovalAndDeclineRunsNothing(t *testing.T) {
	fake := lifecycleFake(t)
	project := projectNamed(t, "hooks-tui")
	marker := filepath.Join(t.TempDir(), "tui-marker.log")
	if err := os.MkdirAll(filepath.Join(project, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(project, "scripts", "pre.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nprintf 'ran\n' >> "+marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".dcraft.yaml"), []byte("hooks:\n  - path: scripts/pre.sh\n    stage: pre\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := fake.env(t, t.TempDir())

	output, code := runLifecyclePTYSteps(t, project, env, []lifecyclePTYStep{
		{wait: "[↑↓] 이동", send: menuDown(t, "up")},
		{wait: "▸ 스택 시작", send: "\r"},
		{wait: "서비스 선택", send: "\r"},
		{wait: "승인 확인", send: ""},
		{wait: "스크립트 신뢰", send: ""},
		{wait: "scripts/pre.sh", send: ""},
		{wait: "sha256:", send: ""},
		{wait: "[enter] 실행", send: "\x1b"},
		{wait: "아무것도 실행하지 않았습니다", send: ""},
	})
	if code != 0 {
		t.Fatalf("TUI exit = %d\n%s", code, output)
	}
	for _, want := range []string{
		"스크립트 신뢰", "script_trust", "scripts/pre.sh", "sha256:",
		"새 승인 필요", "아무것도 실행하지 않았습니다",
	} {
		if !screenContains(output, want) {
			t.Fatalf("TUI output missing %q:\n%s", want, output)
		}
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("declining script trust ran the hook: %v", err)
	}
}
