package ui

import (
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
	"unsafe"
)

// The main window folds its title bar into the client area: the client
// starts at the window's top edge, the top strip drags the window except on
// the drawn buttons, the very top resizes, and the rest is client area.
func TestWindowCaption(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	w, err := NewWindow("flit caption test")
	if err != nil {
		t.Skip("no window: ", err)
	}
	defer procDestroyWindow.Call(w.hwnd)
	w.Build = func() Frame { return Frame{Status: "idle"} }

	var wr struct{ Left, Top, Right, Bottom int32 }
	if r, _, err := user32.NewProc("GetWindowRect").Call(w.hwnd, uintptr(unsafe.Pointer(&wr))); r == 0 {
		t.Fatal(err)
	}
	pt := point{wr.Left, wr.Top}
	procScreenToClient.Call(w.hwnd, uintptr(unsafe.Pointer(&pt)))
	if pt.Y != 0 {
		t.Fatalf("client area starts %d px below the window's top: the native caption is still there", -pt.Y)
	}

	// Lay out once, as a paint would, so the buttons' hits are known.
	cw, ch := w.clientSize()
	c := NewCtx(w.scale, w.metrics, time.Now(), "", "", map[string]float32{})
	w.layout(c, float32(cw), float32(ch))
	w.last = c

	hit := func(x, y int32) uintptr {
		p := point{x, y}
		// client → screen: undo ScreenToClient's offset
		origin := point{0, 0}
		procScreenToClient.Call(w.hwnd, uintptr(unsafe.Pointer(&origin)))
		lp := uintptr(uint16(p.X-origin.X)) | uintptr(uint16(p.Y-origin.Y))<<16
		r, _, _ := procSendMessageW.Call(w.hwnd, wmNcHitTest, 0, lp)
		return r
	}
	s := w.scale
	strip := int32(float32(CaptionH)*s) - 4
	bw := int32(float32(captionButtonW) * s)
	for _, tc := range []struct {
		name string
		x, y int32
		want uintptr
	}{
		{"caption strip over the page", cw / 2, strip, htCaption},
		{"caption strip over the sidebar", int32(40 * s), strip, htCaption},
		{"close button", cw - bw/2, strip, htClient},
		{"minimise button", cw - bw - bw/2, strip, htClient},
		{"top edge", cw / 2, 1, htTop},
		{"page", cw / 2, ch / 2, htClient},
		{"a nav entry", int32(60 * s), int32((brandH + 8 + navItemH/2) * s), htClient},
	} {
		if got := hit(tc.x, tc.y); got != tc.want {
			t.Errorf("%s (%d,%d): hit %d, want %d", tc.name, tc.x, tc.y, got, tc.want)
		}
	}
}

func TestCaptionButtonsRender(t *testing.T) {
	f := Frame{Status: "idle", Caption: true, OnClose: func() {}, OnMinimize: func() {}}
	img, err := RenderFrame(f, 900, 600, 1, "caption:close", nil)
	if err != nil {
		t.Fatal(err)
	}
	c := img.RGBAAt(900-4, 4) // inside the close button, away from the glyph
	if c.R < 0xE0 || c.G > 0x60 {
		t.Fatalf("hovered close %v, want the red", c)
	}
	if c := img.RGBAAt(900-46-4, 4); c.R > 0x40 {
		t.Fatalf("minimise %v, want the plain background", c)
	}
}

// Content scrolled up disappears under the page's top bar: not drawn there,
// not clickable there.
func TestCaptionBarHidesScrolledPage(t *testing.T) {
	var items []Node
	for i := 0; i < 30; i++ {
		items = append(items, Card{Items: []Node{Button{ID: "b" + itoa(i), Label: "Button", OnClick: func() {}}}})
	}
	f := Frame{Status: "idle", Caption: true, Page: VStack{Gap: 12, Items: items}, PageID: "p"}
	scroll := map[string]float32{FrameScrollID("p"): 300}
	m, err := D2DMetrics()
	if err != nil {
		t.Skip(err)
	}
	c := NewCtx(1, m, time.Now(), "", "", scroll)
	f.Layout(c, 900, 600)
	for y := float32(1); y < CaptionH; y += 3 {
		if h, ok := c.HitAt(sidebarW+pagePad+20, y); ok {
			t.Fatalf("page target %q clickable under the bar at y=%v", h.ID, y)
		}
	}
	if _, ok := c.HitAt(sidebarW+pagePad+20, CaptionH+40); !ok {
		t.Log("(no target right below the bar at this scroll offset)")
	}
	img, err := RenderFrame(f, 900, 600, 1, "", scroll)
	if err != nil {
		t.Fatal(err)
	}
	base := col(ColBase)
	for x := sidebarW + 10; x < 900-2*captionButtonW; x += 7 {
		px := img.RGBAAt(x, 12)
		if d := int(px.R) - int(base.R*255+0.5); d > 2 || d < -2 {
			t.Fatalf("bar pixel at x=%d is %v: page content shows through", x, px)
		}
	}
	if dir := os.Getenv("FLIT_RENDER_DIR"); dir != "" {
		fh, _ := os.Create(filepath.Join(dir, "window-scrolled-bar.png"))
		png.Encode(fh, img)
		fh.Close()
	}
}
