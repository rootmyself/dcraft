package e2e

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rootmyself/dcraft/internal/backup"
	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/restore"
)

func restoreCompose(name string) string {
	return fmt.Sprintf(`name: %s
services:
  app:
    image: alpine:3.20
    command: ["sleep", "600"]
    volumes:
      - data:/data
  idle:
    image: alpine:3.20
    command: ["sleep", "600"]
    volumes:
      - data:/marker
volumes:
  data: {}
`, name)
}

func restoreComposeProject(t *testing.T, name string) (string, []string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(path, []byte(restoreCompose(name)), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, []string{path}
}

func seedRestoreV1(t *testing.T, env []string, volume string) {
	t.Helper()
	script := `set -e
mkdir -p /data/nested "/data/space dir"
printf 'v1' > /data/file.txt
printf 'dot-v1' > /data/.hidden
printf 'nested' > /data/nested/file.txt
printf 'korean' > "/data/한글 파일.txt"
printf 'space' > "/data/space dir/space name.txt"
ln -s nested/file.txt /data/link
ln /data/file.txt /data/hard
printf 'owned' > /data/owned.txt
chown 1000:2000 /data/owned.txt
chmod 640 /data/owned.txt
printf 'mode600' > /data/mode600.txt
chmod 600 /data/mode600.txt
mkdir /data/dir750
chown 1001:1001 /data/dir750
chmod 750 /data/dir750
printf 'suid' > /data/suidfile
chmod 4755 /data/suidfile
`
	backupComposeRun(t, env, t.TempDir(), "run", "--rm", "-v", volume+":/data", "alpine:3.20", "sh", "-c", script)
}

func mutateRestoreV2(t *testing.T, env []string, volume string) {
	t.Helper()
	script := `set -e
printf 'v2' > /data/file.txt
printf 'dot-v2' > /data/.hidden
printf 'new' > /data/new-v2.txt
mkdir -p /data/newdir
printf 'new' > /data/newdir/file.txt
rm -f /data/owned.txt
`
	backupComposeRun(t, env, t.TempDir(), "run", "--rm", "-v", volume+":/data", "alpine:3.20", "sh", "-c", script)
}

func restoreInodeFacts(t *testing.T, env []string, volume string) string {
	t.Helper()
	return backupComposeRun(t, env, t.TempDir(), "run", "--rm", "-v", volume+":/data:ro", "alpine:3.20",
		"sh", "-c", "stat -c '%i' /data/file.txt /data/hard")
}

func backupRestoreArtifact(t *testing.T, dir string, env []string) (string, string) {
	t.Helper()
	volume := ""
	_ = volume
	outDir := filepath.Join(t.TempDir(), "backups")
	res := runProcess(t, dir, buildEnv(map[string]string{"DCRAFT_STATE_DIR": t.TempDir()}), strings.NewReader(""),
		"backup", "--path", dir, "--json", "--volume", "data", "--output", outDir)
	if res.code != 0 {
		t.Fatalf("backup exit=%d stdout=%s stderr=%s", res.code, res.stdout, res.stderr)
	}
	parsed := parseBackup(t, res.stdout)
	if len(parsed.Data.Backup.Artifacts) != 1 {
		t.Fatalf("backup artifacts = %+v", parsed.Data.Backup.Artifacts)
	}
	return parsed.Data.Backup.Artifacts[0].Path, outDir
}

func TestIntegrationVolumeRestoreReplacesContentAndKeepsSafetyBackup(t *testing.T) {
	env := requireIntegration(t)
	name := "dcraft-test-rst-" + randomHex(t, 5)
	dir, files := restoreComposeProject(t, name)
	t.Cleanup(func() { cleanupBackupProject(t, env, dir, name, files) })
	cleanupBackupProject(t, env, dir, name, files)

	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	if out, err := composeCommand(ctx, env, dir, name, files, "up", "-d", "--wait", "--wait-timeout", "90").CombinedOutput(); err != nil {
		t.Fatalf("compose up: %v\n%s", err, out)
	}
	if out, err := composeCommand(ctx, env, dir, name, files, "stop", "idle").CombinedOutput(); err != nil {
		t.Fatalf("stop idle: %v\n%s", err, out)
	}
	volume := name + "_data"
	seedRestoreV1(t, env, volume)
	v1Facts := volumeFacts(t, env, volume)
	appID := containerID(t, name, "app")
	idleID := containerID(t, name, "idle")
	if appID == "" || idleID == "" {
		t.Fatalf("missing containers app=%s idle=%s", appID, idleID)
	}
	hookMarker := filepath.Join(t.TempDir(), "restore-hook-ran")
	hookScript := filepath.Join(dir, "restore-hook.sh")
	if err := os.WriteFile(hookScript, []byte("#!/bin/sh\ntouch '"+hookMarker+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".dcraft.yaml"), []byte("hooks:\n  - path: restore-hook.sh\n    stage: pre\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	artifact, _ := backupRestoreArtifact(t, dir, env)
	mutateRestoreV2(t, env, volume)
	v2Facts := volumeFacts(t, env, volume)
	if v1Facts == v2Facts {
		t.Fatal("v2 mutation did not change the volume")
	}

	// Another stack must stay untouched.
	otherName := "dcraft-test-rst-other-" + randomHex(t, 5)
	otherDir, otherFiles := restoreComposeProject(t, otherName)
	t.Cleanup(func() { cleanupBackupProject(t, env, otherDir, otherName, otherFiles) })
	cleanupBackupProject(t, env, otherDir, otherName, otherFiles)
	if out, err := composeCommand(ctx, env, otherDir, otherName, otherFiles, "up", "-d").CombinedOutput(); err != nil {
		t.Fatalf("compose up other: %v\n%s", err, out)
	}
	otherID := containerID(t, otherName, "app")
	otherVolume := otherName + "_data"
	backupComposeRun(t, env, t.TempDir(), "run", "--rm", "-v", otherVolume+":/data", "alpine:3.20",
		"sh", "-c", "printf 'other-keep' > /data/keep.txt")
	otherFacts := volumeFacts(t, env, otherVolume)

	safety := filepath.Join(t.TempDir(), "safety")
	dcraftEnv := buildEnv(map[string]string{"DCRAFT_STATE_DIR": t.TempDir()})
	res := runProcess(t, dir, dcraftEnv, strings.NewReader(""), "restore", "--path", dir, "--json",
		"--backup", artifact, "--safety-output", safety, "--approve", "data_restore")
	if res.code != 0 {
		t.Fatalf("restore exit=%d stdout=%s stderr=%s", res.code, res.stdout, res.stderr)
	}
	restored := parseRestore(t, res.stdout)
	if restored.Outcome != core.OutcomeSuccess || restored.Data.Restore.DataState != restore.DataReplaced {
		t.Fatalf("restore result = %+v", restored)
	}
	if got := volumeFacts(t, env, volume); got != v1Facts {
		t.Fatalf("restored content differs from v1:\n--- v1 ---\n%s\n--- restored ---\n%s", v1Facts, got)
	}
	if got := restoreInodeFacts(t, env, volume); len(strings.Fields(got)) != 2 || strings.Fields(got)[0] != strings.Fields(got)[1] {
		t.Fatalf("hardlink was not preserved after restore: %q", got)
	}
	for _, absent := range []string{"new-v2.txt", "newdir"} {
		out := backupComposeRun(t, env, t.TempDir(), "run", "--rm", "-v", volume+":/data:ro", "alpine:3.20",
			"sh", "-c", "[ ! -e /data/"+absent+" ] && echo absent || echo present")
		if out != "absent" {
			t.Fatalf("stale v2 entry survived restore: %s", absent)
		}
	}
	if got := backupComposeRun(t, env, t.TempDir(), "run", "--rm", "-v", volume+":/data:ro", "alpine:3.20",
		"sh", "-c", "[ -f /data/owned.txt ] && echo restored || echo missing"); got != "restored" {
		t.Fatalf("v1 file removed by v2 was not restored: %s", got)
	}
	if got := backupComposeRun(t, env, t.TempDir(), "run", "--rm", "-v", volume+":/data:ro", "alpine:3.20", "cat", "/data/.hidden"); got != "dot-v1" {
		t.Fatalf("dotfile content = %q", got)
	}

	// The safety backup is complete, verifiable and contains v2.
	report := restored.Data.Restore
	if report.SafetyBackup == "" {
		t.Fatal("safety backup path missing")
	}
	if _, err := backup.Verify(report.SafetyBackup); err != nil {
		t.Fatalf("safety backup did not verify: %v", err)
	}
	checkVolume := name + "-safety-check-" + randomHex(t, 5)
	t.Cleanup(func() { _ = exec.Command("docker", "volume", "rm", checkVolume).Run() })
	backupComposeRun(t, env, t.TempDir(), "volume", "create", checkVolume)
	extractArtifactToVolume(t, env, report.SafetyBackup, checkVolume)
	if got := volumeFacts(t, env, checkVolume); got != v2Facts {
		t.Fatalf("safety backup content differs from v2:\n--- v2 ---\n%s\n--- safety ---\n%s", v2Facts, got)
	}

	// Only the previously running service returns; container identities stay.
	if got := serviceState(t, appID); got != "running" {
		t.Fatalf("app state = %s", got)
	}
	if got := serviceState(t, idleID); got != "exited" {
		t.Fatalf("idle state = %s", got)
	}
	if after := containerID(t, name, "app"); after != appID {
		t.Fatalf("app container recreated: %s -> %s", appID, after)
	}
	if after := containerID(t, name, "idle"); after != idleID {
		t.Fatalf("idle container recreated: %s -> %s", idleID, after)
	}
	if got := serviceState(t, otherID); got != "running" {
		t.Fatalf("other stack container changed: %s", got)
	}
	if got := volumeFacts(t, env, otherVolume); got != otherFacts {
		t.Fatalf("other stack volume changed:\n%s", got)
	}
	if _, err := os.Stat(hookMarker); !os.IsNotExist(err) {
		t.Fatalf("restore executed a project hook: %v", err)
	}
}

func TestIntegrationVolumeRestoreRefusesCorruptArtifactAndSafetyFailure(t *testing.T) {
	env := requireIntegration(t)
	name := "dcraft-test-rst-" + randomHex(t, 5)
	dir, files := restoreComposeProject(t, name)
	t.Cleanup(func() { cleanupBackupProject(t, env, dir, name, files) })
	cleanupBackupProject(t, env, dir, name, files)
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	if out, err := composeCommand(ctx, env, dir, name, files, "up", "-d", "--wait", "--wait-timeout", "90").CombinedOutput(); err != nil {
		t.Fatalf("compose up: %v\n%s", err, out)
	}
	volume := name + "_data"
	seedRestoreV1(t, env, volume)
	v1Facts := volumeFacts(t, env, volume)
	appID := containerID(t, name, "app")
	artifact, _ := backupRestoreArtifact(t, dir, env)

	raw, err := os.ReadFile(filepath.Join(artifact, backup.DataFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(artifact, backup.DataFile), raw[:len(raw)-8], 0o600); err != nil {
		t.Fatal(err)
	}
	dcraftEnv := buildEnv(map[string]string{"DCRAFT_STATE_DIR": t.TempDir()})
	res := runProcess(t, dir, dcraftEnv, strings.NewReader(""), "restore", "--path", dir, "--json",
		"--backup", artifact, "--approve", "data_restore")
	if res.code != 1 {
		t.Fatalf("corrupt restore exit=%d stdout=%s", res.code, res.stdout)
	}
	parsed := parseRestore(t, res.stdout)
	if !hasRestoreCategory(parsed.Diagnostics, core.CategoryRestoreIntegrityFailed) {
		t.Fatalf("corrupt diagnostics = %+v", parsed.Diagnostics)
	}
	if got := volumeFacts(t, env, volume); got != v1Facts {
		t.Fatalf("corrupt artifact changed the volume:\n%s", got)
	}
	if got := serviceState(t, appID); got != "running" {
		t.Fatalf("corrupt artifact stopped app: %s", got)
	}
	if after := containerID(t, name, "app"); after != appID {
		t.Fatalf("corrupt artifact changed the container: %s -> %s", appID, after)
	}

	artifact, _ = backupRestoreArtifact(t, dir, env)
	blocked := filepath.Join(t.TempDir(), "blocked")
	if err := os.Mkdir(blocked, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(blocked, 0o700) })
	res = runProcess(t, dir, dcraftEnv, strings.NewReader(""), "restore", "--path", dir, "--json",
		"--backup", artifact, "--safety-output", blocked, "--approve", "data_restore")
	if res.code != 1 {
		t.Fatalf("blocked safety exit=%d stdout=%s", res.code, res.stdout)
	}
	parsed = parseRestore(t, res.stdout)
	if !hasRestoreCategory(parsed.Diagnostics, core.CategoryRestoreSafetyFailed) {
		t.Fatalf("blocked safety diagnostics = %+v", parsed.Diagnostics)
	}
	if got := volumeFacts(t, env, volume); got != v1Facts {
		t.Fatalf("blocked safety changed the volume:\n%s", got)
	}
	if got := serviceState(t, appID); got != "running" {
		t.Fatalf("blocked safety stopped app: %s", got)
	}
}

func TestIntegrationVolumeRestoreInterruptionReportsAndRecovers(t *testing.T) {
	env := requireIntegration(t)
	name := "dcraft-test-rst-" + randomHex(t, 5)
	dir, files := restoreComposeProject(t, name)
	t.Cleanup(func() { cleanupBackupProject(t, env, dir, name, files) })
	cleanupBackupProject(t, env, dir, name, files)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	if out, err := composeCommand(ctx, env, dir, name, files, "up", "-d", "--wait", "--wait-timeout", "90").CombinedOutput(); err != nil {
		t.Fatalf("compose up: %v\n%s", err, out)
	}
	volume := name + "_data"
	seedRestoreV1(t, env, volume)
	v1Facts := volumeFacts(t, env, volume)
	appID := containerID(t, name, "app")
	artifact, _ := backupRestoreArtifact(t, dir, env)
	mutateRestoreV2(t, env, volume)

	slowImage := buildBackupHelperImage(t, "slow")
	safety := filepath.Join(t.TempDir(), "safety")
	stateDir := t.TempDir()
	dcraftEnv := buildEnv(map[string]string{
		"DCRAFT_STATE_DIR":    stateDir,
		"DCRAFT_BACKUP_IMAGE": slowImage,
	})
	cmd := exec.Command(binaryPath, "restore", "--path", dir, "--json",
		"--backup", artifact, "--safety-output", safety, "--approve", "data_restore")
	cmd.Dir = dir
	cmd.Env = dcraftEnv
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// Wait until the completed safety artifact is published. Only then has
	// the replace stage begun; a probe container before that would be a
	// legitimate outside consumer and the restore must refuse it.
	safetyDeadline := time.Now().Add(60 * time.Second)
	for {
		if artifacts, err := backup.List(safety); err == nil && len(artifacts) == 1 {
			break
		}
		if time.Now().After(safetyDeadline) {
			_ = cmd.Process.Kill()
			t.Fatalf("restore never published the safety backup; stdout=%s stderr=%s", stdout.String(), stderr.String())
		}
		time.Sleep(200 * time.Millisecond)
	}
	// The target becomes empty after the replace stage starts. SIGINT during
	// the slow extraction leaves a partial (or empty) volume, never a
	// success.
	deadline := time.Now().Add(60 * time.Second)
	for {
		out := backupComposeRun(t, env, t.TempDir(), "run", "--rm", "-v", volume+":/data:ro", "alpine:3.20",
			"sh", "-c", "[ -e /data/file.txt ] && echo present || echo gone")
		if out == "gone" {
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			t.Fatalf("restore never emptied the volume; stdout=%s stderr=%s", stdout.String(), stderr.String())
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		_ = cmd.Process.Kill()
		t.Fatal(err)
	}
	err := cmd.Wait()
	if err == nil {
		t.Fatalf("interrupted restore reported success; stdout=%s", stdout.String())
	}
	var exitErr *exec.ExitError
	if !errorAs(err, &exitErr) || exitErr.ExitCode() != 130 {
		t.Fatalf("interrupted restore exit = %v, want 130; stdout=%s stderr=%s", err, stdout.String(), stderr.String())
	}
	parsed := parseRestore(t, stdout.String())
	if parsed.Outcome != core.OutcomeCanceled {
		t.Fatalf("interrupted outcome = %q", parsed.Outcome)
	}
	var canceled *core.Diagnostic
	for i := range parsed.Diagnostics {
		if parsed.Diagnostics[i].Category == core.CategoryCanceled {
			canceled = &parsed.Diagnostics[i]
		}
	}
	if canceled == nil {
		t.Fatalf("canceled diagnostic missing: %+v", parsed.Diagnostics)
	}
	for _, want := range []string{"replace", string(restore.DataUncertain), safety} {
		if !strings.Contains(canceled.Message, want) {
			t.Fatalf("canceled message missing %q: %s", want, canceled.Message)
		}
	}
	if got := serviceState(t, appID); got != "exited" {
		t.Fatalf("app restarted with uncertain data: %s", got)
	}
	// The safety backup is still a valid v1 artifact and contains v2.
	refs, err := os.ReadDir(safety)
	if err != nil || len(refs) != 1 {
		t.Fatalf("safety backup entries = %v, err=%v", refs, err)
	}
	safetyPath := filepath.Join(safety, refs[0].Name())
	if _, err := backup.Verify(safetyPath); err != nil {
		t.Fatalf("interrupted safety backup did not verify: %v", err)
	}
	assertNoRestoreHelpers(t)

	// A normal restore of the original artifact recovers the v1 state. The
	// interrupted operation deliberately left the service stopped because
	// integrity was unknown; start it again first so this second run has the
	// same prior state to restore.
	if out, err := composeCommand(ctx, env, dir, name, files, "start", "app").CombinedOutput(); err != nil {
		t.Fatalf("start app before recovery: %v\n%s", err, out)
	}
	res := runProcess(t, dir, buildEnv(map[string]string{"DCRAFT_STATE_DIR": stateDir}), strings.NewReader(""),
		"restore", "--path", dir, "--json", "--backup", artifact,
		"--safety-output", filepath.Join(t.TempDir(), "safety2"), "--approve", "data_restore")
	if res.code != 0 {
		t.Fatalf("recovery restore exit=%d stdout=%s stderr=%s", res.code, res.stdout, res.stderr)
	}
	if got := volumeFacts(t, env, volume); got != v1Facts {
		t.Fatalf("recovery restore content differs from v1:\n%s", got)
	}
	if got := serviceState(t, appID); got != "running" {
		t.Fatalf("app not running after recovery: %s", got)
	}
	assertNoRestoreHelpers(t)
}

func assertNoRestoreHelpers(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		out, err := exec.Command("docker", "ps", "-a", "--filter", "label=dcraft.restore=1", "--format", "{{.Names}}").Output()
		if err != nil {
			t.Fatalf("list restore helpers: %v", err)
		}
		names := strings.TrimSpace(string(out))
		if names == "" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("restore helper containers survived: %s", names)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
