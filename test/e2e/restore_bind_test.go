package e2e

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rootmyself/dcraft/internal/backup"
	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/data"
	"github.com/rootmyself/dcraft/internal/restore"
)

// restoreBindArtifact builds a complete bind-folder v1 artifact for the fake
// stack. It is the same format the backup command publishes, without invoking
// the Docker adapter.
func restoreBindArtifact(t *testing.T, project, hostDir string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(hostDir)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(project, "dcraft-backups", "dcraft-backup-test-bind-20260101T000000Z")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := backup.ArchiveHostDir(context.Background(), resolved, filepath.Join(dir, backup.DataFile)); err != nil {
		t.Fatalf("ArchiveHostDir: %v", err)
	}
	if _, err := backup.BuildArtifact(dir, backup.Source{
		Kind: backup.KindBind, Project: "backup-test", Environment: "", ProjectDir: project,
		HostPath: resolved, MountTargets: []string{"/bind"}, Services: []string{"app"},
	}, backup.StackState{
		ProjectName: "backup-test", Services: []string{"app"}, RunningBefore: []string{"app"}, StoppedForBackup: []string{"app"},
	}, backup.Tool{Version: "test"}, time.Unix(1700000000, 0)); err != nil {
		t.Fatalf("BuildArtifact: %v", err)
	}
	return dir
}

func bindRestoreFake(t *testing.T, hostDir string) (*fakeDocker, string, []string) {
	t.Helper()
	fake := backupFake(t)
	fake.writeConfig(t, backupBindConfig(hostDir))
	project := projectNamed(t, "backup-project")
	env := fake.env(t, t.TempDir())
	return fake, project, env
}

func TestRestoreBindCLIReplacesTheFolderAndRestoresOnlyRunningServices(t *testing.T) {
	hostDir := writeBindFixture(t)
	fake, project, env := bindRestoreFake(t, hostDir)
	artifact := restoreBindArtifact(t, project, hostDir)
	if err := os.WriteFile(filepath.Join(hostDir, "file.txt"), []byte("version two"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hostDir, "stale.txt"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(hostDir, ".hidden")); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(hostDir)
	if err != nil {
		t.Fatal(err)
	}

	res := runProcess(t, project, env, strings.NewReader(""), "restore", "--path", project, "--json",
		"--backup", artifact, "--bind", hostDir, "--approve", "data_restore")
	if res.code != 0 {
		t.Fatalf("restore exit = %d stdout=%s stderr=%s", res.code, res.stdout, res.stderr)
	}
	if strings.Contains(res.stdout, "SUPER_SECRET_VALUE") {
		t.Fatalf("JSON output leaked a project environment value: %s", res.stdout)
	}
	parsed := parseRestore(t, res.stdout)
	if parsed.Outcome != core.OutcomeSuccess || parsed.Action != "restore" {
		t.Fatalf("result = %+v", parsed)
	}
	report := parsed.Data.Restore
	if report.TargetKind != data.KindBind || report.DataState != restore.DataReplaced {
		t.Fatalf("report = %+v", report)
	}
	if report.HostPath != resolved || report.VolumeKey != "" || report.VolumeName != "" {
		t.Fatalf("bind target fields = %+v", report)
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
	if manifest.Source.Kind != backup.KindBind || manifest.Source.HostPath != resolved {
		t.Fatalf("safety manifest = %+v", manifest)
	}
	if raw, err := os.ReadFile(filepath.Join(hostDir, "file.txt")); err != nil || string(raw) != "hello" {
		t.Fatalf("restored file = %q, %v", raw, err)
	}
	if _, err := os.Stat(filepath.Join(hostDir, "stale.txt")); !os.IsNotExist(err) {
		t.Fatalf("stale file survived: %v", err)
	}
	if _, err := os.Stat(filepath.Join(hostDir, ".hidden")); err != nil {
		t.Fatalf(".hidden was not restored: %v", err)
	}
	calls := strings.Join(fake.calls(t), "\n")
	if !strings.Contains(calls, " stop app") || !strings.Contains(calls, " start app") {
		t.Fatalf("bind restore did not stop/start the consumer:\n%s", calls)
	}
	if strings.Contains(calls, "create --name dcraft-restore-") {
		t.Fatalf("bind restore used the volume helper:\n%s", calls)
	}
	if strings.Contains(calls, " stop worker") {
		t.Fatalf("already stopped worker was stopped:\n%s", calls)
	}
}

func TestRestoreBindCLIPathMismatchRequiresTheExplicitFlag(t *testing.T) {
	archivedDir := writeBindFixture(t)
	fake := backupFake(t)
	targetDir := t.TempDir()
	fake.writeConfig(t, backupBindConfig(targetDir))
	project := projectNamed(t, "backup-project")
	env := fake.env(t, t.TempDir())
	artifact := restoreBindArtifact(t, project, archivedDir)

	before := treeHash(t, targetDir)
	res := runProcess(t, project, env, strings.NewReader(""), "restore", "--path", project, "--json",
		"--backup", artifact, "--bind", targetDir, "--approve", "data_restore")
	if res.code != 1 {
		t.Fatalf("path mismatch exit = %d, want 1; stdout=%s", res.code, res.stdout)
	}
	parsed := parseRestore(t, res.stdout)
	if !hasRestoreCategory(parsed.Diagnostics, core.CategoryRestoreTargetMismatch) {
		t.Fatalf("path mismatch diagnostics = %+v", parsed.Diagnostics)
	}
	if calls := strings.Join(fake.calls(t), "\n"); strings.Contains(calls, " stop ") {
		t.Fatalf("path mismatch stopped services:\n%s", calls)
	}
	if after := treeHash(t, targetDir); after != before {
		t.Fatalf("path mismatch changed the target folder")
	}

	res = runProcess(t, project, env, strings.NewReader(""), "restore", "--path", project, "--json",
		"--backup", artifact, "--bind", targetDir, "--allow-path-mismatch", "--approve", "data_restore")
	if res.code != 0 {
		t.Fatalf("explicit path mapping exit = %d; stdout=%s stderr=%s", res.code, res.stdout, res.stderr)
	}
	parsed = parseRestore(t, res.stdout)
	report := parsed.Data.Restore
	if !report.PathMapping {
		t.Fatalf("path mapping not reported: %+v", report)
	}
	if !containsDiagnosticWarning(report.Warnings, "경로가 백업과 다름") {
		t.Fatalf("mapping warning missing: %v", report.Warnings)
	}
	if raw, err := os.ReadFile(filepath.Join(targetDir, "file.txt")); err != nil || string(raw) != "hello" {
		t.Fatalf("explicitly mapped restore content = %q, %v", raw, err)
	}
}

func TestRestoreBindCLIUndeclaredAndCorruptInputsFailBeforeStopping(t *testing.T) {
	hostDir := writeBindFixture(t)
	fake, project, env := bindRestoreFake(t, hostDir)
	artifact := restoreBindArtifact(t, project, hostDir)

	res := runProcess(t, project, env, strings.NewReader(""), "restore", "--path", project, "--json",
		"--backup", artifact, "--bind", hostDir)
	if res.code != 2 {
		t.Fatalf("missing approval exit = %d, want 2; stdout=%s", res.code, res.stdout)
	}
	parsed := parseRestore(t, res.stdout)
	if !hasRestoreCategory(parsed.Diagnostics, core.CategoryApprovalRequired) {
		t.Fatalf("missing approval diagnostics = %+v", parsed.Diagnostics)
	}
	if calls := strings.Join(fake.calls(t), "\n"); strings.Contains(calls, " stop ") {
		t.Fatalf("missing approval stopped services:\n%s", calls)
	}

	res = runProcess(t, project, env, strings.NewReader(""), "restore", "--path", project, "--json",
		"--backup", artifact, "--bind", filepath.Join(t.TempDir(), "not-declared"), "--approve", "data_restore")
	if res.code != 2 {
		t.Fatalf("undeclared target exit = %d, want 2; stdout=%s", res.code, res.stdout)
	}
	parsed = parseRestore(t, res.stdout)
	if !hasRestoreCategory(parsed.Diagnostics, core.CategoryRestoreBindUnknown) {
		t.Fatalf("undeclared target diagnostics = %+v", parsed.Diagnostics)
	}
	if calls := strings.Join(fake.calls(t), "\n"); strings.Contains(calls, " stop ") {
		t.Fatalf("undeclared target stopped services:\n%s", calls)
	}

	raw, err := os.ReadFile(filepath.Join(artifact, backup.DataFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(artifact, backup.DataFile), raw[:len(raw)-8], 0o600); err != nil {
		t.Fatal(err)
	}
	before := treeHash(t, hostDir)
	res = runProcess(t, project, env, strings.NewReader(""), "restore", "--path", project, "--json",
		"--backup", artifact, "--bind", hostDir, "--approve", "data_restore")
	if res.code != 1 {
		t.Fatalf("corrupt bind exit = %d, want 1; stdout=%s", res.code, res.stdout)
	}
	parsed = parseRestore(t, res.stdout)
	if !hasRestoreCategory(parsed.Diagnostics, core.CategoryRestoreIntegrityFailed) {
		t.Fatalf("corrupt bind diagnostics = %+v", parsed.Diagnostics)
	}
	if calls := strings.Join(fake.calls(t), "\n"); strings.Contains(calls, " stop ") {
		t.Fatalf("corrupt bind archive stopped services:\n%s", calls)
	}
	if after := treeHash(t, hostDir); after != before {
		t.Fatalf("corrupt archive changed the target folder")
	}
}

func TestRestoreBindTUIWizardSelectsABindArtifactAndTarget(t *testing.T) {
	hostDir := writeBindFixture(t)
	_, project, env := bindRestoreFake(t, hostDir)
	restoreBindArtifact(t, project, hostDir)

	output, code := runLifecyclePTYSteps(t, project, env, []lifecyclePTYStep{
		// The menu order is the registration order; compute the distance from
		// status so another ticket's feature does not break this journey.
		{wait: "마지막 조회", walk: "데이터 복원", send: "\r"},
		{wait: "복원할 백업 선택", send: "\r"},
		{wait: "복원 대상 선택", send: "down"},
		{wait: "복원 대상 선택", send: "down"},
		{wait: "복원 대상 선택", send: "\r"},
		{wait: "안전 백업 폴더", send: "\r"},
		{wait: "복원 계획", send: "\r"},
		{wait: "승인 확인", send: ""},
		{wait: "데이터 복원 (data_restore)", send: "\x1b[D"}, // the panel starts on cancel
		{wait: "▸ [ 승인 ]", send: "\r"},
		{wait: "복원 완료", send: ""},
	})
	if code != 0 {
		t.Fatalf("bind restore TUI exit = %d\n%s", code, output)
	}
	for _, want := range []string{"데이터 복원 설정", "볼륨과 연결 폴더 백업", "폴더", "복원 계획", "data_restore", "복원 완료"} {
		if !screenContains(output, want) {
			t.Fatalf("TUI output missing %q:\n%s", want, output)
		}
	}
}

func containsDiagnosticWarning(values []string, want string) bool {
	for _, value := range values {
		if strings.Contains(value, want) {
			return true
		}
	}
	return false
}
