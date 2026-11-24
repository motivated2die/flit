package ui

import (
	"fmt"
	"image/color"
	"math"
)

// Soft light in a corner, where the recording dot is: the tray panel's top
// left while recording, the window's bottom left beside the sidebar's live
// status. The idea is Resound's panel glow (its glow.go): a blurred dot of
// the accent colour as if it sat just outside the surface, fading with a
// Gaussian that reaches exactly zero at its ellipse so it has no edge, and
// dithered so the faint gradient doesn't band on the dark background.
// flit's own code, drawn as a cached bitmap through Canvas.Bitmap.

// glowSpec is one light, in logical pixels.
type glowSpec struct {
	RX, RY   float32 // how far it reaches across and up/down
	Strength float64 // the colour's opacity at the centre (0..1)
	Falloff  float64 // how quickly it fades; higher is a tighter light
}

// liveGlow is the light around the recording dot's corner.
var liveGlow = glowSpec{RX: 150, RY: 140, Strength: 0.2, Falloff: 5.5}

// Glow draws g's light in colour k centred at (cx, cy) (device pixels), at
// an opacity (a breathing light changes only this, not the cached bitmap).
// What falls outside the current clip isn't drawn.
func (c *Ctx) Glow(cx, cy float32, g glowSpec, k color.RGBA, opacity float32) {
	rx, ry := c.Px(g.RX), c.Px(g.RY)
	w, h := int(2*rx), int(2*ry)
	if w <= 0 || h <= 0 {
		return
	}
	key := fmt.Sprintf("glow:%d:%d:%v:%v:%v", w, h, k, g.Strength, g.Falloff)
	x, y := float32(math.Round(float64(cx-rx))), float32(math.Round(float64(cy-ry)))
	c.Draw(func(cv Canvas) {
		cv.Bitmap(key, x, y, w, h, opacity, func() []byte { return glowPixels(w, h, k, g.Strength, g.Falloff) })
	})
}

const rippleTexSize = 160

// SoftRipple draws a soft-edged, feathered circular area of colour k centred
// at (cx, cy) with radius r (device pixels) at opacity. One canonical texture
// is cached on the GPU and scaled smoothly per frame with linear filtering so
// the expanding disc dissipates uniformly as a whole.
func (c *Ctx) SoftRipple(cx, cy, r float32, k color.RGBA, opacity float32) {
	if r <= 0 || opacity <= 0 {
		return
	}
	key := fmt.Sprintf("ripple:%d:%v", rippleTexSize, k)
	dst := RectF{cx - r, cy - r, cx + r, cy + r}
	c.Draw(func(cv Canvas) {
		cv.ScaledBitmap(key, dst, rippleTexSize, rippleTexSize, opacity, func() []byte {
			return rippleDiscPixels(rippleTexSize, k)
		})
	})
}

// rippleDiscPixels renders a size×size premultiplied BGRA filled circular disc
// of colour k: uniform inside innerR, then smoothly feathered with smoothstep
// to exact zero at u=1.0 so there is no hard edge, dithered against banding.
func rippleDiscPixels(size int, k color.RGBA) []byte {
	pix := make([]byte, 4*size*size)
	half := float64(size) / 2
	const (
		innerR = 0.62
		fillA  = 0.30
	)
	for y := range size {
		for x := range size {
			dx, dy := (float64(x)+0.5-half)/half, (float64(y)+0.5-half)/half
			u := math.Hypot(dx, dy)
			if u >= 1 {
				continue
			}
			a := fillA
			if u > innerR {
				t := (1 - u) / (1 - innerR)
				a = fillA * t * t * (3 - 2*t) // smoothstep fade to 0 at u=1
			}
			alpha := math.Round(a*255 + dither(x, y) - 0.5)
			if alpha <= 0 {
				continue
			}
			alpha = min(255, alpha)
			i := 4 * (y*size + x)
			pix[i] = uint8(math.Round(float64(k.B) * alpha / 255))
			pix[i+1] = uint8(math.Round(float64(k.G) * alpha / 255))
			pix[i+2] = uint8(math.Round(float64(k.R) * alpha / 255))
			pix[i+3] = uint8(alpha)
		}
	}
	return pix
}

// glowPixels renders a w×h box holding an ellipse of light of colour k
// (centred, touching the box's edges) as premultiplied BGRA.
func glowPixels(w, h int, k color.RGBA, strength, falloff float64) []byte {
	pix := make([]byte, 4*w*h)
	cx, cy := float64(w)/2, float64(h)/2
	floor := math.Exp(-falloff)
	for y := range h {
		for x := range w {
			dx, dy := (float64(x)+0.5-cx)/cx, (float64(y)+0.5-cy)/cy
			t := dx*dx + dy*dy
			if t >= 1 {
				continue // outside the light: transparent
			}
			a := strength * (math.Exp(-falloff*t) - floor) / (1 - floor)
			alpha := math.Round(a*255 + dither(x, y) - 0.5)
			if alpha <= 0 {
				continue
			}
			alpha = min(255, alpha)
			i := 4 * (y*w + x)
			pix[i] = uint8(math.Round(float64(k.B) * alpha / 255))
			pix[i+1] = uint8(math.Round(float64(k.G) * alpha / 255))
			pix[i+2] = uint8(math.Round(float64(k.R) * alpha / 255))
			pix[i+3] = uint8(alpha)
		}
	}
	return pix
}

// dither is interleaved gradient noise in [0, 1): a fixed, even pattern
// that turns a faint gradient's steps into fine grain.
func dither(x, y int) float64 {
	_, f := math.Modf(52.9829189 * math.Mod(0.06711056*float64(x)+0.00583715*float64(y), 1))
	return f
}

// The panel's 1-pixel border is painted by flit (Windows' own is turned
// off) so the light can reach it: near the lit corner the edge turns from
// the plain edge colour towards a light tint of the accent.
const (
	borderLit   = 0.5 // how far towards the light colour the edge goes at the corner
	borderReach = 1.4 // how much further than the background light it reaches
)

// borderPixels renders the 1-pixel border of a w×h surface whose corners
// are rounded with radius r (pixels; Windows rounds the window itself) as
// premultiplied BGRA: base, lit towards light near the top left corner by
// g's falloff over rx, ry pixels (lit false: plain base). The ring's inner
// edge is anti-aliased; the outer one is the window's.
func borderPixels(w, h int, r float64, base, light color.RGBA, lit bool, rx, ry float64, g glowSpec) []byte {
	pix := make([]byte, 4*w*h)
	floor := math.Exp(-g.Falloff)
	// The ring only lies near the edges: visit those pixels.
	band := int(math.Ceil(r)) + 2
	put := func(x, y int) {
		px, py := float64(x)+0.5, float64(y)+0.5
		d := edgeDepth(px, py, float64(w), float64(h), r)
		cover := min(1, 1.5-d) // 1 up to the ring's inner edge, then fading out
		if d < -0.5 || cover <= 0 {
			return
		}
		l := 0.0
		if t := (px/rx)*(px/rx) + (py/ry)*(py/ry); lit && t < 1 {
			l = borderLit * (math.Exp(-g.Falloff*t) - floor) / (1 - floor)
		}
		mix := func(b, c uint8) uint8 {
			return uint8(math.Round((float64(b) + (float64(c)-float64(b))*l) * cover))
		}
		i := 4 * (y*w + x)
		pix[i], pix[i+1], pix[i+2] = mix(base.B, light.B), mix(base.G, light.G), mix(base.R, light.R)
		pix[i+3] = uint8(math.Round(255 * cover))
	}
	for y := range h {
		if y < band || y >= h-band {
			for x := range w {
				put(x, y)
			}
			continue
		}
		for x := 0; x < min(band, w); x++ {
			put(x, y)
			put(w-1-x, y)
		}
	}
	return pix
}

// edgeDepth is how far (x, y) lies inside the w×h rectangle whose corners
// are rounded with radius r: 0 on its edge, negative outside.
func edgeDepth(x, y, w, h, r float64) float64 {
	// In a corner's square, the edge is the corner's arc around (cx, cy).
	cx, cy := min(max(x, r), w-r), min(max(y, r), h-r)
	if cx != x && cy != y {
		return r - math.Hypot(x-cx, y-cy)
	}
	return min(x, y, w-x, h-y)
}
