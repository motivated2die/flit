package ui

import (
	"runtime"
	"testing"

	"github.com/motivated2die/flit/internal/notes"
)

func TestFloatWindowHitTestAndSizeLimits(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	f, err := NewFloatWindow("flit notes test")
	if err != nil {
		t.Skip("no window: ", err)
	}
	defer func() {
		delete(floatByHwnd, f.hwnd)
		procDestroyWindow.Call(f.hwnd)
	}()

	ed := notes.NewEditor(notes.Parse("Hello floating notes\n"))
	unpinned := false
	f.Build = func() Node {
		return NotesEditor{
			ID:      "float:1",
			Ed:      ed,
			Focus:   true,
			Toolbar: true,
			Pinned:  true,
			OnPin:   func() { unpinned = true },
			MinH:    140,
		}
	}
	f.paint()

	cw, ch := f.clientSize()
	if cw <= 0 || ch <= 0 {
		t.Fatalf("invalid client size %dx%d", cw, ch)
	}

	// Corners and edges report native Windows resize hit-test codes.
	cases := []struct {
		name string
		x, y int32
		want uintptr
	}{
		{"top-left", 2, 2, htTopLeft},
		{"top-right", cw - 2, 2, htTopRight},
		{"bottom-left", 2, ch - 2, htBottomLeft},
		{"bottom-right", cw - 2, ch - 2, htBottomRight},
		{"left", 2, ch / 2, htLeft},
		{"right", cw - 2, ch / 2, htRight},
		{"top", cw / 2, 2, htTop},
		{"bottom", cw / 2, ch - 2, htBottom},
	}
	for _, tc := range cases {
		if got := f.hitTestClient(tc.x, tc.y, cw, ch); got != tc.want {
			t.Errorf("%s (%d,%d): got %d, want %d", tc.name, tc.x, tc.y, got, tc.want)
		}
	}

	// 6-dot drag handle and empty toolbar lane both map to HTCAPTION.
	drag, ok := f.last.HitByID("float:1:drag")
	if !ok {
		t.Fatal("missing :drag handle in FloatWindow layout")
	}
	dx, dy := int32((drag.R.L+drag.R.R)/2), int32((drag.R.T+drag.R.B)/2)
	if got := f.hitTestClient(dx, dy, cw, ch); got != htCaption {
		t.Errorf("drag handle (%d,%d): got %d, want htCaption (%d)", dx, dy, got, htCaption)
	}
	bar, ok := f.last.HitByID("float:1:bar")
	if !ok {
		t.Fatal("missing :bar swim lane in FloatWindow layout")
	}
	bx, by := int32(drag.R.L-10), int32((bar.R.T+bar.R.B)/2)
	if got := f.hitTestClient(bx, by, cw, ch); got != htCaption {
		t.Errorf("empty toolbar lane (%d,%d): got %d, want htCaption (%d)", bx, by, got, htCaption)
	}

	// Pin button stays clickable (HTCLIENT): click-release activates OnPin,
	// whereas press-and-drag hands off to native window dragging without firing OnPin.
	pin, ok := f.last.HitByID("float:1:tool:pin")
	if !ok {
		t.Fatal("missing :tool:pin button in FloatWindow layout")
	}
	px, py := int32((pin.R.L+pin.R.R)/2), int32((pin.R.T+pin.R.B)/2)
	if got := f.hitTestClient(px, py, cw, ch); got != htClient {
		t.Errorf("pin button (%d,%d): got %d, want htClient (%d)", px, py, got, htClient)
	}
	at := func(x, y int32) uintptr { return uintptr(uint16(int16(x))) | uintptr(uint16(int16(y)))<<16 }
	moved := 0
	f.startMove = func() { moved++ }

	// Press and drag on the pin button -> drags window, does NOT unpin.
	floatProc(f.hwnd, wmLButtonDown, 0, at(px, py))
	floatProc(f.hwnd, wmMouseMove, 0, at(px+12, py))
	floatProc(f.hwnd, wmLButtonUp, 0, at(px+12, py))
	if moved != 1 || unpinned {
		t.Errorf("drag on toolbar button: moved=%d unpinned=%v, want 1, false", moved, unpinned)
	}

	// Press and release on the pin button -> activates OnPin, does NOT drag.
	floatProc(f.hwnd, wmLButtonDown, 0, at(px, py))
	floatProc(f.hwnd, wmLButtonUp, 0, at(px, py))
	if moved != 1 || !unpinned {
		t.Errorf("click on toolbar button: moved=%d unpinned=%v, want 1, true", moved, unpinned)
	}

	// WM_NCACTIVATE and WM_NCPAINT suppress classic white border painting.
	if got := floatProc(f.hwnd, wmNcActivate, 0, 0); got != 1 {
		t.Errorf("WM_NCACTIVATE(inactive) = %d, want 1", got)
	}
	if got := floatProc(f.hwnd, wmNcPaint, 0, 0); got != 0 {
		t.Errorf("WM_NCPAINT = %d, want 0", got)
	}

	// Size limits respect [MinW..MaxW] × [MinH..MaxH].
	var minPt, maxPt point
	f.limitSize(&minPt, &maxPt)
	if minPt.X != int32(floatMinW*f.scale) || minPt.Y != int32(floatMinH*f.scale) {
		t.Errorf("minTrack = %+v, want %dx%d", minPt, int32(floatMinW*f.scale), int32(floatMinH*f.scale))
	}
	if maxPt.X < minPt.X || maxPt.X > int32(floatMaxW*f.scale) || maxPt.Y < minPt.Y || maxPt.Y > int32(floatMaxH*f.scale) {
		t.Errorf("maxTrack = %+v out of bounds", maxPt)
	}
}
