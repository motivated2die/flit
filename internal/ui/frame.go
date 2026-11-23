package ui

import (
	"fmt"
	"image/color"
	"math"
	"time"
)

// NavEntry is a page in the sidebar.
type NavEntry struct {
	ID    string
	Label string
	Glyph string
}

// Frame is the window's layout: a sidebar with the mark, the navigation
// and a status line at the bottom, and a scrolling page beside it.
type Frame struct {
	Nav      []NavEntry
	Selected string
	OnNav    func(id string)
	Page     Node
	PageID   string // keeps each page's scroll position apart

	// Dock floats in a pill at the bottom of the page area, the page
	// column's width, over a fade of the page (a recording's player); it
	// doesn't scroll. It slides up when DockID changes; DockAt is when
	// that was (the Window keeps it; zero: shown at once). DockOut slides
	// it back down instead: the page it belonged to was left.
	Dock    Node
	DockID  string
	DockAt  time.Time
	DockOut bool

	// Modal, when set, is a popover in the middle of the window (a
	// question that needs an answer): the rest dims and takes no clicks
	// until it closes. ModalAt is when it opened (the Window keeps it;
	// zero: shown at once); Esc closes it through Window.OnEscape.
	Modal   Node
	ModalAt time.Time

	// Status is shown at the bottom of the sidebar; Live puts the red dot
	// before it (recording).
	Status string
	Live   bool

	// Update, when set, is a slim white pill above the debug lines ("Update
	// available"); clicking it calls OnUpdate.
	Update   string
	OnUpdate func()

	// Debug lines sit just above the status line (the loaded model and its
	// memory); clicking them calls OnDebug.
	Debug   []string
	OnDebug func()
	// DebugCores, while transcribing, are the cores the engine keeps
	// busy (0..1 each, busiest first, at most 8): small squares beside
	// the debug lines, filled from the top by their use.
	DebugCores []float32
	// DebugGPU, when DebugGPUShown, is how busy the graphics processor is
	// for the work (0..1): one square two squares tall right of the cores.
	DebugGPU      float32
	DebugGPUShown bool

	// Caption draws minimise and close at the top right, for a window
	// without a native title bar (the Window sets these).
	Caption    bool
	OnMinimize func()
	OnClose    func()
}

// FrameMaxW is the widest a frame's content gets (logical pixels): the
// sidebar and a page at its widest with its margins. The window is never
// wider (its client area).
const FrameMaxW = sidebarW + 2*pagePad + pageMaxW

// Window geometry (logical pixels).
const (
	sidebarW     = 208
	pagePad      = 32
	pageMaxW     = 720
	navItemH     = 38
	scrollStep   = 56
	scrollbarW   = 4
	brandH       = 64
	statusLineH  = 44
	brandFeather = 22
	// CaptionH is the drawn title bar's height: the strip the window can be
	// dragged by, and the caption buttons' height.
	CaptionH       = 32
	captionButtonW = 46
)

// FrameScrollID is the scroll state key of a page.
func FrameScrollID(page string) string { return "page:" + page }

// Layout fills a w×h client area.
func (f Frame) Layout(c *Ctx, w, h float32) {
	side := c.Px(sidebarW)
	c.Draw(func(cv Canvas) {
		cv.RoundRect(RectF{0, 0, side, h}, 0, col(ColSidebar))
		cv.RoundRect(RectF{side - max(1, c.Px(1)), 0, side, h}, 0, col(ColDivider))
	})
	if f.Live {
		// The recording dot's corner glows, breathing with the dot.
		pulse := float32(Pulse(time.Duration(c.Now.UnixNano()), notchPulse))
		c.PushClip(RectF{0, 0, side - max(1, c.Px(1)), h})
		c.Glow(0, h, liveGlow, ColLive, 0.75+0.25*pulse)
		c.PopClip()
	}
	f.brand(c, side)
	f.nav(c, side)
	f.status(c, side, h)
	f.debug(c, side, h)
	f.update(c, h)
	f.page(c, side, w, h)
	if f.Modal != nil {
		f.modal(c, w, h)
	}
	if f.Caption {
		f.caption(c, w) // last: its buttons win over the page beneath
	}
}

// The modal popover (logical pixels).
const (
	modalW      = 460
	modalPad    = 24
	modalRadius = 14
	modalFade   = 160 // ms, the backdrop dimming and the card rising
)

// modal dims the window and lays out f.Modal on a card in its middle.
// The backdrop is a hover area over everything beneath: nothing there
// can be clicked or hovered while the popover is open.
func (f Frame) modal(c *Ctx, w, h float32) {
	e := float32(1)
	if !f.ModalAt.IsZero() {
		t := float64(c.Now.Sub(f.ModalAt)) / float64(modalFade*time.Millisecond)
		if t < 1 {
			c.Animating = true
		}
		e = float32(EaseOut(max(0, min(1, t))))
	}
	all := RectF{0, 0, w, h}
	c.Draw(func(cv Canvas) { cv.RoundRect(all, 0, ColorF{0, 0, 0, 0.55 * e}) })
	c.HoverArea("modal:backdrop", all)

	pad := c.Px(modalPad)
	mw := min(c.Px(modalW), w-2*c.Px(pagePad))
	probe := NewCtx(c.Scale, c.M, c.Now, "", "", nil)
	mh := f.Modal.Layout(probe, 0, 0, mw-2*pad) + 2*pad
	x := float32(math.Round(float64((w - mw) / 2)))
	y := float32(math.Round(float64((h-mh)/2 + c.Px(10)*(1-e))))
	card := RectF{x, y, x + mw, y + mh}
	edge := max(1, c.Px(1))
	radius := c.Px(modalRadius)
	c.Draw(func(cv Canvas) {
		// A soft shadow, then the outlined card.
		for i, a := range []float32{0.10, 0.08, 0.06} {
			d := c.Px(float32(4 + 4*i))
			cv.RoundRect(RectF{card.L - d/2, card.T, card.R + d/2, card.B + d}, radius+d/2, ColorF{0, 0, 0, a * e})
		}
		cv.RoundRect(card, radius, col(ColEdge))
		cv.RoundRect(card.Inset(edge), radius-edge, col(ColCard))
	})
	c.HoverArea("modal", card)
	f.Modal.Layout(c, x+pad, y+pad, mw-2*pad)
}

// caption draws the minimise and close buttons in the top right corner,
// Windows-sized (46×32), close turning red on hover.
func (f Frame) caption(c *Ctx, w float32) {
	bw, bh := c.Px(captionButtonW), c.Px(CaptionH)
	icon := c.Px(16)
	button := func(id, glyph string, r RectF, danger bool, onClick func()) {
		hover, pressed := c.Hover == id, c.Pressed == id
		c.Draw(func(cv Canvas) {
			fg := col(ColMuted)
			switch {
			case danger && pressed:
				cv.RoundRect(r, 0, RGBA(ColLive, 0.8))
				fg = col(ColText)
			case danger && hover:
				cv.RoundRect(r, 0, col(ColLive))
				fg = col(ColText)
			case pressed:
				cv.RoundRect(r, 0, col(ColControl))
				fg = col(ColText)
			case hover:
				cv.RoundRect(r, 0, col(ColCard))
				fg = col(ColText)
			}
			DrawIcon(cv, glyph, r, icon, fg)
		})
		if onClick != nil {
			c.Click(id, r, onClick)
		}
	}
	button("caption:min", glyphMinimize, RectF{w - 2*bw, 0, w - bw, bh}, false, f.OnMinimize)
	button("caption:close", glyphClose, RectF{w - bw, 0, w, bh}, true, f.OnClose)
}

func (f Frame) brand(c *Ctx, side float32) {
	s := c.Px(brandFeather)
	x, y := c.Px(20), (c.Px(brandH)-s)/2
	fill, cuts := FeatherPolys(s, x, y)
	tx := x + s + c.Px(10)
	c.Draw(func(cv Canvas) {
		cv.Polygons(fill, col(ColText))
		cv.Polygons(cuts, col(ColSidebar))
		cv.Text("flit", RectF{tx, 0, side, c.Px(brandH)}, c.F(Font{Family: uiFont, Size: 17, Weight: 600}), col(ColText))
	})
}

func (f Frame) nav(c *Ctx, side float32) {
	y := c.Px(brandH + 8)
	for _, e := range f.Nav {
		r := RectF{c.Px(10), y, side - c.Px(10), y + c.Px(navItemH)}
		selected := e.ID == f.Selected
		hover := c.Hover == "nav:"+e.ID
		glyph := RectF{r.L + c.Px(12), r.T, r.L + c.Px(32), r.B}
		label := RectF{glyph.R + c.Px(10), r.T, r.R - c.Px(8), r.B}
		e := e
		c.Draw(func(cv Canvas) {
			text := col(ColMuted)
			switch {
			case selected:
				cv.RoundRect(r, c.Px(8), col(ColCard))
				text = col(ColText)
			case hover:
				cv.RoundRect(r, c.Px(8), RGBA(ColCard, 0.6))
				text = col(ColText)
			}
			DrawIcon(cv, e.Glyph, glyph, c.Px(18), text)
			lf := fontBody
			if selected {
				lf = fontBodySemi
			}
			cv.Text(e.Label, label, c.F(lf), text)
		})
		if f.OnNav != nil {
			c.Click("nav:"+e.ID, r, func() { f.OnNav(e.ID) })
		}
		y += c.Px(navItemH + 2)
	}
}

func (f Frame) status(c *Ctx, side, h float32) {
	if f.Status == "" {
		return
	}
	y := h - c.Px(statusLineH)
	x := c.Px(22)
	r := RectF{x, y, side - c.Px(12), h - c.Px(8)}
	if f.Live {
		c.Animating = true
	}
	pulse := float32(Pulse(time.Duration(c.Now.UnixNano()), notchPulse))
	c.Draw(func(cv Canvas) {
		cv.RoundRect(RectF{c.Px(16), y - c.Px(1), side - c.Px(16), y}, 0, col(ColDivider))
		tx := r.L
		if f.Live {
			cy := (r.T + r.B) / 2
			cv.Circle(x+c.Px(4), cy, c.Px(4)*(0.9+0.25*pulse), col(ColLive))
			tx += c.Px(14)
		}
		color := col(ColMuted)
		if f.Live {
			color = col(ColText)
		}
		cv.Text(f.Status, RectF{tx, r.T, r.R, r.B}, c.F(fontSmall), color)
	})
}

// debugLineH is the height of one debug line (logical pixels).
const debugLineH = 16

// The core squares beside the debug lines (logical pixels).
const (
	coreSquare = 10
	coreGap    = 3
)

func (f Frame) debug(c *Ctx, side, h float32) {
	if len(f.Debug) == 0 {
		return
	}
	bottom := h - c.Px(statusLineH) - c.Px(6)
	r := RectF{c.Px(10), bottom - c.Px(12+debugLineH*float32(len(f.Debug))), side - c.Px(10), bottom}
	hover := c.Hover == "debug"
	font := c.F(fontSmall)
	textR := r.R - c.Px(8)
	cores := f.DebugCores
	if len(cores) > 8 {
		cores = cores[:8]
	}
	// The cores' grid, and the GPU's tall square at its right.
	var grid, gpu RectF
	if len(cores) > 0 || f.DebugGPUShown {
		cols := (len(cores) + 1) / 2
		gw := float32(cols)*c.Px(coreSquare) + float32(max(0, cols-1))*c.Px(coreGap)
		gh := 2*c.Px(coreSquare) + c.Px(coreGap)
		mid := (r.T + r.B) / 2
		right := r.R - c.Px(10)
		if f.DebugGPUShown {
			gpu = RectF{right - c.Px(coreSquare), mid - gh/2, right, mid + gh/2}
			right = gpu.L
			if cols > 0 {
				right -= c.Px(coreGap)
			}
		}
		grid = RectF{right - gw, mid - gh/2, right, mid + gh/2}
		textR = grid.L - c.Px(6) // with no cores: the GPU square's left
	}
	c.Draw(func(cv Canvas) {
		if hover {
			cv.RoundRect(r, c.Px(8), RGBA(ColCard, 0.6))
		}
		y := r.T + c.Px(6)
		for i, line := range f.Debug {
			color := col(ColFaint)
			if i == 0 || hover {
				color = col(ColMuted)
			}
			cv.Text(line, RectF{r.L + c.Px(12), y, textR, y + c.Px(debugLineH)}, font, color)
			y += c.Px(debugLineH)
		}
		sq, gap, rad := c.Px(coreSquare), c.Px(coreGap), c.Px(2)
		for i, use := range cores {
			// Busiest at the top right, then column by column leftwards.
			col, row := i/2, i%2
			x := grid.R - float32(col+1)*sq - float32(col)*gap
			yy := grid.T + float32(row)*(sq+gap)
			a := float32(1)
			if !hover { // a 2×2 in full; the columns beyond fade out leftwards
				a = []float32{1, 1, 0.45, 0.18}[col]
			}
			meterBox(cv, RectF{x, yy, x + sq, yy + sq}, use, rad, a)
		}
		if f.DebugGPUShown {
			meterBox(cv, gpu, f.DebugGPU, rad, 1)
		}
	})
	if f.OnDebug != nil {
		c.Click("debug", r, f.OnDebug)
	}
}

// meterBox is one of the debug squares: a faint box filled from the top
// down by use (0..1), at opacity a.
func meterBox(cv Canvas, box RectF, use, rad, a float32) {
	cv.RoundRect(box, rad, RGBA(ColText, 0.16*a))
	if fill := max(0, min(1, use)) * box.H(); fill > 0 {
		cv.PushClip(RectF{box.L, box.T, box.R, box.T + fill})
		cv.RoundRect(box, rad, RGBA(ColText, 0.43*a))
		cv.PopClip()
	}
}

// The update pill (logical pixels): about half a navigation row tall.
const (
	updatePillH   = 20
	updatePillPad = 10
	updatePillDot = 6
)

// update draws the update pill above the debug lines (or the status line
// when there are none), left-aligned with their text: white, fully
// rounded, a dark dot and the label in dark ink.
func (f Frame) update(c *Ctx, h float32) {
	if f.Update == "" {
		return
	}
	bottom := h - c.Px(statusLineH) - c.Px(6)
	if len(f.Debug) > 0 {
		bottom -= c.Px(12 + debugLineH*float32(len(f.Debug)))
	}
	font := c.F(fontCaption)
	pad, dot := c.Px(updatePillPad), c.Px(updatePillDot)
	tw := c.M.TextWidth(f.Update, font)
	x, ph := c.Px(22), c.Px(updatePillH)
	r := RectF{x, bottom - c.Px(6) - ph, x + pad + dot + c.Px(6) + tw + pad, bottom - c.Px(6)}
	hover, pressed := c.Hover == "update", c.Pressed == "update"
	c.Draw(func(cv Canvas) {
		bg := col(ColText)
		switch {
		case pressed:
			bg = RGBA(ColText, 0.78)
		case hover:
			bg = RGBA(ColText, 0.9)
		}
		cv.RoundRect(r, ph/2, bg)
		cy := (r.T + r.B) / 2
		dx := r.L + pad
		cv.RoundRect(RectF{dx, cy - dot/2, dx + dot, cy + dot/2}, dot/2, col(ColInk))
		cv.Text(f.Update, RectF{dx + dot + c.Px(6), r.T, r.R, r.B}, font, col(ColInk))
	})
	if f.OnUpdate != nil {
		c.Click("update", r, f.OnUpdate)
	}
}

func (f Frame) page(c *Ctx, side, w, h float32) {
	if f.Page == nil {
		return
	}
	area := RectF{side, 0, w, h}
	pad := c.Px(pagePad)
	pw := min(area.W()-2*pad, c.Px(pageMaxW))
	id := FrameScrollID(f.PageID)
	// Whole pixels: a smooth scroll passes through fractions, and text
	// drawn between pixels would blur while it moves.
	off := float32(math.Round(float64(c.Scroll[id])))

	// The dock floats over the page's bottom: the page keeps scrolling
	// under it, and can scroll far enough for its end to clear the pill.
	// A dock sliding out (another page) takes no room.
	var reserve float32
	var dk dockGeom
	if f.Dock != nil {
		dk = f.dockGeom(c, side, pw, w, h)
		if !f.DockOut {
			reserve = dk.reserve
		}
	}

	// With a drawn caption, the page's top CaptionH is a bar in the page
	// colour that the caption buttons sit on: content scrolls under it
	// (drawing and clicks are clipped), with no line at its edge.
	view := area
	if f.Caption {
		view.T = c.Px(CaptionH)
	}
	c.PushClip(view)
	ph := f.Page.Layout(c, side+pad, pad-off, pw)
	c.PopClip()
	if f.Dock != nil {
		f.drawDock(c, dk)
	}

	maxScroll := max(0, ph+2*pad+reserve-h)
	if c.Scroll[id] > maxScroll { // the window grew, or the page got shorter
		c.Scroll[id] = maxScroll
	}
	c.AddScroll(ScrollArea{ID: id, R: area, Max: maxScroll, View: RectF{area.L, view.T, area.R, h - reserve}})
	if k := c.keepView; k != nil && c.intoView == nil {
		// As little as it takes, with a margin.
		margin, viewB := c.Px(24), h-reserve
		want := float32(0)
		switch {
		case k[0] < view.T+margin:
			want = k[0] - (view.T + margin)
		case k[1] > viewB-margin:
			want = k[1] - (viewB - margin)
		}
		if want != 0 {
			c.WantScroll = &ScrollTarget{ID: id, To: max(0, min(maxScroll, off+want))}
		}
		c.keepView = nil
	}
	if c.intoView != nil {
		// Bring it to a third of the way down the view.
		want := *c.intoView - (view.T + (view.B-reserve-view.T)/3)
		c.WantScroll = &ScrollTarget{ID: id, To: max(0, min(maxScroll, off+want))}
		c.intoView = nil
	}
	if maxScroll > 0 {
		// A thin, quiet indicator of where the page is.
		top := c.Px(8)
		if f.Caption {
			top = c.Px(CaptionH + 4)
		}
		track := h - reserve - top - c.Px(8)
		th := max(c.Px(28), track*(h-reserve)/(ph+2*pad))
		ty := top + (track-th)*min(1, off/maxScroll)
		bar := RectF{w - c.Px(scrollbarW+4), ty, w - c.Px(4), ty + th}
		c.Draw(func(cv Canvas) { cv.RoundRect(bar, bar.W()/2, RGBA(ColText, 0.18)) })
	}
}

// The floating dock (logical pixels): a pill in the card colour a little
// off the window's bottom edge, snug around its content, over a fade of
// the page colour.
const (
	dockPadY     = 4    // above and below the content, inside the pill
	dockPadR     = 14   // right, after the time
	dockMargin   = 16   // from the window's bottom edge
	dockFade     = 56   // how far above the pill's top the backdrop fades out
	dockBackdrop = 0.63 // the backdrop's opacity from the pill's top down
	dockSlide    = 260  // ms, sliding up (in) or down (out)
)

// dockGeom is where the dock is in this frame.
type dockGeom struct {
	pill    RectF   // where the pill is now (sliding)
	inner   RectF   // the content's place in it
	reserve float32 // the room at the page's bottom it covers when shown
	fade    RectF   // the backdrop, page-wide, sliding with the pill
}

// dockGeom places the pill in the page column (width pw) and slides it in
// after DockAt, or out when DockOut.
func (f Frame) dockGeom(c *Ctx, side, pw, w, h float32) dockGeom {
	// Measure the content in a pass of its own; it is laid out for real
	// in drawDock.
	probe := NewCtx(c.Scale, c.M, c.Now, "", "", nil)
	inW := pw // the content has the page column's width; the pill reaches out by its padding
	ph := f.Dock.Layout(probe, 0, 0, inW) + c.Px(2*dockPadY)
	margin := c.Px(dockMargin)
	shown := float32(1)
	if !f.DockAt.IsZero() {
		t := float64(c.Now.Sub(f.DockAt)) / float64(dockSlide*time.Millisecond)
		if t < 1 {
			c.Animating = true
		}
		t = max(0, min(1, t))
		if f.DockOut {
			shown = 1 - float32(EaseIn(t))
		} else {
			shown = float32(EaseOut(t))
		}
	}
	// Hidden is fully below the edge, fade and all.
	travel := ph + margin + c.Px(dockFade)
	dy := float32(math.Round(float64(travel * (1 - shown))))
	// The content lines up with the page column (the play button's left
	// edge on the column's); the pill reaches out by its padding. On the
	// left that is what makes a round button at the start concentric with
	// the pill's rounded end.
	padL := max(0, (ph-c.Px(roundButtonSize))/2)
	left := side + c.Px(pagePad) - padL
	top := h - margin - ph + dy
	pill := RectF{left, top, left + padL + pw + c.Px(dockPadR), top + ph}
	return dockGeom{
		pill:    pill,
		inner:   RectF{pill.L + padL, pill.T + c.Px(dockPadY), pill.R - c.Px(dockPadR), pill.B - c.Px(dockPadY)},
		reserve: ph + margin + c.Px(12),
		// One height while it slides, so its bitmap is made once.
		fade: RectF{side, top - c.Px(dockFade), w, top + ph + margin},
	}
}

// drawDock draws the backdrop and the pill with the dock's content. A dock
// sliding out is drawn only: it takes no clicks.
func (f Frame) drawDock(c *Ctx, d dockGeom) {
	fw, fh := int(d.fade.W()), int(math.Ceil(float64(d.fade.H())))
	solid := int(c.Px(dockFade))
	if fw > 0 && fh > 0 {
		key := fmt.Sprintf("dockfade:%dx%d:%d", fw, fh, solid)
		c.Draw(func(cv Canvas) {
			cv.Bitmap(key, d.fade.L, d.fade.T, fw, fh, 1, func() []byte {
				return fadePixels(fw, fh, solid, ColBase, dockBackdrop)
			})
		})
	}
	pill := d.pill
	c.Draw(func(cv Canvas) { cv.RoundRect(pill, pill.H()/2, col(ColCard)) })
	if f.DockOut {
		probe := NewCtx(c.Scale, c.M, c.Now, "", "", nil)
		f.Dock.Layout(probe, d.inner.L, d.inner.T, d.inner.W())
		c.Draw(func(cv Canvas) { probe.Paint(cv) })
		return
	}
	// The pill itself catches clicks, so none reach the page beneath it.
	c.HoverArea("dock", pill)
	f.Dock.Layout(c, d.inner.L, d.inner.T, d.inner.W())
}

// fadePixels renders a w×h box of colour k as premultiplied BGRA: clear at
// the top, rising smoothly to opacity over the first `from` rows, then
// steady to the bottom. Dithered, like the glow, so the fade doesn't band.
func fadePixels(w, h, from int, k color.RGBA, opacity float64) []byte {
	pix := make([]byte, 4*w*h)
	for y := range h {
		t := 1.0
		if y < from {
			t = (float64(y) + 0.5) / float64(from)
			t = t * t * (3 - 2*t) // smoothstep
		}
		for x := range w {
			alpha := math.Round(opacity*t*255 + dither(x, y) - 0.5)
			if alpha <= 0 {
				continue
			}
			alpha = min(255, alpha)
			i := 4 * (y*w + x)
			pix[i] = uint8(math.Round(float64(k.B) * alpha / 255))
			pix[i+1] = uint8(math.Round(float64(k.G) * alpha / 255))
			pix[i+2] = uint8(math.Round(float64(k.R) * alpha / 255))
			pix[i+3] = uint8(alpha)
		}
	}
	return pix
}

// haloPixels renders a w×h box of colour k as premultiplied BGRA: a
// rounded rectangle (radius r) inset by fade on every side at opacity,
// fading smoothly to clear over fade pixels beyond its edge. Dithered.
func haloPixels(w, h int, fade, r float64, k color.RGBA, opacity float64) []byte {
	pix := make([]byte, 4*w*h)
	iw, ih := float64(w)-2*fade, float64(h)-2*fade
	r = math.Min(r, math.Min(iw, ih)/2)
	for y := range h {
		for x := range w {
			d := edgeDepth(float64(x)+0.5-fade, float64(y)+0.5-fade, iw, ih, r) // > 0 inside
			t := 1.0
			if d < 0 {
				t = math.Max(0, 1+d/fade)
				t = t * t * (3 - 2*t) // smoothstep
			}
			alpha := math.Round(opacity*t*255 + dither(x, y) - 0.5)
			if alpha <= 0 {
				continue
			}
			alpha = min(255, alpha)
			i := 4 * (y*w + x)
			pix[i] = uint8(math.Round(float64(k.B) * alpha / 255))
			pix[i+1] = uint8(math.Round(float64(k.G) * alpha / 255))
			pix[i+2] = uint8(math.Round(float64(k.R) * alpha / 255))
			pix[i+3] = uint8(alpha)
		}
	}
	return pix
}

// FeatherPolys returns the feather mark as polygons at size s placed at
// (x, y): the shape to fill, and the cuts to paint in the background colour.
func FeatherPolys(s, x, y float32) (fill, cuts [][]PointF) {
	conv := func(p *Path) [][]PointF {
		p = p.Transform(float64(s), float64(x), float64(y))
		var out [][]PointF
		for _, poly := range p.polys {
			q := make([]PointF, len(poly))
			for i, v := range poly {
				q[i] = PointF{float32(v.x), float32(v.y)}
			}
			out = append(out, q)
		}
		return out
	}
	return conv(feather()), nil
}
