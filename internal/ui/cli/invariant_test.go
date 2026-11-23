package cli_test

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/ui/view"
)

// These are the CLI-side halves of the redesign's invariants. The visual layer
// belongs to the TUI; the CLI must not adopt it, and `--json` must stay the v1
// contract scripts read.

// The CLI package must not depend on the TUI or its visual layer. A structural
// assertion is stronger than a text diff: an import would show up here even if
// the output happened to stay the same.
func TestCLIDoesNotDependOnTheTUIVisualLayer(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Skipf("go list is unavailable in this environment: %v", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "github.com/rootmyself/dcraft/internal/ui/tui" ||
			strings.HasPrefix(line, "github.com/rootmyself/dcraft/internal/ui/tui/") {
			t.Fatalf("the CLI now depends on %s; the visual layer belongs to the TUI", line)
		}
	}
}

// The view package the CLI shares with the TUI must also stay free of the
// visual layer: its text is printed verbatim by the CLI.
func TestSharedViewPackageHasNoTUIVisualDependency(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "../../view").Output()
	if err != nil {
		t.Skipf("go list is unavailable in this environment: %v", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "github.com/rootmyself/dcraft/internal/ui/tui") {
			t.Fatalf("internal/ui/view now depends on %s; CLI output would change with the TUI", line)
		}
	}
}

// The JSON contract is schema v1 with the same required keys. The redesign added
// no field and removed none.
func TestJSONSchemaVersionAndRequiredKeysAreUnchanged(t *testing.T) {
	status := &stubFeature{desc: descriptor("status")}
	h := newHarness(t, status)
	if err := h.run("status", "--json"); err != nil {
		t.Fatalf("status --json: %v", err)
	}
	if h.stderr.Len() != 0 {
		t.Fatalf("JSON mode wrote stderr: %q", h.stderr.String())
	}
	if !strings.HasSuffix(h.stdout.String(), "\n") {
		t.Fatalf("JSON output does not end with one newline: %q", h.stdout.String())
	}
	if strings.Count(strings.TrimRight(h.stdout.String(), "\n"), "\n") == 0 {
		t.Fatalf("JSON output is not indented: %q", h.stdout.String())
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(h.stdout.Bytes(), &raw); err != nil {
		t.Fatalf("stdout is not one pure JSON object: %v\n%s", err, h.stdout.String())
	}
	for _, key := range []string{
		"schema_version", "operation_id", "action", "outcome",
		"target", "tools", "started_at", "finished_at", "stages", "diagnostics", "data",
	} {
		if _, ok := raw[key]; !ok {
			t.Fatalf("schema v1 key %q is missing:\n%s", key, h.stdout.String())
		}
	}
	var version int
	if err := json.Unmarshal(raw["schema_version"], &version); err != nil {
		t.Fatal(err)
	}
	if version != 1 {
		t.Fatalf("schema_version = %d, want 1", version)
	}
	if got := core.SchemaVersion; got != 1 {
		t.Fatalf("core.SchemaVersion = %d, want 1", got)
	}
	// An unknown key would be an accidental contract addition.
	allowed := map[string]bool{
		"schema_version": true, "operation_id": true, "action": true, "outcome": true,
		"required_approval": true, "target": true, "tools": true, "started_at": true,
		"finished_at": true, "stages": true, "diagnostics": true, "data": true,
	}
	for key := range raw {
		if !allowed[key] {
			t.Fatalf("unexpected JSON key %q: the redesign added a field to schema v1", key)
		}
	}
}

// Exit codes are the published 0/1/2/130 contract.
func TestExitCodesAreUnchanged(t *testing.T) {
	cases := []struct {
		name  string
		feat  *stubFeature
		args  []string
		want  int
		diags []core.Diagnostic
	}{
		{name: "success", feat: &stubFeature{desc: descriptor("status")}, args: []string{"status"}, want: 0},
		{name: "environment failure", feat: &stubFeature{desc: descriptor("status"), run: func(ctx context.Context, in core.Invocation) (any, error) {
			return nil, core.Fail(core.Diagnostic{
				Category: core.CategoryEngineUnreachable, Severity: core.SeverityError,
				Message: "Docker Engine에 연결 불가.",
			})
		}}, args: []string{"status"}, want: 1},
		{name: "decision error", feat: &stubFeature{desc: descriptor("status"), run: func(ctx context.Context, in core.Invocation) (any, error) {
			return nil, core.Fail(core.Diagnostic{
				Category: core.CategoryInvalidOption, Severity: core.SeverityError,
				Message: "알 수 없는 옵션.",
			})
		}}, args: []string{"status"}, want: 2},
		{name: "canceled", feat: &stubFeature{desc: descriptor("status"), run: func(ctx context.Context, in core.Invocation) (any, error) {
			return nil, context.Canceled
		}}, args: []string{"status"}, want: 130},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, tc.feat)
			err := h.run(tc.args...)
			if got := exitCode(err); got != tc.want {
				t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", got, tc.want, h.stdout.String(), h.stderr.String())
			}
		})
	}
}

// An approval-gated feature with no --approve still exits 2 and says what is
// needed, in the same Korean sentence.
func TestApprovalRefusalStillExitsTwoWithTheSameSentence(t *testing.T) {
	d := descriptor("install-compose")
	d.ReadOnly = false
	d.Approval = core.ApprovalInstall
	d.Requires = core.Requirements{core.RequirementDockerCLI}
	h := newHarness(t, &stubFeature{desc: d})
	err := h.run("install-compose")
	if exitCode(err) != 2 {
		t.Fatalf("exit = %d, want 2", exitCode(err))
	}
	out := h.stderr.String()
	for _, want := range []string{"Compose 설치", "승인", "install"} {
		if !strings.Contains(out, want) {
			t.Fatalf("approval refusal missing %q:\n%s", want, out)
		}
	}
}

// Human output is still the feature presenter's text, and it is still free of
// escape sequences: the TUI's theme is never injected into the CLI.
func TestHumanOutputStaysPlainPresenterText(t *testing.T) {
	status := &stubFeature{desc: descriptor("status"), present: func(res core.Result, width int) string {
		if width != 0 {
			t.Fatalf("the CLI passed width %d to Present; it must pass 0", width)
		}
		return "CLI-BODY"
	}}
	h := newHarness(t, status)
	if err := h.run("status"); err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(h.stdout.String(), "CLI-BODY") {
		t.Fatalf("presenter text missing: %q", h.stdout.String())
	}
	if strings.Contains(h.stdout.String(), "\x1b") {
		t.Fatalf("CLI stdout carries an escape sequence: %q", h.stdout.String())
	}
	if h.stderr.String() != "" {
		t.Fatalf("a successful CLI run wrote stderr: %q", h.stderr.String())
	}
}

// The shared failure wording the CLI prints is the same one the TUI renders;
// this pins the CLI half so a wording change on either side is visible.
func TestSharedFailureWordingIsPinned(t *testing.T) {
	res := core.Result{
		SchemaVersion: core.SchemaVersion,
		OperationID:   "op_cli_invariant",
		Action:        "up",
		Outcome:       core.OutcomeFailure,
		Target:        core.Target{ProjectDir: "/work/acme-platform"},
		Stages:        []core.Stage{{Name: "resolve_target", Title: "대상 확인", Status: core.StageSucceeded}},
		Diagnostics: []core.Diagnostic{{
			Category: core.CategoryEngineUnreachable, Severity: core.SeverityError,
			Message: "Docker Engine에 연결 불가.",
			NextActions: []string{
				"Docker Desktop을 시작.",
			},
		}},
	}
	out := view.Failure(res)
	for _, want := range []string{
		"작업 실패: up",
		"대상: /work/acme-platform",
		"작업 ID: op_cli_invariant",
		"문제: Docker Engine에 연결 불가.",
		"다음 행동:",
		"1. Docker Desktop을 시작.",
		"단계:",
		"✓ 대상 확인 (완료)",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("shared failure text changed; missing %q:\n%s", want, out)
		}
	}
}

// A non-interactive invocation still refuses the TUI and prints the usage in
// Korean, listing commands from the registry.
func TestNonInteractiveUsageIsUnchanged(t *testing.T) {
	status := &stubFeature{desc: descriptor("status")}
	h := newHarness(t, status)
	err := h.run()
	if exitCode(err) != 2 {
		t.Fatalf("exit = %d, want 2", exitCode(err))
	}
	if h.homeRan {
		t.Fatal("the TUI opened in a non-interactive context")
	}
	out := h.stderr.String()
	for _, want := range []string{"대화형 터미널이 아니어서", "dcraft status", "--json"} {
		if !strings.Contains(out, want) {
			t.Fatalf("usage text missing %q:\n%s", want, out)
		}
	}
}

// The approval-kind machine values are part of the published contract: the TUI
// collects exactly these strings and the CLI parses exactly these strings.
func TestApprovalMachineValuesAreUnchanged(t *testing.T) {
	want := map[core.ApprovalKind]string{
		core.ApprovalInstall:       "install",
		core.ApprovalScriptTrust:   "script_trust",
		core.ApprovalPreviousGood:  "previous_version",
		core.ApprovalDataRestore:   "data_restore",
		core.ApprovalFileOverwrite: "file_overwrite",
	}
	for kind, value := range want {
		if string(kind) != value {
			t.Fatalf("approval %q = %q, want %q", kind.KoreanLabel(), string(kind), value)
		}
	}
	if core.ApprovalNone != "" {
		t.Fatalf("ApprovalNone = %q, want the empty value", core.ApprovalNone)
	}
}

// The TUI is never an entry point for machine output: it does not depend on the
// CLI package, which owns `--json`.
func TestTheCLIOwnsJSONNotTheTUI(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "../../tui").Output()
	if err != nil {
		t.Skipf("go list is unavailable: %v", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == "github.com/rootmyself/dcraft/internal/ui/cli" {
			t.Fatal("the TUI depends on the CLI package")
		}
	}
}
