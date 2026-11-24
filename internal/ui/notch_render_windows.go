package ui

import (
	"math"
	"time"
)

// notchFrame is one frame's animated state.
type notchFrame struct {
	view   NotchView
	layout NotchLayout
	scale  float32
	slide  float64 // 0 hidden above the edge … 1 in place (progress, for the fade)
	rise   float64 // where the capsule hangs: 0 above the edge, 1 in place, a little more while it bounces
	width  float32 // the capsule's current width (animating to layout.W)
	height float32 // its current height (animating between collapsed and open)
	// collapsed draws the small capsule's cue instead of the content;
	// hidden means the collapsed state is fully hidden (zero height) until hovered.
	// hoverProgress (0..1) is how far resting the pointer on it has come.
	collapsed     bool
	hidden        bool
	hover         bool
	hoverProgress float64
	content       float32 // content opacity while resizing
	hoverBtn      string
	pressBtn      string
	levels        [2]float32 // me, them (0..1, smoothed)
	progress      float32    // smoothed
	now           time.Time
	born          time.Time // when the notch appeared (for the pulse's phase)
}

func notchFill() ColorF       { return RGBA(ColBase, 0.97) }
func notchFillReady() ColorF  { return RGBA(ColCardHover, 0.97) }
func notchEdge() ColorF       { return RGBA(ColEdge, 1) }
func notchTitle() ColorF      { return RGBA(ColText, 1) }
func notchMuted() ColorF      { return RGBA(ColMuted, 1) }
func notchButton() ColorF     { return RGBA(ColControl, 1) }
func notchButtonHov() ColorF  { return RGBA(ColControlHi, 1) }
func notchPrimary() ColorF    { return RGBA(ColText, 1) }
func notchPrimaryHov() ColorF { return RGBA(ColPrimaryHover, 1) }
func notchOnPrimary() ColorF  { return RGBA(ColInk, 1) }
func notchTrack() ColorF      { return RGBA(ColText, 0.18) }

// notchSurfaceSize is the fixed size of the window: the widest capsule
// plus room for the top-edge flares and the shadow below and at the sides.
func notchSurfaceSize(scale float32) (int32, int32) {
	px := func(v float32) int32 { return int32(math.Ceil(float64(v * scale))) }
	return px(notchMaxW + 2*(notchShadow+notchFlareMax)), px(notchHeight + notchHighlightGrowH + notchShadow)
}

// capsuleOrigin is where the capsule's top-left corner is in the surface.
func (f notchFrame) capsuleOrigin(surfW int32) (float32, float32) {
	x := (float32(surfW) - f.width) / 2
	y := -float32(1-f.rise) * (f.height + float32(notchShadow)*f.scale)
	return x, y
}

func renderNotch(s *Surface, f notchFrame) error {
	p, err := Begin(s)
	if err != nil {
		return err
	}
	ox, oy := f.capsuleOrigin(s.W)
	h := f.height
	w := f.width
	fill := notchFill()
	edge := notchEdge()
	if f.view.Mode == NotchStashDrop && f.view.Highlight {
		fill = notchFillReady()
		edge = RGBA(ColText, 0.35)
	}
	if f.collapsed && f.hoverProgress > 0 {
		// Resting the pointer on it: the capsule swells a little and its
		// edge brightens, so the expansion is seen coming.
		hp := float32(EaseOut(f.hoverProgress))
		grow := hp * 5 * f.scale
		dh := hp * 3 * f.scale
		if f.hidden {
			dh = hp * 5 * f.scale
		}
		ox, w, h = ox-grow, w+2*grow, h+dh
		edge = RGBA(ColText, 0.15+0.45*hp)
	}
	if h <= 0.5 {
		return p.End()
	}
	// The blob hangs from the top edge (clamped to y <= 0 so a downward bounce
	// keeps the flares anchored to the top of the screen) with concave flares
	// flowing into the edge and rounded bottom corners.
	top := min(float32(0), oy)
	bottom := oy + h
	spanH := bottom - top
	br := min(h*0.48, w/2)
	fr := min(spanH-br, min(h*0.36, notchFlareMax*f.scale))
	capsule := RectF{ox, top, ox + w, bottom}

	// A soft shadow: a few translucent, growing outlines.
	for i := 1; i <= 5; i++ {
		g := float32(i) * 2 * f.scale
		p.NotchShape(ox-g, top, ox+w+g, bottom+g*1.1, br+g, max(0, fr-g*0.35), ColorF{0, 0, 0, 0.055})
	}
	p.NotchShape(ox-1, top, ox+w+1, bottom+1, br+1, max(0, fr-1), edge)
	p.NotchShape(ox, top, ox+w, bottom, br, fr, fill)
	if cd := f.view.CountdownAt(f.now); cd >= 0 && !f.collapsed {
		// The countdown fills the notch's background from the left.
		p.PushClip(RectF{ox - fr, top, ox - fr + (w+2*fr)*float32(cd), bottom})
		p.NotchShape(ox, top, ox+w, bottom, br, fr, RGBA(ColText, 0.13*f.content))
		p.PopClip()
	}

	a := f.content
	if a <= 0 {
		return p.End()
	}
	if f.collapsed || f.view.Mode == NotchStashed {
		if !f.hidden {
			f.drawCollapsed(p, RectF{capsule.L, oy, capsule.R, capsule.B}, a)
		}
		return p.End()
	}
	// Content keeps its final layout and is centred in the capsule while
	// the capsule resizes, fading in as it reaches its width.
	cx := ox + (f.width-f.layout.W)/2
	cy := oy + (f.height-f.layout.H)/2
	at := func(rc RectF) RectF { return RectF{rc.L + cx, rc.T + cy, rc.R + cx, rc.B + cy} }
	fade := func(c ColorF) ColorF { c.A *= a; return c }
	scaled := func(fn Font) Font { fn.Size *= f.scale; return fn }

	f.drawIcon(p, at(f.layout.Icon), fade)
	link := f.layout.LinkID != "" && f.hoverBtn == f.layout.LinkID
	if f.view.Title != "" {
		p.Text(f.view.Title, at(f.layout.Title), scaled(notchTitleFont), fade(notchTitle()))
	}
	if d := f.view.DetailText(f.now); d != "" {
		dc := notchMuted()
		if link {
			dc = notchTitle()
		}
		p.Text(d, at(f.layout.Detail), scaled(notchDetailFont), fade(dc))
	}
	if link {
		// The title is a link: underlined while the pointer is on it.
		l := at(f.layout.Link)
		y := (l.T+l.B)/2 + 8*f.scale
		p.RoundRect(RectF{l.L, y, l.R - 1, y + max(1, f.scale)}, 0, fade(RGBA(ColText, 0.7)))
	}
	if f.layout.Levels.W() > 0 {
		f.drawLevels(p, at(f.layout.Levels), fade)
	}
	for i, br := range f.layout.Buttons {
		rc := at(br)
		id := f.layout.IDs[i]
		fill, text := notchButton(), notchTitle()
		if f.layout.Primary[i] {
			fill, text = notchPrimary(), notchOnPrimary()
			if id == f.hoverBtn {
				fill = notchPrimaryHov()
			}
		} else if id == f.hoverBtn {
			fill = notchButtonHov()
		}
		if id == f.pressBtn {
			rc = RectF{rc.L + 1, rc.T + 1, rc.R - 1, rc.B - 1}
		}
		p.RoundRect(rc, rc.H()/2, fade(fill))
		if i < len(f.layout.Glyphs) && f.layout.Glyphs[i] != "" && f.layout.Labels[i] == "" {
			DrawIcon(p, f.layout.Glyphs[i], rc, 14*f.scale, fade(text))
		} else {
			p.Text(f.layout.Labels[i], rc, scaled(notchButtonFont), fade(text))
		}
	}
	return p.End()
}

func (f notchFrame) drawIcon(p *Painter, rc RectF, fade func(ColorF) ColorF) {
	cx, cy := (rc.L+rc.R)/2, (rc.T+rc.B)/2
	rad := rc.W() / 2
	pulse := float32(Pulse(f.now.Sub(f.born), notchPulse))
	switch f.view.Mode {
	case NotchRecording:
		// A breathing red dot with a soft halo.
		p.Circle(cx, cy, rad*(0.9+0.35*pulse), fade(RGBA(ColLive, 0.22*pulse)))
		p.Circle(cx, cy, rad*0.62, fade(RGBA(ColLive, 0.75+0.25*pulse)))
	case NotchPrompt:
		p.Circle(cx, cy, rad*(0.8+0.3*pulse), fade(RGBA(ColText, 0.25*pulse)))
		p.Circle(cx, cy, rad*0.5, fade(RGBA(ColText, 1)))
	case NotchWorking:
		inner, outer := rad*0.62, rad
		p.Arc(cx, cy, inner, outer, 0, 2*math.Pi, fade(notchTrack()))
		if f.progress >= 0 {
			p.Arc(cx, cy, inner, outer, 0, 2*math.Pi*min(1, f.progress), fade(RGBA(ColText, 1)))
		} else {
			// Indeterminate: a quarter turn that spins.
			a0 := float32(2 * math.Pi * float64(f.now.Sub(f.born)%time.Second) / float64(time.Second))
			p.Arc(cx, cy, inner, outer, a0, a0+math.Pi/2, fade(RGBA(ColText, 1)))
		}
	case NotchReady:
		p.Circle(cx, cy, rad*0.62, fade(RGBA(ColText, 1)))
	case NotchStashDrop:
		drawGripDots(p, cx, cy, f.scale, fade(RGBA(ColText, 0.9)))
	default:
		c := ColMuted
		if f.view.Warning {
			c = ColText
		}
		p.Circle(cx, cy, rad*0.5, fade(RGBA(c, 1)))
	}
}

// drawCollapsed draws the small capsule's cue: a breathing red dot in the
// left end and the mic level across the rest while recording, a filling
// bar while transcribing, a check and "Finished" when a transcript is
// ready. The cue keeps the capsule's resting size, centred on the capsule
// as it is now: while the capsule swells under the pointer or changes
// size, it grows around the cue, which stays where it is.
func (f notchFrame) drawCollapsed(p *Painter, rc RectF, a float32) {
	cx, cy := (rc.L+rc.R)/2, (rc.T+rc.B)/2
	l := f.layout
	box := RectF{cx - l.W/2, cy - l.H/2, cx + l.W/2, cy + l.H/2}
	fade := func(c ColorF) ColorF { c.A *= a; return c }
	s := f.scale
	if f.view.StashedNotes || f.view.Mode == NotchStashed {
		gripCol := RGBA(ColMuted, 0.9)
		if f.hover || f.hoverProgress > 0 {
			gripCol = RGBA(ColText, 0.95)
		}
		if f.view.Mode == NotchRecording {
			pulse := float32(Pulse(f.now.Sub(f.born), notchPulse))
			dx := box.L + 13*s
			p.Circle(dx, cy, 4.5*s*(0.8+0.3*pulse), fade(RGBA(ColLive, 0.25*pulse)))
			p.Circle(dx, cy, 2.8*s, fade(RGBA(ColLive, 0.8+0.2*pulse)))
			drawGripDots(p, (dx+box.R-6*s)/2, cy, s, fade(gripCol))
			return
		}
		drawGripDots(p, cx, cy, s, fade(gripCol))
		return
	}
	switch f.view.Mode {
	case NotchRecording:
		// The dot sits in the cue's left end; the microphone's level
		// takes the rest of the width.
		pulse := float32(Pulse(f.now.Sub(f.born), notchPulse))
		end := box.H() / 2
		dx := box.L + end
		p.Circle(dx, cy, 4.2*s*(0.8+0.3*pulse), fade(RGBA(ColLive, 0.25*pulse)))
		p.Circle(dx, cy, 2.6*s, fade(RGBA(ColLive, 0.8+0.2*pulse)))
		bh := 2 * s
		bar := RectF{dx + 5*s, cy - bh/2, box.R - end, cy + bh/2}
		bw := bar.W()
		p.RoundRect(bar, bh/2, fade(notchTrack()))
		// Meeting audio (them) extends right-to-left underneath the personal mic bar.
		if them := min(1, f.levels[1]); them > 0 {
			p.RoundRect(RectF{bar.R - max(bh, bw*them), bar.T, bar.R, bar.B}, bh/2, fade(RGBA(ColMuted, 0.9)))
		}
		if me := min(1, f.levels[0]); me > 0 {
			p.RoundRect(RectF{bar.L, bar.T, bar.L + max(bh, bw*me), bar.B}, bh/2, fade(RGBA(ColText, 0.85)))
		}
	case NotchWorking:
		bw, bh := box.W()-14*s, 3*s
		bar := RectF{cx - bw/2, cy - bh/2, cx + bw/2, cy + bh/2}
		p.RoundRect(bar, bh/2, fade(notchTrack()))
		if f.progress > 0 {
			p.RoundRect(RectF{bar.L, bar.T, bar.L + max(bh, bw*min(1, f.progress)), bar.B}, bh/2, fade(RGBA(ColText, 1)))
		}
	case NotchReady:
		if l.Word.W() == 0 { // no room measured: the dot, as before
			p.Circle(cx, cy, 2.8*s, fade(RGBA(ColText, 1)))
			return
		}
		at := func(r RectF) RectF { return RectF{box.L + r.L, box.T + r.T, box.L + r.R, box.T + r.B} }
		DrawIcon(p, GlyphCheck, at(l.Check), 9*s, fade(RGBA(ColText, 1)))
		tf := notchTinyFont
		tf.Size *= s
		p.Text(NotchFinished, at(l.Word), tf, fade(RGBA(ColText, 0.92)))
	default:
		p.Circle(cx, cy, 2.4*s, fade(notchMuted()))
	}
}

// drawGripDots draws the 3×2 grab-handle dots centred at (cx, cy).
func drawGripDots(p *Painter, cx, cy, scale float32, c ColorF) {
	step := 4.5 * scale
	rad := max(1.1, 1.4*scale)
	for row := range 2 {
		dy := (float32(row) - 0.5) * step
		for colIdx := range 3 {
			dx := (float32(colIdx) - 1) * step
			p.Circle(cx+dx, cy+dy, rad, c)
		}
	}
}

// drawLevels draws two small bars: the mic (Me) and the meeting (Them).
func (f notchFrame) drawLevels(p *Painter, rc RectF, fade func(ColorF) ColorF) {
	bw := rc.W() / 3
	for i, lv := range f.levels {
		x := rc.L + float32(i)*2*bw
		h := max(2*f.scale, rc.H()*min(1, lv))
		col := RGBA(ColText, 0.9)
		if i == 1 {
			col = RGBA(ColMuted, 0.9)
		}
		p.RoundRect(RectF{x, rc.B - rc.H(), x + bw, rc.B}, bw/2, fade(notchTrack()))
		p.RoundRect(RectF{x, rc.B - h, x + bw, rc.B}, bw/2, fade(col))
	}
}

// LevelMeter maps a peak (0..1) to a bar height on a dB scale from -48 dB.
func LevelMeter(peak float32) float32 {
	if peak <= 0 {
		return 0
	}
	db := 20 * math.Log10(float64(peak))
	return float32(math.Max(0, math.Min(1, (db+48)/48)))
}
