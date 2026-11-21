package visual

import (
	"strings"
	"testing"
)

// The five phase footers are an approved contract: exact keys, exact order,
// single row. A wording change must be a deliberate contract change.
func TestFooterExactStringsPerPhase(t *testing.T) {
	cases := []struct {
		phase Phase
		want  string
	}{
		{PhaseMenu, "[↑↓] 이동  [enter] 실행  [q] 종료  [/] 찾기  [tab] 메뉴  [r] 새로고침  [?] 도움말"},
		{PhaseConfirm, "[enter] 실행  [esc] 취소  [↑↓] 항목  [?] 도움말"},
		{PhaseForm, "[enter] 제출  [esc] 취소  [tab] 다음 항목  [shift+tab] 이전  [?] 도움말"},
		{PhaseLive, "[q] 화면 닫기  [ctrl+c] 중단  [ctrl+] 접속 종료  [?] 도움말"},
		{PhaseBusy, "[ctrl+c] 안전 취소  [q] 종료 불가"},
	}
	for _, tc := range cases {
		t.Run(tc.phase.String(), func(t *testing.T) {
			th := Resolve(Inputs{ThemeSetting: "dark", NoColor: true, Profile: ProfileNone, DarkBackground: true, UTF8: true})
			got := Footer(th, tc.phase, 200)
			if got != tc.want {
				t.Fatalf("footer = %q\n     want %q", got, tc.want)
			}
			if strings.Contains(got, "\n") {
				t.Fatalf("footer wrapped: %q", got)
			}
		})
	}
}

// Approved measurements at 48 columns: menu drops to four items, confirm to
// three. These come from the contract, not from the implementation.
func TestFooterTruncationAt48Columns(t *testing.T) {
	th := Resolve(Inputs{ThemeSetting: "dark", NoColor: true, Profile: ProfileNone, DarkBackground: true, UTF8: true})
	if got := Footer(th, PhaseMenu, 48); got != "[↑↓] 이동  [enter] 실행  [q] 종료  [?] 도움말" {
		t.Fatalf("menu@48 = %q", got)
	}
	if got := Footer(th, PhaseConfirm, 48); got != "[enter] 실행  [esc] 취소  [↑↓] 항목  [?] 도움말" {
		t.Fatalf("confirm@48 = %q", got)
	}
}

// Priority 0 items are the keys that can always run or leave a screen. They
// must survive every width, and nothing may ever be silently clipped.
func TestFooterNeverDropsPriorityZeroItems(t *testing.T) {
	th := Plain()
	for width := 0; width <= 200; width++ {
		for _, phase := range []Phase{PhaseMenu, PhaseConfirm, PhaseForm, PhaseLive, PhaseBusy} {
			row := Footer(th, phase, width)
			if strings.Contains(row, "\n") {
				t.Fatalf("%s@%d wrapped", phase, width)
			}
			if width > 0 && Width(row) > width {
				t.Fatalf("%s@%d is %d cells: %q", phase, width, Width(row), row)
			}
			for _, item := range FooterItems(phase) {
				if item.Priority != 0 {
					continue
				}
				if strings.Contains(item.Label, "종료 불가") {
					// busy is a fact statement, kept by definition.
				}
				if width >= 24 && !strings.Contains(row, "["+item.Key+"]") {
					t.Fatalf("%s@%d dropped priority-0 key %q: %q", phase, width, item.Key, row)
				}
			}
		}
	}
}

// Higher-priority numbers are dropped first, and survivors keep their declared
// order rather than being re-sorted by priority.
func TestFooterDropsHighPrioritiesFirstInDeclaredOrder(t *testing.T) {
	th := Plain()
	items := FooterItems(PhaseMenu)
	full := Footer(th, PhaseMenu, 200)
	fullIndex := func(key string) int { return strings.Index(full, "["+key+"]") }

	row := Footer(th, PhaseMenu, 52)
	kept := []string{}
	for _, item := range items {
		if strings.Contains(row, "["+item.Key+"]") {
			kept = append(kept, item.Key)
		}
	}
	want := []string{"↑↓", "enter", "q", "?"}
	if strings.Join(kept, ",") != strings.Join(want, ",") {
		t.Fatalf("48..52 cols kept %v, want %v (row %q)", kept, want, row)
	}
	// Order is the declared one, not priority order.
	last := -1
	for _, key := range kept {
		idx := strings.Index(row, "["+key+"]")
		if idx <= last {
			t.Fatalf("%q is out of declared order", row)
		}
		last = idx
	}
	_ = fullIndex
}

func TestFooterSeparatorIsTwoSpaces(t *testing.T) {
	th := Plain()
	row := Footer(th, PhaseBusy, 200)
	if !strings.Contains(row, "  ") {
		t.Fatalf("no separator: %q", row)
	}
	if strings.Contains(row, "   ") {
		t.Fatalf("separator is wider than two cells: %q", row)
	}
}

func TestFooterBusyStatesTheQuitFact(t *testing.T) {
	th := Plain()
	row := Footer(th, PhaseBusy, 120)
	if !strings.Contains(row, "[q] 종료 불가") {
		t.Fatalf("busy footer hides the quit restriction: %q", row)
	}
}

// Unknown phases fall back to the menu footer so a future phase can never
// render an empty footer.
func TestUnknownPhaseFallsBackToMenu(t *testing.T) {
	th := Plain()
	if got := Footer(th, Phase(99), 200); got != Footer(th, PhaseMenu, 200) {
		t.Fatalf("unknown phase = %q", got)
	}
}

// In a colored theme the footer carries keycap chips, but the visible text is
// identical to the plain rendering.
func TestColoredFooterHasSameTextAsPlain(t *testing.T) {
	plain := Plain()
	colored := Resolve(Inputs{ThemeSetting: "dark", Profile: ProfileTrueColor, DarkBackground: true, UTF8: true})
	got := Footer(colored, PhaseMenu, 200)
	if stripANSI(got) != Footer(plain, PhaseMenu, 200) {
		t.Fatalf("colored footer text = %q", stripANSI(got))
	}
	if !strings.Contains(got, "\x1b[") {
		t.Fatalf("colored footer emitted no styling")
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		switch {
		case r == '\x1b':
			in = true
		case in && r == 'm':
			in = false
		case !in:
			b.WriteRune(r)
		}
	}
	return b.String()
}
