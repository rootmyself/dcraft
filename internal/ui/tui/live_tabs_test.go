package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rootmyself/dcraft/internal/core"
)

// logScreen builds the live screen a log run opens for the given services
// without starting the children: the tab row, the selection and the keys are
// what these tests describe, and a real child would only add timing.
func logScreen(t *testing.T, live *fakeLiveFeature, services ...string) *Model {
	t.Helper()
	m := newTestModel(t, live)
	m.theme = *plainTheme()
	m.active = live
	m.phase = phaseLoading
	m.target = core.TargetRequest{Path: "/work"}
	m.req = core.Request{Feature: "logs", Target: m.target, Services: services}
	m.liveCh = make(chan struct{}, 1)
	m.live = &liveState{mode: core.LiveStream}
	for _, name := range logStreamNames(services) {
		stream := m.newLiveStream(name)
		m.live.streams = append(m.live.streams, stream)
	}
	m.liveFollow = true
	return m
}

// A log screen with several services draws one tab per service in name order,
// and the cursor starts on the first one.
func TestLiveLogTabsOpenInNameOrderWithTheFirstSelected(t *testing.T) {
	live := streamingLogs(t, func(context.Context, core.Invocation) (any, error) { return nil, nil })
	m := logScreen(t, live, "web", "db", "api")
	resize(t, m, 100, 30)

	if got := len(m.live.streams); got != 3 {
		t.Fatalf("streams = %d, want one per service", got)
	}
	for i, want := range []string{"api", "db", "web"} {
		if got := m.live.streams[i].name; got != want {
			t.Fatalf("tab %d = %q, want name order %q", i, got, want)
		}
	}
	row := m.liveTabRow()
	if !strings.Contains(row, "[api]") {
		t.Fatalf("first tab is not the current one: %q", row)
	}
	// Output goes to its own tab's grid, never to a shared one.
	for _, stream := range m.live.streams {
		fmt.Fprintf(stream.writer, "%s-only\n", stream.name)
	}
	if !strings.Contains(m.View(), "api-only") {
		t.Fatalf("the current tab's output is not drawn:\n%s", m.View())
	}
	if strings.Contains(m.View(), "web-only") {
		t.Fatalf("another tab's output is drawn:\n%s", m.View())
	}
}

// A finished stream says so on its own label, and a failure is named as one.
func TestLiveLogTabsLabelFinishedAndFailedStreams(t *testing.T) {
	live := streamingLogs(t, func(context.Context, core.Invocation) (any, error) { return nil, nil })
	m := logScreen(t, live, "api", "db")
	m.live.streams[0].done = true
	m.live.streams[1].done = true
	m.live.streams[1].err = "exit status 1"

	row := m.liveTabRow()
	if !strings.Contains(row, "api · 종료") {
		t.Fatalf("a clean exit is not labelled: %q", row)
	}
	if !strings.Contains(row, "db · 실패") {
		t.Fatalf("a failed stream is not labelled: %q", row)
	}
}

// tab and shift+tab switch the visible tab and wrap at both ends.
func TestLiveLogTabsSwitchWithTabKeys(t *testing.T) {
	live := streamingLogs(t, func(context.Context, core.Invocation) (any, error) { return nil, nil })
	m := logScreen(t, live, "api", "db")
	resize(t, m, 100, 30)
	fmt.Fprintln(m.live.streams[0].writer, "api-output")
	fmt.Fprintln(m.live.streams[1].writer, "db-output")

	pressKey(t, m, tea.KeyMsg{Type: tea.KeyTab})
	if got := m.live.selected; got != 1 {
		t.Fatalf("selected tab after tab = %d, want 1", got)
	}
	if !strings.Contains(m.View(), "db-output") {
		t.Fatalf("the second tab's output is not drawn:\n%s", m.View())
	}
	pressKey(t, m, tea.KeyMsg{Type: tea.KeyTab})
	if got := m.live.selected; got != 0 {
		t.Fatalf("tab did not wrap to the first: %d", got)
	}
	pressKey(t, m, tea.KeyMsg{Type: tea.KeyShiftTab})
	if got := m.live.selected; got != 1 {
		t.Fatalf("shift+tab did not move back: %d", got)
	}
}

// The footer names the tab key only when there is more than one tab.
func TestLiveLogTabKeyIsAdvertisedOnlyWithSeveralTabs(t *testing.T) {
	live := streamingLogs(t, func(context.Context, core.Invocation) (any, error) { return nil, nil })
	one := logScreen(t, live, "api")
	resize(t, one, 100, 30)
	if strings.Contains(one.View(), "[tab]") {
		t.Fatalf("a single-tab screen advertises a key that does nothing:\n%s", one.View())
	}
	two := logScreen(t, live, "api", "db")
	resize(t, two, 100, 30)
	if !strings.Contains(two.View(), "[tab]") {
		t.Fatalf("a two-tab screen does not advertise the tab key:\n%s", two.View())
	}
}

// scopeDeclaringCatalog adds the descriptor capability the generic scope screen
// reads, so the test drives the same decision a real logs feature declares.
type scopeDeclaringCatalog struct {
	*catalogStatus
	required bool
	max      int
}

func (s *scopeDeclaringCatalog) RequiresServiceScope() bool { return s.required }
func (s *scopeDeclaringCatalog) MaxServices() int           { return s.max }
func (s *scopeDeclaringCatalog) Required() scopeMode {
	if s.required {
		return scopeRequired
	}
	return scopeWholeStack
}

// The selection screen that feeds logs has no whole-stack row, refuses Enter
// with nothing chosen, and caps the selection at four services.
func TestLogsScopeScreenRequiresASelectionAndCapsIt(t *testing.T) {
	catalog := newCatalogStatus([]core.ServiceStatus{
		{Name: "api", State: "running", Health: "healthy", Instances: 1, Configured: true},
		{Name: "db", State: "running", Health: "healthy", Instances: 1, Configured: true},
		{Name: "web", State: "running", Health: "healthy", Instances: 1, Configured: true},
		{Name: "worker", State: "running", Health: "healthy", Instances: 1, Configured: true},
		{Name: "cache", State: "running", Health: "healthy", Instances: 1, Configured: true},
	})
	catalog.desc.SupportsServices = true
	catalog.desc.Name = "logs"
	catalog.desc.Title = "서비스 로그"
	scope := &scopeDeclaringCatalog{catalogStatus: catalog, required: true, max: 4}
	m := newServiceModel(t, catalog)
	m.theme = *plainTheme()
	initModel(t, m)
	m.serviceScope = scope.Required()
	m.serviceCap = 4
	if m.phase != phaseSelectingServices {
		t.Fatalf("phase = %v, want the scope screen", m.phase)
	}
	if strings.Contains(m.View(), "전체 (스택 전체)") {
		t.Fatalf("a required-scope screen still offers the whole stack:\n%s", m.View())
	}
	if !strings.Contains(m.View(), "최대 4개") {
		t.Fatalf("the screen does not state the cap:\n%s", m.View())
	}
	// Enter with nothing chosen states what is missing and runs nothing.
	pressKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if catalog.runs != 0 {
		t.Fatalf("runs = %d, want 0 without a selection", catalog.runs)
	}
	if m.phase != phaseSelectingServices {
		t.Fatalf("phase after an empty confirm = %v, want the scope screen", m.phase)
	}
	if m.notice != msgServiceRequired {
		t.Fatalf("notice = %q, want %q", m.notice, msgServiceRequired)
	}
	// Choosing five services keeps the first four and states the cap.
	for i := 0; i < 5; i++ {
		pressKey(t, m, tea.KeyMsg{Type: tea.KeySpace})
		pressKey(t, m, tea.KeyMsg{Type: tea.KeyDown})
	}
	if got := countChecked(m.serviceChecked); got != 4 {
		t.Fatalf("checked = %d, want the four chosen before the cap", got)
	}
	if m.notice != fmt.Sprintf(msgServiceCap, 4) {
		t.Fatalf("notice after the fifth pick = %q, want the cap", m.notice)
	}
}
