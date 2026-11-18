package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rootmyself/dcraft/internal/backup"
	"github.com/rootmyself/dcraft/internal/core"
)

func bindIntegrationCompose(name, hostDir string) string {
	return fmt.Sprintf(`name: %s
services:
  app:
    image: alpine:3.20
    command: ["sleep", "600"]
    volumes:
      - %s:/data
      - data:/volume
  writer:
    image: alpine:3.20
    command: ["sh", "-c", "if [ ! -f /data/.started ]; then touch /data/.started; printf 'BEGIN-001|' >> /data/stream.log; sleep 60; printf 'END-001\\n' >> /data/stream.log; fi; sleep 600"]
    volumes:
      - %s:/data
  idle:
    image: alpine:3.20
    command: ["sleep", "600"]
    volumes:
      - %s:/marker
volumes:
  data: {}
`, name, hostDir, hostDir, hostDir)
}

func bindIntegrationProject(t *testing.T, name, hostDir string) (string, []string, []string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(path, []byte(bindIntegrationCompose(name, hostDir)), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, []string{path}, buildEnv(nil)
}

func seedBindHostDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "nested", "dir750"), 0o750); err != nil {
		t.Fatal(err)
	}
	write := func(path, body string, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(dir, "file.txt"), "hello", 0o640)
	write(filepath.Join(dir, ".hidden"), "dot", 0o600)
	write(filepath.Join(dir, "nested", "child.txt"), "child", 0o644)
	write(filepath.Join(dir, "nested", "dir750", "deep.txt"), "deep", 0o600)
	if err := os.Symlink("file.txt", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/hosts", filepath.Join(dir, "abs-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(dir, "file.txt"), filepath.Join(dir, "hard")); err != nil {
		t.Fatal(err)
	}
	return dir
}

func treeHash(t *testing.T, dir string) string {
	t.Helper()
	h := sha256.New()
	var paths []string
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == dir {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		paths = append(paths, rel)
		return nil
	})
	if err != nil {
		return "unreadable:" + err.Error()
	}
	sort.Strings(paths)
	for _, rel := range paths {
		path := filepath.Join(dir, rel)
		info, err := os.Lstat(path)
		if err != nil {
			return "unreadable:" + err.Error()
		}
		fmt.Fprintf(h, "%s|%s|", rel, info.Mode().String())
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, _ := os.Readlink(path)
			fmt.Fprintf(h, "link:%s", target)
		case info.Mode().IsRegular():
			raw, err := os.ReadFile(path)
			if err != nil {
				return "unreadable:" + err.Error()
			}
			fmt.Fprintf(h, "%x", sha256.Sum256(raw))
		}
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func bindWaiter(t *testing.T, env []string, dir, name string, files []string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	if out, err := composeCommand(ctx, env, dir, name, files, "up", "-d", "--wait", "--wait-timeout", "90").CombinedOutput(); err != nil {
		t.Fatalf("compose up: %v\n%s", err, out)
	}
}

func TestIntegrationBindBackupFidelityAndServiceState(t *testing.T) {
	env := requireIntegration(t)
	name := "dcraft-test-bind-" + randomHex(t, 5)
	hostDir := seedBindHostDir(t)
	dir, files, _ := bindIntegrationProject(t, name, hostDir)
	dcraftEnv := buildEnv(map[string]string{"DCRAFT_STATE_DIR": t.TempDir()})
	output := filepath.Join(t.TempDir(), "backups")
	t.Cleanup(func() { cleanupBackupProject(t, env, dir, name, files) })
	cleanupBackupProject(t, env, dir, name, files)

	bindWaiter(t, env, dir, name, files)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if out, err := composeCommand(ctx, env, dir, name, files, "stop", "idle").CombinedOutput(); err != nil {
		t.Fatalf("stop idle: %v\n%s", err, out)
	}
	beforeIDs := map[string]string{
		"app":    containerID(t, name, "app"),
		"writer": containerID(t, name, "writer"),
		"idle":   containerID(t, name, "idle"),
	}
	sourceHash := treeHash(t, hostDir)

	// Wait until the writer is mid-record, then back up. The archive must be
	// the stopped state, not a live read.
	stream := filepath.Join(hostDir, "stream.log")
	deadline := time.Now().Add(30 * time.Second)
	for {
		raw, _ := os.ReadFile(stream)
		if strings.Contains(string(raw), "BEGIN-001|") && !strings.Contains(string(raw), "END-001") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("writer never reached the mid-record state: %q", string(raw))
		}
		time.Sleep(200 * time.Millisecond)
	}

	res := runProcess(t, dir, dcraftEnv, strings.NewReader(""), "backup", "--path", dir, "--json",
		"--bind", hostDir, "--output", output)
	if res.code != 0 {
		t.Fatalf("bind backup exit=%d stdout=%s stderr=%s", res.code, res.stdout, res.stderr)
	}
	report := parseBackup(t, res.stdout).Data.Backup
	if len(report.Artifacts) != 1 {
		t.Fatalf("artifacts = %+v", report.Artifacts)
	}
	if strings.Join(report.Stopped, ",") != "app,writer" {
		t.Fatalf("stopped = %v", report.Stopped)
	}
	resolved, err := filepath.EvalSymlinks(hostDir)
	if err != nil {
		t.Fatal(err)
	}
	artifact := report.Artifacts[0]
	manifest, err := backup.Verify(artifact.Path)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if manifest.Source.Kind != backup.KindBind || manifest.Source.HostPath != resolved || manifest.Source.VolumeName != "" || manifest.Source.VolumeKey != "" {
		t.Fatalf("manifest source = %+v", manifest.Source)
	}
	if !manifest.Sensitive || !strings.Contains(manifest.OwnershipNote, "xattr") {
		t.Fatalf("manifest notes = %+v", manifest)
	}
	entries := map[string]backup.Entry{}
	for _, entry := range manifest.Entries {
		entries[entry.Path] = entry
	}
	for _, want := range []string{"./.hidden", "./file.txt", "./hard", "./link", "./abs-link", "./nested/child.txt", "./nested/dir750/deep.txt", "./stream.log"} {
		if _, ok := entries[want]; !ok {
			t.Fatalf("manifest missing %q: %+v", want, manifest.Entries)
		}
	}
	if entries["./link"].Type != backup.EntrySymlink || entries["./link"].LinkTarget != "file.txt" {
		t.Fatalf("symlink = %+v", entries["./link"])
	}
	if entries["./abs-link"].Type != backup.EntrySymlink || entries["./abs-link"].LinkTarget != "/etc/hosts" {
		t.Fatalf("absolute symlink = %+v", entries["./abs-link"])
	}
	if entries["./hard"].Type != backup.EntryHardlink && entries["./file.txt"].Type != backup.EntryHardlink {
		t.Fatalf("hardlink pair not recorded: %+v / %+v", entries["./hard"], entries["./file.txt"])
	}
	if entries["./.hidden"].Mode != "0600" || entries["./file.txt"].Mode != "0640" || entries["./nested/dir750"].Mode != "0750" {
		t.Fatalf("modes = %+v / %+v / %+v", entries["./.hidden"], entries["./file.txt"], entries["./nested/dir750"])
	}
	if entries["./.hidden"].UID != os.Getuid() || entries["./.hidden"].GID != os.Getgid() {
		t.Fatalf("host ownership = %+v", entries["./.hidden"])
	}
	for path, entry := range entries {
		if entry.Type != backup.EntryFile {
			continue
		}
		sourcePath := filepath.Join(hostDir, filepath.FromSlash(strings.TrimPrefix(path, "./")))
		raw, err := os.ReadFile(sourcePath)
		if err != nil {
			t.Fatalf("read source %s: %v", sourcePath, err)
		}
		sum := sha256.Sum256(raw)
		if entry.SHA256 != hex.EncodeToString(sum[:]) || entry.SizeBytes != int64(len(raw)) {
			t.Fatalf("content mismatch for %s: manifest=%+v size=%d", path, entry, len(raw))
		}
	}
	archivedStream := entries["./stream.log"]
	rawStream, err := os.ReadFile(stream)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(rawStream)
	if archivedStream.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("archive stream does not match the stopped host state")
	}
	if !strings.Contains(string(rawStream), "BEGIN-001|") || strings.Contains(string(rawStream), "END-001") {
		t.Fatalf("stopped host state is not mid-record: %q", string(rawStream))
	}
	if after := treeHash(t, hostDir); after != sourceHash && !strings.Contains(after, "unreadable") {
		// The writer service is stopped, so dcraft must not modify the source.
		t.Fatalf("backup modified the source folder:\nbefore=%s\nafter=%s", sourceHash, after)
	}
	for service, id := range beforeIDs {
		got := serviceState(t, id)
		if service == "idle" {
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

	// A mixed volume + bind selection produces one v1 artifact of each kind.
	res = runProcess(t, dir, dcraftEnv, strings.NewReader(""), "backup", "--path", dir, "--json",
		"--volume", "data", "--bind", hostDir, "--output", output)
	if res.code != 0 {
		t.Fatalf("mixed backup exit=%d stdout=%s stderr=%s", res.code, res.stdout, res.stderr)
	}
	mixed := parseBackup(t, res.stdout).Data.Backup
	kinds := map[backup.Kind]int{}
	for _, artifact := range mixed.Artifacts {
		kinds[artifact.Source.Kind]++
	}
	if kinds[backup.KindVolume] != 1 || kinds[backup.KindBind] != 1 {
		t.Fatalf("mixed artifact kinds = %v", kinds)
	}
	if artifacts, err := backup.List(output); err != nil || len(artifacts) != 3 {
		t.Fatalf("artifacts = %v, err=%v (want 3: bind, volume, bind)", artifacts, err)
	}
}

func TestIntegrationBindBackupRefusesOutputInsideTheSource(t *testing.T) {
	env := requireIntegration(t)
	name := "dcraft-test-bind-" + randomHex(t, 5)
	hostDir := seedBindHostDir(t)
	dir, files, _ := bindIntegrationProject(t, name, hostDir)
	t.Cleanup(func() { cleanupBackupProject(t, env, dir, name, files) })
	cleanupBackupProject(t, env, dir, name, files)
	bindWaiter(t, env, dir, name, files)
	app := containerID(t, name, "app")
	output := filepath.Join(hostDir, "backups")
	res := runProcess(t, dir, buildEnv(map[string]string{"DCRAFT_STATE_DIR": t.TempDir()}), strings.NewReader(""),
		"backup", "--path", dir, "--json", "--bind", hostDir, "--output", output)
	if res.code != 1 {
		t.Fatalf("exit = %d, want 1; stdout=%s", res.code, res.stdout)
	}
	parsed := parseBackup(t, res.stdout)
	if !hasBackupCategory(parsed.Diagnostics, core.CategoryBackupBindUnsafe) {
		t.Fatalf("diagnostics = %+v", parsed.Diagnostics)
	}
	if got := serviceState(t, app); got != "running" {
		t.Fatalf("output containment check stopped a service: %s", got)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("dcraft created the output directory inside the bind source")
	}
}

func TestIntegrationBindBackupRefusesAnotherStackSharingTheFolder(t *testing.T) {
	env := requireIntegration(t)
	name := "dcraft-test-bind-" + randomHex(t, 5)
	other := "dcraft-test-bind-other-" + randomHex(t, 5)
	hostDir := seedBindHostDir(t)
	otherData := seedBindHostDir(t)
	dir, files, _ := bindIntegrationProject(t, name, hostDir)
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
	sharedHash := treeHash(t, hostDir)
	otherHash := treeHash(t, otherData)
	output := filepath.Join(t.TempDir(), "backups")

	res := runProcess(t, dir, buildEnv(map[string]string{"DCRAFT_STATE_DIR": t.TempDir()}), strings.NewReader(""),
		"backup", "--path", dir, "--json", "--bind", hostDir, "--output", output)
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
	if after := treeHash(t, hostDir); after != sharedHash {
		t.Fatalf("shared bind folder changed:\nbefore=%s\nafter=%s", sharedHash, after)
	}
	if after := treeHash(t, otherData); after != otherHash {
		t.Fatalf("another project's bind folder changed:\nbefore=%s\nafter=%s", otherHash, after)
	}
	if artifacts, _ := backup.List(output); len(artifacts) != 0 {
		t.Fatalf("artifact created despite refusal: %+v", artifacts)
	}
}

func TestIntegrationBindBackupReadFailureRestoresServices(t *testing.T) {
	env := requireIntegration(t)
	name := "dcraft-test-bind-" + randomHex(t, 5)
	hostDir := seedBindHostDir(t)
	locked := filepath.Join(hostDir, "locked")
	if err := os.Mkdir(locked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locked, "secret.txt"), []byte("locked"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	dir, files, _ := bindIntegrationProject(t, name, hostDir)
	t.Cleanup(func() { cleanupBackupProject(t, env, dir, name, files) })
	cleanupBackupProject(t, env, dir, name, files)
	bindWaiter(t, env, dir, name, files)
	app := containerID(t, name, "app")
	output := filepath.Join(t.TempDir(), "backups")

	res := runProcess(t, dir, buildEnv(map[string]string{"DCRAFT_STATE_DIR": t.TempDir()}), strings.NewReader(""),
		"backup", "--path", dir, "--json", "--bind", hostDir, "--output", output)
	if res.code != 1 {
		t.Fatalf("exit = %d, want 1; stdout=%s", res.code, res.stdout)
	}
	parsed := parseBackup(t, res.stdout)
	if !hasBackupCategory(parsed.Diagnostics, core.CategoryBackupBindInvalid) {
		t.Fatalf("diagnostics = %+v", parsed.Diagnostics)
	}
	if !strings.Contains(res.stdout, "locked") {
		t.Fatalf("unreadable path not named: %s", res.stdout)
	}
	if got := serviceState(t, app); got != "running" {
		t.Fatalf("app was not restored after the read failure: %s", got)
	}
	if artifacts, _ := backup.List(output); len(artifacts) != 0 {
		t.Fatalf("read failure was listed as a valid backup: %+v", artifacts)
	}
}

func TestIntegrationBindBackupInterruptionRestoresServices(t *testing.T) {
	env := requireIntegration(t)
	name := "dcraft-test-bind-" + randomHex(t, 5)
	hostDir := seedBindHostDir(t)
	big, err := os.Create(filepath.Join(hostDir, "big.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if err := big.Truncate(512 << 20); err != nil {
		t.Fatal(err)
	}
	if err := big.Close(); err != nil {
		t.Fatal(err)
	}
	dir, files, _ := bindIntegrationProject(t, name, hostDir)
	t.Cleanup(func() { cleanupBackupProject(t, env, dir, name, files) })
	cleanupBackupProject(t, env, dir, name, files)
	bindWaiter(t, env, dir, name, files)
	app := containerID(t, name, "app")
	output := filepath.Join(t.TempDir(), "interrupt")

	stateDir := t.TempDir()
	cmd := exec.Command(binaryPath, "backup", "--path", dir, "--json", "--bind", hostDir, "--output", output)
	cmd.Dir = dir
	cmd.Env = buildEnv(map[string]string{"DCRAFT_STATE_DIR": stateDir})
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for !hasPartial(t, output) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !hasPartial(t, output) {
		_ = cmd.Process.Kill()
		t.Fatalf("backup never created a partial artifact; stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	err = cmd.Wait()
	if err == nil {
		t.Fatalf("interrupted backup reported success; stdout=%s", stdout.String())
	}
	var exitErr *exec.ExitError
	if ok := errorAs(err, &exitErr); !ok || exitErr.ExitCode() != 130 {
		t.Fatalf("interrupted backup exit = %v, want 130; stdout=%s stderr=%s", err, stdout.String(), stderr.String())
	}
	if artifacts, _ := backup.List(output); len(artifacts) != 0 {
		t.Fatalf("interrupted backup was listed as valid: %+v", artifacts)
	}
	if got := serviceState(t, app); got != "running" {
		t.Fatalf("app was not restored after interruption: %s", got)
	}
	assertNoBackupHelpers(t)
	if names := backupHelperNames(t); len(names) != 0 {
		t.Fatalf("host-side backup left helper containers: %v", names)
	}
}

func TestIntegrationBindBackupDoesNotTouchAnUnrelatedBindFolder(t *testing.T) {
	env := requireIntegration(t)
	name := "dcraft-test-bind-" + randomHex(t, 5)
	hostDir := seedBindHostDir(t)
	canary := seedBindHostDir(t)
	dir, files, _ := bindIntegrationProject(t, name, hostDir)
	t.Cleanup(func() { cleanupBackupProject(t, env, dir, name, files) })
	cleanupBackupProject(t, env, dir, name, files)
	bindWaiter(t, env, dir, name, files)
	before := treeHash(t, canary)
	output := filepath.Join(t.TempDir(), "backups")
	res := runProcess(t, dir, buildEnv(map[string]string{"DCRAFT_STATE_DIR": t.TempDir()}), strings.NewReader(""),
		"backup", "--path", dir, "--json", "--bind", hostDir, "--output", output)
	if res.code != 0 {
		t.Fatalf("backup exit=%d stdout=%s stderr=%s", res.code, res.stdout, res.stderr)
	}
	if after := treeHash(t, canary); after != before {
		t.Fatalf("unrelated bind folder changed:\nbefore=%s\nafter=%s", before, after)
	}
}
