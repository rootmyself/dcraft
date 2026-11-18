package e2e

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/stack"
)

type rollbackJSON struct {
	SchemaVersion    int               `json:"schema_version"`
	OperationID      string            `json:"operation_id"`
	Action           string            `json:"action"`
	Outcome          core.Outcome      `json:"outcome"`
	RequiredApproval core.ApprovalKind `json:"required_approval"`
	Target           core.Target       `json:"target"`
	Stages           []core.Stage      `json:"stages"`
	Diagnostics      []core.Diagnostic `json:"diagnostics"`
	Data             struct {
		Rollback struct {
			ProjectName      string                   `json:"project_name"`
			Environment      string                   `json:"environment"`
			Scope            string                   `json:"scope"`
			Services         []string                 `json:"services"`
			Files            []string                 `json:"files"`
			EnvFiles         []string                 `json:"env_files"`
			Profiles         []string                 `json:"profiles"`
			RecordedAt       string                   `json:"recorded_at"`
			Images           []stack.RollbackPin      `json:"images"`
			Applied          bool                     `json:"applied"`
			Verified         bool                     `json:"verified"`
			ImageIDsVerified bool                     `json:"image_ids_verified"`
			AlreadyAtVersion bool                     `json:"already_at_version"`
			Readiness        []stack.ServiceReadiness `json:"readiness"`
			RecordStatus     string                   `json:"record_status"`
			DataRestored     bool                     `json:"data_restored"`
			HooksRun         bool                     `json:"hooks_run"`
			Notices          []string                 `json:"notices"`
		} `json:"rollback"`
	} `json:"data"`
}

func parseRollback(t *testing.T, out string) rollbackJSON {
	t.Helper()
	var res rollbackJSON
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("parse rollback JSON: %v\n%s", err, out)
	}
	return res
}

func hasRollbackCategory(res rollbackJSON, cat core.Category) bool {
	for _, d := range res.Diagnostics {
		if d.Category == cat {
			return true
		}
	}
	return false
}

func rollbackRun(t *testing.T, project, stateDir string, fake *updateFakeDocker, args ...string) (rollbackJSON, processResult) {
	t.Helper()
	full := append([]string{"rollback", "--path", project, "--json"}, args...)
	res := runProcess(t, project, fake.env(t, stateDir, nil), strings.NewReader(""), full...)
	parsed := rollbackJSON{}
	if strings.TrimSpace(res.stdout) != "" {
		parsed = parseRollback(t, res.stdout)
	}
	return parsed, res
}

// createGoodRecord runs a normal update through the fake Docker so a real
// previous-good record exists for the rollback tests.
func createGoodRecord(t *testing.T, project, stateDir string, fake *updateFakeDocker) updateJSON {
	t.Helper()
	parsed, res := updateJSONFor(t, project, fake.env(t, stateDir, nil))
	if res.code != 0 || parsed.Outcome != core.OutcomeSuccess {
		t.Fatalf("setup update exit=%d outcome=%q stderr=%q diagnostics=%+v",
			res.code, parsed.Outcome, res.stderr, parsed.Diagnostics)
	}
	return parsed
}

// The previous_version approval is independent: without it nothing changes,
// and another approval kind is not accepted as a substitute.
func TestRollbackRequiresItsOwnApproval(t *testing.T) {
	fake := newUpdateFakeDocker(t)
	project := t.TempDir()
	writeProject(t, project)
	stateDir := t.TempDir()
	createGoodRecord(t, project, stateDir, fake)

	for _, args := range [][]string{
		nil,
		{"--approve", "install"},
		{"--approve", "script_trust"},
		{"--approve", "data_restore"},
	} {
		parsed, res := rollbackRun(t, project, stateDir, fake, args...)
		if res.code != 2 {
			t.Fatalf("args %v: exit = %d, want 2 (stderr=%q)", args, res.code, res.stderr)
		}
		if parsed.Outcome != core.OutcomeFailure || !hasRollbackCategory(parsed, core.CategoryApprovalRequired) {
			t.Fatalf("args %v: result = %+v", args, parsed)
		}
		if parsed.RequiredApproval != core.ApprovalPreviousGood {
			t.Fatalf("args %v: required approval = %q", args, parsed.RequiredApproval)
		}
	}
}

// A successful rollback applies the recorded images only, verifies them, keeps
// the record and reports what was not restored. It never pulls, builds, prunes
// or re-runs hooks.
func TestRollbackCLIAppliesRecordedImagesAndKeepsRecord(t *testing.T) {
	fake := newUpdateFakeDocker(t)
	project := t.TempDir()
	writeProject(t, project)
	stateDir := t.TempDir()
	update := createGoodRecord(t, project, stateDir, fake)
	callsBefore := len(fake.calls(t))
	recordPath := findStateFile(t, filepath.Join(stateDir, "stacks"), "state.json")
	before, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}

	// A deliberately different explicit file must not replace the recorded
	// execution configuration.
	other := filepath.Join(project, "other.yaml")
	if err := os.WriteFile(other, []byte("services:\n  web:\n    image: other:1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A hook-looking script in the project folder must never run on rollback.
	hookMarker := filepath.Join(project, "hook-ran")
	hookScript := filepath.Join(project, "post-up.sh")
	if err := os.WriteFile(hookScript, []byte("#!/bin/sh\ntouch "+hookMarker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	parsed, res := rollbackRun(t, project, stateDir, fake,
		"--approve", "previous_version", "-f", other)
	if res.code != 0 || res.stderr != "" {
		t.Fatalf("rollback exit=%d stderr=%q stdout=%s", res.code, res.stderr, res.stdout)
	}
	if parsed.Outcome != core.OutcomeSuccess || parsed.Action != "rollback" {
		t.Fatalf("result = %+v", parsed)
	}
	var stageNames []string
	for _, st := range parsed.Stages {
		stageNames = append(stageNames, st.Name)
	}
	want := "resolve_target,preflight,load_record,precheck,apply,verify,report,history"
	if got := strings.Join(stageNames, ","); got != want {
		t.Fatalf("stages = %q, want %q", got, want)
	}
	report := parsed.Data.Rollback
	if !report.Applied || !report.ImageIDsVerified {
		t.Fatalf("report = %+v", report)
	}
	if report.Verified {
		t.Fatal("a rollback containing services without a healthcheck claimed full verification")
	}
	statuses := map[string]stack.Readiness{}
	for _, svc := range report.Readiness {
		statuses[svc.Service] = svc.Status
	}
	if statuses["web"] != stack.ReadinessHealthy || statuses["worker"] != stack.ReadinessRunningUnverified ||
		statuses["builder"] != stack.ReadinessRunningUnverified {
		t.Fatalf("readiness = %+v", report.Readiness)
	}
	if !report.AlreadyAtVersion {
		t.Fatal("the fake stack already ran the recorded images but was not reported as such")
	}
	if report.RecordStatus != "unchanged" || report.DataRestored || report.HooksRun {
		t.Fatalf("honesty fields = %+v", report)
	}
	if len(report.Images) != len(update.Data.Update.GoodVersion.Services) {
		t.Fatalf("images = %+v", report.Images)
	}
	if len(report.Files) != 1 || report.Files[0] != filepath.Join(project, "compose.yaml") {
		t.Fatalf("report files = %v, want the recorded Compose file", report.Files)
	}
	if strings.Contains(strings.Join(report.Files, ","), "other.yaml") {
		t.Fatalf("report used the current explicit file: %v", report.Files)
	}
	for _, img := range report.Images {
		if !strings.HasPrefix(img.ImageID, "sha256:") || !strings.HasPrefix(img.RetentionTag, "dcraft-good/") {
			t.Fatalf("image identity = %+v", img)
		}
	}
	if !strings.Contains(strings.Join(report.Notices, "\n"), "더 오래된 버전은 없습니다") {
		t.Fatalf("notices = %v", report.Notices)
	}

	calls := fake.calls(t)[callsBefore:]
	apply := findCall(t, calls, " up ")
	for _, want := range []string{"--detach", "--pull never", "--no-build"} {
		if !strings.Contains(apply, want) {
			t.Fatalf("apply call misses %q: %q", want, apply)
		}
	}
	if !strings.Contains(apply, filepath.Join(project, "compose.yaml")) {
		t.Fatalf("apply did not use the recorded Compose file: %q", apply)
	}
	if strings.Contains(apply, other) {
		t.Fatalf("apply used the current explicit file instead of the record: %q", apply)
	}
	if !strings.Contains(apply, "dcraft-rollback-") {
		t.Fatalf("apply did not pass a dcraft-managed pin override: %q", apply)
	}
	for _, line := range calls {
		for _, forbidden := range []string{"--wait", " pull ", " build ", " down ", " prune", "image rm", "stop "} {
			if strings.Contains(line, forbidden) {
				t.Fatalf("rollback ran a forbidden command %q: %q", forbidden, line)
			}
		}
	}
	if _, err := os.Stat(hookMarker); !os.IsNotExist(err) {
		t.Fatalf("rollback ran a host script: %v", err)
	}
	after, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("rollback changed the good record:\nbefore=%s\nafter=%s", before, after)
	}
	if !strings.Contains(historyFor(t, stateDir), `"action":"rollback"`) {
		t.Fatal("successful rollback was not written to history")
	}
}

// A stack with no record fails with a clear category and changes nothing.
func TestRollbackWithoutRecordFailsCleanly(t *testing.T) {
	fake := newUpdateFakeDocker(t)
	project := t.TempDir()
	writeProject(t, project)
	stateDir := t.TempDir()

	parsed, res := rollbackRun(t, project, stateDir, fake, "--approve", "previous_version")
	if res.code != 1 {
		t.Fatalf("exit = %d, want 1 (stderr=%q)", res.code, res.stderr)
	}
	if !hasRollbackCategory(parsed, core.CategoryRollbackUnavailable) {
		t.Fatalf("diagnostics = %+v", parsed.Diagnostics)
	}
	for _, line := range fake.calls(t) {
		if strings.Contains(line, " up ") {
			t.Fatalf("rollback changed a stack without a record: %q", line)
		}
	}
}

// A missing recorded image aborts before the stack changes.
func TestRollbackMissingImageAbortsBeforeChange(t *testing.T) {
	fake := newUpdateFakeDocker(t)
	project := t.TempDir()
	writeProject(t, project)
	stateDir := t.TempDir()
	createGoodRecord(t, project, stateDir, fake)
	callsBefore := len(fake.calls(t))
	recordPath := findStateFile(t, filepath.Join(stateDir, "stacks"), "state.json")
	record, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}
	var rec struct {
		Services []struct {
			ImageID      string `json:"image_id"`
			RetentionTag string `json:"retention_tag"`
		} `json:"services"`
	}
	if err := json.Unmarshal(record, &rec); err != nil {
		t.Fatal(err)
	}
	if len(rec.Services) == 0 {
		t.Fatal("record has no services")
	}
	// Remove the retention tag of the first recorded image from the fake
	// engine; the image ID remains.
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(readFile(t, fake.images)), "\n") {
		if strings.HasPrefix(line, rec.Services[0].RetentionTag+" ") {
			continue
		}
		lines = append(lines, line)
	}
	if err := os.WriteFile(fake.images, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	parsed, res := rollbackRun(t, project, stateDir, fake, "--approve", "previous_version")
	if res.code != 1 {
		t.Fatalf("exit = %d, want 1 (stderr=%q)", res.code, res.stderr)
	}
	if !hasRollbackCategory(parsed, core.CategoryRollbackImageMissing) {
		t.Fatalf("diagnostics = %+v", parsed.Diagnostics)
	}
	for _, line := range fake.calls(t)[callsBefore:] {
		if strings.Contains(line, " up ") {
			t.Fatalf("rollback applied despite a missing image: %q", line)
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// Cancellation during apply exits 130, is not success and leaves the record
// untouched.
func TestRollbackCancellationIsNotSuccess(t *testing.T) {
	fake := newUpdateFakeDocker(t)
	project := t.TempDir()
	writeProject(t, project)
	stateDir := t.TempDir()
	createGoodRecord(t, project, stateDir, fake)
	recordPath := findStateFile(t, filepath.Join(stateDir, "stacks"), "state.json")
	before, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(binaryPath, "rollback", "--path", project, "--approve", "previous_version", "--json")
	cmd.Dir = project
	cmd.Env = fake.env(t, stateDir, map[string]string{"DOCKER_FAKE_SLEEP_UP": "10"})
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1200 * time.Millisecond)
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
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
		t.Fatalf("exit = %d, want 130 (stderr=%q)", code, stderr.String())
	}
	parsed := parseRollback(t, stdout.String())
	if parsed.Outcome != core.OutcomeCanceled || !hasRollbackCategory(parsed, core.CategoryCanceled) {
		t.Fatalf("result = %+v", parsed)
	}
	after, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("canceled rollback changed the good record:\nbefore=%s\nafter=%s", before, after)
	}
}
