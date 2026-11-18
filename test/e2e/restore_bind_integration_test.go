package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rootmyself/dcraft/internal/backup"
	"github.com/rootmyself/dcraft/internal/core"
)

func bindRestoreCompose(name, hostDir string) string {
	return fmt.Sprintf(`name: %s
services:
  app:
    image: alpine:3.20
    command: ["sleep", "600"]
    volumes:
      - %s:/data
  worker:
    image: alpine:3.20
    command: ["sleep", "600"]
    volumes:
      - %s:/data
  idle:
    image: alpine:3.20
    command: ["sleep", "600"]
    volumes:
      - %s:/marker
`, name, hostDir, hostDir, hostDir)
}

func bindRestoreProject(t *testing.T, name, hostDir string) (string, []string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(path, []byte(bindRestoreCompose(name, hostDir)), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, []string{path}
}

func integrationBindArtifact(t *testing.T, project, hostDir string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(hostDir)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "artifact")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := backup.ArchiveHostDir(context.Background(), resolved, filepath.Join(dir, backup.DataFile)); err != nil {
		t.Fatalf("ArchiveHostDir: %v", err)
	}
	if _, err := backup.BuildArtifact(dir, backup.Source{
		Kind: backup.KindBind, Project: project, HostPath: resolved,
		MountTargets: []string{"/data", "/marker"}, Services: []string{"app", "worker", "idle"},
	}, backup.StackState{ProjectName: project}, backup.Tool{Version: "test"}, time.Unix(1700000000, 0)); err != nil {
		t.Fatalf("BuildArtifact: %v", err)
	}
	return dir
}

func bindBackupArtifact(t *testing.T, projectDir, hostDir string, env []string) string {
	t.Helper()
	output := filepath.Join(t.TempDir(), "backups")
	res := runProcess(t, projectDir, env, strings.NewReader(""), "backup", "--path", projectDir, "--json",
		"--bind", hostDir, "--output", output)
	if res.code != 0 {
		t.Fatalf("bind backup exit=%d stdout=%s stderr=%s", res.code, res.stdout, res.stderr)
	}
	report := parseBackup(t, res.stdout).Data.Backup
	if len(report.Artifacts) != 1 {
		t.Fatalf("bind backup artifacts = %+v", report.Artifacts)
	}
	if _, err := backup.Verify(report.Artifacts[0].Path); err != nil {
		t.Fatalf("bind backup artifact did not verify: %v", err)
	}
	return report.Artifacts[0].Path
}

func bindServicesRunning(t *testing.T, project string, services ...string) {
	t.Helper()
	for _, service := range services {
		if got := serviceState(t, containerID(t, project, service)); got != "running" {
			t.Fatalf("%s state = %s, want running", service, got)
		}
	}
}

func bindHashBytes(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func TestIntegrationBindRestoreReplacesContentAndPreservesDocumentedFidelity(t *testing.T) {
	env := requireIntegration(t)
	name := "dcraft-test-bind-restore-" + randomHex(t, 5)
	hostDir := seedBindHostDir(t)
	if err := os.WriteFile(filepath.Join(hostDir, "run.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(hostDir, "run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	dir, files := bindRestoreProject(t, name, hostDir)
	dcraftEnv := buildEnv(map[string]string{"DCRAFT_STATE_DIR": t.TempDir()})
	t.Cleanup(func() { cleanupBackupProject(t, env, dir, name, files) })
	cleanupBackupProject(t, env, dir, name, files)
	bindWaiter(t, env, dir, name, files)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if out, err := composeCommand(ctx, env, dir, name, files, "stop", "idle").CombinedOutput(); err != nil {
		t.Fatalf("stop idle: %v\n%s", err, out)
	}
	ids := map[string]string{
		"app":    containerID(t, name, "app"),
		"worker": containerID(t, name, "worker"),
		"idle":   containerID(t, name, "idle"),
	}
	v1Hash := treeHash(t, hostDir)
	artifact := bindBackupArtifact(t, dir, hostDir, dcraftEnv)

	// v2: changed content, a deleted dotfile, a new stale file, a stale symlink
	// and a stale hardlink. Restore must replace, not merge.
	if err := os.WriteFile(filepath.Join(hostDir, "file.txt"), []byte("version two"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(hostDir, ".hidden")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hostDir, "stale.txt"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("file.txt", filepath.Join(hostDir, "v2-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(hostDir, "file.txt"), filepath.Join(hostDir, "v2-hard")); err != nil {
		t.Fatal(err)
	}
	v2File := []byte("version two")
	v2Hash := bindHashBytes(v2File)
	canary := seedBindHostDir(t)
	canaryHash := treeHash(t, canary)
	marker := filepath.Join(t.TempDir(), "hook-ran")
	if err := os.WriteFile(filepath.Join(dir, "pre.sh"), []byte("#!/bin/sh\ntouch '"+marker+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".dcraft.yaml"), []byte("hooks:\n  - path: pre.sh\n    stage: pre\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := runProcess(t, dir, dcraftEnv, strings.NewReader(""), "restore", "--path", dir, "--json",
		"--backup", artifact, "--bind", hostDir, "--approve", "data_restore")
	if res.code != 0 {
		t.Fatalf("bind restore exit=%d stdout=%s stderr=%s", res.code, res.stdout, res.stderr)
	}
	report := parseRestore(t, res.stdout).Data.Restore
	if report.DataState != "replaced_verified" || report.TargetKind != "bind" {
		t.Fatalf("report = %+v", report)
	}
	if strings.Join(report.Stopped, ",") != "app,worker" || strings.Join(report.Restarted, ",") != "app,worker" {
		t.Fatalf("services stopped=%v restarted=%v", report.Stopped, report.Restarted)
	}
	if after := treeHash(t, hostDir); after != v1Hash {
		t.Fatalf("restored tree differs from v1:\nbefore=%s\nafter=%s", v1Hash, after)
	}
	for _, stale := range []string{"stale.txt", "v2-link", "v2-hard"} {
		if _, err := os.Lstat(filepath.Join(hostDir, stale)); !os.IsNotExist(err) {
			t.Fatalf("stale v2 entry %q survived: %v", stale, err)
		}
	}
	fileInfo, err := os.Stat(filepath.Join(hostDir, "file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	hardInfo, err := os.Stat(filepath.Join(hostDir, "hard"))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(fileInfo, hardInfo) {
		t.Fatal("hardlink identity was not preserved")
	}
	linkTarget, err := os.Readlink(filepath.Join(hostDir, "link"))
	if err != nil || linkTarget != "file.txt" {
		t.Fatalf("symlink = %q, %v", linkTarget, err)
	}
	if info, err := os.Stat(filepath.Join(hostDir, "run.sh")); err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("executable mode = %v, %v", info, err)
	}

	safety, err := backup.Verify(report.SafetyBackup)
	if err != nil {
		t.Fatalf("safety backup did not verify: %v", err)
	}
	if safety.Source.Kind != backup.KindBind {
		t.Fatalf("safety source = %+v", safety.Source)
	}
	safetyEntries := map[string]backup.Entry{}
	for _, entry := range safety.Entries {
		safetyEntries[entry.Path] = entry
	}
	if entry, ok := safetyEntries["./stale.txt"]; !ok || entry.SHA256 != bindHashBytes([]byte("stale")) {
		t.Fatalf("safety backup does not contain the v2 stale file: %+v", entry)
	}
	if entry, ok := safetyEntries["./file.txt"]; !ok || entry.SHA256 != v2Hash {
		t.Fatalf("safety backup does not contain the v2 content: %+v", entry)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("restore executed a project hook: %v", err)
	}
	if after := treeHash(t, canary); after != canaryHash {
		t.Fatalf("an unrelated bind folder changed:\nbefore=%s\nafter=%s", canaryHash, after)
	}
	for service, id := range ids {
		if after := containerID(t, name, service); after != id {
			t.Fatalf("%s container was recreated: %s -> %s", service, id, after)
		}
		state := serviceState(t, id)
		if service == "idle" {
			if state != "exited" {
				t.Fatalf("already stopped idle was started: %s", state)
			}
		} else if state != "running" {
			t.Fatalf("%s state = %s, want running", service, state)
		}
	}
}

func TestIntegrationBindRestorePathMismatchNeedsTheExplicitFlag(t *testing.T) {
	env := requireIntegration(t)
	name := "dcraft-test-bind-restore-" + randomHex(t, 5)
	archivedDir := seedBindHostDir(t)
	archivedHash := treeHash(t, archivedDir)
	currentDir := seedBindHostDir(t)
	if err := os.WriteFile(filepath.Join(currentDir, "current-only.txt"), []byte("current"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir, files := bindRestoreProject(t, name, currentDir)
	dcraftEnv := buildEnv(map[string]string{"DCRAFT_STATE_DIR": t.TempDir()})
	t.Cleanup(func() { cleanupBackupProject(t, env, dir, name, files) })
	cleanupBackupProject(t, env, dir, name, files)
	bindWaiter(t, env, dir, name, files)

	ids := map[string]string{
		"app":    containerID(t, name, "app"),
		"worker": containerID(t, name, "worker"),
	}
	before := treeHash(t, currentDir)
	artifact := integrationBindArtifact(t, name, archivedDir)

	res := runProcess(t, dir, dcraftEnv, strings.NewReader(""), "restore", "--path", dir, "--json",
		"--backup", artifact, "--bind", currentDir, "--approve", "data_restore")
	if res.code != 1 {
		t.Fatalf("path mismatch exit=%d stdout=%s stderr=%s", res.code, res.stdout, res.stderr)
	}
	parsed := parseRestore(t, res.stdout)
	if !hasRestoreCategory(parsed.Diagnostics, core.CategoryRestoreTargetMismatch) {
		t.Fatalf("path mismatch diagnostics = %+v", parsed.Diagnostics)
	}
	if after := treeHash(t, currentDir); after != before {
		t.Fatal("path mismatch changed the target folder")
	}
	for service, id := range ids {
		if after := containerID(t, name, service); after != id {
			t.Fatalf("%s container changed during refusal", service)
		}
		bindServicesRunning(t, name, service)
	}

	res = runProcess(t, dir, dcraftEnv, strings.NewReader(""), "restore", "--path", dir, "--json",
		"--backup", artifact, "--bind", currentDir, "--allow-path-mismatch", "--approve", "data_restore")
	if res.code != 0 {
		t.Fatalf("explicit path mapping exit=%d stdout=%s stderr=%s", res.code, res.stdout, res.stderr)
	}
	report := parseRestore(t, res.stdout).Data.Restore
	if !report.PathMapping || len(report.Warnings) == 0 {
		t.Fatalf("explicit path mapping was not reported: %+v", report)
	}
	if after := treeHash(t, currentDir); after != archivedHash {
		t.Fatalf("explicit mapping did not restore the archived content:\nbefore=%s\nafter=%s", archivedHash, after)
	}
	if _, err := os.Stat(filepath.Join(currentDir, "current-only.txt")); !os.IsNotExist(err) {
		t.Fatal("current-only file survived the replacement")
	}
	bindServicesRunning(t, name, "app", "worker")
}

func TestIntegrationBindRestoreRefusesCorruptArchivesBeforeStopping(t *testing.T) {
	env := requireIntegration(t)
	name := "dcraft-test-bind-restore-" + randomHex(t, 5)
	hostDir := seedBindHostDir(t)
	dir, files := bindRestoreProject(t, name, hostDir)
	dcraftEnv := buildEnv(map[string]string{"DCRAFT_STATE_DIR": t.TempDir()})
	t.Cleanup(func() { cleanupBackupProject(t, env, dir, name, files) })
	cleanupBackupProject(t, env, dir, name, files)
	bindWaiter(t, env, dir, name, files)
	beforeHash := treeHash(t, hostDir)
	ids := map[string]string{
		"app":    containerID(t, name, "app"),
		"worker": containerID(t, name, "worker"),
	}
	artifact := bindBackupArtifact(t, dir, hostDir, dcraftEnv)
	raw, err := os.ReadFile(filepath.Join(artifact, backup.DataFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(artifact, backup.DataFile), raw[:len(raw)-8], 0o600); err != nil {
		t.Fatal(err)
	}

	res := runProcess(t, dir, dcraftEnv, strings.NewReader(""), "restore", "--path", dir, "--json",
		"--backup", artifact, "--bind", hostDir, "--approve", "data_restore")
	if res.code != 1 {
		t.Fatalf("corrupt exit=%d stdout=%s stderr=%s", res.code, res.stdout, res.stderr)
	}
	parsed := parseRestore(t, res.stdout)
	if !hasRestoreCategory(parsed.Diagnostics, core.CategoryRestoreIntegrityFailed) {
		t.Fatalf("corrupt diagnostics = %+v", parsed.Diagnostics)
	}
	if after := treeHash(t, hostDir); after != beforeHash {
		t.Fatal("corrupt archive changed the target folder")
	}
	for service, id := range ids {
		if after := containerID(t, name, service); after != id {
			t.Fatalf("%s container was recreated for a corrupt archive", service)
		}
		bindServicesRunning(t, name, service)
	}
}

func TestIntegrationBindRestoreUnwritableSafetyFolderLeavesTheFolderUntouched(t *testing.T) {
	env := requireIntegration(t)
	name := "dcraft-test-bind-restore-" + randomHex(t, 5)
	hostDir := seedBindHostDir(t)
	dir, files := bindRestoreProject(t, name, hostDir)
	dcraftEnv := buildEnv(map[string]string{"DCRAFT_STATE_DIR": t.TempDir()})
	t.Cleanup(func() { cleanupBackupProject(t, env, dir, name, files) })
	cleanupBackupProject(t, env, dir, name, files)
	bindWaiter(t, env, dir, name, files)
	beforeHash := treeHash(t, hostDir)
	app := containerID(t, name, "app")
	artifact := bindBackupArtifact(t, dir, hostDir, dcraftEnv)
	safety := filepath.Join(t.TempDir(), "safety")
	if err := os.Mkdir(safety, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(safety, 0o700) })

	res := runProcess(t, dir, dcraftEnv, strings.NewReader(""), "restore", "--path", dir, "--json",
		"--backup", artifact, "--bind", hostDir, "--safety-output", safety, "--approve", "data_restore")
	if res.code != 1 {
		t.Fatalf("unwritable safety exit=%d stdout=%s stderr=%s", res.code, res.stdout, res.stderr)
	}
	parsed := parseRestore(t, res.stdout)
	if !hasRestoreCategory(parsed.Diagnostics, core.CategoryRestoreSafetyFailed) {
		t.Fatalf("safety diagnostics = %+v", parsed.Diagnostics)
	}
	if after := treeHash(t, hostDir); after != beforeHash {
		t.Fatal("failed safety backup changed the target folder")
	}
	if state := serviceState(t, app); state != "running" {
		t.Fatalf("failed safety backup stopped the service: %s", state)
	}
	if after := containerID(t, name, "app"); after != app {
		t.Fatal("failed safety backup recreated the service container")
	}
}

func TestIntegrationBindRestoreInterruptionReportsStateSafetyAndStopsRestart(t *testing.T) {
	env := requireIntegration(t)
	name := "dcraft-test-bind-restore-" + randomHex(t, 5)
	hostDir := seedBindHostDir(t)
	big, err := os.Create(filepath.Join(hostDir, "big.bin"))
	if err != nil {
		t.Fatal(err)
	}
	chunk := make([]byte, 1<<20)
	for i := range chunk {
		chunk[i] = byte(i*31 + 7)
	}
	for i := 0; i < 128; i++ {
		chunk[0] = byte(i)
		if _, err := big.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	if err := big.Close(); err != nil {
		t.Fatal(err)
	}
	dir, files := bindRestoreProject(t, name, hostDir)
	dcraftEnv := buildEnv(map[string]string{"DCRAFT_STATE_DIR": t.TempDir()})
	t.Cleanup(func() { cleanupBackupProject(t, env, dir, name, files) })
	cleanupBackupProject(t, env, dir, name, files)
	bindWaiter(t, env, dir, name, files)
	app := containerID(t, name, "app")
	worker := containerID(t, name, "worker")
	artifact := bindBackupArtifact(t, dir, hostDir, dcraftEnv)
	marker := filepath.Join(hostDir, ".v2-marker")
	if err := os.WriteFile(marker, []byte("v2"), 0o600); err != nil {
		t.Fatal(err)
	}
	safety := filepath.Join(t.TempDir(), "safety")
	stateDir := t.TempDir()
	cmd := exec.Command(binaryPath, "restore", "--path", dir, "--json",
		"--backup", artifact, "--bind", hostDir, "--safety-output", safety, "--approve", "data_restore")
	cmd.Dir = dir
	cmd.Env = buildEnv(map[string]string{"DCRAFT_STATE_DIR": stateDir})
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(180 * time.Second)
	for {
		artifacts, _ := backup.List(safety)
		_, markerErr := os.Lstat(marker)
		if len(artifacts) == 1 && os.IsNotExist(markerErr) {
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			t.Fatalf("restore never reached replacement; stdout=%s stderr=%s", stdout.String(), stderr.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	err = cmd.Wait()
	if err == nil {
		t.Fatalf("interrupted restore reported success; stdout=%s", stdout.String())
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 130 {
		t.Fatalf("interrupted restore exit = %v, want 130; stdout=%s stderr=%s", err, stdout.String(), stderr.String())
	}
	parsed := parseRestore(t, stdout.String())
	if parsed.Outcome != core.OutcomeCanceled {
		t.Fatalf("outcome = %s", parsed.Outcome)
	}
	var message string
	for _, diag := range parsed.Diagnostics {
		if diag.Category == core.CategoryCanceled {
			message = diag.Message
		}
	}
	for _, want := range []string{"uncertain_partial", "replace", safety} {
		if !strings.Contains(message, want) {
			t.Fatalf("canceled diagnostic missing %q: %s", want, message)
		}
	}
	artifacts, err := backup.List(safety)
	if err != nil || len(artifacts) != 1 {
		t.Fatalf("safety artifacts = %v, %v", artifacts, err)
	}
	if _, err := backup.Verify(artifacts[0].Path); err != nil {
		t.Fatalf("safety backup after interruption did not verify: %v", err)
	}
	if state := serviceState(t, app); state != "exited" {
		t.Fatalf("uncertain partial restore restarted app: %s", state)
	}
	if state := serviceState(t, worker); state != "exited" {
		t.Fatalf("uncertain partial restore restarted worker: %s", state)
	}
}

func TestIntegrationBindRestoreRefusesAnotherStackSharingTheFolder(t *testing.T) {
	env := requireIntegration(t)
	name := "dcraft-test-bind-restore-" + randomHex(t, 5)
	other := "dcraft-test-bind-restore-other-" + randomHex(t, 5)
	hostDir := seedBindHostDir(t)
	otherData := seedBindHostDir(t)
	dir, files := bindRestoreProject(t, name, hostDir)
	otherDir := t.TempDir()
	otherFile := filepath.Join(otherDir, "compose.yaml")
	otherCompose := fmt.Sprintf(`name: %s
services:
  intruder:
    image: alpine:3.20
    command: ["sleep", "600"]
    volumes:
      - %s:/shared
      - %s:/other
`, other, hostDir, otherData)
	if err := os.WriteFile(otherFile, []byte(otherCompose), 0o644); err != nil {
		t.Fatal(err)
	}
	dcraftEnv := buildEnv(map[string]string{"DCRAFT_STATE_DIR": t.TempDir()})
	t.Cleanup(func() {
		cleanupBackupProject(t, env, otherDir, other, []string{otherFile})
		cleanupBackupProject(t, env, dir, name, files)
	})
	cleanupBackupProject(t, env, otherDir, other, []string{otherFile})
	cleanupBackupProject(t, env, dir, name, files)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	if out, err := composeCommand(ctx, env, dir, name, files, "up", "-d", "--wait", "--wait-timeout", "90").CombinedOutput(); err != nil {
		t.Fatalf("compose up main: %v\n%s", err, out)
	}
	if out, err := composeCommand(ctx, env, otherDir, other, []string{otherFile}, "up", "-d", "--wait", "--wait-timeout", "90").CombinedOutput(); err != nil {
		t.Fatalf("compose up other: %v\n%s", err, out)
	}
	app := containerID(t, name, "app")
	intruder := containerID(t, other, "intruder")
	before := treeHash(t, hostDir)
	otherBefore := treeHash(t, otherData)
	artifact := integrationBindArtifact(t, name, hostDir)

	res := runProcess(t, dir, dcraftEnv, strings.NewReader(""), "restore", "--path", dir, "--json",
		"--backup", artifact, "--bind", hostDir, "--approve", "data_restore")
	if res.code != 1 {
		t.Fatalf("shared consumer exit=%d stdout=%s stderr=%s", res.code, res.stdout, res.stderr)
	}
	parsed := parseRestore(t, res.stdout)
	if !hasRestoreCategory(parsed.Diagnostics, core.CategoryRestoreConsumerUnsafe) {
		t.Fatalf("shared consumer diagnostics = %+v", parsed.Diagnostics)
	}
	if after := treeHash(t, hostDir); after != before {
		t.Fatal("shared consumer refusal changed the target folder")
	}
	if after := treeHash(t, otherData); after != otherBefore {
		t.Fatal("another stack's bind folder changed")
	}
	if state := serviceState(t, app); state != "running" {
		t.Fatalf("main stack app was stopped: %s", state)
	}
	if state := serviceState(t, intruder); state != "running" {
		t.Fatalf("other stack intruder was stopped: %s", state)
	}
	if after := containerID(t, name, "app"); after != app {
		t.Fatal("main stack container changed")
	}
	if after := containerID(t, other, "intruder"); after != intruder {
		t.Fatal("other stack container changed")
	}
}
