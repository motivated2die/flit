package ui

import (
	"image"
	"image/color"
	"math"
)

// TrayState is what the tray icon shows.
type TrayState int

const (
	TrayIdle         TrayState = iota // waiting for a meeting
	TrayDetected                      // a meeting app is in a call; asking whether to transcribe
	TrayRecording                     // recording both tracks
	TrayTranscribing                  // with progress
	TrayReady                         // transcript ready to copy
	TrayError
)

// TrayIcon describes one icon frame.
type TrayIcon struct {
	State    TrayState
	Progress float64 // 0..1, for TrayTranscribing
	Size     int     // pixels (16 at 100%, 20 at 125%, 24 at 150%, 32 at 200%)
	Light    bool    // the taskbar is light: draw dark ink
}

// Draw renders the icon: the feather, with a badge in its lower right
// corner for states that need one. Red appears only while recording.
//
//	idle          the feather
//	detected      + a hollow badge (something noticed)
//	recording     + a red dot
//	transcribing  the feather fills up from the bottom with the progress
//	ready         + a filled badge
//	error         the feather faded, with a hollow badge
func (t TrayIcon) Draw() *image.RGBA {
	n := t.Size
	img := image.NewRGBA(image.Rect(0, 0, n, n))
	s := float64(n)
	ink := ColText
	if t.Light {
		ink = ColInkOnLight
	}
	path := featherSpec{size: s * 1.1, ox: -s * 0.05, oy: -s * 0.08}.path()
	feather := func(c color.RGBA, clip image.Rectangle) {
		layer := image.NewRGBA(img.Bounds())
		FillClipped(layer, path, c, clip)
		over(img, layer)
	}

	switch t.State {
	case TrayTranscribing:
		feather(withAlpha(ink, 0.32), img.Bounds())
		p := math.Max(0, math.Min(1, t.Progress))
		top := int(math.Round((1 - p) * s))
		feather(ink, image.Rect(0, top, n, n))
	case TrayError:
		feather(withAlpha(ink, 0.45), img.Bounds())
	default:
		feather(ink, img.Bounds())
	}

	// The badge: a dot in the lower right, with a gap cut around it so it
	// reads as separate from the feather.
	r := s * 0.2
	cx, cy := s-r-s*0.02, s-r-s*0.02
	switch t.State {
	case TrayRecording:
		Erase(img, (&Path{}).Circle(cx, cy, r+s*0.08))
		Fill(img, (&Path{}).Circle(cx, cy, r), ColLive)
	case TrayReady:
		Erase(img, (&Path{}).Circle(cx, cy, r+s*0.08))
		Fill(img, (&Path{}).Circle(cx, cy, r*0.85), ink)
	case TrayDetected, TrayError:
		Erase(img, (&Path{}).Circle(cx, cy, r+s*0.08))
		ring := (&Path{}).Circle(cx, cy, r*0.95)
		Fill(img, ring, ink)
		Erase(img, (&Path{}).Circle(cx, cy, r*0.95-math.Max(1.2, s*0.075)))
	}
	return img
}

// over composites src over dst (both straight alpha).
func over(dst, src *image.RGBA) {
	for i := 0; i < len(src.Pix); i += 4 {
		if a := src.Pix[i+3]; a > 0 {
			blend(dst, (i/4)%src.Rect.Dx(), (i/4)/src.Rect.Dx(), color.RGBA{src.Pix[i], src.Pix[i+1], src.Pix[i+2], 255}, float64(a)/255)
		}
	}
}

func withAlpha(c color.RGBA, a float64) color.RGBA {
	return color.RGBA{c.R, c.G, c.B, uint8(float64(c.A) * a)}
}

// blend paints c with coverage cov over the pixel (straight alpha, "over").
func blend(img *image.RGBA, x, y int, c color.RGBA, cov float64) {
	a := float64(c.A) / 255 * cov
	if a <= 0 {
		return
	}
	i := img.PixOffset(x, y)
	p := img.Pix[i : i+4]
	da := float64(p[3]) / 255
	oa := a + da*(1-a)
	mix := func(src, dst uint8) uint8 {
		return uint8((float64(src)*a + float64(dst)*da*(1-a)) / oa)
	}
	p[0], p[1], p[2] = mix(c.R, p[0]), mix(c.G, p[1]), mix(c.B, p[2])
	p[3] = uint8(oa*255 + 0.5)
}
