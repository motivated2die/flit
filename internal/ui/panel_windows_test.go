package ui

import "testing"

func TestPanelPlacement(t *testing.T) {
	work := struct{ L, T, R, B int32 }{0, 0, 1920, 1040} // a 1080p screen, taskbar at the bottom
	// Bottom taskbar, cursor on a tray icon at the right.
	x, y := panelPlacement(work, point{1800, 1060}, 340, 400, 10)
	if x != 1920-340-10 || y != 1040-400-10 {
		t.Fatalf("bottom: %d,%d", x, y)
	}
	// Top taskbar.
	work2 := struct{ L, T, R, B int32 }{0, 48, 1920, 1080}
	if _, y := panelPlacement(work2, point{900, 20}, 340, 400, 10); y != 58 {
		t.Fatalf("top: y %d", y)
	}
	// Left taskbar.
	work3 := struct{ L, T, R, B int32 }{60, 0, 1920, 1080}
	if x, y := panelPlacement(work3, point{30, 900}, 340, 400, 10); x != 70 || y != 1080-400-10 {
		t.Fatalf("left: %d,%d", x, y)
	}
	// Cursor in the middle (hidden icons flyout): above it, on screen.
	if x, y := panelPlacement(work, point{960, 300}, 340, 400, 10); y != 10 || x != 960-170 {
		t.Fatalf("middle: %d,%d", x, y)
	}
}
