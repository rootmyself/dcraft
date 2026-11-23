package visual

// Scrollbar paints the approved one-cell scrollbar gutter: a blank track with a
// solid thumb (`█`, ASCII `#`) painted in the accent token. It sits inside the
// content area, in the last cell of each band row, so it never touches a frame
// border and never covers a value.
//
// height is how many band rows the frame draws and top/visible/total describe
// the same window the counter uses. A band that fits paints nothing: a gutter
// with no thumb would claim a scroll position that does not exist.
func Scrollbar(th Theme, height, top, visible, total int) []string {
	if height <= 0 || visible <= 0 || total <= visible {
		return nil
	}
	size := height * visible / total
	if size < 1 {
		size = 1
	}
	if size > height {
		size = height
	}
	start := 0
	if span := total - visible; span > 0 {
		start = (height - size) * top / span
	}
	if start < 0 {
		start = 0
	}
	if start+size > height {
		start = height - size
	}
	out := make([]string, height)
	for i := range out {
		if i >= start && i < start+size {
			out[i] = th.Bold(TokenAccent, th.Glyphs.Thumb)
			continue
		}
		// The track is blank, not a second vertical rule: one cell from a
		// border, a rule track reads as a doubled frame.
		out[i] = " "
	}
	return out
}
