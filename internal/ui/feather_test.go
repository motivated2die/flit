package ui

import (
	"testing"
)

// The mark is read from assets/flit.svg (embedded): its path fitted into
// the unit square, its gradient fading to the last stop's opacity.
func TestFeatherFromSVG(t *testing.T) {
	a := featherSrc()
	if a.fade <= 0 || a.fade >= 1 || a.x1 == a.x2 {
		t.Fatalf("gradient not read: %+v", a)
	}
	minX, minY, maxX, maxY := feather().bounds()
	if d := minY - featherTop; d < -1e-9 || d > 1e-9 || maxY-minY < featherHeight-1e-9 || (minX+maxX)/2 < 0.499 || (minX+maxX)/2 > 0.501 {
		t.Fatalf("not fitted: %v %v %v %v", minX, minY, maxX, maxY)
	}
	if _, err := parseFeatherSVG(`<svg><rect/></svg>`); err == nil {
		t.Fatal("an SVG without a path accepted")
	}
	flat, err := parseFeatherSVG(`<svg><path d="M0 0L10 0L10 10Z" fill="white"/></svg>`)
	if err != nil || flat.fade != 0 {
		t.Fatalf("flat fill: %+v %v", flat, err)
	}
}

func TestParseSVGPath(t *testing.T) {
	p, err := ParseSVGPath("M0 0h10v10H0z m20,0 l5-5.5.5 1 Z")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.polys) != 2 {
		t.Fatalf("%d polygons", len(p.polys))
	}
	if got := p.polys[0][2]; got != (pt2{10, 10}) {
		t.Fatalf("relative h/v: %v", got)
	}
	// "5-5.5.5" is 5, -5.5, .5: from (20,0) to (25,-5.5), then (25.5, -4.5).
	if got := p.polys[1][2]; got != (pt2{25.5, -4.5}) {
		t.Fatalf("number splitting: %v", p.polys[1])
	}
	if _, err := ParseSVGPath("M0 0 L1"); err == nil {
		t.Fatal("missing number accepted")
	}
}
