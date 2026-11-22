package ui

import (
	"fmt"
	"strings"
	"testing"
)

func TestTextLines(t *testing.T) {
	f := Font{Size: 10}
	// fakeMetrics: 5.4 px a character, so 10 characters a line at 54 px.
	lines := fakeMetrics{}.ParagraphLines("abcdefghijKLMNOPQRSTuvw", f, 54)
	if len(lines.Lines) != 3 || lines.End() != 23 {
		t.Fatalf("%d lines, end %d", len(lines.Lines), lines.End())
	}
	if p := lines.PosAt(5.4*3+1, 2); p != 3 {
		t.Fatalf("first line, after c: %d", p)
	}
	if p := lines.PosAt(0, 15); p != 10 { // second line's start
		t.Fatalf("second line: %d", p)
	}
	if p := lines.PosAt(500, 500); p != 23 { // past the end
		t.Fatalf("past the end: %d", p)
	}
	// A range over two lines: two boxes, from the start column to the end.
	r := lines.Rects(8, 12)
	if len(r) != 2 || r[0].L < 43 || r[0].R < 53 || r[1].L != 0 || r[1].R > 11 {
		t.Fatalf("rects %+v", r)
	}
}

func TestTextLinesUTF8(t *testing.T) {
	// "für" is 4 bytes: stops land on rune boundaries.
	lines := fakeMetrics{}.ParagraphLines("für alle", Font{Size: 10}, 500)
	if p := lines.PosAt(5.4*2+1, 2); p != 3 { // after ü (bytes 1..2)
		t.Fatalf("after ü: %d", p)
	}
}

func TestSelectAcrossAreas(t *testing.T) {
	texts := []string{"Hallo zusammen.", "Gerne, ich bin da.", "Dann los."}
	layout := func(sel *Selection) *Ctx {
		c := ctx("", nil)
		c.Sel = sel
		for i, s := range texts {
			lines := fakeMetrics{}.ParagraphLines(s, Font{Size: 10}, 400)
			c.Selectable("t"+string(rune('a'+i)), RectF{0, float32(i) * 40, 400, float32(i)*40 + 14}, s, func() *TextLines { return lines })
		}
		return c
	}
	c := layout(nil)
	// The pointer on the second text, and between the texts.
	if a, p, ok := c.textAt(5.4*5+1, 45); !ok || a != 1 || p != 5 {
		t.Fatalf("on the second text: %d %d %v", a, p, ok)
	}
	if a, p, _ := c.textAt(10, 30); a != 1 || p != 0 {
		t.Fatalf("between the first and second: %d %d", a, p)
	}
	if a, p, _ := c.textAt(10, 500); a != 2 || p != len(texts[2]) {
		t.Fatalf("below everything: %d %d", a, p)
	}
	// Selected backwards from the third text into the first.
	sel := &Selection{AnchorArea: 2, AnchorPos: 4, FocusArea: 0, FocusPos: 6}
	if got := c.SelectedText(*sel); got != "zusammen.\n\nGerne, ich bin da.\n\nDann" {
		t.Fatalf("selected %q", got)
	}
	c = layout(sel)
	if len(c.texts) != 3 {
		t.Fatal("areas not kept")
	}
	// Each area's share, as Selectable returns it.
	c2 := ctx("", nil)
	c2.Sel = sel
	var got []string
	for i, s := range texts {
		from, to := c2.Selectable("x", RectF{0, float32(i) * 40, 400, float32(i)*40 + 14}, s, nil)
		got = append(got, s[from:to])
	}
	if strings.Join(got, "|") != "zusammen.|Gerne, ich bin da.|Dann" {
		t.Fatalf("shares %q", got)
	}
}

func TestTranscriptBlockHover(t *testing.T) {
	b := TranscriptBlock{ID: "para:3", Speaker: "Anna", Time: "00:15", Text: "Von meiner Seite gibt es noch zwei offene Punkte.", OnPlay: func() {}, OnCopy: func() {}}
	c := ctx("", nil)
	b.Layout(c, 0, 0, 500)
	if _, ok := c.HitByID("para:3:copy"); ok {
		t.Fatal("buttons without hover")
	}
	text, ok := c.HitByID("para:3:text")
	if !ok || !text.text || text.clickable() {
		t.Fatalf("text area %+v", text)
	}
	// Hovering the text (a part of the block) shows the buttons, on top.
	c = ctx("para:3:text", nil)
	b.Layout(c, 0, 0, 500)
	cp, ok1 := c.HitByID("para:3:copy")
	pl, ok2 := c.HitByID("para:3:play")
	if !ok1 || !ok2 || pl.R.R > cp.R.L {
		t.Fatalf("buttons %+v %+v", pl, cp)
	}
	if h, _ := c.HitAt((cp.R.L+cp.R.R)/2, (cp.R.T+cp.R.B)/2); h.ID != "para:3:copy" {
		t.Fatalf("copy covered by %q", h.ID)
	}
	// Playing keeps them without hover.
	b.Playing = true
	c = ctx("", nil)
	b.Layout(c, 0, 0, 500)
	if _, ok := c.HitByID("para:3:play"); !ok {
		t.Fatal("no pause button while playing")
	}
}

func TestWordAt(t *testing.T) {
	s := "Gerne, lasst uns den Release-Plan klären – geht's?"
	word := func(at string) string {
		i := strings.Index(s, at)
		from, to := wordAt(s, i+1)
		return s[from:to]
	}
	for at, want := range map[string]string{
		"erne": "Gerne", "elease": "Release-Plan", "lären": "klären", "eht": "geht's",
	} {
		if got := word(at); got != want {
			t.Errorf("around %q: %q, want %q", at, got, want)
		}
	}
	// On the comma right after a word: that word.
	if from, to := wordAt(s, strings.Index(s, ",")); s[from:to] != "Gerne" {
		t.Errorf("at the comma: %q", s[from:to])
	}
	// On a space between words that follows nothing: just it.
	if from, to := wordAt("  x", 0); from != 0 || to != 1 {
		t.Errorf("leading space: %d %d", from, to)
	}
}

// TestSelectionSurvivesScrolling: every paragraph's text is registered,
// on screen or not, so a selection's ends keep their places while the
// page scrolls (they used to shift as paragraphs left the view).
func TestSelectionSurvivesScrolling(t *testing.T) {
	var items []Node
	for i := range 40 {
		items = append(items, TranscriptBlock{ID: fmt.Sprint("p", i), Speaker: "Anna", Text: fmt.Sprint("Absatz ", i, " mit ein paar Worten.")})
	}
	f := frame(VStack{Items: items}, nil)
	sel := &Selection{AnchorArea: 2, AnchorPos: 3, FocusArea: 30, FocusPos: 5}
	for _, off := range []float32{0, 900, 2000} {
		c := ctx("", map[string]float32{FrameScrollID("home"): off})
		c.Sel = sel
		f.Layout(c, 900, 600)
		if len(c.texts) != 40 {
			t.Fatalf("scrolled %v: %d text areas, want all 40", off, len(c.texts))
		}
		if got := c.SelectedText(*sel); !strings.HasPrefix(got, "atz 2 mit") || !strings.HasSuffix(got, "Absat") {
			t.Fatalf("scrolled %v: selected %q", off, got)
		}
	}
}
