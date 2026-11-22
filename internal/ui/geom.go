package ui

// ColorF is a colour with straight alpha (D2D1_COLOR_F's layout).
type ColorF struct{ R, G, B, A float32 }

// RGBA makes a ColorF from a palette colour and an opacity.
func RGBA(c interface{ RGBA() (r, g, b, a uint32) }, alpha float32) ColorF {
	r, g, b, _ := c.RGBA()
	return ColorF{float32(r>>8) / 255, float32(g>>8) / 255, float32(b>>8) / 255, alpha}
}

// RectF is a rectangle in pixels (D2D1_RECT_F's layout).
type RectF struct{ L, T, R, B float32 }

// W and H are the rectangle's size.
func (r RectF) W() float32 { return r.R - r.L }
func (r RectF) H() float32 { return r.B - r.T }

// Contains reports whether (x, y) lies inside.
func (r RectF) Contains(x, y float32) bool { return x >= r.L && x < r.R && y >= r.T && y < r.B }

// Inset shrinks the rectangle by d on every side.
func (r RectF) Inset(d float32) RectF { return RectF{r.L + d, r.T + d, r.R - d, r.B - d} }

// Intersect returns the overlap of two rectangles (empty if none).
func (r RectF) Intersect(o RectF) RectF {
	out := RectF{max(r.L, o.L), max(r.T, o.T), min(r.R, o.R), min(r.B, o.B)}
	if out.R < out.L {
		out.R = out.L
	}
	if out.B < out.T {
		out.B = out.T
	}
	return out
}

// Font selects a text format.
type Font struct {
	Family string
	Size   float32 // pixels
	Weight int     // 400 regular, 600 semibold, 700 bold
	Center bool    // centred horizontally
	Right  bool    // aligned right
}
