package s3

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSyncScriptRunsOnlyTheNonDeletingSyncCommand(t *testing.T) {
	gen := build(t, "s3://bucket/deploy", ModeSync, "")
	script := string(gen.Content)
	for _, want := range []string{
		"#!/usr/bin/env bash",
		"set -euo pipefail",
		"command -v aws",
		"PROFILE=''",
		"aws s3 sync \"$SRC\" \"$DEST\" --only-show-errors --no-progress",
		"if [ -n \"$PROFILE\" ]",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("sync script missing %q:\n%s", want, script)
		}
	}
	for _, forbidden := range []string{"--delete", "aws configure", "AWS_SECRET_ACCESS_KEY", "AWS_ACCESS_KEY_ID", "eval ", "aws s3 cp"} {
		if executableContains(script, forbidden) {
			t.Fatalf("sync script executes %q:\n%s", forbidden, script)
		}
	}
	if strings.Contains(script, "tar -x") {
		t.Fatalf("sync script extracts archives:\n%s", script)
	}
}

func TestTarScriptValidatesBeforeExtractingIntoAFreshFolder(t *testing.T) {
	gen := build(t, "s3://bucket/path/app.tar.gz", ModeTar, "")
	script := string(gen.Content)
	for _, want := range []string{
		"#!/usr/bin/env bash",
		"set -euo pipefail",
		"command -v aws",
		"command -v tar",
		"command -v gzip",
		"aws s3 cp \"$SRC\" \"$ARCHIVE\" --only-show-errors --no-progress",
		"gzip -t \"$ARCHIVE\"",
		"tar -tzf \"$ARCHIVE\"",
		"tar -tvzf \"$ARCHIVE\"",
		"tar -xzf \"$ARCHIVE\" -C \"$STAGE\"",
		"cp -R \"$STAGE\"/. \"$DEST\"/",
		"mktemp -d",
		"trap cleanup EXIT",
		"readlink",
		`*" -> "*`,
		"find \"$STAGE\" -print0",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("tar script missing %q:\n%s", want, script)
		}
	}
	for _, forbidden := range []string{"--delete", "aws configure", "AWS_SECRET_ACCESS_KEY", "AWS_ACCESS_KEY_ID", "eval ", "-P ", "-C \"$DEST\"", "tar -xzf \"$ARCHIVE\" -C \"$DEST\""} {
		if executableContains(script, forbidden) {
			t.Fatalf("tar script executes %q:\n%s", forbidden, script)
		}
	}
	for _, want := range []string{"절대 경로", "..", "심볼릭 링크", "하드 링크", "임시"} {
		if !strings.Contains(script, want) {
			t.Fatalf("tar script lacks Korean safety text %q:\n%s", want, script)
		}
	}
}

func TestProfileIsEmbeddedOnlyWhenExplicitlySupplied(t *testing.T) {
	gen := build(t, "s3://bucket/prefix", ModeSync, "team-deploy")
	script := string(gen.Content)
	if !strings.Contains(script, "PROFILE='team-deploy'") || !strings.Contains(script, "if [ -n \"$PROFILE\" ]") {
		t.Fatalf("profile not rendered:\n%s", script)
	}
	if strings.Contains(script, "AWS_PROFILE=") || strings.Contains(script, "export AWS") {
		t.Fatalf("script exports AWS state:\n%s", script)
	}
	without := string(build(t, "s3://bucket/prefix", ModeSync, "").Content)
	if strings.Contains(without, "team-deploy") {
		t.Fatalf("default script contains a profile:\n%s", without)
	}
}

func TestGeneratedScriptsAreValidBash(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	for _, tc := range []struct {
		source string
		mode   Mode
	}{
		{"s3://bucket/prefix", ModeSync},
		{"s3://bucket/path/app.tar.gz", ModeTar},
	} {
		gen := build(t, tc.source, tc.mode, "team")
		path := filepath.Join(t.TempDir(), "script.sh")
		if err := os.WriteFile(path, gen.Content, 0o755); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command(bash, "-n", path).CombinedOutput(); err != nil {
			t.Fatalf("bash -n %s: %v\n%s", tc.mode, err, out)
		}
	}
}

func TestGeneratedScriptsExplainKoreanInputsEffectsAndFailure(t *testing.T) {
	for _, tc := range []struct {
		source string
		mode   Mode
	}{
		{"s3://bucket/prefix", ModeSync},
		{"s3://bucket/path/app.tar.gz", ModeTar},
	} {
		gen := build(t, tc.source, tc.mode, "")
		if len(gen.SHA256) != 64 {
			t.Fatalf("sha256 = %q", gen.SHA256)
		}
		script := string(gen.Content)
		for _, want := range []string{
			"# 입력", "# 실행 위치", "# 하는 일", "# 변경되는 파일", "# 전제 조건", "# 실패 시", "# 수정 방법",
			"기존 AWS CLI", "aws configure", "script_trust", "dcraft hooks --add --stage pre",
			"이 스크립트는 도구를 자동 설치하지 않음",
		} {
			if !strings.Contains(script, want) {
				t.Fatalf("%s script missing Korean guidance %q:\n%s", tc.mode, want, script)
			}
		}
	}
}

func TestScriptContentIsDeterministicAndNewlineTerminated(t *testing.T) {
	layout := testLayout(t)
	first, err := Build(Input{Layout: layout, Source: "s3://bucket/prefix", Mode: ModeSync})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Build(Input{Layout: layout, Source: "s3://bucket/prefix", Mode: ModeSync})
	if err != nil {
		t.Fatal(err)
	}
	if string(first.Content) != string(second.Content) {
		t.Fatal("same input rendered different scripts")
	}
	if !strings.HasSuffix(string(first.Content), "\n") {
		t.Fatal("script does not end with a newline")
	}
}

// executableContains ignores comments so guidance such as "aws configure is
// not used" is not mistaken for an executed command.
func executableContains(script, fragment string) bool {
	for _, line := range strings.Split(script, "\n") {
		code := strings.TrimSpace(line)
		if code == "" || strings.HasPrefix(code, "#") {
			continue
		}
		if strings.Contains(code, fragment) {
			return true
		}
	}
	return false
}
