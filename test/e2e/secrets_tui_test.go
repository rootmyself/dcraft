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

	declineOutput, code := runLifecyclePTYSteps(t, project, env, []lifecyclePTYStep{
		{wait: "> 스택 상태 (status)", send: "up"},
		{wait: "> 비밀값 연결 (secrets)", send: "\r"},
		{wait: "서비스 선택", send: "\r"}, // api is the first sorted service
		{wait: "전달 시점 선택", send: "\r"},
		{wait: "전달 방식 선택", send: "\r"},
		{wait: "원본 종류 선택", send: "\r"},
		{wait: "원본 참조 입력", send: "DB_PASSWORD\r"},
		{wait: "저장할 비밀값 연결", send: ""},
		{wait: "Docker 조회 권한", send: "\x1b"}, // esc: decline, save nothing
		{wait: "저장된 내용은 없습니다", send: ""},
	})
	if code != 0 {
		t.Fatalf("TUI decline exit = %d\n%s", code, declineOutput)
	}
	for _, want := range []string{
		"비밀값 연결 설정", "서비스 선택", "전달 방식 선택", "원본 종류 선택",
		"저장할 비밀값 연결", "환경변수", "Docker 조회 권한", "저장된 내용은 없습니다",
	} {
		if !strings.Contains(declineOutput, want) {
			t.Fatalf("TUI output missing %q:\n%s", want, declineOutput)
		}
	}
	if _, err := os.Stat(filepath.Join(project, ".dcraft.yaml")); !os.IsNotExist(err) {
		t.Fatalf("declining the wizard wrote config: %v", err)
	}

	output, code := runLifecyclePTYSteps(t, project, env, []lifecyclePTYStep{
		{wait: "> 스택 상태 (status)", send: "up"},
		{wait: "> 비밀값 연결 (secrets)", send: "\r"},
		{wait: "서비스 선택", send: "\r"}, // api
		{wait: "전달 시점 선택", send: "\r"},
		{wait: "전달 방식 선택", send: "\r"},
		{wait: "원본 종류 선택", send: "\r"},
		{wait: "원본 참조 입력", send: "DB_PASSWORD\r"},
		{wait: "저장할 비밀값 연결", send: "\r"}, // enter: save
		{wait: "비밀값 참조를 저장했습니다", send: ""},
	})
	if code != 0 {
		t.Fatalf("TUI save exit = %d\n%s", code, output)
	}
	if !strings.Contains(output, "비밀값 참조를 저장했습니다") || !strings.Contains(output, "DB_PASSWORD") {
		t.Fatalf("TUI output missing the saved reference:\n%s", output)
	}
	config := readConfig(t, project)
	if !strings.Contains(config, "service: api") || !strings.Contains(config, "ref: DB_PASSWORD") {
		t.Fatalf("saved config = %s", config)
	}
	if strings.Contains(config, "value:") {
		t.Fatalf("config persisted a value field: %s", config)
	}
}

func TestSecretsTUIWizardBuildPhaseFixesDeliveryToBuildKit(t *testing.T) {
	fake := newFakeDocker(t)
	project := t.TempDir()
	writeProject(t, project)
	env := fake.env(t, t.TempDir())

	output, code := runLifecyclePTYSteps(t, project, env, []lifecyclePTYStep{
		{wait: "> 스택 상태 (status)", send: "up"},
		{wait: "> 비밀값 연결 (secrets)", send: "\r"},
		{wait: "서비스 선택", send: "\r"}, // api (first sorted service) has build:
		{wait: "전달 시점 선택", send: "down"},
		{wait: "> 빌드 시점", send: "\r"},
		{wait: "전달 방식 선택", send: "\r"}, // environment is selected; build fixes it
		{wait: "원본 종류 선택", send: "\r"},
		{wait: "원본 참조 입력", send: "BUILD_TOKEN\r"},
		{wait: "저장할 비밀값 연결", send: ""},
		{wait: "BuildKit secret mount로 고정", send: "\r"},
		{wait: "비밀값 참조를 저장했습니다", send: ""},
	})
	if code != 0 {
		t.Fatalf("TUI exit = %d\n%s", code, output)
	}
	for _, want := range []string{
		"빌드 시점", "BuildKit", "--no-cache", "자동으로 수정하지 않습니다",
		"비밀값 참조를 저장했습니다",
	} {
		if !screenContains(output, want) {
			t.Fatalf("TUI output missing %q:\n%s", want, output)
		}
	}
	config := readConfig(t, project)
	if !strings.Contains(config, "phase: build") || !strings.Contains(config, "delivery: file") {
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
