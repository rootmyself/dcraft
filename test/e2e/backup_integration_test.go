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
)

func backupIntegrationCompose(name string) string {
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
  writer:
    image: alpine:3.20
    command: ["sh", "-c", "if [ ! -f /live/.started ]; then touch /live/.started; printf 'BEGIN-001|' >> /live/stream.log; sleep 60; printf 'END-001\\n' >> /live/stream.log; fi; sleep 600"]
    volumes:
      - live:/live
volumes:
  data: {}
  live: {}
`, name)
}

func backupIntegrationProject(t *testing.T, name string) (string, []string, []string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(path, []byte(backupIntegrationCompose(name)), 0o644); err != nil {
		t.Fatal(err)
	}
	env := buildEnv(nil)
	return dir, []string{path}, env
}

func backupComposeRun(t *testing.T, env []string, dir string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func cleanupBackupProject(t *testing.T, env []string, dir, name string, files []string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := composeCommand(ctx, env, dir, name, files, "down", "-v", "--remove-orphans")
	_, _ = cmd.CombinedOutput()
}

func seedBackupVolume(t *testing.T, env []string, volume string) {
	t.Helper()
	script := `set -e
mkdir -p /data/nested "/data/space dir"
printf 'hello' > /data/file.txt
printf 'dot' > /data/.hidden
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
printf 'setgid' > /data/setgidfile
chmod 2755 /data/setgidfile
`
	backupComposeRun(t, env, t.TempDir(), "run", "--rm", "-v", volume+":/data", "alpine:3.20", "sh", "-c", script)
}

func volumeFacts(t *testing.T, env []string, volume string) string {
	t.Helper()
	script := `cd /data && find . -mindepth 1 -exec stat -c '%n|%u|%g|%a|%F|%s|%N' {} + | sort`
	return backupComposeRun(t, env, t.TempDir(), "run", "--rm", "-v", volume+":/data:ro", "alpine:3.20", "sh", "-c", script)
}

func extractArtifactToVolume(t *testing.T, env []string, artifactDir, volume string) {
	t.Helper()
	script := `set -e
find /data -mindepth 1 -delete
cd /data
tar -xpzf /backup/data.tar.gz
`
	backupComposeRun(t, env, t.TempDir(), "run", "--rm",
		"-v", volume+":/data",
		"-v", artifactDir+":/backup:ro",
		"alpine:3.20", "sh", "-c", script)
}

func serviceState(t *testing.T, container string) string {
	t.Helper()
	out, err := exec.Command("docker", "inspect", "-f", "{{.State.Status}}", container).Output()
	if err != nil {
		t.Fatalf("inspect %s: %v", container, err)
	}
	return strings.TrimSpace(string(out))
}

func containerID(t *testing.T, project, service string) string {
	t.Helper()
	out, err := exec.Command("docker", "ps", "-a", "--filter", "label=com.docker.compose.project="+project,
		"--filter", "label=com.docker.compose.service="+service, "--format", "{{.ID}}").Output()
	if err != nil {
		t.Fatalf("ps %s/%s: %v", project, service, err)
	}
	return strings.TrimSpace(string(out))
}

func TestIntegrationVolumeBackupFidelityAndServiceState(t *testing.T) {
	env := requireIntegration(t)
	name := "dcraft-test-bak-" + randomHex(t, 5)
	dir, files, _ := backupIntegrationProject(t, name)
	dcraftEnv := buildEnv(map[string]string{"DCRAFT_STATE_DIR": t.TempDir()})
	volume := name + "_data"
	liveVolume := name + "_live"
	cleanupBackupProject(t, env, dir, name, files)
	t.Cleanup(func() { cleanupBackupProject(t, env, dir, name, files) })

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	if out, err := composeCommand(ctx, env, dir, name, files, "up", "-d", "--wait", "--wait-timeout", "90").CombinedOutput(); err != nil {
		t.Fatalf("compose up: %v\n%s", err, out)
	}
	seedBackupVolume(t, env, volume)

	// One service is deliberately stopped before the backup: it must stay
	// stopped afterwards (dcraft only restarts what it stopped).
	if out, err := composeCommand(ctx, env, dir, name, files, "stop", "idle").CombinedOutput(); err != nil {
		t.Fatalf("stop idle: %v\n%s", err, out)
	}
	beforeIDs := map[string]string{
		"app":    containerID(t, name, "app"),
		"idle":   containerID(t, name, "idle"),
		"writer": containerID(t, name, "writer"),
	}
	if beforeIDs["app"] == "" || beforeIDs["idle"] == "" || beforeIDs["writer"] == "" {
		t.Fatalf("missing containers: %v", beforeIDs)
	}
	sourceFacts := volumeFacts(t, env, volume)

	// Start the deterministic writer and wait until it is mid-record.
	outDir := filepath.Join(t.TempDir(), "backups")
	writerLog := func() string {
		return backupComposeRun(t, env, t.TempDir(), "run", "--rm", "-v", liveVolume+":/live:ro", "alpine:3.20", "cat", "/live/stream.log")
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		if strings.Contains(writerLog(), "BEGIN-001|") && !strings.Contains(writerLog(), "END-001") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("writer never reached the mid-record state: %q", writerLog())
		}
		time.Sleep(200 * time.Millisecond)
	}

	res := runProcess(t, dir, dcraftEnv, strings.NewReader(""), "backup", "--path", dir, "--json",
		"--volume", "live", "--output", outDir)
	if res.code != 0 {
		t.Fatalf("backup live exit=%d stdout=%s stderr=%s", res.code, res.stdout, res.stderr)
	}
	liveReport := parseBackup(t, res.stdout).Data.Backup
	if len(liveReport.Artifacts) != 1 {
		t.Fatalf("live artifacts = %+v", liveReport.Artifacts)
	}
	archiveLog := backupComposeRun(t, env, t.TempDir(), "run", "--rm",
		"-v", liveReport.Artifacts[0].Path+":/backup:ro", "alpine:3.20",
		"tar", "-xOzf", "/backup/data.tar.gz", "./stream.log")
	liveSource := writerLog()
	if archiveLog != liveSource {
		t.Fatalf("live writer archive differs from the stopped volume:\narchive=%q\nvolume=%q", archiveLog, liveSource)
	}
	if !strings.Contains(archiveLog, "BEGIN-001|") || strings.Contains(archiveLog, "END-001") {
		t.Fatalf("live archive is not the stopped mid-record state: %q", archiveLog)
	}
	if strings.Join(liveReport.Stopped, ",") != "writer" {
		t.Fatalf("live stopped = %v", liveReport.Stopped)
	}
	if got := serviceState(t, beforeIDs["writer"]); got != "running" {
		t.Fatalf("writer not restarted: %s", got)
	}
	if got := serviceState(t, beforeIDs["idle"]); got != "exited" {
		t.Fatalf("previously stopped idle service was started: %s", got)
	}

	// Main data volume backup with full fidelity verification.
	res = runProcess(t, dir, dcraftEnv, strings.NewReader(""), "backup", "--path", dir, "--json",
		"--volume", "data", "--output", outDir)
	if res.code != 0 {
		t.Fatalf("backup data exit=%d stdout=%s stderr=%s", res.code, res.stdout, res.stderr)
	}
	report := parseBackup(t, res.stdout).Data.Backup
	if len(report.Artifacts) != 1 {
		t.Fatalf("data artifacts = %+v", report.Artifacts)
	}
	artifact := report.Artifacts[0]
	manifest, err := backup.Verify(artifact.Path)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if manifest.Source.VolumeName != volume || !strings.Contains(strings.Join(manifest.Stack.RunningBefore, ","), "app") {
		t.Fatalf("manifest = %+v", manifest)
	}
	if !manifest.Sensitive || manifest.OwnershipNote == "" {
		t.Fatalf("manifest notices = %+v", manifest)
	}

	checkVolume := name + "-check-" + randomHex(t, 5)
	t.Cleanup(func() { _ = exec.Command("docker", "volume", "rm", checkVolume).Run() })
	backupComposeRun(t, env, t.TempDir(), "volume", "create", checkVolume)
	extractArtifactToVolume(t, env, artifact.Path, checkVolume)
	restoredFacts := volumeFacts(t, env, checkVolume)
	if sourceFacts != restoredFacts {
		t.Fatalf("restored content differs from the source:\n--- source ---\n%s\n--- restored ---\n%s", sourceFacts, restoredFacts)
	}
	link := backupComposeRun(t, env, t.TempDir(), "run", "--rm", "-v", checkVolume+":/data:ro", "alpine:3.20", "readlink", "/data/link")
	if link != "nested/file.txt" {
		t.Fatalf("symlink target = %q", link)
	}
	inodes := backupComposeRun(t, env, t.TempDir(), "run", "--rm", "-v", checkVolume+":/data:ro", "alpine:3.20",
		"sh", "-c", "stat -c '%i' /data/file.txt /data/hard")
	parts := strings.Fields(inodes)
	if len(parts) != 2 || parts[0] != parts[1] {
		t.Fatalf("hardlink was not preserved: %q", inodes)
	}
	for service, id := range beforeIDs {
		if got := serviceState(t, id); service == "idle" {
			if got != "exited" {
				t.Fatalf("idle state = %s", got)
			}
		} else if got != "running" {
			t.Fatalf("%s state = %s", service, got)
		}
		if after := containerID(t, name, service); after != id {
			t.Fatalf("%s container was recreated: %s -> %s", service, id, after)
		}
	}

	// Two backups both remain.
	res = runProcess(t, dir, dcraftEnv, strings.NewReader(""), "backup", "--path", dir, "--json",
		"--volume", "data", "--output", outDir)
	if res.code != 0 {
		t.Fatalf("second backup exit=%d stdout=%s", res.code, res.stdout)
	}
	artifacts, err := backup.List(outDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 3 {
		t.Fatalf("artifacts = %d, want 3 (live + two data)", len(artifacts))
	}
}

func TestIntegrationVolumeBackupRefusesAnotherStackConsumer(t *testing.T) {
	env := requireIntegration(t)
	name := "dcraft-test-bak-" + randomHex(t, 5)
	other := "dcraft-test-bak-other-" + randomHex(t, 5)
	dir, files, _ := backupIntegrationProject(t, name)
	otherDir := t.TempDir()
	otherCompose := fmt.Sprintf(`name: %s
services:
  intruder:
    image: alpine:3.20
    command: ["sleep", "600"]
    volumes:
      - shared:/data
volumes:
  shared:
    name: %s_data
    external: true
`, other, name)
	otherFile := filepath.Join(otherDir, "compose.yaml")
	if err := os.WriteFile(otherFile, []byte(otherCompose), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupBackupProject(t, env, otherDir, other, []string{otherFile})
		cleanupBackupProject(t, env, dir, name, files)
	})
	cleanupBackupProject(t, env, otherDir, other, []string{otherFile})
	cleanupBackupProject(t, env, dir, name, files)

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	if out, err := composeCommand(ctx, env, dir, name, files, "up", "-d", "app").CombinedOutput(); err != nil {
		t.Fatalf("compose up main: %v\n%s", err, out)
	}
	if out, err := composeCommand(ctx, env, otherDir, other, []string{otherFile}, "up", "-d").CombinedOutput(); err != nil {
		t.Fatalf("compose up other: %v\n%s", err, out)
	}
	intruder := containerID(t, other, "intruder")
	app := containerID(t, name, "app")

	outDir := filepath.Join(t.TempDir(), "backups")
	res := runProcess(t, dir, buildEnv(map[string]string{"DCRAFT_STATE_DIR": t.TempDir()}), strings.NewReader(""),
		"backup", "--path", dir, "--json", "--volume", "data", "--output", outDir)
	if res.code != 1 {
		t.Fatalf("exit = %d, want 1; stdout=%s", res.code, res.stdout)
	}
	parsed := parseBackup(t, res.stdout)
	if !hasBackupCategory(parsed.Diagnostics, core.CategoryBackupConsumerUnsafe) {
		t.Fatalf("diagnostics = %+v", parsed.Diagnostics)
	}
	if got := serviceState(t, intruder); got != "running" {
		t.Fatalf("other stack intruder was changed: %s", got)
	}
	if got := serviceState(t, app); got != "running" {
		t.Fatalf("main stack was stopped: %s", got)
	}
	if after := containerID(t, other, "intruder"); after != intruder {
		t.Fatalf("intruder container changed: %s -> %s", intruder, after)
	}
	if artifacts, _ := backup.List(outDir); len(artifacts) != 0 {
		t.Fatalf("artifact created despite refusal: %+v", artifacts)
	}
}

func TestIntegrationVolumeBackupInterruptionAndConcurrentUp(t *testing.T) {
	env := requireIntegration(t)
	name := "dcraft-test-bak-" + randomHex(t, 5)
	dir, files, _ := backupIntegrationProject(t, name)
	t.Cleanup(func() { cleanupBackupProject(t, env, dir, name, files) })
	cleanupBackupProject(t, env, dir, name, files)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	if out, err := composeCommand(ctx, env, dir, name, files, "up", "-d", "app", "writer").CombinedOutput(); err != nil {
		t.Fatalf("compose up: %v\n%s", err, out)
	}
	app := containerID(t, name, "app")

	slowImage := buildBackupHelperImage(t, "slow")
	failImage := buildBackupHelperImage(t, "fail")
	stateDir := t.TempDir()
	dcraftEnv := buildEnv(map[string]string{
		"DCRAFT_STATE_DIR":    stateDir,
		"DCRAFT_BACKUP_IMAGE": slowImage,
	})

	// Interrupt mid-copy: wait for the fresh partial artifact, then SIGINT.
	interruptOut := filepath.Join(t.TempDir(), "interrupt")
	cmd := exec.Command(binaryPath, "backup", "--path", dir, "--json", "--volume", "data", "--output", interruptOut)
	cmd.Dir = dir
	cmd.Env = dcraftEnv
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waitForPartial := time.Now().Add(30 * time.Second)
	for {
		if hasPartial(t, interruptOut) || time.Now().After(waitForPartial) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !hasPartial(t, interruptOut) {
		_ = cmd.Process.Kill()
		t.Fatalf("backup never created a partial artifact; stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	err := cmd.Wait()
	if err == nil {
		t.Fatalf("interrupted backup reported success; stdout=%s", stdout.String())
	}
	var exitErr *exec.ExitError
	if ok := errorAs(err, &exitErr); !ok || exitErr.ExitCode() != 130 {
		t.Fatalf("interrupted backup exit = %v, want 130; stdout=%s stderr=%s", err, stdout.String(), stderr.String())
	}
	if artifacts, _ := backup.List(interruptOut); len(artifacts) != 0 {
		t.Fatalf("interrupted backup was listed as valid: %+v", artifacts)
	}
	if got := serviceState(t, app); got != "running" {
		t.Fatalf("app was not restored after interruption: %s", got)
	}
	assertNoBackupHelpers(t)

	// The immediately following backup of the same volume must succeed: a
	// helper left behind would be seen as an outside consumer and refuse.
	afterOut := filepath.Join(t.TempDir(), "after")
	res := runProcess(t, dir, buildEnv(map[string]string{"DCRAFT_STATE_DIR": stateDir}), strings.NewReader(""),
		"backup", "--path", dir, "--json", "--volume", "data", "--output", afterOut)
	if res.code != 0 {
		t.Fatalf("backup after cancellation exit=%d stdout=%s stderr=%s", res.code, res.stdout, res.stderr)
	}
	afterArtifacts, err := backup.List(afterOut)
	if err != nil || len(afterArtifacts) != 1 {
		t.Fatalf("backup after cancellation artifacts = %v, err=%v", afterArtifacts, err)
	}
	if _, err := backup.Verify(afterArtifacts[0].Path); err != nil {
		t.Fatalf("backup after cancellation did not verify: %v", err)
	}
	assertNoBackupHelpers(t)

	// Concurrent up while the slow helper holds the lock. Wait until a
	// service is actually stopped: that proves the backup acquired the lock
	// and passed discovery, so stack_busy is deterministic. A stale .partial
	// from the interrupted run must not be used as the synchronization point.
	concurrentOut := filepath.Join(t.TempDir(), "concurrent")
	cmd = exec.Command(binaryPath, "backup", "--path", dir, "--json", "--volume", "data", "--output", concurrentOut)
	cmd.Dir = dir
	cmd.Env = dcraftEnv
	stdout.Reset()
	stderr.Reset()
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	slowDone := make(chan error, 1)
	go func() { slowDone <- cmd.Wait() }()
	stopDeadline := time.Now().Add(30 * time.Second)
	for serviceState(t, app) != "exited" {
		select {
		case err := <-slowDone:
			t.Fatalf("slow backup ended before stopping the service: %v stdout=%s stderr=%s", err, stdout.String(), stderr.String())
		default:
		}
		if time.Now().After(stopDeadline) {
			_ = cmd.Process.Kill()
			t.Fatalf("slow backup did not stop the service; stdout=%s stderr=%s", stdout.String(), stderr.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	up := runProcess(t, dir, buildEnv(map[string]string{"DCRAFT_STATE_DIR": stateDir}), strings.NewReader(""), "up", "--path", dir, "--json")
	if up.code != 1 {
		_ = cmd.Process.Kill()
		t.Fatalf("concurrent up exit = %d, want 1; stdout=%s", up.code, up.stdout)
	}
	upParsed := parseBackup(t, up.stdout)
	if !hasBackupCategory(upParsed.Diagnostics, core.CategoryStackBusy) {
		_ = cmd.Process.Kill()
		t.Fatalf("concurrent up diagnostics = %+v", upParsed.Diagnostics)
	}
	if err := <-slowDone; err != nil {
		t.Fatalf("slow backup failed: %v stdout=%s stderr=%s", err, stdout.String(), stderr.String())
	}
	if artifacts, _ := backup.List(concurrentOut); len(artifacts) != 1 {
		t.Fatalf("slow backup did not publish after the concurrent up was refused: %v", artifacts)
	}
	assertNoBackupHelpers(t)

	// Helper failure after stop: no valid artifact, services restored, and no
	// helper container left behind.
	failEnv := buildEnv(map[string]string{
		"DCRAFT_STATE_DIR":    stateDir,
		"DCRAFT_BACKUP_IMAGE": failImage,
	})
	failOut := filepath.Join(t.TempDir(), "fail")
	res = runProcess(t, dir, failEnv, strings.NewReader(""), "backup", "--path", dir, "--json",
		"--volume", "data", "--output", failOut)
	if res.code != 1 {
		t.Fatalf("failing helper exit = %d; stdout=%s", res.code, res.stdout)
	}
	parsed := parseBackup(t, res.stdout)
	if !hasBackupCategory(parsed.Diagnostics, core.CategoryBackupFailed) {
		t.Fatalf("failing helper diagnostics = %+v", parsed.Diagnostics)
	}
	if !strings.Contains(res.stdout, "Permission denied") {
		t.Fatalf("helper failure output missing: %s", res.stdout)
	}
	if artifacts, _ := backup.List(failOut); len(artifacts) != 0 {
		t.Fatalf("failed helper published an artifact: %v", artifacts)
	}
	assertNoBackupHelpers(t)
	if got := serviceState(t, app); got != "running" {
		t.Fatalf("app not restored after helper failure: %s", got)
	}
}

func backupHelperNames(t *testing.T) []string {
	t.Helper()
	out, err := exec.Command("docker", "ps", "-a", "--filter", "label=dcraft.backup=1", "--format", "{{.Names}}").Output()
	if err != nil {
		t.Fatalf("list backup helpers: %v", err)
	}
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			names = append(names, line)
		}
	}
	return names
}

func assertNoBackupHelpers(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if names := backupHelperNames(t); len(names) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("backup helper containers survived the operation: %v", backupHelperNames(t))
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func hasPartial(t *testing.T, dir string) bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() && strings.HasSuffix(e.Name(), backup.PartialSuffix) {
			return true
		}
	}
	return false
}

func errorAs(err error, target **exec.ExitError) bool {
	for err != nil {
		if e, ok := err.(*exec.ExitError); ok {
			*target = e
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// buildBackupHelperImage builds a local alpine-based helper whose tar either
// sleeps before copying or fails with a read-permission message. Both are
// isolated dcraft-test images removed by the caller.
func buildBackupHelperImage(t *testing.T, mode string) string {
	t.Helper()
	image := "dcraft-test-backup-" + mode + ":" + randomHex(t, 5)
	var dockerfile string
	switch mode {
	case "slow":
		dockerfile = `FROM alpine:3.20
RUN mv /bin/tar /bin/tar.real
RUN printf '#!/bin/sh\nsleep 8\nexec /bin/busybox tar "$@"\n' > /bin/tar && chmod +x /bin/tar
`
	case "fail":
		dockerfile = `FROM alpine:3.20
RUN mv /bin/tar /bin/tar.real
RUN printf '#!/bin/sh\necho "tar: can not open '"'"'/data/secret'"'"': Permission denied" >&2\nexit 1\n' > /bin/tar && chmod +x /bin/tar
`
	default:
		t.Fatalf("unknown helper mode %q", mode)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "build", "-t", image, "-")
	cmd.Stdin = strings.NewReader(dockerfile)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build helper image: %v\n%s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "rmi", "-f", image).Run() })
	return image
}

func TestIntegrationVolumeBackupOutputDirectoryPrecheck(t *testing.T) {
	env := requireIntegration(t)
	name := "dcraft-test-bak-" + randomHex(t, 5)
	dir, files, _ := backupIntegrationProject(t, name)
	t.Cleanup(func() { cleanupBackupProject(t, env, dir, name, files) })
	cleanupBackupProject(t, env, dir, name, files)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	if out, err := composeCommand(ctx, env, dir, name, files, "up", "-d", "app").CombinedOutput(); err != nil {
		t.Fatalf("compose up: %v\n%s", err, out)
	}
	app := containerID(t, name, "app")
	output := t.TempDir()
	if err := os.Chmod(output, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(output, 0o700) })
	res := runProcess(t, dir, buildEnv(map[string]string{"DCRAFT_STATE_DIR": t.TempDir()}), strings.NewReader(""),
		"backup", "--path", dir, "--json", "--volume", "data", "--output", output)
	if res.code != 1 {
		t.Fatalf("exit = %d, want 1; stdout=%s", res.code, res.stdout)
	}
	parsed := parseBackup(t, res.stdout)
	if !hasBackupCategory(parsed.Diagnostics, core.CategoryBackupOutputInvalid) {
		t.Fatalf("diagnostics = %+v", parsed.Diagnostics)
	}
	if got := serviceState(t, app); got != "running" {
		t.Fatalf("output precheck stopped a service: %s", got)
	}
	if after := containerID(t, name, "app"); after != app {
		t.Fatalf("app container changed: %s -> %s", app, after)
	}
}
