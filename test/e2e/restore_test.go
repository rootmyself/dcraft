package e2e

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rootmyself/dcraft/internal/backup"
	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/restore"
)

type restoreResult struct {
	SchemaVersion int               `json:"schema_version"`
	Action        string            `json:"action"`
	Outcome       core.Outcome      `json:"outcome"`
	Diagnostics   []core.Diagnostic `json:"diagnostics"`
	Data          struct {
		Restore restore.Report `json:"restore"`
	} `json:"data"`
}

func parseRestore(t *testing.T, out string) restoreResult {
	t.Helper()
	var res restoreResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("parse restore JSON: %v\n%s", err, out)
	}
	return res
}

func hasRestoreCategory(diags []core.Diagnostic, category core.Category) bool {
	for _, d := range diags {
		if d.Category == category {
			return true
		}
	}
	return false
}

// restoreArtifact builds a complete v1 artifact for the fake stack. It also
// lives under the project's ./dcraft-backups so the TUI wizard discovers it.
func restoreArtifact(t *testing.T, project string) string {
	t.Helper()
	root := filepath.Join(project, "dcraft-backups")
	dir := filepath.Join(root, "dcraft-backup-test-volume-data-20260101T000000Z")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	entries := []struct {
		name string
		mode int64
		body string
	}{
		{"./", 0o755, ""},
		{"./.hidden", 0o600, "dot"},
		{"./file.txt", 0o644, "hello from backup"},
	}
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Mode: e.mode, ModTime: time.Unix(1700000000, 0)}
		if e.name == "./" {
			hdr.Typeflag = tar.TypeDir
		} else {
			hdr.Typeflag = tar.TypeReg
			hdr.Size = int64(len(e.body))
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if e.body != "" {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, backup.DataFile), buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	source := backup.Source{
		Kind: backup.KindVolume, Project: "backup-test", Environment: "",
		ProjectDir: project, VolumeKey: "data", VolumeName: "backup-test_data",
		MountTargets: []string{"/data"}, Services: []string{"app", "worker"},
	}
	if _, err := backup.BuildArtifact(dir, source, backup.StackState{
		ProjectName: "backup-test", Services: []string{"app", "worker"},
		RunningBefore: []string{"app"}, StoppedForBackup: []string{"app"},
	}, backup.Tool{Version: "test"}, time.Unix(1700000000, 0)); err != nil {
		t.Fatalf("build artifact: %v", err)
	}
	return dir
}

func restoreProject(t *testing.T) (string, *fakeDocker, []string) {
	t.Helper()
	fake := backupFake(t)
	project := projectNamed(t, "backup-project")
	env := fake.env(t, t.TempDir())
	return project, fake, env
}

func TestRestoreWithoutBackupOrApprovalFailsBeforeStoppingAnything(t *testing.T) {
	project, fake, env := restoreProject(t)
	artifact := restoreArtifact(t, project)

	res := runProcess(t, project, env, strings.NewReader(""), "restore", "--path", project, "--json",
		"--approve", "data_restore")
	if res.code != 2 {
		t.Fatalf("missing backup exit = %d, want 2; stdout=%s", res.code, res.stdout)
	}
	parsed := parseRestore(t, res.stdout)
	if !hasRestoreCategory(parsed.Diagnostics, core.CategoryRestoreBackupRequired) {
		t.Fatalf("missing backup diagnostics = %+v", parsed.Diagnostics)
	}

	res = runProcess(t, project, env, strings.NewReader(""), "restore", "--path", project, "--json",
		"--backup", artifact)
	if res.code != 2 {
		t.Fatalf("missing approval exit = %d, want 2; stdout=%s", res.code, res.stdout)
	}
	parsed = parseRestore(t, res.stdout)
	if !hasRestoreCategory(parsed.Diagnostics, core.CategoryApprovalRequired) {
		t.Fatalf("missing approval diagnostics = %+v", parsed.Diagnostics)
	}
	calls := strings.Join(fake.calls(t), "\n")
	if strings.Contains(calls, " stop ") || strings.Contains(calls, "dcraft-restore-") {
		t.Fatalf("missing input stopped or replaced data:\n%s", calls)
	}
}

func TestRestoreCLIReplacesThroughSafetyBackupAndRestartsOnlyRunningServices(t *testing.T) {
	project, fake, env := restoreProject(t)
	artifact := restoreArtifact(t, project)
	marker := filepath.Join(t.TempDir(), "hook-ran")
	script := filepath.Join(project, "pre.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch '"+marker+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".dcraft.yaml"), []byte("hooks:\n  - path: pre.sh\n    stage: pre\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := runProcess(t, project, env, strings.NewReader(""), "restore", "--path", project, "--json",
		"--backup", artifact, "--approve", "data_restore")
	if res.code != 0 {
		t.Fatalf("restore exit = %d stdout=%s stderr=%s", res.code, res.stdout, res.stderr)
	}
	parsed := parseRestore(t, res.stdout)
	if parsed.Outcome != core.OutcomeSuccess || parsed.Action != "restore" {
		t.Fatalf("result = %+v", parsed)
	}
	report := parsed.Data.Restore
	if report.DataState != restore.DataReplaced || report.VolumeName != "backup-test_data" {
		t.Fatalf("report = %+v", report)
	}
	if strings.Join(report.Stopped, ",") != "app" || strings.Join(report.Restarted, ",") != "app" {
		t.Fatalf("services stopped=%v restarted=%v", report.Stopped, report.Restarted)
	}
	if report.SafetyBackup == "" {
		t.Fatal("safety backup path missing")
	}
	manifest, err := backup.Verify(report.SafetyBackup)
	if err != nil {
		t.Fatalf("safety backup did not verify: %v", err)
	}
	if manifest.Source.VolumeName != "backup-test_data" || !manifest.Complete {
		t.Fatalf("safety manifest = %+v", manifest)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("restore executed a project hook: %v", err)
	}
	calls := strings.Join(fake.calls(t), "\n")
	for _, want := range []string{
		" stop app",
		"create --name dcraft-restore-",
		"find /data -mindepth 1 -delete",
		"-xpzf /backup/data.tar.gz",
		"/verify/check.sh",
		" start app",
	} {
		if !strings.Contains(calls, want) {
			t.Fatalf("restore call missing %q:\n%s", want, calls)
		}
	}
	if strings.Contains(calls, " stop worker") {
		t.Fatalf("already stopped worker was stopped:\n%s", calls)
	}
}

func TestRestoreCLIRefusesCorruptArtifactsAndUnknownVolumes(t *testing.T) {
	project, fake, env := restoreProject(t)
	artifact := restoreArtifact(t, project)
	raw, err := os.ReadFile(filepath.Join(artifact, backup.DataFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(artifact, backup.DataFile), raw[:len(raw)-8], 0o600); err != nil {
		t.Fatal(err)
	}
	res := runProcess(t, project, env, strings.NewReader(""), "restore", "--path", project, "--json",
		"--backup", artifact, "--approve", "data_restore")
	if res.code != 1 {
		t.Fatalf("corrupt exit = %d; stdout=%s", res.code, res.stdout)
	}
	parsed := parseRestore(t, res.stdout)
	if !hasRestoreCategory(parsed.Diagnostics, core.CategoryRestoreIntegrityFailed) {
		t.Fatalf("corrupt diagnostics = %+v", parsed.Diagnostics)
	}
	if calls := strings.Join(fake.calls(t), "\n"); strings.Contains(calls, " stop ") {
		t.Fatalf("corrupt artifact stopped services:\n%s", calls)
	}

	artifact = restoreArtifact(t, project)
	res = runProcess(t, project, env, strings.NewReader(""), "restore", "--path", project, "--json",
		"--backup", artifact, "--volume", "nope", "--approve", "data_restore")
	if res.code != 2 {
		t.Fatalf("unknown volume exit = %d; stdout=%s", res.code, res.stdout)
	}
	parsed = parseRestore(t, res.stdout)
	if !hasRestoreCategory(parsed.Diagnostics, core.CategoryRestoreVolumeUnknown) {
		t.Fatalf("unknown volume diagnostics = %+v", parsed.Diagnostics)
	}
}

func TestRestoreCLIUnwritableSafetyDirectoryLeavesTheDataUntouched(t *testing.T) {
	project, fake, env := restoreProject(t)
	artifact := restoreArtifact(t, project)
	safety := filepath.Join(t.TempDir(), "safety")
	if err := os.Mkdir(safety, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(safety, 0o700) })
	res := runProcess(t, project, env, strings.NewReader(""), "restore", "--path", project, "--json",
		"--backup", artifact, "--safety-output", safety, "--approve", "data_restore")
	if res.code != 1 {
		t.Fatalf("unwritable safety exit = %d; stdout=%s stderr=%s", res.code, res.stdout, res.stderr)
	}
	parsed := parseRestore(t, res.stdout)
	if !hasRestoreCategory(parsed.Diagnostics, core.CategoryRestoreSafetyFailed) {
		t.Fatalf("safety diagnostics = %+v", parsed.Diagnostics)
	}
	calls := strings.Join(fake.calls(t), "\n")
	if strings.Contains(calls, " stop ") || strings.Contains(calls, "find /data -mindepth 1 -delete") {
		t.Fatalf("unwritable safety location touched data:\n%s", calls)
	}
}

func TestRestoreTUIWizardUsesTheGenericPlanAndDataRestoreApproval(t *testing.T) {
	project, _, env := restoreProject(t)
	restoreArtifact(t, project)

	output, code := runLifecyclePTYSteps(t, project, env, []lifecyclePTYStep{
		{wait: "> 스택 상태 (status)", send: "up"},
		{wait: "> 비밀값 연결 (secrets)", send: "up"},
		{wait: "> 이전 버전 복원 (rollback)", send: "up"},
		{wait: "> 데이터 복원 (restore)", send: "\r"},
		{wait: "복원할 백업 선택", send: "\r"},
		{wait: "복원 대상 선택", send: "\r"},
		{wait: "안전 백업 폴더", send: "\r"},
		{wait: "복원 계획", send: "\r"}, // wizard preview is not an approval
		{wait: "승인 확인", send: ""},
		{wait: "데이터 복원 (data_restore)", send: "\r"},
		{wait: "복원 완료", send: ""},
	})
	if code != 0 {
		t.Fatalf("restore TUI exit = %d\n%s", code, output)
	}
	for _, want := range []string{"데이터 복원 설정", "복원 계획", "안전 백업", "승인 확인", "data_restore", "복원 완료"} {
		if !strings.Contains(output, want) {
			t.Fatalf("TUI output missing %q:\n%s", want, output)
		}
	}
}
