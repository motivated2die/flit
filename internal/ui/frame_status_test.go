package ui

import (
	"testing"
	"time"
)

func TestFrameLiveStatusClickAndHover(t *testing.T) {
	clicked := false
	f := Frame{
		Status:        "Recording · 1:23",
		Live:          true,
		OnStatus:      func() { clicked = true },
		OnToggleTheme: func() {},
	}

	c := NewCtx(1, fakeMetrics{}, time.Now(), "status", "", nil)
	f.Layout(c, 800, 600)

	hit, ok := c.HitByID("status")
	if !ok {
		t.Fatal("status hit target missing when OnStatus is set")
	}
	tog, ok := c.HitByID("theme:toggle")
	if !ok {
		t.Fatal("theme:toggle hit target missing")
	}
	if hit.R.R > tog.R.L {
		t.Errorf("status pill (%v) overlaps theme toggle (%v)", hit.R.R, tog.R.L)
	}
	hit.OnClick()
	if !clicked {
		t.Fatal("clicking status did not invoke OnStatus")
	}

	// Without OnStatus (idle), no status click target is registered.
	f.Live = false
	f.OnStatus = nil
	c2 := NewCtx(1, fakeMetrics{}, time.Now(), "", "", nil)
	f.Layout(c2, 800, 600)
	if _, ok := c2.HitByID("status"); ok {
		t.Fatal("status should not be clickable when OnStatus is nil")
	}
}
