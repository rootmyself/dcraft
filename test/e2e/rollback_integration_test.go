package e2e

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rootmyself/dcraft/internal/core"
)

// rollbackScenario is one real-Docker stack prepared with a verified v1
// deployment, then a failed v2 update that keeps the v1 record.
type rollbackScenario struct {
	project     string
	dir         string
	composePath string
	stateDir    string
	env         []string
	dcraftEnv   []string
	first       updateJSON
	appV1Image  string
	appV1Tag    string
	v2Container string
	recordPath  string
	record      []byte
}

func setupRollbackScenario(t *testing.T, ignoreTERM bool) *rollbackScenario {
	t.Helper()
	env := requireIntegration(t)
	project := "dcraft-test-rb-" + randomHex(t, 6)
	dir := filepath.Join(t.TempDir(), project)
	if err := os.MkdirAll(filepath.Join(dir, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	stateDir := t.TempDir()
	dcraftEnv := buildEnv(map[string]string{"DCRAFT_STATE_DIR": stateDir})
	composePath := filepath.Join(dir, "compose.yaml")
	v2Path := filepath.Join(dir, "compose.v2.yaml")

	command := "sleep 600"
	if ignoreTERM {
		command = "trap '' TERM; sleep 600"
	}
	v1 := fmt.Sprintf(`name: %s
services:
  app:
    build:
      context: ./app
    command: ["sh", "-c", "%s"]
    environment:
      APP_MARKER: v1
    healthcheck:
      test: ["CMD", "true"]
      interval: 1s
      timeout: 1s
      retries: 5
    volumes:
      - data:/data
volumes:
  data:
`, project, command)
	if err := os.WriteFile(composePath, []byte(v1), 0o644); err != nil {
		t.Fatal(err)
	}
	dockerfile := "FROM busybox:1.36\nCOPY marker /marker\nCMD [\"sh\", \"-c\", \"sleep 600\"]\n"
	if err := os.WriteFile(filepath.Join(dir, "app", "Dockerfile"), []byte(dockerfile), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app", "marker"), []byte("v1-image"), 0o644); err != nil {
		t.Fatal(err)
	}
	v2 := `services:
  app:
    environment:
      APP_MARKER: v2
    healthcheck:
      test: ["CMD", "false"]
      interval: 1s
      timeout: 1s
      retries: 1
`
	if err := os.WriteFile(v2Path, []byte(v2), 0o644); err != nil {
		t.Fatal(err)
	}

	imagesCleanup := &updateImageCleanup{}
	imagesCleanup.addRef(project + "-app")
	imagesCleanup.register(t)
	t.Cleanup(func() { cleanupUpdateProject(t, env, dir, project, composePath) })

	// v1 must be running and healthy before an update can capture it as the
	// previous-good deployment. No extra build may run between up and that
	// update, or the Compose build tag could move away from the running image.
	up := runProcess(t, dir, dcraftEnv, strings.NewReader(""), "up", "--path", dir, "--json")
	if up.code != 0 {
		t.Fatalf("up v1 exit=%d stderr=%q", up.code, up.stderr)
	}
	waitForContainerHealthy(t, env, composeInspect(t, env, project, composePath, "app"))

	first, res := updateJSONFor(t, dir, dcraftEnv)
	requireSuccessUpdate(t, first, res)
	imagesCleanup.track(first)
	appV1 := recordedServices(first)["app"]
	tag := recordedTags(first)["app"]
	if appV1 == "" || tag == "" {
		t.Fatalf("v1 record = %+v (status=%s detail=%q notices=%v)", first.Data.Update.GoodVersion,
			first.Data.Update.RecordStatus, first.Data.Update.RecordDetail, first.Data.Update.Notices)
	}
	v1Container := composeInspect(t, env, project, composePath, "app")
	if got := containerImageID(t, v1Container); got != appV1 {
		t.Fatalf("recorded image %q is not the pre-update running image %q", appV1, got)
	}
	recordPath := stateFileFor(t, stateDir)
	record, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}

	// v2 changes the source and the execution config but fails readiness, so
	// the v1 record must stay byte-identical.
	if err := os.WriteFile(filepath.Join(dir, "app", "marker"), []byte("v2-image"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, res := updateJSONFor(t, dir, dcraftEnv, "-f", composePath, "-f", v2Path, "--timeout", "10")
	if res.code != 1 || !hasUpdateCategory(second, "readiness_failed") {
		t.Fatalf("v2 update should fail readiness: exit=%d outcome=%q diagnostics=%+v", res.code, second.Outcome, second.Diagnostics)
	}
	after, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(record) {
		t.Fatal("the failed v2 update replaced the v1 record")
	}
	imagesCleanup.track(second)
	v2Container := composeInspect(t, env, project, composePath, "app")
	if v2Container == "" {
		t.Fatal("v2 update did not leave an app container")
	}
	if got := containerImageID(t, v2Container); got == appV1 {
		t.Fatal("the v2 update did not change the image; the rollback would be trivial")
	}
	return &rollbackScenario{
		project: project, dir: dir, composePath: composePath, stateDir: stateDir,
		env: env, dcraftEnv: dcraftEnv, first: first, appV1Image: appV1, appV1Tag: tag,
		v2Container: v2Container, recordPath: recordPath, record: record,
	}
}

func rollbackJSONFor(t *testing.T, dir string, env []string, args ...string) (rollbackJSON, processResult) {
	t.Helper()
	full := append([]string{"rollback", "--path", dir, "--json", "--approve", "previous_version"}, args...)
	res := runProcess(t, dir, env, strings.NewReader(""), full...)
	parsed := rollbackJSON{}
	if strings.TrimSpace(res.stdout) != "" {
		parsed = parseRollback(t, res.stdout)
	}
	return parsed, res
}

func TestIntegrationRollbackRestoresImagesConfigAndKeepsData(t *testing.T) {
	s := setupRollbackScenario(t, false)

	// Write data into the named volume while v2 runs; rollback must not
	// restore volume contents.
	out, err := dockerRun(t, nil, "", "exec", s.v2Container, "sh", "-c", "mkdir -p /data && echo keep > /data/marker")
	if err != nil {
		t.Fatalf("write data marker: %v\n%s", err, out)
	}

	// A different stack stays untouched.
	canaryName := "dcraft-test-rb-canary-" + randomHex(t, 6)
	canaryDir := filepath.Join(t.TempDir(), canaryName)
	if err := os.MkdirAll(canaryDir, 0o755); err != nil {
		t.Fatal(err)
	}
	canaryCompose := filepath.Join(canaryDir, "compose.yaml")
	canaryDoc := fmt.Sprintf(`name: %s
services:
  hold:
    image: busybox:1.36
    command: ["sh", "-c", "echo canary > /data/marker; sleep 600"]
    volumes:
      - data:/data
volumes:
  data:
`, canaryName)
	if err := os.WriteFile(canaryCompose, []byte(canaryDoc), 0o644); err != nil {
		t.Fatal(err)
	}
	canaryUp := runProcess(t, canaryDir, s.dcraftEnv, strings.NewReader(""), "up", "--path", canaryDir, "--json")
	if canaryUp.code != 0 {
		t.Fatalf("canary up exit=%d stderr=%q", canaryUp.code, canaryUp.stderr)
	}
	t.Cleanup(func() { cleanupUpdateProject(t, s.env, canaryDir, canaryName, canaryCompose) })
	canaryID := composeInspect(t, s.env, canaryName, canaryCompose, "hold")
	canaryCreated := containerIDCreated(t, canaryID)

	parsed, res := rollbackJSONFor(t, s.dir, s.dcraftEnv)
	if res.code != 0 {
		t.Fatalf("rollback exit=%d stderr=%q stdout=%s", res.code, res.stderr, res.stdout)
	}
	report := parsed.Data.Rollback
	if !report.Applied || !report.ImageIDsVerified || report.AlreadyAtVersion {
		t.Fatalf("report = %+v", report)
	}
	if report.RecordStatus != "unchanged" || report.DataRestored || report.HooksRun {
		t.Fatalf("honesty fields = %+v", report)
	}
	if len(report.Readiness) != 1 || report.Readiness[0].Service != "app" || report.Readiness[0].Status != "healthy" {
		t.Fatalf("readiness = %+v", report.Readiness)
	}

	appContainer := composeInspect(t, s.env, s.project, s.composePath, "app")
	if appContainer == s.v2Container {
		t.Fatal("rollback did not recreate the app container")
	}
	if got := containerImageID(t, appContainer); got != s.appV1Image {
		t.Fatalf("running image = %s, want the recorded v1 image %s", got, s.appV1Image)
	}
	if got := containerEnv(t, appContainer, "APP_MARKER"); got != "v1" {
		t.Fatalf("APP_MARKER = %q, want the recorded execution configuration v1", got)
	}
	if out, err := dockerRun(t, nil, "", "exec", appContainer, "cat", "/marker"); err != nil || strings.TrimSpace(out) != "v1-image" {
		t.Fatalf("baked image marker = %q, err=%v: the exact recorded image was not restored", out, err)
	}
	if out, err := dockerRun(t, nil, "", "exec", appContainer, "cat", "/data/marker"); err != nil || strings.TrimSpace(out) != "keep" {
		t.Fatalf("volume data = %q, err=%v: rollback restored or lost data", out, err)
	}
	if got := imageID(t, s.appV1Tag); got != s.appV1Image {
		t.Fatalf("retention tag %q = %s, want %s", s.appV1Tag, got, s.appV1Image)
	}
	after, err := os.ReadFile(s.recordPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(s.record) {
		t.Fatalf("rollback replaced the good record:\nbefore=%s\nafter=%s", s.record, after)
	}

	// A repeated rollback is idempotent and reports no older version.
	again, res := rollbackJSONFor(t, s.dir, s.dcraftEnv)
	if res.code != 0 {
		t.Fatalf("second rollback exit=%d stderr=%q", res.code, res.stderr)
	}
	if !again.Data.Rollback.AlreadyAtVersion {
		t.Fatal("repeated rollback did not report that the recorded version is already running")
	}
	if !strings.Contains(strings.Join(again.Data.Rollback.Notices, "\n"), "더 오래된 버전은 없음") {
		t.Fatalf("notices = %v", again.Data.Rollback.Notices)
	}
	if got := composeInspect(t, s.env, s.project, s.composePath, "app"); got != appContainer {
		t.Fatalf("repeated rollback recreated the container: %s -> %s", appContainer, got)
	}
	if got := composeInspect(t, s.env, canaryName, canaryCompose, "hold"); got != canaryID {
		t.Fatalf("rollback changed another stack: %s -> %s", canaryID, got)
	}
	if got := containerIDCreated(t, canaryID); got != canaryCreated {
		t.Fatalf("rollback changed another stack's container Created: %s -> %s", canaryCreated, got)
	}
	if !strings.Contains(historyFor(t, s.stateDir), `"action":"rollback"`) {
		t.Fatal("successful rollback was not written to history")
	}
}

func TestIntegrationRollbackPrecheckAbortsBeforeChange(t *testing.T) {
	s := setupRollbackScenario(t, false)
	assertUnchanged := func(label string) {
		t.Helper()
		if got := composeInspect(t, s.env, s.project, s.composePath, "app"); got != s.v2Container {
			t.Fatalf("%s: rollback changed the running container %s -> %s", label, s.v2Container, got)
		}
		if got := containerImageID(t, s.v2Container); got == s.appV1Image {
			t.Fatalf("%s: rollback changed the running image to the recorded one", label)
		}
		after, err := os.ReadFile(s.recordPath)
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != string(s.record) {
			t.Fatalf("%s: rollback changed the good record", label)
		}
	}

	// The retention tag disappears while the image ID still exists. A
	// temporary extra tag keeps the image after the dcraft tag is removed;
	// otherwise Docker would delete the now-unreferenced image as well.
	tmpTag := "dcraft-test-rb-keep-" + randomHex(t, 6) + ":keep"
	if out, err := dockerRun(t, nil, "", "tag", s.appV1Image, tmpTag); err != nil {
		t.Fatalf("add temporary tag: %v\n%s", err, out)
	}
	t.Cleanup(func() { _, _ = dockerRun(t, nil, "", "image", "rm", "-f", tmpTag) })
	if out, err := dockerRun(t, nil, "", "image", "rm", "-f", s.appV1Tag); err != nil {
		t.Fatalf("remove retention tag: %v\n%s", err, out)
	}
	if got := imageID(t, s.appV1Image); got != s.appV1Image {
		t.Fatalf("image ID disappeared with the tag: %s", got)
	}
	parsed, res := rollbackJSONFor(t, s.dir, s.dcraftEnv)
	if res.code != 1 || !hasRollbackCategory(parsed, core.CategoryRollbackImageMissing) {
		t.Fatalf("missing tag: exit=%d diagnostics=%+v", res.code, parsed.Diagnostics)
	}
	assertUnchanged("missing tag")

	// Re-tag, then remove the image ID (which removes every tag).
	if out, err := dockerRun(t, nil, "", "tag", s.appV1Image, s.appV1Tag); err != nil {
		t.Fatalf("re-tag: %v\n%s", err, out)
	}
	if out, err := dockerRun(t, nil, "", "image", "rm", "-f", s.appV1Image); err != nil {
		t.Fatalf("remove image ID: %v\n%s", err, out)
	}
	parsed, res = rollbackJSONFor(t, s.dir, s.dcraftEnv)
	if res.code != 1 || !hasRollbackCategory(parsed, core.CategoryRollbackImageMissing) {
		t.Fatalf("missing image ID: exit=%d diagnostics=%+v", res.code, parsed.Diagnostics)
	}
	assertUnchanged("missing image ID")
}

func TestIntegrationRollbackCancellationIsNotSuccess(t *testing.T) {
	s := setupRollbackScenario(t, true)

	cmd := exec.Command(binaryPath, "rollback", "--path", s.dir, "--approve", "previous_version", "--json")
	cmd.Dir = s.dir
	cmd.Env = s.dcraftEnv
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// The v2 container ignores SIGTERM for up to 8s, so the apply is still in
	// progress when the signal arrives.
	time.Sleep(2 * time.Second)
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	err := cmd.Wait()
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("wait: %v", err)
		}
		code = exitErr.ExitCode()
	}
	if code != 130 {
		t.Fatalf("cancellation exit = %d, want 130 (stderr=%q)", code, stderr.String())
	}
	parsed := parseRollback(t, stdout.String())
	if parsed.Outcome != core.OutcomeCanceled || !hasRollbackCategory(parsed, core.CategoryCanceled) {
		t.Fatalf("result = %+v", parsed)
	}
	after, err := os.ReadFile(s.recordPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(s.record) {
		t.Fatalf("canceled rollback replaced the good record:\nbefore=%s\nafter=%s", s.record, after)
	}
}

func TestIntegrationRollbackWithoutRecordFailsCleanly(t *testing.T) {
	env := requireIntegration(t)
	project := "dcraft-test-rb-" + randomHex(t, 6)
	dir := filepath.Join(t.TempDir(), project)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	composePath := filepath.Join(dir, "compose.yaml")
	doc := fmt.Sprintf("name: %s\nservices:\n  app:\n    image: busybox:1.36\n    command: [\"sleep\", \"600\"]\n", project)
	if err := os.WriteFile(composePath, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	stateDir := t.TempDir()
	dcraftEnv := buildEnv(map[string]string{"DCRAFT_STATE_DIR": stateDir})
	t.Cleanup(func() { cleanupUpdateProject(t, env, dir, project, composePath) })

	parsed, res := rollbackJSONFor(t, dir, dcraftEnv)
	if res.code != 1 || !hasRollbackCategory(parsed, core.CategoryRollbackUnavailable) {
		t.Fatalf("no record: exit=%d outcome=%q diagnostics=%+v", res.code, parsed.Outcome, parsed.Diagnostics)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "ps", "-a", "-q", "--filter", "label=com.docker.compose.project="+project)
	out, _ := cmd.CombinedOutput()
	if strings.TrimSpace(string(out)) != "" {
		t.Fatalf("rollback without a record created containers: %s", out)
	}
}

// Rollback re-resolves exactly the secret references stored in the record, not
// the current .dcraft.yaml. After the current reference is changed from A to B,
// rollback still delivers A; a missing A aborts before any change.
func TestIntegrationRollbackUsesRecordedSecretReferences(t *testing.T) {
	env := requireIntegration(t)
	project := "dcraft-test-rbsec-" + randomHex(t, 6)
	dir := filepath.Join(t.TempDir(), project)
	if err := os.MkdirAll(filepath.Join(dir, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	stateDir := t.TempDir()
	composePath := filepath.Join(dir, "compose.yaml")
	secretA := "sentinel-rb-secret-a-" + randomHex(t, 8)
	secretB := "sentinel-rb-secret-b-" + randomHex(t, 8)
	dcraftEnv := func(extra map[string]string) []string {
		values := map[string]string{"DCRAFT_STATE_DIR": stateDir}
		for k, v := range extra {
			values[k] = v
		}
		return buildEnv(values)
	}
	envA := dcraftEnv(map[string]string{"DCRAFT_RB_SECRET": secretA})
	envB := dcraftEnv(map[string]string{"DCRAFT_RB_SECRET_B": secretB})
	envNoA := dcraftEnv(map[string]string{"DCRAFT_RB_SECRET_B": secretB})

	compose := fmt.Sprintf(`name: %s
services:
  app:
    build:
      context: ./app
    command: ["sh", "-c", "sleep 600"]
    environment:
      APP_SECRET: ${APP_SECRET:-missing}
      APP_MARKER: v1
    healthcheck:
      test: ["CMD", "true"]
      interval: 1s
      timeout: 1s
      retries: 5
`, project)
	if err := os.WriteFile(composePath, []byte(compose), 0o644); err != nil {
		t.Fatal(err)
	}
	dockerfile := "FROM busybox:1.36\nCOPY marker /marker\nCMD [\"sh\", \"-c\", \"sleep 600\"]\n"
	if err := os.WriteFile(filepath.Join(dir, "app", "Dockerfile"), []byte(dockerfile), 0o644); err != nil {
		t.Fatal(err)
	}
	markerPath := filepath.Join(dir, "app", "marker")
	if err := os.WriteFile(markerPath, []byte("v1-image"), 0o644); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, ".dcraft.yaml")
	writeSecret := func(ref string) {
		t.Helper()
		config := fmt.Sprintf(`secrets:
  - service: app
    phase: runtime
    delivery: environment
    source: env
    ref: %s
    target: APP_SECRET
`, ref)
		if err := os.WriteFile(configPath, []byte(config), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeSecret("DCRAFT_RB_SECRET")

	imagesCleanup := &updateImageCleanup{}
	imagesCleanup.addRef(project + "-app")
	imagesCleanup.register(t)
	t.Cleanup(func() { cleanupUpdateProject(t, env, dir, project, composePath) })

	if up := runProcess(t, dir, envA, strings.NewReader(""), "up", "--path", dir, "--json"); up.code != 0 {
		t.Fatalf("up v1 exit=%d stderr=%q", up.code, up.stderr)
	}
	v1ID := composeInspect(t, env, project, composePath, "app")
	if v1ID == "" {
		t.Fatal("v1 container missing")
	}
	waitForContainerHealthy(t, env, v1ID)

	first, res := updateJSONFor(t, dir, envA)
	requireSuccessUpdate(t, first, res)
	imagesCleanup.track(first)
	if got := dockerExec(t, env, v1ID, `printf %s "$APP_SECRET"`); got != secretA {
		t.Fatalf("v1 secret = %q", got)
	}
	recordPath := stateFileFor(t, stateDir)
	recordBefore, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}

	// The current configuration now names B and the source changes to v2.
	writeSecret("DCRAFT_RB_SECRET_B")
	if err := os.WriteFile(markerPath, []byte("v2-image"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, res := updateJSONFor(t, dir, envB)
	requireSuccessUpdate(t, second, res)
	imagesCleanup.track(second)
	v2ID := composeInspect(t, env, project, composePath, "app")
	if v2ID == "" || v2ID == v1ID {
		t.Fatalf("v2 update did not replace the container: %s", v2ID)
	}
	if got := dockerExec(t, env, v2ID, `printf %s "$APP_SECRET"`); got != secretB {
		t.Fatalf("v2 secret = %q, want the current source", got)
	}
	recordAfterUpdate, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(recordAfterUpdate) == string(recordBefore) {
		t.Fatal("v2 update did not publish the captured pre-update record")
	}

	// Without A, the recorded source is unavailable: abort before any change.
	failed, res := rollbackJSONFor(t, dir, envNoA)
	if res.code != 1 || !hasRollbackCategory(failed, core.CategoryRollbackSecretUnavailable) {
		t.Fatalf("rollback without A exit=%d diagnostics=%+v", res.code, failed.Diagnostics)
	}
	if got := composeInspect(t, env, project, composePath, "app"); got != v2ID {
		t.Fatalf("aborted rollback changed the container: %s -> %s", v2ID, got)
	}
	afterAbort, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterAbort) != string(recordAfterUpdate) {
		t.Fatal("aborted rollback changed the record")
	}

	// With A present, rollback restores v1 and delivers the recorded A, not B.
	restored, res := rollbackJSONFor(t, dir, envA)
	if res.code != 0 || restored.Outcome != "success" {
		t.Fatalf("rollback exit=%d stderr=%q diagnostics=%+v", res.code, res.stderr, restored.Diagnostics)
	}
	restoredID := composeInspect(t, env, project, composePath, "app")
	if restoredID == "" || restoredID == v2ID {
		t.Fatalf("rollback did not restore v1: v2=%s now=%s", v2ID, restoredID)
	}
	if got := containerImageID(t, restoredID); got != recordedServices(first)["app"] {
		t.Fatalf("restored image = %q, want v1", got)
	}
	if got := dockerExec(t, env, restoredID, `printf %s "$APP_SECRET"`); got != secretA {
		t.Fatalf("restored secret = %q, want the recorded A (not current B %q)", got, secretB)
	}
}
