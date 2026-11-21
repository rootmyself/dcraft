package visual

import (
	"strings"
	"testing"
)

// Korean user-facing text lives in messages.go per package. These guards catch a
// new inline literal before it reaches a snapshot, so the relocation cannot
// silently regress into a second vocabulary.
//
// They scan the non-test renderer sources and fail on any quoted string that
// contains a Hangul syllable. Comments are allowed to be Korean.
func TestNoInlineKoreanInRendererSources(t *testing.T) {
	files := []string{
		"frame.go", "footer.go", "nav.go", "document.go", "document_layout.go",
		"modal.go", "viewport.go", "layout.go", "width.go", "ascii.go", "theme.go",
		"scrollbar.go",
	}
	for _, name := range files {
		src := readSource(t, name)
		for i, line := range strings.Split(src, "\n") {
			code := stripLineComment(line)
			if containsHangul(code) && strings.Contains(code, `"`) {
				t.Fatalf("%s:%d has an inline Korean literal; move it to messages.go:\n\t%s", name, i+1, strings.TrimSpace(line))
			}
		}
	}
}

// The keycap labels the visual layer owns must all be reachable constants, not
// copies of the same word with different spellings.
func TestFooterLabelsComeFromMessages(t *testing.T) {
	for phase := PhaseMenu; phase <= PhaseBusy; phase++ {
		for _, item := range FooterItems(phase) {
			if item.Label == "" {
				t.Fatalf("phase %s has a keycap with no label", phase)
			}
			if containsHangul(item.Label) && !knownKeyLabel(item.Label) {
				t.Fatalf("phase %s label %q is not one of the messages.go constants", phase, item.Label)
			}
		}
	}
}

func knownKeyLabel(label string) bool {
	for _, known := range []string{
		msgKeyRun, msgKeyCancel, msgKeyItem, msgKeyHelp, msgKeySubmit, msgKeyNextField,
		msgKeyPrev, msgKeyCloseScreen, msgKeyStop, msgKeyDetach, msgKeySafeCancel,
		msgKeyQuitBlocked, msgKeyMove, msgKeyQuit, msgKeyFind, msgKeyMenu, msgKeyRefresh,
	} {
		if label == known {
			return true
		}
	}
	return false
}

func stripLineComment(line string) string {
	if i := strings.Index(line, "//"); i >= 0 {
		// A "//" inside a string literal is not a comment; the renderer's
		// literals never contain one, so a plain cut is safe here.
		return line[:i]
	}
	return line
}

func containsHangul(s string) bool {
	for _, r := range s {
		if r >= 0xAC00 && r <= 0xD7A3 {
			return true
		}
	}
	return false
}
