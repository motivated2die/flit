package ui

import (
	"fmt"
	"math"
	"strings"
	"time"
	"unsafe"
)

// FloatWindow is a compact, always-on-top, borderless rounded window that
// stays above other apps (WS_EX_TOPMOST | WS_EX_TOOLWINDOW) without closing
// on focus loss. It has no native title bar: the 6-dot drag handle (:drag)
// moves it natively via HTCAPTION, all corners and edges resize it within
// [MinW..MaxW] × [MinH..MaxH], and its content scrolls smoothly inside.
type FloatWindow struct {
	host
	placed      bool
	dragArm     bool
	dragStart   PointF
	moving      bool
	preDragRect struct{ L, T, R, B int32 }
	hasPreDrag  bool
	startMove   func() // test hook; nil sends WM_NCLBUTTONDOWN(HTCAPTION)

	// Build returns the content node (a NotesEditor in pinned mode).
	Build func() Node
	// OnClose runs on WM_CLOSE (e.g. Alt+F4); if nil, the window hides.
	OnClose func()
	// OnDragMove runs while the floating window is being dragged by the user
	// (screen coordinates of the cursor and the proposed window top edge).
	OnDragMove func(cursorX, cursorY, winTop int32)
	// OnDragEnd runs when a drag finishes. Returning true signals that the
	// window was stashed/consumed by the caller.
	OnDragEnd func(cursorX, cursorY, winTop int32) bool
}

const (
	floatClass    = "flit.Float"
	floatScrollID = "float"

	wsThickFrame = 0x00040000

	wmNcPaint       = 0x0085
	wmNcActivate    = 0x0086
	wmNcLButtonDown = 0x00A1
	wmMoving        = 0x0216
	wmEnterSizeMove = 0x0231
	wmExitSizeMove  = 0x0232

	htLeft        = 10
	htRight       = 11
	htBottom      = 15
	htBottomLeft  = 16
	htBottomRight = 17

	floatDefaultW      = 420
	floatDefaultH      = 440
	floatMinW          = 380
	floatMinH          = 220
	floatMaxW          = 680
	floatMaxH          = 860
	floatPad           = 12
	floatEdgeBand      = 6
	floatCornerBand    = 14
	floatDragThreshold = 4
)

var floatByHwnd = map[uintptr]*FloatWindow{}
var floatClassDone bool
var floatBgBrush uintptr

// NewFloatWindow creates the (hidden) always-on-top floating window.
func NewFloatWindow(title string) (*FloatWindow, error) {
	m, err := D2DMetrics()
	if err != nil {
		return nil, err
	}
	if !floatClassDone {
		cursor, _, _ := procLoadCursorW.Call(0, idcArrow)
		floatBgBrush = darkBrush(ColCard)
		wc := wndClassEx{Style: csHRedraw | csVRedraw, WndProc: windowsNewCallback(floatProc), Instance: moduleHandle(), ClassName: u16(floatClass), Cursor: cursor, Background: floatBgBrush}
		wc.Size = uint32(unsafe.Sizeof(wc))
		if r, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
			return nil, fmt.Errorf("registering the float class: %w", err)
		}
		floatClassDone = true
	}
	h, _, err := procCreateWindowExW.Call(wsExToolWindow|wsExTopmost, uintptr(unsafe.Pointer(u16(floatClass))), uintptr(unsafe.Pointer(u16(title))),
		wsPopup|wsThickFrame, 0, 0, floatDefaultW, floatDefaultH, 0, 0, moduleHandle(), 0)
	if h == 0 {
		return nil, fmt.Errorf("creating the float window: %w", err)
	}
	f := &FloatWindow{host: host{hwnd: h, metrics: m, scroll: map[string]float32{}, bg: col(ColCard), bgBrush: floatBgBrush}}
	f.layout = func(c *Ctx, cw, ch float32) {
		f.layoutContent(c, cw, ch)
	}
	floatByHwnd[h] = f
	f.setDPI(f.dpiScale())
	procSetWindowPos.Call(h, 0, 0, 0, 0, 0, swpFrameChange|swpNoActivate|0x0001|0x0002|0x0004)
	if procDwmSetWindowAttr.Find() == nil {
		round := uint32(2) // DWMWCP_ROUND
		procDwmSetWindowAttr.Call(h, dwmwaCornerPref, uintptr(unsafe.Pointer(&round)), 4)
		dark := uint32(1)
		procDwmSetWindowAttr.Call(h, dwmwaDarkMode, uintptr(unsafe.Pointer(&dark)), 4)
		border := colorref(ColEdge)
		procDwmSetWindowAttr.Call(h, dwmwaBorderColor, uintptr(unsafe.Pointer(&border)), 4)
	}
	return f, nil
}

func (f *FloatWindow) layoutContent(c *Ctx, w, h float32) {
	c.Draw(func(cv Canvas) { cv.RoundRect(RectF{0, 0, w, h}, 0, col(ColCard)) })
	if f.Build == nil {
		return
	}
	node := f.Build()
	if node == nil {
		return
	}
	pad := c.Px(floatPad)
	off := float32(math.Round(float64(c.Scroll[floatScrollID])))
	view := RectF{0, 0, w, h}
	c.PushClip(view)
	ph := node.Layout(c, pad, pad-off, w-2*pad)
	c.PopClip()

	maxScroll := max(0, ph+2*pad-h)
	if c.Scroll[floatScrollID] > maxScroll {
		c.Scroll[floatScrollID] = maxScroll
	}
	c.AddScroll(ScrollArea{ID: floatScrollID, R: view, Max: maxScroll, View: view})
	if k := c.keepView; k != nil {
		margin := c.Px(24)
		want := float32(0)
		switch {
		case k[0] < view.T+margin:
			want = k[0] - (view.T + margin)
		case k[1] > view.B-margin:
			want = k[1] - (view.B - margin)
		}
		if want != 0 {
			c.WantScroll = &ScrollTarget{ID: floatScrollID, To: max(0, min(maxScroll, off+want))}
		}
		c.keepView = nil
	}
	if maxScroll > 0 {
		top := c.Px(8)
		track := h - 2*top
		th := max(c.Px(28), track*h/(ph+2*pad))
		ty := top + (track-th)*min(1, off/maxScroll)
		bar := RectF{w - c.Px(scrollbarW+3), ty, w - c.Px(3), ty + th}
		c.Draw(func(cv Canvas) { cv.RoundRect(bar, bar.W()/2, RGBA(ColText, 0.18)) })
	}
}

// ShowNear brings the floating window up on top of other windows with a
// subtle fade-in and enlarge (94% → 100% over ~150 ms). The first time it
// opens, it positions itself near (ax, ay) (or the right side of the primary
// work area when hasAnchor is false); subsequent opens remember the user's
// last position and size.
func (f *FloatWindow) ShowNear(ax, ay int32, hasAnchor bool) {
	if !f.placed {
		f.place(ax, ay, hasAnchor)
		f.placed = true
	}
	wasHidden := !f.Visible() || (f.trans.active && f.trans.fadeOut)
	if wasHidden {
		f.cancelTransition()
		var target winRect
		hasRect := false
		if ret, _, _ := procGetWindowRect.Call(f.hwnd, uintptr(unsafe.Pointer(&target))); ret != 0 && target.R > target.L && target.B > target.T {
			hasRect = true
		}
		f.beginFadeIn(150*time.Millisecond, 0.94, target, hasRect)
	} else {
		f.cancelTransition()
	}
	procSetWindowPos.Call(f.hwnd, hwndTopmost, 0, 0, 0, 0, swpShowWindow|swpNoSize|swpNoMove)
	procSetForegroundWindow.Call(f.hwnd)
	f.Invalidate()
	if wasHidden {
		procUpdateWindow.Call(f.hwnd)
		f.startTransitionTimer()
	}
}

// FadeHide fades and subtly shrinks the floating window out (~100 ms) before
// hiding it at its resting rect (used when returning to the main window).
func (f *FloatWindow) FadeHide() {
	if !f.Visible() {
		return
	}
	f.CommitEdit()
	f.startFadeOut(100*time.Millisecond, 0.96, nil)
}

// RestorePreDrag moves the window back to where it sat before the last drag
// (used when unstashing by click after dragging to the top edge).
func (f *FloatWindow) RestorePreDrag() {
	if !f.hasPreDrag {
		return
	}
	r := f.preDragRect
	f.hasPreDrag = false
	if r.R > r.L && r.B > r.T {
		f.placed = true
		procSetWindowPos.Call(f.hwnd, hwndTopmost, uintptr(r.L), uintptr(r.T), uintptr(r.R-r.L), uintptr(r.B-r.T), swpNoActivate)
	}
}

// ShowAtDrag brings the floating window up centred horizontally under (cx, cy)
// and hands off to native caption dragging so the user can pull it straight
// out of the notch in one continuous motion.
func (f *FloatWindow) ShowAtDrag(cx, cy int32) {
	f.cancelTransition()
	f.hasPreDrag = false
	var cur struct{ L, T, R, B int32 }
	procGetWindowRect.Call(f.hwnd, uintptr(unsafe.Pointer(&cur)))
	ww, wh := cur.R-cur.L, cur.B-cur.T
	if ww <= 0 || wh <= 0 || !f.placed {
		f.place(cx, cy, false)
		f.placed = true
		procGetWindowRect.Call(f.hwnd, uintptr(unsafe.Pointer(&cur)))
		ww, wh = cur.R-cur.L, cur.B-cur.T
	}
	mon, _, _ := procMonitorFromPoint.Call(uintptr(uint32(cx))|uintptr(uint32(cy))<<32, monitorNearest)
	mi := monitorInfo{}
	mi.Size = uint32(unsafe.Sizeof(mi))
	procGetMonitorInfoW.Call(mon, uintptr(unsafe.Pointer(&mi)))
	work := mi.Work
	s := max(float32(1), f.scale)
	x := max(work.L, min(cx-ww/2, work.R-ww))
	y := max(work.T, min(cy-int32(26*s), work.B-wh))
	procSetWindowPos.Call(f.hwnd, hwndTopmost, uintptr(x), uintptr(y), uintptr(ww), uintptr(wh), swpShowWindow)
	procSetForegroundWindow.Call(f.hwnd)
	f.Invalidate()
	if f.startMove != nil {
		f.startMove()
		return
	}
	lp := uintptr(uint16(int16(cx))) | uintptr(uint16(int16(cy)))<<16
	procSendMessageW.Call(f.hwnd, wmNcLButtonDown, htCaption, lp)
}

// Hide hides the floating window.
func (f *FloatWindow) Hide() {
	if !f.Visible() {
		return
	}
	f.CommitEdit()
	procShowWindow.Call(f.hwnd, swHide)
	f.stopTimers()
}

func (f *FloatWindow) place(ax, ay int32, hasAnchor bool) {
	var mon uintptr
	if hasAnchor {
		mon, _, _ = procMonitorFromPoint.Call(uintptr(uint32(ax))|uintptr(uint32(ay))<<32, monitorNearest)
	} else {
		mon, _, _ = procMonitorFromPoint.Call(0, monitorPrimary)
	}
	if procGetDpiForMonitor.Find() == nil {
		var dx, dy uint32
		if r, _, _ := procGetDpiForMonitor.Call(mon, 0, uintptr(unsafe.Pointer(&dx)), uintptr(unsafe.Pointer(&dy))); r == 0 && dx > 0 {
			f.setDPI(float32(dx) / 96)
		}
	}
	mi := monitorInfo{}
	mi.Size = uint32(unsafe.Sizeof(mi))
	procGetMonitorInfoW.Call(mon, uintptr(unsafe.Pointer(&mi)))
	work := mi.Work
	s := f.scale
	ww := min(int32(floatDefaultW*s), work.R-work.L)
	wh := min(int32(floatDefaultH*s), work.B-work.T)
	margin := int32(24 * s)
	x := work.R - ww - margin
	y := work.T + margin
	if hasAnchor {
		x = max(work.L+margin, min(ax-ww, work.R-ww-margin))
		y = max(work.T+margin, min(ay, work.B-wh-margin))
	}
	procSetWindowPos.Call(f.hwnd, hwndTopmost, uintptr(x), uintptr(y), uintptr(ww), uintptr(wh), swpNoActivate)
}

// hitTest maps screen coordinates to corner/edge resize zones, HTCAPTION on
// the 6-dot drag handle (:drag), or HTCLIENT everywhere else.
func (f *FloatWindow) hitTest(lp uintptr) uintptr {
	pt := point{int32(int16(lp & 0xFFFF)), int32(int16(lp >> 16 & 0xFFFF))}
	procScreenToClient.Call(f.hwnd, uintptr(unsafe.Pointer(&pt)))
	cw, ch := f.clientSize()
	return f.hitTestClient(pt.X, pt.Y, cw, ch)
}

func (f *FloatWindow) hitTestClient(x, y, cw, ch int32) uintptr {
	edge := int32(float32(floatEdgeBand) * f.scale)
	corner := int32(float32(floatCornerBand) * f.scale)
	switch {
	case x < corner && y < corner:
		return htTopLeft
	case x >= cw-corner && y < corner:
		return htTopRight
	case x < corner && y >= ch-corner:
		return htBottomLeft
	case x >= cw-corner && y >= ch-corner:
		return htBottomRight
	case x < edge:
		return htLeft
	case x >= cw-edge:
		return htRight
	case y < edge:
		return htTop
	case y >= ch-edge:
		return htBottom
	}
	if f.last != nil {
		if hit, ok := f.last.HitAt(float32(x), float32(y)); ok && (strings.HasSuffix(hit.ID, ":drag") || strings.HasSuffix(hit.ID, ":bar")) {
			return htCaption
		}
	}
	return htClient
}

func (f *FloatWindow) limitSize(minTrack, maxTrack *point) {
	s := f.scale
	minTrack.X, minTrack.Y = int32(floatMinW*s), int32(floatMinH*s)
	maxW, maxH := int32(floatMaxW*s), int32(floatMaxH*s)
	mon, _, _ := procMonitorFromWindow.Call(f.hwnd, monitorDefaultToNearest)
	mi := monitorInfo{}
	mi.Size = uint32(unsafe.Sizeof(mi))
	if r, _, _ := procGetMonitorInfoW.Call(mon, uintptr(unsafe.Pointer(&mi))); r != 0 {
		if workW := mi.Work.R - mi.Work.L; workW > minTrack.X {
			maxW = min(maxW, workW)
		}
		if workH := mi.Work.B - mi.Work.T; workH > minTrack.Y {
			maxH = min(maxH, workH)
		}
	}
	maxTrack.X, maxTrack.Y = maxW, maxH
}

func floatProc(hwnd uintptr, m uint32, wp, lp uintptr) uintptr {
	f := floatByHwnd[hwnd]
	if f == nil {
		return defWindowProc(hwnd, m, wp, lp)
	}
	switch m {
	case wmLButtonDown:
		f.dragArm = false
		if f.last != nil {
			x, y := float32(int16(lp&0xFFFF)), float32(int16(lp>>16&0xFFFF))
			if hit, ok := f.last.HitAt(x, y); ok && strings.Contains(hit.ID, ":tool:") {
				f.dragArm = true
				f.dragStart = PointF{x, y}
			}
		}
	case wmMouseMove:
		if f.dragArm {
			x, y := float32(int16(lp&0xFFFF)), float32(int16(lp>>16&0xFFFF))
			thresh := max(float32(floatDragThreshold), floatDragThreshold*f.scale)
			if abs32(x-f.dragStart.X) >= thresh || abs32(y-f.dragStart.Y) >= thresh {
				f.dragArm = false
				f.pressed = ""
				f.blurOnUp = false
				f.Invalidate()
				procReleaseCapture.Call()
				if f.startMove != nil {
					f.startMove()
				} else {
					procSendMessageW.Call(hwnd, wmNcLButtonDown, htCaption, 0)
				}
				return 0
			}
		}
	case wmLButtonUp, wmCaptureChange:
		f.dragArm = false
	}
	if r, ok := f.handle(m, wp, lp); ok {
		return r
	}
	switch m {
	case wmNcCalcSize:
		if wp != 0 {
			return 0 // entire window is client area: no native title bar
		}
	case wmNcPaint:
		// Suppress classic Win32 non-client border painting; DWM draws the
		// rounded 1 px ColEdge border.
		return 0
	case wmNcActivate:
		// Allow activation state change without letting DefWindowProc paint a
		// thick classic white WS_THICKFRAME border on deactivation.
		return 1
	case wmNcHitTest:
		return f.hitTest(lp)
	case wmGetMinMaxInfo:
		mmi := (*struct {
			Reserved, MaxSize, MaxPos, MinTrack, MaxTrack point
		})(cptrUI(lp))
		f.limitSize(&mmi.MinTrack, &mmi.MaxTrack)
		return 0
	case wmDpiChanged:
		f.setDPI(float32(wp&0xFFFF) / 96)
		r := (*struct{ L, T, R, B int32 })(cptrUI(lp))
		procSetWindowPos.Call(hwnd, hwndTopmost, uintptr(r.L), uintptr(r.T), uintptr(r.R-r.L), uintptr(r.B-r.T), swpNoActivate)
		f.Invalidate()
		return 0
	case wmEnterSizeMove:
		f.moving = false
		var r struct{ L, T, R, B int32 }
		if res, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r))); res != 0 {
			f.preDragRect, f.hasPreDrag = r, true
		}
		return 0
	case wmMoving:
		f.moving = true
		r := (*struct{ L, T, R, B int32 })(cptrUI(lp))
		var pt point
		procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
		if f.OnDragMove != nil {
			f.OnDragMove(pt.X, pt.Y, r.T)
		}
		return 1
	case wmExitSizeMove:
		wasMoving := f.moving
		f.moving = false
		if wasMoving && f.OnDragEnd != nil {
			var r struct{ L, T, R, B int32 }
			procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
			var pt point
			procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
			if f.OnDragEnd(pt.X, pt.Y, r.T) {
				return 0
			}
		}
		f.hasPreDrag = false
		return 0
	case wmClose:
		if f.OnClose != nil {
			f.OnClose()
		} else {
			f.Hide()
		}
		return 0
	}
	return defWindowProc(hwnd, m, wp, lp)
}
