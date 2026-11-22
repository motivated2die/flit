package ui

import (
	"math"
	"testing"
	"time"
)

// fixedMeasure: 7 px per character at scale 1, scaled with the font.
func fixedMeasure(s string, f Font) float32 { return float32(len([]rune(s))) * f.Size * 0.56 }

func TestLayoutPrompt(t *testing.T) {
	v := NotchView{Mode: NotchPrompt, Title: "Teams meeting", Buttons: []NotchButton{{ID: "record", Label: "Transcribe", Primary: true}, {ID: "dismiss", Label: "Not now"}}}
	l := Layout(v, 1, time.Now(), fixedMeasure)
	if len(l.Buttons) != 2 || l.Buttons[0].R >= l.Buttons[1].L || l.Buttons[1].R > l.W {
		t.Fatalf("buttons %+v in width %v", l.Buttons, l.W)
	}
	if l.Title.L <= l.Icon.R || l.Buttons[0].L <= l.Title.R {
		t.Fatalf("overlap: icon %+v title %+v button %+v", l.Icon, l.Title, l.Buttons[0])
	}
	mid := (l.Buttons[1].T + l.Buttons[1].B) / 2
	if id := l.HitButton(l.Buttons[1].L+2, mid); id != "dismiss" {
		t.Fatalf("hit %q", id)
	}
	if id := l.HitButton(l.Title.L+1, mid); id != "" {
		t.Fatalf("title hit as button %q", id)
	}
}

func TestLayoutScales(t *testing.T) {
	v := NotchView{Mode: NotchReady, Title: "Transcript ready", Buttons: []NotchButton{{ID: "copy", Label: "Copy"}}}
	a := Layout(v, 1, time.Now(), fixedMeasure)
	b := Layout(v, 2, time.Now(), fixedMeasure)
	if b.H != 2*a.H || b.W < 1.95*a.W || b.W > 2.05*a.W {
		t.Fatalf("scale 1: %vx%v, scale 2: %vx%v", a.W, a.H, b.W, b.H)
	}
}

func TestRecordingClockAndStop(t *testing.T) {
	start := time.Date(2026, 11, 21, 14, 0, 0, 0, time.UTC)
	v := NotchView{Mode: NotchRecording, Title: "Recording", Since: start, Buttons: []NotchButton{{ID: "stop", Label: "Stop"}}}
	if d := v.DetailText(start.Add(83 * time.Second)); d != "1:23" {
		t.Fatalf("clock %q", d)
	}
	// Stop is always there: opening the notch is the only step.
	plain := Layout(v, 1, start.Add(5*time.Second), fixedMeasure)
	if len(plain.Buttons) != 1 || plain.IDs[0] != "stop" {
		t.Fatalf("buttons %v", plain.IDs)
	}
	if plain.Levels.W() == 0 {
		t.Fatal("no level bars while recording")
	}
	// The clock's width doesn't change from 0:05 to 9:59.
	later := Layout(v, 1, start.Add(599*time.Second), fixedMeasure)
	if later.W != plain.W {
		t.Fatalf("width jumps with the clock: %v → %v", plain.W, later.W)
	}
}

func TestWorkingDetail(t *testing.T) {
	if d := (NotchView{Mode: NotchWorking, Progress: 0.456}).DetailText(time.Now()); d != "46%" {
		t.Fatalf("%q", d)
	}
	if d := (NotchView{Mode: NotchWorking, Progress: -1}).DetailText(time.Now()); d != "" {
		t.Fatalf("indeterminate shows %q", d)
	}
}

func TestEasing(t *testing.T) {
	if EaseOut(0) != 0 || EaseOut(1) != 1 || EaseOut(2) != 1 || EaseOut(0.5) < 0.8 {
		t.Fatal("EaseOut")
	}
	if p := Pulse(0, time.Second); p != 0 {
		t.Fatalf("pulse at 0: %v", p)
	}
	if p := Pulse(500*time.Millisecond, time.Second); p < 0.99 {
		t.Fatalf("pulse at half: %v", p)
	}
}

func TestBounce(t *testing.T) {
	for _, sv := range []float64{notchBounceIn, notchBounceResize} {
		if EaseOutBack(0, sv) != 0 || math.Abs(EaseOutBack(1, sv)-1) > 1e-12 {
			t.Fatalf("s=%v: ends %v, %v", sv, EaseOutBack(0, sv), EaseOutBack(1, sv))
		}
		top := 0.0
		for i := range 101 {
			top = max(top, EaseOutBack(float64(i)/100, sv))
		}
		// Past the end a little, never wildly.
		if top <= 1.01 || top > 1.12 {
			t.Fatalf("s=%v: overshoots to %v", sv, top)
		}
	}
	if Smoothstep(0.3, 0.85, 0.2) != 0 || Smoothstep(0.3, 0.85, 0.9) != 1 || math.Abs(Smoothstep(0.3, 0.85, 0.575)-0.5) > 1e-9 {
		t.Fatal("smoothstep")
	}
}

func TestTitleLink(t *testing.T) {
	measure := func(s string, f Font) float32 { return float32(len(s)) * f.Size * 0.55 }
	v := NotchView{Mode: NotchReady, Title: "Transcript ready", Detail: "52 words", TitleID: "open",
		Buttons: []NotchButton{{ID: "copy", Label: "Copy", Primary: true}}}
	l := Layout(v, 1, time.Now(), measure)
	if l.LinkID != "open" || l.HitButton((l.Title.L+l.Title.R)/2, l.H/2) != "open" || l.HitButton((l.Detail.L+l.Detail.R)/2, l.H/2) != "open" {
		t.Fatalf("title link %+v", l)
	}
	if l.HitButton((l.Buttons[0].L+l.Buttons[0].R)/2, l.H/2) != "copy" {
		t.Fatal("the button lost to the link")
	}
	v.TitleID = ""
	if l := Layout(v, 1, time.Now(), measure); l.HitButton((l.Title.L+l.Title.R)/2, l.H/2) != "" {
		t.Fatal("a title without TitleID is clickable")
	}
}

func TestCollapsedFinished(t *testing.T) {
	measure := func(s string, f Font) float32 { return float32(len(s)) * f.Size * 0.55 }
	for _, sc := range []float32{1, 1.5} {
		l := CollapsedLayoutFor(NotchView{Mode: NotchReady}, sc, measure)
		plain := CollapsedLayout(sc)
		if l.H != plain.H || l.Word.R > l.W || l.Check.R > l.Word.L || l.Word.W() <= 0 {
			t.Fatalf("scale %v: %+v", sc, l)
		}
		if l2 := CollapsedLayoutFor(NotchView{Mode: NotchRecording}, sc, measure); l2.W != plain.W {
			t.Fatalf("recording capsule changed: %v", l2.W)
		}
	}
}

func TestCollapser(t *testing.T) {
	t0 := time.Date(2026, 11, 21, 14, 0, 0, 0, time.UTC)
	var c Collapser
	c.Expand(t0)
	// Hovered: stays open however long.
	c.Step(t0.Add(10*time.Second), true, true, false)
	if c.Collapsed {
		t.Fatal("collapsed while hovered")
	}
	// Left alone for 3 s: collapses.
	c.Step(t0.Add(12*time.Second), true, false, false)
	if c.Collapsed {
		t.Fatal("collapsed after 2 s")
	}
	c.Step(t0.Add(13*time.Second), true, false, false)
	if !c.Collapsed {
		t.Fatal("not collapsed after 3 s")
	}
	// A passing pointer doesn't expand it; resting 1 s does.
	at := t0.Add(20 * time.Second)
	if p := c.Step(at, true, false, true); p != 0 || !c.Collapsed {
		t.Fatalf("p %v", p)
	}
	if p := c.Step(at.Add(450*time.Millisecond), true, false, true); p < 0.4 || p > 0.5 || !c.Collapsed {
		t.Fatalf("halfway p %v", p)
	}
	c.Step(at.Add(900*time.Millisecond), true, false, false) // moved away: starts over
	if p := c.Step(at.Add(time.Second), true, false, true); p != 0 {
		t.Fatalf("hover didn't restart: %v", p)
	}
	if p := c.Step(at.Add(1900*time.Millisecond), true, false, true); p < 0.85 || !c.Collapsed {
		t.Fatalf("expanded too early: p %v", p)
	}
	if p := c.Step(at.Add(2*time.Second), true, false, true); p != 1 || c.Collapsed {
		t.Fatalf("not expanded after resting 1 s: p %v", p)
	}
	// Not collapsible (a prompt): never collapses.
	var d Collapser
	d.Expand(t0)
	d.Step(t0.Add(time.Minute), false, false, false)
	if d.Collapsed {
		t.Fatal("a prompt collapsed")
	}
}
