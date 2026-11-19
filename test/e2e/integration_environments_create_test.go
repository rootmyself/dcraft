package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
)

// Creating both aliases through the CLI must produce the same separate stacks
// as a hand-written config, and removing an alias must never stop or delete a
// running stack.
func TestIntegrationCLICreatedEnvironmentAliasesKeepSeparateStacks(t *testing.T) {
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
    healthcheck:
      test: ["CMD", "true"]
      interval: 1s
      timeout: 1s
      retries: 10
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
	files := map[string]string{
		"compose.yaml":      base,
		"compose.dev.yaml":  devFile,
		"compose.prod.yaml": prodFile,
		".env.dev":          "SLEEP_SECONDS=240\n",
		".env.prod":         "SLEEP_SECONDS=300\n",
	}
	for name, body := range files {
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

	hashes := map[string]string{
		"compose.yaml":      sha256File(t, filepath.Join(projectDir, "compose.yaml")),
		"compose.dev.yaml":  sha256File(t, filepath.Join(projectDir, "compose.dev.yaml")),
		"compose.prod.yaml": sha256File(t, filepath.Join(projectDir, "compose.prod.yaml")),
		".env.dev":          sha256File(t, devEnvFile),
		".env.prod":         sha256File(t, prodEnvFile),
	}
	assertProjectFiles := func() {
		t.Helper()
		for name, want := range hashes {
			if got := sha256File(t, filepath.Join(projectDir, name)); got != want {
				t.Fatalf("%s was modified", name)
			}
		}
	}

	dcraftEnv := buildEnv(map[string]string{"DCRAFT_STATE_DIR": stateDir})
	add := func(alias string, extra ...string) {
		t.Helper()
		args := append([]string{"environments", "--add", "--name", alias}, extra...)
		args = append(args, "--json")
		res := runProcess(t, projectDir, dcraftEnv, strings.NewReader(""), args...)
		if res.code != 0 {
			t.Fatalf("add %s exit = %d stderr=%q", alias, res.code, res.stderr)
		}
		parsed := parseEnvironmentResult(t, res.stdout)
		if parsed.Outcome != core.OutcomeSuccess {
			t.Fatalf("add %s result = %+v", alias, parsed)
		}
		found := false
		for _, got := range parsed.Data.Environments.Aliases {
			if got.Name != alias {
				continue
			}
			found = true
			wantEnv := map[string]string{"dev": devEnvFile, "prod": prodEnvFile}[alias]
			if len(got.EnvFiles) != 1 || !samePath(t, got.EnvFiles[0], wantEnv) {
				t.Fatalf("add %s env file = %v", alias, got.EnvFiles)
			}
		}
		if !found {
			t.Fatalf("add %s did not return the new alias: %+v", alias, parsed.Data.Environments.Aliases)
		}
	}
	add("dev",
		"--file", "compose.yaml", "--file", "compose.dev.yaml",
		"--env-file", ".env.dev", "--profile", "devtools")
	add("prod",
		"--file", "compose.yaml", "--file", "compose.prod.yaml",
		"--env-file", ".env.prod", "--profile", "prodtools")
	assertProjectFiles()

	config, err := os.ReadFile(filepath.Join(projectDir, ".dcraft.yaml"))
	if err != nil {
		t.Fatalf("read created config: %v", err)
	}
	for _, want := range []string{"environments:", "dev:", "prod:", "compose.dev.yaml", "compose.prod.yaml", ".env.dev", ".env.prod", "devtools", "prodtools"} {
		if !strings.Contains(string(config), want) {
			t.Fatalf("created config missing %q:\n%s", want, config)
		}
	}

	upDev := runProcess(t, projectDir, dcraftEnv, strings.NewReader(""), "up", "--env", "dev", "--json")
	if upDev.code != 0 {
		t.Fatalf("up dev exit = %d stderr=%q", upDev.code, upDev.stderr)
	}
	devReport := parseLifecycle(t, upDev.stdout).Data.Lifecycle
	if devReport.ProjectName != devName {
		t.Fatalf("dev project = %q, want %q", devReport.ProjectName, devName)
	}
	devIDs := containerIDs(t, env, projectDir, devName, devFiles)
	if len(devIDs) != 2 {
		t.Fatalf("dev containers = %v, want 2", devIDs)
	}

	upProd := runProcess(t, projectDir, dcraftEnv, strings.NewReader(""), "up", "--env", "prod", "--json")
	if upProd.code != 0 {
		t.Fatalf("up prod exit = %d stderr=%q", upProd.code, upProd.stderr)
	}
	prodReport := parseLifecycle(t, upProd.stdout).Data.Lifecycle
	if prodReport.ProjectName != prodName {
		t.Fatalf("prod project = %q, want %q", prodReport.ProjectName, prodName)
	}
	prodIDs := containerIDs(t, env, projectDir, prodName, prodFiles)
	if len(prodIDs) != 2 {
		t.Fatalf("prod containers = %v, want 2", prodIDs)
	}
	for _, id := range append(append([]string{}, devIDs...), prodIDs...) {
		assertRunning(t, id)
	}
	for _, id := range devIDs {
		for _, other := range prodIDs {
			if id == other {
				t.Fatalf("dev and prod share container %s", id)
			}
		}
	}

	devStatus := runProcess(t, projectDir, dcraftEnv, strings.NewReader(""), "status", "--env", "dev", "--json")
	if devStatus.code != 0 {
		t.Fatalf("status dev exit = %d stderr=%q", devStatus.code, devStatus.stderr)
	}
	devParsed := parseStatus(t, devStatus.stdout)
	if devParsed.Target.Environment != "dev" || devParsed.Data.Stack.ProjectName != devName {
		t.Fatalf("dev status = env %q project %q", devParsed.Target.Environment, devParsed.Data.Stack.ProjectName)
	}
	if !findService(t, devParsed.Data.Stack, "devonly").Configured {
		t.Fatalf("devonly missing from dev status: %+v", devParsed.Data.Stack.Services)
	}
	for _, svc := range devParsed.Data.Stack.Services {
		if svc.Name == "prodonly" {
			t.Fatalf("prod service leaked into dev status")
		}
	}
	prodStatus := runProcess(t, projectDir, dcraftEnv, strings.NewReader(""), "status", "--env", "prod", "--json")
	if prodStatus.code != 0 {
		t.Fatalf("status prod exit = %d stderr=%q", prodStatus.code, prodStatus.stderr)
	}
	if got := parseStatus(t, prodStatus.stdout).Data.Stack.ProjectName; got != prodName {
		t.Fatalf("prod status project = %q", got)
	}

	stacks, err := os.ReadDir(filepath.Join(stateDir, "stacks"))
	if err != nil {
		t.Fatalf("read per-environment state: %v", err)
	}
	if len(stacks) != 2 {
		t.Fatalf("per-environment state dirs = %d, want 2", len(stacks))
	}

	remove := runProcess(t, projectDir, dcraftEnv, strings.NewReader(""), "environments", "--remove", "--name", "dev", "--json")
	if remove.code != 0 {
		t.Fatalf("remove dev exit = %d stderr=%q", remove.code, remove.stderr)
	}
	if got := containerIDs(t, env, projectDir, devName, devFiles); strings.Join(got, ",") != strings.Join(devIDs, ",") {
		t.Fatalf("removing the alias changed the running dev stack: %v -> %v", devIDs, got)
	}
	for _, id := range devIDs {
		assertRunning(t, id)
	}
	if got := containerIDs(t, env, projectDir, prodName, prodFiles); strings.Join(got, ",") != strings.Join(prodIDs, ",") {
		t.Fatalf("removing dev changed the prod stack: %v -> %v", prodIDs, got)
	}
	for _, id := range prodIDs {
		assertRunning(t, id)
	}
	if res := runProcess(t, projectDir, dcraftEnv, strings.NewReader(""), "status", "--env", "dev", "--json"); res.code != 2 {
		t.Fatalf("status after removing dev exit = %d, want 2", res.code)
	}
	if stacks, err := os.ReadDir(filepath.Join(stateDir, "stacks")); err != nil || len(stacks) != 2 {
		t.Fatalf("removing an alias deleted per-environment state: %v %d", err, len(stacks))
	}
	assertProjectFiles()
}
