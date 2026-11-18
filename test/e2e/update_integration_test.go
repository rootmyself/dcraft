package e2e

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func dockerRun(t *testing.T, env []string, dir string, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func dockerMust(t *testing.T, args ...string) string {
	t.Helper()
	out, err := dockerRun(t, nil, "", args...)
	if err != nil {
		t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func containerIDCreated(t *testing.T, id string) string {
	t.Helper()
	return dockerMust(t, "inspect", "-f", "{{.Id}}|{{.Created}}", id)
}

func containerImageID(t *testing.T, id string) string {
	t.Helper()
	return dockerMust(t, "inspect", "-f", "{{.Image}}", id)
}

func imageID(t *testing.T, ref string) string {
	t.Helper()
	return dockerMust(t, "image", "inspect", ref, "--format", "{{.Id}}")
}

func updateJSONFor(t *testing.T, projectDir string, env []string, args ...string) (updateJSON, processResult) {
	t.Helper()
	full := append([]string{"update", "--path", projectDir, "--json"}, args...)
	res := runProcess(t, projectDir, env, strings.NewReader(""), full...)
	var parsed updateJSON
	if res.stdout != "" {
		parsed = parseUpdate(t, res.stdout)
	}
	return parsed, res
}

func stateFileFor(t *testing.T, stateDir string) string {
	t.Helper()
	path := findStateFileIfAny(filepath.Join(stateDir, "stacks"), "state.json")
	return path
}

func requireSuccessUpdate(t *testing.T, parsed updateJSON, res processResult) {
	t.Helper()
	if res.code != 0 || parsed.Outcome != "success" {
		t.Fatalf("update failed: exit=%d outcome=%q diagnostics=%+v stderr=%q", res.code, parsed.Outcome, parsed.Diagnostics, res.stderr)
	}
}

func readinessByService(parsed updateJSON) map[string]string {
	out := map[string]string{}
	for _, svc := range parsed.Data.Update.Readiness {
		out[svc.Service] = string(svc.Status)
	}
	return out
}

func recordedServices(parsed updateJSON) map[string]string {
	out := map[string]string{}
	if parsed.Data.Update.GoodVersion == nil {
		return out
	}
	for _, svc := range parsed.Data.Update.GoodVersion.Services {
		out[svc.Service] = svc.ImageID
	}
	return out
}

func recordedTags(parsed updateJSON) map[string]string {
	out := map[string]string{}
	if parsed.Data.Update.GoodVersion == nil {
		return out
	}
	for _, svc := range parsed.Data.Update.GoodVersion.Services {
		out[svc.Service] = svc.RetentionTag
	}
	return out
}

// updateImageCleanup removes only images and tags this test created. It is
// registered before the project down cleanup, so it runs after the containers
// are gone (cleanups run LIFO). Shared images such as busybox:1.36 are never
// deleted; only their dcraft-owned retention tags are removed.
type updateImageCleanup struct {
	refs []string
	ids  []string
}

func (c *updateImageCleanup) track(parsed updateJSON) {
	if parsed.Data.Update.GoodVersion == nil {
		return
	}
	for _, svc := range parsed.Data.Update.GoodVersion.Services {
		c.refs = append(c.refs, svc.RetentionTag)
		if svc.Build {
			c.ids = append(c.ids, svc.ImageID)
		}
	}
}

func (c *updateImageCleanup) addRef(ref string) { c.refs = append(c.refs, ref) }

func (c *updateImageCleanup) register(t *testing.T) {
	t.Cleanup(func() {
		seen := map[string]bool{}
		for _, id := range c.ids {
			if seen[id] {
				continue
			}
			seen[id] = true
			_, _ = dockerRun(t, nil, "", "image", "rm", "-f", id)
		}
		seen = map[string]bool{}
		for _, ref := range c.refs {
			if ref == "" || seen[ref] {
				continue
			}
			seen[ref] = true
			_, _ = dockerRun(t, nil, "", "image", "rm", "-f", ref)
		}
	})
}

func cleanupUpdateProject(t *testing.T, env []string, dir, project, file string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "compose", "-p", project, "-f", file, "down", "-v", "--remove-orphans", "-t", "1")
	cmd.Dir = dir
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Logf("cleanup down %s: %v\n%s", project, err, out)
	}
}

// startLocalRegistry runs a temporary registry on the Docker VM's host network
// so the Docker daemon can reach 127.0.0.1:PORT (Docker Desktop does not route
// the VM loopback to a host-published port). Only this container is removed.
func startLocalRegistry(t *testing.T) (string, func()) {
	t.Helper()
	if _, err := exec.Command("docker", "image", "inspect", "registry:2").CombinedOutput(); err != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "docker", "pull", "registry:2")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("registry:2 is not available and could not be pulled: %v\n%s", err, out)
		}
	}
	port, err := freeLocalPort()
	if err != nil {
		t.Skipf("no free local port for the registry: %v", err)
	}
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	name := "dcraft-test-upd-registry-" + randomHex(t, 6)
	out, err := dockerRun(t, nil, "", "run", "-d", "--name", name, "--network", "host",
		"-e", "REGISTRY_HTTP_ADDR="+addr, "registry:2")
	if err != nil {
		t.Skipf("start host-network registry: %v\n%s", err, out)
	}
	stop := func() {
		_, _ = dockerRun(t, nil, "", "rm", "-f", name)
	}
	t.Cleanup(stop)
	for i := 0; i < 40; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		cmd := exec.CommandContext(ctx, "docker", "run", "--rm", "--network", "host", "busybox:1.36",
			"wget", "-qO-", "http://"+addr+"/v2/")
		err := cmd.Run()
		cancel()
		if err == nil {
			return addr, stop
		}
		time.Sleep(250 * time.Millisecond)
	}
	stop()
	t.Skipf("registry at %s did not become reachable from the Docker daemon", addr)
	return "", nil
}

func freeLocalPort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

func registryPush(t *testing.T, source, destination string) {
	t.Helper()
	if out, err := dockerRun(t, nil, "", "tag", source, destination); err != nil {
		t.Fatalf("tag %s -> %s: %v\n%s", source, destination, err, out)
	}
	var last string
	for i := 0; i < 3; i++ {
		out, err := dockerRun(t, nil, "", "push", destination)
		if err == nil {
			return
		}
		last = out
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("push %s: %s", destination, last)
}

func waitForUpdateLock(t *testing.T, stateDir string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		found := false
		_ = filepath.Walk(filepath.Join(stateDir, "stacks"), func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || info.Name() != ".lock" {
				return nil
			}
			data, readErr := os.ReadFile(path)
			if readErr == nil && strings.Contains(string(data), `"action":"update"`) {
				found = true
			}
			return nil
		})
		if found {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("update did not take the per-stack lock")
}

// ---------------------------------------------------------------------------
// real Docker tests
// ---------------------------------------------------------------------------

func TestIntegrationUpdateRepeatSourceImageAndRetention(t *testing.T) {
	env := requireIntegration(t)
	project := "dcraft-test-upd-" + randomHex(t, 6)
	dir := filepath.Join(t.TempDir(), project)
	if err := os.MkdirAll(filepath.Join(dir, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	stateDir := t.TempDir()
	dcraftEnv := buildEnv(map[string]string{"DCRAFT_STATE_DIR": stateDir, "APP_TOKEN": "integration-secret-token"})
	composePath := filepath.Join(dir, "compose.yaml")

	registry, _ := startLocalRegistry(t)
	apiRef := registry + "/api:v1"
	registryPush(t, "busybox:1.36", apiRef)
	if out, err := dockerRun(t, nil, "", "image", "inspect", "alpine:3.20"); err != nil {
		t.Skipf("alpine:3.20 is required for the tag-move scenario: %v\n%s", err, out)
	}

	imagesCleanup := &updateImageCleanup{}
	imagesCleanup.addRef(apiRef)
	imagesCleanup.addRef(project + "-app")
	imagesCleanup.register(t)
	writeCompose := func(appMarker string) {
		doc := fmt.Sprintf(`name: %s
services:
  app:
    build:
      context: ./app
    command: ["sh", "-c", "sleep 600"]
    environment:
      APP_TOKEN: ${APP_TOKEN:-missing}
    healthcheck:
      test: ["CMD", "true"]
      interval: 1s
      timeout: 1s
      retries: 5
  api:
    image: %s
    command: ["sh", "-c", "sleep 600"]
  other:
    image: busybox:1.36
    command: ["sleep", "600"]
`, project, apiRef)
		if err := os.WriteFile(composePath, []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
		dockerfile := "FROM busybox:1.36\nCOPY marker /marker\nCMD [\"sh\", \"-c\", \"sleep 600\"]\n"
		if err := os.WriteFile(filepath.Join(dir, "app", "Dockerfile"), []byte(dockerfile), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "app", "marker"), []byte(appMarker), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeCompose("v1")
	imagesCleanup.register(t)
	t.Cleanup(func() { cleanupUpdateProject(t, env, dir, project, composePath) })

	first, res := updateJSONFor(t, dir, dcraftEnv)
	requireSuccessUpdate(t, first, res)
	imagesCleanup.track(first)
	imagesCleanup.track(first)
	if got := readinessByService(first)["app"]; got != "healthy" {
		t.Fatalf("app readiness = %q", got)
	}
	if got := readinessByService(first)["api"]; got != "running_unverified" {
		t.Fatalf("api readiness = %q", got)
	}
	if first.Data.Update.Built[0] != "app" {
		t.Fatalf("built = %v", first.Data.Update.Built)
	}
	if strings.Join(first.Data.Update.Pulled, ",") != "api,other" {
		t.Fatalf("pulled = %v", first.Data.Update.Pulled)
	}
	appID := composeInspect(t, env, project, composePath, "app")
	apiID := composeInspect(t, env, project, composePath, "api")
	otherID := composeInspect(t, env, project, composePath, "other")
	appCreated := containerIDCreated(t, appID)
	apiCreated := containerIDCreated(t, apiID)
	otherCreated := containerIDCreated(t, otherID)
	if got := containerEnv(t, appID, "APP_TOKEN"); got != "integration-secret-token" {
		t.Fatalf("app env = %q", got)
	}
	recordPath := stateFileFor(t, stateDir)
	recordData, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(recordData), "integration-secret-token") {
		t.Fatalf("good record contains a resolved secret value:\n%s", recordData)
	}
	for _, want := range []string{`"files"`, `"project_name"`, `"image_id"`, `"retention_tag"`} {
		if !strings.Contains(string(recordData), want) {
			t.Fatalf("good record misses %q:\n%s", want, recordData)
		}
	}

	// Unchanged repeat: no container is recreated.
	second, res := updateJSONFor(t, dir, dcraftEnv)
	requireSuccessUpdate(t, second, res)
	imagesCleanup.track(second)
	for name, before := range map[string]string{"app": appID, "api": apiID, "other": otherID} {
		if after := composeInspect(t, env, project, composePath, name); after != before {
			t.Fatalf("repeat update recreated %s: %s -> %s", name, before, after)
		}
	}
	if got := containerIDCreated(t, appID); got != appCreated {
		t.Fatalf("app Created changed on a no-op update: %s -> %s", appCreated, got)
	}
	if got := containerIDCreated(t, apiID); got != apiCreated {
		t.Fatalf("api Created changed on a no-op update: %s -> %s", apiCreated, got)
	}
	if got := containerIDCreated(t, otherID); got != otherCreated {
		t.Fatalf("other Created changed on a no-op update: %s -> %s", otherCreated, got)
	}

	// Source change: only the local build service is rebuilt/recreated.
	writeCompose("v2")
	third, res := updateJSONFor(t, dir, dcraftEnv)
	requireSuccessUpdate(t, third, res)
	imagesCleanup.track(third)
	newAppID := composeInspect(t, env, project, composePath, "app")
	if newAppID == appID {
		t.Fatal("source change did not recreate the local build service")
	}
	if got := composeInspect(t, env, project, composePath, "api"); got != apiID {
		t.Fatalf("source change recreated an unrelated image service: %s -> %s", apiID, got)
	}
	if got := composeInspect(t, env, project, composePath, "other"); got != otherID {
		t.Fatalf("source change recreated an unrelated service: %s -> %s", otherID, got)
	}

	// Move the image-only tag in the local registry: the real image identity
	// must be picked up and only that service recreated.
	tags := recordedTags(third)
	recordedAPI := recordedServices(third)["api"]
	if recordedAPI != containerImageID(t, apiID) {
		t.Fatalf("recorded api image %q does not match the running container %q", recordedAPI, containerImageID(t, apiID))
	}
	registryPush(t, "alpine:3.20", apiRef)
	fourth, res := updateJSONFor(t, dir, dcraftEnv)
	requireSuccessUpdate(t, fourth, res)
	imagesCleanup.track(fourth)
	newAPIID := composeInspect(t, env, project, composePath, "api")
	if newAPIID == apiID {
		t.Fatal("moved tag did not recreate the image-only service")
	}
	newAPIImage := containerImageID(t, newAPIID)
	if newAPIImage == recordedAPI {
		t.Fatal("moved tag kept the old image ID")
	}
	if got := composeInspect(t, env, project, composePath, "app"); got != newAppID {
		t.Fatalf("image move recreated the local build service: %s -> %s", newAppID, got)
	}
	if got := composeInspect(t, env, project, composePath, "other"); got != otherID {
		t.Fatalf("image move recreated an unrelated service: %s -> %s", otherID, got)
	}
	if recordedServices(fourth)["api"] != newAPIImage {
		t.Fatalf("new image identity was not recorded: %+v", fourth.Data.Update.GoodVersion)
	}

	// The retained tag still resolves to the recorded image after the
	// container is replaced again.
	oldTag := tags["api"]
	if got := imageID(t, oldTag); got != recordedAPI {
		t.Fatalf("retention tag %q resolves to %q, want %q", oldTag, got, recordedAPI)
	}
	forced, res := updateJSONFor(t, dir, dcraftEnv, "--force-recreate")
	requireSuccessUpdate(t, forced, res)
	imagesCleanup.track(forced)
	if got := composeInspect(t, env, project, composePath, "api"); got == newAPIID {
		t.Fatal("--force-recreate did not recreate the container")
	}
	if got := imageID(t, oldTag); got != recordedAPI {
		t.Fatalf("retention tag moved after recreation: %q != %q", got, recordedAPI)
	}

	// A second stack stays untouched.
	canaryName := "dcraft-test-upd-canary-" + randomHex(t, 6)
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
	canaryUp := runProcess(t, canaryDir, dcraftEnv, strings.NewReader(""), "up", "--path", canaryDir, "--json")
	if canaryUp.code != 0 {
		t.Fatalf("canary up exit=%d stderr=%q", canaryUp.code, canaryUp.stderr)
	}
	t.Cleanup(func() { cleanupUpdateProject(t, env, canaryDir, canaryName, canaryCompose) })
	canaryID := composeInspect(t, env, canaryName, canaryCompose, "hold")
	canaryCreated := containerIDCreated(t, canaryID)
	canaryVolume := canaryName + "_data"
	last, res := updateJSONFor(t, dir, dcraftEnv)
	requireSuccessUpdate(t, last, res)
	imagesCleanup.track(last)
	if got := composeInspect(t, env, canaryName, canaryCompose, "hold"); got != canaryID {
		t.Fatalf("update recreated another stack's container: %s -> %s", canaryID, got)
	}
	if got := containerIDCreated(t, canaryID); got != canaryCreated {
		t.Fatalf("update changed another stack's container Created: %s -> %s", canaryCreated, got)
	}
	canaryData := ""
	for i := 0; i < 20; i++ {
		out, err := dockerRun(t, nil, "", "run", "--rm", "-v", canaryVolume+":/data", "busybox:1.36", "cat", "/data/marker")
		if err == nil && strings.TrimSpace(out) == "canary" {
			canaryData = strings.TrimSpace(out)
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if canaryData != "canary" {
		t.Fatalf("update changed another stack's volume data: %q", canaryData)
	}

	// After down, the retention tag still protects the recorded image.
	downCtx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	down := exec.CommandContext(downCtx, "docker", "compose", "-p", project, "-f", composePath, "down", "-v", "--remove-orphans")
	down.Dir = dir
	down.Env = env
	if out, err := down.CombinedOutput(); err != nil {
		t.Fatalf("down: %v\n%s", err, out)
	}
	if got := imageID(t, oldTag); got != recordedAPI {
		t.Fatalf("retention tag lost after down: %q != %q", got, recordedAPI)
	}
}

func TestIntegrationUpdateReadinessFailuresKeepRecord(t *testing.T) {
	env := requireIntegration(t)
	project := "dcraft-test-upd-" + randomHex(t, 6)
	dir := filepath.Join(t.TempDir(), project)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	stateDir := t.TempDir()
	dcraftEnv := buildEnv(map[string]string{"DCRAFT_STATE_DIR": stateDir})
	composePath := filepath.Join(dir, "compose.yaml")

	base := fmt.Sprintf(`name: %s
services:
  healthy:
    image: busybox:1.36
    command: ["sh", "-c", "sleep 600"]
    healthcheck:
      test: ["CMD", "true"]
      interval: 1s
      timeout: 1s
      retries: 5
  unverified:
    image: busybox:1.36
    command: ["sleep", "600"]
`, project)
	if err := os.WriteFile(composePath, []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}
	imagesCleanup := &updateImageCleanup{}
	imagesCleanup.register(t)
	t.Cleanup(func() { cleanupUpdateProject(t, env, dir, project, composePath) })

	first, res := updateJSONFor(t, dir, dcraftEnv)
	requireSuccessUpdate(t, first, res)
	imagesCleanup.track(first)
	imagesCleanup.track(first)
	if got := readinessByService(first)["healthy"]; got != "healthy" {
		t.Fatalf("healthy = %q", got)
	}
	if got := readinessByService(first)["unverified"]; got != "running_unverified" {
		t.Fatalf("unverified = %q", got)
	}
	if first.Data.Update.Verified {
		t.Fatal("a no-healthcheck service was counted as verified")
	}
	recordPath := stateFileFor(t, stateDir)
	before, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}

	assertRecordUnchanged := func(label string) {
		t.Helper()
		after, err := os.ReadFile(recordPath)
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != string(before) {
			t.Fatalf("%s replaced the previous good record:\nbefore=%s\nafter=%s", label, before, after)
		}
	}

	failing := func(extra string) {
		doc := base + extra
		if err := os.WriteFile(composePath, []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	failing(`  broken:
    image: busybox:1.36
    command: ["sh", "-c", "sleep 600"]
    healthcheck:
      test: ["CMD", "false"]
      interval: 1s
      timeout: 1s
      retries: 1
`)
	parsed, res := updateJSONFor(t, dir, dcraftEnv, "--timeout", "15")
	if res.code != 1 || !hasUpdateCategory(parsed, "readiness_failed") {
		t.Fatalf("unhealthy update: exit=%d outcome=%q diagnostics=%+v", res.code, parsed.Outcome, parsed.Diagnostics)
	}
	if !strings.Contains(parsed.Diagnostics[0].Message, "broken=unhealthy") {
		t.Fatalf("diagnostic does not report the observed state: %q", parsed.Diagnostics[0].Message)
	}
	assertRecordUnchanged("unhealthy update")

	failing(`  broken:
    image: busybox:1.36
    command: ["sh", "-c", "sleep 0.2; exit 1"]
    restart: always
`)
	parsed, res = updateJSONFor(t, dir, dcraftEnv, "--timeout", "15")
	if res.code != 1 || !hasUpdateCategory(parsed, "readiness_failed") {
		t.Fatalf("crash-loop update: exit=%d outcome=%q diagnostics=%+v", res.code, parsed.Outcome, parsed.Diagnostics)
	}
	if !strings.Contains(parsed.Diagnostics[0].Message, "broken=restarting") {
		t.Fatalf("diagnostic does not report the crash loop: %q", parsed.Diagnostics[0].Message)
	}
	assertRecordUnchanged("crash-loop update")

	failing(`  broken:
    image: busybox:1.36
    command: ["sh", "-c", "sleep 1; exit 0"]
`)
	parsed, res = updateJSONFor(t, dir, dcraftEnv, "--timeout", "15")
	if res.code != 1 || !hasUpdateCategory(parsed, "readiness_failed") {
		t.Fatalf("early-exit update: exit=%d outcome=%q diagnostics=%+v", res.code, parsed.Outcome, parsed.Diagnostics)
	}
	if !strings.Contains(parsed.Diagnostics[0].Message, "broken=exited") {
		t.Fatalf("diagnostic does not report the early exit: %q", parsed.Diagnostics[0].Message)
	}
	assertRecordUnchanged("early-exit update")

	failing(`  broken:
    image: busybox:1.36
    command: ["sh", "-c", "sleep 600"]
    healthcheck:
      test: ["CMD-SHELL", "sleep 600"]
      interval: 1s
      timeout: 1s
      retries: 50
`)
	parsed, res = updateJSONFor(t, dir, dcraftEnv, "--timeout", "3")
	if res.code != 1 || !hasUpdateCategory(parsed, "readiness_failed") {
		t.Fatalf("timeout update: exit=%d outcome=%q diagnostics=%+v", res.code, parsed.Outcome, parsed.Diagnostics)
	}
	if !strings.Contains(parsed.Diagnostics[0].Message, "broken=timeout") {
		t.Fatalf("diagnostic does not report the timeout: %q", parsed.Diagnostics[0].Message)
	}
	if !strings.Contains(parsed.Diagnostics[0].Message, "3초") {
		t.Fatalf("timeout message = %q", parsed.Diagnostics[0].Message)
	}
	assertRecordUnchanged("timeout update")
}

func TestIntegrationUpdateCancellationKeepsRecord(t *testing.T) {
	env := requireIntegration(t)
	project := "dcraft-test-upd-" + randomHex(t, 6)
	dir := filepath.Join(t.TempDir(), project)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	stateDir := t.TempDir()
	dcraftEnv := buildEnv(map[string]string{"DCRAFT_STATE_DIR": stateDir})
	composePath := filepath.Join(dir, "compose.yaml")
	doc := fmt.Sprintf(`name: %s
services:
  slow:
    image: busybox:1.36
    command: ["sh", "-c", "trap '' TERM; sleep 600"]
    stop_grace_period: 8s
`, project)
	if err := os.WriteFile(composePath, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	imagesCleanup := &updateImageCleanup{}
	imagesCleanup.register(t)
	t.Cleanup(func() { cleanupUpdateProject(t, env, dir, project, composePath) })

	first, res := updateJSONFor(t, dir, dcraftEnv)
	requireSuccessUpdate(t, first, res)
	imagesCleanup.track(first)
	imagesCleanup.track(first)
	recordPath := stateFileFor(t, stateDir)
	before, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(binaryPath, "update", "--path", dir, "--force-recreate", "--json")
	cmd.Dir = dir
	cmd.Env = dcraftEnv
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// The apply stops the SIGTERM-ignoring container for up to 8s.
	time.Sleep(2 * time.Second)
	_ = cmd.Process.Signal(os.Interrupt)
	err = cmd.Wait()
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
	parsed := parseUpdate(t, stdout.String())
	if parsed.Outcome != "canceled" || !hasUpdateCategory(parsed, "canceled") {
		t.Fatalf("result = %+v", parsed)
	}
	after, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("cancellation replaced the good record:\nbefore=%s\nafter=%s", before, after)
	}
}

func TestIntegrationUpdateCoordinatesWithConcurrentUp(t *testing.T) {
	env := requireIntegration(t)
	project := "dcraft-test-upd-" + randomHex(t, 6)
	dir := filepath.Join(t.TempDir(), project)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	stateDir := t.TempDir()
	dcraftEnv := buildEnv(map[string]string{"DCRAFT_STATE_DIR": stateDir})
	composePath := filepath.Join(dir, "compose.yaml")
	doc := fmt.Sprintf(`name: %s
services:
  app:
    build:
      context: .
    command: ["sh", "-c", "sleep 600"]
`, project)
	if err := os.WriteFile(composePath, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	dockerfile := "FROM busybox:1.36\nARG CACHEBUST\nRUN sleep 6\nCMD [\"sh\", \"-c\", \"sleep 600\"]\n"
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(dockerfile), 0o644); err != nil {
		t.Fatal(err)
	}
	imagesCleanup := &updateImageCleanup{}
	imagesCleanup.register(t)
	t.Cleanup(func() { cleanupUpdateProject(t, env, dir, project, composePath) })

	// A different stack must stay untouched.
	canaryName := "dcraft-test-upd-canary-" + randomHex(t, 6)
	canaryDir := filepath.Join(t.TempDir(), canaryName)
	if err := os.MkdirAll(canaryDir, 0o755); err != nil {
		t.Fatal(err)
	}
	canaryCompose := filepath.Join(canaryDir, "compose.yaml")
	canaryDoc := fmt.Sprintf("name: %s\nservices:\n  hold:\n    image: busybox:1.36\n    command: [\"sleep\", \"600\"]\n", canaryName)
	if err := os.WriteFile(canaryCompose, []byte(canaryDoc), 0o644); err != nil {
		t.Fatal(err)
	}
	canaryUp := runProcess(t, canaryDir, dcraftEnv, strings.NewReader(""), "up", "--path", canaryDir, "--json")
	if canaryUp.code != 0 {
		t.Fatalf("canary up exit=%d stderr=%q", canaryUp.code, canaryUp.stderr)
	}
	t.Cleanup(func() { cleanupUpdateProject(t, env, canaryDir, canaryName, canaryCompose) })
	canaryID := composeInspect(t, env, canaryName, canaryCompose, "hold")

	// Start the long update in the background, wait until it holds the stack
	// lock, then try an up on the same stack.
	type result struct {
		code   int
		stdout string
		stderr string
		err    error
	}
	done := make(chan result, 1)
	go func() {
		cmd := exec.Command(binaryPath, "update", "--path", dir, "--json")
		cmd.Dir = dir
		cmd.Env = dcraftEnv
		var stdout, stderr strings.Builder
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		code := 0
		if err != nil {
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) {
				done <- result{err: err}
				return
			}
			code = exitErr.ExitCode()
		}
		done <- result{code: code, stdout: stdout.String(), stderr: stderr.String()}
	}()
	waitForUpdateLock(t, stateDir)

	up := runProcess(t, dir, dcraftEnv, strings.NewReader(""), "up", "--path", dir, "--json")
	if up.code != 1 {
		t.Fatalf("concurrent up exit = %d, want 1 (stderr=%q stdout=%s)", up.code, up.stderr, up.stdout)
	}
	upParsed := parseUpdate(t, up.stdout)
	if !hasUpdateCategory(upParsed, "stack_busy") {
		t.Fatalf("concurrent up diagnostics = %+v", upParsed.Diagnostics)
	}

	winner := <-done
	if winner.err != nil {
		t.Fatalf("update: %v", winner.err)
	}
	if winner.code != 0 {
		t.Fatalf("update exit = %d (stderr=%q stdout=%s)", winner.code, winner.stderr, winner.stdout)
	}
	if parsed := parseUpdate(t, winner.stdout); parsed.Outcome != "success" {
		t.Fatalf("update outcome = %q", parsed.Outcome)
	} else {
		imagesCleanup.track(parsed)
	}
	if got := composeInspect(t, env, canaryName, canaryCompose, "hold"); got != canaryID {
		t.Fatalf("concurrent update changed another stack: %s -> %s", canaryID, got)
	}
}

func TestIntegrationUpdateBuildCacheChoice(t *testing.T) {
	env := requireIntegration(t)
	project := "dcraft-test-upd-" + randomHex(t, 6)
	dir := filepath.Join(t.TempDir(), project)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	stateDir := t.TempDir()
	dcraftEnv := buildEnv(map[string]string{"DCRAFT_STATE_DIR": stateDir})
	composePath := filepath.Join(dir, "compose.yaml")
	doc := fmt.Sprintf(`name: %s
services:
  app:
    build:
      context: .
    command: ["sh", "-c", "sleep 600"]
`, project)
	if err := os.WriteFile(composePath, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	dockerfile := "FROM busybox:1.36\nRUN date +%s%N > /buildstamp\nCMD [\"sh\", \"-c\", \"sleep 600\"]\n"
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(dockerfile), 0o644); err != nil {
		t.Fatal(err)
	}
	imagesCleanup := &updateImageCleanup{}
	imagesCleanup.addRef(project + "-app")
	imagesCleanup.register(t)
	t.Cleanup(func() { cleanupUpdateProject(t, env, dir, project, composePath) })

	first, res := updateJSONFor(t, dir, dcraftEnv)
	requireSuccessUpdate(t, first, res)
	imagesCleanup.track(first)
	appID := composeInspect(t, env, project, composePath, "app")
	firstImage := containerImageID(t, appID)

	// A normal update keeps the build cache: the date layer is not re-executed,
	// so the image identity and the container stay the same.
	second, res := updateJSONFor(t, dir, dcraftEnv)
	requireSuccessUpdate(t, second, res)
	imagesCleanup.track(second)
	if got := composeInspect(t, env, project, composePath, "app"); got != appID {
		t.Fatalf("cached update recreated the container: %s -> %s", appID, got)
	}
	if got := containerImageID(t, appID); got != firstImage {
		t.Fatalf("cached update changed the image: %s -> %s", firstImage, got)
	}

	// --no-cache re-executes the build and applies the new image.
	third, res := updateJSONFor(t, dir, dcraftEnv, "--no-cache")
	requireSuccessUpdate(t, third, res)
	imagesCleanup.track(third)
	newAppID := composeInspect(t, env, project, composePath, "app")
	if newAppID == appID {
		t.Fatal("--no-cache did not rebuild/recreate the service")
	}
	newImage := containerImageID(t, newAppID)
	if newImage == firstImage {
		t.Fatal("--no-cache kept the same image identity")
	}
	if third.Data.Update.Options["no_cache"] != true {
		t.Fatalf("payload options = %+v", third.Data.Update.Options)
	}
}
