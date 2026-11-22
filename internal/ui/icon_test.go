package ui

import (
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestIconsParse(t *testing.T) {
	names := IconNames()
	if len(names) < 20 {
		t.Fatalf("only %d icons embedded", len(names))
	}
	for _, n := range names {
		sh, err := loadIcon(n)
		if err != nil {
			t.Fatalf("%s: %v", n, err)
		}
		if len(sh.strokes)+len(sh.fills) == 0 {
			t.Fatalf("%s: nothing to draw", n)
		}
		for _, size := range []float32{14, 18, 27} {
			polys := iconOutline(n, size)
			if len(polys) == 0 {
				t.Fatalf("%s at %v: no polygons", n, size)
			}
			// Everything inside the box (a stroke reaches half its width
			// past the view box at most).
			for _, p := range polys {
				for _, v := range p {
					if v.X < -1 || v.Y < -1 || v.X > size+1 || v.Y > size+1 {
						t.Fatalf("%s at %v: point %v outside the box", n, size, v)
					}
				}
			}
		}
	}
}

func TestGlyphsAreIcons(t *testing.T) {
	have := map[string]bool{}
	for _, n := range IconNames() {
		have[n] = true
	}
	for _, g := range []string{GlyphHome, GlyphHistory, GlyphSettings, GlyphFolder, GlyphCopy, GlyphMic, GlyphStop, GlyphRecord,
		GlyphDocument, GlyphBack, GlyphChevron, GlyphRefresh, GlyphCheck, GlyphWarning, GlyphGlobe, GlyphEdit, GlyphCalendar,
		GlyphMeeting, GlyphOpenLink, GlyphPlay, GlyphPause, GlyphDelete, GlyphArticle, GlyphSearch, GlyphPeople, GlyphPower,
		GlyphUp, GlyphDown, glyphMinimize, glyphClose} {
		if !have[g] {
			t.Errorf("no icons/%s.svg", g)
		}
	}
}

func TestPathTokens(t *testing.T) {
	// Tabler writes numbers as tightly as SVG allows.
	got := pathTokens("M1.5.5c-1.1 0-2 .9-2 2h1e1z")
	want := []pathTok{{cmd: 'M'}, {v: 1.5}, {v: .5}, {cmd: 'c'}, {v: -1.1}, {v: 0}, {v: -2}, {v: .9}, {v: -2}, {v: 2}, {cmd: 'h'}, {v: 10}, {cmd: 'z'}}
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("token %d: %v, want %v", i, got[i], want[i])
		}
	}
}

func TestArcAndRelativePath(t *testing.T) {
	// A half circle from (2,12) to (22,12), relative, then closed.
	lines, err := parsePath("M2 12a10 10 0 0 1 20 0z")
	if err != nil || len(lines) != 1 || !lines[0].closed {
		t.Fatalf("%v %v", lines, err)
	}
	pts := lines[0].pts
	if end := pts[len(pts)-1]; end != (vec{22, 12}) {
		t.Fatalf("arc ends at %v", end)
	}
	// Sweep 1 from left to right goes over the top (y < 12), through (12, 2).
	top := 12.0
	for _, p := range pts {
		if math.Hypot(p.x-12, p.y-12) > 10.01 || math.Hypot(p.x-12, p.y-12) < 9.99 {
			t.Fatalf("point %v off the circle", p)
		}
		top = min(top, p.y)
	}
	if math.Abs(top-2) > 0.05 {
		t.Fatalf("highest point %v, want 2", top)
	}
}

func TestIconPicture(t *testing.T) {
	dir := os.Getenv("FLIT_RENDER_DIR")
	if dir == "" {
		t.Skip("FLIT_RENDER_DIR not set")
	}
	names := IconNames()
	n := nodeOf(func(c *Ctx, x, y, w float32) float32 {
		c.Draw(func(cv Canvas) {
			for i, name := range names {
				for j, size := range []float32{14, 18, 24} {
					cx := x + float32(i%10)*30
					cy := y + float32(i/10)*96 + float32(j)*30
					DrawIcon(cv, name, RectF{cx, cy, cx + 28, cy + 28}, size, col(ColText))
				}
			}
		})
		return float32((len(names)+9)/10) * 96
	})
	s, err := RenderPanel(n, 1, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Free()
	fh, _ := os.Create(filepath.Join(dir, "icons.png"))
	png.Encode(fh, SurfaceImage(s))
	fh.Close()
}

type nodeOf func(c *Ctx, x, y, w float32) float32

func (f nodeOf) Layout(c *Ctx, x, y, w float32) float32 { return f(c, x, y, w) }
