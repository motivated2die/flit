package ui

import (
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// Selecting text with the pointer, across paragraphs (a transcript's):
// where a paragraph's lines and characters are (TextLines, from the
// metrics), which text areas a page has (Ctx.Selectable), and what is
// selected (Selection, kept by the window between passes).

// TextLines is a wrapped paragraph's geometry, relative to its top left.
type TextLines struct {
	Lines []TextLine
}

// TextLine is one line: bytes [From, To) of the text, its top and height,
// and the caret stops along it (byte offset and x), from From to To.
type TextLine struct {
	From, To int
	Top, H   float32
	Stops    []TextStop
}

// TextStop is a place the caret can be: before byte At, at x.
type TextStop struct {
	At int
	X  float32
}

// LineMetrics is what Metrics implementations offer to lay text out in
// lines (DirectWrite does; selection needs it).
type LineMetrics interface {
	ParagraphLines(s string, f Font, width float32) *TextLines
}

// PosAt is the byte offset nearest to (x, y), relative to the paragraph.
func (t *TextLines) PosAt(x, y float32) int {
	if t == nil || len(t.Lines) == 0 {
		return 0
	}
	l := t.Lines[len(t.Lines)-1]
	for _, ln := range t.Lines {
		if y < ln.Top+ln.H {
			l = ln
			break
		}
	}
	if y < t.Lines[0].Top {
		return t.Lines[0].From
	}
	best, bestD := l.From, float32(1e9)
	for _, s := range l.Stops {
		if d := abs32(s.X - x); d < bestD {
			best, bestD = s.At, d
		}
	}
	return best
}

// End is the byte offset after the text.
func (t *TextLines) End() int {
	if t == nil || len(t.Lines) == 0 {
		return 0
	}
	return t.Lines[len(t.Lines)-1].To
}

// Rects are the boxes covering bytes [from, to), one per line touched,
// relative to the paragraph.
func (t *TextLines) Rects(from, to int) []RectF {
	if t == nil || to <= from {
		return nil
	}
	var out []RectF
	for _, l := range t.Lines {
		a, b := max(from, l.From), min(to, l.To)
		if b <= a {
			continue
		}
		out = append(out, RectF{l.x(a), l.Top, l.x(b), l.Top + l.H})
	}
	return out
}

// x is where byte offset at falls on the line (the last stop at or before
// it).
func (l TextLine) x(at int) float32 {
	x := float32(0)
	for _, s := range l.Stops {
		if s.At > at {
			break
		}
		x = s.X
	}
	return x
}

func abs32(v float32) float32 { return max(v, -v) }

// linesFromMetrics builds TextLines from DirectWrite's line metrics
// (length, height in UTF-16 units and pixels) and cluster metrics (width,
// length): a stop after every cluster, lines stacked from the top.
func linesFromMetrics(s string, lineLens []int, lineHs []float32, clusterLens []int, clusterWs []float32) *TextLines {
	// UTF-16 index → byte offset.
	b := make([]int, 0, len(s)+1)
	for i, r := range s {
		for range utf16.RuneLen(r) {
			b = append(b, i)
		}
	}
	b = append(b, len(s))
	byteAt := func(u int) int { return b[min(u, len(b)-1)] }

	t := &TextLines{}
	u, ci := 0, 0
	top := float32(0)
	for i, ll := range lineLens {
		l := TextLine{From: byteAt(u), Top: top, H: lineHs[i]}
		l.Stops = append(l.Stops, TextStop{At: l.From, X: 0})
		end := u + ll
		x := float32(0)
		for u < end && ci < len(clusterLens) {
			u += clusterLens[ci]
			x += clusterWs[ci]
			ci++
			l.Stops = append(l.Stops, TextStop{At: byteAt(u), X: x})
		}
		l.To = byteAt(end)
		t.Lines = append(t.Lines, l)
		top += lineHs[i]
		u = end
	}
	return t
}

// wordAt is the word around byte offset pos of s: letters, digits and the
// apostrophes and hyphens inside a word ("Release-Plan", "geht's"). On no
// word (a space, a comma), just that character.
func wordAt(s string, pos int) (from, to int) {
	isWord := func(r rune) bool {
		return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '\'' || r == '-' || r == '’'
	}
	pos = max(0, min(pos, len(s)))
	if pos < len(s) {
		if r, _ := utf8.DecodeRuneInString(s[pos:]); !isWord(r) {
			// Between words, or before punctuation: the word before.
			if pos > 0 {
				if q, _ := utf8.DecodeLastRuneInString(s[:pos]); isWord(q) {
					return wordAt(s, pos-utf8.RuneLen(q))
				}
			}
			_, size := utf8.DecodeRuneInString(s[pos:])
			return pos, pos + size
		}
	}
	from, to = pos, pos
	for from > 0 {
		r, size := utf8.DecodeLastRuneInString(s[:from])
		if !isWord(r) {
			break
		}
		from -= size
	}
	for to < len(s) {
		r, size := utf8.DecodeRuneInString(s[to:])
		if !isWord(r) {
			break
		}
		to += size
	}
	// A hyphen or apostrophe only belongs inside a word.
	for from < to && (s[from] == '-' || s[from] == '\'') {
		from++
	}
	for to > from && (s[to-1] == '-' || s[to-1] == '\'') {
		to--
	}
	return from, to
}

// --- the selection ----------------------------------------------------------

// Selection is text selected across a page's text areas: from an anchor
// (where the button went down) to a focus (where the pointer is), each a
// text area's place in the pass (its order) and a byte offset in it.
type Selection struct {
	AnchorArea, AnchorPos int
	FocusArea, FocusPos   int
}

// ordered returns the selection's start and end.
func (s Selection) ordered() (a, ap, b, bp int) {
	if s.AnchorArea < s.FocusArea || (s.AnchorArea == s.FocusArea && s.AnchorPos <= s.FocusPos) {
		return s.AnchorArea, s.AnchorPos, s.FocusArea, s.FocusPos
	}
	return s.FocusArea, s.FocusPos, s.AnchorArea, s.AnchorPos
}

// Empty reports whether nothing is selected.
func (s Selection) Empty() bool {
	a, ap, b, bp := s.ordered()
	return a == b && ap == bp
}

// textArea is selectable text in a pass.
type textArea struct {
	id    string
	r     RectF // where its lines start (their top left) and end
	text  string
	lines func() *TextLines // worked out when needed
	got   *TextLines
}

// geometry is the area's lines (computed once per pass).
func (t *textArea) geometry() *TextLines {
	if t.got == nil && t.lines != nil {
		t.got = t.lines()
	}
	return t.got
}

// Selectable registers text that can be selected with the pointer: s,
// wrapped as lines, drawn from r's top left. It returns the part of s
// that is selected now, [from, to) (equal: none), for the node to draw
// its highlight.
func (c *Ctx) Selectable(id string, r RectF, s string, lines func() *TextLines) (from, to int) {
	i := len(c.texts)
	c.texts = append(c.texts, textArea{id: id, r: r, text: s, lines: lines})
	hit := r
	if len(c.clips) > 0 {
		hit = r.Intersect(c.clips[len(c.clips)-1])
	}
	if hit.W() > 0 && hit.H() > 0 {
		c.hits = append(c.hits, Hit{ID: id, R: hit, text: true})
	}
	if c.Sel == nil {
		return 0, 0
	}
	a, ap, b, bp := c.Sel.ordered()
	if i < a || i > b {
		return 0, 0
	}
	from, to = 0, len(s)
	if i == a {
		from = ap
	}
	if i == b {
		to = bp
	}
	return min(from, len(s)), min(to, len(s))
}

// textAt is the text area and byte offset for (x, y): the area under it,
// or, between areas while selecting, the nearest place in reading order.
func (c *Ctx) textAt(x, y float32) (area, pos int, ok bool) {
	if len(c.texts) == 0 {
		return 0, 0, false
	}
	for i := range c.texts {
		if t := &c.texts[i]; t.r.Contains(x, y) {
			return i, t.geometry().PosAt(x-t.r.L, y-t.r.T), true
		}
	}
	// Outside every area: before the first one below the point, or at the
	// end of the last one above it.
	for i := range c.texts {
		t := &c.texts[i]
		if y < t.r.T {
			return i, 0, true
		}
		if y < t.r.B { // beside it
			if x < t.r.L {
				return i, t.geometry().PosAt(0, y-t.r.T), true
			}
			return i, t.geometry().PosAt(t.r.W(), y-t.r.T), true
		}
	}
	last := len(c.texts) - 1
	return last, len(c.texts[last].text), true
}

// SelectedText is the text of a selection in this pass: the selected
// part of each area, joined by blank lines.
func (c *Ctx) SelectedText(s Selection) string {
	a, ap, b, bp := s.ordered()
	var parts []string
	for i := a; i <= b && i < len(c.texts); i++ {
		t := c.texts[i].text
		from, to := 0, len(t)
		if i == a {
			from = min(ap, len(t))
		}
		if i == b {
			to = min(bp, len(t))
		}
		if to > from {
			parts = append(parts, strings.TrimSpace(t[from:to]))
		}
	}
	return strings.Join(parts, "\n\n")
}
