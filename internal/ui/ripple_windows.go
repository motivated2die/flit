package ui

import (
	"errors"
	"image/color"
	"math"
	"time"
	"unsafe"
)

const themeRippleDur = 360 * time.Millisecond

// themeRipple drives a GPU-composited circular wipe between two frozen theme
// snapshots (oldBrush -> newBrush) expanding from the theme toggle button at
// the monitor's native VSync refresh rate. UI thread only.
type themeRipple struct {
	oldBrush  *comObj
	newBrush  *comObj
	w, h      int32
	cx, cy    float32
	maxR      float32
	start     time.Time
	dur       time.Duration
	oldBorder color.RGBA
	newBorder color.RGBA
	hasBorder bool
	onDone    func()
}

func (r *themeRipple) armed() bool { return r.oldBrush != nil && r.newBrush == nil }
func (r *themeRipple) ready() bool { return r.oldBrush != nil && r.newBrush != nil }

func (r *themeRipple) free() {
	if r.oldBrush != nil {
		r.oldBrush.release()
		r.oldBrush = nil
	}
	if r.newBrush != nil {
		r.newBrush.release()
		r.newBrush = nil
	}
	r.w, r.h = 0, 0
	r.hasBorder = false
	r.onDone = nil
}

// lerpColorref linearly interpolates between two RGB colours and encodes the
// result as a Win32 COLORREF (0x00BBGGRR) for DWMWA_BORDER_COLOR.
func lerpColorref(a, b color.RGBA, t float64) uint32 {
	if t <= 0 {
		return uint32(a.R) | uint32(a.G)<<8 | uint32(a.B)<<16
	}
	if t >= 1 {
		return uint32(b.R) | uint32(b.G)<<8 | uint32(b.B)<<16
	}
	mix := func(x, y uint8) uint32 {
		return uint32(math.Round(float64(x) + (float64(y)-float64(x))*t))
	}
	return mix(a.R, b.R) | mix(a.G, b.G)<<8 | mix(a.B, b.B)<<16
}

func setDWMBorderColor(hwnd uintptr, cr uint32) {
	if hwnd != 0 && procDwmSetWindowAttr.Find() == nil {
		procDwmSetWindowAttr.Call(hwnd, dwmwaBorderColor, uintptr(unsafe.Pointer(&cr)), 4)
	}
}

// rippleMaxRadius returns the distance from (cx, cy) to the farthest corner of
// a w×h rectangle, plus a small margin for anti-aliased edge coverage.
func rippleMaxRadius(cx, cy, w, h float32) float32 {
	dx := max(cx, w-cx)
	dy := max(cy, h-cy)
	return float32(math.Hypot(float64(dx), float64(dy))) + 2
}

// captureThemeRipple snapshots the current rendered frame into a GPU bitmap
// brush before the global theme switches. When the window next paints in the
// new theme, it captures the new snapshot once and animates the circular wipe.
func (h *host) captureThemeRipple(cx, cy float32) {
	h.cancelRipple()
	if !h.Visible() || !h.hw.ready() || h.last == nil {
		return
	}
	cw, ch := h.clientSize()
	if cw <= 0 || ch <= 0 || uint32(cw) != h.hw.w || uint32(ch) != h.hw.h {
		return
	}
	oldCtx := h.last
	oldBrush, err := h.hw.CaptureBrush(func(p *Painter) { oldCtx.Paint(p) }, h.bg)
	if err != nil {
		return
	}
	h.ripple = themeRipple{
		oldBrush: oldBrush,
		w:        cw,
		h:        ch,
		cx:       cx,
		cy:       cy,
		maxR:     rippleMaxRadius(cx, cy, float32(cw), float32(ch)),
		dur:      themeRippleDur,
	}
}

// cancelRipple stops any active or armed theme ripple, releases its GPU
// brushes, and runs onDone (e.g. applying final DWM frame attributes).
func (h *host) cancelRipple() {
	done := h.ripple.onDone
	h.ripple.free()
	h.setVsync(vsyncRipple, false)
	if done != nil {
		done()
	}
}

// paintRippleFrame draws one VSync frame of the circular theme wipe using two
// GPU draw calls (old snapshot quad + new snapshot expanding ellipse). Returns
// false when the animation has finished so the caller falls through to a normal
// live paint.
func (h *host) paintRippleFrame(cw, ch int32) bool {
	if !h.ripple.ready() {
		return false
	}
	t := float64(time.Since(h.ripple.start)) / float64(h.ripple.dur)
	if t >= 1 {
		h.cancelRipple()
		return false
	}
	e := easeOutCubic(t)
	if h.ripple.hasBorder {
		setDWMBorderColor(h.hwnd, lerpColorref(h.ripple.oldBorder, h.ripple.newBorder, e))
	}
	p, err := h.hw.Begin(h.hwnd, cw, ch, h.bg)
	if err != nil {
		h.cancelRipple()
		if errors.Is(err, ErrRecreateTarget) {
			h.Invalidate()
			return true
		}
		return false
	}
	p.FillBrush(RectF{0, 0, float32(cw), float32(ch)}, h.ripple.oldBrush)
	rad := h.ripple.maxR * float32(e)
	p.FillEllipseBrush(h.ripple.cx, h.ripple.cy, rad, rad, h.ripple.newBrush)
	if err := p.End(); err != nil {
		h.cancelRipple()
		if errors.Is(err, ErrRecreateTarget) {
			h.hw.Free()
			h.Invalidate()
		}
		return true
	}
	return true
}
