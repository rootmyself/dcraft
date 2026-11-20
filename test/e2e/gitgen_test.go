package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
)

type gitgenJSON struct {
	Outcome     core.Outcome      `json:"outcome"`
	Diagnostics []core.Diagnostic `json:"diagnostics"`
	Data        struct {
		Generator struct {
			Script        string   `json:"script"`
			ScriptPath    string   `json:"script_path"`
			Destination   string   `json:"destination"`
			RefKind       string   `json:"ref_kind"`
			Stage         string   `json:"stage"`
			Created       bool     `json:"created"`
			Replaced      bool     `json:"replaced"`
			Registered    bool     `json:"registered"`
			TrustRequired bool     `json:"trust_required"`
			NextCommands  []string `json:"next_commands"`
			Notices       []string `json:"notices"`
		} `json:"generator"`
	} `json:"data"`
}

func parseGitgen(t *testing.T, out string) gitgenJSON {
	t.Helper()
	var res gitgenJSON
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("parse JSON: %v\n%s", err, out)
	}
	return res
}

func TestGenerateGitHookJSONCreatesOnlyTheScript(t *testing.T) {
	fake := newFakeDocker(t)
	project := t.TempDir()
	env := fake.env(t, t.TempDir())

	res := runProcess(t, project, env, strings.NewReader(""),
		"generate-git-hook",
		"--repo", "https://127.0.0.1:1/never-contacted.git",
		"--ref", "main",
		"--dest", "src",
		"--output", "scripts/git-sync.sh",
		"--approve", "file_overwrite",
		"--json",
	)
	if res.code != 0 || res.stderr != "" {
		t.Fatalf("exit=%d stderr=%q stdout=%s", res.code, res.stderr, res.stdout)
	}
	parsed := parseGitgen(t, res.stdout)
	if parsed.Outcome != core.OutcomeSuccess {
		t.Fatalf("outcome = %+v", parsed)
	}
	report := parsed.Data.Generator
	if !report.Created || report.Replaced || report.Registered || !report.TrustRequired {
		t.Fatalf("report = %+v", report)
	}
	if report.RefKind != "auto" || report.Stage != "pre" {
		t.Fatalf("ref kind/stage = %q/%q", report.RefKind, report.Stage)
	}
	if !strings.Contains(strings.Join(report.NextCommands, "\n"), "dcraft hooks --add --stage pre --script") {
		t.Fatalf("next commands = %+v", report.NextCommands)
	}
	if _, err := os.Stat(filepath.Join(project, "scripts", "git-sync.sh")); err != nil {
		t.Fatalf("script missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, ".dcraft.yaml")); !os.IsNotExist(err) {
		t.Fatalf("generator registered a hook: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, "src")); !os.IsNotExist(err) {
		t.Fatalf("generator cloned the destination: %v", err)
	}
}

func TestGenerateGitHookWithoutApprovalLeavesExistingFileUntouched(t *testing.T) {
	fake := newFakeDocker(t)
	project := t.TempDir()
	env := fake.env(t, t.TempDir())
	path := filepath.Join(project, "scripts", "git-sync.sh")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("keep\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	res := runProcess(t, project, env, strings.NewReader(""),
		"generate-git-hook", "--repo", "https://example.com/r.git", "--ref", "main",
		"--dest", "src", "--output", "scripts/git-sync.sh", "--json",
	)
	if res.code != 2 {
		t.Fatalf("exit = %d stderr=%q stdout=%s", res.code, res.stderr, res.stdout)
	}
	parsed := parseGitgen(t, res.stdout)
	if parsed.Outcome != core.OutcomeFailure {
		t.Fatalf("outcome = %+v", parsed)
	}
	if len(parsed.Diagnostics) == 0 || parsed.Diagnostics[0].Category != core.CategoryApprovalRequired {
		t.Fatalf("diagnostics = %+v", parsed.Diagnostics)
	}
	if data, _ := os.ReadFile(path); string(data) != "keep\n" {
		t.Fatalf("existing file changed: %q", data)
	}
}

func TestGenerateGitHookMissingInputFailsWithoutPrompting(t *testing.T) {
	fake := newFakeDocker(t)
	project := t.TempDir()
	env := fake.env(t, t.TempDir())
	res := runProcess(t, project, env, strings.NewReader(""), "generate-git-hook", "--approve", "file_overwrite", "--json")
	if res.code != 2 {
		t.Fatalf("exit = %d stdout=%s", res.code, res.stdout)
	}
	parsed := parseGitgen(t, res.stdout)
	found := false
	for _, d := range parsed.Diagnostics {
		if d.Category == core.CategoryGeneratorInputInvalid {
			found = true
		}
	}
	if !found {
		t.Fatalf("diagnostics = %+v", parsed.Diagnostics)
	}
	if _, err := os.Stat(filepath.Join(project, "scripts")); !os.IsNotExist(err) {
		t.Fatal("missing input created a script")
	}
}

func TestGenerateGitHookHumanOutputShowsThePlanAndNextCommands(t *testing.T) {
	fake := newFakeDocker(t)
	project := t.TempDir()
	env := fake.env(t, t.TempDir())
	res := runProcess(t, project, env, strings.NewReader(""),
		"generate-git-hook", "--repo", "https://example.com/r.git", "--ref", "main",
		"--dest", "src", "--output", "scripts/git-sync.sh", "--approve", "file_overwrite",
	)
	if res.code != 0 {
		t.Fatalf("exit = %d stderr=%q", res.code, res.stderr)
	}
	for _, want := range []string{"Git 호스트 스크립트 생성 계획", "원격 저장소에 접속하지 않습니다", "script_trust", "file_overwrite"} {
		if !strings.Contains(res.stderr, want) {
			t.Fatalf("plan missing %q:\n%s", want, res.stderr)
		}
	}
	for _, want := range []string{"Git 호스트 스크립트를 저장했습니다", "dcraft hooks --add --stage pre", "script_trust"} {
		if !strings.Contains(res.stdout, want) {
			t.Fatalf("human output missing %q:\n%s", want, res.stdout)
		}
	}
}

func TestGenerateGitHookTUIWizardShowsThePreviewAndDeclineWritesNothing(t *testing.T) {
	fake := lifecycleFake(t)
	project := projectNamed(t, "gitgen-tui")
	env := fake.env(t, t.TempDir())
	steps := []lifecyclePTYStep{
		{wait: "상태를 새로 고치려면", send: ""},
		// The menu order is the registration order; compute the distance from
		// status so another ticket's feature does not break this journey.
		{wait: "> 스택 상태 (status)", send: strings.Repeat("\x1b[B", menuDownPresses(t, "generate-git-hook"))},
		{wait: "> Git 호스트 스크립트 생성 (generate-git-hook)", send: "\r"},
		lifecyclePTYStep{wait: "저장소", send: "https://127.0.0.1:1/never.git"},
		lifecyclePTYStep{wait: "never.git", send: "\r"},
		lifecyclePTYStep{wait: "branch, tag 또는 전체 SHA", send: "main"},
		lifecyclePTYStep{wait: "main_", send: "\r"},
		lifecyclePTYStep{wait: "소스를 받을 폴더", send: "\r"},
		lifecyclePTYStep{wait: "생성할 스크립트 파일 경로", send: "\r"},
		lifecyclePTYStep{wait: "어느 시점에 실행할지", send: "\r"},
		lifecyclePTYStep{wait: "원격 저장소에 접속하지 않습니다", send: ""},
		lifecyclePTYStep{wait: "파일 덮어쓰기", send: ""},
		lifecyclePTYStep{wait: "file_overwrite", send: ""},
		lifecyclePTYStep{wait: "dcraft hooks --add", send: "\x1b"},
		lifecyclePTYStep{wait: "취소", send: ""},
	}
	output, code := runLifecyclePTYSteps(t, project, env, steps)
	if code != 0 {
		t.Fatalf("TUI exit = %d\n%s", code, output)
	}
	for _, want := range []string{"Git 호스트 스크립트 생성", "원격 저장소에 접속하지 않습니다", "file_overwrite", "script_trust"} {
		if !screenContains(output, want) {
			t.Fatalf("TUI output missing %q:\n%s", want, output)
		}
	}
	if _, err := os.Stat(filepath.Join(project, "scripts", "git-sync.sh")); !os.IsNotExist(err) {
		t.Fatal("declining the wizard wrote a script")
	}
}
