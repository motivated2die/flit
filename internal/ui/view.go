package ui

import (
	"math"
	"time"
)

// flit's window is drawn, not built from Windows controls. Pages are trees
// of Nodes, built fresh from the app's state whenever something changes
// (as Resound rebuilds its panel's rows): no widget state to keep in sync.
// A layout pass (Ctx) records what to draw and where clicks go; painting
// and hit-testing then use the same record, so they always agree.
//
// Everything here is portable and tested with a recording Canvas; the
// window that drives it is window_windows.go.

// PointF is a point in pixels (D2D1_POINT_2F's layout).
type PointF struct{ X, Y float32 }

// Canvas is what nodes draw on.
type Canvas interface {
	RoundRect(r RectF, radius float32, c ColorF)
	Circle(cx, cy, r float32, c ColorF)
	Arc(cx, cy, inner, outer, a0, a1 float32, c ColorF)
	Text(s string, r RectF, f Font, c ColorF)      // one line, vertically centred, clipped
	Paragraph(s string, r RectF, f Font, c ColorF) // wrapped, from the top
	// MarkedParagraph is Paragraph with parts in other colours (search
	// results); marks change no metrics, so it wraps as Paragraph does.
	MarkedParagraph(s string, r RectF, f Font, c ColorF, marks []TextMark)
	// StyledParagraph is Paragraph with ranges in bold, italics,
	// strikethrough or their own colour (the notes editor); it wraps as
	// StyledLines measures.
	StyledParagraph(s string, r RectF, f Font, c ColorF, styles []TextStyle)
	Polygons(polys [][]PointF, c ColorF)
	PushClip(r RectF)
	PopClip()
	// Bitmap draws a w×h premultiplied BGRA picture with its top left at
	// (x, y), at an opacity. pixels makes it the first time key is drawn;
	// the canvas keeps it under key (soft light that is costly to compute).
	Bitmap(key string, x, y float32, w, h int, opacity float32, pixels func() []byte)
	// ScaledBitmap draws a cached srcW×srcH premultiplied BGRA picture scaled
	// with linear filtering into dst at an opacity (soft expanding ripples).
	ScaledBitmap(key string, dst RectF, srcW, srcH int, opacity float32, pixels func() []byte)
}

// TextMark colours bytes [From, To) of a paragraph's text, optionally
// underlined (the current search result).
type TextMark struct {
	From, To  int
	Color     ColorF
	Underline bool
}

// TextStyle styles bytes [From, To) of a styled paragraph; Color nil keeps
// the paragraph's colour.
type TextStyle struct {
	From, To             int
	Bold, Italic, Strike bool
	Color                *ColorF
}

// StyledMetrics lays out styled paragraphs (DirectWrite does).
type StyledMetrics interface {
	StyledLines(s string, f Font, width float32, styles []TextStyle) *TextLines
}

// Metrics measures text in pixels.
type Metrics interface {
	TextWidth(s string, f Font) float32
	ParagraphHeight(s string, f Font, width float32) float32
}

// Node is a part of a page. Layout places it at (x, y) with width w,
// records its drawing and clicks in c, and returns its height.
type Node interface {
	Layout(c *Ctx, x, y, w float32) float32
}

// Sized nodes have a natural width (buttons in a row).
type Sized interface {
	Width(c *Ctx) float32
}

// Hit is a clickable area.
type Hit struct {
	ID        string
	R         RectF
	OnClick   func()
	OnClickAt func(x, y float32) // instead of OnClick: where the click was
	// OnDrag instead of OnClick: called where the button went down, as the
	// pointer moves while it is held (anywhere, even outside the window),
	// and with done where it is released (a seek bar's scrubbing).
	OnDrag func(x, y float32, done bool)
	// text marks selectable text (Ctx.Selectable): the window selects
	// with the pointer there, under an I-beam.
	text bool
	// editor marks a multi-line editor (Ctx.Editor): a click focuses it
	// and goes to its Press, under an I-beam.
	editor bool
}

// clickable reports whether the hit does something on a click (a hover
// area doesn't).
func (h Hit) clickable() bool { return h.OnClick != nil || h.OnClickAt != nil || h.OnDrag != nil }

// ScrollArea is a region the wheel scrolls.
type ScrollArea struct {
	ID  string
	R   RectF
	Max float32
	// View is the part where content is seen (under a caption bar, above
	// a docked bar): selecting text near its edges scrolls. Zero: R.
	View RectF
}

// Anchor is a subheading's label and window-coordinate Y recorded during a
// layout pass, used by the page scrollbar to place section jump dots.
type Anchor struct {
	Label string
	Y     float32
}

// Ctx is one layout pass.
type Ctx struct {
	Scale float32
	M     Metrics
	Now   time.Time
	// Interaction state, kept by the window between passes.
	Hover, Pressed string
	Scroll         map[string]float32
	// ScrollTo smoothly scrolls a scroll area to a target offset; SetScroll
	// jumps immediately and cancels any running smooth scroll (dragging).
	ScrollTo  func(id string, to float32)
	SetScroll func(id string, to float32)

	ops      []func(Canvas)
	overlays []func(*Ctx)
	hits     []Hit
	scrolls  []ScrollArea
	anchors  []Anchor
	clips    []RectF
	// Animating is set by nodes that change over time (a live dot, a
	// clock), asking the window to draw again soon (20 fps).
	Animating bool
	// AnimatingFast is set by short motion transitions (dock slide,
	// modal fade) that should run at the monitor's native refresh rate.
	AnimatingFast bool
	// live are the regions that change faster than that (DrawLive).
	live []liveArea
	// intoView is a node's wish to be scrolled into view (its top, in
	// window pixels); the Frame turns it into WantScroll for its page.
	intoView *float32
	// WantScroll, when set after a pass, is where the window should
	// scroll a scroll area to (smoothly).
	WantScroll *ScrollTarget
	// Sel is the window's text selection (nil: none), for Selectable;
	// texts are the pass's selectable text areas, in order.
	Sel   *Selection
	texts []textArea

	// Text editing, kept by the window: the field being edited, and how a
	// click on a field starts editing it (nil where nothing can be typed).
	Edit      *Editing
	StartEdit func(id, value string, maxLen int)
	inputs    []input
	wantEdit  *input // a field asking to be edited as it is shown

	// Focus is the multi-line editor that has the keyboard (its ID), kept
	// by the window; editors are this pass's (Ctx.Editor).
	Focus   string
	editors []EditorTarget
	// keepView is a range (window pixels) a node wants visible: the
	// caret being typed at; the Frame scrolls as little as needed.
	keepView *[2]float32
}

// Mods are the modifier keys held with a key.
type Mods struct{ Ctrl, Shift, Alt bool }

// EditorEnv is what a focused editor's keys may use from the window.
type EditorEnv struct {
	SetClipboard func(text, html string) // text, and HTML for rich paste ("" for none)
	Clipboard    func() string
	Blur         func() // let go of the keyboard
	// GoalX is where Up and Down aim, kept between them (negative: none,
	// the caret's own x).
	GoalX *float32
}

// EditorTarget is a multi-line editor in a pass: where it is, and what
// the window calls while it has the keyboard.
type EditorTarget struct {
	ID string
	R  RectF
	// Press: the button went down at (x, y), the nth click in a row;
	// Drag: the pointer moved there while held.
	Press func(x, y float32, shift bool, clicks int)
	Drag  func(x, y float32)
	// Key: a key went down (true: handled); Char: a character was typed.
	Key  func(vk uintptr, m Mods, env *EditorEnv) bool
	Char func(r rune, env *EditorEnv)
	// WantFocus asks for the keyboard (once, when shown on request).
	WantFocus bool
	// ExpandTop is the height (device pixels) this editor adds above its
	// text when focused (ToolbarOnFocus), so the host adjusts scroll by
	// +ExpandTop on focus and -ExpandTop on blur, extending the container
	// upward while keeping the text below completely still.
	ExpandTop float32
}

// Editor registers a multi-line editor: a click inside it focuses it.
// Hits registered after it with IDs starting with its ID and ":" (its
// checkboxes) count as part of it.
func (c *Ctx) Editor(t EditorTarget) {
	c.editors = append(c.editors, t)
	r := t.R
	if len(c.clips) > 0 {
		r = r.Intersect(c.clips[len(c.clips)-1])
	}
	if r.W() > 0 && r.H() > 0 {
		c.hits = append(c.hits, Hit{ID: t.ID, R: r, editor: true})
	}
}

// EditorTarget is editor id of this pass (tests drive it like the window).
func (c *Ctx) EditorTarget(id string) (EditorTarget, bool) { return c.editorByID(id) }

// editorByID is editor id of this pass.
func (c *Ctx) editorByID(id string) (EditorTarget, bool) {
	for _, t := range c.editors {
		if t.ID == id {
			return t, true
		}
	}
	return EditorTarget{}, false
}

// KeepInView asks for the page to scroll, as little as it takes, so that
// [top, bottom] (window pixels) is visible: the caret while typing.
func (c *Ctx) KeepInView(top, bottom float32) { c.keepView = &[2]float32{top, bottom} }

// NewCtx starts a pass.
func NewCtx(scale float32, m Metrics, now time.Time, hover, pressed string, scroll map[string]float32) *Ctx {
	if scroll == nil {
		scroll = map[string]float32{}
	}
	return &Ctx{Scale: scale, M: m, Now: now, Hover: hover, Pressed: pressed, Scroll: scroll}
}

// Px scales logical pixels to device pixels, rounded.
func (c *Ctx) Px(v float32) float32 { return float32(math.Round(float64(v * c.Scale))) }

// F scales a font.
func (c *Ctx) F(f Font) Font { f.Size *= c.Scale; return f }

// Draw records a drawing step.
func (c *Ctx) Draw(f func(Canvas)) { c.ops = append(c.ops, f) }

// Overlay defers fn until the end of the page layout pass so popovers paint
// and hit-test above all other page content.
func (c *Ctx) Overlay(fn func(*Ctx)) { c.overlays = append(c.overlays, fn) }

// FlushOverlays runs all deferred overlays in registration order.
func (c *Ctx) FlushOverlays() {
	for len(c.overlays) > 0 {
		ops := c.overlays
		c.overlays = nil
		for _, fn := range ops {
			fn(c)
		}
	}
}

// liveArea is a region the window repaints on its own, often.
type liveArea struct {
	r    RectF
	draw func(cv Canvas, now time.Time)
}

// DrawLive records a region that moves faster than the window repaints
// (the voice meter's bars): draw must paint all of r, opaque, from live
// data as of now. It is drawn with the rest, and besides that the window
// repaints just r about 60 times a second, without a layout pass, while
// it is on screen. r is snapped outwards to whole pixels.
func (c *Ctx) DrawLive(r RectF, draw func(cv Canvas, now time.Time)) {
	r = RectF{float32(math.Floor(float64(r.L))), float32(math.Floor(float64(r.T))), float32(math.Ceil(float64(r.R))), float32(math.Ceil(float64(r.B)))}
	if len(c.clips) > 0 {
		k := c.clips[len(c.clips)-1]
		if r.T < k.T || r.B > k.B || r.L < k.L || r.R > k.R {
			// Partly scrolled away: drawn with the rest, not on its own.
			c.Draw(func(cv Canvas) { draw(cv, c.Now) })
			return
		}
	}
	c.Draw(func(cv Canvas) {
		cv.PushClip(r)
		draw(cv, c.Now)
		cv.PopClip()
	})
	c.live = append(c.live, liveArea{r, draw})
}

// ScrollTarget is a scroll area and the offset to move it to.
type ScrollTarget struct {
	ID string
	To float32
}

// ScrollIntoView asks for the page to scroll so that top (window pixels,
// where a node starts) sits in the upper part of the view: a search result
// being shown. The last call of a pass wins.
func (c *Ctx) ScrollIntoView(top float32) { c.intoView = &top }

// ClipRect is the current clip: the visible part of the page while it is
// laid out (everything, outside any clip).
func (c *Ctx) ClipRect() RectF {
	if len(c.clips) == 0 {
		return RectF{-1e6, -1e6, 1e6, 1e6}
	}
	return c.clips[len(c.clips)-1]
}

// Click makes r clickable as id (inside the current clip only).
func (c *Ctx) Click(id string, r RectF, fn func()) {
	if len(c.clips) > 0 {
		r = r.Intersect(c.clips[len(c.clips)-1])
		if r.W() <= 0 || r.H() <= 0 {
			return
		}
	}
	c.hits = append(c.hits, Hit{ID: id, R: r, OnClick: fn})
}

// ClickAt is Click for targets that need the click's position (a seek
// bar).
func (c *Ctx) ClickAt(id string, r RectF, fn func(x, y float32)) {
	if len(c.clips) > 0 {
		r = r.Intersect(c.clips[len(c.clips)-1])
		if r.W() <= 0 || r.H() <= 0 {
			return
		}
	}
	c.hits = append(c.hits, Hit{ID: id, R: r, OnClickAt: fn})
}

// Drag makes r a target that follows the pointer while the button is held
// on it (see Hit.OnDrag).
func (c *Ctx) Drag(id string, r RectF, fn func(x, y float32, done bool)) {
	if len(c.clips) > 0 {
		r = r.Intersect(c.clips[len(c.clips)-1])
		if r.W() <= 0 || r.H() <= 0 {
			return
		}
	}
	c.hits = append(c.hits, Hit{ID: id, R: r, OnDrag: fn})
}

// HoverArea makes r report hover as id without being clickable (no hand
// cursor): a row that shows its actions while the pointer is on it.
// Register it before the row's buttons, so they stay on top.
func (c *Ctx) HoverArea(id string, r RectF) {
	if len(c.clips) > 0 {
		r = r.Intersect(c.clips[len(c.clips)-1])
		if r.W() <= 0 || r.H() <= 0 {
			return
		}
	}
	c.hits = append(c.hits, Hit{ID: id, R: r})
}

// Visible reports whether r is at least partly inside the current clip
// (everything is, without one), so long pages can skip what's scrolled
// away.
func (c *Ctx) Visible(r RectF) bool {
	if len(c.clips) == 0 {
		return true
	}
	k := c.clips[len(c.clips)-1]
	return r.B > k.T && r.T < k.B && r.R > k.L && r.L < k.R
}

// PushClip limits drawing and clicks to r.
func (c *Ctx) PushClip(r RectF) {
	if len(c.clips) > 0 {
		r = r.Intersect(c.clips[len(c.clips)-1])
	}
	c.clips = append(c.clips, r)
	c.Draw(func(cv Canvas) { cv.PushClip(r) })
}

// PopClip ends the last PushClip.
func (c *Ctx) PopClip() {
	c.clips = c.clips[:len(c.clips)-1]
	c.Draw(func(cv Canvas) { cv.PopClip() })
}

// AddScroll registers a scrollable region.
func (c *Ctx) AddScroll(a ScrollArea) { c.scrolls = append(c.scrolls, a) }

// AddAnchor records a page subheading at window-coordinate y for the
// scrollbar's section jump dots.
func (c *Ctx) AddAnchor(label string, y float32) {
	if label == "" {
		return
	}
	c.anchors = append(c.anchors, Anchor{Label: label, Y: y})
}

// Anchors returns the subheading anchors recorded during this layout pass.
func (c *Ctx) Anchors() []Anchor { return c.anchors }

// Paint replays the recorded drawing.
func (c *Ctx) Paint(cv Canvas) {
	for _, op := range c.ops {
		op(cv)
	}
}

// HitAt returns the topmost click target at (x, y).
func (c *Ctx) HitAt(x, y float32) (Hit, bool) {
	for i := len(c.hits) - 1; i >= 0; i-- {
		if c.hits[i].R.Contains(x, y) {
			return c.hits[i], true
		}
	}
	return Hit{}, false
}

// HitByID returns the click target id of this pass (a drag goes on with
// the latest layout's target).
func (c *Ctx) HitByID(id string) (Hit, bool) {
	for i := len(c.hits) - 1; i >= 0; i-- {
		if c.hits[i].ID == id {
			return c.hits[i], true
		}
	}
	return Hit{}, false
}

// ScrollArea returns the scroll area id of this pass.
func (c *Ctx) ScrollArea(id string) (ScrollArea, bool) {
	for _, a := range c.scrolls {
		if a.ID == id {
			return a, true
		}
	}
	return ScrollArea{}, false
}

// ScrollAt returns the innermost scroll area at (x, y).
func (c *Ctx) ScrollAt(x, y float32) (ScrollArea, bool) {
	for i := len(c.scrolls) - 1; i >= 0; i-- {
		if c.scrolls[i].R.Contains(x, y) {
			return c.scrolls[i], true
		}
	}
	return ScrollArea{}, false
}

// Hits returns the click targets (for tests).
func (c *Ctx) Hits() []Hit { return c.hits }

// Lay out a whole tree: Root lays out n over a w×h area.
func Root(c *Ctx, n Node, w, h float32) {
	n.Layout(c, 0, 0, w)
	c.FlushOverlays()
}
