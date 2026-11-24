package ui

import "testing"

func TestTransitionGeometryAndEasing(t *testing.T) {
	if got := easeOutCubic(0); got != 0 {
		t.Errorf("easeOutCubic(0) = %v, want 0", got)
	}
	if got := easeOutCubic(1); got != 1 {
		t.Errorf("easeOutCubic(1) = %v, want 1", got)
	}
	if mid := easeOutCubic(0.5); mid <= 0.5 || mid >= 1 {
		t.Errorf("easeOutCubic(0.5) = %v, want in (0.5, 1)", mid)
	}

	r := winRect{L: 100, T: 200, R: 500, B: 600} // 400x400 centered at (300, 400)
	s := scaledRect(r, 0.94)
	if w, h := s.R-s.L, s.B-s.T; w != 376 || h != 376 {
		t.Errorf("scaledRect(0.94) size = %dx%d, want 376x376", w, h)
	}
	if cx, cy := (s.L+s.R)/2, (s.T+s.B)/2; cx != 300 || cy != 400 {
		t.Errorf("scaledRect(0.94) center = (%d,%d), want (300,400)", cx, cy)
	}
	if full := scaledRect(r, 1.0); full != r {
		t.Errorf("scaledRect(1.0) = %+v, want %+v", full, r)
	}
}
