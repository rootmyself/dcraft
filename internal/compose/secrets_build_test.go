package compose

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/execx"
	"github.com/rootmyself/dcraft/internal/execx/execxtest"
	"github.com/rootmyself/dcraft/internal/secretref"
	"github.com/rootmyself/dcraft/internal/stack"
	"github.com/rootmyself/dcraft/internal/state"
)

func fileHash(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func buildSecretRuntime(t *testing.T, env map[string]string) (*state.Store, *secretref.Runtime) {
	t.Helper()
	store := state.New(privateStateDir(t))
	return store, secretref.NewRuntime(store, func(name string) string { return env[name] })
}

func refForKey(t *testing.T, project, service, target string) secretref.Reference {
	t.Helper()
	return secretref.Reference{
		Service: service, Phase: secretref.PhaseBuild, Delivery: secretref.DeliveryFile,
		Source: secretref.SourceEnv, Ref: "REFERENCE", Target: target,
	}
}

func buildSecretProject(t *testing.T, config string, hasBuild bool) (string, core.Target) {
	t.Helper()
	dir := t.TempDir()
	composePath := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(composePath, []byte("services:\n  api:\n    build: .\n  web:\n    image: busybox\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM busybox\nRUN --mount=type=secret,id=API_TOKEN cat /run/secrets/API_TOKEN > /out\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".dcraft.yaml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = hasBuild
	return dir, core.Target{ProjectDir: dir, Files: []string{composePath}, ProjectName: "dcraft-test-build"}
}

func buildSecretRunner(t *testing.T, project, buildOutput string) *execxtest.Runner {
	t.Helper()
	config := `{"name":"dcraft-test-build","services":{"api":{"image":"dcraft-test-build-api","build":{"context":"` + project + `","dockerfile":"Dockerfile"}},"web":{"image":"busybox"}}}`
	runner := &execxtest.Runner{}
	runner.Handler = func(cmd execx.Command) (execx.Result, error) {
		line := strings.Join(cmd.Args, " ")
		switch {
		case strings.Contains(line, "buildx version"):
			return execx.Result{Stdout: []byte("github.com/docker/buildx v0.38.1")}, nil
		case strings.Contains(line, " config --format json"):
			return execx.Result{Stdout: []byte(config)}, nil
		case strings.Contains(line, " build"):
			return execx.Result{Stdout: []byte(buildOutput)}, nil
		default:
			return execx.Result{Stderr: []byte("unexpected " + line), ExitCode: 9}, nil
		}
	}
	return runner
}

func TestBuildAppendsManagedBuildOverrideAndRedactsOutput(t *testing.T) {
	sentinel := "sentinel-build-log-7d1a"
	project, target := buildSecretProject(t, `secrets:
  - service: api
    phase: build
    delivery: file
    source: env
    ref: API_BUILD_TOKEN
    target: API_TOKEN
`, true)
	composeHash := fileHash(t, filepath.Join(project, "compose.yaml"))
	dockerfileHash := fileHash(t, filepath.Join(project, "Dockerfile"))
	runner := buildSecretRunner(t, project, "step: "+sentinel)
	store, rt := buildSecretRuntime(t, map[string]string{"API_BUILD_TOKEN": sentinel})
	c := New(runner, nil)
	c.SetSecretRuntime(rt)

	res, err := c.Build(context.Background(), target, []string{"api"}, stack.BuildOptions{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if strings.Contains(res.Output, sentinel) || !strings.Contains(res.Output, "[redacted]") {
		t.Fatalf("build output was not redacted: %q", res.Output)
	}
	notices := strings.Join(res.Notices, "\n")
	for _, want := range []string{"캐시", "--no-cache", "BuildKit", "Dockerfile"} {
		if !strings.Contains(notices, want) {
			t.Fatalf("notices missing %q:\n%s", want, notices)
		}
	}

	var buildCall []string
	for _, call := range runner.Calls() {
		line := strings.Join(call.Args, " ")
		if strings.Contains(line, " build") && strings.HasSuffix(line, " build api") {
			buildCall = call.Args
		}
	}
	if buildCall == nil {
		t.Fatalf("no build call: %v", runner.CallLines())
	}
	override := ""
	for i := 0; i+1 < len(buildCall); i++ {
		if buildCall[i] == "-f" && strings.HasSuffix(buildCall[i+1], "compose.build-secrets.yaml") {
			override = buildCall[i+1]
		}
	}
	if override == "" {
		t.Fatalf("build did not append the managed build override: %v", buildCall)
	}
	for _, arg := range buildCall {
		if arg == "--build-arg" || strings.Contains(arg, sentinel) {
			t.Fatalf("build used an ARG or a value: %v", buildCall)
		}
	}
	data, err := os.ReadFile(override)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "build:") || strings.Contains(string(data), sentinel) {
		t.Fatalf("build override = %s", data)
	}
	if fileHash(t, filepath.Join(project, "compose.yaml")) != composeHash || fileHash(t, filepath.Join(project, "Dockerfile")) != dockerfileHash {
		t.Fatal("dcraft modified the user Compose file or Dockerfile")
	}
	// Environment-source build materialization exists only during the build.
	envPath, err := rt.BuildMaterializedPath(target, refForKey(t, project, "api", "API_TOKEN"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(envPath); !os.IsNotExist(err) {
		t.Fatalf("environment build materialization survived the build: %v", err)
	}
	_ = store
}

func TestBuildRejectsNonBuildServiceBeforeRunningCompose(t *testing.T) {
	project, target := buildSecretProject(t, `secrets:
  - service: web
    phase: build
    delivery: file
    source: env
    ref: WEB_BUILD_TOKEN
`, false)
	runner := buildSecretRunner(t, project, "should not build")
	_, rt := buildSecretRuntime(t, map[string]string{"WEB_BUILD_TOKEN": "value"})
	c := New(runner, nil)
	c.SetSecretRuntime(rt)

	_, err := c.Build(context.Background(), target, []string{"web"}, stack.BuildOptions{})
	if err == nil {
		t.Fatal("Build accepted a non-build service")
	}
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Diagnostics[0].Category != core.CategoryBuildServiceInvalid {
		t.Fatalf("error = %v, want build_service_invalid", err)
	}
	for _, line := range runner.CallLines() {
		if strings.Contains(line, " compose ") && strings.Contains(line, " build ") {
			t.Fatalf("compose build ran for a non-build service: %v", runner.CallLines())
		}
	}
}

func TestBuildFailsWhenBuildKitIsUnavailable(t *testing.T) {
	project, target := buildSecretProject(t, `secrets:
  - service: api
    phase: build
    delivery: file
    source: env
    ref: API_BUILD_TOKEN
`, true)
	for _, tc := range []struct {
		name string
		env  map[string]string
	}{
		{name: "legacy builder selected", env: map[string]string{"DOCKER_BUILDKIT": "0"}},
		{name: "buildx missing", env: map[string]string{"DOCKER_BUILDX_MISSING": "1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := buildSecretRunner(t, project, "should not build")
			if tc.env["DOCKER_BUILDX_MISSING"] == "1" {
				base := runner.Handler
				runner.Handler = func(cmd execx.Command) (execx.Result, error) {
					if strings.Contains(strings.Join(cmd.Args, " "), "buildx version") {
						return execx.Result{Stderr: []byte("docker: 'buildx' is not a docker command"), ExitCode: 1}, nil
					}
					return base(cmd)
				}
			}
			values := map[string]string{"API_BUILD_TOKEN": "value"}
			for k, v := range tc.env {
				values[k] = v
			}
			c := New(runner, func(name string) string { return values[name] })
			_, rt := buildSecretRuntime(t, values)
			c.SetSecretRuntime(rt)
			_, err := c.Build(context.Background(), target, []string{"api"}, stack.BuildOptions{})
			if err == nil {
				t.Fatal("Build succeeded without BuildKit secret mount support")
			}
			if !strings.Contains(err.Error(), "BuildKit") {
				t.Fatalf("error = %v", err)
			}
			for _, line := range runner.CallLines() {
				if strings.HasSuffix(line, " build api") {
					t.Fatalf("compose build ran without secret mount support: %v", runner.CallLines())
				}
			}
		})
	}
}

func TestUpUsesNoBuildWhenBuildSecretsExist(t *testing.T) {
	project, target := buildSecretProject(t, `secrets:
  - service: api
    phase: build
    delivery: file
    source: env
    ref: API_BUILD_TOKEN
    target: API_TOKEN
`, true)
	config := `{"name":"dcraft-test-build","services":{"api":{"build":{"context":"` + project + `","dockerfile":"Dockerfile"}},"web":{"image":"busybox"}}}`
	runner := &execxtest.Runner{}
	var upArgs []string
	runner.Handler = func(cmd execx.Command) (execx.Result, error) {
		line := strings.Join(cmd.Args, " ")
		switch {
		case strings.Contains(line, " config --format json"):
			return execx.Result{Stdout: []byte(config)}, nil
		case strings.Contains(line, " up --detach"):
			upArgs = cmd.Args
			return execx.Result{Stdout: []byte("Container started")}, nil
		default:
			return execx.Result{Stderr: []byte("unexpected " + line), ExitCode: 9}, nil
		}
	}
	accessed := map[string]int{}
	c := New(runner, func(name string) string { accessed[name]++; return "" })
	_, rt := buildSecretRuntime(t, nil)
	c.SetSecretRuntime(rt)

	res, err := c.Up(context.Background(), target, nil)
	if err != nil {
		t.Fatalf("Up: %v", err)
	}
	if upArgs == nil {
		t.Fatalf("no up call: %v", runner.CallLines())
	}
	found := false
	for _, arg := range upArgs {
		if arg == "--no-build" {
			found = true
		}
	}
	if !found {
		t.Fatalf("up did not prevent an implicit build without secrets: %v", upArgs)
	}
	if accessed["API_BUILD_TOKEN"] != 0 {
		t.Fatalf("up resolved a build secret: %v", accessed)
	}
	if !strings.Contains(strings.Join(res.Notices, "\n"), "dcraft update") {
		t.Fatalf("up did not guide the user to the explicit build: %v", res.Notices)
	}
}

func TestBuildWithoutBuildReferencesKeepsUserInputsUnchanged(t *testing.T) {
	project, target := buildSecretProject(t, `secrets:
  - service: web
    phase: runtime
    delivery: environment
    source: env
    ref: RUNTIME_TOKEN
`, true)
	runner := buildSecretRunner(t, project, "built")
	c := New(runner, nil)
	res, err := c.Build(context.Background(), target, []string{"api"}, stack.BuildOptions{NoCache: true})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(res.Notices) != 0 {
		t.Fatalf("notices = %v", res.Notices)
	}
	lines := runner.CallLines()
	if len(lines) != 1 {
		t.Fatalf("calls = %v, want only the build", lines)
	}
	if strings.Contains(lines[0], "compose.build-secrets.yaml") || strings.Contains(lines[0], "--build-arg") {
		t.Fatalf("build with no build references changed its inputs: %q", lines[0])
	}
	if !strings.Contains(lines[0], "build --no-cache api") {
		t.Fatalf("cache option was not passed through: %q", lines[0])
	}
}
