package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/stack"
)

func freeHostPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func composeRun(t *testing.T, env []string, dir, name string, files []string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	cmd := composeCommand(ctx, env, dir, name, files, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("docker compose %s (%s) failed: %v\n%s", strings.Join(args, " "), name, err, out)
	}
	return string(out)
}

func cleanupCompose(t *testing.T, env []string, dir, name string, files []string, profiles []string) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		args := []string{}
		for _, p := range profiles {
			args = append(args, "--profile", p)
		}
		args = append(args, "down", "-v", "--remove-orphans")
		cmd := composeCommand(ctx, env, dir, name, files, args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Logf("cleanup %s failed: %v\n%s", name, err, out)
		}
	})
}

func serviceContainerID(t *testing.T, env []string, dir, name string, files []string, service string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := composeCommand(ctx, env, dir, name, files, "ps", "-q", service).Output()
	if err != nil {
		t.Fatalf("compose ps -q %s: %v", service, err)
	}
	id := strings.TrimSpace(string(out))
	if id == "" {
		t.Fatalf("service %s has no container in project %s", service, name)
	}
	return id
}

func containerCmd(t *testing.T, id string) []string {
	t.Helper()
	out, err := exec.Command("docker", "inspect", "-f", "{{json .Config.Cmd}}", id).Output()
	if err != nil {
		t.Fatalf("inspect %s: %v", id, err)
	}
	var cmd []string
	if err := json.Unmarshal(out, &cmd); err != nil {
		t.Fatalf("parse command %q: %v", out, err)
	}
	return cmd
}

func TestIntegrationEnvironmentAliasesProduceSeparateStacks(t *testing.T) {
	env := requireIntegration(t)
	suffix := randomHex(t, 5)
	stateDir := t.TempDir()
	projectDir := t.TempDir()

	devName := "dcraft-test-" + suffix + "-dev"
	prodName := "dcraft-test-" + suffix + "-prod"

	base := `services:
  web:
    image: busybox:1.36
    command: ["sleep", "${SLEEP_SECONDS:-180}"]
    ports:
      - "127.0.0.1::80"
  common:
    image: busybox:1.36
    command: ["sleep", "180"]
`
	devFile := fmt.Sprintf(`name: %s
services:
  devonly:
    image: busybox:1.36
    command: ["sleep", "180"]
    profiles: ["devtools"]
`, devName)
	prodFile := fmt.Sprintf(`name: %s
services:
  prodonly:
    image: busybox:1.36
    command: ["sleep", "180"]
    profiles: ["prodtools"]
`, prodName)
	config := fmt.Sprintf(`environments:
  dev:
    compose_files: [compose.yaml, compose.dev.yaml]
    env_files: [.env.dev]
    profiles: [devtools]
  prod:
    compose_files: [compose.yaml, compose.prod.yaml]
    env_files: [.env.prod]
    profiles: [prodtools]
`)
	for name, body := range map[string]string{
		"compose.yaml":      base,
		"compose.dev.yaml":  devFile,
		"compose.prod.yaml": prodFile,
		".env.dev":          "SLEEP_SECONDS=240\n",
		".env.prod":         "SLEEP_SECONDS=300\n",
		".dcraft.yaml":      config,
	} {
		if err := os.WriteFile(filepath.Join(projectDir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	devFiles := []string{filepath.Join(projectDir, "compose.yaml"), filepath.Join(projectDir, "compose.dev.yaml")}
	prodFiles := []string{filepath.Join(projectDir, "compose.yaml"), filepath.Join(projectDir, "compose.prod.yaml")}
	devEnvFile := filepath.Join(projectDir, ".env.dev")
	prodEnvFile := filepath.Join(projectDir, ".env.prod")
	cleanupCompose(t, env, projectDir, devName, devFiles, []string{"devtools"})
	cleanupCompose(t, env, projectDir, prodName, prodFiles, []string{"prodtools"})

	composeRun(t, env, projectDir, devName, devFiles, "--env-file", devEnvFile, "--profile", "devtools", "up", "-d", "--wait", "--wait-timeout", "90")
	composeRun(t, env, projectDir, prodName, prodFiles, "--env-file", prodEnvFile, "--profile", "prodtools", "up", "-d", "--wait", "--wait-timeout", "90")

	devIDs := containerIDs(t, env, projectDir, devName, devFiles)
	prodIDs := containerIDs(t, env, projectDir, prodName, prodFiles)
	if len(devIDs) != 3 || len(prodIDs) != 3 {
		t.Fatalf("containers dev=%v prod=%v, want 3 each", devIDs, prodIDs)
	}
	for _, id := range append(append([]string{}, devIDs...), prodIDs...) {
		assertRunning(t, id)
	}
	for _, id := range devIDs {
		for _, other := range prodIDs {
			if id == other {
				t.Fatalf("environments share container %s", id)
			}
		}
	}
	if cmd := containerCmd(t, serviceContainerID(t, env, projectDir, devName, devFiles, "web")); len(cmd) < 2 || cmd[1] != "240" {
		t.Fatalf("dev web command = %v, want sleep 240 from .env.dev", cmd)
	}
	if cmd := containerCmd(t, serviceContainerID(t, env, projectDir, prodName, prodFiles, "web")); len(cmd) < 2 || cmd[1] != "300" {
		t.Fatalf("prod web command = %v, want sleep 300 from .env.prod", cmd)
	}

	dcraftEnv := buildEnv(map[string]string{"DCRAFT_STATE_DIR": stateDir})
	devRes := runProcess(t, projectDir, dcraftEnv, strings.NewReader(""), "status", "--env", "dev", "--json")
	if devRes.code != 0 {
		t.Fatalf("dev status exit = %d, stderr=%q", devRes.code, devRes.stderr)
	}
	prodRes := runProcess(t, projectDir, dcraftEnv, strings.NewReader(""), "status", "--env", "prod", "--json")
	if prodRes.code != 0 {
		t.Fatalf("prod status exit = %d, stderr=%q", prodRes.code, prodRes.stderr)
	}
	devParsed := parseStatus(t, devRes.stdout)
	prodParsed := parseStatus(t, prodRes.stdout)
	if devParsed.Target.Environment != "dev" || prodParsed.Target.Environment != "prod" {
		t.Fatalf("target environments = %q/%q", devParsed.Target.Environment, prodParsed.Target.Environment)
	}
	if len(devParsed.Target.Files) != 2 || !samePath(t, devParsed.Target.Files[0], devFiles[0]) || !samePath(t, devParsed.Target.Files[1], devFiles[1]) {
		t.Fatalf("dev target files = %v", devParsed.Target.Files)
	}
	if len(prodParsed.Target.Files) != 2 || !samePath(t, prodParsed.Target.Files[1], prodFiles[1]) {
		t.Fatalf("prod target files = %v", prodParsed.Target.Files)
	}
	if len(devParsed.Target.EnvFiles) != 1 || !samePath(t, devParsed.Target.EnvFiles[0], devEnvFile) ||
		len(prodParsed.Target.EnvFiles) != 1 || !samePath(t, prodParsed.Target.EnvFiles[0], prodEnvFile) {
		t.Fatalf("env files = %v / %v", devParsed.Target.EnvFiles, prodParsed.Target.EnvFiles)
	}
	if strings.Join(devParsed.Target.Profiles, ",") != "devtools" || strings.Join(prodParsed.Target.Profiles, ",") != "prodtools" {
		t.Fatalf("profiles = %v / %v", devParsed.Target.Profiles, prodParsed.Target.Profiles)
	}
	if devParsed.Data.Stack.ProjectName != devName || prodParsed.Data.Stack.ProjectName != prodName {
		t.Fatalf("project names = %q/%q, want %q/%q",
			devParsed.Data.Stack.ProjectName, prodParsed.Data.Stack.ProjectName, devName, prodName)
	}
	if !findService(t, devParsed.Data.Stack, "devonly").Configured {
		t.Fatalf("devonly not configured in dev: %+v", devParsed.Data.Stack.Services)
	}
	if !findService(t, prodParsed.Data.Stack, "prodonly").Configured {
		t.Fatalf("prodonly not configured in prod: %+v", prodParsed.Data.Stack.Services)
	}
	for _, svc := range devParsed.Data.Stack.Services {
		if svc.Name == "prodonly" {
			t.Fatalf("prod service leaked into dev status: %+v", svc)
		}
	}
	for _, svc := range prodParsed.Data.Stack.Services {
		if svc.Name == "devonly" {
			t.Fatalf("dev service leaked into prod status: %+v", svc)
		}
	}
	var devPort, prodPort int
	for _, c := range findService(t, devParsed.Data.Stack, "web").Connections {
		if c.Source == stack.SourceRuntime {
			devPort = c.PublishedPort
		}
	}
	for _, c := range findService(t, prodParsed.Data.Stack, "web").Connections {
		if c.Source == stack.SourceRuntime {
			prodPort = c.PublishedPort
		}
	}
	if devPort == 0 || prodPort == 0 || devPort == prodPort {
		t.Fatalf("published ports dev=%d prod=%d, want distinct runtime ports", devPort, prodPort)
	}

	// The two status reads are read-only and must leave both stacks untouched.
	if got := containerIDs(t, env, projectDir, devName, devFiles); strings.Join(got, ",") != strings.Join(devIDs, ",") {
		t.Fatalf("dev containers changed after status: %v -> %v", devIDs, got)
	}
	if got := containerIDs(t, env, projectDir, prodName, prodFiles); strings.Join(got, ",") != strings.Join(prodIDs, ",") {
		t.Fatalf("prod containers changed after status: %v -> %v", prodIDs, got)
	}
}

func TestIntegrationEnvironmentOverlapIsReportedBeforeApply(t *testing.T) {
	env := requireIntegration(t)
	suffix := randomHex(t, 5)
	stateDir := t.TempDir()
	projectDir := t.TempDir()
	sharedDir := t.TempDir()
	port := freeHostPort(t)

	nameA := "dcraft-test-" + suffix + "-a"
	nameB := "dcraft-test-" + suffix + "-b"
	fileA := filepath.Join(projectDir, "compose.a.yaml")
	fileB := filepath.Join(projectDir, "compose.b.yaml")
	body := func(name string) string {
		return fmt.Sprintf(`name: %s
services:
  web:
    image: busybox:1.36
    command: ["sleep", "180"]
    ports:
      - "127.0.0.1:%d:80"
    volumes:
      - %s:/data
`, name, port, sharedDir)
	}
	if err := os.WriteFile(fileA, []byte(body(nameA)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fileB, []byte(body(nameB)), 0o644); err != nil {
		t.Fatal(err)
	}
	config := `environments:
  a:
    compose_files: [compose.a.yaml]
  b:
    compose_files: [compose.b.yaml]
`
	if err := os.WriteFile(filepath.Join(projectDir, ".dcraft.yaml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}

	cleanupCompose(t, env, projectDir, nameA, []string{fileA}, nil)
	composeRun(t, env, projectDir, nameA, []string{fileA}, "up", "-d", "--wait", "--wait-timeout", "90")
	aIDs := containerIDs(t, env, projectDir, nameA, []string{fileA})
	if len(aIDs) != 1 {
		t.Fatalf("conflict stack containers = %v", aIDs)
	}

	dcraftEnv := buildEnv(map[string]string{"DCRAFT_STATE_DIR": stateDir})
	res := runProcess(t, projectDir, dcraftEnv, strings.NewReader(""), "status", "--env", "b", "--json")
	if res.code != 0 {
		t.Fatalf("status exit = %d, stderr=%q", res.code, res.stderr)
	}
	parsed := parseStatus(t, res.stdout)
	var overlap *core.Diagnostic
	for i := range parsed.Diagnostics {
		if parsed.Diagnostics[i].Category == core.CategoryEnvironmentOverlap {
			overlap = &parsed.Diagnostics[i]
		}
	}
	if overlap == nil {
		t.Fatalf("no environment_overlap warning: %+v", parsed.Diagnostics)
	}
	if overlap.Severity != core.SeverityWarning {
		t.Fatalf("overlap severity = %q", overlap.Severity)
	}
	for _, want := range []string{fmt.Sprintf("%d/tcp", port), sharedDir, "'a'", "'b'"} {
		if !strings.Contains(overlap.Message+overlap.Detail, want) {
			t.Fatalf("overlap diagnostic %+v missing %q", overlap, want)
		}
	}
	if got := containerIDs(t, env, projectDir, nameA, []string{fileA}); strings.Join(got, ",") != strings.Join(aIDs, ",") {
		t.Fatalf("conflict stack changed after a read-only warning: %v -> %v", aIDs, got)
	}
}

func TestIntegrationUnrelatedStackIsUntouched(t *testing.T) {
	env := requireIntegration(t)
	suffix := randomHex(t, 5)
	stateDir := t.TempDir()
	projectDir := t.TempDir()
	canaryDir := t.TempDir()
	canaryName := "dcraft-test-" + suffix + "-canary"
	canaryFile := filepath.Join(canaryDir, "compose.yaml")

	if err := os.WriteFile(canaryFile, []byte(fmt.Sprintf(`name: %s
services:
  canary:
    image: busybox:1.36
    command: ["sleep", "180"]
`, canaryName)), 0o644); err != nil {
		t.Fatal(err)
	}
	writeEnvironmentProject(t, projectDir)

	cleanupCompose(t, env, canaryDir, canaryName, []string{canaryFile}, nil)
	composeRun(t, env, canaryDir, canaryName, []string{canaryFile}, "up", "-d", "--wait", "--wait-timeout", "90")
	canaryIDs := containerIDs(t, env, canaryDir, canaryName, []string{canaryFile})
	if len(canaryIDs) != 1 {
		t.Fatalf("canary containers = %v", canaryIDs)
	}

	dcraftEnv := buildEnv(map[string]string{"DCRAFT_STATE_DIR": stateDir})
	if res := runProcess(t, projectDir, dcraftEnv, strings.NewReader(""), "status", "--env", "dev", "--json"); res.code != 0 {
		t.Fatalf("environment status exit = %d, stderr=%q", res.code, res.stderr)
	}
	if got := containerIDs(t, env, canaryDir, canaryName, []string{canaryFile}); strings.Join(got, ",") != strings.Join(canaryIDs, ",") {
		t.Fatalf("unrelated stack changed: %v -> %v", canaryIDs, got)
	}
}
