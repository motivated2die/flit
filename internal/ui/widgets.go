package ui

import (
	"math"
	"strings"
	"time"
)

// Type and spacing of the window (logical pixels).
const (
	uiFont = "Segoe UI"
)

var (
	fontBody     = Font{Family: uiFont, Size: 13, Weight: 400}
	fontBodySemi = Font{Family: uiFont, Size: 13, Weight: 600}
	fontSmall    = Font{Family: uiFont, Size: 11.5, Weight: 400}
	fontCaption  = Font{Family: uiFont, Size: 11, Weight: 600}
	fontTitle    = Font{Family: uiFont, Size: 22, Weight: 600}
	fontLarge    = Font{Family: uiFont, Size: 15, Weight: 600}
	fontMono     = Font{Family: "Consolas", Size: 11, Weight: 400}
)

// Icons: Tabler icons embedded from icons/ (icon.go), by file name. Every
// "Glyph" field of a widget takes one of these.
const (
	GlyphHome      = "home"
	GlyphHistory   = "history"
	GlyphSettings  = "settings"
	GlyphFolder    = "folder"
	GlyphCopy      = "copy"
	GlyphMic       = "microphone"
	GlyphStop      = "player-stop"
	GlyphRecord    = "player-record-filled"
	GlyphDocument  = "file-text"
	GlyphBack      = "arrow-left"
	GlyphChevron   = "chevron-right"
	GlyphRefresh   = "refresh"
	GlyphCheck     = "check"
	GlyphWarning   = "alert-triangle"
	GlyphGlobe     = "world"
	GlyphEdit      = "pencil"
	GlyphCalendar  = "calendar"
	GlyphMeeting   = "calendar-event" // a meeting in a list
	GlyphOpenLink  = "external-link"
	GlyphPlay      = "player-play-filled"
	GlyphPause     = "player-pause-filled"
	GlyphDelete    = "trash"
	GlyphArticle    = "file-text" // a meeting with notes / Meetings nav
	GlyphFileSignal = "file-signal"
	GlyphFileTextAI = "file-text-ai"
	GlyphSearch    = "search"
	GlyphPeople    = "users"
	GlyphPower     = "power"
	GlyphUp        = "chevron-up"
	glyphPlayLine  = "player-play" // outline, for quiet buttons
	glyphPauseLine = "player-pause"
	GlyphDown      = "chevron-down"
	GlyphUpdate    = "download"
	GlyphNotes     = "notes"
	GlyphTask      = "square"
	GlyphTaskDone  = "square-check"
	GlyphBold      = "bold"
	GlyphItalic    = "italic"
	GlyphList      = "list"
	GlyphNumbered  = "list-numbers"
	GlyphH2        = "h-2"
	GlyphH3        = "h-3"
	GlyphAt        = "at"
	GlyphPin       = "pin"
	glyphMinimize  = "minus"
	glyphClose     = "x"
)

func col(c interface{ RGBA() (r, g, b, a uint32) }) ColorF { return RGBA(c, 1) }

// --- layout -------------------------------------------------------------

// VStack stacks nodes vertically with Gap between them.
type VStack struct {
	Gap   float32
	Items []Node
}

func (s VStack) Layout(c *Ctx, x, y, w float32) float32 {
	h := float32(0)
	first := true
	for _, it := range s.Items {
		if it == nil {
			continue
		}
		if !first {
			h += c.Px(s.Gap)
		}
		first = false
		h += it.Layout(c, x, y+h, w)
	}
	return h
}

// HStack places nodes side by side: Sized nodes at their width, the others
// sharing what is left. Right aligns a row of only Sized nodes to the right.
type HStack struct {
	Gap   float32
	Items []Node
	Right bool
}

func (s HStack) Layout(c *Ctx, x, y, w float32) float32 {
	var items []Node
	for _, it := range s.Items {
		if it != nil {
			items = append(items, it)
		}
	}
	gap := c.Px(s.Gap)
	fixed, flex := float32(0), 0
	widths := make([]float32, len(items))
	for i, it := range items {
		if sz, ok := it.(Sized); ok {
			widths[i] = sz.Width(c)
			fixed += widths[i]
		} else {
			flex++
		}
	}
	rest := w - fixed - gap*float32(max(0, len(items)-1))
	for i := range items {
		if _, ok := items[i].(Sized); !ok {
			widths[i] = max(0, rest/float32(flex))
		}
	}
	cx := x
	if s.Right && flex == 0 {
		cx = x + rest
	}
	h := float32(0)
	for i, it := range items {
		h = max(h, it.Layout(c, cx, y, widths[i]))
		cx += widths[i] + gap
	}
	return h
}

// Inset lays out a Sized node lower by Top (logical pixels), to line it
// up with taller neighbours in an HStack.
type Inset struct {
	Top   float32
	Child Node
}

func (i Inset) Width(c *Ctx) float32 {
	if sz, ok := i.Child.(Sized); ok {
		return sz.Width(c)
	}
	return 0
}

func (i Inset) Layout(c *Ctx, x, y, w float32) float32 {
	return c.Px(i.Top) + i.Child.Layout(c, x, y+c.Px(i.Top), w)
}

// Group lays out nodes one under the other with the same gap as the
// stack it sits in would (12), and is nothing when empty: a way to put an
// optional section into a list of nodes.
type Group []Node

func (g Group) Layout(c *Ctx, x, y, w float32) float32 {
	if len(g) == 0 {
		return 0
	}
	return VStack{Gap: 12, Items: g}.Layout(c, x, y, w)
}

// Space is empty vertical room.
type Space float32

func (s Space) Layout(c *Ctx, x, y, w float32) float32 { return c.Px(float32(s)) }

// --- text ---------------------------------------------------------------

// Title is a page's heading.
type Title string

func (t Title) Layout(c *Ctx, x, y, w float32) float32 {
	h := c.Px(34)
	r := RectF{x, y, x + w, y + h}
	c.Draw(func(cv Canvas) { cv.Text(string(t), r, c.F(fontTitle), col(ColText)) })
	return h
}

// Caption is a section's small heading.
type Caption string

func (t Caption) Layout(c *Ctx, x, y, w float32) float32 {
	h := c.Px(22)
	r := RectF{x + c.Px(2), y, x + w, y + h}
	c.Draw(func(cv Canvas) { cv.Text(strings.ToUpper(string(t)), r, c.F(fontCaption), col(ColFaint)) })
	return h
}

// Label is one line of text.
type Label struct {
	Text   string
	Muted  bool
	Strong bool
	Large  bool
	Right  bool
}

func (l Label) font() Font {
	switch {
	case l.Large:
		return fontLarge
	case l.Strong:
		return fontBodySemi
	}
	return fontBody
}

func (l Label) Width(c *Ctx) float32 { return c.M.TextWidth(l.Text, c.F(l.font())) + c.Px(2) }

func (l Label) Layout(c *Ctx, x, y, w float32) float32 {
	f := c.F(l.font())
	f.Right = l.Right
	h := c.Px(f.Size/c.Scale*1.6 + 2)
	color := col(ColText)
	if l.Muted {
		color = col(ColMuted)
	}
	r := RectF{x, y, x + w, y + h}
	c.Draw(func(cv Canvas) { cv.Text(l.Text, r, f, color) })
	return h
}

// Para is wrapped text; Mono sets it small in a fixed-width face (logs).
type Para struct {
	Text  string
	Muted bool
	Small bool
	Mono  bool
}

func (p Para) Layout(c *Ctx, x, y, w float32) float32 {
	f := fontBody
	switch {
	case p.Mono:
		f = fontMono
	case p.Small:
		f = fontSmall
	}
	f = c.F(f)
	h := c.M.ParagraphHeight(p.Text, f, w)
	color := col(ColText)
	if p.Muted {
		color = col(ColMuted)
	}
	r := RectF{x, y, x + w, y + h + 2}
	if c.Visible(r) {
		c.Draw(func(cv Canvas) { cv.Paragraph(p.Text, r, f, color) })
	}
	return h
}

// --- containers -----------------------------------------------------------

// Card groups nodes on a lighter rounded surface.
type Card struct {
	Items []Node
	Gap   float32
	// ID and OnClick, when set, make the whole card clickable (under its
	// own buttons), lighter while hovered: Home's recording card.
	ID      string
	OnClick func()
}

func (k Card) Layout(c *Ctx, x, y, w float32) float32 {
	pad := c.Px(16)
	// Draw the background first, sized after laying out the content.
	idx := len(c.ops)
	c.Draw(nil)
	hits := len(c.hits)
	h := VStack{Gap: k.Gap, Items: k.Items}.Layout(c, x+pad, y+pad, w-2*pad) + 2*pad
	r := RectF{x, y, x + w, y + h}
	radius := c.Px(10)
	bg := col(ColCard)
	if k.OnClick != nil {
		if c.Hover == k.ID {
			bg = col(ColCardHover)
		}
		hr := r
		if len(c.clips) > 0 {
			hr = r.Intersect(c.clips[len(c.clips)-1])
		}
		if hr.W() > 0 && hr.H() > 0 {
			// Under the content's own targets, which stay on top.
			c.hits = append(c.hits[:hits], append([]Hit{{ID: k.ID, R: hr, OnClick: k.OnClick}}, c.hits[hits:]...)...)
		}
	}
	c.ops[idx] = func(cv Canvas) { cv.RoundRect(r, radius, bg) }
	return h
}

// Divider is a thin line.
type Divider struct{}

func (Divider) Layout(c *Ctx, x, y, w float32) float32 {
	h := c.Px(9)
	t := max(1, c.Px(1))
	r := RectF{x, y + (h-t)/2, x + w, y + (h+t)/2}
	c.Draw(func(cv Canvas) { cv.RoundRect(r, 0, col(ColDivider)) })
	return h
}

// --- controls -------------------------------------------------------------

// Button is a pill with a label and an optional glyph. Primary buttons are
// white with dark text; others are grey.
type Button struct {
	ID       string
	Label    string
	Glyph    string
	Primary  bool
	Disabled bool
	Large    bool // a page's main action: taller, larger text
	Small    bool // a chip: as low as the sidebar's update pill
	Danger   bool // destructive: red text (red fill while hovered)
	// HoverDanger stays neutral until hovered, then turns red (Quit).
	HoverDanger bool
	OnClick     func()
}

func (b Button) font() Font {
	f := fontBodySemi
	switch {
	case b.Large:
		f.Size = 14
	case b.Small:
		f.Size = 12
	}
	return f
}

func (b Button) Width(c *Ctx) float32 {
	if b.iconOnly() {
		return b.height(c) // round
	}
	pad := c.Px(16)
	switch {
	case b.Large:
		pad = c.Px(22)
	case b.Small:
		pad = c.Px(11)
	}
	w := c.M.TextWidth(b.Label, c.F(b.font())) + 2*pad
	if b.Glyph != "" {
		w += c.Px(22)
		if b.Small {
			w -= c.Px(4)
		}
	}
	return w
}

// iconOnly: a glyph without a label makes a round button.
func (b Button) iconOnly() bool { return b.Label == "" && b.Glyph != "" }

func (b Button) height(c *Ctx) float32 {
	switch {
	case b.Large:
		return c.Px(40)
	case b.Small:
		return c.Px(smallButtonH)
	}
	return c.Px(32)
}

// smallButtonH is a chip's height (logical pixels), a little above the
// sidebar's update pill.
const smallButtonH = 22

func (b Button) Layout(c *Ctx, x, y, w float32) float32 {
	h := b.height(c)
	if b.iconOnly() {
		w = h
	}
	r := RectF{x, y, x + w, y + h}
	fill, text := col(ColControl), col(ColText)
	switch {
	case b.Disabled:
		fill, text = RGBA(ColControl, 0.5), col(ColFaint)
	case b.Primary && c.Hover == b.ID:
		fill, text = col(rgb(0xDC, 0xDC, 0xE0)), col(ColInk)
	case b.Primary:
		fill, text = col(ColText), col(ColInk)
	case (b.Danger || b.HoverDanger) && c.Hover == b.ID:
		fill, text = col(ColLive), col(ColText)
	case b.Danger:
		text = col(ColLive)
	case c.Hover == b.ID:
		fill = col(ColControlHi)
	}
	if c.Pressed == b.ID && !b.Disabled {
		r = r.Inset(c.Px(1))
	}
	gr := RectF{}
	tr := RectF{r.L, r.T, r.R, r.B}
	if b.iconOnly() {
		gr = r
	} else if b.Glyph != "" {
		gx := r.L + c.Px(14)
		switch {
		case b.Large:
			gx = r.L + c.Px(18)
		case b.Small:
			gx = r.L + c.Px(9)
		}
		gw := c.Px(16)
		if b.Small {
			gw = c.Px(13)
		}
		gr = RectF{gx, r.T, gx + gw, r.B}
		tr.L = gr.R + c.Px(5)
		tr.R = r.R - c.Px(10)
	}
	label := c.F(b.font())
	label.Center = b.Glyph == ""
	c.Draw(func(cv Canvas) {
		cv.RoundRect(r, r.H()/2, fill)
		if b.Glyph != "" {
			size := c.Px(16)
			if b.Small {
				size = c.Px(13)
			}
			DrawIcon(cv, b.Glyph, gr, size, text)
		}
		if b.Label != "" {
			cv.Text(b.Label, tr, label, text)
		}
	})
	if !b.Disabled && b.OnClick != nil {
		c.Click(b.ID, r, b.OnClick)
	}
	return h
}

// Toggle is a row with a label (and detail) and a switch; the whole row is
// clickable.
type Toggle struct {
	ID       string
	Label    string
	Detail   string
	On       bool
	OnChange func(on bool)
}

func (t Toggle) Layout(c *Ctx, x, y, w float32) float32 {
	h := c.Px(40)
	if t.Detail != "" {
		h = c.Px(52)
	}
	r := RectF{x - c.Px(8), y, x + w + c.Px(8), y + h}
	sw, sh := c.Px(36), c.Px(20)
	track := RectF{x + w - sw, y + (h-sh)/2, x + w, y + (h+sh)/2}
	hover := c.Hover == t.ID
	trackCol, knobCol := col(ColControl), col(ColMuted)
	if t.On {
		trackCol, knobCol = col(ColText), col(ColInk)
	}
	if hover && !t.On {
		trackCol = col(ColControlHi)
	}
	kr := sh/2 - c.Px(4)
	kx := track.L + sh/2
	if t.On {
		kx = track.R - sh/2
	}
	lines := []struct {
		s string
		f Font
		c ColorF
	}{{t.Label, c.F(fontBody), col(ColText)}}
	if t.Detail != "" {
		lines = append(lines, struct {
			s string
			f Font
			c ColorF
		}{t.Detail, c.F(fontSmall), col(ColMuted)})
	}
	c.Draw(func(cv Canvas) {
		if hover {
			cv.RoundRect(r, c.Px(8), col(ColCardHover))
		}
		if len(lines) == 1 {
			cv.Text(lines[0].s, RectF{x, y, track.L - c.Px(12), y + h}, lines[0].f, lines[0].c)
		} else {
			mid := y + h/2
			cv.Text(lines[0].s, RectF{x, mid - c.Px(19), track.L - c.Px(12), mid + c.Px(1)}, lines[0].f, lines[0].c)
			cv.Text(lines[1].s, RectF{x, mid + c.Px(1), track.L - c.Px(12), mid + c.Px(19)}, lines[1].f, lines[1].c)
		}
		cv.RoundRect(track, sh/2, trackCol)
		cv.Circle(kx, (track.T+track.B)/2, kr, knobCol)
	})
	if t.OnChange != nil {
		on := t.On
		c.Click(t.ID, r, func() { t.OnChange(!on) })
	}
	return h
}

// Segmented is a choice between a few options, as one pill.
type Segmented struct {
	ID       string
	Options  []string
	Selected int
	OnSelect func(i int)
}

func (s Segmented) segWidths(c *Ctx) []float32 {
	out := make([]float32, len(s.Options))
	for i, o := range s.Options {
		out[i] = c.M.TextWidth(o, c.F(fontBodySemi)) + 2*c.Px(16)
	}
	return out
}

func (s Segmented) Width(c *Ctx) float32 {
	w := c.Px(6)
	for _, v := range s.segWidths(c) {
		w += v
	}
	return w
}

func (s Segmented) Layout(c *Ctx, x, y, w float32) float32 {
	h := c.Px(34)
	pad := c.Px(3)
	outer := RectF{x, y, x + s.Width(c), y + h}
	c.Draw(func(cv Canvas) { cv.RoundRect(outer, h/2, col(ColControl)) })
	cx := x + pad
	f := c.F(fontBodySemi)
	f.Center = true
	for i, sw := range s.segWidths(c) {
		r := RectF{cx, y + pad, cx + sw, y + h - pad}
		id := s.ID + ":" + s.Options[i]
		selected := i == s.Selected
		hover := c.Hover == id
		label := s.Options[i]
		c.Draw(func(cv Canvas) {
			switch {
			case selected:
				cv.RoundRect(r, r.H()/2, col(ColText))
				cv.Text(label, r, f, col(ColInk))
			case hover:
				cv.RoundRect(r, r.H()/2, col(ColControlHi))
				cv.Text(label, r, f, col(ColText))
			default:
				cv.Text(label, r, f, col(ColMuted))
			}
		})
		if !selected && s.OnSelect != nil {
			i := i
			c.Click(id, r, func() { s.OnSelect(i) })
		}
		cx += sw
	}
	return h
}

// StepSlider is a compact notched slider with discrete labelled stops
// (e.g. "80%", "100%", "110%", "125%"). Clicking or dragging snaps to the
// nearest notch; OnChange receives the snapped index and whether the drag
// finished (true on release or click).
type StepSlider struct {
	ID       string
	Labels   []string
	Selected int
	OnChange func(i int, done bool)
}

func (s StepSlider) Width(c *Ctx) float32 { return c.Px(216) }

func (s StepSlider) Layout(c *Ctx, x, y, w float32) float32 {
	h := c.Px(36)
	n := len(s.Labels)
	if n < 2 {
		return h
	}
	sel := max(0, min(s.Selected, n-1))
	sw := s.Width(c)
	outer := RectF{x, y, x + sw, y + h}
	padX := c.Px(18)
	trackL, trackR := x+padX, x+sw-padX
	trackY := y + c.Px(10)
	trackH := c.Px(4)
	tickW := max(1, c.Px(2))
	tickH := c.Px(8)
	hover := c.Hover == s.ID || c.Pressed == s.ID
	knobR := c.Px(6.5)
	if hover {
		knobR = c.Px(7.5)
	}
	stopX := func(i int) float32 {
		return trackL + (trackR-trackL)*float32(i)/float32(n-1)
	}
	knobX := stopX(sel)
	labels := s.Labels
	c.Draw(func(cv Canvas) {
		cv.RoundRect(RectF{trackL, trackY - trackH/2, trackR, trackY + trackH/2}, trackH/2, col(ColControl))
		if knobX > trackL {
			cv.RoundRect(RectF{trackL, trackY - trackH/2, knobX, trackY + trackH/2}, trackH/2, col(ColText))
		}
		for i := range n {
			sx := stopX(i)
			tc := col(ColControlHi)
			if i <= sel {
				tc = col(ColText)
			}
			cv.RoundRect(RectF{sx - tickW/2, trackY - tickH/2, sx + tickW/2, trackY + tickH/2}, tickW/2, tc)
		}
		cv.Circle(knobX, trackY, knobR, col(ColText))
		lf := c.F(Font{Family: uiFont, Size: 10.5, Weight: 400, Center: true})
		lfSel := c.F(Font{Family: uiFont, Size: 10.5, Weight: 600, Center: true})
		for i, lbl := range labels {
			sx := stopX(i)
			colLbl, fn := col(ColFaint), lf
			if i == sel {
				colLbl, fn = col(ColText), lfSel
			} else if hover {
				colLbl = col(ColMuted)
			}
			half := c.Px(22)
			cv.Text(lbl, RectF{sx - half, y + c.Px(19), sx + half, y + h}, fn, colLbl)
		}
	})
	if s.OnChange != nil {
		onChange := s.OnChange
		c.Drag(s.ID, outer, func(px, _ float32, done bool) {
			t := max(0, min(1, (px-trackL)/(trackR-trackL)))
			idx := int(math.Round(float64(t) * float64(n-1)))
			idx = max(0, min(idx, n-1))
			onChange(idx, done)
		})
	}
	return h
}

// ListRow is a clickable line in a list: a glyph (or a live red dot), a
// title with a muted detail under it, and text on the right.
type ListRow struct {
	ID       string
	Glyph    string
	Live     bool
	Title    string
	Detail   string
	Trailing string
	OnClick  func()
}

func (l ListRow) Layout(c *Ctx, x, y, w float32) float32 {
	h := c.Px(56)
	r := RectF{x, y, x + w, y + h}
	hover := c.Hover == l.ID && l.OnClick != nil
	ix := x + c.Px(14)
	icon := RectF{ix, y, ix + c.Px(20), y + h}
	tx := icon.R + c.Px(12)
	tw := c.M.TextWidth(l.Trailing, c.F(fontSmall))
	trail := RectF{x + w - c.Px(34) - tw, y, x + w - c.Px(34), y + h}
	chev := RectF{x + w - c.Px(30), y, x + w - c.Px(10), y + h}
	mid := y + h/2
	if l.Live {
		c.Animating = true
	}
	pulse := float32(Pulse(time.Duration(c.Now.UnixNano()), notchPulse))
	c.Draw(func(cv Canvas) {
		if hover {
			cv.RoundRect(r, c.Px(8), col(ColCardHover))
		}
		if l.Live {
			cx, cy := (icon.L+icon.R)/2, mid
			cv.Circle(cx, cy, c.Px(8)*(0.85+0.3*pulse), RGBA(ColLive, 0.22*pulse))
			cv.Circle(cx, cy, c.Px(5), col(ColLive))
		} else if l.Glyph != "" {
			DrawIcon(cv, l.Glyph, icon, c.Px(18), col(ColMuted))
		}
		cv.Text(l.Title, RectF{tx, mid - c.Px(20), trail.L - c.Px(10), mid}, c.F(fontBodySemi), col(ColText))
		cv.Text(l.Detail, RectF{tx, mid, trail.L - c.Px(10), mid + c.Px(18)}, c.F(fontSmall), col(ColMuted))
		cv.Text(l.Trailing, trail, c.F(fontSmall), col(ColMuted))
		if l.OnClick != nil {
			DrawIcon(cv, GlyphChevron, chev, c.Px(14), col(ColFaint))
		}
	})
	if l.OnClick != nil {
		c.Click(l.ID, r, l.OnClick)
	}
	return h
}

// Progress is a thin bar.
type Progress struct{ Value float64 }

func (p Progress) Layout(c *Ctx, x, y, w float32) float32 {
	h := c.Px(6)
	r := RectF{x, y, x + w, y + h}
	v := float32(math.Max(0, math.Min(1, p.Value)))
	fill := RectF{x, y, x + max(h, w*v), y + h}
	c.Draw(func(cv Canvas) {
		cv.RoundRect(r, h/2, col(ColControl))
		if v > 0 {
			cv.RoundRect(fill, h/2, col(ColText))
		}
	})
	return h
}

// LiveTitle is a large line with a pulsing red dot in front: the sign that
// flit is recording.
type LiveTitle struct {
	Text   string
	Detail string // muted, after the text (the clock)
}

func (l LiveTitle) Layout(c *Ctx, x, y, w float32) float32 {
	h := c.Px(30)
	c.Animating = true
	pulse := float32(Pulse(time.Duration(c.Now.UnixNano()), notchPulse))
	cx, cy := x+c.Px(7), y+h/2
	tx := x + c.Px(22)
	tw := c.M.TextWidth(l.Text, c.F(fontLarge))
	c.Draw(func(cv Canvas) {
		cv.Circle(cx, cy, c.Px(9)*(0.8+0.3*pulse), RGBA(ColLive, 0.22*pulse))
		cv.Circle(cx, cy, c.Px(5), col(ColLive))
		cv.Text(l.Text, RectF{tx, y, tx + tw + c.Px(2), y + h}, c.F(fontLarge), col(ColText))
		cv.Text(l.Detail, RectF{tx + tw + c.Px(10), y, x + w, y + h}, c.F(fontLarge), col(ColMuted))
	})
	return h
}

// Link is a small clickable text with a glyph (back, open a folder).
type Link struct {
	ID      string
	Glyph   string
	Label   string
	OnClick func()
}

func (l Link) Width(c *Ctx) float32 {
	return c.M.TextWidth(l.Label, c.F(fontBody)) + c.Px(26)
}

func (l Link) Layout(c *Ctx, x, y, w float32) float32 {
	h := c.Px(28)
	r := RectF{x, y, x + l.Width(c), y + h}
	color := col(ColMuted)
	if c.Hover == l.ID {
		color = col(ColText)
	}
	c.Draw(func(cv Canvas) {
		DrawIcon(cv, l.Glyph, RectF{r.L, r.T, r.L + c.Px(16), r.B}, c.Px(15), color)
		cv.Text(l.Label, RectF{r.L + c.Px(22), r.T, r.R, r.B}, c.F(fontBody), color)
	})
	if l.OnClick != nil {
		c.Click(l.ID, r, l.OnClick)
	}
	return h
}

// SettingRow is a label (with an optional detail under it) on the left and
// a control on the right.
type SettingRow struct {
	Label   string
	Detail  string
	Control Node // ideally Sized; laid out at its width on the right
}

func (s SettingRow) Layout(c *Ctx, x, y, w float32) float32 {
	cw := w / 2
	if sz, ok := s.Control.(Sized); ok {
		cw = sz.Width(c)
	}
	h := c.Px(44)
	if s.Detail != "" {
		h = c.Px(52)
	}
	ch := c.Px(34)
	if s.Control != nil {
		ch = s.Control.Layout(c, x+w-cw, y+(h-c.Px(34))/2, cw)
	}
	_ = ch
	tr := x + w - cw - c.Px(12)
	mid := y + h/2
	c.Draw(func(cv Canvas) {
		if s.Detail == "" {
			cv.Text(s.Label, RectF{x, y, tr, y + h}, c.F(fontBody), col(ColText))
			return
		}
		cv.Text(s.Label, RectF{x, mid - c.Px(19), tr, mid + c.Px(1)}, c.F(fontBody), col(ColText))
		cv.Text(s.Detail, RectF{x, mid + c.Px(1), tr, mid + c.Px(19)}, c.F(fontSmall), col(ColMuted))
	})
	return h
}

// ChoiceRow is one option of a list where one is chosen (a device): a
// radio mark, a title with a muted detail, and optionally a live level bar.
type ChoiceRow struct {
	ID       string
	Title    string
	Detail   string
	Selected bool
	Level    float32 // 0..1 on the meter scale; negative: no meter
	OnClick  func()
}

func (r ChoiceRow) Layout(c *Ctx, x, y, w float32) float32 {
	h := c.Px(50)
	if r.Detail == "" {
		h = c.Px(42)
	}
	row := RectF{x - c.Px(8), y, x + w + c.Px(8), y + h}
	hover := c.Hover == r.ID && r.OnClick != nil
	mid := y + h/2
	rad := c.Px(8)
	cx := x + rad
	meterW := float32(0)
	if r.Level >= 0 {
		meterW = c.Px(110)
		c.Animating = true
	}
	tx := cx + rad + c.Px(12)
	tr := x + w - meterW - c.Px(12)
	bh := c.Px(6)
	bar := RectF{x + w - meterW, mid - bh/2, x + w, mid + bh/2}
	lv := max(0, min(1, r.Level))
	c.Draw(func(cv Canvas) {
		if hover {
			cv.RoundRect(row, c.Px(8), col(ColCardHover))
		}
		if r.Selected {
			cv.Circle(cx, mid, rad, col(ColText))
			cv.Circle(cx, mid, rad*0.42, col(ColInk))
		} else {
			cv.Circle(cx, mid, rad, col(ColControlHi))
			cv.Circle(cx, mid, rad-max(1.5, c.Px(1.5)), col(ColCard))
		}
		if r.Detail == "" {
			cv.Text(r.Title, RectF{tx, y, tr, y + h}, c.F(fontBody), col(ColText))
		} else {
			cv.Text(r.Title, RectF{tx, mid - c.Px(19), tr, mid + c.Px(1)}, c.F(fontBody), col(ColText))
			cv.Text(r.Detail, RectF{tx, mid + c.Px(1), tr, mid + c.Px(19)}, c.F(fontSmall), col(ColMuted))
		}
		if meterW > 0 {
			cv.RoundRect(bar, bh/2, col(ColControl))
			if lv > 0 {
				cv.RoundRect(RectF{bar.L, bar.T, bar.L + max(bh, bar.W()*lv), bar.B}, bh/2, col(ColText))
			}
		}
	})
	if r.OnClick != nil {
		c.Click(r.ID, row, r.OnClick)
	}
	return h
}
