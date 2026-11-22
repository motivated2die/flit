package ui

import (
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestLiveRepaintMatches draws the voice meter with Direct2D, then repaints
// its live regions from Go, as the window does between full paints: the
// bars must look the same, and a live frame must be cheap.
func TestLiveRepaintMatches(t *testing.T) {
	m, err := D2DMetrics()
	if err != nil {
		t.Skip(err)
	}
	for _, sc := range []float32{1, 1.5} {
		at := time.Now()
		var sm MeterSmoother
		node := VoiceMeter{Labels: [2]string{"You", "Meeting"}, Bands: speaking, Smooth: &sm}
		s, _ := NewSurface(int32(700*sc), int32(80*sc))
		c := NewCtx(sc, m, at, "", "", nil)
		node.Layout(c, 20*sc, 20*sc, 640*sc)
		p, _ := BeginWith(s, col(ColCard))
		c.Paint(p)
		p.End()
		procGdiFlush.Call()
		d2d := append([]byte(nil), s.Bits...)

		pc := &pixelCanvas{pix: s.Bits, w: int(s.W), h: int(s.H)}
		n := 1000
		t0 := time.Now()
		for range n {
			for _, a := range c.live {
				pc.PushClip(a.r)
				a.draw(pc, at) // the same instant: the same bars
				pc.PopClip()
			}
		}
		per := time.Since(t0) / time.Duration(n)
		t.Logf("scale %.1f: %v per live frame", sc, per)
		if per > time.Millisecond { // measured 0.04–0.1 ms; Direct2D would be 0.45+
			t.Errorf("a live frame takes %v", per)
		}
		// Compare inside the live regions: mean difference tiny, and no
		// pixel far off (anti-aliasing differs a little at the edges).
		var sum, worst float64
		count := 0
		for _, a := range c.live {
			for y := int(a.r.T); y < int(a.r.B); y++ {
				for x := int(a.r.L); x < int(a.r.R); x++ {
					i := 4 * (y*int(s.W) + x)
					for j := range 3 {
						d := math.Abs(float64(d2d[i+j]) - float64(s.Bits[i+j]))
						sum += d
						worst = max(worst, d)
						count++
					}
				}
			}
		}
		if mean := sum / float64(count); mean > 3 || worst > 90 {
			t.Errorf("scale %.1f: Go-drawn bars differ from Direct2D's: mean %.1f, worst %.0f", sc, mean, worst)
		}
		if dir := os.Getenv("FLIT_RENDER_DIR"); dir != "" && sc == 1.5 {
			fh, _ := os.Create(filepath.Join(dir, "voice-meter-live.png"))
			png.Encode(fh, SurfaceImage(s))
			fh.Close()
		}
		s.Free()
	}
}

// TestDirectWriteLines: the lines DirectWrite reports for selection add up
// to the paragraph's measured height, cover the text, and their stops run
// left to right.
func TestDirectWriteLines(t *testing.T) {
	m, err := D2DMetrics()
	if err != nil {
		t.Skip(err)
	}
	lm := m.(LineMetrics)
	f := fontBody
	s := "Von meiner Seite gibt es noch zwei offene Punkte beim Installer, die wir vor dem Release klären sollten, sonst wird es knapp."
	lines := lm.ParagraphLines(s, f, 300)
	if len(lines.Lines) < 2 || lines.End() != len(s) || lines.Lines[0].From != 0 {
		t.Fatalf("%d lines, end %d of %d", len(lines.Lines), lines.End(), len(s))
	}
	var total float32
	for i, l := range lines.Lines {
		total += l.H
		if i > 0 && l.From != lines.Lines[i-1].To {
			t.Fatalf("line %d starts at %d, the last ended at %d", i, l.From, lines.Lines[i-1].To)
		}
		for j := 1; j < len(l.Stops); j++ {
			if l.Stops[j].X < l.Stops[j-1].X || l.Stops[j].At <= l.Stops[j-1].At {
				t.Fatalf("line %d stops out of order: %+v", i, l.Stops[j-1:j+1])
			}
		}
	}
	if h := m.ParagraphHeight(s, f, 300); total < h-1 || total > h+1 {
		t.Fatalf("lines %v tall, the paragraph %v", total, h)
	}
	// "klären": a stop right after "ä" (two bytes), none inside it.
	i := strings.Index(s, "ä")
	for _, l := range lines.Lines {
		for _, st := range l.Stops {
			if st.At == i+1 {
				t.Fatal("a stop inside ä")
			}
		}
	}
}
