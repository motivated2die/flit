package ui

import "testing"

func TestGlowPixels(t *testing.T) {
	w, h := 120, 80
	pix := glowPixels(w, h, ColLive, 0.2, 5.5)
	alpha := func(x, y int) int { return int(pix[4*(y*w+x)+3]) }
	// Brightest in the middle, about the strength's opacity.
	if a := alpha(w/2, h/2); a < 48 || a > 54 {
		t.Fatalf("centre alpha %d, want about 51", a)
	}
	// Nothing at the box's edges: the light has no border.
	for x := range w {
		if alpha(x, 0) != 0 || alpha(x, h-1) != 0 {
			t.Fatalf("edge at x=%d lit", x)
		}
	}
	// Fading outwards, and premultiplied (no channel above alpha).
	if !(alpha(w/2, h/2) > alpha(w/2+20, h/2) && alpha(w/2+20, h/2) > alpha(w/2+40, h/2)) {
		t.Fatal("not fading")
	}
	for i := 0; i < len(pix); i += 4 {
		if pix[i] > pix[i+3] || pix[i+1] > pix[i+3] || pix[i+2] > pix[i+3] {
			t.Fatalf("pixel %d not premultiplied: %v", i/4, pix[i:i+4])
		}
	}
}

func TestBorderPixels(t *testing.T) {
	w, h, r := 200, 160, 8.0
	px := func(pix []byte, x, y int) []byte { return pix[4*(y*w+x) : 4*(y*w+x)+4] }
	plain := borderPixels(w, h, r, ColEdge, ColLive, false, 210, 196, liveGlow)
	lit := borderPixels(w, h, r, ColEdge, ColLive, true, 210, 196, liveGlow)
	// Outside the rounded corner, and inside the window: transparent.
	if px(plain, 0, 0)[3] != 0 || px(plain, w/2, h/2)[3] != 0 {
		t.Fatal("border outside the ring")
	}
	// The straight edges are the edge colour, opaque.
	if e := px(plain, w/2, 0); e[3] != 255 || e[2] != ColEdge.R {
		t.Fatalf("top edge %v", e)
	}
	// Lit: redder near the top left corner, plain far from it.
	if near := px(lit, 20, 0); near[2] <= ColEdge.R+20 {
		t.Fatalf("near the light %v", near)
	}
	if far := px(lit, w-20, h-1); far[2] != ColEdge.R {
		t.Fatalf("far from the light %v", far)
	}
}

func TestEdgeDepth(t *testing.T) {
	for _, c := range []struct{ x, y, want float64 }{
		{50, 0, 0}, {50, 3, 3}, {0, 50, 0}, {8, 8, 8}, {0, 0, 8 - 8*1.4142135623730951},
	} {
		if got := edgeDepth(c.x, c.y, 100, 100, 8); got < c.want-1e-9 || got > c.want+1e-9 {
			t.Errorf("edgeDepth(%v, %v) = %v, want %v", c.x, c.y, got, c.want)
		}
	}
}
