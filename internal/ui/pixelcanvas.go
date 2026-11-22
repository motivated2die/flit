package ui

import "math"

// pixelCanvas draws straight into a premultiplied BGRA buffer (a window's
// kept surface) from Go: anti-aliased rounded rectangles and circles,
// nothing else. It is how live regions (Ctx.DrawLive) repaint between full
// paints: a few small shapes cost microseconds this way, where a Direct2D
// pass costs a quarter of a millisecond before it draws anything
// (docs/research/window-rendering.md). Text, polygons and bitmaps are
// ignored, so a live region must draw only shapes.
type pixelCanvas struct {
	pix   []byte
	w, h  int
	clips []RectF
}

func (p *pixelCanvas) clip() RectF {
	if len(p.clips) == 0 {
		return RectF{0, 0, float32(p.w), float32(p.h)}
	}
	return p.clips[len(p.clips)-1]
}

func (p *pixelCanvas) PushClip(r RectF) {
	if len(p.clips) > 0 {
		r = r.Intersect(p.clip())
	}
	p.clips = append(p.clips, r)
}

func (p *pixelCanvas) PopClip() {
	if len(p.clips) > 0 {
		p.clips = p.clips[:len(p.clips)-1]
	}
}

// RoundRect fills r with corners of radius rad, anti-aliased.
func (p *pixelCanvas) RoundRect(r RectF, rad float32, c ColorF) {
	k := r.Intersect(p.clip())
	x0, y0 := max(0, int(math.Floor(float64(k.L)))), max(0, int(math.Floor(float64(k.T))))
	x1, y1 := min(p.w, int(math.Ceil(float64(k.R)))), min(p.h, int(math.Ceil(float64(k.B))))
	w, h := float64(r.W()), float64(r.H())
	rr := math.Max(0, math.Min(float64(rad), math.Min(w, h)/2))
	if rr == 0 && float64(r.L) == math.Floor(float64(r.L)) && float64(r.T) == math.Floor(float64(r.T)) &&
		float64(r.R) == math.Floor(float64(r.R)) && float64(r.B) == math.Floor(float64(r.B)) {
		for y := y0; y < y1; y++ { // a whole-pixel rectangle: every pixel fully covered
			for x := x0; x < x1; x++ {
				p.blend(x, y, c, 1)
			}
		}
		return
	}
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			// Coverage from the distance to the edge, at the pixel centre.
			d := edgeDepth(float64(x)+0.5-float64(r.L), float64(y)+0.5-float64(r.T), w, h, rr)
			cov := math.Max(0, math.Min(1, d+0.5))
			// The clip's own edges cut whole pixels (they are whole pixels).
			if cov > 0 {
				p.blend(x, y, c, float32(cov))
			}
		}
	}
}

// Circle fills a circle, anti-aliased.
func (p *pixelCanvas) Circle(cx, cy, rad float32, c ColorF) {
	p.RoundRect(RectF{cx - rad, cy - rad, cx + rad, cy + rad}, rad, c)
}

func (p *pixelCanvas) blend(x, y int, c ColorF, cov float32) {
	a := c.A * cov
	i := 4 * (y*p.w + x)
	q := p.pix[i : i+4 : i+4]
	if a >= 1 { // covered by an opaque colour: just write it
		q[0], q[1], q[2], q[3] = uint8(c.B*255+0.5), uint8(c.G*255+0.5), uint8(c.R*255+0.5), 255
		return
	}
	k := 1 - a
	q[0] = uint8(c.B*255*a + float32(q[0])*k + 0.5)
	q[1] = uint8(c.G*255*a + float32(q[1])*k + 0.5)
	q[2] = uint8(c.R*255*a + float32(q[2])*k + 0.5)
	q[3] = uint8(255*a + float32(q[3])*k + 0.5)
}

func (p *pixelCanvas) Arc(cx, cy, inner, outer, a0, a1 float32, c ColorF)       {}
func (p *pixelCanvas) Text(string, RectF, Font, ColorF)                         {}
func (p *pixelCanvas) Paragraph(string, RectF, Font, ColorF)                    {}
func (p *pixelCanvas) MarkedParagraph(string, RectF, Font, ColorF, []TextMark)  {}
func (p *pixelCanvas) StyledParagraph(string, RectF, Font, ColorF, []TextStyle) {}
func (p *pixelCanvas) Polygons([][]PointF, ColorF)                              {}
func (p *pixelCanvas) Bitmap(string, float32, float32, int, int, float32, func() []byte) {
}

var _ Canvas = (*pixelCanvas)(nil)
