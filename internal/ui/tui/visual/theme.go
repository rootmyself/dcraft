// Package visual is the shared visual system of the interactive screen: the
// resolved theme, display-cell width helpers, breakpoint geometry, the
// header/body/keycap shell and the glyph set.
//
// Renderers receive a resolved Theme and never read the environment
// themselves. Resolve and Detect are pure functions over explicit inputs so
// every environment combination is testable without a terminal.
//
// The navigation renderers (Sidebar, NavRow, MenuLines) take a Nav they never
// derive: the TUI builds it from descriptors, so no feature name exists here.
package visual

import (
	"math"
	"strconv"
	"strings"
)

// Profile is the terminal color capability that decides whether the resolved
// theme may emit color. Non-TTY terminals degrade to no color.
type Profile int

const (
	// ProfileNone is a non-TTY or otherwise colorless output.
	ProfileNone Profile = iota
	// Profile16 is a 16-color terminal.
	Profile16
	// Profile256 is a 256-color terminal.
	Profile256
	// ProfileTrueColor is a 24-bit color terminal.
	ProfileTrueColor
)

// Inputs is everything theme resolution depends on. The caller gathers it
// once; no renderer reads any of it later.
type Inputs struct {
	// ThemeSetting is DCRAFT_THEME: auto, light, dark, or empty.
	ThemeSetting string
	// NoColor is true when NO_COLOR is set to a non-empty value.
	NoColor bool
	// Term is TERM. "dumb" disables color.
	Term string
	// Profile is the terminal color capability.
	Profile Profile
	// DarkBackground is the detected (or caller-supplied) background brightness.
	DarkBackground bool
	// UTF8 reports a UTF-8 locale. False switches every glyph to ASCII.
	UTF8 bool
}

// Token names a semantic color.
// Background and foreground are the terminal's own colors, not tokens.
type Token int

const (
	// TokenAccent marks selection, focus and the current position.
	TokenAccent Token = iota
	// TokenSuccess marks running and healthy states.
	TokenSuccess
	// TokenWarning marks in-progress, restarting and attention states.
	TokenWarning
	// TokenDanger marks failed, unhealthy and irreversible actions.
	TokenDanger
	// TokenMuted marks secondary information, labels and borders.
	TokenMuted
	// TokenSurface is the only token painted as a background plate.
	TokenSurface
	TokenInfo
	TokenCode
	TokenName
	TokenURL
	TokenCommand
	TokenProject
)

// String is the token name used in tests and reports.
func (t Token) String() string {
	switch t {
	case TokenAccent:
		return "accent"
	case TokenSuccess:
		return "success"
	case TokenWarning:
		return "warning"
	case TokenDanger:
		return "danger"
	case TokenMuted:
		return "muted"
	case TokenSurface:
		return "surface"
	case TokenInfo:
		return "info"
	case TokenCode:
		return "code"
	case TokenName:
		return "name"
	case TokenURL:
		return "url"
	case TokenCommand:
		return "command"
	case TokenProject:
		return "project"
	default:
		return "unknown"
	}
}

// Palette is one resolved theme. BG and FG are the terminal's own background
// and foreground; the tokens carry meaning only.
type Palette struct {
	Name                  string
	Dark                  bool
	BG                    string
	FG                    string
	Accent                string
	Success               string
	Warning               string
	Danger                string
	Muted                 string
	Surface               string
	Info, Code, NameColor string
	URL, Command, Project string
}

// Color resolves a semantic token to its approved #rrggbb value.
func (p Palette) Color(t Token) (string, bool) {
	switch t {
	case TokenAccent:
		return p.Accent, true
	case TokenSuccess:
		return p.Success, true
	case TokenWarning:
		return p.Warning, true
	case TokenDanger:
		return p.Danger, true
	case TokenMuted:
		return p.Muted, true
	case TokenSurface:
		return p.Surface, true
	case TokenInfo:
		return p.Info, true
	case TokenCode:
		return p.Code, true
	case TokenName:
		return p.NameColor, true
	case TokenURL:
		return p.URL, true
	case TokenCommand:
		return p.Command, true
	case TokenProject:
		return p.Project, true
	default:
		return "", false
	}
}

// Glyphs is the UTF-8 / ASCII glyph set from the approved visual contract.
// Every ASCII replacement is exactly one cell wide, so column math never
// changes between modes.
type Glyphs struct {
	ASCII bool

	TL, TR, BL, BR string
	H, V           string
	Sel            string
	Dot            string
	Ell            string
	OK             string
	Warn           string
	Stopped        string
	None           string
	Danger         string
	Thumb          string
	Spin           [4]string
}

// UTF8Glyphs is the default glyph set.
func UTF8Glyphs() Glyphs {
	return Glyphs{
		TL: "╭", TR: "╮", BL: "╰", BR: "╯",
		H: "─", V: "│",
		Sel:     "▸",
		Dot:     "·",
		Ell:     "…",
		OK:      "✓",
		Warn:    "◐",
		Stopped: "■",
		None:    "○",
		Danger:  "▲",
		Thumb:   "█",
		Spin:    [4]string{"◐", "◓", "◑", "◒"},
	}
}

// ASCIIGlyphs is the fallback for non-UTF-8 locales. Korean labels stay; only
// the glyphs change.
func ASCIIGlyphs() Glyphs {
	return Glyphs{
		ASCII: true,
		TL:    "+", TR: "+", BL: "+", BR: "+",
		H: "-", V: "|",
		Sel:     ">",
		Dot:     "-",
		Ell:     "~",
		OK:      "+",
		Warn:    "!",
		Stopped: "x",
		None:    "o",
		Danger:  "!",
		Thumb:   "#",
		Spin:    [4]string{"/", "-", "\\", "|"},
	}
}

// SpinnerFrame returns one animation frame; the index wraps in both
// directions so a decrementing counter never indexes out of range.
func (g Glyphs) SpinnerFrame(i int) string {
	n := len(g.Spin)
	return g.Spin[((i%n)+n)%n]
}

// Theme is the resolved visual theme injected into every renderer.
type Theme struct {
	Palette Palette
	// Color is false when the terminal must receive plain text: NO_COLOR,
	// non-TTY, or TERM=dumb.
	Color   bool
	Glyphs  Glyphs
	Profile Profile
}

// Resolved reports whether this theme came from Resolve. The zero Theme means
// "not injected"; the TUI resolves one at construction.
func (t Theme) Resolved() bool { return t.Palette.Name != "" }

// Ellipsis is the explicit truncation marker for this theme.
func (t Theme) Ellipsis() string { return t.Glyphs.Ell }

// DarkPalette targets a dark terminal background. All six tokens clear 4.5:1
// contrast on both BG and Surface.
func DarkPalette() Palette {
	return Palette{
		Name: "dark", Dark: true, BG: "#10131a", FG: "#d8dee9",
		Accent: "#7aa2f7", Success: "#5fd08a", Warning: "#e5b567",
		Danger: "#f07178", Muted: "#9aa5b8", Surface: "#1b2130",
		Info: "#5fd7ff", URL: "#5f5fff", Code: "#ffff55", Command: "#ff5555", NameColor: "#55ff77", Project: "#ff55ff",
	}
}

// LightPalette targets a light terminal background. All six tokens clear
// 4.5:1 contrast on both BG and Surface.
func LightPalette() Palette {
	return Palette{
		Name: "light", Dark: false, BG: "#ffffff", FG: "#1f2430",
		Accent: "#2f5bd1", Success: "#1b7a43", Warning: "#8a5a00",
		Danger: "#b4232f", Muted: "#5b6472", Surface: "#f1f4f9",
		Info: "#005f87", URL: "#003faf", Code: "#875f00", Command: "#af0000", NameColor: "#008700", Project: "#8700af",
	}
}

// Resolve turns explicit inputs into the palette, color decision and glyph set.
// It is pure: no environment is read and no global state is touched.
func Resolve(in Inputs) Theme {
	dark := in.DarkBackground
	switch strings.ToLower(strings.TrimSpace(in.ThemeSetting)) {
	case "light":
		dark = false
	case "dark":
		dark = true
	case "", "auto":
		// Follow the detected background.
	default:
		// An unrecognised setting follows detection instead of failing the
		// screen; DCRAFT_THEME is a convenience, never a requirement.
	}
	pal := LightPalette()
	if dark {
		pal = DarkPalette()
	}
	color := !in.NoColor && !strings.EqualFold(strings.TrimSpace(in.Term), "dumb") &&
		(in.Profile == Profile16 || in.Profile == Profile256 || in.Profile == ProfileTrueColor)
	glyphs := UTF8Glyphs()
	if !in.UTF8 {
		glyphs = ASCIIGlyphs()
	}
	return Theme{Palette: pal, Color: color, Glyphs: glyphs, Profile: in.Profile}
}

// Plain returns a resolved theme that never emits escape sequences. Tests and
// colorless snapshots use it.
func Plain() Theme {
	return Resolve(Inputs{ThemeSetting: "dark", NoColor: true, Profile: ProfileNone, DarkBackground: true, UTF8: true})
}

// Paint wraps s in the token's foreground color. A colorless theme returns s
// unchanged, so no escape sequence can leak into plain output.
func (t Theme) Paint(tok Token, s string) string {
	if !t.Color || s == "" {
		return s
	}
	hex, ok := t.Palette.Color(tok)
	if !ok {
		return s
	}
	return t.sgr(hex, "", false) + s + "\x1b[0m"
}

// Bold is Paint with the bold attribute.
func (t Theme) Bold(tok Token, s string) string {
	if !t.Color || s == "" {
		return s
	}
	hex, ok := t.Palette.Color(tok)
	if !ok {
		return s
	}
	return t.sgr(hex, "", true) + s + "\x1b[0m"
}

// PaintOn wraps s in a foreground token over a background token.
func (t Theme) PaintOn(fg, bg Token, s string) string {
	return t.style(fg, bg, false, s)
}

// BoldOn is PaintOn with the bold attribute; used for keycap chips and header
// identity on the surface plate.
func (t Theme) BoldOn(fg, bg Token, s string) string {
	return t.style(fg, bg, true, s)
}

func (t Theme) style(fg, bg Token, bold bool, s string) string {
	if !t.Color || s == "" {
		return s
	}
	fgHex, ok := t.Palette.Color(fg)
	if !ok {
		return s
	}
	bgHex := ""
	if bg == TokenSurface {
		bgHex = t.Palette.Surface
	} else if b, ok := t.Palette.Color(bg); ok {
		bgHex = b
	}
	return t.sgr(fgHex, bgHex, bold) + s + "\x1b[0m"
}

func (t Theme) sgr(fg, bg string, bold bool) string {
	if t.Profile != Profile16 {
		return sgr(fg, bg, bold)
	}
	codes := []string{"0"}
	if bold {
		codes = append(codes, "1")
	}
	if fg != "" {
		codes = append(codes, strconv.Itoa(ansi16(fg, false)))
	}
	if bg != "" {
		codes = append(codes, strconv.Itoa(ansi16(bg, true)))
	}
	return "\x1b[" + strings.Join(codes, ";") + "m"
}

func ansi16(hex string, background bool) int {
	colors := [16][3]int{{0, 0, 0}, {128, 0, 0}, {0, 128, 0}, {128, 128, 0}, {0, 0, 128}, {128, 0, 128}, {0, 128, 128}, {192, 192, 192}, {128, 128, 128}, {255, 0, 0}, {0, 255, 0}, {255, 255, 0}, {0, 0, 255}, {255, 0, 255}, {0, 255, 255}, {255, 255, 255}}
	r, g, b := HexRGB(hex)
	best, distance := 0, math.MaxInt
	for i, c := range colors {
		d := (r-c[0])*(r-c[0]) + (g-c[1])*(g-c[1]) + (b-c[2])*(b-c[2])
		if d < distance {
			best, distance = i, d
		}
	}
	base := 30
	if background {
		base = 40
	}
	if best >= 8 {
		base += 60
		best -= 8
	}
	return base + best
}

// sgr builds a 24-bit SGR sequence.
func sgr(fgHex, bgHex string, bold bool) string {
	codes := []string{"0"}
	if bold {
		codes = append(codes, "1")
	}
	if fgHex != "" {
		r, g, b := HexRGB(fgHex)
		codes = append(codes, "38;2", strconv.Itoa(r), strconv.Itoa(g), strconv.Itoa(b))
	}
	if bgHex != "" {
		r, g, b := HexRGB(bgHex)
		codes = append(codes, "48;2", strconv.Itoa(r), strconv.Itoa(g), strconv.Itoa(b))
	}
	return "\x1b[" + strings.Join(codes, ";") + "m"
}

// HexRGB parses "#rrggbb". Invalid input yields black rather than a panic: a
// palette value is compiled in, never user input.
func HexRGB(hex string) (int, int, int) {
	if len(hex) != 7 || hex[0] != '#' {
		return 0, 0, 0
	}
	r, errR := strconv.ParseUint(hex[1:3], 16, 8)
	g, errG := strconv.ParseUint(hex[3:5], 16, 8)
	b, errB := strconv.ParseUint(hex[5:7], 16, 8)
	if errR != nil || errG != nil || errB != nil {
		return 0, 0, 0
	}
	return int(r), int(g), int(b)
}

// Contrast returns the WCAG 2.x contrast ratio between two #rrggbb colors.
func Contrast(a, b string) float64 {
	la, lb := relativeLuminance(a), relativeLuminance(b)
	hi, lo := math.Max(la, lb), math.Min(la, lb)
	return (hi + 0.05) / (lo + 0.05)
}

func relativeLuminance(hex string) float64 {
	r, g, b := HexRGB(hex)
	channel := func(v int) float64 {
		c := float64(v) / 255
		if c <= 0.03928 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*channel(r) + 0.7152*channel(g) + 0.0722*channel(b)
}

// Detect builds Inputs from the process environment and the terminal facts the
// caller already knows. getenv is injected so this stays testable and nothing
// here reads os.Getenv directly; darkBackground is the terminal's own answer
// (or the documented fallback), not something this package re-derives.
//
// No terminal query happens here: startup never blocks on an OSC response, and
// a terminal that misreports its background is fixed with DCRAFT_THEME.
func Detect(getenv func(string) string, profile Profile, darkBackground bool) Inputs {
	return Inputs{
		ThemeSetting:   getenv("DCRAFT_THEME"),
		NoColor:        getenv("NO_COLOR") != "",
		Term:           getenv("TERM"),
		Profile:        profile,
		DarkBackground: darkBackground,
		UTF8:           UTFLocale(getenv),
	}
}

// UTFLocale reports whether the effective locale is UTF-8. The precedence is
// LC_ALL, LC_CTYPE, LANG; an entirely unset locale is treated as UTF-8 so
// modern terminals keep the richer glyphs.
func UTFLocale(getenv func(string) string) bool {
	value := ""
	for _, key := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if v := strings.TrimSpace(getenv(key)); v != "" {
			value = v
			break
		}
	}
	if value == "" {
		return true
	}
	lower := strings.ToLower(value)
	return strings.Contains(lower, "utf-8") || strings.Contains(lower, "utf8")
}
