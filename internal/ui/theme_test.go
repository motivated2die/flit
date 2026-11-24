package ui

import (
	"testing"
	"time"
)

func TestThemeSwitchAndSidebarToggle(t *testing.T) {
	defer SetTheme(false)

	SetTheme(false)
	if IsLight() || ColBase != DarkPalette.Base || ColText != DarkPalette.Text {
		t.Fatalf("dark theme mismatch: light=%v base=%v text=%v", IsLight(), ColBase, ColText)
	}

	SetTheme(true)
	if !IsLight() || ColBase != LightPalette.Base || ColText != LightPalette.Text || ColCard != LightPalette.Card {
		t.Fatalf("light theme mismatch: light=%v base=%v text=%v", IsLight(), ColBase, ColText)
	}

	// Tray icon on a dark taskbar must keep light ink even when app is in light mode.
	img := TrayIcon{State: TrayIdle, Size: 32, Light: false}.Draw()
	if count(img, ColInkOnDark, img.Bounds()) < 40 {
		t.Errorf("dark-taskbar tray icon lost light ink in light app theme")
	}

	// Sidebar sun/moon toggle renders in the bottom-right of the sidebar and fires OnToggleTheme.
	toggled := false
	f := Frame{
		Status:        "Ready",
		LightTheme:    true,
		OnToggleTheme: func() { toggled = true },
	}
	c := NewCtx(1, fakeMetrics{}, time.Now(), "", "", nil)
	f.Layout(c, 800, 600)
	h, ok := c.HitByID("theme:toggle")
	if !ok {
		t.Fatal("missing theme:toggle hit target in sidebar")
	}
	if h.R.R > sidebarW || h.R.T < 600-statusLineH {
		t.Errorf("theme:toggle rect %+v outside sidebar bottom-right", h.R)
	}
	h.OnClick()
	if !toggled {
		t.Error("clicking theme:toggle did not invoke OnToggleTheme")
	}
}
