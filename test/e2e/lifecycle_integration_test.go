package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/stack"
	"github.com/rootmyself/dcraft/internal/state"
)

const lifecycleCompose = `services:
  web:
    image: busybox:${DCRAFT_TEST_TAG:-1.36}
    command: ["sh", "-c", "echo boot >/data/boot; sleep 600"]
    environment:
      MODE: ${MODE:-one}
    volumes:
      - data:/data
volumes:
  data:
`

type composeProjectRow struct {
	Name        string `json:"Name"`
	Status      string `json:"Status"`
	ConfigFiles string `json:"ConfigFiles"`
}

func listComposeProjects(t *testing.T, env []string) map[string]composeProjectRow {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "compose", "ls", "-a", "--format", "json")
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("docker compose ls: %v", err)
	}
	var rows []composeProjectRow
	if strings.TrimSpace(string(out)) == "" {
		return map[string]composeProjectRow{}
	}
	if err := json.Unmarshal(out, &rows); err != nil {
		t.Fatalf("parse compose ls: %v\n%s", err, out)
	}
	projects := map[string]composeProjectRow{}
	for _, row := range rows {
		projects[row.Name] = row
	}
	return projects
}

func composeInspect(t *testing.T, env []string, project, file, service string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "compose", "-p", project, "-f", file, "ps", "-q", service)
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("compose ps %s: %v", service, err)
	}
	return strings.TrimSpace(string(out))
}

func containerEnv(t *testing.T, id, name string) string {
	t.Helper()
	out, err := exec.Command("docker", "inspect", "-f", "{{range .Config.Env}}{{println .}}{{end}}", id).Output()
	if err != nil {
		t.Fatalf("inspect env: %v", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, name+"=") {
			return strings.TrimPrefix(line, name+"=")
		}
	}
	return ""
}

func containerRunning(t *testing.T, id string) bool {
	t.Helper()
	out, err := exec.Command("docker", "inspect", "-f", "{{.State.Running}}", id).Output()
	if err != nil {
		t.Fatalf("inspect running: %v", err)
	}
	return strings.TrimSpace(string(out)) == "true"
}

func cleanupLifecycleCompose(t *testing.T, env []string, dir, project, file string, removeImages bool) {
	t.Helper()
	args := []string{"compose", "-p", project, "-f", file, "down", "-v", "--remove-orphans"}
	if removeImages {
		args = append(args, "--rmi", "local")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("cleanup down %s failed: %v\n%s", project, err, out)
	}
}

func TestIntegrationBlockedRenameDoesNotRedirectTheNextOperation(t *testing.T) {
	env := requireIntegration(t)
	base := "dcraft-rename-" + randomHex(t, 4)
	project := filepath.Join(t.TempDir(), base)
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	composePath := filepath.Join(project, "compose.yaml")
	if err := os.WriteFile(composePath, []byte(lifecycleCompose), 0o644); err != nil {
		t.Fatal(err)
	}
	stateDir := t.TempDir()
	dcraftEnv := buildEnv(map[string]string{"DCRAFT_STATE_DIR": stateDir})
	store := state.New(stateDir)

	release := func(res processResult, want int) lifecycleResult {
		t.Helper()
		if res.code != want {
			t.Fatalf("exit = %d, want %d; stdout=%s stderr=%s", res.code, want, res.stdout, res.stderr)
		}
		return parseLifecycle(t, res.stdout)
	}

	up := release(runProcess(t, project, dcraftEnv, strings.NewReader(""), "up", "--json"), 0)
	name := up.Data.Lifecycle.ProjectName
	if name != base {
		t.Fatalf("initial name = %q, want %q", name, base)
	}
	t.Cleanup(func() { cleanupLifecycleCompose(t, env, project, name, composePath, false) })

	namesPath := filepath.Join(stateDir, "names.json")
	before, err := os.ReadFile(namesPath)
	if err != nil {
		t.Fatal(err)
	}

	// Hold the real per-stack lock so a rename attempt is refused before its
	// name claim could be written.
	lock, err := store.AcquireStackLock(context.Background(),
		core.Target{ProjectDir: project}, state.LockHolder{Action: "down", PID: 4242, StartedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	blocked := runProcess(t, project, dcraftEnv, strings.NewReader(""),
		"up", "--json", "-p", base+"-renamed")
	_ = lock.Release()
	blockedParsed := release(blocked, 1)
	if !hasLifecycleCategory(blockedParsed.Diagnostics, core.CategoryStackBusy) {
		t.Fatalf("blocked rename diagnostics = %+v", blockedParsed.Diagnostics)
	}

	after, err := os.ReadFile(namesPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("blocked rename changed names.json:\nbefore=%s\nafter=%s", before, after)
	}

	next := release(runProcess(t, project, dcraftEnv, strings.NewReader(""), "up", "--json"), 0)
	if next.Data.Lifecycle.ProjectName != name {
		t.Fatalf("next operation used %q, want the original %q", next.Data.Lifecycle.ProjectName, name)
	}
	if _, ok := listComposeProjects(t, env)[base+"-renamed"]; ok {
		t.Fatalf("blocked rename created a second project %q", base+"-renamed")
	}
}

func TestIntegrationSuffixedProjectIsReusedAfterStateLoss(t *testing.T) {
	env := requireIntegration(t)
	base := "dcraft-reuse-" + randomHex(t, 4)
	root := t.TempDir()
	ownerDir := filepath.Join(root, "owner", base)
	oursDir := filepath.Join(root, "ours", base)
	for _, dir := range []string{ownerDir, oursDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(lifecycleCompose), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sharedState := t.TempDir()
	sharedEnv := buildEnv(map[string]string{"DCRAFT_STATE_DIR": sharedState})
	ownerCompose := filepath.Join(ownerDir, "compose.yaml")
	oursCompose := filepath.Join(oursDir, "compose.yaml")

	ownerUp := runProcess(t, ownerDir, sharedEnv, strings.NewReader(""), "up", "--json")
	if ownerUp.code != 0 {
		t.Fatalf("owner up exit=%d stderr=%q", ownerUp.code, ownerUp.stderr)
	}
	if got := parseLifecycle(t, ownerUp.stdout).Data.Lifecycle.ProjectName; got != base {
		t.Fatalf("owner name = %q, want %q", got, base)
	}
	t.Cleanup(func() { cleanupLifecycleCompose(t, env, ownerDir, base, ownerCompose, false) })

	// This folder first used a declared high suffix.
	declared := base + "-3"
	oursDeclared := runProcess(t, oursDir, sharedEnv, strings.NewReader(""), "up", "-p", declared, "--json")
	if oursDeclared.code != 0 {
		t.Fatalf("declared up exit=%d stderr=%q", oursDeclared.code, oursDeclared.stderr)
	}
	t.Cleanup(func() { cleanupLifecycleCompose(t, env, oursDir, declared, oursCompose, false) })

	// Every state file is gone, but the Compose projects remain. The next
	// unqualified up must adopt the existing suffixed project, never allocate
	// the smallest free name and create a duplicate.
	lostEnv := buildEnv(map[string]string{"DCRAFT_STATE_DIR": t.TempDir()})
	next := runProcess(t, oursDir, lostEnv, strings.NewReader(""), "up", "--json")
	if next.code != 0 {
		t.Fatalf("up after state loss exit=%d stderr=%q", next.code, next.stderr)
	}
	if got := parseLifecycle(t, next.stdout).Data.Lifecycle.ProjectName; got != declared {
		t.Fatalf("name after state loss = %q, want the existing %q", got, declared)
	}
	projects := listComposeProjects(t, env)
	if _, ok := projects[declared]; !ok {
		t.Fatalf("suffixed project disappeared: %+v", projects)
	}
	if _, ok := projects[base+"-1"]; ok {
		t.Fatalf("state loss created a duplicate project %q", base+"-1")
	}
}

func hasLifecycleCategory(diags []core.Diagnostic, category core.Category) bool {
	for _, d := range diags {
		if d.Category == category {
			return true
		}
	}
	return false
}

func TestIntegrationLifecycleStack(t *testing.T) {
	env := requireIntegration(t)
	// A unique readable base keeps the suffix assertions meaningful without
	// depending on unrelated projects that may exist on this Docker host.
	base := "dcraft-hw-" + randomHex(t, 4)

	root := t.TempDir()
	project := filepath.Join(root, "a", base)
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	composePath := filepath.Join(project, "compose.yaml")
	if err := os.WriteFile(composePath, []byte(lifecycleCompose), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".env"), []byte("MODE=one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stateDir := t.TempDir()
	dcraftEnv := buildEnv(map[string]string{"DCRAFT_STATE_DIR": stateDir})
	registerCleanup := func(dir, name, file string) {
		t.Cleanup(func() { cleanupLifecycleCompose(t, env, dir, name, file, false) })
	}

	run := func(args ...string) processResult {
		t.Helper()
		res := runProcess(t, project, dcraftEnv, strings.NewReader(""), args...)
		if res.stderr != "" && strings.Contains(strings.Join(args, " "), "--json") {
			t.Fatalf("JSON mode wrote stderr: %q", res.stderr)
		}
		return res
	}
	tracked := func(res processResult) lifecycleResult {
		t.Helper()
		parsed := parseLifecycle(t, res.stdout)
		if parsed.Outcome != core.OutcomeSuccess {
			t.Fatalf("outcome=%q diagnostics=%+v stderr=%q", parsed.Outcome, parsed.Diagnostics, res.stderr)
		}
		return parsed
	}

	up := run("up", "--json")
	if up.code != 0 {
		t.Fatalf("up exit=%d stderr=%q", up.code, up.stderr)
	}
	upParsed := tracked(up)
	if got := upParsed.Data.Lifecycle.ProjectName; got != base {
		t.Fatalf("allocated name = %q, want %s", got, base)
	}
	registerCleanup(project, upParsed.Data.Lifecycle.ProjectName, composePath)
	if !upParsed.Data.Lifecycle.ConfigApplied {
		t.Fatal("up did not state that it applied the configuration")
	}
	webID := composeInspect(t, env, base, composePath, "web")
	if webID == "" || !containerRunning(t, webID) {
		t.Fatalf("web container is not running: %q", webID)
	}

	status := run("status", "--json")
	if status.code != 0 {
		t.Fatalf("status exit=%d stderr=%q", status.code, status.stderr)
	}
	statusParsed := parseStatus(t, status.stdout)
	if statusParsed.Data.Stack.Summary.Running != 1 || findService(t, statusParsed.Data.Stack, "web").State != stack.StateRunning {
		t.Fatalf("status after up = %+v", statusParsed.Data.Stack.Summary)
	}

	stop := run("stop", "--json")
	if stop.code != 0 {
		t.Fatalf("stop exit=%d stderr=%q", stop.code, stop.stderr)
	}
	stopParsed := tracked(stop)
	if stopParsed.Data.Lifecycle.ProjectName != base || stopParsed.Data.Lifecycle.ConfigApplied {
		t.Fatalf("stop report = %+v", stopParsed.Data.Lifecycle)
	}
	if containerRunning(t, webID) {
		t.Fatal("stop left the container running")
	}
	stoppedStatus := parseStatus(t, run("status", "--json").stdout)
	if svc := findService(t, stoppedStatus.Data.Stack, "web"); svc.State != stack.StateExited {
		t.Fatalf("status after stop = %+v", svc)
	}

	resume := tracked(run("up", "--json"))
	if resume.Data.Lifecycle.ProjectName != base {
		t.Fatalf("resume name = %q", resume.Data.Lifecycle.ProjectName)
	}
	resumedID := composeInspect(t, env, base, composePath, "web")
	if resumedID != webID {
		t.Fatalf("resume recreated the container: %s -> %s", webID, resumedID)
	}
	if !containerRunning(t, webID) {
		t.Fatal("resumed container is not running")
	}
	if got := containerEnv(t, webID, "MODE"); got != "one" {
		t.Fatalf("MODE after up = %q", got)
	}

	// restart must not apply a configuration change.
	if err := os.WriteFile(filepath.Join(project, ".env"), []byte("MODE=two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	restart := tracked(run("restart", "-s", "web", "--json"))
	if restart.Data.Lifecycle.ConfigApplied {
		t.Fatal("restart claimed to apply configuration")
	}
	if restart.Data.Lifecycle.Scope != "services" || strings.Join(restart.Data.Lifecycle.Services, ",") != "web" {
		t.Fatalf("restart scope = %+v", restart.Data.Lifecycle)
	}
	afterRestartID := composeInspect(t, env, base, composePath, "web")
	if afterRestartID != webID {
		t.Fatalf("restart recreated the container: %s -> %s", webID, afterRestartID)
	}
	if got := containerEnv(t, webID, "MODE"); got != "one" {
		t.Fatalf("restart applied the new configuration: MODE=%q", got)
	}

	// An explicit up applies the current configuration.
	if res := run("up", "--json"); res.code != 0 {
		t.Fatalf("up after change exit=%d stderr=%q", res.code, res.stderr)
	}
	updatedID := composeInspect(t, env, base, composePath, "web")
	if got := containerEnv(t, updatedID, "MODE"); got != "two" {
		t.Fatalf("up did not apply the new configuration: MODE=%q", got)
	}

	// Put a marker in the named volume so down can prove it kept the data.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	execCmd := exec.CommandContext(ctx, "docker", "compose", "-p", base, "-f", composePath, "exec", "-T", "web", "sh", "-c", "echo marker >/data/marker")
	execCmd.Dir = project
	execCmd.Env = env
	if out, err := execCmd.CombinedOutput(); err != nil {
		cancel()
		t.Fatalf("write volume marker: %v\n%s", err, out)
	}
	cancel()

	// A second stack must stay untouched for every later operation.
	otherDir := filepath.Join(root, "other", "dcraft-other-"+randomHex(t, 4))
	if err := os.MkdirAll(otherDir, 0o755); err != nil {
		t.Fatal(err)
	}
	otherCompose := filepath.Join(otherDir, "compose.yaml")
	otherYAML := `services:
  hold:
    image: busybox:${DCRAFT_TEST_TAG:-1.36}
    command: ["sh", "-c", "echo other >/data/other; sleep 600"]
    volumes:
      - other:/data
volumes:
  other:
`
	if err := os.WriteFile(otherCompose, []byte(otherYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	otherUp := runProcess(t, otherDir, dcraftEnv, strings.NewReader(""), "up", "--json")
	if otherUp.code != 0 {
		t.Fatalf("other up exit=%d stderr=%q stdout=%s", otherUp.code, otherUp.stderr, otherUp.stdout)
	}
	otherName := parseLifecycle(t, otherUp.stdout).Data.Lifecycle.ProjectName
	registerCleanup(otherDir, otherName, otherCompose)
	otherID := composeInspect(t, env, otherName, otherCompose, "hold")
	if otherID == "" || !containerRunning(t, otherID) {
		t.Fatal("other stack did not start")
	}
	otherVolume := otherName + "_other"
	if !volumeExists(t, otherVolume) {
		t.Fatalf("other volume %q missing", otherVolume)
	}

	down := tracked(run("down", "--json"))
	if down.Data.Lifecycle.VolumesRemoved {
		t.Fatal("down claimed to remove volumes")
	}
	if ids := strings.TrimSpace(composeInspectAll(t, env, base, composePath)); ids != "" {
		t.Fatalf("down left containers: %s", ids)
	}
	if !volumeExists(t, base+"_data") {
		t.Fatal("down removed the named data volume")
	}
	readOut, err := exec.Command("docker", "run", "--rm", "-v", base+"_data:/data", "busybox:1.36", "cat", "/data/marker").Output()
	if err != nil || strings.TrimSpace(string(readOut)) != "marker" {
		t.Fatalf("volume data lost after down: %v %q", err, readOut)
	}
	otherData, err := exec.Command("docker", "run", "--rm", "-v", otherVolume+":/data", "busybox:1.36", "cat", "/data/other").Output()
	if err != nil || strings.TrimSpace(string(otherData)) != "other" {
		t.Fatalf("other stack data changed during down: %v %q", err, otherData)
	}
	if got := composeInspect(t, env, otherName, otherCompose, "hold"); got != otherID || !containerRunning(t, otherID) {
		t.Fatalf("other stack changed during down: %s -> %s", otherID, got)
	}

	// A stopped stack resumes with up and keeps its data.
	afterDown := tracked(run("up", "--json"))
	if afterDown.Data.Lifecycle.ProjectName != base {
		t.Fatalf("up after down used %q", afterDown.Data.Lifecycle.ProjectName)
	}
	catOut, err := exec.Command("docker", "compose", "-p", base, "-f", composePath, "exec", "-T", "web", "cat", "/data/marker").CombinedOutput()
	if err != nil || strings.TrimSpace(string(catOut)) != "marker" {
		t.Fatalf("data after up following down: %v %q", err, catOut)
	}

	// Two same-named folders in different places get readable suffixes.
	secondDir := filepath.Join(t.TempDir(), "b", base)
	if err := os.MkdirAll(secondDir, 0o755); err != nil {
		t.Fatal(err)
	}
	secondCompose := filepath.Join(secondDir, "compose.yaml")
	secondYAML := `services:
  web:
    image: busybox:${DCRAFT_TEST_TAG:-1.36}
    command: ["sleep", "600"]
`
	if err := os.WriteFile(secondCompose, []byte(secondYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	secondUp := runProcess(t, secondDir, dcraftEnv, strings.NewReader(""), "up", "--json")
	registerCleanup(secondDir, base+"-1", secondCompose)
	if secondUp.code != 0 {
		t.Fatalf("second folder up exit=%d stderr=%q", secondUp.code, secondUp.stderr)
	}
	if got := parseLifecycle(t, secondUp.stdout).Data.Lifecycle.ProjectName; got != base+"-1" {
		t.Fatalf("second folder name = %q, want %q", got, base+"-1")
	}
	if ids := composeInspect(t, env, base, composePath, "web"); ids == "" || !containerRunning(t, ids) {
		t.Fatal("first stack changed when the second folder started")
	}

	// Status must identify the suffixed stack through the recorded name, use
	// the same containers, and write no state.
	statusStateBefore := stateTree(t, stateDir)
	statusB := runProcess(t, secondDir, dcraftEnv, strings.NewReader(""), "status", "--path", secondDir, "--json")
	if statusB.code != 0 || statusB.stderr != "" {
		t.Fatalf("suffixed status exit=%d stderr=%q", statusB.code, statusB.stderr)
	}
	statusBParsed := parseStatus(t, statusB.stdout)
	if statusBParsed.Target.ProjectName != base+"-1" {
		t.Fatalf("status target project = %q, want %q", statusBParsed.Target.ProjectName, base+"-1")
	}
	if statusBParsed.Data.Stack.ProjectName != base+"-1" {
		t.Fatalf("status payload project = %q, want %q", statusBParsed.Data.Stack.ProjectName, base+"-1")
	}
	webB := findService(t, statusBParsed.Data.Stack, "web")
	if webB.State != stack.StateRunning || len(webB.Instances) != 1 {
		t.Fatalf("status web = %+v", webB)
	}
	bContainerID := composeInspect(t, env, base+"-1", secondCompose, "web")
	if bContainerID == "" {
		t.Fatal("second stack has no web container")
	}
	nameOut, err := exec.Command("docker", "inspect", "-f", "{{.Name}}", bContainerID).Output()
	if err != nil {
		t.Fatalf("inspect container name: %v", err)
	}
	wantInstance := strings.TrimPrefix(strings.TrimSpace(string(nameOut)), "/")
	if webB.Instances[0].Name != wantInstance {
		t.Fatalf("status instance = %q, want the second stack's %q", webB.Instances[0].Name, wantInstance)
	}
	if statusBParsed.Data.Stack.ProjectDir != secondDir {
		t.Fatalf("status folder = %q, want %q", statusBParsed.Data.Stack.ProjectDir, secondDir)
	}
	if !equalStateTrees(statusStateBefore, stateTree(t, stateDir)) {
		t.Fatal("status changed per-computer state")
	}

	projects := listComposeProjects(t, env)
	if _, ok := projects[base+"-1"]; !ok {
		t.Fatalf("compose has no %s project: %+v", base+"-1", projects)
	}

	// An explicit name collision is refused before a second stack is created.
	thirdDir := filepath.Join(t.TempDir(), "c", base)
	if err := os.MkdirAll(thirdDir, 0o755); err != nil {
		t.Fatal(err)
	}
	thirdCompose := filepath.Join(thirdDir, "compose.yaml")
	if err := os.WriteFile(thirdCompose, []byte(secondYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	collision := runProcess(t, thirdDir, dcraftEnv, strings.NewReader(""),
		"up", "-p", base, "--json")
	if collision.code != 2 {
		t.Fatalf("collision exit = %d, want 2 (stderr=%q)", collision.code, collision.stderr)
	}
	collisionParsed := parseLifecycle(t, collision.stdout)
	if cats := lifecycleCategories(collisionParsed); len(cats) != 1 || cats[0] != core.CategoryProjectNameConflict {
		t.Fatalf("collision diagnostics = %+v", collisionParsed.Diagnostics)
	}
	if _, ok := listComposeProjects(t, env)[base+"-1"]; !ok {
		t.Fatal("second stack disappeared after the collision attempt")
	}
	if got := composeInspect(t, env, base, composePath, "web"); got == "" || !containerRunning(t, got) {
		t.Fatal("collision attempt changed the first stack")
	}

	// TUI quit does not change services.
	tuiOutput, code := runPTY(t, project, dcraftEnv, base)
	if code != 0 {
		t.Fatalf("TUI exit = %d\n%s", code, tuiOutput)
	}
	if ids := composeInspect(t, env, base, composePath, "web"); ids == "" || !containerRunning(t, ids) {
		t.Fatal("TUI quit stopped the stack")
	}
	if got := composeInspect(t, env, otherName, otherCompose, "hold"); got != otherID || !containerRunning(t, otherID) {
		t.Fatalf("TUI changed the other stack: %s -> %s", otherID, got)
	}
}

func composeInspectAll(t *testing.T, env []string, project, file string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "compose", "-p", project, "-f", file, "ps", "-a", "-q")
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("compose ps -a: %v", err)
	}
	return string(out)
}

func volumeExists(t *testing.T, name string) bool {
	t.Helper()
	out, err := exec.Command("docker", "volume", "ls", "-q").Output()
	if err != nil {
		t.Fatalf("docker volume ls: %v", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == name {
			return true
		}
	}
	return false
}

func TestIntegrationLifecycleExistingProjectIsReused(t *testing.T) {
	env := requireIntegration(t)
	name := "dcraft-pre-" + randomHex(t, 6)
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	composePath := filepath.Join(dir, "compose.yaml")
	doc := `services:
  web:
    image: busybox:${DCRAFT_TEST_TAG:-1.36}
    command: ["sleep", "600"]
`
	if err := os.WriteFile(composePath, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupLifecycleCompose(t, env, dir, name, composePath, false) })

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	up := exec.CommandContext(ctx, "docker", "compose", "-p", name, "-f", composePath, "up", "-d")
	up.Dir = dir
	up.Env = env
	if out, err := up.CombinedOutput(); err != nil {
		t.Fatalf("pre-create stack: %v\n%s", err, out)
	}
	before := composeInspect(t, env, name, composePath, "web")
	if before == "" {
		t.Fatal("pre-created stack has no container")
	}
	stateDir := t.TempDir()
	res := runProcess(t, dir, buildEnv(map[string]string{"DCRAFT_STATE_DIR": stateDir}), strings.NewReader(""), "up", "--json")
	if res.code != 0 {
		t.Fatalf("dcraft up exit=%d stderr=%q", res.code, res.stderr)
	}
	report := parseLifecycle(t, res.stdout).Data.Lifecycle
	if report.ProjectName != name {
		t.Fatalf("existing project was renamed to %q", report.ProjectName)
	}
	if report.ProjectNameSource != "existing" {
		t.Fatalf("source = %q, want existing", report.ProjectNameSource)
	}
	if after := composeInspect(t, env, name, composePath, "web"); after != before {
		t.Fatalf("existing stack was recreated: %s -> %s", before, after)
	}
	if n := len(listComposeProjects(t, env)); n < 1 {
		t.Fatalf("projects = %d", n)
	}
}

func TestIntegrationLifecycleConcurrentUpFailsClearly(t *testing.T) {
	env := requireIntegration(t)
	dir := filepath.Join(t.TempDir(), "dcraft-concurrent-"+randomHex(t, 6))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	composePath := filepath.Join(dir, "compose.yaml")
	cacheBust := randomHex(t, 8)
	doc := fmt.Sprintf(`services:
  slow:
    build:
      context: .
      args:
        CACHEBUST: %s
`, cacheBust)
	if err := os.WriteFile(composePath, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	dockerfile := `FROM busybox:1.36
ARG CACHEBUST
RUN echo "$CACHEBUST" > /cachebust
CMD ["sleep", "600"]
`
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(dockerfile), 0o644); err != nil {
		t.Fatal(err)
	}
	stateDir := t.TempDir()
	dcraftEnv := buildEnv(map[string]string{"DCRAFT_STATE_DIR": stateDir})
	name := filepath.Base(dir)
	t.Cleanup(func() { cleanupLifecycleCompose(t, env, dir, name, composePath, true) })

	type outcome struct {
		res processResult
		err error
	}
	results := make(chan outcome, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, binaryPath, "up", "--path", dir, "--json")
			cmd.Dir = dir
			cmd.Env = dcraftEnv
			var stdout, stderr strings.Builder
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			err := cmd.Run()
			code := 0
			if err != nil {
				var exitErr *exec.ExitError
				if ok := asExit(err, &exitErr); ok {
					code = exitErr.ExitCode()
				} else {
					results <- outcome{err: err}
					return
				}
			}
			results <- outcome{res: processResult{stdout: stdout.String(), stderr: stderr.String(), code: code}}
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	var outcomes []processResult
	for o := range results {
		if o.err != nil {
			t.Fatalf("concurrent up: %v", o.err)
		}
		outcomes = append(outcomes, o.res)
	}
	if len(outcomes) != 2 {
		t.Fatalf("outcomes = %d", len(outcomes))
	}
	success, busy := 0, 0
	for _, res := range outcomes {
		switch res.code {
		case 0:
			success++
		case 1:
			parsed := parseLifecycle(t, res.stdout)
			cats := lifecycleCategories(parsed)
			if len(cats) != 1 || cats[0] != core.CategoryStackBusy {
				t.Fatalf("loser diagnostics = %+v", parsed.Diagnostics)
			}
			if !strings.Contains(parsed.Diagnostics[0].Message, "진행 중") {
				t.Fatalf("busy message = %q", parsed.Diagnostics[0].Message)
			}
			busy++
		default:
			t.Fatalf("unexpected exit = %d stderr=%q", res.code, res.stderr)
		}
	}
	if success != 1 || busy != 1 {
		t.Fatalf("success=%d busy=%d, want one winner and one clear conflict", success, busy)
	}
	projects := listComposeProjects(t, env)
	if _, ok := projects[name]; !ok {
		t.Fatalf("winning up did not create %q: %+v", name, projects)
	}
	if ids := composeInspectAll(t, env, name, composePath); strings.TrimSpace(ids) == "" {
		t.Fatal("winning up created no containers")
	}
}

func asExit(err error, target **exec.ExitError) bool {
	if e, ok := err.(*exec.ExitError); ok {
		*target = e
		return true
	}
	return false
}

func TestIntegrationLifecycleDeclaredNameSources(t *testing.T) {
	env := requireIntegration(t)
	stateDir := t.TempDir()
	dcraftEnv := buildEnv(map[string]string{"DCRAFT_STATE_DIR": stateDir})
	doc := `services:
  web:
    image: busybox:${DCRAFT_TEST_TAG:-1.36}
    command: ["sleep", "600"]
`
	envProject := "dcraft-envproj-" + randomHex(t, 6)
	makeFolder := func(base string) (string, string) {
		dir := filepath.Join(t.TempDir(), base)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		return dir, filepath.Join(dir, "compose.yaml")
	}

	// A COMPOSE_PROJECT_NAME declared in .env is a declared name, not a
	// folder default. Two folders declaring the same name must conflict.
	dirA, fileA := makeFolder("env-name-a")
	if err := os.WriteFile(fileA, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirA, ".env"), []byte("COMPOSE_PROJECT_NAME="+envProject+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupLifecycleCompose(t, env, dirA, envProject, fileA, false) })
	upA := runProcess(t, dirA, dcraftEnv, strings.NewReader(""), "up", "--json")
	if upA.code != 0 {
		t.Fatalf("env-name up exit=%d stderr=%q", upA.code, upA.stderr)
	}
	reportA := parseLifecycle(t, upA.stdout).Data.Lifecycle
	if reportA.ProjectName != envProject || reportA.ProjectNameSource != "configured" {
		t.Fatalf("env-name report = %+v", reportA)
	}

	dirB, fileB := makeFolder("env-name-b")
	if err := os.WriteFile(fileB, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirB, ".env"), []byte("COMPOSE_PROJECT_NAME="+envProject+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	upB := runProcess(t, dirB, dcraftEnv, strings.NewReader(""), "up", "--json")
	if upB.code != 2 {
		t.Fatalf("duplicate declared name exit=%d, want 2 (stderr=%q)", upB.code, upB.stderr)
	}
	if cats := lifecycleCategories(parseLifecycle(t, upB.stdout)); len(cats) != 1 || cats[0] != core.CategoryProjectNameConflict {
		t.Fatalf("diagnostics = %+v", parseLifecycle(t, upB.stdout).Diagnostics)
	}
	if _, ok := listComposeProjects(t, env)[envProject+"-1"]; ok {
		t.Fatal("a suffixed duplicate was created for a declared name")
	}

	// A top-level name: is honored the same way.
	yamlProject := "dcraft-yamln-" + randomHex(t, 6)
	dirC, fileC := makeFolder("yaml-name-c")
	yamlDoc := "name: " + yamlProject + "\n" + doc
	if err := os.WriteFile(fileC, []byte(yamlDoc), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupLifecycleCompose(t, env, dirC, yamlProject, fileC, false) })
	upC := runProcess(t, dirC, dcraftEnv, strings.NewReader(""), "up", "--json")
	if upC.code != 0 {
		t.Fatalf("yaml-name up exit=%d stderr=%q", upC.code, upC.stderr)
	}
	reportC := parseLifecycle(t, upC.stdout).Data.Lifecycle
	if reportC.ProjectName != yamlProject || reportC.ProjectNameSource != "configured" {
		t.Fatalf("yaml-name report = %+v", reportC)
	}

	// An exported COMPOSE_PROJECT_NAME is also a declaration, not a folder
	// default, so a collision must not be silently suffixed.
	exported := "dcraft-exported-" + randomHex(t, 6)
	dirD, fileD := makeFolder("exported-name-d")
	if err := os.WriteFile(fileD, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupLifecycleCompose(t, env, dirD, exported, fileD, false) })
	exportedEnv := buildEnv(map[string]string{
		"DCRAFT_STATE_DIR":     stateDir,
		"COMPOSE_PROJECT_NAME": exported,
	})
	upD := runProcess(t, dirD, exportedEnv, strings.NewReader(""), "up", "--json")
	if upD.code != 0 {
		t.Fatalf("exported-name up exit=%d stderr=%q", upD.code, upD.stderr)
	}
	reportD := parseLifecycle(t, upD.stdout).Data.Lifecycle
	if reportD.ProjectName != exported || reportD.ProjectNameSource != "configured" {
		t.Fatalf("exported-name report = %+v", reportD)
	}
}

func TestIntegrationLifecycleConcurrentNameAllocation(t *testing.T) {
	env := requireIntegration(t)
	base := "dcraft-hwc-" + randomHex(t, 4)
	stateDir := t.TempDir()
	dcraftEnv := buildEnv(map[string]string{"DCRAFT_STATE_DIR": stateDir})
	doc := `services:
  web:
    image: busybox:${DCRAFT_TEST_TAG:-1.36}
    command: ["sleep", "600"]
`
	var dirs []string
	for i := 0; i < 2; i++ {
		dir := filepath.Join(t.TempDir(), base)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
		dirs = append(dirs, dir)
	}

	type outcome struct {
		dir    string
		stdout string
		code   int
		err    error
	}
	results := make(chan outcome, len(dirs))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, dir := range dirs {
		dir := dir
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, binaryPath, "up", "--path", dir, "--json")
			cmd.Dir = dir
			cmd.Env = dcraftEnv
			var stdout, stderr strings.Builder
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			err := cmd.Run()
			code := 0
			if err != nil {
				var exitErr *exec.ExitError
				if ok := asExit(err, &exitErr); ok {
					code = exitErr.ExitCode()
				} else {
					results <- outcome{err: err}
					return
				}
			}
			if code != 0 {
				results <- outcome{dir: dir, code: code, err: fmt.Errorf("exit %d: %s", code, stderr.String())}
				return
			}
			results <- outcome{dir: dir, stdout: stdout.String()}
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	names := map[string]bool{}
	dirByName := map[string]string{}
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent allocation: %v", result.err)
		}
		parsed := parseLifecycle(t, result.stdout)
		name := parsed.Data.Lifecycle.ProjectName
		if name == "" {
			t.Fatalf("no name in %+v", parsed.Data.Lifecycle)
		}
		names[name] = true
		dirByName[name] = result.dir
	}
	if len(names) != 2 {
		t.Fatalf("concurrent allocations produced %v, want two distinct names", names)
	}
	if !names[base] || !names[base+"-1"] {
		t.Fatalf("concurrent allocations = %v, want %s and %s", names, base, base+"-1")
	}
	projects := listComposeProjects(t, env)
	for name, dir := range dirByName {
		if _, ok := projects[name]; !ok {
			t.Fatalf("project %q missing after concurrent up: %+v", name, projects)
		}
		file := filepath.Join(dir, "compose.yaml")
		t.Cleanup(func() { cleanupLifecycleCompose(t, env, dir, name, file, false) })
	}
}
