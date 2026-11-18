package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
)

type hooksComposeProject struct {
	dir     string
	project string
	file    string
	marker  string
	state   string
	env     []string
}

func newHooksProject(t *testing.T, env []string, base string) *hooksComposeProject {
	t.Helper()
	dir := filepath.Join(t.TempDir(), base)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	project := "dcraft-hooks-" + randomHex(t, 6)
	compose := fmt.Sprintf(`name: %s
services:
  web:
    image: busybox:${DCRAFT_TEST_TAG:-1.36}
    command: ["sleep", "600"]
`, project)
	file := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(file, []byte(compose), 0o644); err != nil {
		t.Fatal(err)
	}
	p := &hooksComposeProject{
		dir: dir, project: project, file: file,
		marker: filepath.Join(t.TempDir(), "hook-marker.log"),
		state:  t.TempDir(),
	}
	p.env = buildEnv(map[string]string{"DCRAFT_STATE_DIR": p.state})
	t.Cleanup(func() { cleanupLifecycleCompose(t, env, p.dir, p.project, p.file, false) })
	return p
}

func (p *hooksComposeProject) writeRegistration(t *testing.T, stages ...string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("hooks:\n")
	for _, stage := range stages {
		fmt.Fprintf(&b, "  - path: scripts/%s.sh\n    stage: %s\n", stage, stage)
	}
	if err := os.WriteFile(filepath.Join(p.dir, ".dcraft.yaml"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (p *hooksComposeProject) writeScript(t *testing.T, stage, body string) {
	t.Helper()
	dir := filepath.Join(p.dir, "scripts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, stage+".sh"), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func (p *hooksComposeProject) markerLines(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(p.marker)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	return strings.Split(strings.TrimRight(string(data), "\n"), "\n")
}

func (p *hooksComposeProject) containerIDs(t *testing.T, env []string) string {
	t.Helper()
	return composeInspectAll(t, env, p.project, p.file)
}

func parseAnyResult(t *testing.T, out string) statusJSON {
	t.Helper()
	var res statusJSON
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("parse JSON result: %v\n%s", err, out)
	}
	return res
}

func resultCategories(res statusJSON) []core.Category {
	var out []core.Category
	for _, d := range res.Diagnostics {
		out = append(out, d.Category)
	}
	return out
}

func hasResultCategory(res statusJSON, cat core.Category) bool {
	for _, d := range res.Diagnostics {
		if d.Category == cat {
			return true
		}
	}
	return false
}

func stageStatus(res statusJSON, name string) core.StageStatus {
	for _, st := range res.Stages {
		if st.Name == name {
			return st.Status
		}
	}
	return ""
}

func TestIntegrationHooksRunAroundUpAndUpdateOnly(t *testing.T) {
	env := requireIntegration(t)
	p := newHooksProject(t, env, "hooks-a")
	p.writeScript(t, "pre", `printf 'pre:%s:%s\n' "$DCRAFT_ACTION" "$DCRAFT_HOOK_SHA256" >> `+p.marker+`
printf 'pre hook output %s\n' "$DCRAFT_ACTION"`)
	p.writeScript(t, "post", `printf 'post:%s:%s\n' "$DCRAFT_ACTION" "$DCRAFT_HOOK_SHA256" >> `+p.marker)
	p.writeRegistration(t, "pre", "post")

	// Non-interactive without the explicit independent trust: exit 2, no
	// script, no stack.
	res := runProcess(t, p.dir, p.env, strings.NewReader(""), "up", "--json")
	if res.code != 2 {
		t.Fatalf("unapproved up exit = %d, stderr=%q stdout=%s", res.code, res.stderr, res.stdout)
	}
	parsed := parseAnyResult(t, res.stdout)
	if parsed.Outcome != core.OutcomeFailure || !hasResultCategory(parsed, core.CategoryApprovalRequired) {
		t.Fatalf("unapproved result = %+v", parsed)
	}
	if lines := p.markerLines(t); len(lines) != 0 {
		t.Fatalf("unapproved hook ran: %v", lines)
	}
	if ids := strings.TrimSpace(p.containerIDs(t, env)); ids != "" {
		t.Fatalf("unapproved up changed the stack: %s", ids)
	}

	up := runProcess(t, p.dir, p.env, strings.NewReader(""), "up", "--json", "--approve", "script_trust")
	if up.code != 0 {
		t.Fatalf("approved up exit = %d stderr=%q stdout=%s", up.code, up.stderr, up.stdout)
	}
	upRes := parseAnyResult(t, up.stdout)
	if upRes.Outcome != core.OutcomeSuccess {
		t.Fatalf("up result = %+v", upRes)
	}
	if got := stageStatus(upRes, "hooks_pre"); got != core.StageSucceeded {
		t.Fatalf("hooks_pre = %q", got)
	}
	if got := stageStatus(upRes, "revalidate_inputs"); got != core.StageSucceeded {
		t.Fatalf("revalidate_inputs = %q", got)
	}
	if got := stageStatus(upRes, "hooks_post"); got != core.StageSucceeded {
		t.Fatalf("hooks_post = %q", got)
	}
	lines := p.markerLines(t)
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "pre:up:") || !strings.HasPrefix(lines[1], "post:up:") {
		t.Fatalf("markers = %v", lines)
	}
	idsAfterUp := strings.TrimSpace(p.containerIDs(t, env))
	if idsAfterUp == "" {
		t.Fatal("up created no containers")
	}
	if !hasResultCategory(upRes, core.CategoryHookOutput) {
		t.Fatalf("hook output not reported: %+v", upRes.Diagnostics)
	}

	update := runProcess(t, p.dir, p.env, strings.NewReader(""), "update", "--json", "--approve", "script_trust")
	if update.code != 0 {
		t.Fatalf("update exit = %d stderr=%q stdout=%s", update.code, update.stderr, update.stdout)
	}
	updateRes := parseAnyResult(t, update.stdout)
	if updateRes.Outcome != core.OutcomeSuccess {
		t.Fatalf("update result = %+v", updateRes)
	}
	lines = p.markerLines(t)
	if len(lines) != 4 || !strings.HasPrefix(lines[2], "pre:update:") || !strings.HasPrefix(lines[3], "post:update:") {
		t.Fatalf("update markers = %v", lines)
	}
	if ids := strings.TrimSpace(p.containerIDs(t, env)); ids != idsAfterUp {
		t.Fatalf("unchanged update recreated the container: %s -> %s", idsAfterUp, ids)
	}

	// stop/status never run hooks.
	for _, action := range []string{"stop", "status"} {
		res := runProcess(t, p.dir, p.env, strings.NewReader(""), action, "--json")
		if res.code != 0 {
			t.Fatalf("%s exit = %d stderr=%q", action, res.code, res.stderr)
		}
		if got := len(p.markerLines(t)); got != 4 {
			t.Fatalf("%s ran a hook (markers=%d)", action, got)
		}
		for _, st := range parseAnyResult(t, res.stdout).Stages {
			if strings.HasPrefix(st.Name, "hooks_") {
				t.Fatalf("%s exposed hook stages: %+v", action, parseAnyResult(t, res.stdout).Stages)
			}
		}
	}

	// Another stack is untouched and never runs this project's hooks.
	idsBeforeOther := strings.TrimSpace(p.containerIDs(t, env))
	other := newHooksProject(t, env, "hooks-b")
	otherUp := runProcess(t, other.dir, other.env, strings.NewReader(""), "up", "--json")
	if otherUp.code != 0 {
		t.Fatalf("other up exit = %d stderr=%q", otherUp.code, otherUp.stderr)
	}
	otherRes := parseAnyResult(t, otherUp.stdout)
	for _, st := range otherRes.Stages {
		if strings.HasPrefix(st.Name, "hooks_") {
			t.Fatalf("other stack exposed hook stages: %+v", otherRes.Stages)
		}
	}
	otherIDs := strings.TrimSpace(other.containerIDs(t, env))
	if otherIDs == "" {
		t.Fatal("other stack was not started")
	}
	if got := len(p.markerLines(t)); got != 4 {
		t.Fatalf("other stack ran project A hooks: %v", p.markerLines(t))
	}
	if ids := strings.TrimSpace(p.containerIDs(t, env)); ids == "" || ids != idsBeforeOther {
		t.Fatalf("project A stack was modified by the other stack: %q -> %q", idsBeforeOther, ids)
	}
}

func TestIntegrationHookContentChangeRequiresReapproval(t *testing.T) {
	env := requireIntegration(t)
	p := newHooksProject(t, env, "hooks-c")
	p.writeScript(t, "pre", `printf 'pre:%s\n' "$DCRAFT_HOOK_SHA256" >> `+p.marker)
	p.writeRegistration(t, "pre")

	first := runProcess(t, p.dir, p.env, strings.NewReader(""), "up", "--json", "--approve", "script_trust")
	if first.code != 0 {
		t.Fatalf("first up exit = %d stderr=%q", first.code, first.stderr)
	}
	if got := len(p.markerLines(t)); got != 1 {
		t.Fatalf("markers = %v", p.markerLines(t))
	}
	ids := strings.TrimSpace(p.containerIDs(t, env))
	if ids == "" {
		t.Fatal("first up created no containers")
	}

	p.writeScript(t, "pre", `printf 'pre:%s\n' "$DCRAFT_HOOK_SHA256" >> `+p.marker+"\n# changed after approval")
	changed := runProcess(t, p.dir, p.env, strings.NewReader(""), "up", "--json")
	if changed.code != 2 {
		t.Fatalf("changed content without approval exit = %d stdout=%s", changed.code, changed.stdout)
	}
	changedRes := parseAnyResult(t, changed.stdout)
	if !hasResultCategory(changedRes, core.CategoryApprovalRequired) {
		t.Fatalf("changed diagnostics = %+v", changedRes.Diagnostics)
	}
	if got := len(p.markerLines(t)); got != 1 {
		t.Fatalf("changed script ran without approval: %v", p.markerLines(t))
	}
	if now := strings.TrimSpace(p.containerIDs(t, env)); now != ids {
		t.Fatalf("rejected up changed the stack: %s -> %s", ids, now)
	}

	approved := runProcess(t, p.dir, p.env, strings.NewReader(""), "up", "--json", "--approve", "script_trust")
	if approved.code != 0 {
		t.Fatalf("re-approved up exit = %d stderr=%q stdout=%s", approved.code, approved.stderr, approved.stdout)
	}
	if got := len(p.markerLines(t)); got != 2 {
		t.Fatalf("re-approved script did not run: %v", p.markerLines(t))
	}
}

func TestIntegrationPreFailureAbortsAndPostFailureIsSeparate(t *testing.T) {
	env := requireIntegration(t)
	p := newHooksProject(t, env, "hooks-d")
	p.writeScript(t, "post", `printf 'post-ran\n' >> `+p.marker+`
exit 5`)
	p.writeRegistration(t, "post")
	up := runProcess(t, p.dir, p.env, strings.NewReader(""), "up", "--json", "--approve", "script_trust")
	if up.code != 0 {
		t.Fatalf("post-failing up exit = %d stderr=%q stdout=%s", up.code, up.stderr, up.stdout)
	}
	upRes := parseAnyResult(t, up.stdout)
	if upRes.Outcome != core.OutcomeSuccess {
		t.Fatalf("post failure changed the outcome: %+v", upRes)
	}
	if !hasResultCategory(upRes, core.CategoryHookPostFailed) {
		t.Fatalf("post failure not reported separately: %+v", upRes.Diagnostics)
	}
	if got := stageStatus(upRes, "hooks_post"); got != core.StageFailed {
		t.Fatalf("hooks_post = %q", got)
	}
	ids := strings.TrimSpace(p.containerIDs(t, env))
	if ids == "" {
		t.Fatal("post failure left no stack")
	}
	if out := runCommand(t, env, "docker", "inspect", "-f", "{{.State.Running}}", ids); strings.TrimSpace(out) != "true" {
		t.Fatalf("post failure stopped the stack: %s", out)
	}
	if got := len(p.markerLines(t)); got != 1 {
		t.Fatalf("post script did not run: %v", p.markerLines(t))
	}

	p.writeScript(t, "pre", `printf 'pre-ran\n' >> `+p.marker+`
exit 6`)
	p.writeRegistration(t, "pre")
	before := strings.TrimSpace(p.containerIDs(t, env))
	failed := runProcess(t, p.dir, p.env, strings.NewReader(""), "up", "--json", "--approve", "script_trust")
	if failed.code != 1 {
		t.Fatalf("pre-failing up exit = %d stdout=%s", failed.code, failed.stdout)
	}
	failedRes := parseAnyResult(t, failed.stdout)
	if failedRes.Outcome != core.OutcomeFailure || !hasResultCategory(failedRes, core.CategoryHookFailed) {
		t.Fatalf("pre failure diagnostics = %+v", failedRes.Diagnostics)
	}
	if got := stageStatus(failedRes, "hooks_pre"); got != core.StageFailed {
		t.Fatalf("hooks_pre = %q", got)
	}
	if after := strings.TrimSpace(p.containerIDs(t, env)); after != before {
		t.Fatalf("pre failure changed the stack: %s -> %s", before, after)
	}
}

func TestIntegrationHookChangedAfterApprovalIsRefused(t *testing.T) {
	env := requireIntegration(t)
	p := newHooksProject(t, env, "hooks-e")
	dir := filepath.Join(p.dir, "scripts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	first := filepath.Join(dir, "first.sh")
	second := filepath.Join(dir, "second.sh")
	if err := os.WriteFile(first, []byte(`#!/bin/sh
cat > "$(dirname "$0")/second.sh" <<'SCRIPT'
#!/bin/sh
exit 0
SCRIPT
`), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("#!/bin/sh\nprintf 'second-ran\\n' >> "+p.marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	registration := "hooks:\n  - path: scripts/first.sh\n    stage: pre\n  - path: scripts/second.sh\n    stage: pre\n"
	if err := os.WriteFile(filepath.Join(p.dir, ".dcraft.yaml"), []byte(registration), 0o644); err != nil {
		t.Fatal(err)
	}

	res := runProcess(t, p.dir, p.env, strings.NewReader(""), "up", "--json", "--approve", "script_trust")
	if res.code != 2 {
		t.Fatalf("changed-after-approval up exit = %d, stdout=%s stderr=%q", res.code, res.stdout, res.stderr)
	}
	parsed := parseAnyResult(t, res.stdout)
	if !hasResultCategory(parsed, core.CategoryHookContentChanged) {
		t.Fatalf("diagnostics = %+v", parsed.Diagnostics)
	}
	if lines := p.markerLines(t); len(lines) != 0 {
		t.Fatalf("changed script ran: %v", lines)
	}
	if ids := strings.TrimSpace(p.containerIDs(t, env)); ids != "" {
		t.Fatalf("refused operation changed the stack: %s", ids)
	}
}

// runCommand is a small helper for the host-state assertions in these tests.
func runCommand(t *testing.T, env []string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
	return string(out)
}
