package tui

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rootmyself/dcraft/internal/core"
)

type fakeLiveFeature struct {
	desc   core.Descriptor
	mode   core.LiveMode
	run    func(ctx context.Context, in core.Invocation) (any, error)
	mu     sync.Mutex
	runs   int
	gotInv *core.Invocation
}

func (f *fakeLiveFeature) Descriptor() core.Descriptor { return f.desc }
func (f *fakeLiveFeature) LiveMode() core.LiveMode     { return f.mode }
func (f *fakeLiveFeature) CanceledDiagnostic() core.Diagnostic {
	return core.Diagnostic{
		Category:    core.CategoryCanceled,
		Severity:    core.SeverityWarning,
		Message:     "실시간 화면을 분리했습니다. 서비스는 계속 실행 중입니다.",
		NextActions: []string{"dcraft status로 상태를 확인하세요."},
	}
}

func (f *fakeLiveFeature) Run(ctx context.Context, in core.Invocation) (any, error) {
	f.mu.Lock()
	f.runs++
	copied := in
	f.gotInv = &copied
	f.mu.Unlock()
	if f.run != nil {
		return f.run(ctx, in)
	}
	return "live", nil
}

type fakeInputFeature struct {
	*fakeLiveFeature
	fields  []core.InputField
	mu      sync.Mutex
	applied core.Request
}

func (f *fakeInputFeature) Inputs(ctx context.Context, in core.Invocation) ([]core.InputField, error) {
	if len(in.Services) > 0 && len(in.Args) > 0 {
		return nil, nil
	}
	return f.fields, nil
}

func (f *fakeInputFeature) WithInputs(req core.Request, values map[string]string) (core.Request, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applied = req
	req.Services = []string{values["instance"]}
	req.Args = []string{values["command"]}
	f.applied = req
	return req, nil
}

func inputDescriptor(name string) core.Descriptor {
	d := descriptor(name, false)
	d.AcceptsArgs = true
	d.SupportsServices = true
	return d
}

type msgQueue struct {
	ch chan tea.Msg
}

func startCmds(cmd tea.Cmd) *msgQueue {
	q := &msgQueue{ch: make(chan tea.Msg, 256)}
	q.launch(cmd)
	return q
}

func (q *msgQueue) launch(c tea.Cmd) {
	if c == nil {
		return
	}
	go func() {
		msg := c()
		if msg == nil {
			return
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, sub := range batch {
				q.launch(sub)
			}
			return
		}
		q.ch <- msg
	}()
}

func (q *msgQueue) next(t *testing.T, m *Model) {
	t.Helper()
	select {
	case msg := <-q.ch:
		nm, cmd := m.Update(msg)
		*m = *(nm.(*Model))
		q.launch(cmd)
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for a TUI message; view:\n%s", m.View())
	}
}

func TestTUIExecScreenShowsInstancesAndCommandBeforeRunning(t *testing.T) {
	primary := &stubFeature{desc: descriptor("status", true), body: "status body"}
	input := &fakeInputFeature{fakeLiveFeature: &fakeLiveFeature{
		desc: inputDescriptor("attach"),
		mode: core.LiveAttach,
	}, fields: []core.InputField{
		{Key: "instance", Label: "서비스 인스턴스", Kind: core.InputChoice, Required: true,
			Options: []core.InputOption{{Value: "web|0", Label: "web · proj-web-1"}, {Value: "web|1", Label: "web · proj-web-2"}}},
		{Key: "command", Label: "명령", Kind: core.InputText, Required: true},
	}}
	m := newTestModel(t, primary, input)
	initModel(t, m)

	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = nm.(*Model)
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(*Model)
	feed(t, m, execute(t, cmd))
	if m.form == nil {
		t.Fatalf("form was not opened; view:\n%s", m.View())
	}
	view := m.View()
	for _, want := range []string{"실행 전 확인", "서비스 인스턴스", "web · proj-web-1", "명령", "실행 내용"} {
		if !strings.Contains(view, want) {
			t.Fatalf("form missing %q:\n%s", want, view)
		}
	}
	if input.runs != 0 {
		t.Fatal("exec ran before the form was confirmed")
	}
}

func TestTUIFormCollectsCommandAndRunsThroughTheEngine(t *testing.T) {
	primary := &stubFeature{desc: descriptor("status", true), body: "status body"}
	input := &fakeInputFeature{fakeLiveFeature: &fakeLiveFeature{
		desc: inputDescriptor("attach"),
		mode: core.LiveAttach,
		run: func(ctx context.Context, in core.Invocation) (any, error) {
			fmt.Fprintln(in.IO.Stdout, "attach finished")
			return "done", nil
		},
	}, fields: []core.InputField{
		{Key: "instance", Label: "서비스 인스턴스", Kind: core.InputChoice, Required: true,
			Options: []core.InputOption{{Value: "web|1", Label: "web · proj-web-2"}}},
		{Key: "command", Label: "명령", Kind: core.InputText, Required: true},
	}}
	m := newTestModel(t, primary, input)
	initModel(t, m)
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = nm.(*Model)
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(*Model)
	feed(t, m, execute(t, cmd))
	if m.form == nil {
		t.Fatalf("form did not open:\n%s", m.View())
	}
	// enter moves from the choice to the command field.
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(*Model)
	for _, r := range "echo hi" {
		nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = nm.(*Model)
	}
	nm, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(*Model)
	feed(t, m, execute(t, cmd))

	input.mu.Lock()
	applied := input.applied
	input.mu.Unlock()
	if strings.Join(applied.Services, ",") != "web|1" || strings.Join(applied.Args, " ") != "echo hi" {
		t.Fatalf("collected request = %+v", applied)
	}
	if input.runs != 1 {
		t.Fatalf("runs = %d", input.runs)
	}
	input.fakeLiveFeature.mu.Lock()
	inv := input.gotInv
	input.fakeLiveFeature.mu.Unlock()
	if inv == nil || strings.Join(inv.Services, ",") != "web|1" {
		t.Fatalf("invocation = %+v", inv)
	}
}

func TestTUILogsScreenStreamsAndReturnsToTheManagementView(t *testing.T) {
	live := &fakeLiveFeature{
		desc: descriptor("logs", true),
		mode: core.LiveStream,
		run: func(ctx context.Context, in core.Invocation) (any, error) {
			fmt.Fprintln(in.IO.Stdout, "proj-web-1 | streamed line")
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	m := newTestModel(t, live)
	q := startCmds(m.Init())
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(m.View(), "streamed line") {
		if time.Now().After(deadline) {
			t.Fatalf("live screen never streamed the line; view:\n%s", m.View())
		}
		q.next(t, m)
	}
	if !strings.Contains(m.View(), "q/esc/ctrl+c") {
		t.Fatalf("live hints missing:\n%s", m.View())
	}

	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m = nm.(*Model)
	for m.phase != phaseReady {
		q.next(t, m)
	}
	if m.result == nil || m.result.Outcome != core.OutcomeCanceled {
		t.Fatalf("result = %+v", m.result)
	}
	view := m.View()
	for _, want := range []string{"서비스는 계속 실행 중", "작업", "마지막 실시간 출력", "streamed line"} {
		if !strings.Contains(view, want) {
			t.Fatalf("management view missing %q:\n%s", want, view)
		}
	}
	if !strings.Contains(view, "logs") {
		t.Fatalf("menu is not visible after the live screen:\n%s", view)
	}
}

func TestTUIAttachForwardsKeysAndDetachesWithCtrlBracket(t *testing.T) {
	attachedDesc := inputDescriptor("attach")
	attachedDesc.Primary = true
	attached := &fakeLiveFeature{
		desc: attachedDesc,
		mode: core.LiveAttach,
		run: func(ctx context.Context, in core.Invocation) (any, error) {
			buf := make([]byte, 64)
			n, _ := in.IO.Stdin.Read(buf)
			fmt.Fprintf(in.IO.Stdout, "child read %q\n", string(buf[:n]))
			return "done", nil
		},
	}
	m := newTestModel(t, attached)
	q := startCmds(m.Init())
	for m.live == nil {
		q.next(t, m)
	}
	if !m.req.IO.TTY || m.req.IO.Stdin == nil {
		t.Fatalf("attach did not wire terminal input: %+v", m.req.IO)
	}
	for _, r := range "hi" {
		nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = nm.(*Model)
	}
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	m = nm.(*Model)
	deadline := time.Now().Add(5 * time.Second)
	for m.phase != phaseReady {
		if time.Now().After(deadline) {
			t.Fatalf("attach did not return to the management view; view:\n%s", m.View())
		}
		q.next(t, m)
	}
	if m.result == nil || m.result.Outcome != core.OutcomeCanceled {
		t.Fatalf("result = %+v", m.result)
	}
	if !strings.Contains(m.View(), "ctrl+]") && !strings.Contains(m.View(), "계속 실행") {
		t.Fatalf("management view missing guidance:\n%s", m.View())
	}
}
