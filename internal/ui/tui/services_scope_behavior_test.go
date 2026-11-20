package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// The confirmed subset must reach the run unchanged: the exact names travel as
// Request.Services, and the result's own scope line states the same set. A
// scope that silently widened would change what the command targets.
func TestScopeTableConfirmedScopeReachesTheRunUnchanged(t *testing.T) {
	catalog := newCatalogStatus(scopeServices())
	stop := &serviceCaptureFeature{desc: serviceDescriptor("stop"), body: "중지 본문"}
	m := newServiceModel(t, catalog, stop)
	m.theme = *plainTheme()
	initModel(t, m)
	resize(t, m, 120, 36)
	chooseFeature(t, m, "stop")

	pressDown(m) // api
	pressSpace(m)
	pressDown(m) // worker
	pressSpace(m)
	feedCmd(t, m, press(m, tea.KeyMsg{Type: tea.KeyEnter}))
	if got := strings.Join(m.req.Services, ","); got != "api,worker" {
		t.Fatalf("confirm carries %q, want api,worker", got)
	}
	feedCmd(t, m, press(m, tea.KeyMsg{Type: tea.KeyEnter}))

	if stop.runs != 1 {
		t.Fatalf("runs = %d, want 1", stop.runs)
	}
	if got := strings.Join(stop.got.Services, ","); got != "api,worker" {
		t.Fatalf("run services = %q, want api,worker", got)
	}
}

// Deselecting every service returns to the whole-stack scope, and the run then
// carries no list at all. An empty list is the documented whole-stack request,
// so this must not be confused with "nothing selected".
func TestScopeTableClearingEveryServiceRestoresTheWholeStack(t *testing.T) {
	catalog := newCatalogStatus(scopeServices())
	stop := &serviceCaptureFeature{desc: serviceDescriptor("stop"), body: "중지 본문"}
	m := newServiceModel(t, catalog, stop)
	m.theme = *plainTheme()
	initModel(t, m)
	resize(t, m, 120, 36)
	chooseFeature(t, m, "stop")

	pressDown(m)
	pressSpace(m)
	if m.serviceWhole {
		t.Fatal("the subset was not recorded")
	}
	pressSpace(m) // toggle the only checked service back off
	if !m.serviceWhole {
		t.Fatal("clearing every service did not restore the whole-stack scope")
	}

	feedCmd(t, m, press(m, tea.KeyMsg{Type: tea.KeyEnter}))
	if !strings.Contains(m.View(), "범위: 스택 전체") {
		t.Fatalf("whole-stack scope is not shown:\n%s", m.View())
	}
	feedCmd(t, m, press(m, tea.KeyMsg{Type: tea.KeyEnter}))
	if len(stop.got.Services) != 0 {
		t.Fatalf("whole-stack run carried a list: %v", stop.got.Services)
	}
}

// esc cancels and runs nothing, keeping the "nothing ran" result explicit.
func TestScopeTableEscCancelsWithoutWidening(t *testing.T) {
	catalog := newCatalogStatus(scopeServices())
	stop := &serviceCaptureFeature{desc: serviceDescriptor("stop"), body: "중지 본문"}
	m := newServiceModel(t, catalog, stop)
	m.theme = *plainTheme()
	initModel(t, m)
	resize(t, m, 120, 36)
	chooseFeature(t, m, "stop")

	pressDown(m)
	pressSpace(m)
	press(m, tea.KeyMsg{Type: tea.KeyEsc})

	if m.phase != phaseReady {
		t.Fatalf("phase = %v, want ready", m.phase)
	}
	if stop.runs != 0 {
		t.Fatal("esc ran the feature")
	}
	if len(m.req.Services) != 0 {
		t.Fatalf("a canceled selection was kept: %v", m.req.Services)
	}
	if !strings.Contains(m.View(), "아무것도 실행하지 않았습니다") {
		t.Fatalf("cancel notice missing:\n%s", m.View())
	}
}

// A large stack on the smallest approved screen keeps every line inside the
// width, including the folded header row and the scroll hint.
func TestScopeTableLargeStackFitsNarrowScreens(t *testing.T) {
	for _, size := range [][2]int{{40, 18}, {48, 20}, {60, 18}} {
		m := scopeModel(t, manyServices(25))
		resize(t, m, size[0], size[1])
		for i := 0; i <= 25; i++ {
			assertFits(t, m, size[0], size[1])
			pressDown(m)
		}
	}
}
