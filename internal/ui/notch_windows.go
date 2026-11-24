package ui

import (
	"fmt"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procSetTimer            = user32.NewProc("SetTimer")
	procKillTimer           = user32.NewProc("KillTimer")
	procUpdateLayeredWindow = user32.NewProc("UpdateLayeredWindow")
	procSetWindowLongPtrW   = user32.NewProc("SetWindowLongPtrW")
	procGetDC               = user32.NewProc("GetDC")
	procReleaseDC           = user32.NewProc("ReleaseDC")
	procShowWindow          = user32.NewProc("ShowWindow")
	procSetWindowPos        = user32.NewProc("SetWindowPos")
	procTrackMouseEvent     = user32.NewProc("TrackMouseEvent")
	procLoadCursorW         = user32.NewProc("LoadCursorW")
	procSetCursor           = user32.NewProc("SetCursor")
	procMonitorFromPoint    = user32.NewProc("MonitorFromPoint")
	procGetMonitorInfoW     = user32.NewProc("GetMonitorInfoW")
	procGetDpiForMonitor    = windows.NewLazySystemDLL("shcore.dll").NewProc("GetDpiForMonitor")
)

const (
	notchClass = "flit.Notch"

	wsPopup          = 0x80000000
	wsExLayered      = 0x00080000
	wsExToolWindow   = 0x00000080
	wsExTopmost      = 0x00000008
	wsExNoActivate   = 0x08000000
	wsExTransparent  = 0x00000020
	gwlExStyle       = ^uintptr(19) // GWL_EXSTYLE = -20
	wmTimer          = 0x0113
	wmMouseMove      = 0x0200
	wmLButtonDown    = 0x0201
	wmMouseLeave     = 0x02A3
	wmMouseActivate  = 0x0021
	wmSetCursor      = 0x0020
	wmDisplayChange  = 0x007E
	wmDpiChanged     = 0x02E0
	maNoActivate     = 3
	swHide           = 0
	swShowNoActivate = 4
	swpNoMove        = 0x0002
	swpNoSize        = 0x0001
	swpNoActivate    = 0x0010
	swpShowWindow    = 0x0040
	ulwAlpha         = 0x2
	tmeLeave         = 0x2
	idcHand          = 32649
	idcArrow         = 32512
	monitorPrimary   = 1
	timerFrame       = 1
	frameFast        = 15 // ms, while something moves
	frameSlow        = 80 // ms, for the pulse, the clock and the levels
	hwndTopmost      = ^uintptr(0)
)

type monitorInfo struct {
	Size          uint32
	Monitor, Work struct{ L, T, R, B int32 }
	Flags         uint32
}

type notchPhase int

const (
	phHidden notchPhase = iota
	phShowing
	phShown
	phHiding
)

// Notch is the window. Its methods run on the UI thread.
type Notch struct {
	hwnd  uintptr
	surf  *Surface
	dpi   float32
	zoom  float32
	scale float32
	work  struct{ L, T, R, B int32 }

	view       NotchView
	phase      notchPhase
	phaseStart time.Time
	shownAt    time.Time
	born       time.Time

	fromW, toW  float32
	fromH, toH  float32
	resizeStart time.Time
	wSp, hSp    spring1D
	riseSp      spring1D
	springTick  time.Time
	vsync       vsyncPacer
	vsyncOn     bool

	col           Collapser
	transparent   bool    // collapsed: clicks go through to what lies behind
	hoverProgress float64 // resting on the collapsed capsule, 0..1
	dirty         bool    // something changed since the last frame
	hover         bool
	hoverBtn      string
	pressBtn      string
	stashPress    bool  // mouse button held on the stashed-notes grab handle
	stashDown     point // screen cursor where stashPress began
	levels        [2]float32
	progress      float32
	timer         uintptr // current interval, 0 = off

	// OnButton runs when a button is clicked.
	OnButton func(id string)
	// OnUnstash runs when the user clicks or drags down on the stashed-notes
	// handle in the notch. drag is true when pulled out via downward drag.
	OnUnstash func(cursorX, cursorY int32, drag bool)
	// OnAutoHide runs when an AutoHide view's timer expires and it begins hiding.
	OnAutoHide func()
	// Levels, if set, is polled while recording for the mic's and the
	// meeting's peaks since the last call (0..1).
	Levels func() (me, them float32)
}

var notchByHwnd = map[uintptr]*Notch{}
var notchClassDone bool

// NewNotch creates the (hidden) window.
func NewNotch() (*Notch, error) {
	if _, err := getD2D(); err != nil {
		return nil, err
	}
	if !notchClassDone {
		cursor, _, _ := procLoadCursorW.Call(0, idcArrow)
		wc := wndClassEx{WndProc: windows.NewCallback(notchProc), Instance: moduleHandle(), ClassName: u16(notchClass), Cursor: cursor}
		wc.Size = uint32(unsafe.Sizeof(wc))
		if r, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
			return nil, fmt.Errorf("registering the notch class: %w", err)
		}
		notchClassDone = true
	}
	h, _, err := procCreateWindowExW.Call(wsExLayered|wsExToolWindow|wsExTopmost|wsExNoActivate,
		uintptr(unsafe.Pointer(u16(notchClass))), uintptr(unsafe.Pointer(u16("flit"))), wsPopup, 0, 0, 0, 0, 0, 0, moduleHandle(), 0)
	if h == 0 {
		return nil, fmt.Errorf("creating the notch: %w", err)
	}
	n := &Notch{hwnd: h}
	notchByHwnd[h] = n
	return n, nil
}

// Visible reports whether the notch is on screen or coming in.
func (n *Notch) Visible() bool { return n.phase == phShowing || n.phase == phShown }

// View returns what the notch shows.
func (n *Notch) View() NotchView { return n.view }

// Show shows v, sliding in if hidden or changing shape if already shown.
func (n *Notch) Show(v NotchView) {
	now := time.Now()
	wasVisible := n.Visible()
	if !wasVisible {
		n.place()
	}
	oldW, oldH := n.currentSize(now)
	if v.Mode != n.view.Mode || !wasVisible {
		n.born = now
		if v.StartCollapsed && v.Collapsible {
			n.col.Collapse()
		} else {
			n.col.Expand(now) // new information: show it open first
		}
		n.setTransparent(n.col.Collapsed && !v.StashedNotes && v.Mode != NotchStashed)
	} else if v.StartCollapsed && v.Collapsible && !n.col.Collapsed {
		n.col.Collapse()
		n.hover, n.hoverBtn, n.pressBtn = false, "", ""
		n.setTransparent(!v.StashedNotes && v.Mode != NotchStashed)
	} else if v.StashedNotes != n.view.StashedNotes {
		n.setTransparent(n.col.Collapsed && !v.StashedNotes && v.Mode != NotchStashed)
	}
	n.view = v
	n.shownAt = now
	n.dirty = true
	target := n.layout(now)
	if wasVisible {
		n.fromW, n.toW, n.fromH, n.toH, n.resizeStart = oldW, target.W, oldH, target.H, now
		n.wSp.target, n.hSp.target = float64(target.W), float64(target.H)
	} else {
		n.fromW, n.toW, n.fromH, n.toH = target.W, target.W, target.H, target.H
		n.wSp.snap(float64(target.W))
		n.hSp.snap(float64(target.H))
		n.riseSp.snap(0)
		n.riseSp.target = 1
		n.springTick = now
		n.phase, n.phaseStart = phShowing, now
		if n.born.IsZero() {
			n.born = now
		}
	}
	n.tick()
}

// Hide slides the notch out.
func (n *Notch) Hide() {
	if !n.Visible() {
		return
	}
	n.phase, n.phaseStart = phHiding, time.Now()
	n.hover, n.hoverBtn = false, ""
	n.setTransparent(false)
	n.tick()
}

// InStashZone reports whether a floating window being dragged (cursor at
// (cx, cy), window top edge at winTop, in screen pixels) is in the top-center
// drop zone to stash notes into the notch.
func (n *Notch) InStashZone(cx, cy, winTop int32) bool {
	if n.scale == 0 || n.work.R <= n.work.L {
		n.place()
	}
	s := max(float32(1), n.scale)
	centerX := (n.work.L + n.work.R) / 2
	halfBand := max(int32(260*s), (n.work.R-n.work.L)/3)
	if cx < centerX-halfBand || cx > centerX+halfBand {
		return false
	}
	return cy <= n.work.T+int32(64*s) || winTop <= n.work.T+int32(10*s)
}

// showingStashedHandle reports whether the notch is currently displaying the
// interactive stashed-notes grab handle (either idle or collapsed while recording).
func (n *Notch) showingStashedHandle() bool {
	return n.view.Mode == NotchStashed || (n.col.Collapsed && n.view.StashedNotes)
}

// CollapseNow shrinks a collapsible notch into its compact state immediately.
func (n *Notch) CollapseNow() {
	if !n.Visible() || !n.view.Collapsible {
		return
	}
	n.col.Collapse()
	n.hover = false
	n.setTransparent(!n.view.StashedNotes)
	n.dirty = true
	n.tick()
}

// SetZoom changes the notch's UI scale multiplier (e.g. 0.8, 1.0, 1.1, 1.25).
// RefreshTheme repaints the notch if it is currently visible.
func (n *Notch) RefreshTheme() {
	if n.Visible() {
		n.present(1)
	}
}

func (n *Notch) SetZoom(z float32) {
	if z <= 0 {
		z = 1
	}
	if z == n.zoom {
		return
	}
	n.zoom = z
	if n.Visible() {
		n.place()
		now := time.Now()
		lay := n.layout(now)
		n.fromW, n.toW, n.fromH, n.toH = lay.W, lay.W, lay.H, lay.H
		n.wSp.snap(float64(lay.W))
		n.hSp.snap(float64(lay.H))
		n.dirty = true
		n.tick()
		return
	}
	n.scale = 0 // re-place and reallocate surface on next Show
}

// place sizes the window for the primary screen and puts it at the top
// centre of its work area.
func (n *Notch) place() {
	mon, _, _ := procMonitorFromPoint.Call(0, monitorPrimary)
	mi := monitorInfo{}
	mi.Size = uint32(unsafe.Sizeof(mi))
	procGetMonitorInfoW.Call(mon, uintptr(unsafe.Pointer(&mi)))
	n.work = mi.Work
	dpi := float32(1)
	if procGetDpiForMonitor.Find() == nil {
		var dx, dy uint32
		if r, _, _ := procGetDpiForMonitor.Call(mon, 0, uintptr(unsafe.Pointer(&dx)), uintptr(unsafe.Pointer(&dy))); r == 0 && dx > 0 {
			dpi = float32(dx) / 96
		}
	}
	n.dpi = dpi
	zoom := n.zoom
	if zoom <= 0 {
		zoom = 1
	}
	scale := dpi * zoom
	if scale != n.scale || n.surf == nil {
		n.scale = scale
		n.surf.Free()
		w, h := notchSurfaceSize(scale)
		s, err := NewSurface(w, h)
		if err != nil {
			n.surf = nil
			return
		}
		n.surf = s
	}
}

func (n *Notch) layout(now time.Time) NotchLayout {
	if n.col.Collapsed {
		return CollapsedLayoutFor(n.view, n.scale, Measure)
	}
	return Layout(n.view, n.scale, now, Measure)
}

// hiddenCollapsed reports whether the collapsed state is fully hidden instead
// of showing the small capsule (stashed notes always keep their grab handle).
func (n *Notch) hiddenCollapsed() bool {
	return n.view.HideCollapsed && !n.view.StashedNotes && n.view.Mode != NotchStashed
}

func (n *Notch) currentSize(now time.Time) (float32, float32) {
	if n.wSp.pos == 0 && n.toW > 0 {
		return max(1, min(n.toW, notchMaxW*n.scale)), max(0, n.toH)
	}
	w, h := float32(n.wSp.pos), float32(n.hSp.pos)
	return max(1, min(w, notchMaxW*n.scale)), max(0, h)
}

func (n *Notch) currentWidth(now time.Time) float32 {
	w, _ := n.currentSize(now)
	return w
}

// setTransparent makes the window let clicks through (collapsed) or take
// them (open).
func (n *Notch) setTransparent(on bool) {
	if on == n.transparent {
		return
	}
	n.transparent = on
	style := uintptr(wsExLayered | wsExToolWindow | wsExTopmost | wsExNoActivate)
	if on {
		style |= wsExTransparent
	}
	procSetWindowLongPtrW.Call(n.hwnd, gwlExStyle, style)
}

// overCollapsed reports whether the pointer rests on the collapsed capsule
// (polled: a click-through window gets no mouse messages). A few pixels
// around it count, as the capsule is small.
func (n *Notch) overCollapsed(w, h float32) bool {
	var pt point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	cx := float32(n.work.L+n.work.R) / 2
	if n.hiddenCollapsed() {
		w = max(w, 64*n.scale)
	}
	m := 6 * n.scale
	r := RectF{cx - w/2 - m, float32(n.work.T) - m, cx + w/2 + m, float32(n.work.T) + h + m}
	return r.Contains(float32(pt.X), float32(pt.Y))
}

// tick advances the animation, draws a frame and sets the timer.
func (n *Notch) tick() {
	if n.surf == nil {
		return
	}
	now := time.Now()
	dt := now.Sub(n.springTick).Seconds()
	n.springTick = now
	var slide, rise float64
	switch n.phase {
	case phShowing:
		n.riseSp.target = 1
		if !n.riseSp.step(dt, notchRiseOmega, notchRiseZeta, 0.002, 0.05) {
			n.phase, slide, rise = phShown, 1, 1
		} else {
			rise = n.riseSp.pos
			slide = max(0, min(1, rise))
		}
	case phShown:
		slide, rise = 1, 1
		if n.view.AutoHide > 0 && !n.hover && now.Sub(n.shownAt) >= n.view.AutoHide {
			n.phase, n.phaseStart = phHiding, now
			if n.OnAutoHide != nil {
				n.OnAutoHide()
			}
		}
	case phHiding:
		p := float64(now.Sub(n.phaseStart)) / float64(notchSlideOut)
		slide = 1 - p
		rise = 1 - EaseIn(p) // brisk tuck upward and away
		if p >= 1 {
			n.phase = phHidden
			n.setVsync(false)
			n.setTimer(0)
			procShowWindow.Call(n.hwnd, swHide)
			return
		}
	case phHidden:
		return
	}

	if n.view.Mode == NotchRecording && n.Levels != nil {
		me, them := n.Levels()
		for i, v := range []float32{LevelMeter(me), LevelMeter(them)} {
			// Rise at once, fall gently.
			n.levels[i] = max(v, n.levels[i]*0.82)
		}
	}
	target := float32(n.view.Progress)
	if n.view.Progress < 0 {
		n.progress = -1
	} else {
		if n.progress < 0 {
			n.progress = 0
		}
		n.progress += (target - n.progress) * 0.25
	}

	// Collapse when left alone, expand when the pointer rests on it.
	wasCollapsed := n.col.Collapsed
	curW, curH := n.currentSize(now)
	over := n.col.Collapsed && n.phase == phShown && !n.stashPress && n.overCollapsed(curW, curH)
	policy := CollapsePolicy{Collapsible: n.view.Collapsible, HoverDelay: n.view.HoverDelay}
	hp := n.col.StepWithPolicy(now, policy, n.hover, over)
	if hp != n.hoverProgress {
		n.dirty = true
	}
	n.hoverProgress = hp
	if n.col.Collapsed != wasCollapsed {
		n.dirty = true
		n.setTransparent(n.col.Collapsed && !n.view.StashedNotes && n.view.Mode != NotchStashed)
		if !n.col.Collapsed {
			// Opened by hovering: the pointer is on it; leaving starts the
			// count to collapse again.
			n.hover = true
			n.trackLeave()
			n.hoverProgress = 0
		}
	}

	lay := n.layout(now)
	if lay.W != n.toW || lay.H != n.toH { // collapsed/opened, the clock widened
		n.fromW, n.fromH = curW, curH
		n.toW, n.toH, n.resizeStart = lay.W, lay.H, now
		n.wSp.target, n.hSp.target = float64(lay.W), float64(lay.H)
	}
	wOmega, wZeta := notchExpandWOmega, notchExpandWZeta
	if n.wSp.target < n.wSp.pos {
		wOmega, wZeta = notchShrinkWOmega, notchShrinkWZeta
	}
	hOmega, hZeta := notchExpandHOmega, notchExpandHZeta
	if n.hSp.target < n.hSp.pos {
		hOmega, hZeta = notchShrinkHOmega, notchShrinkHZeta
	}
	resizingW := n.wSp.step(dt, wOmega, wZeta, 0.25, 8)
	resizingH := n.hSp.step(dt, hOmega, hZeta, 0.25, 8)
	resizing := resizingW || resizingH
	width, height := n.currentSize(now)
	content := float32(1)
	if resizing && (n.fromW != n.toW || n.fromH != n.toH) && n.view.Mode != NotchStashDrop {
		// The content fades in on an S-curve as the shape nears its size.
		content = float32(Smoothstep(0.25, 0.8, float64(now.Sub(n.resizeStart))/float64(notchResize)))
	}
	cd := n.view.CountdownAt(now)
	counting := cd >= 0 && cd < 1
	moving := n.phase == phShowing || n.phase == phHiding || resizing || counting ||
		(n.view.Mode == NotchWorking && n.progress >= 0 && absf(target-n.progress) > 0.002)
	// A collapsed, still capsule needs no new frames: only the pointer is
	// polled. The live dot and the prompt's dot always move (except when the
	// collapsed recording capsule is completely hidden).
	hidden := n.hiddenCollapsed()
	live := (n.view.Mode == NotchRecording && !(n.col.Collapsed && hidden)) || n.view.Mode == NotchPrompt || (n.view.Mode == NotchWorking && n.view.Progress < 0)
	if moving || live || n.dirty || hp > 0 || !n.col.Collapsed {
		f := notchFrame{view: n.view, layout: lay, scale: n.scale, slide: slide, rise: rise, width: width, height: height, content: content,
			collapsed: n.col.Collapsed, hidden: hidden, hover: n.hover, hoverProgress: hp,
			hoverBtn: n.hoverBtn, pressBtn: n.pressBtn, levels: n.levels, progress: n.progress, now: now, born: n.born}
		if err := renderNotch(n.surf, f); err == nil {
			n.present(slide)
		}
		n.dirty = false
	}

	switch {
	case moving || hp > 0:
		n.setTimer(0)
		n.setVsync(true)
	case live || n.view.AutoHide > 0 || n.view.Collapsible:
		n.setVsync(false)
		n.setTimer(frameSlow)
	default:
		n.setVsync(false)
		n.setTimer(0)
	}
}

func (n *Notch) setVsync(on bool) {
	if on == n.vsyncOn {
		return
	}
	n.vsyncOn = on
	if on {
		n.vsync.start(n.hwnd)
	} else {
		n.vsync.stopPacer()
	}
}

func absf(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

func (n *Notch) setTimer(ms uintptr) {
	if ms == n.timer {
		return
	}
	if n.timer != 0 {
		procKillTimer.Call(n.hwnd, timerFrame)
	}
	if ms != 0 {
		procSetTimer.Call(n.hwnd, timerFrame, ms, 0)
	}
	n.timer = ms
}

// present puts the frame on screen, faded by how far it slid in.
func (n *Notch) present(slide float64) {
	w, h := n.surf.W, n.surf.H
	dst := point{n.work.L + (n.work.R-n.work.L-w)/2, n.work.T}
	size := struct{ CX, CY int32 }{w, h}
	src := point{}
	alpha := uint32(max(0, min(255, EaseOut(slide)*255)))
	blend := uint32(0 | 0<<8 | alpha<<16 | 1<<24) // AC_SRC_OVER, constant alpha, AC_SRC_ALPHA
	screen, _, _ := procGetDC.Call(0)
	procUpdateLayeredWindow.Call(n.hwnd, screen, uintptr(unsafe.Pointer(&dst)), uintptr(unsafe.Pointer(&size)),
		n.surf.dc, uintptr(unsafe.Pointer(&src)), 0, uintptr(unsafe.Pointer(&blend)), ulwAlpha)
	procReleaseDC.Call(0, screen)
	procSetWindowPos.Call(n.hwnd, hwndTopmost, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate|swpShowWindow)
}

// trackLeave asks for WM_MOUSELEAVE when the pointer leaves the window.
func (n *Notch) trackLeave() {
	tme := struct {
		Size, Flags uint32
		Track       uintptr
		Hover       uint32
	}{Flags: tmeLeave, Track: n.hwnd}
	tme.Size = uint32(unsafe.Sizeof(tme))
	procTrackMouseEvent.Call(uintptr(unsafe.Pointer(&tme)))
}

// toCapsule converts window coordinates to the capsule's.
func (n *Notch) toCapsule(l uintptr) (float32, float32) {
	x, y := float32(int16(l&0xFFFF)), float32(int16(l>>16&0xFFFF))
	w, h := n.currentSize(time.Now())
	f := notchFrame{layout: n.layout(time.Now()), width: w, height: h, slide: 1, rise: 1, scale: n.scale}
	ox, oy := f.capsuleOrigin(n.surf.W)
	cx := ox + (f.width-f.layout.W)/2
	return x - cx, y - oy
}

func notchProc(hwnd uintptr, m uint32, w, l uintptr) uintptr {
	n := notchByHwnd[hwnd]
	if n == nil {
		return defWindowProc(hwnd, m, w, l)
	}
	switch m {
	case wmTimer:
		n.tick()
		return 0
	case wmVsync:
		n.vsync.ack()
		n.tick()
		return 0
	case wmDestroy:
		n.setVsync(false)
		n.setTimer(0)
		return 0
	case wmMouseActivate:
		return maNoActivate
	case wmMouseMove:
		if n.stashPress {
			var pt point
			procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
			thresh := int32(max(6, 6*n.scale))
			if pt.Y-n.stashDown.Y >= thresh || abs32(float32(pt.X-n.stashDown.X)) >= float32(thresh)*1.5 {
				n.stashPress = false
				procReleaseCapture.Call()
				if n.OnUnstash != nil {
					n.OnUnstash(pt.X, pt.Y, true)
				}
			}
			return 0
		}
		if !n.hover {
			n.hover = true
			n.trackLeave()
		}
		n.dirty = true
		x, y := n.toCapsule(l)
		n.hoverBtn = n.layout(time.Now()).HitButton(x, y)
		n.tick()
		return 0
	case wmMouseLeave:
		n.hover, n.hoverBtn, n.pressBtn = false, "", ""
		n.shownAt = time.Now()  // auto-hide counts again from leaving
		n.col.Touch(time.Now()) // and so does collapsing
		n.dirty = true
		n.tick()
		return 0
	case wmLButtonDown:
		if n.showingStashedHandle() {
			var pt point
			procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
			n.stashPress = true
			n.stashDown = pt
			procSetCapture.Call(n.hwnd)
			return 0
		}
		n.dirty = true
		n.col.Touch(time.Now())
		x, y := n.toCapsule(l)
		n.pressBtn = n.layout(time.Now()).HitButton(x, y)
		n.tick()
		return 0
	case wmLButtonUp:
		if n.stashPress {
			n.stashPress = false
			procReleaseCapture.Call()
			var pt point
			procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
			if n.OnUnstash != nil {
				n.OnUnstash(pt.X, pt.Y, false)
			}
			return 0
		}
		x, y := n.toCapsule(l)
		id := n.layout(time.Now()).HitButton(x, y)
		pressed := n.pressBtn
		n.pressBtn = ""
		n.tick()
		if id != "" && id == pressed && n.OnButton != nil {
			n.OnButton(id)
		}
		return 0
	case wmCaptureChange:
		n.stashPress = false
		return 0
	case wmSetCursor:
		cursor := uintptr(idcArrow)
		if n.hoverBtn != "" || n.showingStashedHandle() {
			cursor = idcHand
		}
		c, _, _ := procLoadCursorW.Call(0, cursor)
		procSetCursor.Call(c)
		return 1
	case wmDisplayChange, wmDpiChanged:
		if n.Visible() {
			n.place()
			n.tick()
		}
		return 0
	}
	return defWindowProc(hwnd, m, w, l)
}
