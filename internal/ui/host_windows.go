package ui

import (
	"math"
	"strings"
	"time"
	"unicode/utf16"
	"unsafe"
)

// host is what flit's drawn windows share (the main window, the tray
// panel): a double-buffered Direct2D surface for the client area, a layout
// pass per paint, hover and press tracking, clicks, wheel scrolling, and a
// timer that repaints while something live is on screen.
type host struct {
	hwnd    uintptr
	surf    *Surface
	scale   float32
	metrics Metrics
	bg      ColorF

	hover, pressed string
	scroll         map[string]float32
	// Smooth scrolling: where each moving scroll area is headed, and when
	// the last step was.
	scrollTo   map[string]float32
	scrollTick time.Time
	// drag is the target being dragged (its OnDrag follows the pointer).
	drag         string
	dragX, dragY float32
	last         *Ctx
	ticking      bool
	liveOn       bool // the live regions' timer runs
	// The text selection (nil: none), and whether the button is down
	// making it; ibeam: the hovered hit is selectable text.
	sel       *Selection
	selecting bool
	ibeam     bool
	// Where the pointer is while selecting (it may be outside the
	// window), and the clicks counted for double and triple clicks.
	selX, selY   float32
	clicks       int
	clickAt      time.Time
	clickX       float32
	clickY       float32
	clickTouched bool     // the pointer moved off the click since (a drag)
	edit         *Editing // the text field being typed in
	hand         bool     // the hovered hit is clickable
	surrogate    uint16   // the first half of a character outside the BMP

	// layout lays out the client area (w×h device pixels) into c.
	layout func(c *Ctx, w, h float32)
}

const (
	wmPaint         = 0x000F
	wmEraseBkgnd    = 0x0014
	wmSize          = 0x0005
	wmClose         = 0x0010
	wmActivate      = 0x0006
	wmKeyDown       = 0x0100
	wmGetMinMaxInfo = 0x0024
	wmMouseWheel    = 0x020A
	wmSetIcon       = 0x0080
	wmCaptureChange = 0x0215
	wmChar          = 0x0102
	wmKillFocus     = 0x0008
	vkEnd           = 0x23
	vkHome          = 0x24
	vkLeft          = 0x25
	vkRight         = 0x27
	vkDelete        = 0x2E
	srcCopy         = 0x00CC0020
	timerAnimate    = 2
	timerScroll     = 3
	timerLive       = 4  // the live regions (DrawLive), ~64 Hz
	timerSelScroll  = 5  // scrolling while selecting near an edge
	selEdge         = 28 // px (logical) from the view's edge where selecting scrolls
	// scrollEase is the smooth scroll's time constant: each wheel step
	// is mostly done after 2–3 of these.
	scrollEase    = 50 * time.Millisecond
	vkEscape      = 0x1B
	vkControl     = 0x11
	smCxDoubleClk = 36
	smCyDoubleClk = 37
	idcIBeam      = 32513
)

var (
	procBeginPaint         = user32.NewProc("BeginPaint")
	procEndPaint           = user32.NewProc("EndPaint")
	procBitBlt             = gdi32.NewProc("BitBlt")
	procGetClientRect      = user32.NewProc("GetClientRect")
	procInvalidateRect     = user32.NewProc("InvalidateRect")
	procSetCapture         = user32.NewProc("SetCapture")
	procReleaseCapture     = user32.NewProc("ReleaseCapture")
	procScreenToClient     = user32.NewProc("ScreenToClient")
	procGetDpiForWindow    = user32.NewProc("GetDpiForWindow")
	procSendMessageW       = user32.NewProc("SendMessageW")
	procIsWindowVisible    = user32.NewProc("IsWindowVisible")
	procGetKeyState        = user32.NewProc("GetKeyState")
	procGetDoubleClickTime = user32.NewProc("GetDoubleClickTime")
)

type paintStruct struct {
	Hdc         uintptr
	Erase       int32
	RcPaint     struct{ L, T, R, B int32 }
	Restore     int32
	IncUpdate   int32
	RgbReserved [32]byte
}

func (h *host) dpiScale() float32 {
	if procGetDpiForWindow.Find() == nil {
		if d, _, _ := procGetDpiForWindow.Call(h.hwnd); d > 0 {
			return float32(d) / 96
		}
	}
	return 1
}

// Invalidate asks for a repaint.
func (h *host) Invalidate() { procInvalidateRect.Call(h.hwnd, 0, 0) }

// Visible reports whether the window is shown.
func (h *host) Visible() bool {
	v, _, _ := procIsWindowVisible.Call(h.hwnd)
	return v != 0
}

// Hwnd is the window handle.
func (h *host) Hwnd() uintptr { return h.hwnd }

func (h *host) clientSize() (int32, int32) {
	var r struct{ L, T, R, B int32 }
	procGetClientRect.Call(h.hwnd, uintptr(unsafe.Pointer(&r)))
	return r.R - r.L, r.B - r.T
}

func (h *host) paint() {
	var ps paintStruct
	hdc, _, _ := procBeginPaint.Call(h.hwnd, uintptr(unsafe.Pointer(&ps)))
	defer procEndPaint.Call(h.hwnd, uintptr(unsafe.Pointer(&ps)))
	cw, ch := h.clientSize()
	if cw <= 0 || ch <= 0 || h.layout == nil {
		return
	}
	if h.surf == nil || h.surf.W != cw || h.surf.H != ch {
		h.surf.Free()
		s, err := NewSurface(cw, ch)
		if err != nil {
			h.surf = nil
			return
		}
		h.surf = s
	}
	c := NewCtx(h.scale, h.metrics, time.Now(), h.hover, h.pressed, h.scroll)
	c.Edit, c.StartEdit = h.edit, h.startEdit
	c.Sel = h.sel
	h.layout(c, float32(cw), float32(ch))
	if h.sel != nil && (h.sel.AnchorArea >= len(c.texts) || h.sel.FocusArea >= len(c.texts)) {
		h.sel, h.selecting = nil, false // the text went away (another page)
	}
	if h.selecting {
		// The page moved under the pointer (a wheel turn, scrolling at
		// the edge): the selection's end follows what is under it now.
		defer h.selectAt(h.selX, h.selY)
	}
	if h.edit != nil && c.committer(h.edit.ID) == nil {
		h.edit = nil // the field went away (another page)
	}
	p, err := BeginWith(h.surf, h.bg)
	if err != nil {
		return
	}
	c.Paint(p)
	if p.End() != nil {
		return
	}
	procBitBlt.Call(hdc, 0, 0, uintptr(cw), uintptr(ch), h.surf.dc, 0, 0, srcCopy)
	h.last = c
	h.setTicking(c.Animating)
	h.setLive(len(c.live) > 0)
	if w := c.WantScroll; w != nil && math.Abs(float64(w.To-h.scroll[w.ID])) >= 1 {
		h.scrollSmoothly(w.ID, w.To)
	}
}

// setLive runs the live regions' timer while there are any on screen.
func (h *host) setLive(on bool) {
	if on == h.liveOn {
		return
	}
	h.liveOn = on
	if on {
		procSetTimer.Call(h.hwnd, timerLive, 15, 0) // the system timer's tick, ~64 Hz
	} else {
		procKillTimer.Call(h.hwnd, timerLive)
	}
}

// stopTimers stops repainting (the window hid).
func (h *host) stopTimers() {
	h.setTicking(false)
	h.setLive(false)
}

// paintLive repaints only the live regions of the last layout, onto the
// kept surface (from Go, pixelCanvas) and straight to the window: no
// layout, no Direct2D pass, no full repaint.
func (h *host) paintLive() {
	c := h.last
	if c == nil || len(c.live) == 0 || h.surf == nil || !h.Visible() {
		h.setLive(false)
		return
	}
	if ic, _, _ := procIsIconic.Call(h.hwnd); ic != 0 {
		return // minimised: nothing to see
	}
	if cw, ch := h.clientSize(); cw != h.surf.W || ch != h.surf.H {
		h.Invalidate() // resized: a full paint comes
		return
	}
	procGdiFlush.Call() // the last full paint is in the bits
	now := time.Now()
	pc := &pixelCanvas{pix: h.surf.Bits, w: int(h.surf.W), h: int(h.surf.H)}
	for _, a := range c.live {
		pc.PushClip(a.r)
		a.draw(pc, now)
		pc.PopClip()
	}
	hdc, _, _ := procGetDC.Call(h.hwnd)
	if hdc == 0 {
		return
	}
	for _, a := range c.live {
		x, y := int32(a.r.L), int32(a.r.T)
		procBitBlt.Call(hdc, uintptr(x), uintptr(y), uintptr(int32(a.r.R)-x), uintptr(int32(a.r.B)-y), h.surf.dc, uintptr(x), uintptr(y), srcCopy)
	}
	procReleaseDC.Call(h.hwnd, hdc)
}

// setTicking repaints at 20 fps while something on screen moves.
func (h *host) setTicking(on bool) {
	if on == h.ticking {
		return
	}
	h.ticking = on
	if on {
		procSetTimer.Call(h.hwnd, timerAnimate, 50, 0)
	} else {
		procKillTimer.Call(h.hwnd, timerAnimate)
	}
}

func (h *host) hitAt(l uintptr) (Hit, bool) {
	if h.last == nil {
		return Hit{}, false
	}
	return h.last.HitAt(float32(int16(l&0xFFFF)), float32(int16(l>>16&0xFFFF)))
}

// handle processes the messages all drawn windows share; ok is false for
// the rest.
func (h *host) handle(m uint32, wp, lp uintptr) (uintptr, bool) {
	switch m {
	case wmPaint:
		h.paint()
		return 0, true
	case wmEraseBkgnd:
		return 1, true // all painted in WM_PAINT; no flicker
	case wmSize:
		h.Invalidate()
		return 0, true
	case wmTimer:
		switch wp {
		case timerAnimate:
			h.Invalidate()
		case timerScroll:
			h.stepScroll()
		case timerLive:
			h.paintLive()
		case timerSelScroll:
			h.scrollWhileSelecting()
		}
		return 0, true
	case wmMouseMove:
		if h.drag != "" {
			h.dragX, h.dragY = float32(int16(lp&0xFFFF)), float32(int16(lp>>16&0xFFFF))
			h.dragTo(false)
			return 0, true
		}
		if h.selecting {
			h.selectTo(lp)
			return 0, true
		}
		id, hand, ibeam := "", false, false
		if hit, ok := h.hitAt(lp); ok {
			id, hand, ibeam = hit.ID, hit.clickable(), hit.text
		}
		h.hand, h.ibeam = hand, ibeam
		if id != h.hover {
			h.hover = id
			tme := struct {
				Size, Flags uint32
				Track       uintptr
				Hover       uint32
			}{Flags: tmeLeave, Track: h.hwnd}
			tme.Size = uint32(unsafe.Sizeof(tme))
			procTrackMouseEvent.Call(uintptr(unsafe.Pointer(&tme)))
			h.Invalidate()
		}
		return 0, true
	case wmMouseLeave:
		if h.hover != "" {
			h.hover = ""
			h.Invalidate()
		}
		return 0, true
	case wmLButtonDown:
		if h.edit != nil {
			if hit, ok := h.hitAt(lp); !ok || hit.ID != h.edit.ID {
				h.CommitEdit() // clicking elsewhere saves
			}
		}
		if hit, ok := h.hitAt(lp); ok && hit.text {
			h.startSelecting(float32(int16(lp&0xFFFF)), float32(int16(lp>>16&0xFFFF)))
			return 0, true
		}
		if h.sel != nil { // a click anywhere else ends the selection
			h.sel = nil
			h.Invalidate()
		}
		if hit, ok := h.hitAt(lp); ok {
			h.pressed = hit.ID
			procSetCapture.Call(h.hwnd)
			if hit.OnDrag != nil {
				h.drag = hit.ID
				h.dragX, h.dragY = float32(int16(lp&0xFFFF)), float32(int16(lp>>16&0xFFFF))
				h.dragTo(false)
			}
			h.Invalidate()
		}
		return 0, true
	case wmLButtonUp:
		if h.selecting {
			h.selectTo(lp)
			h.selecting = false
			procKillTimer.Call(h.hwnd, timerSelScroll)
			procReleaseCapture.Call()
			if h.sel != nil && h.sel.Empty() {
				h.sel = nil // a click on text, not a drag
			}
			h.Invalidate()
			return 0, true
		}
		if h.drag != "" {
			h.dragX, h.dragY = float32(int16(lp&0xFFFF)), float32(int16(lp>>16&0xFFFF))
			h.endDrag()
			return 0, true
		}
		pressed := h.pressed
		h.pressed = ""
		procReleaseCapture.Call()
		h.Invalidate()
		if hit, ok := h.hitAt(lp); ok && hit.ID == pressed {
			switch {
			case hit.OnClickAt != nil:
				hit.OnClickAt(float32(int16(lp&0xFFFF)), float32(int16(lp>>16&0xFFFF)))
			case hit.OnClick != nil:
				hit.OnClick()
			}
		}
		return 0, true
	case wmCaptureChange:
		h.selecting = false
		if h.drag != "" {
			h.endDrag() // capture taken away: the drag ends where it was
		}
		if h.pressed != "" {
			h.pressed = ""
			h.Invalidate()
		}
		return 0, true
	case wmMouseWheel:
		delta := float32(int16(wp >> 16 & 0xFFFF))
		pt := point{int32(int16(lp & 0xFFFF)), int32(int16(lp >> 16 & 0xFFFF))}
		procScreenToClient.Call(h.hwnd, uintptr(unsafe.Pointer(&pt)))
		if h.last != nil {
			if a, ok := h.last.ScrollAt(float32(pt.X), float32(pt.Y)); ok {
				// From where the scroll is headed, so quick wheel turns add up.
				from, moving := h.scrollTo[a.ID]
				if !moving {
					from = h.scroll[a.ID]
				}
				h.scrollSmoothly(a.ID, max(0, min(a.Max, from-delta/120*scrollStep*h.scale)))
			}
		}
		return 0, true
	case wmChar:
		if h.edit == nil {
			return 0, false
		}
		h.typed(uint16(wp))
		return 0, true
	case wmKeyDown:
		if h.edit == nil {
			return h.selectionKey(wp)
		}
		e := h.edit
		switch wp {
		case vkLeft:
			e.Move(e.Caret - 1)
		case vkRight:
			e.Move(e.Caret + 1)
		case vkHome:
			e.Move(0)
		case vkEnd:
			e.Move(len(e.Text))
		case vkDelete:
			e.Delete()
		default:
			return 0, false // characters arrive as WM_CHAR
		}
		e.Since = time.Now()
		h.Invalidate()
		if wp == vkDelete {
			h.changed()
		}
		return 0, true
	case wmKillFocus:
		h.CommitEdit()
		return 0, false
	case wmSetCursor:
		if lp&0xFFFF == 1 && (h.selecting || (h.hover != "" && h.ibeam)) { // HTCLIENT over text
			c, _, _ := procLoadCursorW.Call(0, idcIBeam)
			procSetCursor.Call(c)
			return 1, true
		}
		if lp&0xFFFF == 1 && h.hover != "" && h.hand { // HTCLIENT over something clickable
			c, _, _ := procLoadCursorW.Call(0, idcHand)
			procSetCursor.Call(c)
			return 1, true
		}
	}
	return 0, false
}

// startSelecting begins a selection where the button went down on text:
// one click puts its anchor there, a double click selects the word, a
// triple click the whole paragraph (clicks counted within Windows' double
// click time and distance).
func (h *host) startSelecting(x, y float32) {
	area, pos, ok := h.last.textAt(x, y)
	if !ok {
		return
	}
	dt, _, _ := procGetDoubleClickTime.Call()
	cx, _, _ := procGetSystemMetrics.Call(smCxDoubleClk)
	cy, _, _ := procGetSystemMetrics.Call(smCyDoubleClk)
	n := 1
	if time.Since(h.clickAt) <= time.Duration(dt)*time.Millisecond && !h.clickTouched &&
		abs32(x-h.clickX) <= float32(cx) && abs32(y-h.clickY) <= float32(cy) {
		n = h.clicks%3 + 1
	}
	h.clicks, h.clickAt, h.clickX, h.clickY, h.clickTouched = n, time.Now(), x, y, false
	text := h.last.texts[area].text
	switch n {
	case 2:
		from, to := wordAt(text, pos)
		h.sel = &Selection{AnchorArea: area, AnchorPos: from, FocusArea: area, FocusPos: to}
	case 3:
		h.sel = &Selection{AnchorArea: area, AnchorPos: 0, FocusArea: area, FocusPos: len(text)}
	default:
		h.sel = &Selection{AnchorArea: area, AnchorPos: pos, FocusArea: area, FocusPos: pos}
	}
	h.selecting, h.selX, h.selY = true, x, y
	procSetCapture.Call(h.hwnd)
	procSetTimer.Call(h.hwnd, timerSelScroll, 15, 0)
	h.Invalidate()
}

// selectTo moves the selection's focus to the pointer (window
// coordinates in lp, which may lie outside the window while it is held).
func (h *host) selectTo(lp uintptr) {
	h.selX, h.selY = float32(int16(lp&0xFFFF)), float32(int16(lp>>16&0xFFFF))
	h.selectAt(h.selX, h.selY)
}

// selectAt moves the selection's focus to (x, y). A double or triple
// click's word or paragraph stays as it is until the pointer moves off
// the click.
func (h *host) selectAt(x, y float32) {
	if h.sel == nil || h.last == nil {
		return
	}
	if !h.clickTouched {
		if abs32(x-h.clickX) < 4*h.scale && abs32(y-h.clickY) < 4*h.scale {
			return
		}
		h.clickTouched = true
	}
	if area, pos, ok := h.last.textAt(x, y); ok && (area != h.sel.FocusArea || pos != h.sel.FocusPos) {
		h.sel.FocusArea, h.sel.FocusPos = area, pos
		h.Invalidate()
	}
}

// scrollWhileSelecting scrolls the page while the pointer, selecting, is
// near or past the top or bottom of its view: faster the further out.
// The selection's end follows after the repaint (paint).
func (h *host) scrollWhileSelecting() {
	if !h.selecting || h.last == nil || len(h.last.scrolls) == 0 {
		procKillTimer.Call(h.hwnd, timerSelScroll)
		return
	}
	a := h.last.scrolls[0]
	for _, s := range h.last.scrolls {
		if h.selX >= s.R.L && h.selX < s.R.R {
			a = s
		}
	}
	v := a.View
	if v.H() <= 0 {
		v = a.R
	}
	edge := selEdge * h.scale
	var d float32
	switch {
	case h.selY < v.T+edge:
		d = h.selY - (v.T + edge)
	case h.selY > v.B-edge:
		d = h.selY - (v.B - edge)
	default:
		return
	}
	step := min(abs32(d)/3+2*h.scale, 40*h.scale)
	if d < 0 {
		step = -step
	}
	to := max(0, min(a.Max, h.scroll[a.ID]+step))
	if to != h.scroll[a.ID] {
		h.scroll[a.ID] = to
		delete(h.scrollTo, a.ID)
		h.Invalidate()
	}
}

// selectionKey handles keys while nothing is typed in: Ctrl+C copies the
// selected text, Esc lets go of it.
func (h *host) selectionKey(wp uintptr) (uintptr, bool) {
	if h.sel == nil || h.last == nil {
		return 0, false
	}
	switch {
	case wp == 'C' && keyDown(vkControl):
		if text := h.last.SelectedText(*h.sel); text != "" {
			_ = SetClipboard(h.hwnd, text)
		}
		return 0, true
	case wp == vkEscape:
		h.sel = nil
		h.Invalidate()
		return 0, true
	}
	return 0, false
}

// keyDown reports whether a key is held (GetKeyState's high bit).
func keyDown(vk uintptr) bool {
	r, _, _ := procGetKeyState.Call(vk)
	return int16(r) < 0
}

// startEdit begins typing in field id (saving another field first).
func (h *host) startEdit(id, value string, maxLen int) {
	if h.edit != nil {
		if h.edit.ID == id {
			return
		}
		h.CommitEdit()
	}
	text := []rune(value)
	h.edit = &Editing{ID: id, Text: text, Caret: len(text), All: len(text) > 0, Since: time.Now(), Max: maxLen, Orig: value}
	h.Invalidate()
}

// changed tells the edited field's OnChange what it holds now.
func (h *host) changed() {
	if h.edit == nil || h.last == nil {
		return
	}
	if fn := h.last.changer(h.edit.ID); fn != nil {
		fn(string(h.edit.Text))
	}
}

// scrollSmoothly moves scroll area id to offset to over a few frames.
func (h *host) scrollSmoothly(id string, to float32) {
	if h.scrollTo == nil {
		h.scrollTo = map[string]float32{}
	}
	if len(h.scrollTo) == 0 {
		h.scrollTick = time.Now()
		procSetTimer.Call(h.hwnd, timerScroll, 10, 0) // as fast as the system timer goes (~64 Hz)
	}
	h.scrollTo[id] = to
	h.stepScroll()
}

// stepScroll eases each moving scroll area towards its target.
func (h *host) stepScroll() {
	now := time.Now()
	k := float32(1 - math.Exp(-float64(now.Sub(h.scrollTick))/float64(scrollEase)))
	h.scrollTick = now
	for id, to := range h.scrollTo {
		if h.last != nil {
			if a, ok := h.last.ScrollArea(id); ok {
				to = min(to, a.Max) // the page got shorter meanwhile
			}
		}
		cur := h.scroll[id] + (to-h.scroll[id])*k
		if math.Abs(float64(to-cur)) < 0.5 {
			cur = to
			delete(h.scrollTo, id)
		}
		h.scroll[id] = cur
	}
	if len(h.scrollTo) == 0 {
		procKillTimer.Call(h.hwnd, timerScroll)
	}
	h.Invalidate()
}

// dragTo hands the pointer's position to the dragged target (from the
// latest layout, which may have moved it).
func (h *host) dragTo(done bool) {
	if h.last == nil {
		return
	}
	if hit, ok := h.last.HitByID(h.drag); ok && hit.OnDrag != nil {
		hit.OnDrag(h.dragX, h.dragY, done)
	}
}

// endDrag finishes a drag where the pointer was last.
func (h *host) endDrag() {
	h.dragTo(true)
	h.drag, h.pressed = "", ""
	procReleaseCapture.Call()
	h.Invalidate()
}

// CommitEdit ends typing and hands the text to the field's OnCommit.
func (h *host) CommitEdit() {
	e := h.edit
	if e == nil {
		return
	}
	h.edit = nil
	h.Invalidate()
	if h.last != nil {
		if commit := h.last.committer(e.ID); commit != nil {
			commit(string(e.Text))
		}
	}
}

// CancelEdit ends typing without saving (a field that follows its text
// as it changes gets its old value back).
func (h *host) CancelEdit() {
	e := h.edit
	if e == nil {
		return
	}
	h.edit = nil
	h.Invalidate()
	if h.last != nil {
		if fn := h.last.changer(e.ID); fn != nil {
			fn(e.Orig)
		}
	}
}

// typed handles one WM_CHAR (a UTF-16 unit) while editing.
func (h *host) typed(u uint16) {
	e := h.edit
	switch {
	case u == 0x0D: // Enter
		h.CommitEdit()
		return
	case u == 0x1B: // Esc
		h.CancelEdit()
		return
	case u == 0x08: // Backspace
		e.Backspace()
	case u == 0x01: // Ctrl+A
		e.All = len(e.Text) > 0
	case u == 0x16: // Ctrl+V
		text := ClipboardText(h.hwnd)
		if i := strings.IndexAny(text, "\r\n"); i >= 0 {
			text = text[:i]
		}
		e.Insert(text)
	case u >= 0xD800 && u < 0xDC00:
		h.surrogate = u
		return
	case u >= 0xDC00 && u < 0xE000:
		if h.surrogate != 0 {
			e.Insert(string(utf16.DecodeRune(rune(h.surrogate), rune(u))))
		}
		h.surrogate = 0
	case u >= 0x20:
		e.Insert(string(rune(u)))
	default:
		return // other control keys
	}
	e.Since = time.Now()
	h.Invalidate()
	h.changed()
}

// render lays out and draws offscreen (tests, pictures for the docs).
func render(layout func(c *Ctx, w, h float32), w, h int32, scale float32, bg ColorF, hover string, scroll map[string]float32) (*Surface, error) {
	m, err := D2DMetrics()
	if err != nil {
		return nil, err
	}
	s, err := NewSurface(w, h)
	if err != nil {
		return nil, err
	}
	c := NewCtx(scale, m, time.Now(), hover, "", scroll)
	layout(c, float32(w), float32(h))
	p, err := BeginWith(s, bg)
	if err != nil {
		s.Free()
		return nil, err
	}
	c.Paint(p)
	if err := p.End(); err != nil {
		s.Free()
		return nil, err
	}
	return s, nil
}
