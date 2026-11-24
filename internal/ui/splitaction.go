package ui

import (
	"math"
	"time"
)

// SplitState is which of the three states a SplitAction button is in.
type SplitState int

const (
	SplitIdle    SplitState = iota // full primary pill ("Start recording")
	SplitLive                      // split pill: [grey trash | white Transcribe]
	SplitConfirm                   // full red danger pill ("Discard recording")
)

const (
	splitActionW   = 185 // fixed logical width across all three states
	splitActionH   = 40  // matches Large button height
	splitLeftFrac  = 0.26
	splitAnimOmega = 34.0
	splitAnimZeta  = 0.94
	splitFadeDur   = 125 * time.Millisecond
)

// splitAnim tracks a SplitAction button's springs and cross-fade between frames.
type splitAnim struct {
	splitSp   spring1D
	dangerSp  spring1D
	lastTick  time.Time
	state     SplitState
	prevState SplitState
	changedAt time.Time
	init      bool
}

// SplitAction is a fixed-width, three-state pill button:
//   - SplitIdle:    single primary button (e.g. "Start recording")
//   - SplitLive:    split pill with a grey icon button on the left (discard)
//     and a white primary button on the right ("Transcribe")
//   - SplitConfirm: full red danger pill ("Discard recording")
//
// Because all three states share the exact same width (192 logical px),
// switching states never shifts neighbouring text or layout.
type SplitAction struct {
	ID           string // animation state key
	State        SplitState
	IdleID       string
	IdleLabel    string
	IdleGlyph    string
	OnIdle       func()
	LeftID       string
	LeftGlyph    string
	ConfirmLabel string
	OnLeft       func()
	OnConfirm    func()
	RightID      string
	RightLabel   string
	RightGlyph   string
	OnRight      func()
}

func (s SplitAction) Width(c *Ctx) float32 { return c.Px(splitActionW) }

func (s SplitAction) targets() (splitT, dangerT float64) {
	switch s.State {
	case SplitLive:
		return splitLeftFrac, 0
	case SplitConfirm:
		return 1, 1
	default:
		return 0, 0
	}
}

func (s SplitAction) Layout(c *Ctx, x, y, w float32) float32 {
	h := c.Px(splitActionH)
	if w <= 0 {
		w = s.Width(c)
	}
	r := RectF{x, y, x + w, y + h}
	if !c.Visible(r) {
		return h
	}

	splitT, dangerT := s.targets()
	splitFrac := float32(splitT)
	dangerFrac := float32(dangerT)
	prevState := s.State
	fadeP := 1.0

	if c.Buttons != nil && s.ID != "" {
		st := c.Buttons.split(s.ID)
		if !st.init {
			st.init = true
			st.splitSp.snap(splitT)
			st.dangerSp.snap(dangerT)
			st.state, st.prevState = s.State, s.State
			st.lastTick = c.Now
		} else {
			if s.State != st.state {
				st.prevState = st.state
				st.state = s.State
				st.changedAt = c.Now
			}
			st.splitSp.target = splitT
			st.dangerSp.target = dangerT
			dt := c.Now.Sub(st.lastTick).Seconds()
			if dt > 0 {
				st.lastTick = c.Now
			}
			m1 := st.splitSp.step(dt, splitAnimOmega, splitAnimZeta, 0.002, 0.05)
			m2 := st.dangerSp.step(dt, splitAnimOmega, splitAnimZeta, 0.002, 0.05)
			splitFrac = float32(max(0, min(1, st.splitSp.pos)))
			dangerFrac = float32(max(0, min(1, st.dangerSp.pos)))
			if !st.changedAt.IsZero() {
				fadeP = float64(c.Now.Sub(st.changedAt)) / float64(splitFadeDur)
				if fadeP >= 1 {
					fadeP = 1
					st.changedAt = time.Time{}
				}
			}
			if m1 || m2 || fadeP < 1 {
				c.Animating = true
				c.AnimatingFast = true
			}
		}
		prevState = st.prevState
	}

	// Hit targets match the resting layout of the active state.
	switch s.State {
	case SplitIdle:
		if s.OnIdle != nil {
			c.Click(s.IdleID, r, s.OnIdle)
		}
	case SplitLive:
		splitX := r.L + w*splitLeftFrac
		gap := c.Px(2)
		if s.OnLeft != nil {
			c.Click(s.LeftID, RectF{r.L, r.T, splitX - gap/2, r.B}, s.OnLeft)
		}
		if s.OnRight != nil {
			c.Click(s.RightID, RectF{splitX + gap/2, r.T, r.R, r.B}, s.OnRight)
		}
	case SplitConfirm:
		if s.OnConfirm != nil {
			c.Click(s.LeftID, r, s.OnConfirm)
		}
	}

	// Subtle press depression on the whole pill when pressed.
	drawR := r
	pressed := (s.State == SplitIdle && c.Pressed == s.IdleID) ||
		(s.State == SplitConfirm && c.Pressed == s.LeftID) ||
		(s.State == SplitLive && (c.Pressed == s.LeftID || c.Pressed == s.RightID))
	if pressed {
		drawR = drawR.Inset(c.Px(1))
	}

	// Segment fills, smoothly blending into ColLive red when entering SplitConfirm.
	leftBase := col(ColControl)
	if s.State == SplitLive && c.Hover == s.LeftID {
		leftBase = col(ColControlHi)
	}
	rightBase := col(ColText)
	if (s.State == SplitIdle && c.Hover == s.IdleID) || (s.State == SplitLive && c.Hover == s.RightID) {
		rightBase = col(ColPrimaryHover)
	}
	dangerCol := col(ColLive)
	if s.State == SplitConfirm && c.Hover == s.LeftID {
		dangerCol = RGBA(ColLive, 0.90)
	}
	leftFill := lerpColorF(leftBase, dangerCol, dangerFrac)
	rightFill := lerpColorF(rightBase, dangerCol, dangerFrac)
	leftInk := lerpColorF(col(ColText), col(ColInkOnDark), dangerFrac)
	rightInk := lerpColorF(col(ColInk), col(ColInkOnDark), dangerFrac)

	font := c.F(Font{Family: uiFont, Size: 14, Weight: 600})
	iconSz := c.Px(16)
	iconGap := c.Px(6)
	rad := drawR.H() / 2

	// Seam gap smoothly appears only while split is meaningfully between 0 and 1.
	seamVis := float32(Smoothstep(0.02, 0.12, float64(splitFrac)) * Smoothstep(0.02, 0.12, float64(1-splitFrac)))
	halfGap := c.Px(1.5) * seamVis
	splitX := drawR.L + drawR.W()*splitFrac

	// Helper to draw an icon + label centred horizontally inside box.
	drawCentered := func(cv Canvas, box RectF, glyph, text string, ink ColorF) {
		if ink.A <= 0.01 {
			return
		}
		if text == "" {
			if glyph != "" {
				DrawIcon(cv, glyph, box, iconSz, ink)
			}
			return
		}
		tw := c.M.TextWidth(text, font)
		totalW := tw
		if glyph != "" {
			totalW += iconSz + iconGap
		}
		sx := float32(math.Round(float64(box.L + (box.W()-totalW)/2)))
		if glyph != "" {
			DrawIcon(cv, glyph, RectF{sx, box.T, sx + iconSz, box.B}, iconSz, ink)
			sx += iconSz + iconGap
		}
		cv.Text(text, RectF{sx, box.T, box.R, box.B}, font, ink)
	}

	c.Draw(func(cv Canvas) {
		scaleA := func(k ColorF, a float32) ColorF {
			k.A *= a
			return k
		}
		outA := float32(max(0, 1-fadeP/0.45))
		inA := float32(Smoothstep(0.12, 0.85, fadeP))

		// Left segment: grey trash icon in SplitLive, expanding to full red
		// "Discard recording" pill in SplitConfirm.
		if leftR := splitX - halfGap; leftR > drawR.L+0.5 {
			cv.PushClip(RectF{drawR.L, drawR.T, leftR, drawR.B})
			cv.RoundRect(drawR, rad, leftFill)

			// Smoothly transition from icon-only centred in the left slice to
			// icon + "Discard recording" centred in the full pill.
			confirmMix := float32(Smoothstep(0.35, 0.92, float64(splitFrac)))
			if confirmMix < 0.99 {
				// Icon-only centred in the left segment, fading/sliding as confirm text appears.
				leftBox := RectF{drawR.L, drawR.T, max(drawR.L+c.Px(splitActionW*splitLeftFrac), leftR), drawR.B}
				if confirmMix <= 0.01 {
					DrawIcon(cv, s.LeftGlyph, leftBox, iconSz, leftInk)
				} else {
					// Slide the trash icon smoothly from leftBox centre toward the
					// final icon position inside the full "Discard recording" layout.
					tw := c.M.TextWidth(s.ConfirmLabel, font)
					finalIconX := drawR.L + (drawR.W()-(iconSz+iconGap+tw))/2
					startIconX := leftBox.L + (leftBox.W()-iconSz)/2
					ix := startIconX + (finalIconX-startIconX)*confirmMix
					DrawIcon(cv, s.LeftGlyph, RectF{ix, drawR.T, ix + iconSz, drawR.B}, iconSz, leftInk)
					cv.Text(s.ConfirmLabel, RectF{ix + iconSz + iconGap, drawR.T, drawR.R, drawR.B}, font, scaleA(leftInk, confirmMix))
				}
			} else {
				drawCentered(cv, drawR, s.LeftGlyph, s.ConfirmLabel, leftInk)
			}
			cv.PopClip()
		}

		// Right segment: full "Start recording" in SplitIdle, contracting to
		// "Transcribe" on the right in SplitLive, pushed off right in SplitConfirm.
		if rightL := splitX + halfGap; rightL < drawR.R-0.5 {
			cv.PushClip(RectF{rightL, drawR.T, drawR.R, drawR.B})
			cv.RoundRect(drawR, rad, rightFill)
			rightBox := RectF{rightL, drawR.T, drawR.R, drawR.B}
			liveRightBox := RectF{drawR.L + drawR.W()*splitLeftFrac + c.Px(1.5), drawR.T, drawR.R, drawR.B}
			switch {
			case s.State == SplitConfirm:
				// Fade out "Transcribe" quickly in place (anchored to liveRightBox)
				// so it never squishes or clips awkwardly as the split sweeps right.
				if fadeP < 0.32 {
					a := float32(max(0, 1-fadeP/0.32))
					drawCentered(cv, liveRightBox, s.RightGlyph, s.RightLabel, scaleA(rightInk, a))
				}
			case s.State == SplitLive && prevState == SplitConfirm && fadeP < 1:
				a := float32(Smoothstep(0.30, 0.90, fadeP))
				drawCentered(cv, liveRightBox, s.RightGlyph, s.RightLabel, scaleA(rightInk, a))
			case s.State == SplitIdle && prevState == SplitLive && fadeP < 1:
				drawCentered(cv, rightBox, s.RightGlyph, s.RightLabel, scaleA(rightInk, outA))
				drawCentered(cv, rightBox, s.IdleGlyph, s.IdleLabel, scaleA(rightInk, inA))
			case s.State == SplitLive && prevState == SplitIdle && fadeP < 1:
				drawCentered(cv, rightBox, s.IdleGlyph, s.IdleLabel, scaleA(rightInk, outA))
				drawCentered(cv, rightBox, s.RightGlyph, s.RightLabel, scaleA(rightInk, inA))
			case s.State == SplitIdle:
				drawCentered(cv, rightBox, s.IdleGlyph, s.IdleLabel, rightInk)
			default:
				drawCentered(cv, rightBox, s.RightGlyph, s.RightLabel, rightInk)
			}
			cv.PopClip()
		}
	})

	return h
}
