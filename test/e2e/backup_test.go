package e2e

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/backup"
	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/state"
)

const backupConfig = `{
  "name": "backup-test",
  "services": {
    "app": {"volumes": [
      {"type": "volume", "source": "data", "target": "/data", "volume": {}},
      {"type": "bind", "source": "/host/bind", "target": "/bind", "bind": {}}
    ]},
    "worker": {"volumes": [
      {"type": "volume", "source": "data", "target": "/work", "volume": {}}
    ]}
  },
  "environment": {"APP_PASSWORD": "SUPER_SECRET_VALUE"},
  "volumes": {
    "data": {"name": "backup-test_data", "external": false}
  }
}`

const backupPS = `{"Name":"backup-test-app-1","Service":"app","State":"running","ExitCode":0}
{"Name":"backup-test-worker-1","Service":"worker","State":"exited","ExitCode":137}
{"Name":"backup-test-idle-1","Service":"idle","State":"exited","ExitCode":0}`

type backupResult struct {
	SchemaVersion int               `json:"schema_version"`
	Action        string            `json:"action"`
	Outcome       core.Outcome      `json:"outcome"`
	Diagnostics   []core.Diagnostic `json:"diagnostics"`
	Data          struct {
		Backup backup.Report `json:"backup"`
	} `json:"data"`
}

func parseBackup(t *testing.T, out string) backupResult {
	t.Helper()
	var res backupResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("parse backup JSON: %v\n%s", err, out)
	}
	return res
}

func backupFake(t *testing.T) *fakeDocker {
	t.Helper()
	f := newFakeDocker(t)
	f.writeConfig(t, backupConfig)
	f.writePS(t, backupPS)
	f.writeConsumers(t, "backup-test-app-1\tbackup-test\tapp\trunning\n")
	return f
}

func TestBackupWithoutExplicitInputFailsBeforeStoppingAnything(t *testing.T) {
	fake := backupFake(t)
	project := projectNamed(t, "backup-project")
	stateDir := t.TempDir()
	env := fake.env(t, stateDir)

	res := runProcess(t, project, env, strings.NewReader(""), "backup", "--path", project, "--json", "--volume", "data")
	if res.code != 2 {
		t.Fatalf("exit = %d, want 2; stderr=%q", res.code, res.stderr)
	}
	parsed := parseBackup(t, res.stdout)
	if parsed.Outcome != core.OutcomeFailure || !hasBackupCategory(parsed.Diagnostics, core.CategoryBackupOutputRequired) {
		t.Fatalf("result = %+v", parsed)
	}
	if !strings.Contains(parsed.Diagnostics[0].Message, "저장 위치") {
		t.Fatalf("Korean guidance missing: %+v", parsed.Diagnostics[0])
	}
	calls := strings.Join(fake.calls(t), "\n")
	if strings.Contains(calls, " stop ") || strings.Contains(calls, "run --rm") {
		t.Fatalf("missing output still stopped/copied:\n%s", calls)
	}
}

func TestBackupUnknownVolumeFailsWithAUsageError(t *testing.T) {
	fake := backupFake(t)
	project := projectNamed(t, "backup-project")
	stateDir := t.TempDir()
	env := fake.env(t, stateDir)
	outDir := filepath.Join(t.TempDir(), "backups")

	res := runProcess(t, project, env, strings.NewReader(""), "backup", "--path", project, "--json",
		"--volume", "nope", "--output", outDir)
	if res.code != 2 {
		t.Fatalf("exit = %d, want 2; stderr=%q", res.code, res.stderr)
	}
	parsed := parseBackup(t, res.stdout)
	if !hasBackupCategory(parsed.Diagnostics, core.CategoryBackupVolumeUnknown) {
		t.Fatalf("diagnostics = %+v", parsed.Diagnostics)
	}
	if calls := strings.Join(fake.calls(t), "\n"); strings.Contains(calls, " stop ") {
		t.Fatalf("unknown volume stopped services:\n%s", calls)
	}
}

func TestBackupCLIStopsOnlyRunningConsumersPublishesAndRestarts(t *testing.T) {
	fake := backupFake(t)
	project := projectNamed(t, "backup-project")
	stateDir := t.TempDir()
	env := fake.env(t, stateDir)
	outDir := filepath.Join(t.TempDir(), "backups")

	res := runProcess(t, project, env, strings.NewReader(""), "backup", "--path", project, "--json",
		"--volume", "data", "--output", outDir)
	if res.code != 0 || res.stderr != "" {
		t.Fatalf("exit=%d stderr=%q stdout=%q", res.code, res.stderr, res.stdout)
	}
	if strings.Contains(res.stdout, "SUPER_SECRET_VALUE") {
		t.Fatalf("JSON output leaked a project environment value: %s", res.stdout)
	}
	parsed := parseBackup(t, res.stdout)
	if parsed.Outcome != core.OutcomeSuccess || parsed.Action != "backup" {
		t.Fatalf("result = %+v", parsed)
	}
	report := parsed.Data.Backup
	if report.ProjectName != "backup-test" {
		t.Fatalf("project name = %q", report.ProjectName)
	}
	if strings.Join(report.AffectedServices, ",") != "app,worker" {
		t.Fatalf("affected services = %v", report.AffectedServices)
	}
	if strings.Join(report.RunningBefore, ",") != "app" {
		t.Fatalf("running before = %v", report.RunningBefore)
	}
	if strings.Join(report.Stopped, ",") != "app" || strings.Join(report.Restarted, ",") != "app" {
		t.Fatalf("stopped/restarted = %v/%v", report.Stopped, report.Restarted)
	}
	if len(report.Artifacts) != 1 || !report.Artifacts[0].Complete {
		t.Fatalf("artifacts = %+v", report.Artifacts)
	}
	artifact := report.Artifacts[0]
	resolvedOut, err := filepath.EvalSymlinks(outDir)
	if err != nil {
		t.Fatal(err)
	}
	if artifact.SizeBytes == 0 || artifact.Entries == 0 || !strings.HasPrefix(artifact.Path, resolvedOut) {
		t.Fatalf("artifact = %+v", artifact)
	}
	verified, err := backup.Verify(artifact.Path)
	if err != nil {
		t.Fatalf("published manifest did not verify: %v", err)
	}
	if verified.Source.VolumeName != "backup-test_data" || !verified.Complete {
		t.Fatalf("manifest = %+v", verified)
	}
	if !report.SensitiveData || !containsNotice(report.Notices, "비밀번호") {
		t.Fatalf("sensitive notice missing: %+v", report.Notices)
	}

	calls := fake.calls(t)
	log := strings.Join(calls, "\n")
	stopAt, startAt, archiveAt := -1, -1, -1
	for i, line := range calls {
		if strings.Contains(line, " stop app") {
			stopAt = i
		}
		if strings.Contains(line, "run --rm --name dcraft-backup-") {
			archiveAt = i
		}
		if strings.Contains(line, " start app") {
			startAt = i
		}
	}
	if stopAt < 0 || archiveAt < 0 || startAt < 0 || !(stopAt < archiveAt && archiveAt < startAt) {
		t.Fatalf("stop/archive/restart order wrong (%d/%d/%d):\n%s", stopAt, archiveAt, startAt, log)
	}
	if !strings.Contains(log, "ps -a --filter volume=backup-test_data") {
		t.Fatalf("consumer query did not use the actual engine name:\n%s", log)
	}
	if !strings.Contains(log, "-v backup-test_data:/data:ro") {
		t.Fatalf("archive did not mount the volume read-only:\n%s", log)
	}
	if strings.Contains(log, " down ") || strings.Contains(log, " -v backup-test_data -v") {
		t.Fatalf("backup removed resources:\n%s", log)
	}
}

func TestBackupRefusesAnotherStacksConsumerWithoutStoppingIt(t *testing.T) {
	fake := backupFake(t)
	fake.writeConsumers(t, "other-stack-app-1\tother-stack\tapp\trunning\nbackup-test-app-1\tbackup-test\tapp\trunning\n")
	project := projectNamed(t, "backup-project")
	stateDir := t.TempDir()
	env := fake.env(t, stateDir)
	outDir := filepath.Join(t.TempDir(), "backups")

	res := runProcess(t, project, env, strings.NewReader(""), "backup", "--path", project, "--json",
		"--volume", "data", "--output", outDir)
	if res.code != 1 {
		t.Fatalf("exit = %d, want 1; stdout=%q", res.code, res.stdout)
	}
	parsed := parseBackup(t, res.stdout)
	if !hasBackupCategory(parsed.Diagnostics, core.CategoryBackupConsumerUnsafe) {
		t.Fatalf("diagnostics = %+v", parsed.Diagnostics)
	}
	calls := strings.Join(fake.calls(t), "\n")
	if strings.Contains(calls, " stop ") || strings.Contains(calls, "start ") || strings.Contains(calls, "run --rm") {
		t.Fatalf("another stack's consumer caused a change:\n%s", calls)
	}
	if entries, _ := os.ReadDir(outDir); len(entries) != 0 {
		t.Fatalf("artifact was created despite refusal: %v", entries)
	}
	if !strings.Contains(res.stdout, "다른 스택") {
		t.Fatalf("Korean refusal missing: %s", res.stdout)
	}
}

func TestBackupFailsClearlyWhenTheStackIsBusy(t *testing.T) {
	fake := backupFake(t)
	project := projectNamed(t, "backup-project")
	stateDir := t.TempDir()
	env := fake.env(t, stateDir)
	outDir := filepath.Join(t.TempDir(), "backups")
	resolved, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}
	store := state.New(stateDir)
	lock, err := store.AcquireStackLock(context.Background(), core.Target{ProjectDir: resolved, Files: []string{filepath.Join(resolved, "compose.yaml")}}, state.LockHolder{Action: "up", PID: 123})
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()

	res := runProcess(t, project, env, strings.NewReader(""), "backup", "--path", project, "--json",
		"--volume", "data", "--output", outDir)
	if res.code != 1 {
		t.Fatalf("exit = %d, want 1; stdout=%q", res.code, res.stdout)
	}
	parsed := parseBackup(t, res.stdout)
	if !hasBackupCategory(parsed.Diagnostics, core.CategoryStackBusy) {
		t.Fatalf("diagnostics = %+v", parsed.Diagnostics)
	}
	if calls := strings.Join(fake.calls(t), "\n"); strings.Contains(calls, " stop ") || strings.Contains(calls, "run --rm") {
		t.Fatalf("busy stack was changed:\n%s", calls)
	}
}

func TestBackupReadFailureDoesNotPublishAndRestoresServices(t *testing.T) {
	fake := backupFake(t)
	project := projectNamed(t, "backup-project")
	stateDir := t.TempDir()
	env := fake.envExtra(t, stateDir, map[string]string{"DOCKER_FAKE_ARCHIVE_FAIL": "Permission denied"})
	outDir := filepath.Join(t.TempDir(), "backups")

	res := runProcess(t, project, env, strings.NewReader(""), "backup", "--path", project, "--json",
		"--volume", "data", "--output", outDir)
	if res.code != 1 {
		t.Fatalf("exit = %d, want 1; stdout=%q", res.code, res.stdout)
	}
	parsed := parseBackup(t, res.stdout)
	if !hasBackupCategory(parsed.Diagnostics, core.CategoryBackupFailed) {
		t.Fatalf("diagnostics = %+v", parsed.Diagnostics)
	}
	if !strings.Contains(res.stdout, "Permission denied") {
		t.Fatalf("tool output missing from the diagnostic: %s", res.stdout)
	}
	calls := fake.calls(t)
	log := strings.Join(calls, "\n")
	if !strings.Contains(log, " stop app") || !strings.Contains(log, " start app") {
		t.Fatalf("services were not restored after the failure:\n%s", log)
	}
	entries, readErr := os.ReadDir(outDir)
	if readErr != nil {
		t.Fatal(readErr)
	}
	for _, entry := range entries {
		if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") && !strings.HasSuffix(entry.Name(), ".partial") {
			t.Fatalf("failed backup was published: %v", entries)
		}
	}
	artifacts, err := backup.List(outDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 0 {
		t.Fatalf("failed backup was listed as valid: %+v", artifacts)
	}
}

func TestRepeatedBackupsBothRemain(t *testing.T) {
	fake := backupFake(t)
	project := projectNamed(t, "backup-project")
	stateDir := t.TempDir()
	env := fake.env(t, stateDir)
	outDir := filepath.Join(t.TempDir(), "backups")
	for i := 0; i < 2; i++ {
		res := runProcess(t, project, env, strings.NewReader(""), "backup", "--path", project, "--json",
			"--volume", "data", "--output", outDir)
		if res.code != 0 {
			t.Fatalf("run %d exit=%d stdout=%q stderr=%q", i, res.code, res.stdout, res.stderr)
		}
	}
	artifacts, err := backup.List(outDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 2 {
		t.Fatalf("artifacts = %d, want 2: %+v", len(artifacts), artifacts)
	}
}

func hasBackupCategory(diags []core.Diagnostic, category core.Category) bool {
	for _, d := range diags {
		if d.Category == category {
			return true
		}
	}
	return false
}

func containsNotice(notices []string, substring string) bool {
	for _, notice := range notices {
		if strings.Contains(notice, substring) {
			return true
		}
	}
	return false
}
