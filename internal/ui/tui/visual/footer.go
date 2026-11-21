package visual

import "strings"

// Phase is the interaction phase a screen is in. The footer changes with it so
// the keys on screen are never ambiguous.
type Phase int

const (
	// PhaseMenu is the idle feature menu.
	PhaseMenu Phase = iota
	// PhaseConfirm is a confirmation/approval screen.
	PhaseConfirm
	// PhaseForm is an input form.
	PhaseForm
	// PhaseLive is an attached or streaming screen.
	PhaseLive
	// PhaseBusy is an in-flight operation.
	PhaseBusy
)

// String is the phase name used in tests and reports.
func (p Phase) String() string {
	switch p {
	case PhaseConfirm:
		return "confirm"
	case PhaseForm:
		return "form"
	case PhaseLive:
		return "live"
	case PhaseBusy:
		return "busy"
	default:
		return "menu"
	}
}

// Keycap is one footer entry. Priority controls what is dropped when the row
// does not fit: 0 is never dropped, higher numbers go first.
type Keycap struct {
	Key      string
	Label    string
	Priority int
}

// Text is the plain chip+label form the footer renders. A key that already ends
// with "]" (ctrl+]) is not given a second closing bracket; the approved footer
// reads [ctrl+] 접속 종료.
func (k Keycap) Text() string { return chip(k.Key) + " " + k.Label }

func chip(key string) string {
	if strings.HasSuffix(key, "]") {
		return "[" + key
	}
	return "[" + key + "]"
}

// footerSeparator joins keycaps; the contract fixes it at two cells.
const footerSeparator = "  "

// FooterItems returns the approved keycaps for a phase, in declared order.
func FooterItems(p Phase) []Keycap {
	switch p {
	case PhaseConfirm:
		// Approving is a single key. The footer advertises exactly the keys
		// that act on this screen, so a second accept key is not listed.
		return []Keycap{
			{Key: "enter", Label: "실행", Priority: 0},
			{Key: "esc", Label: "취소", Priority: 0},
			{Key: "↑↓", Label: "항목", Priority: 3},
			{Key: "?", Label: "도움말", Priority: 2},
		}
	case PhaseForm:
		return []Keycap{
			{Key: "enter", Label: "제출", Priority: 0},
			{Key: "esc", Label: "취소", Priority: 0},
			{Key: "tab", Label: "다음 항목", Priority: 2},
			{Key: "shift+tab", Label: "이전", Priority: 3},
			{Key: "?", Label: "도움말", Priority: 3},
		}
	case PhaseLive:
		return []Keycap{
			{Key: "q", Label: "화면 닫기", Priority: 0},
			{Key: "ctrl+c", Label: "중단", Priority: 1},
			{Key: "ctrl+]", Label: "접속 종료", Priority: 2},
			{Key: "?", Label: "도움말", Priority: 3},
		}
	case PhaseBusy:
		return []Keycap{
			{Key: "ctrl+c", Label: "안전 취소", Priority: 0},
			{Key: "q", Label: "종료 불가", Priority: 1},
		}
	default: // PhaseMenu, and any future phase until it declares its own row.
		return []Keycap{
			{Key: "↑↓", Label: "이동", Priority: 1},
			{Key: "enter", Label: "실행", Priority: 0},
			{Key: "q", Label: "종료", Priority: 0},
			{Key: "/", Label: "찾기", Priority: 3},
			{Key: "tab", Label: "메뉴", Priority: 3},
			{Key: "r", Label: "새로고침", Priority: 3},
			{Key: "?", Label: "도움말", Priority: 2},
		}
	}
}

// Footer renders the approved keycap row for a phase, exactly one row, never
// wrapped. Items are dropped by priority until the row fits; priority 0 is
// kept. If even the priority-0 row cannot fit, the row is truncated with an
// explicit ellipsis rather than silently clipped.
func Footer(th Theme, p Phase, width int) string {
	return Row(th, FooterItems(p), width)
}

// Row renders an explicit keycap row with the same priority rules. Screens with
// keys the approved five phases do not cover (a service toggle list) supply
// their own row here; the footer stays the only keycap surface.
func Row(th Theme, items []Keycap, width int) string {
	keep := make([]bool, len(items))
	for i := range keep {
		keep[i] = true
	}
	render := func() string { return renderKeycaps(th, items, keep) }
	if width <= 0 {
		// Before the first WindowSizeMsg there is no width to fit yet; the row
		// is still returned so the keys are never missing from a screen.
		return render()
	}
	for priority := 3; priority >= 1; priority-- {
		if Width(render()) <= width {
			break
		}
		for i, item := range items {
			if item.Priority == priority {
				keep[i] = false
			}
		}
	}
	row := render()
	if Width(row) <= width {
		return row
	}
	// Narrower than the mandatory keys: state the cut instead of clipping a
	// chip mid-glyph.
	return Truncate(row, width, th.Glyphs.Ell)
}

func renderKeycaps(th Theme, items []Keycap, keep []bool) string {
	var b strings.Builder
	first := true
	for i, item := range items {
		if !keep[i] {
			continue
		}
		if !first {
			b.WriteString(footerSeparator)
		}
		first = false
		if th.Color {
			b.WriteString(th.BoldOn(TokenMuted, TokenSurface, chip(item.Key)))
			b.WriteString(th.Paint(TokenMuted, " "+item.Label))
			continue
		}
		b.WriteString(item.Text())
	}
	return b.String()
}

// FooterText is the plain, uncolored footer for a phase. It is what tests and
// colorless snapshots compare against.
func FooterText(p Phase, width int) string {
	return Footer(Plain(), p, width)
}
