package ui

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func notchViews(now time.Time) map[string]NotchView {
	return map[string]NotchView{
		"prompt": {Mode: NotchPrompt, Title: "Teams meeting", Detail: "Weekly sync",
			Buttons: []NotchButton{{ID: "record", Label: "Transcribe", Primary: true}, {ID: "dismiss", Label: "Not now"}}},
		"recording":       {Mode: NotchRecording, Title: "Recording", Since: now.Add(-754 * time.Second), Buttons: []NotchButton{{ID: "notes", Label: "Notes"}, {ID: "stop", Label: "Stop"}}},
		"recording-hover": {Mode: NotchRecording, Title: "Recording", Since: now.Add(-754 * time.Second), Buttons: []NotchButton{{ID: "notes", Label: "Notes"}, {ID: "stop", Label: "Stop"}}},
		"working":         {Mode: NotchWorking, Title: "Transcribing", Progress: 0.62},
		"countdown": {Mode: NotchPrompt, Title: "Teams meeting detected", Detail: "recording starts automatically", CountFrom: now.Add(-3 * time.Second), CountFor: 5 * time.Second,
			Buttons: []NotchButton{{ID: "record", Label: "Record now", Primary: true}, {ID: "dismiss", Label: "Cancel"}}},
		"ready":      {Mode: NotchReady, Title: "Transcript ready", Detail: "1,284 words", Buttons: []NotchButton{{ID: "copy", Label: "Copy", Primary: true}}},
		"message":    {Mode: NotchMessage, Title: "Couldn't start recording", Detail: "the microphone is blocked", Warning: true},
		"stash-drop":       {Mode: NotchStashDrop, Title: "Stash the notes"},
		"stash-drop-ready": {Mode: NotchStashDrop, Title: "Stash the notes", Highlight: true},
		"stashed":          {Mode: NotchStashed, StashedNotes: true},
		"join": {Mode: NotchPrompt, Title: "Weekly sync", Detail: "starts in 1 min", CountFrom: now.Add(-6 * time.Second), CountFor: 15 * time.Second,
			Buttons: []NotchButton{{ID: "join", Label: "Join", Primary: true}, {ID: "join-close", Label: "Close"}}},
	}
}

// toImage converts a premultiplied BGRA surface to straight-alpha RGBA.
func toImage(s *Surface) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, int(s.W), int(s.H)))
	for i := 0; i < int(s.W*s.H); i++ {
		b, g, r, a := s.Bits[4*i], s.Bits[4*i+1], s.Bits[4*i+2], s.Bits[4*i+3]
		if a > 0 {
			un := func(v byte) byte { return byte(min(255, int(v)*255/int(a))) }
			r, g, b = un(r), un(g), un(b)
		}
		img.Pix[4*i], img.Pix[4*i+1], img.Pix[4*i+2], img.Pix[4*i+3] = r, g, b, a
	}
	return img
}

func TestNotchRender(t *testing.T) {
	now := time.Now()
	dir := os.Getenv("FLIT_RENDER_DIR")
	for _, scale := range []float32{1, 1.5} {
		w, h := notchSurfaceSize(scale)
		s, err := NewSurface(w, h)
		if err != nil {
			t.Fatal(err)
		}
		for name, v := range notchViews(now) {
			hover := name == "recording-hover"
			lay := Layout(v, scale, now, Measure)
			f := notchFrame{view: v, layout: lay, scale: scale, slide: 1, rise: 1, width: lay.W, height: lay.H, content: 1,
				levels: [2]float32{0.6, 0.35}, progress: float32(v.Progress), now: now, born: now.Add(-700 * time.Millisecond)}
			if hover {
				f.hoverBtn = "stop"
			}
			if err := renderNotch(s, f); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			img := toImage(s)
			ox, _ := f.capsuleOrigin(s.W)
			// Inside the capsule, left of the content: the dark fill.
			c := img.RGBAAt(int(ox+2*scale), int(lay.H/2))
			if c.A < 240 || c.R > 0x40 {
				t.Errorf("%s @%v: capsule pixel %v", name, scale, c)
			}
			// Far left of the window: nothing.
			if img.RGBAAt(1, 1).A != 0 {
				t.Errorf("%s: corner not transparent", name)
			}
			if dir != "" {
				save(t, filepath.Join(dir, "notch-"+name+"-"+itoa(int(scale*100))+".png"), img)
			}
		}
		// The collapsed capsules, and each half-way through a hover to open.
		for _, mode := range []NotchMode{NotchRecording, NotchWorking, NotchReady} {
			for _, hp := range []float64{0, 0.6} {
				v := NotchView{Mode: mode, Progress: 0.6}
				lay := CollapsedLayoutFor(v, scale, Measure)
				f := notchFrame{view: v, layout: lay, scale: scale, slide: 1, rise: 1, width: lay.W, height: lay.H, content: 1,
					collapsed: true, hoverProgress: hp, levels: [2]float32{0.7, 0.4}, progress: 0.6, now: now, born: now.Add(-700 * time.Millisecond)}
				if err := renderNotch(s, f); err != nil {
					t.Fatal(err)
				}
				img := toImage(s)
				ox, _ := f.capsuleOrigin(s.W)
				if c := img.RGBAAt(int(ox+2*scale), int(lay.H/2)); c.A < 200 {
					t.Errorf("collapsed %d: capsule pixel %v", mode, c)
				}
				if dir != "" {
					save(t, filepath.Join(dir, "notch-collapsed-"+itoa(int(mode))+"-"+itoa(int(hp*10))+"-"+itoa(int(scale*100))+".png"), img)
				}
			}
		}
		s.Free()
	}
}

func save(t *testing.T, path string, img *image.RGBA) {
	// On a desktop-like background, to judge it as it will be seen.
	bg := image.NewRGBA(img.Bounds())
	for i := 0; i < len(bg.Pix); i += 4 {
		y := i / 4 / img.Bounds().Dx()
		v := byte(0xB0 - y/3)
		bg.Pix[i], bg.Pix[i+1], bg.Pix[i+2], bg.Pix[i+3] = v, v+8, v+20, 255
	}
	for i := 0; i < len(img.Pix); i += 4 {
		a := float64(img.Pix[i+3]) / 255
		for c := 0; c < 3; c++ {
			bg.Pix[i+c] = byte(float64(img.Pix[i+c])*a + float64(bg.Pix[i+c])*(1-a))
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	png.Encode(f, bg)
	_ = color.RGBA{}
}

// TestCollapsedCueStaysCentred: while the collapsed capsule swells under
// the pointer, its cue (here the check and "Finished") stays where it is.
func TestCollapsedCueStaysCentred(t *testing.T) {
	scale := float32(1.5)
	w, h := notchSurfaceSize(scale)
	s, err := NewSurface(w, h)
	if err != nil {
		t.Skip(err)
	}
	defer s.Free()
	now := time.Now()
	v := NotchView{Mode: NotchReady}
	lay := CollapsedLayoutFor(v, scale, Measure)
	centre := func(hp float64) float64 {
		f := notchFrame{view: v, layout: lay, scale: scale, slide: 1, rise: 1, width: lay.W, height: lay.H, content: 1,
			collapsed: true, hoverProgress: hp, now: now, born: now}
		if err := renderNotch(s, f); err != nil {
			t.Fatal(err)
		}
		img := toImage(s)
		sum, n := 0.0, 0.0
		for y := 0; y < int(lay.H)+4; y++ {
			for x := 0; x < img.Bounds().Dx(); x++ {
				if c := img.RGBAAt(x, y); c.A > 200 && c.R > 0xC0 { // the white cue
					sum += float64(x)
					n++
				}
			}
		}
		if n == 0 {
			t.Fatal("no cue drawn")
		}
		return sum / n
	}
	if a, b := centre(0), centre(0.8); math.Abs(a-b) > 1 {
		t.Fatalf("the cue moved from x %.1f to %.1f as the capsule swelled", a, b)
	}
}
