package e2e

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestIntegrationAWSSecretsRuntimeBuildAndRestart uses the real Docker daemon
// with a controlled AWS CLI shim. It proves runtime environment and file
// delivery, restart survival, and build-phase delivery through the same
// registered source. No real AWS credentials or API calls are involved.
func TestIntegrationAWSSecretsRuntimeBuildAndRestart(t *testing.T) {
	env := requireIntegration(t)
	name := "dcraft-test-aws-" + randomHex(t, 6)
	stateDir := t.TempDir()
	shimDir, awsLog := writeAWSShim(t)
	envSentinel := "aws-real-env-" + randomHex(t, 8)
	userSentinel := "aws-real-user-" + randomHex(t, 8)
	fileSentinel := "aws-real-file-" + randomHex(t, 8)
	buildSentinel := "aws-real-build-" + randomHex(t, 8)

	projectDir := t.TempDir()
	composePath := filepath.Join(projectDir, "compose.yaml")
	if err := os.WriteFile(composePath, []byte(fmt.Sprintf(`name: %s
services:
  web:
    image: busybox:1.36
    command: ["sh", "-c", "sleep 300"]
  filesvc:
    image: busybox:1.36
    command: ["sh", "-c", "sleep 300"]
  builder:
    image: %s:img
    build:
      context: .
      dockerfile: Dockerfile
    command: ["sh", "-c", "sleep 300"]
`, name, name)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "Dockerfile"), []byte(`FROM busybox:1.36
RUN --mount=type=secret,id=BUILD_TOKEN cat /run/secrets/BUILD_TOKEN > /baked.txt
CMD ["sh", "-c", "sleep 300"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, ".dcraft.yaml"), []byte(`secrets:
  - service: web
    phase: runtime
    delivery: environment
    source: aws
    ref: prod/db
    field: password
    target: DB_PASSWORD
  - service: filesvc
    phase: runtime
    delivery: file
    source: aws
    ref: prod/token
    target: token
  - service: builder
    phase: build
    delivery: file
    source: aws
    ref: build/token
    field: token
    target: BUILD_TOKEN
`), 0o644); err != nil {
		t.Fatal(err)
	}
	composeHash := sha256File(t, composePath)
	dockerfilePath := filepath.Join(projectDir, "Dockerfile")
	dockerfileHash := sha256File(t, dockerfilePath)
	sentinels := []string{envSentinel, userSentinel, fileSentinel, buildSentinel}
	image := name + ":img"
	t.Cleanup(func() { cleanupBuildSecretStack(t, env, projectDir, name, composePath, image) })

	dcraftEnv := buildEnv(map[string]string{
		"PATH":                 shimDir + ":" + os.Getenv("PATH"),
		"DCRAFT_STATE_DIR":     stateDir,
		"AWS_SHIM_LOG":         awsLog,
		"AWS_SHIM_PROD_DB":     jsonQuote(t, `{"username":"`+userSentinel+`","password":"`+envSentinel+`"}`),
		"AWS_SHIM_PROD_TOKEN":  jsonQuote(t, fileSentinel),
		"AWS_SHIM_BUILD_TOKEN": jsonQuote(t, `{"token":"`+buildSentinel+`"}`),
	})
	run := func(timeout time.Duration, args ...string) processResult {
		t.Helper()
		res := runDcraftProcess(t, projectDir, dcraftEnv, timeout, args...)
		assertNoSentinelIn(t, "dcraft "+strings.Join(args, " ")+" stdout", res.stdout, sentinels...)
		assertNoSentinelIn(t, "dcraft "+strings.Join(args, " ")+" stderr", res.stderr, sentinels...)
		return res
	}

	// Another stack must remain untouched by every operation.
	otherDir := t.TempDir()
	otherName := "dcraft-test-aws-other-" + randomHex(t, 6)
	otherCompose := filepath.Join(otherDir, "compose.yaml")
	if err := os.WriteFile(otherCompose, []byte(`name: `+otherName+`
services:
  keep:
    image: busybox:1.36
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

	up := run(180*time.Second, "up", "-p", name, "-s", "web", "-s", "filesvc", "--json")
	if up.code != 0 {
		t.Fatalf("up exit = %d\n%s\n%s", up.code, up.stdout, up.stderr)
	}
	calls := awsCalls(t, awsLog)
	if len(calls) != 2 {
		t.Fatalf("aws calls after up = %v, want runtime env + file only", calls)
	}
	if strings.Contains(strings.Join(calls, "\n"), "build/token") {
		t.Fatalf("up fetched a build-phase reference: %v", calls)
	}
	if got := dockerExec(t, env, name+"-web-1", "printenv DB_PASSWORD"); got != envSentinel {
		t.Fatalf("runtime environment value = %q", got)
	}
	if got := dockerExec(t, env, name+"-filesvc-1", "cat /run/secrets/token"); got != fileSentinel {
		t.Fatalf("runtime file value = %q", got)
	}
	if sha256File(t, composePath) != composeHash {
		t.Fatal("up modified the user Compose file")
	}

	// Restart is not a configuration update: both deliveries keep the values
	// already in the container/materialized file and no source is fetched.
	restart := run(120*time.Second, "restart", "-p", name, "-s", "filesvc", "--json")
	if restart.code != 0 {
		t.Fatalf("restart exit = %d\n%s\n%s", restart.code, restart.stdout, restart.stderr)
	}
	after := awsCalls(t, awsLog)
	if len(after) != 2 {
		t.Fatalf("aws calls after restart = %v, want no additional fetch", after)
	}
	if got := dockerExec(t, env, name+"-filesvc-1", "cat /run/secrets/token"); got != fileSentinel {
		t.Fatalf("file value after restart = %q", got)
	}
	if got := dockerExec(t, env, name+"-web-1", "printenv DB_PASSWORD"); got != envSentinel {
		t.Fatalf("environment value after restart = %q", got)
	}

	// Build-phase AWS uses the same source registration and the BuildKit
	// mount path. The private transient file is removed after the build.
	update := run(300*time.Second, "update", "-p", name, "--no-cache", "--timeout", "30", "--json")
	if update.code != 0 {
		t.Fatalf("update exit = %d\n%s\n%s", update.code, update.stdout, update.stderr)
	}
	buildCalls := awsCalls(t, awsLog)
	buildFetches := 0
	for _, call := range buildCalls {
		if strings.Contains(call, "build/token") {
			buildFetches++
		}
	}
	if buildFetches != 1 {
		t.Fatalf("build secret fetches = %d (%v), want 1", buildFetches, buildCalls)
	}
	if got := dockerRunFile(t, env, image, "/baked.txt"); got != buildSentinel {
		t.Fatalf("baked build value = %q", got)
	}
	buildSecretsDir := filepath.Join(stateDir, "stacks")
	if entries := findBuildSecretFiles(t, buildSecretsDir); len(entries) != 0 {
		t.Fatalf("transient AWS build materializations remain: %v", entries)
	}
	for _, text := range []string{
		dockerText(t, env, "history", "--no-trunc", image),
		dockerText(t, env, "image", "inspect", image),
	} {
		assertNoSentinelIn(t, "image metadata", text, sentinels...)
	}
	if sha256File(t, composePath) != composeHash || sha256File(t, dockerfilePath) != dockerfileHash {
		t.Fatal("dcraft modified the user Compose file or Dockerfile")
	}
	if ids := containerIDs(t, env, otherDir, otherName, []string{otherCompose}); len(ids) != 1 || ids[0] != otherIDs[0] {
		t.Fatalf("another stack was modified: before=%v after=%v", otherIDs, ids)
	}
	scanSecretMetadata(t, stateDir, projectDir, sentinels...)
}

func findBuildSecretFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		if strings.Contains(path, string(filepath.Separator)+"build-secrets"+string(filepath.Separator)) {
			out = append(out, path)
		}
		return nil
	})
	return out
}
