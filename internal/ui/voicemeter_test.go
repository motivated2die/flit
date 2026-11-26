package ui

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func speaking() [2][]float32 {
	return [2][]float32{
		{0.2, 0.5, 0.8, 0.9, 0.7, 0.5, 0.35, 0.2, 0.1},
		{0, 0, 0, 0, 0, 0, 0, 0, 0},
	}
}

func TestVoiceMeterLive(t *testing.T) {
	var sm MeterSmoother
	m := VoiceMeter{Labels: [2]string{"You", "Meeting"}, Bands: speaking, Smooth: &sm}
	c := ctx("", nil)
	h := m.Layout(c, 10, 20, 400)
	if h != vmH || len(c.live) != 2 {
		t.Fatalf("height %v, %d live areas", h, len(c.live))
	}
	// Each side's bars sit in its half, inside the box, and a live frame
	// draws only shapes (pixelCanvas ignores text).
	for i, a := range c.live {
		lo, hi := 10+float32(i)*200, 10+float32(i+1)*200
		if a.r.L < lo || a.r.R > hi || a.r.T < 20 || a.r.B > 20+vmH {
			t.Fatalf("area %d %+v outside its half", i, a.r)
		}
		live := &recorder{}
		a.draw(live, time.Now())
		if len(live.texts) != 0 {
			t.Fatalf("a live frame draws text %v", live.texts)
		}
	}
	rec := &recorder{}
	c.Paint(rec)
	if rec.clips != 0 || len(rec.texts) != 2 {
		t.Fatalf("clips %d, texts %v", rec.clips, rec.texts)
	}
	// Partly scrolled away: drawn with the page, not on its own.
	c = ctx("", nil)
	c.PushClip(RectF{0, 36, 500, 600}) // cuts through the bars (y 31..49)
	m.Layout(c, 10, 20, 400)
	c.PopClip()
	if len(c.live) != 0 {
		t.Fatal("a clipped meter repaints on its own")
	}
}

func TestVoiceMeterStacked(t *testing.T) {
	var sm MeterSmoother
	m := VoiceMeter{Labels: [2]string{"You", "Meeting"}, Bands: speaking, Smooth: &sm, Stacked: true}
	c := ctx("", nil)
	w := m.Width(c)
	h := m.Layout(c, 0, 0, w)
	if h != 2*vmRowH+2*vmStackPad || len(c.live) != 2 {
		t.Fatalf("height %v, %d live areas", h, len(c.live))
	}
	// Two rows, the bars at the same place in both, inside the box.
	a, b := c.live[0].r, c.live[1].r
	if a.L != b.L || a.B > b.T || b.B > h || a.R > w {
		t.Fatalf("rows %+v %+v in a %v×%v box", a, b, w, h)
	}
}

func TestAside(t *testing.T) {
	var sm MeterSmoother
	side := VoiceMeter{Labels: [2]string{"You", "Meeting"}, Bands: speaking, Smooth: &sm, Stacked: true}
	below := Label{Text: "below"}
	n := Aside{Main: Label{Text: "main"}, Side: side, Below: below, MinMain: 280, Gap: 16}
	// Wide: the meter on the right, nothing below.
	c := ctx("", nil)
	n.Layout(c, 0, 0, 700)
	if len(c.live) != 2 || c.live[0].r.R > 700 || c.live[0].r.L < 700-side.Width(c) {
		t.Fatalf("wide: %+v", c.live)
	}
	rec := &recorder{}
	c.Paint(rec)
	if strings.Contains(strings.Join(rec.texts, "|"), "below") {
		t.Fatal("wide: drew the narrow fallback too")
	}
	// Narrow: main, then the fallback under it.
	c = ctx("", nil)
	n.Layout(c, 0, 0, 380)
	rec = &recorder{}
	c.Paint(rec)
	if len(c.live) != 0 || !strings.Contains(strings.Join(rec.texts, "|"), "below") {
		t.Fatalf("narrow: %d live areas, drew %v", len(c.live), rec.texts)
	}
}

func TestMeterSmoother(t *testing.T) {
	var m MeterSmoother
	t0 := time.Now()
	up := [2][]float32{{1}, {0}}
	down := [2][]float32{{0}, {0}}
	m.step(down, t0)
	v := m.step(up, t0.Add(45*time.Millisecond))[0][0] // one rise constant
	if v < 0.6 || v > 0.66 {
		t.Fatalf("after one rise constant %v, want about 0.63", v)
	}
	if again := m.step(down, t0.Add(45*time.Millisecond))[0][0]; again != v {
		t.Fatal("stepped twice at one instant")
	}
	m.step(up, t0.Add(time.Second))
	fell := m.step(down, t0.Add(time.Second+45*time.Millisecond))[0][0]
	if fell < 0.65 {
		t.Fatalf("fell to %v in 45 ms: should fall slower than it rises", fell)
	}
}

func TestVoiceMeterPicture(t *testing.T) {
	dir := os.Getenv("FLIT_RENDER_DIR")
	if dir == "" {
		t.Skip("FLIT_RENDER_DIR not set")
	}
	var sm MeterSmoother
	n := VStack{Gap: 8, Items: []Node{
		LiveTitle{Text: "Weekly sync", Detail: "12:34"},
		VoiceMeter{Labels: [2]string{"You", "Meeting"}, Bands: speaking, Smooth: &sm},
	}}
	s, err := RenderPanel(n, 1.5, "", true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Free()
	fh, _ := os.Create(filepath.Join(dir, "voice-meter.png"))
	png.Encode(fh, SurfaceImage(s))
	fh.Close()
}

func TestVoiceMeterClickAndMute(t *testing.T) {
	var sm MeterSmoother
	var toggled []int
	m := VoiceMeter{
		ID:       "test:meter",
		Labels:   [2]string{"You", "Meeting"},
		Bands:    speaking,
		Smooth:   &sm,
		Muted:    [2]bool{true, false},
		OnToggle: func(side int) { toggled = append(toggled, side) },
	}
	c := ctx("", nil)
	m.Layout(c, 0, 0, 320)
	if len(c.live) != 1 {
		t.Fatalf("live regions = %d, want 1 (muted side must not register live bars)", len(c.live))
	}
	var clicked int
	for _, h := range c.Hits() {
		if h.ID == "test:meter:0" || h.ID == "test:meter:1" {
			h.OnClick()
			clicked++
		}
	}
	if clicked != 2 || len(toggled) != 2 || toggled[0] != 0 || toggled[1] != 1 {
		t.Fatalf("clicked=%d toggled=%v, want [0 1]", clicked, toggled)
	}
}

func TestPlayerBarTrimHandles(t *testing.T) {
	var gotStart, gotEnd time.Duration
	var gotDone bool
	bar := PlayerBar{
		ID:  "player",
		Len: 100 * time.Second,
		OnTrim: func(start, end time.Duration, done bool) {
			gotStart, gotEnd, gotDone = start, end, done
		},
	}
	c := ctx("", nil)
	bar.Layout(c, 0, 0, 400)
	hits := c.Hits()
	var startHit, endHit *Hit
	for i := range hits {
		switch hits[i].ID {
		case "player:trim-start":
			startHit = &hits[i]
		case "player:trim-end":
			endHit = &hits[i]
		}
	}
	if startHit == nil || endHit == nil {
		t.Fatalf("missing trim handle hits: start=%v end=%v", startHit, endHit)
	}
	barL := (startHit.R.L + startHit.R.R) / 2
	barR := (endHit.R.L + endHit.R.R) / 2
	// Drag start handle to 25%.
	startHit.OnDrag(barL+0.25*(barR-barL), 20, true)
	if !gotDone || gotStart < 24*time.Second || gotStart > 26*time.Second || gotEnd != 0 {
		t.Fatalf("start drag: start=%v end=%v done=%v", gotStart, gotEnd, gotDone)
	}
	// Drag end handle to 75%.
	endHit.OnDrag(barL+0.75*(barR-barL), 20, true)
	if !gotDone || gotEnd < 74*time.Second || gotEnd > 76*time.Second {
		t.Fatalf("end drag: start=%v end=%v done=%v", gotStart, gotEnd, gotDone)
	}
	// Drag end handle back to the far right edge snaps to 0 (untrimmed).
	endHit.OnDrag(barR, 20, true)
	if gotEnd != 0 {
		t.Fatalf("end edge snap = %v, want 0", gotEnd)
	}
}
