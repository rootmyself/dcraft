package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecretsTUIWizardShowsEffectAndSavesOnlyAfterConfirmation(t *testing.T) {
	fake := newFakeDocker(t)
	project := t.TempDir()
	writeProject(t, project)
	env := fake.env(t, t.TempDir())

	output, code := runLifecyclePTYSteps(t, project, env, []lifecyclePTYStep{
		{wait: "> 스택 상태 (status)", send: "up"},
		{wait: "> 비밀값 연결 (secrets)", send: "\r"},
		{wait: "서비스 선택", send: "down"},
		{wait: "> web", send: "\r"}, // web is selected after down from api
		{wait: "전달 시점 선택", send: "\r"},
		{wait: "전달 방식 선택", send: "\r"},
		{wait: "원본 종류 선택", send: "\r"},
		{wait: "원본 참조 입력", send: "DB_PASSWORD\r"},
		{wait: "저장할 비밀값 연결", send: ""},
		{wait: "Docker 조회 권한", send: "\x1b"}, // esc: decline, save nothing
		{wait: "저장된 내용은 없습니다", send: ""},
		// Start the wizard again; the menu selection is still secrets.
		{wait: "> 비밀값 연결 (secrets)", send: "\r"},
		{wait: "서비스 선택", send: "down"},
		{wait: "> web", send: "\r"},
		{wait: "전달 시점 선택", send: "\r"},
		{wait: "전달 방식 선택", send: "\r"},
		{wait: "원본 종류 선택", send: "\r"},
		{wait: "원본 참조 입력", send: "DB_PASSWORD\r"},
		{wait: "저장할 비밀값 연결", send: "\r"}, // enter: save
		{wait: "비밀값 참조를 저장했습니다", send: ""},
	})
	if code != 0 {
		t.Fatalf("TUI exit = %d\n%s", code, output)
	}
	for _, want := range []string{
		"비밀값 연결 설정", "서비스 선택", "전달 방식 선택", "원본 종류 선택",
		"저장할 비밀값 연결", "환경변수", "Docker 조회 권한", "저장된 내용은 없습니다",
		"비밀값 참조를 저장했습니다", "DB_PASSWORD",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("TUI output missing %q:\n%s", want, output)
		}
	}
	config := readConfig(t, project)
	if !strings.Contains(config, "service: web") || !strings.Contains(config, "ref: DB_PASSWORD") {
		t.Fatalf("saved config = %s", config)
	}
	if strings.Contains(config, "value:") {
		t.Fatalf("config persisted a value field: %s", config)
	}
}

func TestSecretsTUIDeclineLeavesNoConfig(t *testing.T) {
	fake := newFakeDocker(t)
	project := t.TempDir()
	writeProject(t, project)
	env := fake.env(t, t.TempDir())

	output, code := runLifecyclePTYSteps(t, project, env, []lifecyclePTYStep{
		{wait: "> 스택 상태 (status)", send: "up"},
		{wait: "> 비밀값 연결 (secrets)", send: "\r"},
		{wait: "서비스 선택", send: "\r"},
		{wait: "전달 시점 선택", send: "\r"},
		{wait: "전달 방식 선택", send: "\r"},
		{wait: "원본 종류 선택", send: "\r"},
		{wait: "원본 참조 입력", send: "DB_PASSWORD\r"},
		{wait: "저장할 비밀값 연결", send: "\x1b"},
		{wait: "저장된 내용은 없습니다", send: ""},
	})
	if code != 0 {
		t.Fatalf("TUI exit = %d\n%s", code, output)
	}
	if _, err := os.Stat(filepath.Join(project, ".dcraft.yaml")); !os.IsNotExist(err) {
		t.Fatalf("declining the wizard wrote config: %v", err)
	}
}
