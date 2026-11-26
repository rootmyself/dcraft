package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
)

type s3genJSON struct {
	Outcome     core.Outcome      `json:"outcome"`
	Diagnostics []core.Diagnostic `json:"diagnostics"`
	Data        struct {
		Generator struct {
			Script        string   `json:"script"`
			ScriptPath    string   `json:"script_path"`
			Destination   string   `json:"destination"`
			Source        string   `json:"source"`
			Mode          string   `json:"mode"`
			AWSProfile    string   `json:"aws_profile"`
			SHA256        string   `json:"sha256"`
			Created       bool     `json:"created"`
			Replaced      bool     `json:"replaced"`
			Registered    bool     `json:"registered"`
			TrustRequired bool     `json:"trust_required"`
			NextCommands  []string `json:"next_commands"`
			Notices       []string `json:"notices"`
		} `json:"generator"`
	} `json:"data"`
}

func parseS3gen(t *testing.T, out string) s3genJSON {
	t.Helper()
	var res s3genJSON
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("parse JSON: %v\n%s", err, out)
	}
	return res
}

func TestGenerateS3HookJSONCreatesOnlyTheScript(t *testing.T) {
	fake := newFakeDocker(t)
	project := t.TempDir()
	env := fake.env(t, t.TempDir())

	res := runProcess(t, project, env, strings.NewReader(""),
		"generate-s3-hook",
		"--source", "s3://bucket/deploy",
		"--mode", "sync",
		"--dest", "deploy",
		"--output", "scripts/s3-sync.sh",
		"--aws-profile", "team-deploy",
		"--approve", "file_overwrite",
		"--json",
	)
	if res.code != 0 || res.stderr != "" {
		t.Fatalf("exit=%d stderr=%q stdout=%s", res.code, res.stderr, res.stdout)
	}
	parsed := parseS3gen(t, res.stdout)
	if parsed.Outcome != core.OutcomeSuccess {
		t.Fatalf("outcome = %+v", parsed)
	}
	report := parsed.Data.Generator
	if !report.Created || report.Replaced || report.Registered || !report.TrustRequired {
		t.Fatalf("report = %+v", report)
	}
	if report.Source != "s3://bucket/deploy" || report.Mode != "sync" || report.AWSProfile != "team-deploy" {
		t.Fatalf("source/mode/profile = %q/%q/%q", report.Source, report.Mode, report.AWSProfile)
	}
	if len(report.SHA256) != 64 {
		t.Fatalf("sha256 = %q", report.SHA256)
	}
	joined := strings.Join(report.NextCommands, "\n")
	for _, want := range []string{"dcraft hooks --add --stage pre --script", "script_trust"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("next commands = %+v", report.NextCommands)
		}
	}
	scriptPath := filepath.Join(project, "scripts", "s3-sync.sh")
	if info, err := os.Stat(scriptPath); err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("script = %v, %v", info, err)
	}
	if _, err := os.Stat(filepath.Join(project, ".dcraft.yaml")); !os.IsNotExist(err) {
		t.Fatalf("generator registered a hook: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, "deploy")); !os.IsNotExist(err) {
		t.Fatalf("generator downloaded the destination: %v", err)
	}
}

func TestGenerateS3HookWithoutApprovalLeavesExistingFileUntouched(t *testing.T) {
	fake := newFakeDocker(t)
	project := t.TempDir()
	env := fake.env(t, t.TempDir())
	path := filepath.Join(project, "scripts", "s3-sync.sh")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("keep\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	res := runProcess(t, project, env, strings.NewReader(""),
		"generate-s3-hook", "--source", "s3://bucket/deploy", "--mode", "sync",
		"--dest", "deploy", "--output", "scripts/s3-sync.sh", "--json",
	)
	if res.code != 2 {
		t.Fatalf("exit = %d stderr=%q stdout=%s", res.code, res.stderr, res.stdout)
	}
	parsed := parseS3gen(t, res.stdout)
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

func TestGenerateS3HookMissingInputFailsWithoutPrompting(t *testing.T) {
	fake := newFakeDocker(t)
	project := t.TempDir()
	env := fake.env(t, t.TempDir())
	res := runProcess(t, project, env, strings.NewReader(""), "generate-s3-hook", "--approve", "file_overwrite", "--json")
	if res.code != 2 {
		t.Fatalf("exit = %d stdout=%s", res.code, res.stdout)
	}
	parsed := parseS3gen(t, res.stdout)
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

func TestGenerateS3HookOutsideDestinationIsRefused(t *testing.T) {
	fake := newFakeDocker(t)
	project := t.TempDir()
	env := fake.env(t, t.TempDir())
	outside := filepath.Join(filepath.Dir(project), "s3-outside")
	res := runProcess(t, project, env, strings.NewReader(""),
		"generate-s3-hook", "--source", "s3://bucket/deploy", "--mode", "sync",
		"--dest", outside, "--output", "scripts/s3-sync.sh",
		"--approve", "file_overwrite", "--json",
	)
	if res.code != 2 {
		t.Fatalf("exit = %d stderr=%q stdout=%s", res.code, res.stderr, res.stdout)
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("outside destination was created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, "scripts")); !os.IsNotExist(err) {
		t.Fatalf("outside destination still wrote the script: %v", err)
	}
}

func TestGenerateS3HookHumanOutputShowsThePlanAndNextCommands(t *testing.T) {
	fake := newFakeDocker(t)
	project := t.TempDir()
	env := fake.env(t, t.TempDir())
	res := runProcess(t, project, env, strings.NewReader(""),
		"generate-s3-hook", "--source", "s3://bucket/path/app.tar.gz", "--mode", "tar",
		"--dest", "deploy", "--output", "scripts/s3-deploy.sh", "--approve", "file_overwrite",
	)
	if res.code != 0 {
		t.Fatalf("exit = %d stderr=%q", res.code, res.stderr)
	}
	for _, want := range []string{
		"S3 호스트 스크립트 생성 계획", "S3에 접속하지 않음", "aws CLI",
		"gzip -t", "script_trust", "file_overwrite",
	} {
		if !strings.Contains(res.stderr, want) {
			t.Fatalf("plan missing %q:\n%s", want, res.stderr)
		}
	}
	for _, want := range []string{"S3 호스트 스크립트 저장", "dcraft hooks --add --stage pre", "script_trust"} {
		if !strings.Contains(res.stdout, want) {
			t.Fatalf("human output missing %q:\n%s", want, res.stdout)
		}
	}
}

func TestGenerateS3HookTUIWizardShowsThePreviewAndDeclineWritesNothing(t *testing.T) {
	fake := lifecycleFake(t)
	project := projectNamed(t, "s3gen-tui-decline")
	env := fake.env(t, t.TempDir())
	steps := []lifecyclePTYStep{
		{wait: "마지막 조회", send: ""},
		{wait: "마지막 조회", walk: "S3", send: "\r"},
		{wait: "S3 원본", send: "s3://bucket/path/app.tar.gz"},
		{wait: "app.tar.gz_", send: "\r"},
		{wait: "> 디렉터리 동기화", send: "down"},
		{wait: "> tar.gz 다운로드", send: "\r"},
		{wait: "받을 폴더", send: "\r"},
		{wait: "생성할 스크립트 파일 경로", send: "\r"},
		{wait: "S3 호스트 스크립트 생성 계획", send: ""},
		{wait: "file_overwrite", send: ""},
		{wait: "dcraft hooks --add", send: "\x1b[6~"}, // page down: applied values lead the panel
		{wait: "aws s3 cp", send: "\x1b"},
		{wait: "취소", send: ""},
	}
	output, code := runLifecyclePTYSteps(t, project, env, steps)
	if code != 0 {
		t.Fatalf("TUI exit = %d\n%s", code, output)
	}
	for _, want := range []string{
		"S3 호스트 스크립트 생성", "S3에 접속하지 않음", "aws s3 cp",
		"gzip -t", "file_overwrite", "script_trust", "취소",
	} {
		if !screenContains(output, want) {
			t.Fatalf("TUI output missing %q:\n%s", want, output)
		}
	}
	if _, err := os.Stat(filepath.Join(project, "scripts", "s3-sync.sh")); !os.IsNotExist(err) {
		t.Fatal("declining the wizard wrote a script")
	}
}

func TestGenerateS3HookTUIWizardAcceptSavesTheScript(t *testing.T) {
	fake := lifecycleFake(t)
	project := projectNamed(t, "s3gen-tui-accept")
	env := fake.env(t, t.TempDir())
	steps := []lifecyclePTYStep{
		{wait: "마지막 조회", send: ""},
		{wait: "마지막 조회", walk: "S3", send: "\r"},
		{wait: "S3 원본", send: "s3://bucket/deploy"},
		{wait: "deploy_", send: "\r"},
		{wait: "> 디렉터리 동기화", send: "\r"},
		{wait: "받을 폴더", send: "\r"},
		{wait: "생성할 스크립트 파일 경로", send: "\r"},
		{wait: "S3 호스트 스크립트 생성 계획", send: ""},
		{wait: "dcraft hooks --add", send: "\r"},
		{wait: "승인 확인", send: "\x1b[D"}, // the panel starts on cancel
		{wait: "▸ [ 승인 ]", send: "\r"},
		{wait: "S3 호스트 스크립트 저장", send: ""},
	}
	output, code := runLifecyclePTYSteps(t, project, env, steps)
	if code != 0 {
		t.Fatalf("TUI exit = %d\n%s", code, output)
	}
	path := filepath.Join(project, "scripts", "s3-sync.sh")
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("accepted wizard did not save the script: %v, %v", info, err)
	}
	if !strings.Contains(output, "S3 호스트 스크립트 저장") {
		t.Fatalf("TUI output missing the success screen:\n%s", output)
	}
	if _, err := os.Stat(filepath.Join(project, ".dcraft.yaml")); !os.IsNotExist(err) {
		t.Fatalf("TUI saved a hook registration: %v", err)
	}
}
