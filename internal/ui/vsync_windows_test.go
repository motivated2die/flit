package ui

import (
	"image/color"
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

	// Detented wheel notch (±120): queues smooth scroll (rounded target) and activates vsyncScroll.
	deltaNotch := int16(-120)
	h.handle(wmMouseWheel, uintptr(uint16(deltaNotch))<<16, 0)
	if h.vsyncMask&vsyncScroll == 0 {
		t.Fatalf("detented wheel delta did not activate vsyncScroll")
	}
	wantTarget := float32(math.Round(float64(wantFrac + scrollStep)))
	if got := h.scrollTo["page:a"]; got != wantTarget {
		t.Fatalf("scrollTo = %v, want %v", got, wantTarget)
	}

	// Step spring at 144 Hz (~7 ms): must approach monotonically with zero overshoot and settle cleanly.
	prev := h.scroll["page:a"]
	for i := 0; i < 120 && h.vsyncMask&vsyncScroll != 0; i++ {
		h.scrollTick = time.Now().Add(-7 * time.Millisecond)
		h.stepScroll()
		cur := h.scroll["page:a"]
		if cur < prev-1e-3 || cur > wantTarget+1e-3 {
			t.Fatalf("step %d: spring out of bounds: prev=%v cur=%v target=%v", i, prev, cur, wantTarget)
		}
		prev = cur
	}
	if h.vsyncMask&vsyncScroll != 0 {
		t.Fatalf("vsyncScroll still active after spring settled")
	}
	if got := h.scroll["page:a"]; got != wantTarget {
		t.Fatalf("final scroll = %v, want %v", got, wantTarget)
	}
	if vel := h.scrollVel["page:a"]; vel != 0 {
		t.Fatalf("residual scrollVel = %v, want 0", vel)
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
	// Border COLORREF cross-fade endpoints and midpoint.
	a := color.RGBA{R: 20, G: 40, B: 60, A: 255}
	b := color.RGBA{R: 220, G: 200, B: 180, A: 255}
	if got := lerpColorref(a, b, 0); got != 20|40<<8|60<<16 {
		t.Fatalf("lerpColorref(0) = %#x", got)
	}
	if got := lerpColorref(a, b, 1); got != 220|200<<8|180<<16 {
		t.Fatalf("lerpColorref(1) = %#x", got)
	}
	if got := lerpColorref(a, b, 0.5); got != 120|120<<8|120<<16 {
		t.Fatalf("lerpColorref(0.5) = %#x, want %#x", got, uint32(120|120<<8|120<<16))
	}

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

func TestLiveDotRippleAndSoftDiscPixels(t *testing.T) {
	// Outer corners of the soft disc texture must be fully transparent (no hard box edge).
	pix := rippleDiscPixels(rippleTexSize, ColLive)
	if pix[3] != 0 {
		t.Fatalf("top-left corner alpha = %d, want 0", pix[3])
	}
	// Centre and mid-disc pixels should have uniform non-zero fill, feathering down toward the edge.
	cx := rippleTexSize / 2
	centerAlpha := pix[4*(cx*rippleTexSize+cx)+3]
	edgeAlpha := pix[4*(cx*rippleTexSize+cx+int(float64(cx)*0.9))+3]
	if centerAlpha == 0 || edgeAlpha >= centerAlpha {
		t.Fatalf("expected filled centre (%d) feathering down near edge (%d)", centerAlpha, edgeAlpha)
	}

	now := time.Now()
	f := Frame{
		Status: "Recording",
		Live:   true,
		LiveAt: now.Add(-100 * time.Millisecond),
		Page:   Space(100),
	}
	c := NewCtx(1, fakeMetrics{}, now, "", "", nil)
	f.Layout(c, 960, 660)
	if !c.Animating || !c.AnimatingFast {
		t.Fatalf("mid-ripple Animating=%v AnimatingFast=%v, want both true", c.Animating, c.AnimatingFast)
	}
	var rec recorder
	c.Paint(&rec)
	hasRipple := false
	for _, b := range rec.bitmaps {
		if len(b) >= 7 && b[:7] == "ripple:" {
			hasRipple = true
		}
	}
	if !hasRipple {
		t.Fatalf("expected ripple scaled bitmap in recorded bitmaps: %v", rec.bitmaps)
	}

	// After liveRippleDur elapses, AnimatingFast stops while ambient Animating remains true.
	cDone := NewCtx(1, fakeMetrics{}, now.Add(liveRippleDur+50*time.Millisecond), "", "", nil)
	f.Layout(cDone, 960, 660)
	if cDone.AnimatingFast {
		t.Fatalf("after liveRippleDur AnimatingFast=true, want false")
	}
	if !cDone.Animating {
		t.Fatalf("after liveRippleDur ambient Animating=false, want true")
	}
}

func TestNotchSpringLiquidOvershootAndInterrupt(t *testing.T) {
	// Drop-in spring should have a subtle liquid micro-overshoot (~2-5%) and settle at 1.
	var s spring1D
	s.snap(0)
	s.target = 1
	maxPos := 0.0
	settled := false
	for i := 0; i < 200; i++ {
		moving := s.step(1.0/144.0, notchRiseOmega, notchRiseZeta, 0.002, 0.05)
		if s.pos > maxPos {
			maxPos = s.pos
		}
		if !moving {
			settled = true
			break
		}
	}
	if !settled || s.pos != 1 || s.vel != 0 {
		t.Fatalf("rise spring did not settle cleanly: settled=%v pos=%v vel=%v", settled, s.pos, s.vel)
	}
	if maxPos <= 1.005 || maxPos >= 1.12 {
		t.Fatalf("rise spring overshoot maxPos = %v, want subtle liquid overshoot in (1.005, 1.12)", maxPos)
	}

	// Mid-flight interruption: expanding from 46 -> 300, reversed back to 46 mid-way,
	// should preserve velocity smoothly and settle cleanly at 46.
	var w spring1D
	w.snap(46)
	w.target = 300
	for i := 0; i < 12; i++ {
		w.step(1.0/144.0, notchExpandWOmega, notchExpandWZeta, 0.25, 8)
	}
	midPos, midVel := w.pos, w.vel
	if midPos <= 46 || midVel <= 0 {
		t.Fatalf("expected positive expansion mid-flight, got pos=%v vel=%v", midPos, midVel)
	}
	w.target = 46
	settled = false
	for i := 0; i < 250; i++ {
		if !w.step(1.0/144.0, notchShrinkWOmega, notchShrinkWZeta, 0.25, 8) {
			settled = true
			break
		}
	}
	if !settled || w.pos != 46 || w.vel != 0 {
		t.Fatalf("interrupted width spring did not settle cleanly: settled=%v pos=%v vel=%v", settled, w.pos, w.vel)
	}
}
