package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func initWizardNavigation() []lifecyclePTYStep {
	// The TUI opens on status. init is registered last, so four downs reach it
	// through the same menu every other feature uses.
	return []lifecyclePTYStep{
		{wait: "마지막 조회", send: "\x1b[B"},
		{wait: "▸ 스택 중지", send: "\x1b[B"},
		{wait: "▸ 스택 시작", send: "\x1b[B"},
		{wait: "▸ 갱신", send: "\x1b[B"},
		{wait: "▸ 최초 구성", send: "\r"},
	}
}

func initWizardAnswers() []lifecyclePTYStep {
	return []lifecyclePTYStep{
		{wait: "최초 Compose 설정", send: ""},
		{wait: "단계 1/8", send: "\r"},
		{wait: "단계 2/10", send: "\r"},
		{wait: "단계 3/10", send: "\r"},
		{wait: "단계 4/10", send: "\r"},
		{wait: "단계 5/10", send: "\r"},
		{wait: "단계 6/10", send: "\r"},
		{wait: "단계 7/10", send: "\r"},
		{wait: "단계 8/10", send: "\r"},
		{wait: "단계 9/10", send: "\r"},
		{wait: "단계 10/10", send: "\r"},
		{wait: "Compose 파일 미리보기", send: ""},
		{wait: "다음 명령: dcraft up", send: ""},
	}
}

func TestInitTUIWizardDeclinesWithoutWritingAndSavesAfterConfirmation(t *testing.T) {
	requireIntegration(t)
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "Dockerfile"), []byte("FROM busybox:1.36\nCMD [\"httpd\", \"-f\", \"-p\", \"8080\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := buildEnv(map[string]string{"DCRAFT_STATE_DIR": t.TempDir()})
	composePath := filepath.Join(project, "compose.yaml")

	steps := append(initWizardNavigation(), initWizardAnswers()...)
	steps = append(steps,
		lifecyclePTYStep{wait: "다음 명령: dcraft up", send: "\x1b"},
		lifecyclePTYStep{wait: "저장된 내용 없음", send: ""},
	)
	output, code := runLifecyclePTYSteps(t, project, env, steps)
	if code != 0 {
		t.Fatalf("declined TUI exit = %d\n%s", code, output)
	}
	if _, err := os.Stat(composePath); !os.IsNotExist(err) {
		t.Fatalf("declining the wizard wrote a file:\n%s", output)
	}

	steps = append(initWizardNavigation(), initWizardAnswers()...)
	steps = append(steps,
		lifecyclePTYStep{wait: "다음 명령: dcraft up", send: "\r"},
		lifecyclePTYStep{wait: "Compose 파일 저장.", send: ""},
		// The saved screen points at the menu, not the command: the file is a
		// standard Compose input in this folder, so the menu finds it.
		lifecyclePTYStep{wait: "메뉴에서 스택 시작 선택", send: ""},
	)
	output, code = runLifecyclePTYSteps(t, project, env, steps)
	if code != 0 {
		t.Fatalf("accepted TUI exit = %d\n%s", code, output)
	}
	data, err := os.ReadFile(composePath)
	if err != nil {
		t.Fatalf("wizard did not save compose.yaml: %v\n%s", err, output)
	}
	for _, want := range []string{"services:", "build:", "dockerfile: Dockerfile"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("saved file missing %q:\n%s", want, data)
		}
	}
	if strings.Contains(output, "실행") && strings.Contains(output, "스택 시작") {
		t.Fatalf("init ran the stack after saving:\n%s", output)
	}
}

func TestInitTUIWizardOverwriteShowsTheApprovalAndDeclineKeepsTheOldFile(t *testing.T) {
	requireIntegration(t)
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "Dockerfile"), []byte("FROM busybox:1.36\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	composePath := filepath.Join(project, "compose.yaml")
	oldContent := "services:\n  old:\n    image: busybox:1.36\n"
	if err := os.WriteFile(composePath, []byte(oldContent), 0o644); err != nil {
		t.Fatal(err)
	}
	before := sha256File(t, composePath)
	env := buildEnv(map[string]string{"DCRAFT_STATE_DIR": t.TempDir()})

	steps := append(initWizardNavigation(), initWizardAnswers()...)
	steps = append(steps,
		lifecyclePTYStep{wait: "파일 덮어쓰기 (file_overwrite)", send: "\x1b"},
		lifecyclePTYStep{wait: "저장된 내용 없음", send: ""},
	)
	output, code := runLifecyclePTYSteps(t, project, env, steps)
	if code != 0 {
		t.Fatalf("declined overwrite exit = %d\n%s", code, output)
	}
	if sha256File(t, composePath) != before {
		t.Fatalf("declining the overwrite changed compose.yaml:\n%s", output)
	}

	steps = append(initWizardNavigation(), initWizardAnswers()...)
	steps = append(steps,
		lifecyclePTYStep{wait: "파일 덮어쓰기 (file_overwrite)", send: "\r"},
		lifecyclePTYStep{wait: "승인 확인", send: ""},
		lifecyclePTYStep{wait: "파일 덮어쓰기", send: "\r"},
		lifecyclePTYStep{wait: "Compose 파일 저장.", send: ""},
		lifecyclePTYStep{wait: "기존 파일을 덮어썼음.", send: ""},
		lifecyclePTYStep{wait: "dcraft up", send: ""},
	)
	output, code = runLifecyclePTYSteps(t, project, env, steps)
	if code != 0 {
		t.Fatalf("accepted overwrite exit = %d\n%s", code, output)
	}
	if sha256File(t, composePath) == before {
		t.Fatalf("accepting the overwrite did not replace compose.yaml:\n%s", output)
	}
	data, err := os.ReadFile(composePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "build:") || strings.Contains(string(data), "old:") {
		t.Fatalf("overwritten file = %s", data)
	}
}

func TestInitTUIWizardDoesNotOverwriteAFileCreatedAfterThePreview(t *testing.T) {
	requireIntegration(t)
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "Dockerfile"), []byte("FROM busybox:1.36\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	composePath := filepath.Join(project, "compose.yaml")
	env := buildEnv(map[string]string{"DCRAFT_STATE_DIR": t.TempDir()})
	intruder := "services:\n  intruder:\n    image: busybox:1.36\n"

	steps := append(initWizardNavigation(), initWizardAnswers()...)
	steps = append(steps,
		lifecyclePTYStep{
			wait: "Compose 파일 미리보기",
			action: func() {
				if err := os.WriteFile(composePath, []byte(intruder), 0o644); err != nil {
					t.Errorf("create concurrent compose.yaml: %v", err)
				}
			},
			send: "\r",
		},
		lifecyclePTYStep{wait: "파일 덮어쓰기 승인이 필요.", send: ""},
		lifecyclePTYStep{wait: "다시 미리보기", send: ""},
	)
	output, code := runLifecyclePTYSteps(t, project, env, steps)
	if code != 0 {
		t.Fatalf("TUI exit = %d\n%s", code, output)
	}
	data, err := os.ReadFile(composePath)
	if err != nil {
		t.Fatalf("concurrent compose.yaml missing: %v", err)
	}
	if string(data) != intruder {
		t.Fatalf("a file created after the preview was overwritten:\n%s", data)
	}
	for _, want := range []string{"파일 덮어쓰기 승인이 필요.", "다시 미리보기", "덮어쓰지 않음"} {
		if !screenContains(output, want) {
			t.Fatalf("failure guidance missing %q:\n%s", want, output)
		}
	}
}
