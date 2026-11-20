package visual

// Breakpoint boundaries from the approved contract: width and height only.
const (
	// WideMinCols is the smallest width that shows the two-pane layout.
	WideMinCols = 100
	// MediumMinCols is the smallest width of the single-column layout.
	MediumMinCols = 60
	// CompactMinRows is the smallest height of a non-compact layout.
	CompactMinRows = 18

	sidebarMin = 26
	sidebarMax = 30
)

// Mode is the layout chosen from the terminal size.
type Mode int

const (
	// ModeCompact drops borders, descriptions and secondary columns. Badges
	// and the essential values stay.
	ModeCompact Mode = iota
	// ModeMedium is a single column with a one-row navigation summary.
	ModeMedium
	// ModeWide is the sidebar + content two-pane layout.
	ModeWide
)

// String is the mode name used in tests and reports.
func (m Mode) String() string {
	switch m {
	case ModeWide:
		return "wide"
	case ModeMedium:
		return "medium"
	default:
		return "compact"
	}
}

// Decorated reports whether the mode keeps borders, rules and descriptions.
// Compact is the only mode that drops them.
func (m Mode) Decorated() bool { return m != ModeCompact }

// PickMode chooses the layout from width and height alone. Content never
// changes the mode: a resize must reflow, not renegotiate.
func PickMode(width, height int) Mode {
	if width < MediumMinCols || height < CompactMinRows {
		return ModeCompact
	}
	if width < WideMinCols {
		return ModeMedium
	}
	return ModeWide
}

// SidebarWidth is clamp(width*22/100, 26, 30). It is the only place the
// sidebar budget lives, so the layout and every renderer agree.
func SidebarWidth(width int) int {
	return clamp(width*22/100, sidebarMin, sidebarMax)
}

// ContentWidth is the cells available to the body in the given mode. Wide
// mode subtracts the sidebar and the approved one-cell pane gap.
func ContentWidth(width int, mode Mode) int {
	if width <= 0 {
		return 0
	}
	if mode != ModeWide {
		return width
	}
	content := width - SidebarWidth(width) - 1
	if content < 1 {
		return 1
	}
	return content
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
