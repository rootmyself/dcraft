package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/ui/tui/visual"
)

// documentOnlyFeature implements ViewPresenter and no Presenter: the TUI must
// render its document rather than fall back to a string.
type documentOnlyFeature struct {
	desc core.Descriptor
	doc  core.Document
}

func (f *documentOnlyFeature) Descriptor() core.Descriptor { return f.desc }
func (f *documentOnlyFeature) Run(ctx context.Context, in core.Invocation) (any, error) {
	return "payload", nil
}
func (f *documentOnlyFeature) Document(res core.Result) core.Document { return f.doc }

// stringOnlyFeature implements Presenter and ViewPresenter's neighbour; the
// resolution order must prefer Document when both exist.
type bothFeature struct {
	desc        core.Descriptor
	doc         core.Document
	body        string
	presented   int
	documented  int
	resultValue any
}

func (f *bothFeature) Descriptor() core.Descriptor { return f.desc }
func (f *bothFeature) Run(ctx context.Context, in core.Invocation) (any, error) {
	return f.resultValue, nil
}
func (f *bothFeature) Present(res core.Result, width int) string {
	f.presented++
	return f.body
}
func (f *bothFeature) Document(res core.Result) core.Document {
	f.documented++
	return f.doc
}

func statusDoc() core.Document {
	return core.Document{Blocks: []core.Block{
		{Kind: core.BlockBadges, Badges: []core.Badge{{Kind: core.BadgeRunning, Text: "실행 중 2"}}},
		{Kind: core.BlockTable, Table: &core.Table{
			EmptyText: "서비스가 없습니다.",
			Columns: []core.Column{
				{Header: "서비스", Weight: 30, Min: 12, Max: 24},
				{Header: "상태", Fixed: 11},
			},
			Rows: []core.Row{
				{Selectable: true, Cells: []core.Cell{{Text: "api"}, {Text: "실행 중", Badge: core.BadgeRunning}}},
			},
		}},
		{Kind: core.BlockCallout, Callout: core.CalloutInfo, Text: "다음 행동"},
	}}
}

// The TUI asks ViewPresenter first. A feature that has both capabilities is
// never rendered through the string path.
func TestViewPresenterIsPreferredOverPresenter(t *testing.T) {
	f := &bothFeature{desc: descriptor("status", true), doc: statusDoc(), body: "STRING-BODY"}
	m := newTestModel(t, f)
	m.theme = *plainTheme()
	initModel(t, m)
	resize(t, m, 80, 24)

	view := m.View()
	if !strings.Contains(view, "다음 행동") || !strings.Contains(view, "api") {
		t.Fatalf("document body not rendered:\n%s", view)
	}
	if strings.Contains(view, "STRING-BODY") {
		t.Fatalf("string presenter won over the document:\n%s", view)
	}
	if f.documented == 0 {
		t.Fatal("Document was not called")
	}
}

// A feature with only Presenter keeps working: its string is the body.
func TestStringPresenterStillRenders(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "문자열 본문"}
	m := newTestModel(t, status)
	m.theme = *plainTheme()
	initModel(t, m)
	resize(t, m, 80, 24)
	if !strings.Contains(m.View(), "문자열 본문") {
		t.Fatalf("string body missing:\n%s", m.View())
	}
}

// A feature with neither capability gets the shared success fallback.
func TestFeatureWithoutPresenterUsesTheSharedFallback(t *testing.T) {
	bare := &bareFeature{desc: descriptor("status", true)}
	m := newTestModel(t, bare)
	m.theme = *plainTheme()
	initModel(t, m)
	resize(t, m, 80, 24)
	if !strings.Contains(m.View(), "작업을 완료했습니다") {
		t.Fatalf("fallback missing:\n%s", m.View())
	}
}

type bareFeature struct{ desc core.Descriptor }

func (f *bareFeature) Descriptor() core.Descriptor { return f.desc }
func (f *bareFeature) Run(ctx context.Context, in core.Invocation) (any, error) {
	return "payload", nil
}

// The resolution order is asserted directly too, because it is the contract
// three separate call sites depend on.
func TestPresenterResolutionOrder(t *testing.T) {
	both := &bothFeature{desc: descriptor("status", true), doc: statusDoc(), body: "STRING"}
	onlyString := &stubFeature{desc: descriptor("status", true), body: "STRING"}
	bare := &bareFeature{desc: descriptor("status", true)}

	cases := []struct {
		name string
		f    core.Feature
		want resolutionKind
	}{
		{"document", onlyDocFeature(), resolvedDocument},
		{"presenter", onlyString, resolvedString},
		{"fallback", bare, resolvedFallback},
	}
	for _, tc := range cases {
		m := newTestModel(t, tc.f)
		m.active = tc.f
		m.result = &core.Result{Outcome: core.OutcomeSuccess}
		if got := m.resultMode(); got != tc.want {
			t.Fatalf("%s: resolution = %v, want %v", tc.name, got, tc.want)
		}
	}
	_ = both
}

func onlyDocFeature() core.Feature {
	return &documentOnlyFeature{desc: descriptor("status", true), doc: statusDoc()}
}

// A 25-service table scrolls to the end with the header fixed. This is the
// acceptance criterion, driven through the model's real keys.
func TestStatusTableScrollsToTheLastServiceWithAFixedHeader(t *testing.T) {
	f := &documentOnlyFeature{desc: descriptor("status", true), doc: bigStackDocument(25)}
	m := newTestModel(t, f)
	m.theme = *plainTheme()
	initModel(t, m)
	resize(t, m, 120, 24)

	view := m.View()
	if !strings.Contains(view, "서비스") {
		t.Fatalf("no table header:\n%s", view)
	}
	if strings.Contains(view, "svc-25") {
		t.Fatalf("the last service is visible before scrolling:\n%s", view)
	}
	if !strings.Contains(view, "PgUp/PgDn") {
		t.Fatalf("no scroll hint:\n%s", view)
	}

	// G jumps to the end; the header must still be on screen.
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	m = nm.(*Model)
	view = m.View()
	if !strings.Contains(view, "svc-25") {
		t.Fatalf("G did not reach the last service:\n%s", view)
	}
	if !strings.Contains(view, "서비스") {
		t.Fatalf("header scrolled away:\n%s", view)
	}
	if !strings.Contains(view, "8-25/25") {
		t.Fatalf("counter does not describe the end window:\n%s", view)
	}

	// g returns to the top.
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	m = nm.(*Model)
	if !strings.Contains(m.View(), "svc-1") {
		t.Fatalf("g did not return to the first service:\n%s", m.View())
	}
	if strings.Contains(m.View(), "svc-25") {
		t.Fatalf("g left the last service visible:\n%s", m.View())
	}
}

func bigStackDocument(n int) core.Document {
	table := &core.Table{
		EmptyText: "서비스가 없습니다.",
		Columns: []core.Column{
			{Header: "서비스", Weight: 30, Min: 12, Max: 24},
			{Header: "상태", Fixed: 11},
		},
	}
	for i := 1; i <= n; i++ {
		table.Rows = append(table.Rows, core.Row{
			Selectable: true,
			Cells:      []core.Cell{{Text: "svc-" + itoa(i)}, {Text: "실행 중", Badge: core.BadgeRunning}},
		})
	}
	return core.Document{Blocks: []core.Block{{Kind: core.BlockTable, Table: table}}}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var digits []byte
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}

// PageUp/PageDown move by a window, g/G by an end, and the position indicator
// always describes the rows that are drawn.
func TestResultScrollKeysAndIndicator(t *testing.T) {
	f := &documentOnlyFeature{desc: descriptor("status", true), doc: bigStackDocument(25)}
	m := newTestModel(t, f)
	m.theme = *plainTheme()
	initModel(t, m)
	resize(t, m, 120, 24)

	lines := viewLines(t, m)
	// 24 rows of terminal − header − footer − title − blank − table header −
	// hint = 18 drawn service rows.
	if got := firstCounter(t, lines); got != "1-18/25" {
		t.Fatalf("initial counter = %q", got)
	}

	// PgDn moves a whole window and clamps at the end, so the last page
	// starts at 8: 25 rows with 18 visible leaves seven rows above.
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	m = nm.(*Model)
	if got := firstCounter(t, viewLines(t, m)); got != "8-25/25" {
		t.Fatalf("after PgDn counter = %q", got)
	}

	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m = nm.(*Model)
	if got := firstCounter(t, viewLines(t, m)); got != "1-18/25" {
		t.Fatalf("after PgUp counter = %q", got)
	}
}

// A screen with no overflow says nothing about scrolling.
func TestNoScrollHintWhenEverythingFits(t *testing.T) {
	f := &documentOnlyFeature{desc: descriptor("status", true), doc: bigStackDocument(2)}
	m := newTestModel(t, f)
	m.theme = *plainTheme()
	initModel(t, m)
	resize(t, m, 120, 36)
	view := m.View()
	if strings.Contains(view, "PgUp/PgDn") {
		t.Fatalf("scroll hint shown for a fitting body:\n%s", view)
	}
	for _, line := range viewLines(t, m) {
		if visual.Width(line) > 120 {
			t.Fatalf("row too wide: %q", line)
		}
	}
}

// A truncated value is reachable in full through the detail view, opened with a
// key the footer advertises.
func TestDetailViewShowsTheFullTruncatedValue(t *testing.T) {
	longImage := "registry.example.com/team/a-very-long-image-name-that-does-not-fit:2026-11-20"
	doc := core.Document{Blocks: []core.Block{{Kind: core.BlockTable, Table: &core.Table{
		Columns: []core.Column{
			{Header: "서비스", Weight: 30, Min: 12, Max: 24},
			{Header: "이미지", Weight: 45, Min: 16, Max: 34},
		},
		Rows: []core.Row{{Selectable: true, Cells: []core.Cell{
			{Text: "api"},
			{Text: longImage, Detail: longImage},
		}}},
	}}}}
	f := &documentOnlyFeature{desc: descriptor("status", true), doc: doc}
	m := newTestModel(t, f)
	m.theme = *plainTheme()
	initModel(t, m)
	resize(t, m, 60, 24)
	if !strings.Contains(m.View(), "…") {
		t.Fatalf("body did not truncate:\n%s", m.View())
	}
	if strings.Contains(m.View(), longImage) {
		t.Fatalf("the full value already fits; the test proves nothing:\n%s", m.View())
	}

	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	m = nm.(*Model)
	view := m.View()
	var joined strings.Builder
	for _, line := range viewLines(t, m) {
		joined.WriteString(strings.TrimSpace(line))
	}
	if !strings.Contains(joined.String(), longImage) {
		t.Fatalf("detail view lost the full value:\n%s", view)
	}
	for _, line := range viewLines(t, m) {
		if visual.Width(line) > 60 {
			t.Fatalf("detail row too wide: %q", line)
		}
	}

	// esc closes the detail view without quitting or running anything.
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = nm.(*Model)
	if cmd != nil && isQuit(t, cmd) {
		t.Fatal("esc in the detail view quit")
	}
	if m.detailOpen {
		t.Fatal("esc did not close the detail view")
	}
}

// The empty stack and a read failure are visibly different screens: one is an
// explained empty state, the other is the failure body with diagnostics.
func TestEmptyStackDiffersFromReadFailure(t *testing.T) {
	empty := &documentOnlyFeature{desc: descriptor("status", true), doc: core.Document{Blocks: []core.Block{
		{Kind: core.BlockTable, Table: &core.Table{
			EmptyText: "서비스가 없습니다. Compose 파일에 services 항목이 있는지 확인하세요.",
			Columns:   []core.Column{{Header: "서비스"}, {Header: "상태"}},
		}},
	}}}
	m := newTestModel(t, empty)
	m.theme = *plainTheme()
	initModel(t, m)
	resize(t, m, 80, 24)
	emptyView := m.View()
	if !strings.Contains(emptyView, "서비스가 없습니다") {
		t.Fatalf("empty stack not explained:\n%s", emptyView)
	}
	if strings.Contains(emptyView, "작업 실패") {
		t.Fatalf("empty stack rendered as a failure:\n%s", emptyView)
	}

	failing := &stubFeature{desc: descriptor("status", true), outcome: core.OutcomeFailure}
	m2 := newTestModel(t, failing)
	m2.theme = *plainTheme()
	initModel(t, m2)
	resize(t, m2, 80, 24)
	failView := m2.View()
	if !strings.Contains(failView, "작업 실패") {
		t.Fatalf("read failure not rendered as a failure:\n%s", failView)
	}
	if strings.Contains(failView, "서비스가 없습니다") {
		t.Fatalf("read failure claims the stack is empty:\n%s", failView)
	}
	if failView == emptyView {
		t.Fatal("empty stack and read failure render the same screen")
	}
}

// firstCounter extracts the `first-last/total` token from a frame.
func firstCounter(t *testing.T, lines []string) string {
	t.Helper()
	for _, line := range lines {
		for _, field := range strings.Fields(line) {
			if strings.Count(field, "-") == 1 && strings.Count(field, "/") == 1 {
				return field
			}
		}
	}
	t.Fatalf("no counter in frame:\n%s", strings.Join(lines, "\n"))
	return ""
}
