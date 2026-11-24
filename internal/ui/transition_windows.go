package ui

import (
	"math"
	"time"
	"unsafe"
)

const (
	timerTransition = 6
	lwaAlpha        = 0x00000002
	swpNoZOrder     = 0x0004
)

var (
	procSetClassLongPtrW           = user32.NewProc("SetClassLongPtrW")
	procGetWindowLongPtrW          = user32.NewProc("GetWindowLongPtrW")
	procSetLayeredWindowAttributes = user32.NewProc("SetLayeredWindowAttributes")
	procUpdateWindow               = user32.NewProc("UpdateWindow")
	procFillRect                   = user32.NewProc("FillRect")
	procCreateSolidBrush           = gdi32.NewProc("CreateSolidBrush")
)

const gclpHbrBackground = ^uintptr(9) // -10

func setClassBgBrush(hwnd, brush uintptr) {
	if procSetClassLongPtrW.Find() == nil {
		procSetClassLongPtrW.Call(hwnd, gclpHbrBackground, brush)
	}
}

type winRect struct{ L, T, R, B int32 }

// transition drives a brief, hardware-composited alpha fade (and optional
// subtle centered scale) on a host window using WS_EX_LAYERED. When the
// transition finishes, WS_EX_LAYERED is removed so idle rendering has zero
// layered-window overhead.
type transition struct {
	active    bool
	fadeOut   bool
	start     time.Time
	dur       time.Duration
	scaleFrom float64
	scaleTo   float64
	hasRect   bool
	target    winRect // resting screen rect (restored on finish/cancel)
	baseEx    uintptr // GWL_EXSTYLE without WS_EX_LAYERED
	onDone    func()
}

func darkBrush(c interface{ RGBA() (r, g, b, a uint32) }) uintptr {
	b, _, _ := procCreateSolidBrush.Call(uintptr(colorref(c)))
	return b
}

func easeOutCubic(t float64) float64 {
	if t <= 0 {
		return 0
	}
	if t >= 1 {
		return 1
	}
	u := 1 - t
	return 1 - u*u*u
}

func scaledRect(r winRect, scale float64) winRect {
	if scale >= 0.9999 {
		return r
	}
	w := float64(r.R - r.L)
	h := float64(r.B - r.T)
	nw := math.Round(w * scale)
	nh := math.Round(h * scale)
	dx := int32(math.Round((w - nw) / 2))
	dy := int32(math.Round((h - nh) / 2))
	return winRect{L: r.L + dx, T: r.T + dy, R: r.L + dx + int32(nw), B: r.T + dy + int32(nh)}
}

func (h *host) enableLayered(alpha byte) {
	ex, _, _ := procGetWindowLongPtrW.Call(h.hwnd, gwlExStyle)
	if !h.trans.active {
		h.trans.baseEx = ex &^ uintptr(wsExLayered)
	}
	if ex&uintptr(wsExLayered) == 0 {
		procSetWindowLongPtrW.Call(h.hwnd, gwlExStyle, ex|uintptr(wsExLayered))
	}
	procSetLayeredWindowAttributes.Call(h.hwnd, 0, uintptr(alpha), lwaAlpha)
}

func (h *host) disableLayered() {
	ex, _, _ := procGetWindowLongPtrW.Call(h.hwnd, gwlExStyle)
	if ex&uintptr(wsExLayered) != 0 {
		procSetWindowLongPtrW.Call(h.hwnd, gwlExStyle, ex&^uintptr(wsExLayered))
	}
}

// cancelTransition stops any running fade/scale, restores the resting rect if
// scaled, and removes WS_EX_LAYERED.
func (h *host) cancelTransition() {
	if !h.trans.active {
		return
	}
	procKillTimer.Call(h.hwnd, timerTransition)
	if h.trans.hasRect && h.trans.target.R > h.trans.target.L && h.trans.target.B > h.trans.target.T {
		r := h.trans.target
		procSetWindowPos.Call(h.hwnd, 0, uintptr(r.L), uintptr(r.T), uintptr(r.R-r.L), uintptr(r.B-r.T), swpNoZOrder|swpNoActivate)
	}
	h.trans = transition{}
	h.disableLayered()
}

// beginFadeIn prepares the window at alpha=0 (and optional inset rect) before
// showing so frame 0 never flashes white. Call commitFadeIn right after the
// window is shown and painted.
func (h *host) beginFadeIn(dur time.Duration, scaleFrom float64, target winRect, hasRect bool) {
	h.cancelTransition()
	h.trans = transition{
		active:    true,
		fadeOut:   false,
		start:     time.Now(),
		dur:       dur,
		scaleFrom: scaleFrom,
		scaleTo:   1,
		hasRect:   hasRect && scaleFrom < 0.999,
		target:    target,
	}
	h.enableLayered(0)
	if h.trans.hasRect {
		sr := scaledRect(target, scaleFrom)
		procSetWindowPos.Call(h.hwnd, 0, uintptr(sr.L), uintptr(sr.T), uintptr(sr.R-sr.L), uintptr(sr.B-sr.T), swpNoZOrder|swpNoActivate)
	}
}

func (h *host) startTransitionTimer() {
	h.trans.start = time.Now()
	procSetTimer.Call(h.hwnd, timerTransition, 15, 0)
}

// startFadeOut fades the visible window out over dur (with optional subtle
// shrink around its center), then hides it, restores its resting rect, and
// invokes onDone.
func (h *host) startFadeOut(dur time.Duration, scaleTo float64, onDone func()) {
	h.cancelTransition()
	var rc winRect
	hasRect := false
	if scaleTo < 0.999 {
		if ret, _, _ := procGetWindowRect.Call(h.hwnd, uintptr(unsafe.Pointer(&rc))); ret != 0 && rc.R > rc.L && rc.B > rc.T {
			hasRect = true
		}
	}
	h.trans = transition{
		active:    true,
		fadeOut:   true,
		start:     time.Now(),
		dur:       dur,
		scaleFrom: 1,
		scaleTo:   scaleTo,
		hasRect:   hasRect,
		target:    rc,
		onDone:    onDone,
	}
	h.enableLayered(255)
	procSetTimer.Call(h.hwnd, timerTransition, 15, 0)
}

func (h *host) stepTransition() {
	if !h.trans.active {
		procKillTimer.Call(h.hwnd, timerTransition)
		return
	}
	p := float64(time.Since(h.trans.start)) / float64(h.trans.dur)
	if p >= 1 {
		procKillTimer.Call(h.hwnd, timerTransition)
		tr := h.trans
		h.trans = transition{}
		if tr.fadeOut {
			procShowWindow.Call(h.hwnd, swHide)
			if tr.hasRect {
				r := tr.target
				procSetWindowPos.Call(h.hwnd, 0, uintptr(r.L), uintptr(r.T), uintptr(r.R-r.L), uintptr(r.B-r.T), swpNoZOrder|swpNoActivate)
			}
			h.disableLayered()
			h.stopTimers()
			if tr.onDone != nil {
				tr.onDone()
			}
		} else {
			if tr.hasRect {
				r := tr.target
				procSetWindowPos.Call(h.hwnd, 0, uintptr(r.L), uintptr(r.T), uintptr(r.R-r.L), uintptr(r.B-r.T), swpNoZOrder|swpNoActivate)
			}
			procSetLayeredWindowAttributes.Call(h.hwnd, 0, 255, lwaAlpha)
			h.disableLayered()
		}
		return
	}
	e := easeOutCubic(p)
	var alpha byte
	if h.trans.fadeOut {
		alpha = byte(math.Round(255 * (1 - e)))
	} else {
		alpha = byte(math.Round(255 * e))
	}
	if h.trans.hasRect {
		sc := h.trans.scaleFrom + (h.trans.scaleTo-h.trans.scaleFrom)*e
		sr := scaledRect(h.trans.target, sc)
		procSetWindowPos.Call(h.hwnd, 0, uintptr(sr.L), uintptr(sr.T), uintptr(sr.R-sr.L), uintptr(sr.B-sr.T), swpNoZOrder|swpNoActivate)
	}
	procSetLayeredWindowAttributes.Call(h.hwnd, 0, uintptr(alpha), lwaAlpha)
}
