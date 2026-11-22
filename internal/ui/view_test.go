package ui

import (
	"strings"
	"testing"
	"time"
)

// fakeMetrics: 7 px per character at 13 px, lines 1.4 × the size.
type fakeMetrics struct{}

func (fakeMetrics) TextWidth(s string, f Font) float32 {
	return float32(len([]rune(s))) * f.Size * 0.54
}

func (fakeMetrics) ParagraphHeight(s string, f Font, w float32) float32 {
	perLine := int(w / (f.Size * 0.54))
	lines := (len([]rune(s)) + perLine - 1) / max(1, perLine)
	return float32(max(1, lines)) * f.Size * 1.4
}

// ParagraphLines breaks at characters, as ParagraphHeight counts.
func (fakeMetrics) ParagraphLines(s string, f Font, w float32) *TextLines {
	cw, lh := f.Size*0.54, f.Size*1.4
	per := max(1, int(w/cw))
	var lens, cls []int
	var hs, ws []float32
	n := len([]rune(s))
	for i := 0; i < n; i += per {
		lens, hs = append(lens, min(per, n-i)), append(hs, lh)
	}
	for range n {
		cls, ws = append(cls, 1), append(ws, cw)
	}
	return linesFromMetrics(s, lens, hs, cls, ws)
}

// recorder is a Canvas that keeps what was drawn.
type recorder struct {
	texts   []string
	clips   int
	bitmaps []string
	marks   []TextMark
}

func (r *recorder) RoundRect(RectF, float32, ColorF)                                 {}
func (r *recorder) Circle(float32, float32, float32, ColorF)                         {}
func (r *recorder) Arc(float32, float32, float32, float32, float32, float32, ColorF) {}
func (r *recorder) Text(s string, _ RectF, _ Font, _ ColorF)                         { r.texts = append(r.texts, s) }
func (r *recorder) Paragraph(s string, _ RectF, _ Font, _ ColorF)                    { r.texts = append(r.texts, s) }
func (r *recorder) MarkedParagraph(s string, _ RectF, _ Font, _ ColorF, m []TextMark) {
	r.texts = append(r.texts, s)
	r.marks = append(r.marks, m...)
}
func (r *recorder) StyledParagraph(s string, _ RectF, _ Font, _ ColorF, _ []TextStyle) {
	r.texts = append(r.texts, s)
}
func (r *recorder) Polygons([][]PointF, ColorF) {}
func (r *recorder) PushClip(RectF)              { r.clips++ }
func (r *recorder) PopClip()                    { r.clips-- }
func (r *recorder) Bitmap(key string, _, _ float32, _, _ int, _ float32, _ func() []byte) {
	r.bitmaps = append(r.bitmaps, key)
}

func ctx(hover string, scroll map[string]float32) *Ctx {
	return NewCtx(1, fakeMetrics{}, time.Now(), hover, "", scroll)
}

func frame(page Node, onNav func(string)) Frame {
	return Frame{
		Nav:      []NavEntry{{ID: "home", Label: "Home", Glyph: GlyphHome}, {ID: "settings", Label: "Settings", Glyph: GlyphSettings}},
		Selected: "home", PageID: "home", OnNav: onNav, Page: page, Status: "Idle",
	}
}

func TestFrameNavigates(t *testing.T) {
	var went string
	c := ctx("", nil)
	frame(Title("Home"), func(id string) { went = id }).Layout(c, 900, 600)
	var nav Hit
	for _, h := range c.Hits() {
		if h.ID == "nav:settings" {
			nav = h
		}
	}
	if nav.ID == "" {
		t.Fatal("no settings nav item")
	}
	h, ok := c.HitAt((nav.R.L+nav.R.R)/2, (nav.R.T+nav.R.B)/2)
	if !ok {
		t.Fatal("nothing hit")
	}
	h.OnClick()
	if went != "settings" {
		t.Fatalf("went %q", went)
	}
	rec := &recorder{}
	c.Paint(rec)
	if rec.clips != 0 {
		t.Fatalf("unbalanced clips: %d", rec.clips)
	}
	joined := strings.Join(rec.texts, "|")
	for _, want := range []string{"flit", "Home", "Settings", "Idle"} {
		if !strings.Contains(joined, want) {
			t.Errorf("drew %q, missing %q", joined, want)
		}
	}
}

func tallPage(n int, onClick func(int)) Node {
	var items []Node
	for i := 0; i < n; i++ {
		i := i
		items = append(items, ListRow{ID: "row" + string(rune('a'+i)), Title: "Row", OnClick: func() { onClick(i) }})
	}
	return VStack{Items: items}
}

func TestFrameScrolls(t *testing.T) {
	scroll := map[string]float32{}
	c := ctx("", scroll)
	frame(tallPage(20, func(int) {}), nil).Layout(c, 900, 600)
	a, ok := c.ScrollAt(500, 300)
	if !ok || a.Max <= 0 {
		t.Fatalf("scroll area %+v %v", a, ok)
	}
	// Scrolled past the page's end: clamped.
	scroll[a.ID] = 1e6
	frame(tallPage(20, func(int) {}), nil).Layout(ctx("", scroll), 900, 600)
	if scroll[a.ID] != a.Max {
		t.Fatalf("offset %v, max %v", scroll[a.ID], a.Max)
	}
}

func TestClicksOutsideTheClipDontHit(t *testing.T) {
	scroll := map[string]float32{FrameScrollID("home"): 300}
	clicked := -1
	c := ctx("", scroll)
	frame(tallPage(20, func(i int) { clicked = i }), nil).Layout(c, 900, 600)
	for _, h := range c.Hits() {
		if h.R.T < 0 || h.R.B > 600 {
			t.Fatalf("hit %s outside the visible page: %+v", h.ID, h.R)
		}
	}
	// The first visible row is no longer row 0.
	h, _ := c.HitAt(400, 40)
	h.OnClick()
	if clicked <= 0 {
		t.Fatalf("clicked row %d at the top after scrolling", clicked)
	}
}

func TestToggleAndSegmented(t *testing.T) {
	var on bool
	var picked = -1
	page := VStack{Items: []Node{
		Toggle{ID: "t", Label: "Start with Windows", On: false, OnChange: func(v bool) { on = v }},
		Segmented{ID: "lang", Options: []string{"German", "English"}, Selected: 0, OnSelect: func(i int) { picked = i }},
	}}
	c := ctx("", nil)
	page.Layout(c, 0, 0, 500)
	for _, h := range c.Hits() {
		h.OnClick()
	}
	if !on || picked != 1 {
		t.Fatalf("toggle %v, picked %d (the selected segment isn't clickable)", on, picked)
	}
}

func TestHStackSizes(t *testing.T) {
	c := ctx("", nil)
	b1 := Button{ID: "a", Label: "Copy", OnClick: func() {}}
	b2 := Button{ID: "b", Label: "Open folder", Glyph: GlyphFolder, OnClick: func() {}}
	HStack{Gap: 8, Items: []Node{b1, b2}, Right: true}.Layout(c, 0, 0, 600)
	hits := c.Hits()
	if len(hits) != 2 || hits[1].R.R != 600 || hits[0].R.R >= hits[1].R.L {
		t.Fatalf("hits %+v", hits)
	}
	if b2.Width(c) <= b1.Width(c) {
		t.Fatal("a longer label with a glyph isn't wider")
	}
}

func TestFrameDock(t *testing.T) {
	var scrubbed []float32
	bar := PlayerBar{ID: "player", Len: 10 * time.Minute, OnToggle: func() {}, OnScrub: func(f float32, done bool) {
		if done {
			scrubbed = append(scrubbed, f)
		}
	}}
	scroll := map[string]float32{}
	f := frame(tallPage(20, func(int) {}), nil)
	f.Dock, f.DockID = bar, "player:x"
	c := ctx("", scroll)
	f.Layout(c, 900, 600)
	// The player floats at the bottom; the page goes on beneath it, but
	// a click on the pill never reaches a row under it.
	drag, ok := c.HitByID("player:bar")
	if !ok || drag.OnDrag == nil || drag.R.T < 600-80 {
		t.Fatalf("player bar %+v", drag)
	}
	pill, ok := c.HitByID("dock")
	if !ok {
		t.Fatal("no pill")
	}
	for x := pill.R.L + 1; x < pill.R.R; x += 7 {
		if h, _ := c.HitAt(x, pill.R.B-2); strings.HasPrefix(h.ID, "row") {
			t.Fatalf("a click on the pill at %v reaches %s", x, h.ID)
		}
	}
	// Dragging past the right end and letting go there seeks to the end.
	drag.OnDrag(drag.R.L, 0, false)
	drag.OnDrag(drag.R.R+50, 0, true)
	if len(scrubbed) != 1 || scrubbed[0] != 1 {
		t.Fatalf("scrubbed %v", scrubbed)
	}
	// Scrolled to the end, the last row ends above the dock.
	a, _ := c.ScrollArea(FrameScrollID("home"))
	scroll[a.ID] = a.Max
	c = ctx("", scroll)
	f.Layout(c, 900, 600)
	if h, ok := c.HitByID("rowt"); !ok || h.R.B > pill.R.T {
		t.Fatalf("last row %+v %v, pill at %v", h.R, ok, pill.R.T)
	}
	// While it slides up it asks for frames.
	f.DockAt = c.Now
	c = ctx("", scroll)
	f.Layout(c, 900, 600)
	if !c.Animating {
		t.Fatal("the dock's slide doesn't animate")
	}
	// Sliding out (its page was left): drawn, but no clicks and no room.
	f.DockOut, f.DockAt = true, time.Now().Add(-100*time.Millisecond)
	scroll = map[string]float32{}
	c = ctx("", scroll)
	f.Layout(c, 900, 600)
	if _, ok := c.HitByID("player:bar"); ok || !c.Animating {
		t.Fatalf("leaving dock: clickable or still")
	}
	if out, _ := c.ScrollArea(FrameScrollID("home")); out.Max >= a.Max {
		t.Fatalf("a leaving dock keeps room: max %v, shown %v", out.Max, a.Max)
	}
}

func TestCornerCardSticks(t *testing.T) {
	var items []Node
	for i := 0; i < 30; i++ {
		items = append(items, Label{Text: "line"})
	}
	field := TextField{ID: "q", W: 200}
	corner := func(c *Ctx) RectF { h, _ := c.HitByID("q"); return h.R }
	card := CornerCard{Corner: []Node{field}, Items: items}
	// Not sticky: in its row at the card's top, inside the card's right.
	c := ctx("", nil)
	c.StartEdit = func(string, string, int) {}
	card.Layout(c, 0, 100, 600)
	if r := corner(c); r.T < 100 || r.T > 140 || r.R > 600 {
		t.Fatalf("corner at %+v", r)
	}
	// Sticky, the card scrolled past the view's top (40): at the view's top.
	card.Sticky = true
	c = ctx("", nil)
	c.StartEdit = func(string, string, int) {}
	c.PushClip(RectF{0, 40, 900, 600})
	h := card.Layout(c, 0, -300, 600)
	c.PopClip()
	if r := corner(c); r.T < 40 || r.T > 60 {
		t.Fatalf("stuck corner at %+v", r)
	}
	if _, ok := c.HitByID("corner"); !ok {
		t.Fatal("no backing under the stuck corner")
	}
	// Scrolled so far that only the card's end shows: it stays inside.
	c = ctx("", nil)
	c.StartEdit = func(string, string, int) {}
	c.PushClip(RectF{0, 40, 900, 600})
	card.Layout(c, 0, 40-h+20, 600)
	c.PopClip()
	if r := corner(c); r.B > 40+20 {
		t.Fatalf("corner %+v past the card's end at %v", r, 40+20)
	}
}

func TestScrollIntoView(t *testing.T) {
	scroll := map[string]float32{}
	target := 900
	var items []Node
	for i := 0; i < 40; i++ {
		items = append(items, TranscriptBlock{ID: "b", Speaker: "Me", Text: "words", ScrollTo: i == target/45})
	}
	f := frame(VStack{Items: items}, nil)
	c := ctx("", scroll)
	f.Layout(c, 900, 600)
	w := c.WantScroll
	if w == nil || w.ID != FrameScrollID("home") || w.To <= 0 {
		t.Fatalf("want %+v", w)
	}
	// Scrolled there, the block sits in the upper part of the view.
	scroll[w.ID] = w.To
	c = ctx("", scroll)
	f.Layout(c, 900, 600)
	if c.WantScroll == nil || c.WantScroll.To != w.To {
		t.Fatalf("asks for a different place once there: %+v", c.WantScroll)
	}
}

func TestFrameModal(t *testing.T) {
	var cancelled bool
	f := frame(tallPage(20, func(int) { t.Fatal("a row under the popover was clicked") }), nil)
	f.Modal = VStack{Items: []Node{Label{Text: "Uninstall flit"}, Button{ID: "cancel", Label: "Cancel", OnClick: func() { cancelled = true }}}}
	c := ctx("", nil)
	f.Layout(c, 900, 600)
	// Beneath the popover: the backdrop, not the page or the sidebar.
	for _, p := range [][2]float32{{400, 40}, {50, 100}, {880, 580}} {
		if h, _ := c.HitAt(p[0], p[1]); h.ID != "modal:backdrop" || h.clickable() {
			t.Fatalf("at %v: %q", p, h.ID)
		}
	}
	b, ok := c.HitByID("cancel")
	if !ok {
		t.Fatal("no Cancel in the popover")
	}
	if h, _ := c.HitAt((b.R.L+b.R.R)/2, (b.R.T+b.R.B)/2); h.ID != "cancel" {
		t.Fatalf("Cancel covered by %q", h.ID)
	}
	b.OnClick()
	if !cancelled {
		t.Fatal("Cancel did nothing")
	}
	// Opening, it fades in.
	f.ModalAt = c.Now
	c = ctx("", nil)
	f.Layout(c, 900, 600)
	if !c.Animating {
		t.Fatal("no fade")
	}
}

func TestHaloPixels(t *testing.T) {
	w, h, fade := 200, 80, 24
	pix := haloPixels(w, h, float64(fade), 16, ColCard, 0.76)
	alpha := func(x, y int) int { return int(pix[4*(y*w+x)+3]) }
	// Inside the pill: the halo's opacity; at the box's edge: clear; and
	// fading in between, at the sides and above and below alike.
	if a := alpha(w/2, h/2); a < 190 || a > 196 {
		t.Fatalf("inside %d, want about 194", a)
	}
	if alpha(0, h/2) != 0 || alpha(w/2, 0) != 0 || alpha(0, 0) != 0 {
		t.Fatal("the box's edge is lit")
	}
	if a := alpha(fade/2, h/2); a <= 10 || a >= 190 {
		t.Fatalf("half way out at the side %d", a)
	}
	if alpha(w/2, fade/2) != alpha(fade/2, h/2) && abs(alpha(w/2, fade/2)-alpha(fade/2, h/2)) > 3 {
		t.Fatalf("fades differently above (%d) and beside (%d)", alpha(w/2, fade/2), alpha(fade/2, h/2))
	}
}

func abs(v int) int { return max(v, -v) }

func TestFadePixels(t *testing.T) {
	pix := fadePixels(10, 40, 20, ColBase, 0.9)
	alpha := func(y int) int { return int(pix[4*(y*10)+3]) }
	if alpha(0) > 2 || alpha(10) <= alpha(2) || alpha(30) < 228 || alpha(39) != alpha(30) {
		t.Fatalf("fade alphas %d %d %d %d", alpha(0), alpha(10), alpha(30), alpha(39))
	}
}

func TestSearchFieldChanges(t *testing.T) {
	var cleared bool
	field := TextField{ID: "q", Glyph: GlyphSearch, Value: "abc", OnChange: func(string) {}, OnClear: func() { cleared = true }}
	c := ctx("", nil)
	c.StartEdit = func(string, string, int) {}
	field.Layout(c, 0, 0, 300)
	if c.changer("q") == nil {
		t.Fatal("OnChange not registered")
	}
	h, ok := c.HitByID("q:clear")
	if !ok {
		t.Fatal("no clear button with text in the field")
	}
	h.OnClick()
	if !cleared {
		t.Fatal("clear did nothing")
	}
	// Without text: no clear button.
	c = ctx("", nil)
	TextField{ID: "q", OnClear: func() {}}.Layout(c, 0, 0, 300)
	if _, ok := c.HitByID("q:clear"); ok {
		t.Fatal("clear button on an empty field")
	}
}

func TestLiveNodesAnimate(t *testing.T) {
	c := ctx("", nil)
	LiveTitle{Text: "Recording", Detail: "1:02"}.Layout(c, 0, 0, 400)
	if !c.Animating {
		t.Fatal("a live node didn't ask for frames")
	}
}
