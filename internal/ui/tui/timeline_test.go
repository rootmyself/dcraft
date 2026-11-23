package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/ui/tui/visual"
)

func TestTimelineShowsCompleteLongStage(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "상태"}
	m := newTestModel(t, status)
	m.theme = *plainTheme()
	initModel(t, m)
	resize(t, m, 40, 30)
	title := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	lines := m.timelineView([]core.Stage{{Name: "long", Title: title, Status: core.StageStarted}}, nil)
	joined := strings.ReplaceAll(strings.Join(lines, ""), " ", "")
	if !strings.Contains(joined, title) {
		t.Fatalf("stage title lost: %q", lines)
	}
	for _, line := range lines {
		if visual.Width(line) > m.bodyWidth() {
			t.Fatalf("line exceeds body width: %q", line)
		}
	}
}

func TestFailureDiagnosticWrappingPreservesLines(t *testing.T) {
	long := strings.Repeat("abcdefghij", 9)
	lines := wrapDiagnosticLines("first\n"+long+"\nlast", 40)
	if lines[0] != "first" || lines[len(lines)-1] != "last" {
		t.Fatalf("explicit lines lost: %q", lines)
	}
	if !strings.HasPrefix(lines[2], "  ") || strings.ReplaceAll(strings.Join(lines[1:len(lines)-1], ""), " ", "") != long {
		t.Fatalf("diagnostic continuation lost: %q", lines)
	}
}

// The loading screen is a timeline: a completed stage, the stage that is
// running, and the stages that are still ahead each carry their own mark. The
// reader must be able to tell them apart without color.
func TestTimelineMarksCompletedCurrentAndPendingStages(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "상태 본문"}
	up := &stubFeature{desc: mutatingDescriptor("up"), body: "시작 본문"}
	m := newTestModel(t, status, up)
	m.theme = *plainTheme()
	initModel(t, m)
	resize(t, m, 80, 24)

	if cmd := m.begin(up); cmd == nil {
		t.Fatal("begin returned no command")
	}
	nm, _ := m.Update(stageMsg{stage: core.Stage{
		Name: "resolve_target", Title: "대상 확인", Status: core.StageSucceeded,
	}})
	m = nm.(*Model)
	nm, _ = m.Update(stageMsg{stage: core.Stage{
		Name: "preflight", Title: "환경 확인", Status: core.StageStarted,
	}})
	m = nm.(*Model)

	lines := viewLines(t, m)
	want := []string{
		"  " + m.theme.Glyphs.OK + " 대상 확인 (완료)",
		"  " + m.theme.Glyphs.SpinnerFrame(m.spinnerFrame) + " 환경 확인 (진행 중)",
		"  " + m.theme.Glyphs.Dot + " 작업 실행 (예정)",
		"  " + m.theme.Glyphs.Dot + " 이력 기록 (예정)",
	}
	for _, row := range want {
		if !containsLine(lines, row) {
			t.Fatalf("timeline missing %q:\n%s", row, strings.Join(lines, "\n"))
		}
	}
	if !strings.Contains(strings.Join(lines, "\n"), "작업을 실행하는 중") {
		t.Fatalf("working heading missing:\n%s", strings.Join(lines, "\n"))
	}
}

func containsLine(lines []string, want string) bool {
	for _, line := range lines {
		if strings.TrimRight(line, " ") == want {
			return true
		}
	}
	return false
}

// Every line of the working screen fits 40 columns, including a long Korean
// stage title and a wide terminal.
func TestTimelineFitsFortyColumns(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "상태 본문"}
	up := &stubFeature{desc: mutatingDescriptor("up"), body: "시작 본문"}
	m := newTestModel(t, status, up)
	m.theme = *plainTheme()
	initModel(t, m)
	resize(t, m, 40, 18)

	if cmd := m.begin(up); cmd == nil {
		t.Fatal("begin returned no command")
	}
	nm, _ := m.Update(stageMsg{stage: core.Stage{
		Name: "resolve_target", Status: core.StageSucceeded,
		Title: "대상 확인과 Compose 파일 목록 및 프로필 확인",
	}})
	m = nm.(*Model)
	nm, _ = m.Update(stageMsg{stage: core.Stage{
		Name: "preflight", Status: core.StageStarted, Title: "환경 확인",
	}})
	m = nm.(*Model)

	for i, line := range viewLines(t, m) {
		if got := visual.Width(line); got > 40 {
			t.Fatalf("row %d is %d cells, want <= 40: %q", i, got, line)
		}
	}
	// The same frame must hold at the other approved widths.
	for _, size := range [][2]int{{80, 24}, {120, 36}, {200, 50}} {
		resize(t, m, size[0], size[1])
		for i, line := range viewLines(t, m) {
			if got := visual.Width(line); got > size[0] {
				t.Fatalf("%dx%d row %d is %d cells: %q", size[0], size[1], i, got, line)
			}
		}
	}
}

// The spinner advances across ticks and the interval is injected, so the test
// drives the animation instead of waiting on a wall clock.
func TestSpinnerAdvancesAcrossTicks(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "상태 본문"}
	up := &stubFeature{desc: mutatingDescriptor("up"), body: "시작 본문"}
	m := newTestModel(t, status, up)
	m.theme = *plainTheme()
	m.spinnerInterval = time.Millisecond
	initModel(t, m)
	resize(t, m, 80, 24)

	if cmd := m.begin(up); cmd == nil {
		t.Fatal("begin returned no command")
	}
	if m.spinnerInterval != time.Millisecond {
		t.Fatalf("interval = %v, want the injected value", m.spinnerInterval)
	}
	nm, _ := m.Update(stageMsg{stage: core.Stage{
		Name: "preflight", Title: "환경 확인", Status: core.StageStarted,
	}})
	m = nm.(*Model)

	first := m.theme.Glyphs.SpinnerFrame(m.spinnerFrame)
	seen := map[string]bool{first: true}
	for i := 0; i < len(m.theme.Glyphs.Spin); i++ {
		nm, cmd := m.Update(spinnerTickMsg{})
		m = nm.(*Model)
		if cmd == nil {
			t.Fatal("a tick produced no follow-up frame")
		}
		seen[m.theme.Glyphs.SpinnerFrame(m.spinnerFrame)] = true
	}
	if len(seen) != len(m.theme.Glyphs.Spin) {
		t.Fatalf("spinner showed %d of %d frames", len(seen), len(m.theme.Glyphs.Spin))
	}
	// The drawn screen follows the animation: two consecutive frames cannot
	// render the same row, or the user would still see a frozen mark.
	before := m.View()
	nm, _ = m.Update(spinnerTickMsg{})
	m = nm.(*Model)
	if after := m.View(); after == before {
		t.Fatalf("a tick did not change the drawn frame:\n%s", after)
	}
}

// A result stops the animation, so a finished run leaves no timer behind and a
// test that only delivers messages can never hang on a tick.
func TestTimelineStopsTickingAfterAResult(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "상태 본문"}
	up := &stubFeature{desc: mutatingDescriptor("up"), body: "시작 본문"}
	m := newTestModel(t, status, up)
	m.theme = *plainTheme()
	m.spinnerInterval = time.Millisecond
	initModel(t, m)

	if cmd := m.begin(up); cmd == nil {
		t.Fatal("begin returned no command")
	}
	nm, cmd := m.Update(spinnerTickMsg{})
	m = nm.(*Model)
	if cmd == nil {
		t.Fatal("the running screen stopped ticking on its own")
	}
	nm, _ = m.Update(resultMsg{res: core.Result{
		SchemaVersion: core.SchemaVersion, Action: "up", Outcome: core.OutcomeSuccess,
		Stages: []core.Stage{}, Diagnostics: []core.Diagnostic{},
	}})
	m = nm.(*Model)
	nm, cmd = m.Update(spinnerTickMsg{})
	m = nm.(*Model)
	if cmd != nil {
		t.Fatal("a tick after the result scheduled another frame")
	}
	if m.spinnerCmd() != nil {
		t.Fatal("the finished screen offered a new animation")
	}
	if !strings.Contains(m.View(), "시작 본문") {
		t.Fatalf("result not on screen:\n%s", m.View())
	}
}

// A feature that publishes its own stages replaces the shared execute row
// instead of appearing next to it, and the still-running stage drives the
// spinner.
func TestFeatureStagesReplaceTheSharedPlaceholder(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "상태 본문"}
	up := &stubFeature{desc: mutatingDescriptor("up"), body: "시작 본문"}
	m := newTestModel(t, status, up)
	m.theme = *plainTheme()
	initModel(t, m)
	resize(t, m, 80, 30)

	if cmd := m.begin(up); cmd == nil {
		t.Fatal("begin returned no command")
	}
	for _, st := range []core.Stage{
		{Name: "resolve_target", Title: "대상 확인", Status: core.StageSucceeded},
		{Name: "preflight", Title: "환경 확인", Status: core.StageSucceeded},
		{Name: "compose_up", Title: "컨테이너 시작", Status: core.StageStarted},
	} {
		nm, _ := m.Update(stageMsg{stage: st})
		m = nm.(*Model)
	}
	view := m.View()
	if strings.Contains(view, "작업 실행") {
		t.Fatalf("shared placeholder still drawn beside the feature's own stage:\n%s", view)
	}
	if !strings.Contains(view, m.theme.Glyphs.SpinnerFrame(m.spinnerFrame)+" 컨테이너 시작 (진행 중)") {
		t.Fatalf("feature stage not drawn:\n%s", view)
	}
	if !strings.Contains(view, m.theme.Glyphs.Dot+" 이력 기록 (예정)") {
		t.Fatalf("later shared stage lost:\n%s", view)
	}
}

// A stage the plan did not know about is still shown: real progress must never
// be hidden just because the preview was narrower.
func TestUnplannedObservedStageIsStillShown(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "상태 본문"}
	m := newTestModel(t, status)
	m.theme = *plainTheme()
	initModel(t, m)
	resize(t, m, 80, 30)

	m.setPlan(core.PlannedStages(status))
	nm, _ := m.Update(stageMsg{stage: core.Stage{
		Name: "inspect_stack", Title: "스택 조회", Status: core.StageSucceeded,
	}})
	m = nm.(*Model)
	m.phase = phaseLoading
	view := m.View()
	if !strings.Contains(view, m.theme.Glyphs.OK+" 스택 조회 (완료)") {
		t.Fatalf("observed stage missing from the timeline:\n%s", view)
	}
}

// Rows follow the engine's order regardless of which of them came from the plan
// and which are still ahead: a completed stage is never listed below a pending
// one.
func TestTimelineKeepsEngineOrderAcrossPhases(t *testing.T) {
	observed := []core.Stage{
		{Name: "resolve_target", Title: "대상 확인", Status: core.StageSucceeded},
		{Name: "preflight", Title: "환경 확인", Status: core.StageSucceeded},
		{Name: "plan", Title: "작업 계획", Status: core.StageSucceeded},
	}
	plan := []core.Stage{
		{Name: "resolve_target", Title: "대상 확인"},
		{Name: "preflight", Title: "환경 확인"},
		{Name: "execute", Title: "작업 실행"},
	}
	rows := buildTimeline(observed, plan)
	got := make([]string, 0, len(rows))
	for _, row := range rows {
		got = append(got, row.stage.Name+":"+string(row.stage.Status))
	}
	want := "resolve_target:succeeded,preflight:succeeded,plan:succeeded,execute:"
	if strings.Join(got, ",") != want {
		t.Fatalf("rows = %q, want %q", strings.Join(got, ","), want)
	}
}

// A planned stage that later real progress overtook never ran; showing it as
// pending below a completed stage would be a lie.
func TestSkippedPlannedStageIsNotLeftPending(t *testing.T) {
	observed := []core.Stage{
		{Name: "resolve_target", Title: "대상 확인", Status: core.StageSucceeded},
		{Name: "preflight", Title: "환경 확인", Status: core.StageSucceeded},
		{Name: "execute", Title: "작업 실행", Status: core.StageSucceeded},
	}
	plan := []core.Stage{
		{Name: "resolve_target", Title: "대상 확인"},
		{Name: "preflight", Title: "환경 확인"},
		{Name: "hooks_pre", Title: "사전 스크립트"},
		{Name: "execute", Title: "작업 실행"},
		{Name: "history", Title: "이력 기록"},
	}
	rows := buildTimeline(observed, plan)
	for _, row := range rows {
		if row.stage.Name == "hooks_pre" {
			t.Fatalf("an overtaken stage stayed in the timeline: %+v", rows)
		}
	}
	got := make([]string, 0, len(rows))
	for _, row := range rows {
		got = append(got, row.stage.Name+":"+string(row.stage.Status))
	}
	want := "resolve_target:succeeded,preflight:succeeded,execute:succeeded,history:"
	if strings.Join(got, ",") != want {
		t.Fatalf("rows = %q, want %q", strings.Join(got, ","), want)
	}
}

// A failed plan never runs the operation, so it must not list the run's
// remaining stages as if they were still coming.
func TestFailedPlanDoesNotPromiseRemainingStages(t *testing.T) {
	install := &stubPlanner{
		stubFeature: &stubFeature{desc: approvalDescriptor("install-compose"), body: "설치 결과 본문"},
		plan: func(context.Context, core.Invocation) (core.Plan, error) {
			return core.Plan{}, core.Fail(core.Diagnostic{
				Category: core.CategoryEngineUnreachable, Severity: core.SeverityError,
				Message: "Docker Engine에 연결 불가.",
			})
		},
	}
	m := newTestModel(t, install)
	m.theme = *plainTheme()
	initModel(t, m)
	resize(t, m, 80, 30)

	view := m.View()
	if !strings.Contains(view, "작업 실패") {
		t.Fatalf("failed plan not reported:\n%s", view)
	}
	if strings.Contains(view, "작업 실행 (예정)") {
		t.Fatalf("failed plan promised a stage that will never run:\n%s", view)
	}
}

// A confirmed plan carries its timing into the run: the confirmation screen's
// pending rows become the run's rows and the animation resumes.
func TestConfirmedPlanCarriesTheTimelineIntoTheRun(t *testing.T) {
	install := &stubPlanner{
		stubFeature: &stubFeature{desc: approvalDescriptor("install-compose"), body: "설치 결과 본문"},
	}
	m := newTestModel(t, install)
	m.theme = *plainTheme()
	initModel(t, m)
	resize(t, m, 80, 30)

	// Approving is the plan screen's enter; the run command is not executed, so
	// the model stays on the loading screen.
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(*Model)
	if isQuit(t, cmd) {
		t.Fatal("approving quit instead of running")
	}
	if m.phase != phaseLoading {
		t.Fatalf("phase = %v, want loading", m.phase)
	}
	view := m.View()
	for _, want := range []string{
		m.theme.Glyphs.OK + " 작업 계획 (완료)",
		m.theme.Glyphs.Dot + " 작업 실행 (예정)",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("loading screen after approval missing %q:\n%s", want, view)
		}
	}
}

// A live screen renders process output, so it shows no timeline and never
// animates: the spinner belongs to work the user cannot otherwise see progress
// on, and a live process already prints its own.
func TestLiveScreenHasNoTimelineAnimation(t *testing.T) {
	logs := &fakeLiveFeature{desc: inputDescriptor("logs"), mode: core.LiveStream}
	primary := &stubFeature{desc: descriptor("status", true), body: "상태 본문"}
	m := newTestModel(t, primary, logs)
	m.theme = *plainTheme()
	m.spinnerInterval = time.Millisecond
	m.live = &liveState{mode: core.LiveStream}
	m.phase = phaseLoading
	m.setPlan(core.PlannedStages(logs))

	if m.spinnerCmd() != nil {
		t.Fatal("a live screen started the timeline animation")
	}
	nm, cmd := m.Update(spinnerTickMsg{})
	m = nm.(*Model)
	if cmd != nil {
		t.Fatal("a live screen scheduled another frame")
	}
	if view := m.View(); strings.Contains(view, "(예정)") {
		t.Fatalf("live screen drew the stage timeline:\n%s", view)
	}
}

// The busy screen keeps the safety contract: q states the risk and stays, and
// ctrl+c requests cancellation and then shows the canceled result with its
// partial-effect guidance on the same timeline.
func TestBusyTimelineKeepsQuitRiskAndCancelFlow(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "상태 본문"}
	up := &stubFeature{desc: mutatingDescriptor("up"), body: "시작 본문"}
	m := newTestModel(t, status, up)
	m.theme = *plainTheme()
	initModel(t, m)
	// Wide enough that the full Korean risk sentence is legible; wrapping it is
	// separate screen work and the notice wording is unchanged.
	resize(t, m, 200, 30)

	if cmd := m.begin(up); cmd == nil {
		t.Fatal("begin returned no command")
	}
	nm, _ := m.Update(stageMsg{stage: core.Stage{
		Name: "preflight", Title: "환경 확인", Status: core.StageStarted,
	}})
	m = nm.(*Model)

	// q must not quit and must state the partial-effect risk.
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m = nm.(*Model)
	if isQuit(t, cmd) {
		t.Fatal("q quit during a mutating run")
	}
	view := m.View()
	for _, want := range []string{"지금 화면을 닫으면 일부 변경이 남을 수 있음", "ctrl+c", "환경 확인", "[q] 종료 불가"} {
		if !strings.Contains(view, want) {
			t.Fatalf("busy screen missing %q:\n%s", want, view)
		}
	}

	// ctrl+c requests cancellation instead of quitting and keeps the timeline.
	nm, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = nm.(*Model)
	if isQuit(t, cmd) {
		t.Fatal("ctrl+c quit instead of requesting safe cancellation")
	}
	if m.runCtx == nil || m.runCtx.Err() != context.Canceled {
		t.Fatalf("run context was not canceled")
	}
	view = m.View()
	for _, want := range []string{"중단 요청됨", "일부 변경이 남을 수 있음", "환경 확인"} {
		if !strings.Contains(view, want) {
			t.Fatalf("canceling screen missing %q:\n%s", want, view)
		}
	}

	// The canceled result arrives on the same screen, with the partial-effect
	// guidance and the stage that was interrupted.
	nm, _ = m.Update(resultMsg{res: core.Result{
		SchemaVersion: core.SchemaVersion, Action: "up", Outcome: core.OutcomeCanceled,
		Stages: []core.Stage{
			{Name: "resolve_target", Title: "대상 확인", Status: core.StageSucceeded},
			{Name: "preflight", Title: "환경 확인", Status: core.StageCanceled},
		},
		Diagnostics: []core.Diagnostic{{
			Category: core.CategoryCanceled, Severity: core.SeverityWarning,
			Message: "작업이 중단되었음. 이미 완료된 단계의 효과는 남아 있을 수 있음.",
		}},
	}})
	m = nm.(*Model)
	view = m.View()
	for _, want := range []string{
		"작업 중단", "이미 완료된 단계의 효과는 남아 있을 수 있음",
		m.theme.Glyphs.Stopped + " 환경 확인 (중단)",
		m.theme.Glyphs.Dot + " 작업 실행 (예정)",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("canceled result missing %q:\n%s", want, view)
		}
	}
	nm, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m = nm.(*Model)
	if !isQuit(t, cmd) {
		t.Fatal("q did not quit after the canceled result")
	}
}

// A compact screen shows the current stage and one neighbour on each side; the
// omitted rows are stated, never silently dropped.
func TestCompactTimelineCollapsesToCurrentPlusOne(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "상태 본문"}
	up := &stubFeature{desc: mutatingDescriptor("up"), body: "시작 본문"}
	m := newTestModel(t, status, up)
	m.theme = *plainTheme()
	initModel(t, m)
	resize(t, m, 40, 18)

	if cmd := m.begin(up); cmd == nil {
		t.Fatal("begin returned no command")
	}
	// Four observed stages, then the third one is the current stage. With six
	// planned rows the compact window is the current row and its two
	// neighbours.
	for i, name := range []string{"resolve_target", "preflight"} {
		status := core.StageSucceeded
		if i == 1 {
			status = core.StageStarted
		}
		nm, _ := m.Update(stageMsg{stage: core.Stage{Name: name, Title: name, Status: status}})
		m = nm.(*Model)
	}
	view := m.View()
	lines := viewLines(t, m)
	if !containsLine(lines, "  "+m.theme.Glyphs.OK+" resolve_target (완료)") {
		t.Fatalf("previous stage missing from the compact window:\n%s", view)
	}
	if !containsLine(lines, "  "+m.theme.Glyphs.SpinnerFrame(m.spinnerFrame)+" preflight (진행 중)") {
		t.Fatalf("current stage missing from the compact window:\n%s", view)
	}
	if !containsLine(lines, "  "+m.theme.Glyphs.Dot+" 작업 실행 (예정)") {
		t.Fatalf("next stage missing from the compact window:\n%s", view)
	}
	if strings.Contains(view, "이력 기록") {
		t.Fatalf("compact window kept a distant stage:\n%s", view)
	}
	if !strings.Contains(view, "아래에 더 있음") {
		t.Fatalf("compact window hid rows silently:\n%s", view)
	}
	for _, line := range lines {
		if visual.Width(line) > 40 {
			t.Fatalf("compact row too wide: %q", line)
		}
	}
}

// The confirmation screen continues the same timeline shape: the plan's own
// stages are completed and the run's remaining stages stay marked as pending.
func TestPlanConfirmationShowsTheSameTimelineShape(t *testing.T) {
	install := &stubPlanner{
		stubFeature: &stubFeature{desc: approvalDescriptor("install-compose"), body: "설치 결과 본문"},
	}
	m := newTestModel(t, install)
	m.theme = *plainTheme()
	initModel(t, m)
	resize(t, m, 80, 30)

	view := m.View()
	for _, want := range []string{
		m.theme.Glyphs.OK + " 대상 확인 (완료)",
		m.theme.Glyphs.OK + " 환경 확인 (완료)",
		m.theme.Glyphs.OK + " 작업 계획 (완료)",
		m.theme.Glyphs.Dot + " 작업 실행 (예정)",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("plan screen missing %q:\n%s", want, view)
		}
	}
	if view := m.View(); strings.Contains(view, m.theme.Glyphs.SpinnerFrame(0)+" ") {
		t.Fatalf("plan screen is animating:\n%s", view)
	}
}
func TestFailureKeepsTheFailedStageDiagnosticsAndRemainingStages(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "상태 본문"}
	up := &stubFeature{desc: mutatingDescriptor("up"), body: "시작 본문"}
	m := newTestModel(t, status, up)
	m.theme = *plainTheme()
	initModel(t, m)
	resize(t, m, 80, 30)

	if cmd := m.begin(up); cmd == nil {
		t.Fatal("begin returned no command")
	}
	nm, _ := m.Update(stageMsg{stage: core.Stage{
		Name: "resolve_target", Title: "대상 확인", Status: core.StageSucceeded,
	}})
	m = nm.(*Model)
	nm, _ = m.Update(stageMsg{stage: core.Stage{
		Name: "preflight", Title: "환경 확인", Status: core.StageFailed,
	}})
	m = nm.(*Model)
	nm, _ = m.Update(resultMsg{res: core.Result{
		SchemaVersion: core.SchemaVersion,
		Action:        "up",
		Outcome:       core.OutcomeFailure,
		Stages: []core.Stage{
			{Name: "resolve_target", Title: "대상 확인", Status: core.StageSucceeded},
			{Name: "preflight", Title: "환경 확인", Status: core.StageFailed},
		},
		Diagnostics: []core.Diagnostic{{
			Category: core.CategoryEngineUnreachable, Severity: core.SeverityError,
			Message:     "Docker Engine에 연결 불가.",
			Detail:      "Cannot connect to the Docker daemon",
			NextActions: []string{"Docker Desktop을 실행."},
		}},
	}})
	m = nm.(*Model)

	view := m.View()
	for _, want := range []string{
		"작업 실패",
		"Docker Engine에 연결 불가",
		"Cannot connect to the Docker daemon",
		"Docker Desktop을 실행",
		m.theme.Glyphs.OK + " 대상 확인 (완료)",
		m.theme.Glyphs.Danger + " 환경 확인 (실패)",
		m.theme.Glyphs.Dot + " 작업 실행 (예정)",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("failure screen missing %q:\n%s", want, view)
		}
	}
}
