package ui

import (
	"image"
	"image/color"
	"math"
	"sort"
)

// A tiny vector rasterizer for flit's icons (app icon, tray states): paths
// of lines and Bézier curves, filled with the non-zero winding rule and
// anti-aliased with 16 sub-scanlines and exact horizontal coverage, which
// keeps edges clean even at 16 px. Coordinates are in pixels.

// Path is a set of closed polygons.
type Path struct {
	polys [][]pt2
	cur   []pt2
}

type pt2 struct{ x, y float64 }

// MoveTo starts a new subpath.
func (p *Path) MoveTo(x, y float64) *Path {
	p.close()
	p.cur = []pt2{{x, y}}
	return p
}

// LineTo adds a straight segment.
func (p *Path) LineTo(x, y float64) *Path {
	p.cur = append(p.cur, pt2{x, y})
	return p
}

// QuadTo adds a quadratic Bézier.
func (p *Path) QuadTo(cx, cy, x, y float64) *Path {
	a := p.last()
	for i := 1; i <= 32; i++ { // fine enough for a 256 px icon
		t := float64(i) / 32
		u := 1 - t
		p.cur = append(p.cur, pt2{u*u*a.x + 2*u*t*cx + t*t*x, u*u*a.y + 2*u*t*cy + t*t*y})
	}
	return p
}

// CubicTo adds a cubic Bézier.
func (p *Path) CubicTo(c1x, c1y, c2x, c2y, x, y float64) *Path {
	a := p.last()
	for i := 1; i <= 48; i++ {
		t := float64(i) / 48
		u := 1 - t
		p.cur = append(p.cur, pt2{
			u*u*u*a.x + 3*u*u*t*c1x + 3*u*t*t*c2x + t*t*t*x,
			u*u*u*a.y + 3*u*u*t*c1y + 3*u*t*t*c2y + t*t*t*y,
		})
	}
	return p
}

// Close ends the current subpath.
func (p *Path) Close() *Path {
	p.close()
	return p
}

func (p *Path) last() pt2 {
	if len(p.cur) == 0 {
		return pt2{}
	}
	return p.cur[len(p.cur)-1]
}

func (p *Path) close() {
	if len(p.cur) >= 3 {
		p.polys = append(p.polys, p.cur)
	}
	p.cur = nil
}

// Circle adds a full circle.
func (p *Path) Circle(cx, cy, r float64) *Path {
	p.close()
	n := max(16, int(r*4))
	for i := 0; i < n; i++ {
		a := 2 * math.Pi * float64(i) / float64(n)
		p.cur = append(p.cur, pt2{cx + r*math.Cos(a), cy + r*math.Sin(a)})
	}
	return p.Close()
}

// Capsule adds a stroke of width w from (x0, y0) to (x1, y1) with round
// ends.
func (p *Path) Capsule(x0, y0, x1, y1, w float64) *Path {
	p.close()
	dx, dy := x1-x0, y1-y0
	l := math.Hypot(dx, dy)
	if l == 0 {
		return p.Circle(x0, y0, w/2)
	}
	ang := math.Atan2(dy, dx)
	r := w / 2
	const n = 10
	for i := 0; i <= n; i++ { // around the end
		a := ang - math.Pi/2 + math.Pi*float64(i)/n
		p.cur = append(p.cur, pt2{x1 + r*math.Cos(a), y1 + r*math.Sin(a)})
	}
	for i := 0; i <= n; i++ { // around the start
		a := ang + math.Pi/2 + math.Pi*float64(i)/n
		p.cur = append(p.cur, pt2{x0 + r*math.Cos(a), y0 + r*math.Sin(a)})
	}
	return p.Close()
}

// Transform returns the path scaled by s and moved by (dx, dy).
func (p *Path) Transform(s, dx, dy float64) *Path {
	p.close()
	out := &Path{}
	for _, poly := range p.polys {
		q := make([]pt2, len(poly))
		for i, v := range poly {
			q[i] = pt2{v.x*s + dx, v.y*s + dy}
		}
		out.polys = append(out.polys, q)
	}
	return out
}

// coverage returns per-pixel coverage (0..1) of the path in a w×h grid,
// restricted to clip.
func (p *Path) coverage(w, h int, clip image.Rectangle) []float64 {
	p.close()
	cov := make([]float64, w*h)
	const sub = 16
	type cross struct {
		x   float64
		dir int
	}
	var xs []cross
	row := make([]float64, w+1)
	for y := max(0, clip.Min.Y); y < min(h, clip.Max.Y); y++ {
		for i := range row {
			row[i] = 0
		}
		any := false
		for s := 0; s < sub; s++ {
			sy := float64(y) + (float64(s)+0.5)/sub
			xs = xs[:0]
			for _, poly := range p.polys {
				for i := range poly {
					a, b := poly[i], poly[(i+1)%len(poly)]
					if (a.y <= sy) == (b.y <= sy) {
						continue
					}
					x := a.x + (sy-a.y)*(b.x-a.x)/(b.y-a.y)
					dir := 1
					if b.y < a.y {
						dir = -1
					}
					xs = append(xs, cross{x, dir})
				}
			}
			if len(xs) < 2 {
				continue
			}
			sort.Slice(xs, func(i, j int) bool { return xs[i].x < xs[j].x })
			wind := 0
			for i := 0; i < len(xs)-1; i++ {
				wind += xs[i].dir
				if wind == 0 {
					continue
				}
				x0, x1 := math.Max(xs[i].x, float64(clip.Min.X)), math.Min(xs[i+1].x, float64(min(w, clip.Max.X)))
				if x1 <= x0 {
					continue
				}
				any = true
				// Exact coverage of [x0, x1) over each pixel column.
				for px := int(math.Floor(x0)); px < int(math.Ceil(x1)) && px < w; px++ {
					if px < 0 {
						continue
					}
					l, r := math.Max(x0, float64(px)), math.Min(x1, float64(px+1))
					if r > l {
						row[px] += (r - l) / sub
					}
				}
			}
		}
		if any {
			for x := 0; x < w; x++ {
				cov[y*w+x] = math.Min(1, row[x])
			}
		}
	}
	return cov
}

// Fill paints the path in c over img.
func Fill(img *image.RGBA, p *Path, c color.RGBA) { FillClipped(img, p, c, img.Bounds()) }

// FillClipped paints the path in c over img, only inside clip.
func FillClipped(img *image.RGBA, p *Path, c color.RGBA, clip image.Rectangle) {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	for i, v := range p.coverage(w, h, clip) {
		if v > 0 {
			blend(img, i%w, i/w, c, v)
		}
	}
}

// Erase cuts the path out of img (reduces alpha by its coverage).
func Erase(img *image.RGBA, p *Path) {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	for i, v := range p.coverage(w, h, img.Bounds()) {
		if v > 0 {
			a := &img.Pix[i*4+3]
			*a = uint8(float64(*a)*(1-v) + 0.5)
		}
	}
}
