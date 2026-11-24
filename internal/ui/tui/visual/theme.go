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

// DarkPalette targets a dark terminal background.
//
// The state tokens clear 4.5:1 contrast on both BG and Surface. Muted keeps 3:1
// against both, and sits far enough from FG in brightness to read as secondary
// at a glance: the earlier value was 1.84:1 against FG, which is why a label
// and a value looked alike. A border or rule is structure, not text, so 3:1 is
// the right floor for it. The value-classification tokens are decorative, but
// they keep enough chroma to separate path, URL, image, command, service and
// project from each other without relying on color alone.
func DarkPalette() Palette {
	return Palette{
		Name: "dark", Dark: true, BG: "#10131a", FG: "#d8dee9",
		Accent: "#7aa2f7", Success: "#5fd08a", Warning: "#e5b567",
		Danger: "#f07178", Muted: "#6f7c93", Surface: "#1b2130",
		Info: "#4fd8ff", URL: "#8b8bff", Code: "#ffe000", Command: "#ff3b3b", NameColor: "#3dff6e", Project: "#ff3bff",
	}
}

// LightPalette targets a light terminal background. The six state tokens clear
// 4.5:1 contrast on both BG and Surface; the value-classification tokens are
// decorative and are not held to that floor.
func LightPalette() Palette {
	return Palette{
		Name: "light", Dark: false, BG: "#ffffff", FG: "#1f2430",
		Accent: "#2f5bd1", Success: "#1b7a43", Warning: "#8a5a00",
		Danger: "#b4232f", Muted: "#747d8f", Surface: "#f1f4f9",
		Info: "#0057c2", URL: "#0036d6", Code: "#7a4b00", Command: "#c40000", NameColor: "#00702b", Project: "#8c0090",
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
	return t.sgrToken(tok, hex, "", false) + s + "\x1b[0m"
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
	return t.sgrToken(tok, hex, "", true) + s + "\x1b[0m"
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
	return t.sgrToken(fg, fgHex, bgHex, bold) + s + "\x1b[0m"
}

func (t Theme) sgrToken(tok Token, fg, bg string, bold bool) string {
	if t.Profile != Profile16 {
		return sgr(fg, bg, bold)
	}
	codes := []string{"0"}
	if bold {
		codes = append(codes, "1")
	}
	if fg != "" {
		// A 16-color terminal uses the reviewed slot table: nearest-RGB
		// collapsed distinct tokens onto one color.
		code, ok := t.Palette.ANSI16Slot(tok)
		if !ok {
			code = ansi16Nearest(fg)
		}
		codes = append(codes, strconv.Itoa(code))
	}
	if bg != "" {
		// A background plate is one surface, not a family of meanings, so the
		// nearest slot is the right rule here.
		codes = append(codes, strconv.Itoa(ansi16Nearest(bg)+10))
	}
	return "\x1b[" + strings.Join(codes, ";") + "m"
}

// ansi16Dark and ansi16Light assign each token its own slot on a 16-color
// terminal. Nearest-RGB is the wrong rule here: it put dark success on gray and
// light code on black, and it collapsed accent, warning and URL onto plain
// white. A terminal with 16 colors gives each slot a meaning, so the assignment
// is a reviewed table instead of a distance. Every token has its own code.
var ansi16Dark = map[Token]int{
	TokenAccent:  94, // bright blue: selection and focus
	TokenSuccess: 92, // bright green
	TokenWarning: 93, // bright yellow
	TokenDanger:  91, // bright red
	TokenMuted:   90, // gray: labels, borders and rules
	TokenInfo:    96, // bright cyan: paths
	TokenURL:     35, // magenta: links
	TokenCode:    33, // yellow: images and literals
	TokenCommand: 31, // red: commands
	TokenName:    32, // green: service names
	TokenProject: 95, // bright magenta: project names
}

var ansi16Light = map[Token]int{
	TokenAccent:  34, // blue
	TokenSuccess: 32, // green
	TokenWarning: 33, // yellow
	TokenDanger:  31, // red
	TokenMuted:   90, // gray: labels, borders and rules
	TokenInfo:    36, // cyan: paths
	TokenURL:     94, // bright blue: links
	TokenCode:    95, // bright magenta: images and literals
	TokenCommand: 91, // bright red: commands
	TokenName:    92, // bright green: service names
	TokenProject: 35, // magenta: project names
}

// ansi16Nearest is the fallback for a color the table does not name, such as a
// background plate: the closest of the 16 slots by RGB distance.
func ansi16Nearest(hex string) int {
	colors := [16][3]int{{0, 0, 0}, {128, 0, 0}, {0, 128, 0}, {128, 128, 0}, {0, 0, 128}, {128, 0, 128}, {0, 128, 128}, {192, 192, 192}, {128, 128, 128}, {255, 0, 0}, {0, 255, 0}, {255, 255, 0}, {0, 0, 255}, {255, 0, 255}, {0, 255, 255}, {255, 255, 255}}
	r, g, b := HexRGB(hex)
	best, distance := 0, math.MaxInt
	for i, c := range colors {
		d := (r-c[0])*(r-c[0]) + (g-c[1])*(g-c[1]) + (b-c[2])*(b-c[2])
		if d < distance {
			best, distance = i, d
		}
	}
	if best >= 8 {
		return 90 + best - 8
	}
	return 30 + best
}

// ANSI16Slot returns the SGR foreground code for a token on a 16-color terminal.
func (p Palette) ANSI16Slot(tok Token) (int, bool) {
	table := ansi16Dark
	if !p.Dark {
		table = ansi16Light
	}
	code, ok := table[tok]
	return code, ok
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

// Chroma is the HSL chroma of a color: the distance between its strongest and
// weakest channel. A nearly grey color has almost none, which is why a muted
// gray reads as structure while a saturated value reads as data.
func Chroma(hex string) float64 {
	r, g, b := HexRGB(hex)
	rf, gf, bf := float64(r)/255, float64(g)/255, float64(b)/255
	high := math.Max(rf, math.Max(gf, bf))
	low := math.Min(rf, math.Min(gf, bf))
	return high - low
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
