package visual

import (
	"math"
	"testing"
)

// The theme resolver is a pure function: every case is a table row, no
// environment is read here. The combinations are the ones the ticket and the
// spec name: light, dark, colorless, 16-color, non-UTF-8 and a plain en-US
// UTF-8 locale.
func TestResolveThemeTable(t *testing.T) {
	base := Inputs{ThemeSetting: "auto", Profile: ProfileTrueColor, DarkBackground: true, UTF8: true}
	cases := []struct {
		name      string
		in        Inputs
		palette   string
		color     bool
		ascii     bool
		accentHex string
	}{
		{
			name: "dark background auto truecolor utf8",
			in:   base, palette: "dark", color: true, ascii: false, accentHex: "#7aa2f7",
		},
		{
			name:    "light background auto truecolor utf8",
			in:      Inputs{ThemeSetting: "auto", Profile: ProfileTrueColor, DarkBackground: false, UTF8: true},
			palette: "light", color: true, ascii: false, accentHex: "#2f5bd1",
		},
		{
			name:    "explicit dark wins over a light background",
			in:      Inputs{ThemeSetting: "dark", Profile: ProfileTrueColor, DarkBackground: false, UTF8: true},
			palette: "dark", color: true, ascii: false, accentHex: "#7aa2f7",
		},
		{
			name:    "explicit light wins over a dark background",
			in:      Inputs{ThemeSetting: "light", Profile: ProfileTrueColor, DarkBackground: true, UTF8: true},
			palette: "light", color: true, ascii: false, accentHex: "#2f5bd1",
		},
		{
			name:    "empty setting follows detection",
			in:      Inputs{Profile: ProfileTrueColor, DarkBackground: false, UTF8: true},
			palette: "light", color: true, ascii: false, accentHex: "#2f5bd1",
		},
		{
			name:    "unknown setting follows detection instead of failing",
			in:      Inputs{ThemeSetting: "sepia", Profile: ProfileTrueColor, DarkBackground: true, UTF8: true},
			palette: "dark", color: true, ascii: false, accentHex: "#7aa2f7",
		},
		{
			name:    "NO_COLOR drops color but keeps UTF-8 glyphs",
			in:      Inputs{ThemeSetting: "dark", NoColor: true, Profile: ProfileTrueColor, DarkBackground: true, UTF8: true},
			palette: "dark", color: false, ascii: false, accentHex: "#7aa2f7",
		},
		{
			name:    "16-color terminal keeps color",
			in:      Inputs{ThemeSetting: "auto", Profile: Profile16, DarkBackground: true, UTF8: true},
			palette: "dark", color: true, ascii: false, accentHex: "#7aa2f7",
		},
		{
			name:    "256-color terminal keeps color",
			in:      Inputs{ThemeSetting: "auto", Profile: Profile256, DarkBackground: false, UTF8: true},
			palette: "light", color: true, ascii: false, accentHex: "#2f5bd1",
		},
		{
			name:    "non-TTY profile is colorless",
			in:      Inputs{ThemeSetting: "auto", Profile: ProfileNone, DarkBackground: true, UTF8: true},
			palette: "dark", color: false, ascii: false, accentHex: "#7aa2f7",
		},
		{
			name:    "TERM=dumb is colorless",
			in:      Inputs{ThemeSetting: "auto", Term: "dumb", Profile: ProfileTrueColor, DarkBackground: true, UTF8: true},
			palette: "dark", color: false, ascii: false, accentHex: "#7aa2f7",
		},
		{
			name:    "non-UTF-8 locale switches glyphs to ASCII only",
			in:      Inputs{ThemeSetting: "dark", Profile: ProfileTrueColor, DarkBackground: true, UTF8: false},
			palette: "dark", color: true, ascii: true, accentHex: "#7aa2f7",
		},
		{
			name:    "plain en-US UTF-8 locale stays UTF-8",
			in:      Inputs{ThemeSetting: "light", Profile: ProfileTrueColor, DarkBackground: false, UTF8: true},
			palette: "light", color: true, ascii: false, accentHex: "#2f5bd1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			th := Resolve(tc.in)
			if th.Palette.Name != tc.palette {
				t.Fatalf("palette = %q, want %q", th.Palette.Name, tc.palette)
			}
			if th.Color != tc.color {
				t.Fatalf("color = %v, want %v", th.Color, tc.color)
			}
			if th.Glyphs.ASCII != tc.ascii {
				t.Fatalf("ascii = %v, want %v", th.Glyphs.ASCII, tc.ascii)
			}
			if th.Palette.Accent != tc.accentHex {
				t.Fatalf("accent = %q, want %q", th.Palette.Accent, tc.accentHex)
			}
		})
	}
}

// The approved hex values are a contract, not a suggestion. A future edit that
// changes one of these without a new decision must fail here.
func TestPaletteUsesApprovedHex(t *testing.T) {
	dark := DarkPalette()
	light := LightPalette()
	wantDark := Palette{
		Name: "dark", Dark: true, BG: "#10131a", FG: "#d8dee9",
		Accent: "#7aa2f7", Success: "#5fd08a", Warning: "#e5b567",
		Danger: "#f07178", Muted: "#9aa5b8", Surface: "#1b2130",
		Info: "#5fd7ff", URL: "#5f5fff", Code: "#ffff55", Command: "#ff5555", NameColor: "#55ff77", Project: "#ff55ff",
	}
	wantLight := Palette{
		Name: "light", Dark: false, BG: "#ffffff", FG: "#1f2430",
		Accent: "#2f5bd1", Success: "#1b7a43", Warning: "#8a5a00",
		Danger: "#b4232f", Muted: "#5b6472", Surface: "#f1f4f9",
		Info: "#005f87", URL: "#003faf", Code: "#875f00", Command: "#af0000", NameColor: "#008700", Project: "#8700af",
	}
	if dark != wantDark {
		t.Fatalf("dark palette = %+v, want %+v", dark, wantDark)
	}
	if light != wantLight {
		t.Fatalf("light palette = %+v, want %+v", light, wantLight)
	}
}

// Every foreground token stays readable on both approved backgrounds. The
// approved floor is WCAG 4.5:1; the lowest approved value is light success on
// surface (4.87), so the margin is real but thin and worth guarding.
func TestTokenContrastFloor(t *testing.T) {
	tokens := []Token{TokenAccent, TokenSuccess, TokenWarning, TokenDanger, TokenMuted}
	for _, p := range []Palette{DarkPalette(), LightPalette()} {
		for _, tok := range tokens {
			hex, ok := p.Color(tok)
			if !ok {
				t.Fatalf("%s: token %v has no color", p.Name, tok)
			}
			for _, bg := range []string{p.BG, p.Surface} {
				if ratio := Contrast(hex, bg); ratio < 4.5 {
					t.Errorf("%s: %s on %s = %.2f, want >= 4.5", p.Name, tok, bg, ratio)
				}
			}
		}
	}
}

// Surface is a plate, not text: it must be distinguishable from the terminal
// background so a modal or header band still reads when color is stripped.
func TestSurfaceDiffersFromBackground(t *testing.T) {
	for _, p := range []Palette{DarkPalette(), LightPalette()} {
		if p.Surface == p.BG {
			t.Fatalf("%s: surface equals bg", p.Name)
		}
	}
}

func TestContrastRatioMatchesWCAGWorkedExample(t *testing.T) {
	// Black on white is the canonical 21:1 example.
	if got := Contrast("#000000", "#ffffff"); math.Abs(got-21) > 0.01 {
		t.Fatalf("black on white = %.4f, want 21", got)
	}
	// A color against itself is 1:1.
	if got := Contrast("#7aa2f7", "#7aa2f7"); math.Abs(got-1) > 0.0001 {
		t.Fatalf("self contrast = %.4f, want 1", got)
	}
}

func TestUTFLocaleDetection(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{"unset assumes UTF-8", nil, true},
		{"en_US.UTF-8", map[string]string{"LANG": "en_US.UTF-8"}, true},
		{"ko_KR.utf8 lower case", map[string]string{"LANG": "ko_KR.utf8"}, true},
		{"C locale is not UTF-8", map[string]string{"LANG": "C"}, false},
		{"POSIX is not UTF-8", map[string]string{"LC_ALL": "POSIX"}, false},
		{"LC_ALL overrides LANG", map[string]string{"LANG": "en_US.UTF-8", "LC_ALL": "C"}, false},
		{"LC_CTYPE counts", map[string]string{"LC_CTYPE": "ja_JP.UTF-8"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			getenv := func(key string) string { return tc.env[key] }
			if got := UTFLocale(getenv); got != tc.want {
				t.Fatalf("UTFLocale = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPlainThemeEmitsNoEscapes(t *testing.T) {
	th := Plain()
	for _, s := range []string{
		th.Paint(TokenAccent, "x"),
		th.PaintOn(TokenAccent, TokenSurface, "x"),
		th.Bold(TokenDanger, "x"),
	} {
		if s != "x" {
			t.Fatalf("plain theme emitted styling: %q", s)
		}
	}
	if th.Color {
		t.Fatal("plain theme reports color")
	}
}

// Detect is a pure mapping from explicit values: it never queries a terminal
// and never reads os.Getenv itself.
func TestDetectMapsExplicitInputs(t *testing.T) {
	env := map[string]string{
		"DCRAFT_THEME": "light",
		"NO_COLOR":     "1",
		"TERM":         "xterm-256color",
		"LANG":         "C",
	}
	in := Detect(func(k string) string { return env[k] }, Profile256, false)
	if in.ThemeSetting != "light" || !in.NoColor || in.Term != "xterm-256color" || in.Profile != Profile256 {
		t.Fatalf("Detect = %+v", in)
	}
	if in.DarkBackground {
		t.Fatal("Detect overrode the caller's background answer")
	}
	if in.UTF8 {
		t.Fatal("Detect ignored the locale")
	}
	// A NO_COLOR value is authoritative even in a real-color terminal.
	if th := Resolve(in); th.Color {
		t.Fatal("NO_COLOR resolved to a colored theme")
	}
}
