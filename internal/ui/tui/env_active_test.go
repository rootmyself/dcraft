package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rootmyself/dcraft/internal/core"
)

// activeEnvModel is a two-entry environment menu over a stub resolver that can
// record the active alias.
func activeEnvModel(t *testing.T, active core.EnvironmentActive, target core.TargetRequest) (*Model, *stubEnvResolver) {
	t.Helper()
	status := &stubFeature{desc: descriptor("status", true), body: "상태 본문"}
	adminDesc := descriptor("aliases", false)
	adminDesc.Menu = envMenuEntries("aliases")
	admin := &stubFeature{desc: adminDesc}
	resolver := envResolver()
	resolver.active = active
	reg := core.NewRegistry()
	for _, f := range []core.Feature{status, admin} {
		if err := reg.Register(f); err != nil {
			t.Fatal(err)
		}
	}
	engine := core.NewEngine(reg, resolver, stubPreflight{}, stubState{}, stubClock{})
	engine.CWD = func() (string, error) { return "/cwd", nil }
	engine.NewOperationID = func() string { return "op_tui_env" }
	m := NewModel(contextBackground(), Options{Engine: engine, Registry: reg, Target: target})
	m.theme = *plainTheme()
	return m, resolver
}

// A recorded alias the project still registers starts the session on it: the
// reader made that choice once, so the startup screen is skipped.
func TestRecordedActiveEnvironmentStartsWithoutTheSelectionScreen(t *testing.T) {
	m, resolver := activeEnvModel(t, core.EnvironmentActive{Name: "dev"}, core.TargetRequest{})
	initModel(t, m)
	if strings.Contains(m.View(), msgEnvSelectTitle) {
		t.Fatalf("the selection screen appeared despite a recorded alias:\n%s", m.View())
	}
	if m.target.Environment != "dev" || m.facts.Environment != "dev" {
		t.Fatalf("target=%q header=%q, want dev", m.target.Environment, m.facts.Environment)
	}
	if resolver.lastTarget.Environment != "dev" {
		t.Fatalf("the primary feature ran on %q, want dev", resolver.lastTarget.Environment)
	}
}

// A stored value that no longer names a usable alias is stated above the
// selection screen and left in the file: this screen does not edit a value the
// reader has not chosen again.
func TestInvalidRecordedEnvironmentIsStatedAndChosenAgain(t *testing.T) {
	m, resolver := activeEnvModel(t,
		core.EnvironmentActive{Name: "gone", Invalid: true},
		core.TargetRequest{})
	initModel(t, m)
	view := m.View()
	if !strings.Contains(view, msgEnvSelectTitle) {
		t.Fatalf("the selection screen did not appear:\n%s", view)
	}
	if !strings.Contains(view, "저장된 활성 실행환경을 쓸 수 없음: gone") {
		t.Fatalf("the ignored value is not stated:\n%s", view)
	}
	// Choosing again records the new alias.
	m.envSelected = 0 // dev
	cmd := press(m, tea.KeyMsg{Type: tea.KeyEnter})
	feed(t, m, execute(t, cmd))
	if len(resolver.saved) != 1 || resolver.saved[0] != "dev" {
		t.Fatalf("the new choice was not recorded: %v", resolver.saved)
	}
}

// Switching from the menu records the alias, states the completion and reopens
// the read-only primary screen on the new target.
func TestSwitchRecordsTheAliasAndReopensThePrimary(t *testing.T) {
	m, resolver := activeEnvModel(t, core.EnvironmentActive{}, core.TargetRequest{Environment: "dev"})
	initModel(t, m)
	openSwitchList(t, m)
	m.envSelected = 1 // prod
	cmd := press(m, tea.KeyMsg{Type: tea.KeyEnter})
	feed(t, m, execute(t, cmd))

	if m.target.Environment != "prod" {
		t.Fatalf("target = %q, want prod", m.target.Environment)
	}
	if len(resolver.saved) != 1 || resolver.saved[0] != "prod" {
		t.Fatalf("saved = %v, want prod", resolver.saved)
	}
	if view := m.View(); !strings.Contains(view, "실행환경 전환 완료: prod") {
		t.Fatalf("the completion is not stated:\n%s", view)
	}
	if resolver.lastTarget.Environment != "prod" {
		t.Fatalf("the primary screen ran on %q, want prod", resolver.lastTarget.Environment)
	}
}

// Choosing the environment that is already current changes nothing: no save, no
// rerun, and one line stating it.
func TestSwitchToTheCurrentEnvironmentDoesNothing(t *testing.T) {
	m, resolver := activeEnvModel(t, core.EnvironmentActive{}, core.TargetRequest{Environment: "dev"})
	initModel(t, m)
	baseline := resolver.envCalls
	openSwitchList(t, m)
	m.envSelected = 0 // dev, the current one
	cmd := press(m, tea.KeyMsg{Type: tea.KeyEnter})
	feed(t, m, execute(t, cmd))

	if len(resolver.saved) != 0 {
		t.Fatalf("the current environment was recorded again: %v", resolver.saved)
	}
	if view := m.View(); !strings.Contains(view, "이미 현재 실행환경: dev") {
		t.Fatalf("the screen did not state the current environment:\n%s", view)
	}
	// The primary screen was not reopened: the list read is the only call.
	if resolver.envCalls != baseline+1 {
		t.Fatalf("the primary screen ran again (env calls %d -> %d)", baseline, resolver.envCalls)
	}
}

// With no alias at all the switch screen is not opened: the menu states the next
// action instead.
func TestSwitchWithNoAliasesStaysOnTheMenu(t *testing.T) {
	m, resolver := activeEnvModel(t, core.EnvironmentActive{}, core.TargetRequest{Environment: "dev"})
	resolver.envs = nil
	initModel(t, m)
	openSwitchList(t, m)
	if m.phase != phaseReady {
		t.Fatalf("phase = %v, want the menu", m.phase)
	}
	if view := m.View(); !strings.Contains(view, msgEnvSwitchEmpty) {
		t.Fatalf("the empty switch state is not stated:\n%s", view)
	}
}

// A failure to record the alias never undoes the switch: the target moved, and
// the reader is told the choice will not persist.
func TestActiveSaveFailureKeepsTheSwitch(t *testing.T) {
	m, resolver := activeEnvModel(t, core.EnvironmentActive{}, core.TargetRequest{Environment: "dev"})
	resolver.saveErr = errors.New("disk full")
	initModel(t, m)
	openSwitchList(t, m)
	m.envSelected = 1 // prod
	cmd := press(m, tea.KeyMsg{Type: tea.KeyEnter})
	feed(t, m, execute(t, cmd))

	if m.target.Environment != "prod" {
		t.Fatalf("the switch was undone: target=%q", m.target.Environment)
	}
	view := m.View()
	if !strings.Contains(view, "실행환경 전환 완료: prod") {
		t.Fatalf("the switch is not reported:\n%s", view)
	}
	if !strings.Contains(view, msgEnvActiveSaveFailed) {
		t.Fatalf("the save failure is not stated:\n%s", view)
	}
}

// The completion notice lives until the next key: it describes the change that
// just happened.
func TestEnvironmentCompletionClearsOnTheNextKey(t *testing.T) {
	m, _ := activeEnvModel(t, core.EnvironmentActive{}, core.TargetRequest{Environment: "dev"})
	initModel(t, m)
	openSwitchList(t, m)
	m.envSelected = 1
	cmd := press(m, tea.KeyMsg{Type: tea.KeyEnter})
	feed(t, m, execute(t, cmd))
	if !strings.Contains(m.View(), "전환 완료") {
		t.Fatalf("the completion is not drawn")
	}
	press(m, tea.KeyMsg{Type: tea.KeyUp})
	if strings.Contains(m.View(), "전환 완료") {
		t.Fatalf("the completion survived a key:\n%s", m.View())
	}
}

// The remove flow clears the record and returns the project to the standard
// Compose inputs when the removed alias was the active one.
func TestRemovingTheActiveAliasClearsTheRecordAndTheTarget(t *testing.T) {
	m, resolver := activeEnvModel(t, core.EnvironmentActive{Name: "dev"}, core.TargetRequest{Environment: "dev"})
	initModel(t, m)
	cmd := m.clearRemovedEnvironment("dev")
	feed(t, m, execute(t, cmd))

	if m.target.Environment != "" {
		t.Fatalf("target = %q, want the standard inputs", m.target.Environment)
	}
	if len(resolver.saved) != 1 || resolver.saved[0] != "" {
		t.Fatalf("saved = %v, want one clear", resolver.saved)
	}
	if view := m.View(); !strings.Contains(view, "실행환경 제거 완료: dev") {
		t.Fatalf("the removal is not stated:\n%s", view)
	}
}

// Removing some other alias leaves the active record alone.
func TestRemovingAnotherAliasKeepsTheRecord(t *testing.T) {
	m, resolver := activeEnvModel(t, core.EnvironmentActive{Name: "dev"}, core.TargetRequest{Environment: "dev"})
	initModel(t, m)
	cmd := m.clearRemovedEnvironment("stage")
	feed(t, m, execute(t, cmd))

	if m.target.Environment != "dev" {
		t.Fatalf("the active target changed: %q", m.target.Environment)
	}
	if len(resolver.saved) != 1 || resolver.saved[0] != "dev" {
		t.Fatalf("saved = %v, want the unchanged active alias", resolver.saved)
	}
}

// A generated alias is adopted as the target, recorded, and the primary screen
// reopens on it.
func TestCreatedAliasBecomesActive(t *testing.T) {
	m, resolver := activeEnvModel(t, core.EnvironmentActive{}, core.TargetRequest{Environment: "dev"})
	initModel(t, m)
	cmd := m.activateCreatedEnvironment("staging")
	feed(t, m, execute(t, cmd))

	if m.target.Environment != "staging" {
		t.Fatalf("target = %q, want staging", m.target.Environment)
	}
	if len(resolver.saved) != 1 || resolver.saved[0] != "staging" {
		t.Fatalf("saved = %v, want staging", resolver.saved)
	}
	if view := m.View(); !strings.Contains(view, "실행환경 생성 완료: staging") {
		t.Fatalf("the creation is not stated:\n%s", view)
	}
}

// A mutating primary is never launched by an environment change: it would open a
// confirmation or a wizard on its own.
func TestEnvironmentChangeDoesNotLaunchAMutatingPrimary(t *testing.T) {
	mutating := &stubFeature{desc: mutatingDescriptor("up")}
	adminDesc := descriptor("aliases", false)
	adminDesc.Menu = envMenuEntries("aliases")
	admin := &stubFeature{desc: adminDesc}
	resolver := envResolver()
	reg := core.NewRegistry()
	for _, f := range []core.Feature{mutating, admin} {
		if err := reg.Register(f); err != nil {
			t.Fatal(err)
		}
	}
	// A mutating primary needs an approval to register.
	mutating.desc.Approval = core.ApprovalInstall
	engine := core.NewEngine(reg, resolver, stubPreflight{}, stubState{}, stubClock{})
	engine.CWD = func() (string, error) { return "/cwd", nil }
	m := NewModel(contextBackground(), Options{Engine: engine, Registry: reg, Target: core.TargetRequest{Environment: "dev"}})
	m.theme = *plainTheme()
	initModel(t, m)
	runs := mutating.runs
	cmd := m.activateCreatedEnvironment("staging")
	feed(t, m, execute(t, cmd))
	if mutating.runs != runs {
		t.Fatalf("an environment change launched the mutating primary")
	}
	if m.phase != phaseReady {
		t.Fatalf("phase = %v, want the menu", m.phase)
	}
}
