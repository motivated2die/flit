package ui

import (
	"errors"
	"math"
	"strings"
	"time"
	"unicode/utf16"
	"unsafe"
)

// host is what flit's drawn windows share (the main window, the tray
// panel, the floating notes window): a hardware-accelerated Direct2D
// HwndRenderTarget (with automatic software fallback), a layout pass per
// paint, hover and press tracking, clicks, VSync-paced wheel scrolling,
// and timers/VSync for live regions and animations.
type host struct {
	hwnd    uintptr
	hw      HwndTarget
	surf    *Surface // fallback software surface if HwndTarget is unavailable
	dpi     float32  // OS monitor DPI scale (1 = 96 DPI)
	zoom    float32  // user UI scale multiplier (0 or 1 = 100%)
	scale   float32  // effective scale = dpi * zoom
	metrics Metrics
	bg      ColorF
	bgBrush uintptr // dark GDI brush for WM_ERASEBKGND before first paint
	trans   transition
	ripple  themeRipple
	buttons ButtonAnims

	hover, pressed string
	scroll         map[string]float32
	// Smooth scrolling: critically-damped spring target and velocity per
	// moving scroll area, and when the last step ran.
	scrollTo   map[string]float32
	scrollVel  map[string]float32
	scrollTick time.Time
	// drag is the target being dragged (its OnDrag follows the pointer).
	drag          string
	dragX, dragY  float32
	last          *Ctx
	ticking       bool
	vsync         vsyncPacer
	vsyncMask     uint8
	selScrollTick time.Time
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
	// focus is the multi-line editor with the keyboard ("" none); edDrag:
	// the button is down in it, selecting; goalX: where Up/Down aim.
	focus  string
	edDrag bool
	goalX  float32
	// Upward-expanding ToolbarOnFocus compensation: remember the scroll area
	// and delta added when focus entered so blur restores it cleanly, and
	// defer blurring until button-up when clicking a button so the target
	// does not shift out from under the pointer before release.
	focusScrollID      string
	focusScrollDelta   float32
	focusScrollFrom    float32
	focusScrollPending bool
	blurOnUp           bool

	// layout lays out the client area (w×h device pixels) into c.
	layout func(c *Ctx, w, h float32)
}

const (
	wmDestroy       = 0x0002
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
	selEdge         = 28 // px (logical) from the view's edge where selecting scrolls
	// scrollSpringOmega is the angular frequency (rad/s) of the critically-damped
	// smooth-scroll spring: smooth ramp-up, velocity-preserving retargeting on
	// rapid wheel turns, zero overshoot, and a crisp settle (~190 ms).
	scrollSpringOmega = 26.0
	vkEscape          = 0x1B
	vkControl         = 0x11
	vkShift           = 0x10
	vkMenu            = 0x12 // Alt
	smCxDoubleClk     = 36
	smCyDoubleClk     = 37
	idcIBeam          = 32513
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

// rawDPI returns the monitor's OS DPI scale without user zoom.
func (h *host) rawDPI() float32 {
	if h.dpi > 0 {
		return h.dpi
	}
	if h.scale > 0 {
		return h.scale
	}
	return 1
}

func (h *host) setDPI(d float32) {
	if d <= 0 {
		d = 1
	}
	h.dpi = d
	z := h.zoom
	if z <= 0 {
		z = 1
	}
	h.scale = d * z
}

// SetZoom changes the user UI scale multiplier (e.g. 0.8, 1.0, 1.1, 1.25)
// and repaints the window.
func (h *host) SetZoom(z float32) {
	if z <= 0 {
		z = 1
	}
	if h.dpi <= 0 {
		h.dpi = h.dpiScale()
	}
	h.zoom = z
	h.scale = h.dpi * z
	h.Invalidate()
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
	// Fast path during a circular theme ripple: two GPU draw calls, no layout.
	if h.ripple.ready() {
		if cw != h.ripple.w || ch != h.ripple.h {
			h.cancelRipple()
		} else if h.paintRippleFrame(cw, ch) {
			return
		}
	}
	c := NewCtx(h.scale, h.metrics, time.Now(), h.hover, h.pressed, h.scroll)
	c.ScrollTo = h.scrollSmoothly
	c.SetScroll = func(id string, to float32) {
		delete(h.scrollTo, id)
		delete(h.scrollVel, id)
		if len(h.scrollTo) == 0 {
			h.setVsync(vsyncScroll, false)
		}
		h.scroll[id] = to
		h.Invalidate()
	}
	c.Edit, c.StartEdit = h.edit, h.startEdit
	c.Sel = h.sel
	c.Focus = h.focus
	c.Buttons = &h.buttons
	h.layout(c, float32(cw), float32(ch))
	h.buttons.prune()
	if h.focusScrollPending {
		h.focusScrollDelta = h.scroll[h.focusScrollID] - h.focusScrollFrom
		h.focusScrollPending = false
	}
	if h.focus != "" {
		if _, ok := c.editorByID(h.focus); !ok {
			h.setFocus("") // the editor went away (another page)
		}
	}
	if w := c.wantEdit; w != nil && h.edit == nil {
		defer h.startEdit(w.id, w.value, w.max) // after this pass: it draws again, editing
	}
	for _, t := range c.editors {
		if t.WantFocus && h.focus != t.ID {
			h.CommitEdit()
			h.setFocus(t.ID)
			h.goalX = -1
			defer h.Invalidate() // drawn again with the caret
		}
	}
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
	if h.ripple.armed() {
		if cw == h.ripple.w && ch == h.ripple.h && h.hw.ready() {
			if newBrush, rerr := h.hw.CaptureBrush(func(p *Painter) { c.Paint(p) }, h.bg); rerr == nil {
				h.ripple.newBrush = newBrush
				h.ripple.start = time.Now()
				h.last = c
				h.setVsync(vsyncRipple, true)
				h.paintRippleFrame(cw, ch)
				return
			}
		}
		h.cancelRipple()
	}
	p, err := h.hw.Begin(h.hwnd, cw, ch, h.bg)
	switch {
	case err == nil:
		c.Paint(p)
		err = p.End()
		if errors.Is(err, ErrRecreateTarget) {
			h.hw.Free()
			h.Invalidate()
			return
		}
		if err != nil {
			return
		}
	case errors.Is(err, ErrRecreateTarget):
		h.Invalidate()
		return
	default:
		// Hardware target unavailable: fall back to software Surface + BitBlt.
		if h.surf == nil || h.surf.W != cw || h.surf.H != ch {
			h.surf.Free()
			s, serr := NewSurface(cw, ch)
			if serr != nil {
				h.surf = nil
				return
			}
			h.surf = s
		}
		p, serr := BeginWith(h.surf, h.bg)
		if serr != nil {
			return
		}
		c.Paint(p)
		if p.End() != nil {
			return
		}
		procBitBlt.Call(hdc, 0, 0, uintptr(cw), uintptr(ch), h.surf.dc, 0, 0, srcCopy)
	}
	h.last = c
	h.setVsync(vsyncAnimate, c.AnimatingFast)
	h.setTicking(c.Animating && !c.AnimatingFast)
	h.setLive(len(c.live) > 0)
	if w := c.WantScroll; w != nil && math.Abs(float64(w.To-h.scroll[w.ID])) >= 1 {
		h.scrollSmoothly(w.ID, w.To)
	}
}

// setVsync toggles one VSync reason bit, starting the DWM pacer on the first
// active reason and stopping it when none remain.
func (h *host) setVsync(bit uint8, on bool) {
	prev := h.vsyncMask
	if on {
		h.vsyncMask |= bit
	} else {
		h.vsyncMask &^= bit
	}
	if h.vsyncMask != 0 && prev == 0 {
		h.vsync.start(h.hwnd)
	} else if h.vsyncMask == 0 && prev != 0 {
		h.vsync.stopPacer()
	}
}

// onVsync advances all active VSync-paced animations, smooth scrolling, and
// live regions once per monitor refresh and paints immediately.
func (h *host) onVsync() {
	defer h.vsync.ack()
	if h.vsyncMask == 0 {
		return
	}
	if h.vsyncMask&vsyncTransition != 0 {
		h.stepTransition()
	}
	needsPaint := false
	if h.vsyncMask&vsyncScroll != 0 {
		if h.stepScroll() {
			needsPaint = true
		}
	}
	if h.vsyncMask&vsyncSelScroll != 0 {
		if h.scrollWhileSelecting() {
			needsPaint = true
		}
	}
	if h.vsyncMask&vsyncRipple != 0 || h.ripple.armed() {
		h.Invalidate()
		needsPaint = true
	}
	if h.vsyncMask&vsyncAnimate != 0 {
		h.Invalidate()
		needsPaint = true
	}
	if needsPaint {
		procUpdateWindow.Call(h.hwnd)
	} else if h.vsyncMask&vsyncLive != 0 {
		h.paintLive()
	}
}

// setLive runs VSync-paced live region repaints while there are any on screen.
func (h *host) setLive(on bool) {
	h.setVsync(vsyncLive, on)
}

// stopTimers stops all timers and VSync pacing (the window hid or closed).
func (h *host) stopTimers() {
	h.setTicking(false)
	if h.trans.active {
		h.cancelTransition()
	}
	h.cancelRipple()
	h.vsyncMask = 0
	h.vsync.stopPacer()
	clear(h.scrollTo)
	clear(h.scrollVel)
}

// paintLive repaints the live regions of the last layout. On a hardware
// HwndTarget, it replays h.last directly on the GPU with an updated timestamp
// (no Build(), no Layout(), no disk/text work); on the software fallback, only
// the live regions are redrawn into h.surf via pixelCanvas.
func (h *host) paintLive() {
	if h.ripple.armed() || h.ripple.ready() {
		return
	}
	c := h.last
	if c == nil || len(c.live) == 0 || !h.Visible() {
		h.setLive(false)
		return
	}
	if ic, _, _ := procIsIconic.Call(h.hwnd); ic != 0 {
		return // minimised: nothing to see
	}
	if h.hw.ready() {
		cw, ch := h.clientSize()
		if cw <= 0 || ch <= 0 {
			return
		}
		if uint32(cw) != h.hw.w || uint32(ch) != h.hw.h {
			h.Invalidate() // resized: a full paint comes
			return
		}
		c.Now = time.Now()
		p, err := h.hw.Begin(h.hwnd, cw, ch, h.bg)
		if err != nil {
			if errors.Is(err, ErrRecreateTarget) {
				h.Invalidate()
			}
			return
		}
		c.Paint(p)
		if err := p.End(); errors.Is(err, ErrRecreateTarget) {
			h.hw.Free()
			h.Invalidate()
		}
		return
	}
	if h.surf == nil {
		h.setLive(false)
		return
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
	case wmVsync:
		h.onVsync()
		return 0, true
	case wmDestroy:
		h.stopTimers()
		h.hw.Free()
		h.surf.Free()
		h.surf = nil
		return 0, false
	case wmEraseBkgnd:
		if !h.hw.ready() && h.surf == nil && h.bgBrush != 0 && wp != 0 {
			var rc winRect
			procGetClientRect.Call(h.hwnd, uintptr(unsafe.Pointer(&rc)))
			procFillRect.Call(wp, uintptr(unsafe.Pointer(&rc)), h.bgBrush)
		}
		return 1, true // all painted in WM_PAINT; no flicker
	case wmSize:
		h.Invalidate()
		return 0, true
	case wmTimer:
		if wp == timerAnimate && !h.ripple.armed() && !h.ripple.ready() {
			h.Invalidate()
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
		if h.edDrag {
			if t, ok := h.focused(); ok && t.Drag != nil {
				t.Drag(float32(int16(lp&0xFFFF)), float32(int16(lp>>16&0xFFFF)))
				h.Invalidate()
			}
			return 0, true
		}
		id, hand, ibeam := "", false, false
		if hit, ok := h.hitAt(lp); ok {
			id, hand, ibeam = hit.ID, hit.clickable(), hit.text || hit.editor
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
		h.blurOnUp = false
		if hit, ok := h.hitAt(lp); ok && hit.editor {
			h.pressEditor(hit, float32(int16(lp&0xFFFF)), float32(int16(lp>>16&0xFFFF)))
			return 0, true
		} else if ok && h.last != nil && h.editorPart(hit.ID) != "" {
			h.setFocus(h.editorPart(hit.ID)) // its toolbar or checkboxes: it keeps (or gets) the keyboard
		} else if h.focus != "" {
			if ok && hit.clickable() {
				// Defer blur until button-up so an expanding summary card
				// doesn't shift a clicked button above it before release.
				h.blurOnUp = true
			} else {
				h.setFocus("") // a click elsewhere lets go of the keyboard
				h.Invalidate()
			}
		}
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
		if h.edDrag {
			h.edDrag = false
			procReleaseCapture.Call()
			return 0, true
		}
		if h.selecting {
			h.selectTo(lp)
			h.selecting = false
			h.setVsync(vsyncSelScroll, false)
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
		pressed, blur := h.pressed, h.blurOnUp
		h.pressed, h.blurOnUp = "", false
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
		if blur {
			h.setFocus("")
			h.Invalidate()
		}
		return 0, true
	case wmCaptureChange:
		h.selecting, h.edDrag, h.blurOnUp = false, false, false
		h.setVsync(vsyncSelScroll, false)
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
		if h.last != nil && delta != 0 {
			if a, ok := h.last.ScrollAt(float32(pt.X), float32(pt.Y)); ok {
				if abs32(delta) < 120 {
					// Precision touchpad or hi-res free-spin wheel: OS already
					// delivers high-frequency smooth deltas, so apply directly
					// without adding easing lag.
					to := max(0, min(a.Max, h.scroll[a.ID]-delta/120*scrollStep*h.scale))
					delete(h.scrollTo, a.ID)
					delete(h.scrollVel, a.ID)
					if len(h.scrollTo) == 0 {
						h.setVsync(vsyncScroll, false)
					}
					if to != h.scroll[a.ID] {
						h.scroll[a.ID] = to
						h.Invalidate()
					}
				} else {
					// Detented mouse wheel: from where the scroll is headed,
					// so quick wheel turns add up smoothly.
					from, moving := h.scrollTo[a.ID]
					if !moving {
						from = h.scroll[a.ID]
					}
					h.scrollSmoothly(a.ID, max(0, min(a.Max, from-delta/120*scrollStep*h.scale)))
				}
			}
		}
		return 0, true
	case wmChar:
		if h.focus != "" {
			h.editorChar(uint16(wp))
			return 0, true
		}
		if h.edit == nil {
			return 0, false
		}
		h.typed(uint16(wp))
		return 0, true
	case wmKeyDown:
		if h.focus != "" {
			if t, ok := h.focused(); ok && t.Key != nil {
				m := Mods{Ctrl: keyDown(vkControl), Shift: keyDown(vkShift), Alt: keyDown(vkMenu)}
				if t.Key(wp, m, h.editorEnv()) {
					h.Invalidate()
					return 0, true
				}
			}
			return 0, false
		}
		if h.edit == nil {
			return h.selectionKey(wp)
		}
		e := h.edit
		if wp == vkTab {
			h.commitTab(keyDown(vkShift))
			return 0, true
		}
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
	h.selScrollTick = time.Now()
	procSetCapture.Call(h.hwnd)
	h.setVsync(vsyncSelScroll, true)
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
func (h *host) scrollWhileSelecting() bool {
	if !h.selecting || h.last == nil || len(h.last.scrolls) == 0 {
		h.setVsync(vsyncSelScroll, false)
		return false
	}
	now := time.Now()
	dt := now.Sub(h.selScrollTick)
	h.selScrollTick = now
	if dt < time.Millisecond {
		dt = 15625 * time.Microsecond // synthetic/tight-loop call: one nominal tick
	} else if dt > 100*time.Millisecond {
		dt = 100 * time.Millisecond
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
		return false
	}
	// Keep the same scroll speed in px/s regardless of monitor refresh rate.
	scaleDt := float32(dt) / float32(15625*time.Microsecond)
	step := min(abs32(d)/3+2*h.scale, 40*h.scale) * scaleDt
	if d < 0 {
		step = -step
	}
	to := max(0, min(a.Max, h.scroll[a.ID]+step))
	if to != h.scroll[a.ID] {
		h.scroll[a.ID] = to
		delete(h.scrollTo, a.ID)
		delete(h.scrollVel, a.ID)
		if len(h.scrollTo) == 0 {
			h.setVsync(vsyncScroll, false)
		}
		h.Invalidate()
		return true
	}
	return false
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

// editorPart is the editor a hit belongs to (its ID and ":" start the
// hit's), or "".
func (h *host) editorPart(id string) string {
	for _, t := range h.last.editors {
		if strings.HasPrefix(id, t.ID+":") {
			return t.ID
		}
	}
	return ""
}

// focused is the editor with the keyboard, in the last layout.
func (h *host) focused() (EditorTarget, bool) {
	if h.focus == "" || h.last == nil {
		return EditorTarget{}, false
	}
	return h.last.editorByID(h.focus)
}

// setFocus switches editor keyboard focus and compensates page scroll when
// entering or leaving a ToolbarOnFocus editor (ExpandTop > 0) so its container
// expands/collapses upward without shifting the text beneath.
func (h *host) setFocus(next string) {
	if next == h.focus {
		return
	}
	if h.focus != "" {
		h.edDrag = false
		if h.focusScrollID != "" {
			delta := h.focusScrollDelta
			if h.focusScrollPending {
				delta = h.scroll[h.focusScrollID] - h.focusScrollFrom
				h.focusScrollPending = false
			}
			if delta > 0 {
				h.scroll[h.focusScrollID] = max(0, h.scroll[h.focusScrollID]-delta)
				delete(h.scrollTo, h.focusScrollID)
			}
			h.focusScrollID, h.focusScrollDelta, h.focusScrollFrom = "", 0, 0
		}
	}
	h.focus = next
	if next != "" && h.last != nil {
		if t, ok := h.last.editorByID(next); ok && t.ExpandTop > 0 && len(h.last.scrolls) > 0 {
			a := h.last.scrolls[0]
			for _, s := range h.last.scrolls {
				if s.R.Contains(t.R.L+1, t.R.T+1) {
					a = s
				}
			}
			from := h.scroll[a.ID]
			h.scroll[a.ID] = from + t.ExpandTop
			delete(h.scrollTo, a.ID)
			h.focusScrollID = a.ID
			h.focusScrollFrom = from
			h.focusScrollDelta = t.ExpandTop
			h.focusScrollPending = true
		}
	}
}

// pressEditor focuses the editor clicked and hands it the click (counted
// for double and triple clicks); the button held selects with the pointer.
func (h *host) pressEditor(hit Hit, x, y float32) {
	h.CommitEdit()
	if h.sel != nil {
		h.sel = nil
	}
	h.goalX = -1
	dt, _, _ := procGetDoubleClickTime.Call()
	cx, _, _ := procGetSystemMetrics.Call(smCxDoubleClk)
	cy, _, _ := procGetSystemMetrics.Call(smCyDoubleClk)
	n := 1
	if time.Since(h.clickAt) <= time.Duration(dt)*time.Millisecond && abs32(x-h.clickX) <= float32(cx) && abs32(y-h.clickY) <= float32(cy) {
		n = h.clicks%3 + 1
	}
	h.clicks, h.clickAt, h.clickX, h.clickY = n, time.Now(), x, y
	if t, ok := h.last.editorByID(hit.ID); ok && t.Press != nil {
		t.Press(x, y, keyDown(vkShift), n)
	}
	h.setFocus(hit.ID)
	h.edDrag = true
	procSetCapture.Call(h.hwnd)
	h.Invalidate()
}

// editorChar hands a typed character (WM_CHAR, a UTF-16 unit) to the
// focused editor. Control characters (Enter, Tab, Backspace, Ctrl+letter)
// are its keys, handled at WM_KEYDOWN.
func (h *host) editorChar(u uint16) {
	t, ok := h.focused()
	if !ok || t.Char == nil {
		return
	}
	var r rune
	switch {
	case u >= 0xD800 && u < 0xDC00:
		h.surrogate = u
		return
	case u >= 0xDC00 && u < 0xE000:
		if h.surrogate == 0 {
			return
		}
		r = utf16.DecodeRune(rune(h.surrogate), rune(u))
		h.surrogate = 0
	case u < 0x20 || u == 0x7F:
		return
	default:
		r = rune(u)
	}
	t.Char(r, h.editorEnv())
	h.Invalidate()
}

// editorEnv is what the focused editor's keys may use.
func (h *host) editorEnv() *EditorEnv {
	return &EditorEnv{
		SetClipboard: func(text, html string) { _ = SetClipboardRich(h.hwnd, text, html) },
		Clipboard:    func() string { return ClipboardText(h.hwnd) },
		Blur:         func() { h.setFocus("") },
		GoalX:        &h.goalX,
	}
}

// Blur takes the keyboard from the focused editor.
func (h *host) Blur() {
	if h.focus != "" {
		h.setFocus("")
		h.Invalidate()
	}
}

// startEdit begins typing in field id (saving another field first).
func (h *host) startEdit(id, value string, maxLen int) {
	if h.edit != nil {
		if h.edit.ID == id {
			return
		}
		h.CommitEdit()
	}
	h.setFocus("") // a field takes the keyboard from an editor
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

// scrollSmoothly moves scroll area id to offset to using a critically-damped
// spring (velocity-preserving across rapid wheel turns, landing on a whole
// pixel so resting text stays crisp).
func (h *host) scrollSmoothly(id string, to float32) {
	if h.scrollTo == nil {
		h.scrollTo = map[string]float32{}
	}
	if h.scrollVel == nil {
		h.scrollVel = map[string]float32{}
	}
	to = float32(math.Round(float64(to)))
	if h.last != nil {
		if a, ok := h.last.ScrollArea(id); ok {
			to = max(0, min(a.Max, to))
		}
	}
	if len(h.scrollTo) == 0 {
		h.scrollTick = time.Now()
		h.setVsync(vsyncScroll, true)
	}
	h.scrollTo[id] = to
}

// stepScroll advances each moving scroll area along its analytical
// critically-damped spring and reports whether any scroll offset changed.
func (h *host) stepScroll() bool {
	if len(h.scrollTo) == 0 {
		h.setVsync(vsyncScroll, false)
		return false
	}
	now := time.Now()
	dt := now.Sub(h.scrollTick)
	h.scrollTick = now
	if dt <= 0 {
		return false
	}
	if dt > 100*time.Millisecond {
		dt = 100 * time.Millisecond
	}
	dtSec := dt.Seconds()
	const w = scrollSpringOmega
	e := math.Exp(-w * dtSec)
	changed := false
	for id, to := range h.scrollTo {
		maxS := float32(math.MaxFloat32)
		if h.last != nil {
			if a, ok := h.last.ScrollArea(id); ok {
				maxS = a.Max
				to = min(to, maxS) // the page got shorter meanwhile
			}
		}
		prev := h.scroll[id]
		x0 := float64(prev - to)
		v0 := float64(h.scrollVel[id])
		c2 := v0 + w*x0
		x := (x0 + c2*dtSec) * e
		v := (c2 - w*(x0+c2*dtSec)) * e
		cur := max(0, min(maxS, to+float32(x)))
		if math.Abs(x) < 0.5 && math.Abs(v) < 25 {
			cur = to
			delete(h.scrollTo, id)
			delete(h.scrollVel, id)
		} else {
			h.scrollVel[id] = float32(v)
		}
		if cur != prev {
			h.scroll[id] = cur
			changed = true
		}
	}
	if len(h.scrollTo) == 0 {
		h.setVsync(vsyncScroll, false)
	}
	if changed {
		h.Invalidate()
	}
	return changed
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

// commitToNext saves the field (Enter) and hands the keyboard to the editor
// or field that follows it, if it has one.
func (h *host) commitToNext() {
	next := ""
	if h.edit != nil && h.last != nil {
		next = h.last.nextOf(h.edit.ID)
	}
	h.focusAfterCommit(next)
}

// commitTab saves the field (Tab / Shift+Tab) and moves focus to the next or
// previous field or editor.
func (h *host) commitTab(shift bool) {
	target := ""
	if h.edit != nil && h.last != nil {
		target = h.last.tabTargetOf(h.edit.ID, shift)
	}
	h.focusAfterCommit(target)
}

func (h *host) focusAfterCommit(target string) {
	last := h.last
	h.CommitEdit()
	if target == "" {
		return
	}
	if last != nil {
		if in, ok := last.inputByID(target); ok {
			h.startEdit(in.id, in.value, in.max)
			return
		}
	}
	h.setFocus(target)
	h.goalX = -1
	h.Invalidate()
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
		h.commitToNext()
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
