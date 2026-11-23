package e2e

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rootmyself/dcraft/internal/core"
)

// buildSecretCompose writes an isolated real-Docker project with one local
// build service and a build-phase file source outside the build context.
func buildSecretCompose(t *testing.T, name, dockerfile string) (projectDir, composePath, sourcePath string) {
	t.Helper()
	projectDir = t.TempDir()
	composePath = filepath.Join(projectDir, "compose.yaml")
	if err := os.WriteFile(composePath, []byte(`name: `+name+`
services:
  app:
    image: `+name+`:img
    build:
      context: .
      dockerfile: Dockerfile
    command: ["sh", "-c", "sleep 120"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "Dockerfile"), []byte(dockerfile), 0o644); err != nil {
		t.Fatal(err)
	}
	sourceDir := t.TempDir()
	sourcePath = filepath.Join(sourceDir, "build-token.txt")
	return projectDir, composePath, sourcePath
}

func cleanupBuildSecretStack(t *testing.T, env []string, projectDir, name string, composePath, image string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	down := composeCommand(ctx, env, projectDir, name, []string{composePath}, "down", "-v", "--remove-orphans")
	if out, err := down.CombinedOutput(); err != nil {
		t.Logf("cleanup down for %s failed: %v\n%s", name, err, out)
	}
	refs := []string{image}
	listCtx, listCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer listCancel()
	list := exec.CommandContext(listCtx, "docker", "image", "ls",
		"--filter", "reference=dcraft-good/"+name+"-app", "--format", "{{.Repository}}:{{.Tag}}")
	if out, err := list.CombinedOutput(); err == nil {
		for _, ref := range strings.Fields(string(out)) {
			refs = append(refs, ref)
		}
	}
	for _, ref := range refs {
		rmCtx, rmCancel := context.WithTimeout(context.Background(), 60*time.Second)
		_, _ = exec.CommandContext(rmCtx, "docker", "rmi", "-f", ref).CombinedOutput()
		rmCancel()
	}
}

func dockerRunFile(t *testing.T, env []string, image, path string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "run", "--rm", "--entrypoint", "cat", image, path)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("docker run %s cat %s: %v\n%s", image, path, err, out)
	}
	return strings.TrimSpace(string(out))
}

func dockerText(t *testing.T, env []string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("docker %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func goodVersionRecords(t *testing.T, stateDir string) []string {
	t.Helper()
	var paths []string
	_ = filepath.Walk(stateDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		if info.Name() == "state.json" {
			paths = append(paths, path)
		}
		return nil
	})
	return paths
}

func TestIntegrationBuildSecretsBuildKitCacheRecordAndCleanup(t *testing.T) {
	env := requireIntegration(t)
	name := "dcraft-test-build-" + randomHex(t, 6)
	stateDir := t.TempDir()
	v1 := "sentinel-build-v1-" + randomHex(t, 8)
	v2 := "sentinel-build-v2-" + randomHex(t, 8)
	projectDir, composePath, sourcePath := buildSecretCompose(t, name, `FROM alpine:3.20
RUN --mount=type=secret,id=BUILD_TOKEN sh -c 'cat /run/secrets/BUILD_TOKEN > /baked.txt; echo "baked $(cat /baked.txt)"'
CMD ["sh", "-c", "sleep 120"]
`)
	if err := os.WriteFile(sourcePath, []byte(v1+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	composeHash := sha256File(t, composePath)
	dockerfileHash := sha256File(t, filepath.Join(projectDir, "Dockerfile"))
	image := name + ":img"
	t.Cleanup(func() { cleanupBuildSecretStack(t, env, projectDir, name, composePath, image) })

	dcraftEnv := buildEnv(map[string]string{"DCRAFT_STATE_DIR": stateDir})
	run := func(timeout time.Duration, args ...string) processResult {
		t.Helper()
		res := runDcraftProcess(t, projectDir, dcraftEnv, timeout, args...)
		assertNoSentinelIn(t, "dcraft "+strings.Join(args, " ")+" stdout", res.stdout, v1, v2)
		assertNoSentinelIn(t, "dcraft "+strings.Join(args, " ")+" stderr", res.stderr, v1, v2)
		return res
	}

	// A second stack must remain untouched by every operation below.
	otherDir := t.TempDir()
	otherName := "dcraft-test-other-" + randomHex(t, 6)
	otherCompose := filepath.Join(otherDir, "compose.yaml")
	if err := os.WriteFile(otherCompose, []byte(`name: `+otherName+`
services:
  keep:
    image: alpine:3.20
    command: ["sh", "-c", "sleep 300"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	upCtx, upCancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer upCancel()
	if out, err := composeCommand(upCtx, env, otherDir, otherName, []string{otherCompose}, "up", "-d").CombinedOutput(); err != nil {
		t.Fatalf("other stack up: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		_, _ = composeCommand(ctx, env, otherDir, otherName, []string{otherCompose}, "down", "-v", "--remove-orphans").CombinedOutput()
	})
	otherIDs := containerIDs(t, env, otherDir, otherName, []string{otherCompose})
	if len(otherIDs) != 1 {
		t.Fatalf("other stack containers = %v", otherIDs)
	}

	add := run(30*time.Second, "secrets", "--add", "-s", "app", "--phase", "build",
		"--source", "file", "--ref", sourcePath, "--target", "BUILD_TOKEN", "-p", name, "--json")
	if add.code != 0 {
		t.Fatalf("add build secret exit = %d\n%s\n%s", add.code, add.stdout, add.stderr)
	}
	secretsReport := parseSecrets(t, add.stdout).Data.Secrets
	if secretsReport.BuildOverrideFile == "" {
		t.Fatalf("build override path missing: %+v", secretsReport)
	}
	if _, err := os.Stat(secretsReport.BuildOverrideFile); err != nil {
		t.Fatalf("build override not written: %v", err)
	}
	assertNoSentinelIn(t, "build override", string(mustReadFile(t, secretsReport.BuildOverrideFile)), v1, v2)

	first := run(300*time.Second, "update", "-p", name, "--no-cache", "--timeout", "30", "--json")
	if first.code != 0 {
		t.Fatalf("first update exit = %d\n%s\n%s", first.code, first.stdout, first.stderr)
	}
	if got := dockerRunFile(t, env, image, "/baked.txt"); got != v1 {
		t.Fatalf("baked value after first build = %q, want v1", got)
	}
	// A record describes the deployment that ran before an update, so the
	// first update on an empty stack records nothing. The second update
	// captures the v1 deployment and its build reference.
	second := run(300*time.Second, "update", "-p", name, "--timeout", "30", "--json")
	if second.code != 0 {
		t.Fatalf("second update exit = %d\n%s\n%s", second.code, second.stdout, second.stderr)
	}
	buildSecretsDir := filepath.Join(filepath.Dir(secretsReport.BuildOverrideFile), "build-secrets")
	var materialized []string
	_ = filepath.Walk(buildSecretsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		materialized = append(materialized, path)
		return nil
	})
	if len(materialized) != 1 {
		t.Fatalf("build materializations = %v, want one private file", materialized)
	}
	if info, err := os.Stat(materialized[0]); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("build materialization mode = %v, %v", info, err)
	}
	if data, err := os.ReadFile(materialized[0]); err != nil || string(data) != v1+"\n" {
		t.Fatalf("build materialization = %q, %v", data, err)
	}
	if sha256File(t, composePath) != composeHash || sha256File(t, filepath.Join(projectDir, "Dockerfile")) != dockerfileHash {
		t.Fatal("dcraft modified the user Compose file or Dockerfile")
	}

	// The build log printed the value; image metadata must still not contain it.
	for _, text := range []string{
		dockerText(t, env, "history", "--no-trunc", image),
		dockerText(t, env, "image", "inspect", image),
	} {
		assertNoSentinelIn(t, "image metadata", text, v1, v2)
	}
	recordPaths := goodVersionRecords(t, stateDir)
	if len(recordPaths) == 0 {
		t.Fatal("update did not write a good-version record")
	}
	record := string(mustReadFile(t, recordPaths[0]))
	if !strings.Contains(record, `"phase": "build"`) || !strings.Contains(record, sourcePath) {
		t.Fatalf("good-version record does not reference the build secret:\n%s", record)
	}
	assertNoSentinelIn(t, "good-version record", record, v1, v2)

	// Changing only the source must NOT invalidate the BuildKit cache.
	if err := os.WriteFile(sourcePath, []byte(v2+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stale := run(300*time.Second, "update", "-p", name, "--timeout", "30", "--json")
	if stale.code != 0 {
		t.Fatalf("cached update exit = %d\n%s\n%s", stale.code, stale.stdout, stale.stderr)
	}
	if got := dockerRunFile(t, env, image, "/baked.txt"); got != v1 {
		t.Fatalf("cached rebuild baked %q; the surfaced limitation says the old value stays until --no-cache", got)
	}
	staleReport := parseUpdate(t, stale.stdout)
	if !strings.Contains(strings.Join(staleReport.Data.Update.Notices, "\n"), "--no-cache") {
		t.Fatalf("cached rebuild did not surface the cache limitation: %v", staleReport.Data.Update.Notices)
	}

	fresh := run(300*time.Second, "update", "-p", name, "--no-cache", "--timeout", "30", "--json")
	if fresh.code != 0 {
		t.Fatalf("fresh update exit = %d\n%s\n%s", fresh.code, fresh.stdout, fresh.stderr)
	}
	if got := dockerRunFile(t, env, image, "/baked.txt"); got != v2 {
		t.Fatalf("fresh rebuild baked %q, want v2", got)
	}
	for _, text := range []string{
		dockerText(t, env, "history", "--no-trunc", image),
		dockerText(t, env, "image", "inspect", image),
	} {
		assertNoSentinelIn(t, "image metadata after fresh build", text, v1, v2)
	}

	if ids := containerIDs(t, env, otherDir, otherName, []string{otherCompose}); len(ids) != 1 || ids[0] != otherIDs[0] {
		t.Fatalf("another stack was modified: before=%v after=%v", otherIDs, ids)
	}

	down := run(180*time.Second, "down", "-p", name, "--json")
	if down.code != 0 {
		t.Fatalf("down exit = %d\n%s", down.code, down.stderr)
	}
	if _, err := os.Stat(secretsReport.BuildOverrideFile); !os.IsNotExist(err) {
		t.Fatalf("stack teardown left the managed build override: %v", err)
	}
	if _, err := os.Stat(buildSecretsDir); !os.IsNotExist(err) {
		t.Fatalf("stack teardown left materialized build secrets: %v", err)
	}
	scanSecretMetadata(t, stateDir, projectDir, v1, v2)
}

func TestIntegrationBuildSecretsFailingBuildRedactsLogsAndMissingSourceChangesNothing(t *testing.T) {
	env := requireIntegration(t)
	name := "dcraft-test-buildfail-" + randomHex(t, 6)
	stateDir := t.TempDir()
	sentinel := "sentinel-build-fail-" + randomHex(t, 8)
	projectDir, composePath, sourcePath := buildSecretCompose(t, name, `FROM alpine:3.20
RUN --mount=type=secret,id=BUILD_TOKEN sh -c 'cat /run/secrets/BUILD_TOKEN; exit 7'
CMD ["sh", "-c", "sleep 120"]
`)
	if err := os.WriteFile(sourcePath, []byte(sentinel+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	composeHash := sha256File(t, composePath)
	dockerfileHash := sha256File(t, filepath.Join(projectDir, "Dockerfile"))
	image := name + ":img"
	t.Cleanup(func() { cleanupBuildSecretStack(t, env, projectDir, name, composePath, image) })
	dcraftEnv := buildEnv(map[string]string{"DCRAFT_STATE_DIR": stateDir})

	add := runDcraftProcess(t, projectDir, dcraftEnv, 30*time.Second, "secrets", "--add", "-s", "app",
		"--phase", "build", "--source", "file", "--ref", sourcePath, "--target", "BUILD_TOKEN", "-p", name, "--json")
	if add.code != 0 {
		t.Fatalf("add exit = %d\n%s", add.code, add.stderr)
	}

	failed := runDcraftProcess(t, projectDir, dcraftEnv, 300*time.Second, "update", "-p", name, "--timeout", "30", "--json")
	if failed.code == 0 {
		t.Fatalf("failing Dockerfile update succeeded:\n%s", failed.stdout)
	}
	combined := failed.stdout + failed.stderr
	assertNoSentinelIn(t, "failing build output", combined, sentinel)
	if out, err := exec.Command("docker", "image", "inspect", image).CombinedOutput(); err == nil {
		t.Fatalf("failing build left an image:\n%s", out)
	}
	parsed := parseUpdate(t, failed.stdout)
	if !hasUpdateCategory(parsed, string(core.CategoryUpdateFailed)) {
		t.Fatalf("failing build diagnostics = %+v", parsed.Diagnostics)
	}

	// A missing source must fail before the build and change nothing.
	if err := os.Remove(sourcePath); err != nil {
		t.Fatal(err)
	}
	missing := runDcraftProcess(t, projectDir, dcraftEnv, 120*time.Second, "update", "-p", name, "--timeout", "30", "--json")
	if missing.code == 0 {
		t.Fatalf("missing build source update succeeded:\n%s", missing.stdout)
	}
	missingParsed := parseUpdate(t, missing.stdout)
	if !hasUpdateCategory(missingParsed, string(core.CategorySecretSource)) {
		t.Fatalf("missing source diagnostics = %+v", missingParsed.Diagnostics)
	}
	if out, err := exec.Command("docker", "image", "inspect", image).CombinedOutput(); err == nil {
		t.Fatalf("missing source left an image:\n%s", out)
	}
	if sha256File(t, composePath) != composeHash || sha256File(t, filepath.Join(projectDir, "Dockerfile")) != dockerfileHash {
		t.Fatal("missing source changed the user Compose file or Dockerfile")
	}
	scanSecretMetadata(t, stateDir, projectDir, sentinel)
}

func TestIntegrationBuildSecretsWarnsWithoutMountAndLeavesDockerfileAlone(t *testing.T) {
	env := requireIntegration(t)
	name := "dcraft-test-buildwarn-" + randomHex(t, 6)
	stateDir := t.TempDir()
	sentinel := "sentinel-build-warn-" + randomHex(t, 8)
	projectDir, composePath, sourcePath := buildSecretCompose(t, name, `FROM alpine:3.20
RUN echo no-secret-consumed
CMD ["sh", "-c", "sleep 120"]
`)
	if err := os.WriteFile(sourcePath, []byte(sentinel+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dockerfilePath := filepath.Join(projectDir, "Dockerfile")
	dockerfileHash := sha256File(t, dockerfilePath)
	composeHash := sha256File(t, composePath)
	image := name + ":img"
	t.Cleanup(func() { cleanupBuildSecretStack(t, env, projectDir, name, composePath, image) })
	dcraftEnv := buildEnv(map[string]string{"DCRAFT_STATE_DIR": stateDir})

	add := runDcraftProcess(t, projectDir, dcraftEnv, 30*time.Second, "secrets", "--add", "-s", "app",
		"--phase", "build", "--source", "file", "--ref", sourcePath, "--target", "BUILD_TOKEN", "-p", name, "--json")
	if add.code != 0 {
		t.Fatalf("add exit = %d\n%s", add.code, add.stderr)
	}
	update := runDcraftProcess(t, projectDir, dcraftEnv, 300*time.Second, "update", "-p", name, "--timeout", "30", "--json")
	if update.code != 0 {
		t.Fatalf("warning update exit = %d\n%s\n%s", update.code, update.stdout, update.stderr)
	}
	assertNoSentinelIn(t, "warning update output", update.stdout+update.stderr, sentinel)
	notices := strings.Join(parseUpdate(t, update.stdout).Data.Update.Notices, "\n")
	for _, want := range []string{"Dockerfile", dockerfilePath, "--mount=type=secret,id=BUILD_TOKEN", "자동으로 수정하지 않음", "캐시"} {
		if !strings.Contains(notices, want) {
			t.Fatalf("notices missing %q:\n%s", want, notices)
		}
	}
	if sha256File(t, dockerfilePath) != dockerfileHash || sha256File(t, composePath) != composeHash {
		t.Fatal("dcraft modified the Dockerfile or Compose file")
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
