package ui

import (
	"math"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

func TestVsyncPacerCoalescesAndStops(t *testing.T) {
	ticks := make(chan struct{}, 16)
	var posted atomic.Int32
	postCh := make(chan struct{}, 16)

	p := &vsyncPacer{
		wait: func() { <-ticks },
		post: func(uintptr) {
			posted.Add(1)
			postCh <- struct{}{}
		},
	}

	p.start(1)
	defer p.stopPacer()

	// Fire three VSyncs without acking: only one message should be posted.
	ticks <- struct{}{}
	select {
	case <-postCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first vsync post")
	}
	ticks <- struct{}{}
	ticks <- struct{}{}
	time.Sleep(15 * time.Millisecond)
	if got := posted.Load(); got != 1 {
		t.Fatalf("posted = %d while unacked, want 1", got)
	}

	// Ack and fire again: second message should post.
	p.ack()
	ticks <- struct{}{}
	select {
	case <-postCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for second vsync post after ack")
	}
	if got := posted.Load(); got != 2 {
		t.Fatalf("posted = %d after ack, want 2", got)
	}

	// Stop the pacer and unblock the waiting goroutine: no more posts.
	p.ack()
	p.stopPacer()
	ticks <- struct{}{}
	time.Sleep(15 * time.Millisecond)
	if got := posted.Load(); got != 2 {
		t.Fatalf("posted = %d after stop, want 2", got)
	}
}

func TestHostWheelDirectVsSmooth(t *testing.T) {
	h := &host{
		scale:  1,
		scroll: map[string]float32{"page:a": 0},
		vsync: vsyncPacer{
			wait: func() { time.Sleep(time.Hour) },
			post: func(uintptr) {},
		},
	}
	defer h.stopTimers()

	c := NewCtx(1, fakeMetrics{}, time.Now(), "", "", h.scroll)
	c.AddScroll(ScrollArea{ID: "page:a", R: RectF{0, 0, 500, 500}, Max: 1000})
	h.last = c

	// Fractional delta (precision touchpad): applies immediately, no vsyncScroll.
	deltaFrac := int16(-30)
	h.handle(wmMouseWheel, uintptr(uint16(deltaFrac))<<16, 0)
	if h.vsyncMask&vsyncScroll != 0 {
		t.Fatalf("fractional wheel delta activated vsyncScroll (mask=%#x)", h.vsyncMask)
	}
	wantFrac := float32(30) / 120 * scrollStep
	if got := h.scroll["page:a"]; got != wantFrac {
		t.Fatalf("fractional scroll = %v, want %v", got, wantFrac)
	}

	// Detented wheel notch (±120): queues smooth scroll and activates vsyncScroll.
	deltaNotch := int16(-120)
	h.handle(wmMouseWheel, uintptr(uint16(deltaNotch))<<16, 0)
	if h.vsyncMask&vsyncScroll == 0 {
		t.Fatalf("detented wheel delta did not activate vsyncScroll")
	}
	if got := h.scrollTo["page:a"]; got != wantFrac+scrollStep {
		t.Fatalf("scrollTo = %v, want %v", got, wantFrac+scrollStep)
	}

	// Advance time past easing and verify stepScroll completes and clears vsyncScroll.
	h.scrollTick = time.Now().Add(-500 * time.Millisecond)
	for i := 0; i < 20 && h.vsyncMask&vsyncScroll != 0; i++ {
		h.scrollTick = time.Now().Add(-100 * time.Millisecond)
		h.stepScroll()
	}
	if h.vsyncMask&vsyncScroll != 0 {
		t.Fatalf("vsyncScroll still active after easing completed")
	}
	if got := h.scroll["page:a"]; got != wantFrac+scrollStep {
		t.Fatalf("final scroll = %v, want %v", got, wantFrac+scrollStep)
	}
}

func TestFrameTransitionsRequestFastAnimating(t *testing.T) {
	now := time.Now()
	c := NewCtx(1, fakeMetrics{}, now, "", "", nil)
	f := Frame{
		Page:    Space(100),
		Dock:    Space(40),
		DockID:  "player:1",
		DockAt:  now.Add(-50 * time.Millisecond),
		Modal:   Space(60),
		ModalAt: now.Add(-50 * time.Millisecond),
	}
	f.Layout(c, 960, 660)
	if !c.Animating || !c.AnimatingFast {
		t.Fatalf("mid-transition Animating=%v AnimatingFast=%v, want both true", c.Animating, c.AnimatingFast)
	}

	// Once transitions finish, AnimatingFast should be false.
	done := NewCtx(1, fakeMetrics{}, now.Add(time.Second), "", "", nil)
	f.Layout(done, 960, 660)
	if done.AnimatingFast {
		t.Fatalf("after transitions AnimatingFast=true, want false")
	}
}

func TestHwndTargetPaintResizeFree(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	w, err := NewWindow("flit hwnd target test")
	if err != nil {
		t.Skip("no window: ", err)
	}
	defer procDestroyWindow.Call(w.hwnd)

	w.Build = func() Frame {
		return Frame{
			Nav:      []NavEntry{{ID: "home", Label: "Home", Glyph: GlyphHome}},
			Selected: "home",
			PageID:   "home",
			Page: VStack{Gap: 8, Items: []Node{
				Title("Hardware Direct2D"),
				Para{Text: "Rendered through ID2D1HwndRenderTarget."},
				Button{ID: "btn", Label: "Click", Glyph: GlyphRecord, Primary: true},
			}},
			Dock:   Space(36),
			DockID: "dock:test",
		}
	}

	// First paint creates the hardware HwndRenderTarget (no software Surface).
	w.paint()
	if !w.hw.ready() {
		t.Fatalf("expected HwndTarget to be active after paint")
	}
	if w.surf != nil {
		t.Fatalf("expected no software Surface fallback when HwndTarget succeeds")
	}
	w1, h1 := w.hw.w, w.hw.h

	// Resize the window and repaint: target should resize in place.
	procSetWindowPos.Call(w.hwnd, 0, 0, 0, 1200, 800, swpNoActivate|swpNoMove|swpNoZOrder)
	w.paint()
	if !w.hw.ready() {
		t.Fatalf("HwndTarget not ready after resize")
	}
	if w.hw.w == w1 && w.hw.h == h1 {
		t.Fatalf("HwndTarget did not resize: still %dx%d", w.hw.w, w.hw.h)
	}

	// Simulate device loss / Free and verify clean recreation on next paint.
	w.hw.Free()
	if w.hw.ready() {
		t.Fatalf("HwndTarget still ready after Free")
	}
	w.paint()
	if !w.hw.ready() {
		t.Fatalf("HwndTarget did not recreate after Free")
	}
}

func TestThemeRippleGeometryAndGPUComposite(t *testing.T) {
	// Origin near bottom-left of a 1000×600 window: farthest corner is top-right (1000, 0).
	gotR := rippleMaxRadius(150, 580, 1000, 600)
	wantR := float32(math.Hypot(850, 580)) + 2
	if math.Abs(float64(gotR-wantR)) > 0.5 {
		t.Fatalf("rippleMaxRadius = %v, want %v", gotR, wantR)
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	w, err := NewWindow("flit ripple test")
	if err != nil {
		t.Skip("no window: ", err)
	}
	defer procDestroyWindow.Call(w.hwnd)

	origLight := IsLight()
	defer SetTheme(origLight)

	SetTheme(false)
	w.Build = func() Frame {
		return Frame{
			Nav:           []NavEntry{{ID: "home", Label: "Home", Glyph: GlyphHome}},
			Selected:      "home",
			PageID:        "home",
			LightTheme:    IsLight(),
			OnToggleTheme: func() {},
			Page:          VStack{Items: []Node{Title("Ripple Test"), Para{Text: "Dual-theme GPU composite."}}},
		}
	}

	// Paint dark theme first so HwndTarget and w.last are ready.
	w.paint()
	if !w.hw.ready() || w.last == nil {
		t.Fatalf("expected HwndTarget and layout ready")
	}
	cw, ch := w.clientSize()

	// Capture old (dark) snapshot brush on GPU.
	oldCtx := w.last
	oldBrush, err := w.hw.CaptureBrush(func(p *Painter) { oldCtx.Paint(p) }, w.bg)
	if err != nil {
		t.Fatalf("CaptureBrush (old): %v", err)
	}
	w.ripple = themeRipple{
		oldBrush: oldBrush,
		w:        cw,
		h:        ch,
		cx:       150,
		cy:       float32(ch) - 22,
		maxR:     rippleMaxRadius(150, float32(ch)-22, float32(cw), float32(ch)),
		dur:      themeRippleDur,
	}

	// Switch to light theme and paint frame 0: should capture newBrush and activate vsyncRipple.
	SetTheme(true)
	w.bg = col(ColBase)
	w.paint()
	if !w.ripple.ready() {
		t.Fatalf("expected ripple to be ready (both GPU brushes captured)")
	}
	if w.vsyncMask&vsyncRipple == 0 {
		t.Fatalf("expected vsyncRipple active during transition")
	}

	// Mid-transition frame: fast GPU composite path.
	w.ripple.start = time.Now().Add(-themeRippleDur / 2)
	if !w.paintRippleFrame(cw, ch) {
		t.Fatalf("expected mid-transition paintRippleFrame to return true")
	}

	// Past duration: should clean up brushes, clear vsyncRipple, and return false.
	w.ripple.start = time.Now().Add(-2 * themeRippleDur)
	if w.paintRippleFrame(cw, ch) {
		t.Fatalf("expected finished paintRippleFrame to return false")
	}
	if w.ripple.ready() || w.ripple.armed() {
		t.Fatalf("expected ripple brushes released after completion")
	}
	if w.vsyncMask&vsyncRipple != 0 {
		t.Fatalf("expected vsyncRipple cleared after completion")
	}
}
