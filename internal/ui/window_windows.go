package ui

import (
	"fmt"
	"image"
	"time"
	"unsafe"
)

// Window is flit's main window: a normal Windows window (resizing, snapping,
// the system menu, the taskbar all native) with a dark frame, whose client
// area is drawn by a Frame. The native title bar is folded into the client
// area: the Frame draws minimise and close, and the top CaptionH pixels
// drag the window wherever nothing clickable is (see WM_NCCALCSIZE and
// WM_NCHITTEST below). Its methods run on the UI thread.
type Window struct {
	host
	placed bool
	icons  [2]uintptr
	// The dock shown and since when (it slides up when it changes), its
	// latest content, and the one sliding out after its page was left.
	dockID   string
	dockAt   time.Time
	dockNode Node
	leaving  Node
	// Whether a popover is open, and since when (it fades in).
	modalOn bool
	modalAt time.Time

	// Build returns what to show; it is called on every repaint.
	Build func() Frame
	// OnHide runs when the window is closed (hidden) or minimised.
	OnHide func()
	// OnEscape runs when Esc is pressed and no text is being edited
	// (closing a popover).
	OnEscape func()
	// OnBack runs when the mouse browser Back button (XBUTTON1) or a
	// browser-back APPCOMMAND is triggered.
	OnBack func()
}

const (
	windowClass = "flit.Window"

	wsOverlappedWindow = 0x00CF0000
	csHRedraw          = 0x0002
	csVRedraw          = 0x0001
	swShow             = 5
	swRestore          = 9
	smCxIcon           = 11
	sizeMinimized      = 1
	swMinimize         = 6

	wmNcCalcSize          = 0x0083
	wmNcHitTest           = 0x0084
	wmXButtonUp           = 0x020C
	wmAppCommand          = 0x0319
	xButton1              = 1
	appCommandBrowserBack = 1
	htClient              = 1
	htCaption      = 2
	htTop          = 12
	htTopLeft      = 13
	htTopRight     = 14
	smCyFrame      = 33
	smCxPaddedBord = 92
	swpFrameChange = 0x0020

	dwmwaDarkMode     = 20
	dwmwaCornerPref   = 33
	dwmwaBorderColor  = 34
	dwmwaCaptionColor = 35
	dwmwaTextColor    = 36
)

var (
	procIsIconic         = user32.NewProc("IsIconic")
	procIsZoomed         = user32.NewProc("IsZoomed")
	procGetSysMetricsDpi = user32.NewProc("GetSystemMetricsForDpi")
	dwmapi               = mustLazy("dwmapi.dll")
	procDwmSetWindowAttr = dwmapi.NewProc("DwmSetWindowAttribute")
)

var windowByHwnd = map[uintptr]*Window{}
var windowClassDone bool

// Assert that the Direct2D painter can draw views.
var _ Canvas = (*Painter)(nil)

// NewWindow creates the (hidden) window.
func NewWindow(title string) (*Window, error) {
	m, err := D2DMetrics()
	if err != nil {
		return nil, err
	}
	if !windowClassDone {
		cursor, _, _ := procLoadCursorW.Call(0, idcArrow)
		wc := wndClassEx{Style: csHRedraw | csVRedraw, WndProc: windowsNewCallback(windowProc), Instance: moduleHandle(), ClassName: u16(windowClass), Cursor: cursor}
		wc.Size = uint32(unsafe.Sizeof(wc))
		if r, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
			return nil, fmt.Errorf("registering the window class: %w", err)
		}
		windowClassDone = true
	}
	h, _, err := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(u16(windowClass))), uintptr(unsafe.Pointer(u16(title))),
		wsOverlappedWindow, 0x80000000, 0x80000000, 960, 660, 0, 0, moduleHandle(), 0) // CW_USEDEFAULT
	if h == 0 {
		return nil, fmt.Errorf("creating the window: %w", err)
	}
	w := &Window{host: host{hwnd: h, metrics: m, scroll: map[string]float32{}, bg: col(ColBase)}}
	w.layout = func(c *Ctx, cw, ch float32) {
		if w.Build != nil {
			f := w.Build()
			f.Caption, f.OnMinimize, f.OnClose = true, w.minimize, w.Hide
			if f.DockID != w.dockID {
				// Left a page with a dock for one without: the old dock
				// slides out over the new page.
				w.leaving = nil
				if f.DockID == "" && w.dockNode != nil {
					w.leaving = w.dockNode
				}
				w.dockID, w.dockAt, w.dockNode = f.DockID, c.Now, nil
			}
			switch {
			case f.DockID != "":
				f.DockAt, w.dockNode = w.dockAt, f.Dock
			case w.leaving != nil && c.Now.Sub(w.dockAt) < dockSlide*time.Millisecond:
				f.Dock, f.DockAt, f.DockOut = w.leaving, w.dockAt, true
			default:
				w.leaving = nil
			}
			if on := f.Modal != nil; on != w.modalOn {
				w.modalOn, w.modalAt = on, c.Now
			}
			if f.Modal != nil {
				f.ModalAt = w.modalAt
			}
			f.Layout(c, cw, ch)
		}
	}
	windowByHwnd[h] = w
	w.scale = w.dpiScale()
	// Recompute the frame now that windowProc knows the window: the
	// caption becomes client area.
	procSetWindowPos.Call(h, 0, 0, 0, 0, 0, swpFrameChange|swpNoActivate|0x0001|0x0002|0x0004) // NOSIZE|NOMOVE|NOZORDER
	darkFrame(h, ColSidebar)
	w.setIcons()
	return w, nil
}

// darkFrame gives a window's native frame flit's colours (Windows 11;
// Windows 10 takes the dark mode and ignores the rest).
func darkFrame(hwnd uintptr, caption interface{ RGBA() (r, g, b, a uint32) }) {
	if procDwmSetWindowAttr.Find() != nil {
		return
	}
	set := func(attr uintptr, v uint32) {
		procDwmSetWindowAttr.Call(hwnd, attr, uintptr(unsafe.Pointer(&v)), 4)
	}
	set(dwmwaDarkMode, 1)
	set(dwmwaCaptionColor, colorref(caption))
	set(dwmwaTextColor, colorref(ColText))
	set(dwmwaBorderColor, colorref(ColEdge))
}

func colorref(c interface{ RGBA() (r, g, b, a uint32) }) uint32 {
	r, g, b, _ := c.RGBA()
	return (r >> 8) | (g>>8)<<8 | (b>>8)<<16
}

// setIcons gives the window its big and small icon (taskbar, Alt+Tab,
// caption) drawn at exactly the size the window's DPI asks for, so Windows
// never scales them.
func (w *Window) setIcons() {
	metric := func(idx uintptr) int {
		if procGetSysMetricsDpi.Find() == nil {
			n, _, _ := procGetSysMetricsDpi.Call(idx, uintptr(w.scale*96+0.5))
			return int(n)
		}
		n, _, _ := procGetSystemMetrics.Call(idx)
		return int(n)
	}
	for i, size := range []int{metric(smCxIcon), metric(smCxSmIcon)} {
		if size <= 0 {
			size = 32
		}
		ic := newIcon(AppIcon(size))
		if ic != 0 {
			procSendMessageW.Call(w.hwnd, wmSetIcon, uintptr(1-i), ic) // ICON_BIG = 1, ICON_SMALL = 0
			if w.icons[i] != 0 {
				procDestroyIcon.Call(w.icons[i])
			}
			w.icons[i] = ic
		}
	}
}

// Show brings the window up, centred on the main screen the first time.
func (w *Window) Show() {
	if !w.placed {
		w.place()
		w.placed = true
	}
	if ic, _, _ := procIsIconic.Call(w.hwnd); ic != 0 {
		procShowWindow.Call(w.hwnd, swRestore)
	} else {
		procShowWindow.Call(w.hwnd, swShow)
	}
	procSetForegroundWindow.Call(w.hwnd)
	w.Invalidate()
}

// Shown reports whether the window is visible and not minimised.
func (w *Window) Shown() bool {
	ic, _, _ := procIsIconic.Call(w.hwnd)
	return w.Visible() && ic == 0
}

// Rect returns the window's screen rectangle (left, top, right, bottom).
func (w *Window) Rect() (l, t, r, b int32, ok bool) {
	var rc struct{ L, T, R, B int32 }
	if ret, _, _ := procGetWindowRect.Call(w.hwnd, uintptr(unsafe.Pointer(&rc))); ret == 0 {
		return 0, 0, 0, 0, false
	}
	return rc.L, rc.T, rc.R, rc.B, true
}

// Hide hides the window (flit keeps running in the tray).
func (w *Window) Hide() {
	w.CommitEdit() // closing the window saves what was typed
	procShowWindow.Call(w.hwnd, swHide)
	w.stopTimers()
	if w.OnHide != nil {
		w.OnHide()
	}
}

func (w *Window) minimize() { procShowWindow.Call(w.hwnd, swMinimize) }

// frameY is the thickness of the sizing frame at the window's DPI: what a
// maximised window overhangs the screen by, and the top resize band.
func (w *Window) frameY() int32 {
	dpi := uintptr(w.scale*96 + 0.5)
	if procGetSysMetricsDpi.Find() == nil {
		f, _, _ := procGetSysMetricsDpi.Call(smCyFrame, dpi)
		p, _, _ := procGetSysMetricsDpi.Call(smCxPaddedBord, dpi)
		return int32(f + p)
	}
	f, _, _ := procGetSystemMetrics.Call(smCyFrame)
	p, _, _ := procGetSystemMetrics.Call(smCxPaddedBord)
	return int32(f + p)
}

func (w *Window) zoomed() bool {
	z, _, _ := procIsZoomed.Call(w.hwnd)
	return z != 0
}

// hitTest says what the point (screen coordinates) is for Windows: the top
// resize band, the caption strip (drag, double-click, system menu) where
// nothing clickable is drawn, or the client area.
func (w *Window) hitTest(lp uintptr) uintptr {
	pt := point{int32(int16(lp & 0xFFFF)), int32(int16(lp >> 16 & 0xFFFF))}
	procScreenToClient.Call(w.hwnd, uintptr(unsafe.Pointer(&pt)))
	cw, _ := w.clientSize()
	if !w.zoomed() && pt.Y >= 0 && pt.Y < w.frameY() {
		switch band := w.frameY() * 2; {
		case pt.X < band:
			return htTopLeft
		case pt.X >= cw-band:
			return htTopRight
		}
		// The caption buttons reach the top edge, as in Windows' own.
		if w.last != nil {
			if _, ok := w.last.HitAt(float32(pt.X), float32(pt.Y)); ok {
				return htClient
			}
		}
		return htTop
	}
	if pt.Y >= 0 && float32(pt.Y) < float32(CaptionH)*w.scale {
		if w.last != nil {
			if _, ok := w.last.HitAt(float32(pt.X), float32(pt.Y)); ok {
				return htClient
			}
		}
		return htCaption
	}
	return htClient
}

var (
	procGetWindowRect     = user32.NewProc("GetWindowRect")
	procMonitorFromWindow = user32.NewProc("MonitorFromWindow")
)

const monitorDefaultToNearest = 2

// place centres the window on the main screen's work area at its default
// size for the screen's scale.
func (w *Window) place() {
	mon, _, _ := procMonitorFromPoint.Call(0, monitorPrimary)
	mi := monitorInfo{}
	mi.Size = uint32(unsafe.Sizeof(mi))
	procGetMonitorInfoW.Call(mon, uintptr(unsafe.Pointer(&mi)))
	s := w.scale
	ww, wh := int32(940*s), int32(660*s)
	work := mi.Work
	ww, wh = min(ww, work.R-work.L), min(wh, work.B-work.T)
	x := work.L + (work.R-work.L-ww)/2
	y := work.T + (work.B-work.T-wh)/2
	procSetWindowPos.Call(w.hwnd, 0, uintptr(x), uintptr(y), uintptr(ww), uintptr(wh), swpNoActivate|0x0004) // SWP_NOZORDER
}

func windowProc(hwnd uintptr, m uint32, wp, lp uintptr) uintptr {
	w := windowByHwnd[hwnd]
	if w == nil {
		return defWindowProc(hwnd, m, wp, lp)
	}
	if r, ok := w.handle(m, wp, lp); ok {
		if m == wmSize && wp == sizeMinimized && w.OnHide != nil {
			w.OnHide()
		}
		return r
	}
	switch m {
	case wmNcCalcSize:
		if wp == 0 {
			break
		}
		// Let Windows size the frame, then give the caption to the client
		// area; maximised, keep clear of the part that overhangs the screen.
		r := (*struct{ L, T, R, B int32 })(cptrUI(lp)) // NCCALCSIZE_PARAMS.rgrc[0]
		top := r.T
		defWindowProc(hwnd, m, wp, lp)
		r.T = top
		if w.zoomed() {
			r.T += w.frameY()
		}
		return 0
	case wmNcHitTest:
		if hit := defWindowProc(hwnd, m, wp, lp); hit != htClient {
			return hit // the side and bottom frame stay native
		}
		return w.hitTest(lp)
	case wmKeyDown:
		if wp == vkEscape && w.OnEscape != nil {
			w.OnEscape()
			return 0
		}
	case wmXButtonUp:
		if (wp>>16)&0xFFFF == xButton1 && w.OnBack != nil {
			w.OnBack()
			return 1
		}
	case wmAppCommand:
		if (lp>>16)&0x0FFF == appCommandBrowserBack && w.OnBack != nil {
			w.OnBack()
			return 1
		}
	case wmClose:
		w.Hide() // flit lives on in the tray
		return 0
	case wmGetMinMaxInfo:
		mmi := (*struct {
			Reserved, MaxSize, MaxPos, MinTrack, MaxTrack point
		})(cptrUI(lp))
		mmi.MinTrack = point{int32(720 * w.scale), int32(480 * w.scale)}
		return 0
	case wmDpiChanged:
		w.scale = float32(wp&0xFFFF) / 96
		r := (*struct{ L, T, R, B int32 })(cptrUI(lp))
		procSetWindowPos.Call(hwnd, 0, uintptr(r.L), uintptr(r.T), uintptr(r.R-r.L), uintptr(r.B-r.T), swpNoActivate|0x0004)
		w.setIcons()
		w.Invalidate()
		return 0
	}
	return defWindowProc(hwnd, m, wp, lp)
}

// RenderFrame draws a frame offscreen at w×h device pixels (tests, and
// pictures of the window for the docs).
func RenderFrame(f Frame, w, h int32, scale float32, hover string, scroll map[string]float32) (*image.RGBA, error) {
	s, err := render(func(c *Ctx, cw, ch float32) { f.Layout(c, cw, ch) }, w, h, scale, col(ColBase), hover, scroll)
	if err != nil {
		return nil, err
	}
	defer s.Free()
	return SurfaceImage(s), nil
}

// RenderFrameFocused is RenderFrame with an editor having the keyboard
// (its caret drawn).
func RenderFrameFocused(f Frame, w, h int32, scale float32, focus string, scroll map[string]float32) (*image.RGBA, error) {
	s, err := render(func(c *Ctx, cw, ch float32) { c.Focus = focus; f.Layout(c, cw, ch) }, w, h, scale, col(ColBase), "", scroll)
	if err != nil {
		return nil, err
	}
	defer s.Free()
	return SurfaceImage(s), nil
}

// LayoutFrame lays a frame out without drawing it, an editor having the
// keyboard (tests: the pass's click targets and editors).
func LayoutFrame(f Frame, w, h int32, scale float32, focus string) (*Ctx, error) {
	m, err := D2DMetrics()
	if err != nil {
		return nil, err
	}
	c := NewCtx(scale, m, time.Now(), "", "", nil)
	c.Focus = focus
	f.Layout(c, float32(w), float32(h))
	return c, nil
}

// SurfaceImage copies an opaque surface into an image.
func SurfaceImage(s *Surface) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, int(s.W), int(s.H)))
	for i := 0; i < int(s.W*s.H); i++ {
		img.Pix[4*i], img.Pix[4*i+1], img.Pix[4*i+2], img.Pix[4*i+3] = s.Bits[4*i+2], s.Bits[4*i+1], s.Bits[4*i], 255
	}
	return img
}
