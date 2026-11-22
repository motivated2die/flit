package ui

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func near(a, b color.RGBA) bool {
	d := func(x, y uint8) int {
		if x > y {
			return int(x - y)
		}
		return int(y - x)
	}
	return d(a.R, b.R) < 10 && d(a.G, b.G) < 10 && d(a.B, b.B) < 10
}

// count counts opaque-ish pixels of a colour.
func count(img *image.RGBA, c color.RGBA, rect image.Rectangle) int {
	n := 0
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			if p := img.RGBAAt(x, y); p.A > 200 && near(p, c) {
				n++
			}
		}
	}
	return n
}

func TestTrayIconRedOnlyWhileRecording(t *testing.T) {
	for st := TrayIdle; st <= TrayError; st++ {
		img := TrayIcon{State: st, Size: 32, Progress: 0.5}.Draw()
		red := count(img, ColLive, img.Bounds())
		if (st == TrayRecording) != (red > 20) {
			t.Errorf("state %d: %d red pixels", st, red)
		}
		if count(img, ColText, img.Bounds()) < 40 && st != TrayError && st != TrayTranscribing {
			t.Errorf("state %d: the feather is missing", st)
		}
	}
}

func TestTrayIconBadges(t *testing.T) {
	badge := image.Rect(20, 20, 32, 32)
	idle := TrayIcon{State: TrayIdle, Size: 32}.Draw()
	ready := TrayIcon{State: TrayReady, Size: 32}.Draw()
	if count(ready, ColText, badge) <= count(idle, ColText, badge) {
		t.Error("ready shows no badge")
	}
	// The badge's centre: filled for ready, hollow for detected.
	cx, cy := 24, 24 // the badge centre: size - 0.22*size
	if p := ready.RGBAAt(cx, cy); p.A < 200 {
		t.Errorf("ready badge centre %v", p)
	}
	if p := (TrayIcon{State: TrayDetected, Size: 32}).Draw().RGBAAt(cx, cy); p.A > 60 {
		t.Errorf("detected badge centre %v, want hollow", p)
	}
}

func TestTrayIconProgressFillsFromBottom(t *testing.T) {
	top, bottom := image.Rect(0, 0, 32, 12), image.Rect(0, 20, 32, 32)
	img := TrayIcon{State: TrayTranscribing, Progress: 0.4, Size: 32}.Draw()
	if count(img, ColText, bottom) == 0 {
		t.Error("the bottom isn't filled at 40%")
	}
	if count(img, ColText, top) != 0 {
		t.Error("the top is filled at 40%")
	}
	full := TrayIcon{State: TrayTranscribing, Progress: 1, Size: 32}.Draw()
	if count(full, ColText, top) == 0 {
		t.Error("the top isn't filled at 100%")
	}
}

func TestTrayIconLightTaskbar(t *testing.T) {
	img := TrayIcon{State: TrayIdle, Size: 32, Light: true}.Draw()
	if count(img, ColInkOnLight, img.Bounds()) < 40 {
		t.Error("no dark ink on a light taskbar")
	}
}

// With FLIT_RENDER_DIR set, the icons are saved as PNGs to look at.
func TestTrayIconRender(t *testing.T) {
	dir := os.Getenv("FLIT_RENDER_DIR")
	if dir == "" {
		t.Skip("set FLIT_RENDER_DIR to save the icons")
	}
	for _, size := range []int{16, 24, 32} {
		for _, light := range []bool{false, true} {
			for st := TrayIdle; st <= TrayError; st++ {
				img := TrayIcon{State: st, Progress: 0.6, Size: size, Light: light}.Draw()
				name := filepath.Join(dir, "tray-"+map[bool]string{false: "dark", true: "light"}[light]+"-"+string(rune('0'+st))+"-"+itoa(size)+".png")
				f, _ := os.Create(name)
				png.Encode(f, img)
				f.Close()
			}
		}
	}
}

func itoa(v int) string {
	if v < 10 {
		return string(rune('0' + v))
	}
	return itoa(v/10) + string(rune('0'+v%10))
}
