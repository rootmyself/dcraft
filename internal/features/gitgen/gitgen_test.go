package gitgen

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rootmyself/dcraft/internal/core"
)

type resolver struct{ project string }

func (r resolver) Resolve(context.Context, core.TargetRequest, string) (core.Target, []core.Diagnostic, error) {
	return core.Target{ProjectDir: r.project}, nil, nil
}

type preflight struct{}

func (preflight) Check(context.Context, core.Requirements) core.PreflightReport {
	return core.PreflightReport{}
}

type state struct{}

func (state) StackDir(core.Target) (string, error)                    { return "", nil }
func (state) AppendOperation(core.Target, core.OperationRecord) error { return nil }

type clock struct{}

func (clock) Now() time.Time { return time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC) }

func newEngine(t *testing.T, project string) *core.Engine {
	t.Helper()
	reg := core.NewRegistry()
	if err := reg.Register(New()); err != nil {
		t.Fatalf("Register: %v", err)
	}
	engine := core.NewEngine(reg, resolver{project: project}, preflight{}, state{}, clock{})
	engine.CWD = func() (string, error) { return project, nil }
	engine.NewOperationID = func() string { return "op_gitgen" }
	return engine
}

func request(project string, approvals []core.ApprovalKind, options map[string]string) core.Request {
	return core.Request{
		Feature:   "generate-git-hook",
		Target:    core.TargetRequest{Path: project},
		Options:   options,
		Approvals: approvals,
		JSON:      true,
	}
}

func options(project string) map[string]string {
	return map[string]string{
		"repo":   "https://example.com/team/app.git",
		"ref":    "main",
		"dest":   "src",
		"output": "scripts/git-sync.sh",
		"stage":  "pre",
	}
}

func resultPayload(t *testing.T, res core.Result) Payload {
	t.Helper()
	raw, err := json.Marshal(res.Data)
	if err != nil {
		t.Fatal(err)
	}
	var payload Payload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestDescriptorIsAFlatWizardWithTheFileOverwriteApproval(t *testing.T) {
	d := New().Descriptor()
	if d.Name != "generate-git-hook" {
		t.Fatalf("name = %q", d.Name)
	}
	if d.Approval != core.ApprovalFileOverwrite {
		t.Fatalf("approval = %q", d.Approval)
	}
	if !d.Wizard || !d.TargetOptional {
		t.Fatalf("wizard/targetOptional = %v/%v", d.Wizard, d.TargetOptional)
	}
	if d.ReadOnly {
		t.Fatal("generator cannot be read-only: it saves a file")
	}
	required := map[string]bool{}
	for _, flag := range d.Flags {
		required[flag.Name] = flag.Required
	}
	for _, name := range []string{"repo", "ref", "dest", "output"} {
		if !required[name] {
			t.Fatalf("--%s must be required for non-interactive input", name)
		}
	}
}

func TestRunCreatesTheScriptAndNeverRegistersOrRunsIt(t *testing.T) {
	project := t.TempDir()
	engine := newEngine(t, project)
	res := engine.Run(context.Background(), request(project, []core.ApprovalKind{core.ApprovalFileOverwrite}, options(project)), nil)
	if res.Outcome != core.OutcomeSuccess || res.ExitCode() != 0 {
		t.Fatalf("result = %+v", res)
	}
	scriptPath := filepath.Join(project, "scripts", "git-sync.sh")
	info, err := os.Stat(scriptPath)
	if err != nil {
		t.Fatalf("script missing: %v", err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
	content, _ := os.ReadFile(scriptPath)
	for _, want := range []string{"#!/usr/bin/env bash", "set -euo pipefail", "script_trust", "status --porcelain"} {
		if !strings.Contains(string(content), want) {
			t.Fatalf("script missing %q", want)
		}
	}
	if _, err := os.Stat(filepath.Join(project, ".dcraft.yaml")); !os.IsNotExist(err) {
		t.Fatalf("generator wrote a hook registration: %v", err)
	}
	payload := resultPayload(t, res)
	if payload.Generator.Registered || !payload.Generator.TrustRequired || !payload.Generator.Created {
		t.Fatalf("payload = %+v", payload.Generator)
	}
	if len(payload.Generator.NextCommands) < 2 {
		t.Fatalf("next commands missing: %+v", payload.Generator.NextCommands)
	}
	joined := strings.Join(payload.Generator.NextCommands, "\n")
	if !strings.Contains(joined, "dcraft hooks --add --stage pre --script") {
		t.Fatalf("registration command missing:\n%s", joined)
	}
	if !strings.Contains(joined, "script_trust") {
		t.Fatalf("trust command missing:\n%s", joined)
	}
}

func TestRunWithoutApprovalCreatesNothingForANewFile(t *testing.T) {
	project := t.TempDir()
	engine := newEngine(t, project)
	res := engine.Run(context.Background(), request(project, nil, options(project)), nil)
	if res.ExitCode() != 2 {
		t.Fatalf("exit = %d result=%+v", res.ExitCode(), res)
	}
	if _, err := os.Stat(filepath.Join(project, "scripts")); !os.IsNotExist(err) {
		t.Fatalf("unapproved generation created a script: %v", err)
	}
}

func TestRunWithoutFileOverwriteApprovalLeavesAnExistingScriptUntouched(t *testing.T) {
	project := t.TempDir()
	path := filepath.Join(project, "scripts", "git-sync.sh")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("keep me\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	engine := newEngine(t, project)
	res := engine.Run(context.Background(), request(project, nil, options(project)), nil)
	if res.ExitCode() != 2 || res.RequiredApproval != core.ApprovalFileOverwrite {
		t.Fatalf("result = %+v exit=%d", res, res.ExitCode())
	}
	if data, _ := os.ReadFile(path); string(data) != "keep me\n" {
		t.Fatalf("existing script changed: %q", data)
	}
}

func TestRunWithFileOverwriteApprovalReplacesTheScriptAndReportsIt(t *testing.T) {
	project := t.TempDir()
	path := filepath.Join(project, "scripts", "git-sync.sh")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("old\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	engine := newEngine(t, project)
	res := engine.Run(context.Background(), request(project, []core.ApprovalKind{core.ApprovalFileOverwrite}, options(project)), nil)
	if res.Outcome != core.OutcomeSuccess {
		t.Fatalf("result = %+v", res)
	}
	payload := resultPayload(t, res)
	if payload.Generator.Created || !payload.Generator.Replaced {
		t.Fatalf("payload = %+v", payload.Generator)
	}
	if data, _ := os.ReadFile(path); strings.Contains(string(data), "old\n") {
		t.Fatalf("script not replaced: %q", data)
	}
}

func TestRunReportsAWriteFailureAsExitOne(t *testing.T) {
	project := t.TempDir()
	locked := filepath.Join(project, "locked")
	if err := os.MkdirAll(locked, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	engine := newEngine(t, project)
	opts := options(project)
	opts["output"] = "locked/git-sync.sh"
	res := engine.Run(context.Background(), request(project, []core.ApprovalKind{core.ApprovalFileOverwrite}, opts), nil)
	if res.ExitCode() != 1 || !hasCategory(res, core.CategoryGeneratorFailed) {
		t.Fatalf("exit=%d diagnostics=%+v", res.ExitCode(), res.Diagnostics)
	}
}

func TestRunPropagatesCancellationAsExit130(t *testing.T) {
	project := t.TempDir()
	engine := newEngine(t, project)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := engine.Run(ctx, request(project, []core.ApprovalKind{core.ApprovalFileOverwrite}, options(project)), nil)
	if res.ExitCode() != 130 || res.Outcome != core.OutcomeCanceled {
		t.Fatalf("outcome=%q exit=%d", res.Outcome, res.ExitCode())
	}
}

func TestRunFailsFastOnMissingOrUnsafeInput(t *testing.T) {
	project := t.TempDir()
	engine := newEngine(t, project)
	cases := []map[string]string{
		{},
		func() map[string]string { o := options(project); delete(o, "repo"); return o }(),
		func() map[string]string { o := options(project); delete(o, "ref"); return o }(),
		func() map[string]string { o := options(project); o["dest"] = "."; return o }(),
		func() map[string]string { o := options(project); o["dest"] = "../outside"; return o }(),
		func() map[string]string {
			o := options(project)
			o["dest"] = "src"
			o["output"] = "src/git.sh"
			return o
		}(),
		func() map[string]string {
			o := options(project)
			o["repo"] = "https://user:secret@example.com/r.git"
			return o
		}(),
		func() map[string]string { o := options(project); o["stage"] = "later"; return o }(),
	}
	for _, opts := range cases {
		res := engine.Run(context.Background(), request(project, []core.ApprovalKind{core.ApprovalFileOverwrite}, opts), nil)
		if res.ExitCode() != 2 {
			t.Fatalf("options %v: exit = %d result=%+v", opts, res.ExitCode(), res)
		}
		if !hasCategory(res, core.CategoryGeneratorInputInvalid) {
			t.Fatalf("options %v: category = %+v", opts, res.Diagnostics)
		}
	}
	if _, err := os.Stat(filepath.Join(project, "scripts")); !os.IsNotExist(err) {
		t.Fatalf("failed validation wrote a script: %v", err)
	}
}

func TestPlanExplainsEffectsPrerequisitesAuthenticationAndNextSteps(t *testing.T) {
	project := t.TempDir()
	engine := newEngine(t, project)
	report := engine.Plan(context.Background(), request(project, nil, options(project)), nil)
	if report.Failed() {
		t.Fatalf("plan = %+v", report)
	}
	for _, want := range []string{
		"Git 호스트 스크립트", "원격 저장소에 접속하지 않음", "clone", "fetch",
		"git CLI", "credential helper", "SSH agent", "인증 정보",
		"file_overwrite", "dcraft hooks --add", "script_trust", "자동 등록하지 않음",
		"실패", "수정",
	} {
		if !strings.Contains(report.Text, want) {
			t.Fatalf("plan missing %q:\n%s", want, report.Text)
		}
	}
	if len(report.RequiredApprovals) != 1 || report.RequiredApprovals[0] != core.ApprovalFileOverwrite {
		t.Fatalf("required approvals = %+v", report.RequiredApprovals)
	}
	if _, err := os.Stat(filepath.Join(project, "scripts", "git-sync.sh")); !os.IsNotExist(err) {
		t.Fatal("plan wrote a script")
	}
}

func TestWizardStepsCollectTheSameInputsAndPreviewSavesNothing(t *testing.T) {
	project := t.TempDir()
	feature := New()
	in := core.Invocation{
		Target:  core.Target{ProjectDir: project},
		Options: map[string]string{"wizard": "steps"},
	}
	specAny, err := feature.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("steps: %v", err)
	}
	spec, ok := specAny.(core.WizardSpec)
	if !ok {
		t.Fatalf("steps payload = %T", specAny)
	}
	var names []string
	for _, step := range spec.Steps {
		names = append(names, step.Name)
	}
	for _, want := range []string{"repo", "ref", "dest", "stage"} {
		if !strings.Contains(strings.Join(names, ","), want) {
			t.Fatalf("wizard steps = %v", names)
		}
	}
	preview, err := feature.WizardPreview(map[string]string{
		"repo": "https://example.com/team/app.git", "ref": "main",
		"dest": "src", "output": "scripts/git-sync.sh", "stage": "pre",
	})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if !strings.Contains(preview, "file_overwrite") || !strings.Contains(preview, "script_trust") {
		t.Fatalf("preview missing approvals:\n%s", preview)
	}
	if _, err := os.Stat(filepath.Join(project, "scripts")); !os.IsNotExist(err) {
		t.Fatal("wizard preview wrote a script")
	}
}

// The TUI document declares the script paths and points at the menu entry that
// registers and approves the script. The CLI keeps the exact commands.
func TestGitgenDocumentTagsPathsAndCommands(t *testing.T) {
	feature := New()
	res := core.Result{Outcome: core.OutcomeSuccess, Data: Payload{Generator: Report{
		Script: "scripts/git-sync.sh", Destination: "src", Ref: "main", RefKind: "branch",
		SHA256: "abc", NextCommands: []string{"dcraft hooks --add --stage pre"},
		Notices: []string{"신뢰 승인이 필요."},
	}}}
	doc := feature.Document(res)
	if len(doc.Blocks) != 1 || doc.Blocks[0].Kind != core.BlockValueLines {
		t.Fatalf("document = %+v", doc)
	}
	kinds := map[core.ValueKind]int{}
	var text []string
	for _, line := range doc.Blocks[0].Lines {
		text = append(text, line.Text())
		for _, run := range line {
			kinds[run.Kind]++
		}
	}
	if kinds[core.ValuePath] != 2 {
		t.Fatalf("kinds = %v, want two paths", kinds)
	}
	if kinds[core.ValueCommand] != 0 {
		t.Fatalf("the document still carries command runs: %v", kinds)
	}
	docText := strings.Join(text, "\n")
	if strings.Contains(docText, "다음 명령") || strings.Contains(docText, "다음 행동") {
		t.Fatalf("the success document still carries guidance: %q", docText)
	}
	if strings.Contains(docText, "dcraft hooks --add") {
		t.Fatalf("the document still tells an interactive reader to type the command: %q", docText)
	}
	// The CLI text is unchanged.
	if cliText := feature.Present(res, 0); !strings.Contains(cliText, "dcraft hooks --add --stage pre") {
		t.Fatalf("CLI text lost its command: %q", cliText)
	}
}

func hasCategory(res core.Result, category core.Category) bool {
	for _, d := range res.Diagnostics {
		if d.Category == category {
			return true
		}
	}
	return false
}
