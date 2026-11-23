package ui

import (
	"math"
	"time"
)

// VoiceMeter shows both sides of a recording at a glance: one box with
// "You" and "Meeting" side by side (or stacked, in a narrow box), each with a small curve of bars that
// moves with the voice. The bars are the bands of the track's voice range
// (capture.Track.Bands), eased towards each new reading, and repaint on
// their own about 60 times a second (Ctx.DrawLive) while the rest of the
// window keeps its pace. The look follows Handy's recording overlay
// (references/handy.md): thin rounded bars, centred, a few pixels tall at
// rest.
type VoiceMeter struct {
	Labels [2]string
	// Bands returns each side's latest bands (0..1); called on every
	// frame of the bars, on the UI thread.
	Bands func() [2][]float32
	// Smooth keeps the shown bars between frames (owned by the caller).
	Smooth *MeterSmoother
	// Stacked puts the two sides in two rows of a narrow box (its Width)
	// instead of side by side across the width it is given.
	Stacked bool
	// Flat integrates the meter directly into a ColBase surface without a
	// nested rounded card, using a compact inline strip with a top hairline.
	Flat bool
}

// Geometry (logical pixels).
const (
	vmH        = 40
	vmFlatH    = 28
	vmRadius   = 10
	vmPadX     = 14
	vmFlatPadX = 2
	vmBarW     = 4
	vmBarGap   = 3
	vmBarMin   = 3
	vmBarMax   = 18
	vmLabelGap = 14 // between a label and its bars
	vmRowH     = 26 // a row of the stacked meter
	vmStackPad = 4  // above and below the stacked meter's rows
)

// MeterSmoother eases shown bars towards the latest readings: quickly up,
// a little slower down, by the time between frames, so the curve flows at
// any frame rate.
type MeterSmoother struct {
	v  [2][]float32
	at time.Time
}

const (
	vmRise = 45 * time.Millisecond  // time constant going up
	vmFall = 130 * time.Millisecond // and down
)

// step moves the shown bars to now (once per instant) and returns them.
func (m *MeterSmoother) step(target [2][]float32, now time.Time) [2][]float32 {
	if !now.After(m.at) && m.v[0] != nil {
		return m.v
	}
	dt := now.Sub(m.at)
	if m.at.IsZero() || dt > time.Second {
		dt = time.Second // first frame, or after a pause: jump
	}
	m.at = now
	for s := range 2 {
		if len(m.v[s]) != len(target[s]) {
			m.v[s] = make([]float32, len(target[s]))
		}
		for i, t := range target[s] {
			tau := vmFall
			if t > m.v[s][i] {
				tau = vmRise
			}
			k := float32(1 - math.Exp(-float64(dt)/float64(tau)))
			m.v[s][i] += (t - m.v[s][i]) * k
		}
	}
	return m.v
}

// Width is the stacked meter's own width: padding, the wider label, the
// bars (a side-by-side meter takes the width it is given).
func (v VoiceMeter) Width(c *Ctx) float32 {
	return c.Px(vmPadX) + v.labelW(c) + c.Px(vmLabelGap) + v.barsW(c) + c.Px(vmPadX)
}

func (v VoiceMeter) labelW(c *Ctx) float32 {
	return max(c.M.TextWidth(v.Labels[0], c.F(fontSmall)), c.M.TextWidth(v.Labels[1], c.F(fontSmall)))
}

func (v VoiceMeter) bars() int {
	if v.Bands == nil || v.Smooth == nil {
		return 0
	}
	return len(v.Bands()[0])
}

func (v VoiceMeter) barsW(c *Ctx) float32 {
	n := v.bars()
	return float32(n)*c.Px(vmBarW) + float32(max(0, n-1))*c.Px(vmBarGap)
}

func (v VoiceMeter) Layout(c *Ctx, x, y, w float32) float32 {
	h := c.Px(vmH)
	if v.Stacked {
		h = c.Px(2*vmRowH + 2*vmStackPad)
	} else if v.Flat {
		h = c.Px(vmFlatH)
	}
	box := RectF{x, y, x + w, y + h}
	if !c.Visible(box) {
		return h
	}
	line := max(1, c.Px(1))
	labels := v.Labels
	padX := c.Px(vmPadX)
	if v.Flat {
		padX = c.Px(vmFlatPadX)
	}
	// Each side's slot: a half of the box, or a row of the stacked box.
	var slots [2]RectF
	var divider RectF
	var topRule RectF
	if v.Stacked {
		top := y + c.Px(vmStackPad)
		for s := range 2 {
			t := top + float32(s)*c.Px(vmRowH)
			slots[s] = RectF{x, t, x + w, t + c.Px(vmRowH)}
		}
		mid := top + c.Px(vmRowH)
		divider = RectF{x + padX, mid - line/2, x + w - padX, mid + line/2}
	} else {
		half := w / 2
		top := y
		if v.Flat {
			topRule = RectF{x, y, x + w, y + line}
			top = y + c.Px(6)
		}
		for s := range 2 {
			slots[s] = RectF{x + float32(s)*half, top, x + float32(s+1)*half, y + h}
		}
		divInset := c.Px(10)
		if v.Flat {
			divInset = c.Px(4)
		}
		divider = RectF{x + half - line/2, top + divInset, x + half + line/2, y + h - divInset}
	}
	c.Draw(func(cv Canvas) {
		if !v.Flat {
			cv.RoundRect(box, c.Px(vmRadius), col(ColBase))
		} else if topRule.H() > 0 {
			cv.RoundRect(topRule, 0, col(ColDivider))
		}
		cv.RoundRect(divider, 0, col(ColDivider))
		for s, r := range slots {
			lx := r.L + padX
			if v.Flat && s == 1 {
				lx = r.L + c.Px(12)
			}
			cv.Text(labels[s], RectF{lx, r.T, r.R, r.B}, c.F(fontSmall), col(ColMuted))
		}
	})
	n := v.bars()
	if n == 0 {
		return h
	}
	bw, gap := c.Px(vmBarW), c.Px(vmBarGap)
	barsW, bh := v.barsW(c), c.Px(vmBarMax)
	// The bars follow their label, at the same place in both slots (after
	// the wider label), and never past the slot's end.
	off := min(padX+v.labelW(c)+c.Px(vmLabelGap), slots[0].W()-padX-barsW)
	bands, smooth := v.Bands, v.Smooth
	for s, r := range slots {
		l := r.L + off
		if v.Flat {
			// Right-align each side's bars cleanly inside its half.
			rightPad := c.Px(12)
			if s == 1 {
				rightPad = padX
			}
			l = r.R - rightPad - barsW
		}
		mid := (r.T + r.B) / 2
		area := RectF{l, mid - bh/2, l + barsW, mid + bh/2}
		side := s
		// Only shapes here: between full paints this region is redrawn
		// from Go into the window's pixels (pixelCanvas).
		c.DrawLive(area, func(cv Canvas, now time.Time) {
			cv.RoundRect(RectF{area.L - 1, area.T - 1, area.R + 1, area.B + 1}, 0, col(ColBase)) // over the last frame
			mid := (area.T + area.B) / 2
			for i, lv := range smooth.step(bands(), now)[side] {
				lv = max(0, min(1, lv))
				bar := c.Px(vmBarMin) + float32(math.Pow(float64(lv), 0.7))*c.Px(vmBarMax-vmBarMin)
				bx := area.L + float32(i)*(bw+gap)
				// Quiet bars are dimmer, so speaking lights the curve up.
				cv.RoundRect(RectF{bx, mid - bar/2, bx + bw, mid + bar/2}, bw/2, RGBA(ColText, 0.45+0.55*min(1, lv*1.6)))
			}
		})
	}
	return h
}

// Aside lays Side (Sized) out on the right of Main, when Main keeps at
// least MinMain logical pixels beside it; otherwise Main alone, with
// Below under it (Home's recording card: the stacked voice meter beside
// the title, or the wide one under it in a narrow window).
type Aside struct {
	Main, Side, Below Node
	MinMain           float32
	Gap               float32 // logical pixels between Main and Side, or Main and Below
}

func (a Aside) Layout(c *Ctx, x, y, w float32) float32 {
	gap := c.Px(a.Gap)
	sw := float32(0)
	if sz, ok := a.Side.(Sized); ok {
		sw = sz.Width(c)
	}
	if sw > 0 && w-sw-gap >= c.Px(a.MinMain) {
		mh := a.Main.Layout(c, x, y, w-sw-gap)
		return max(mh, a.Side.Layout(c, x+w-sw, y, sw))
	}
	h := a.Main.Layout(c, x, y, w)
	if a.Below != nil {
		h += gap + a.Below.Layout(c, x, y+h+gap, w)
	}
	return h
}
