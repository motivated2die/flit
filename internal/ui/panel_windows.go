package ui

import (
	"fmt"
	"time"
	"unsafe"
)

// Panel is the tray icon's popover: a small rounded window next to the
// taskbar, drawn like the main window, that says what flit is doing and
// offers what fits. It closes like a menu: on losing the focus, on Esc,
// or after an action. Its methods run on the UI thread.
type Panel struct {
	host
	width      float32 // logical
	hiddenAt   time.Time
	anchor     point
	lastHeight int32
	rounded    bool // Windows rounds its corners (11); the border follows them

	// Build returns the panel's content; it is laid out at the panel's
	// width and the panel takes its height.
	Build func() Node
	// Lit, when it reports true, lights the top left corner, where the
	// recording dot is (recording).
	Lit func() bool
	// OnHide runs when the panel closes.
	OnHide func()
}

const (
	dwmColorNone   = 0xFFFFFFFE // DWMWA_COLOR_NONE: no border
	panelRadius    = 8          // Windows 11's rounding of the panel (DWMWCP_ROUND), logical
	panelClass     = "flit.Panel"
	panelWidth     = 336
	panelPad       = 14
	panelMargin    = 10 // from the taskbar and screen edges
	monitorNearest = 2
	waInactive     = 0
)

var panelByHwnd = map[uintptr]*Panel{}
var panelClassDone bool

// NewPanel creates the (hidden) panel.
func NewPanel() (*Panel, error) {
	m, err := D2DMetrics()
	if err != nil {
		return nil, err
	}
	if !panelClassDone {
		cursor, _, _ := procLoadCursorW.Call(0, idcArrow)
		wc := wndClassEx{WndProc: windowsNewCallback(panelProc), Instance: moduleHandle(), ClassName: u16(panelClass), Cursor: cursor}
		wc.Size = uint32(unsafe.Sizeof(wc))
		if r, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
			return nil, fmt.Errorf("registering the panel class: %w", err)
		}
		panelClassDone = true
	}
	h, _, err := procCreateWindowExW.Call(wsExToolWindow|wsExTopmost, uintptr(unsafe.Pointer(u16(panelClass))), uintptr(unsafe.Pointer(u16("flit"))),
		wsPopup, 0, 0, 100, 100, 0, 0, moduleHandle(), 0)
	if h == 0 {
		return nil, fmt.Errorf("creating the panel: %w", err)
	}
	p := &Panel{host: host{hwnd: h, metrics: m, scroll: map[string]float32{}, bg: col(ColSidebar)}, width: panelWidth}
	p.layout = func(c *Ctx, w, hh float32) {
		if p.Build != nil {
			if node := p.Build(); node != nil {
				panelLayout(c, node, w, hh, p.Lit != nil && p.Lit(), p.rounded)
			}
		}
	}
	panelByHwnd[h] = p
	if procDwmSetWindowAttr.Find() == nil {
		round := uint32(2) // DWMWCP_ROUND
		r, _, _ := procDwmSetWindowAttr.Call(h, dwmwaCornerPref, uintptr(unsafe.Pointer(&round)), 4)
		p.rounded = r == 0
		border := uint32(dwmColorNone) // flit paints the border, lit by the glow
		procDwmSetWindowAttr.Call(h, dwmwaBorderColor, uintptr(unsafe.Pointer(&border)), 4)
		dark := uint32(1)
		procDwmSetWindowAttr.Call(h, dwmwaDarkMode, uintptr(unsafe.Pointer(&dark)), 4)
	}
	return p, nil
}

// RecentlyHidden reports whether the panel closed a moment ago (the click
// on the tray icon that took its focus away shouldn't open it again).
func (p *Panel) RecentlyHidden() bool { return time.Since(p.hiddenAt) < 300*time.Millisecond }

// ShowAt opens the panel next to the taskbar near pt (screen pixels).
func (p *Panel) ShowAt(x, y int32) {
	p.anchor = point{x, y}
	mon, _, _ := procMonitorFromPoint.Call(uintptr(uint32(x))|uintptr(uint32(y))<<32, monitorNearest)
	scale := float32(1)
	if procGetDpiForMonitor.Find() == nil {
		var dx, dy uint32
		if r, _, _ := procGetDpiForMonitor.Call(mon, 0, uintptr(unsafe.Pointer(&dx)), uintptr(unsafe.Pointer(&dy))); r == 0 && dx > 0 {
			scale = float32(dx) / 96
		}
	}
	p.scale = scale
	p.hover, p.pressed = "", ""
	p.place(true)
	procSetForegroundWindow.Call(p.hwnd)
	p.Invalidate()
}

// Hide closes the panel.
func (p *Panel) Hide() {
	if !p.Visible() {
		return
	}
	procShowWindow.Call(p.hwnd, swHide)
	p.stopTimers()
	p.hiddenAt = time.Now()
	if p.OnHide != nil {
		p.OnHide()
	}
}

// Refresh repaints the panel, refitting its height to the content.
func (p *Panel) Refresh() {
	if p.Visible() {
		p.place(false)
		p.Invalidate()
	}
}

// contentHeight lays the content out once to measure it.
func (p *Panel) contentHeight() int32 {
	w := float32(int32(p.width * p.scale))
	c := NewCtx(p.scale, p.metrics, time.Now(), "", "", nil)
	pad := c.Px(panelPad)
	h := float32(0)
	if p.Build != nil {
		if node := p.Build(); node != nil {
			h = node.Layout(c, pad, pad, w-2*pad)
		}
	}
	return int32(h + 2*pad)
}

// place sizes the panel to its content and puts it by the taskbar: above
// a bottom taskbar, below a top one, beside a side one.
func (p *Panel) place(show bool) {
	w, h := int32(p.width*p.scale), p.contentHeight()
	if !show && h == p.lastHeight {
		return
	}
	p.lastHeight = h
	mon, _, _ := procMonitorFromPoint.Call(uintptr(uint32(p.anchor.X))|uintptr(uint32(p.anchor.Y))<<32, monitorNearest)
	mi := monitorInfo{}
	mi.Size = uint32(unsafe.Sizeof(mi))
	procGetMonitorInfoW.Call(mon, uintptr(unsafe.Pointer(&mi)))
	x, y := panelPlacement(mi.Work, p.anchor, w, h, int32(panelMargin*p.scale))
	flags := uintptr(swpNoActivate)
	if show {
		flags = swpShowWindow
	}
	procSetWindowPos.Call(p.hwnd, hwndTopmost, uintptr(x), uintptr(y), uintptr(w), uintptr(h), flags)
}

// panelPlacement puts a w×h panel next to the taskbar near the anchor (the
// cursor on the tray icon), inside the work area.
func panelPlacement(work struct{ L, T, R, B int32 }, at point, w, h, m int32) (int32, int32) {
	clamp := func(v, lo, hi int32) int32 { return max(lo, min(v, hi)) }
	x := clamp(at.X-w/2, work.L+m, work.R-w-m)
	y := clamp(at.Y-h-m, work.T+m, work.B-h-m)
	switch {
	case at.Y >= work.B: // taskbar at the bottom
		y = work.B - h - m
	case at.Y < work.T: // at the top
		y = work.T + m
	case at.X < work.L: // on the left
		x, y = work.L+m, clamp(at.Y-h/2, work.T+m, work.B-h-m)
	case at.X >= work.R: // on the right
		x, y = work.R-w-m, clamp(at.Y-h/2, work.T+m, work.B-h-m)
	}
	return x, y
}

func panelProc(hwnd uintptr, m uint32, wp, lp uintptr) uintptr {
	p := panelByHwnd[hwnd]
	if p == nil {
		return defWindowProc(hwnd, m, wp, lp)
	}
	switch m {
	case wmActivate:
		if wp&0xFFFF == waInactive {
			p.Hide() // closes like a menu
		}
		return 0
	case wmKeyDown:
		if wp == vkEscape {
			p.Hide()
			return 0
		}
	case wmMouseActivate:
		return 1 // MA_ACTIVATE
	case wmClose:
		p.Hide()
		return 0
	case wmDpiChanged:
		p.scale = float32(wp&0xFFFF) / 96
		p.place(false)
		return 0
	}
	if r, ok := p.handle(m, wp, lp); ok {
		return r
	}
	return defWindowProc(hwnd, m, wp, lp)
}

// PanelSheet splits the tray panel into a darker top extension (ColBase,
// where the live recording dot and corner glow sit) emerging from behind a
// lighter bottom card (ColSidebar) with rounded top corners.
type PanelSheet struct {
	Top  Node
	Body Node
}

func (s PanelSheet) Layout(c *Ctx, x, y, w float32) float32 {
	pad := c.Px(panelPad)
	th := float32(0)
	if s.Top != nil {
		th = s.Top.Layout(c, x, y, w)
	}
	bh := float32(0)
	if s.Body != nil {
		bh = s.Body.Layout(c, x, y+th+2*pad, w)
	}
	return th + 2*pad + bh
}

// panelLight is the colour the border turns to near the glow: the live
// red, lightened.
var panelLight = rgb(0xF8, 0xA8, 0xB2)

// panelLayout draws the panel on a w×h surface: its background, the
// corner glow while lit, the content n, and its border on top.
func panelLayout(c *Ctx, n Node, w, h float32, lit, rounded bool) {
	pad := c.Px(panelPad)
	if sheet, ok := n.(PanelSheet); ok {
		// Darker top extension behind a lighter, top-rounded bottom card.
		c.Draw(func(cv Canvas) { cv.RoundRect(RectF{0, 0, w, h}, 0, col(ColBase)) })
		if lit {
			c.Animating = true
			pulse := float32(Pulse(time.Duration(c.Now.UnixNano()), notchPulse))
			c.Glow(0, 0, liveGlow, ColLive, 0.75+0.25*pulse)
		}
		th := float32(0)
		if sheet.Top != nil {
			th = sheet.Top.Layout(c, pad, pad, w-2*pad)
		}
		splitY := th + 2*pad
		cardR := c.Px(12)
		line := max(1, c.Px(1))
		c.Draw(func(cv Canvas) {
			cv.RoundRect(RectF{0, splitY - line, w, h + cardR}, cardR+line, col(ColEdge))
			cv.RoundRect(RectF{0, splitY, w, h + cardR}, cardR, col(ColSidebar))
		})
		if sheet.Body != nil {
			sheet.Body.Layout(c, pad, splitY+pad, w-2*pad)
		}
	} else {
		c.Draw(func(cv Canvas) { cv.RoundRect(RectF{0, 0, w, h}, 0, col(ColSidebar)) })
		if lit {
			c.Animating = true
			pulse := float32(Pulse(time.Duration(c.Now.UnixNano()), notchPulse))
			c.Glow(0, 0, liveGlow, ColLive, 0.75+0.25*pulse)
		}
		n.Layout(c, pad, pad, w-2*pad)
	}
	iw, ih := int(w), int(h)
	r := 0.0
	if rounded {
		r = float64(panelRadius * c.Scale)
	}
	rx, ry := float64(borderReach*c.Px(liveGlow.RX)), float64(borderReach*c.Px(liveGlow.RY))
	key := fmt.Sprintf("border:%dx%d:%v:%v", iw, ih, r, lit)
	c.Draw(func(cv Canvas) {
		cv.Bitmap(key, 0, 0, iw, ih, 1, func() []byte {
			return borderPixels(iw, ih, r, ColEdge, panelLight, lit, rx, ry, liveGlow)
		})
	})
}

// RenderPanel draws panel content offscreen (tests, docs), lit as while
// recording or not.
func RenderPanel(n Node, scale float32, hover string, lit bool) (*Surface, error) {
	m, err := D2DMetrics()
	if err != nil {
		return nil, err
	}
	w := float32(int32(panelWidth * scale))
	c := NewCtx(scale, m, time.Now(), "", "", nil)
	pad := c.Px(panelPad)
	h := n.Layout(c, pad, pad, w-2*pad) + 2*pad
	return render(func(c *Ctx, cw, ch float32) {
		panelLayout(c, n, cw, ch, lit, true)
	}, int32(w), int32(h), scale, col(ColSidebar), hover, nil)
}
