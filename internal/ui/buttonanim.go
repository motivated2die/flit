package ui

import (
	"time"
)

// Snappy, restrained button morphing parameters: crisp width spring (~140 ms,
// nearly critically damped with zero distracting bounce) paired with a fast
// content/colour cross-fade.
const (
	btnSpringOmega = 34.0
	btnSpringZeta  = 0.94
	btnFadeDur     = 125 * time.Millisecond
)

// ButtonAnims keeps per-button morph state between frames on a live host
// (nil in static/offscreen renders so tests stay deterministic). UI thread only.
type ButtonAnims struct {
	items      map[string]*btnAnim
	seen       map[string]bool
	splits     map[string]*splitAnim
	seenSplits map[string]bool
}

type btnAnim struct {
	wSp          spring1D
	lastTick     time.Time
	scale        float32
	visInit      bool
	label        string
	prevLabel    string
	glyph        string
	prevGlyph    string
	iconOnly     bool
	prevIconOnly bool
	fill         ColorF
	prevFill     ColorF
	ink          ColorF
	prevInk      ColorF
	changedAt    time.Time
}

type btnVisual struct {
	p            float64 // 0..1 morph progress (1 = settled)
	prevLabel    string
	prevGlyph    string
	prevIconOnly bool
	fill         ColorF
	ink          ColorF
	animating    bool
}

func (a *ButtonAnims) get(key string) *btnAnim {
	if a.items == nil {
		a.items = map[string]*btnAnim{}
	}
	if a.seen == nil {
		a.seen = map[string]bool{}
	}
	a.seen[key] = true
	st := a.items[key]
	if st == nil {
		st = &btnAnim{}
		a.items[key] = st
	}
	return st
}

// width steps the button's width spring toward targetW and returns the current
// width and whether the spring is still moving.
func (a *ButtonAnims) width(key string, targetW, scale float32, now time.Time) (float32, bool) {
	st := a.get(key)
	if st.lastTick.IsZero() || st.scale != scale {
		st.wSp.snap(float64(targetW))
		st.scale = scale
		st.lastTick = now
		return targetW, false
	}
	st.wSp.target = float64(targetW)
	dt := now.Sub(st.lastTick).Seconds()
	if dt > 0 {
		st.lastTick = now
	}
	moving := st.wSp.step(dt, btnSpringOmega, btnSpringZeta, 0.4, 15)
	return float32(st.wSp.pos), moving
}

// visual tracks label, glyph, icon-only mode, and state-change fill/ink blends.
// Hover colour changes stay instant; fill/ink only cross-fade when the button's
// label, glyph, or icon-only state changes.
func (a *ButtonAnims) visual(key, label, glyph string, fill, ink ColorF, iconOnly bool, now time.Time) btnVisual {
	st := a.get(key)
	if !st.visInit {
		st.visInit = true
		st.label, st.glyph, st.iconOnly = label, glyph, iconOnly
		st.fill, st.ink = fill, ink
		return btnVisual{p: 1, fill: fill, ink: ink}
	}
	if label != st.label || glyph != st.glyph || iconOnly != st.iconOnly {
		// Capture current blended colours if interrupting an earlier morph.
		curFill, curInk := st.fill, st.ink
		if !st.changedAt.IsZero() {
			if prevP := float64(now.Sub(st.changedAt)) / float64(btnFadeDur); prevP < 1 {
				t := float32(EaseOut(prevP))
				curFill = lerpColorF(st.prevFill, st.fill, t)
				curInk = lerpColorF(st.prevInk, st.ink, t)
			}
		}
		st.prevLabel, st.prevGlyph, st.prevIconOnly = st.label, st.glyph, st.iconOnly
		st.prevFill, st.prevInk = curFill, curInk
		st.label, st.glyph, st.iconOnly = label, glyph, iconOnly
		st.changedAt = now
	}
	st.fill, st.ink = fill, ink

	if st.changedAt.IsZero() {
		return btnVisual{p: 1, fill: fill, ink: ink}
	}
	p := float64(now.Sub(st.changedAt)) / float64(btnFadeDur)
	if p >= 1 {
		st.changedAt = time.Time{}
		st.prevLabel, st.prevGlyph = "", ""
		return btnVisual{p: 1, fill: fill, ink: ink}
	}
	t := float32(EaseOut(p))
	return btnVisual{
		p:            p,
		prevLabel:    st.prevLabel,
		prevGlyph:    st.prevGlyph,
		prevIconOnly: st.prevIconOnly,
		fill:         lerpColorF(st.prevFill, fill, t),
		ink:          lerpColorF(st.prevInk, ink, t),
		animating:    true,
	}
}

// split returns the persistent split-button animation state for key.
func (a *ButtonAnims) split(key string) *splitAnim {
	if a.splits == nil {
		a.splits = map[string]*splitAnim{}
	}
	if a.seenSplits == nil {
		a.seenSplits = map[string]bool{}
	}
	a.seenSplits[key] = true
	st := a.splits[key]
	if st == nil {
		st = &splitAnim{}
		a.splits[key] = st
	}
	return st
}

// prune drops morph state for buttons not present in the last layout pass so
// returning to a page later never replays an old transition.
func (a *ButtonAnims) prune() {
	for k := range a.items {
		if !a.seen[k] {
			delete(a.items, k)
		}
	}
	clear(a.seen)
	for k := range a.splits {
		if !a.seenSplits[k] {
			delete(a.splits, k)
		}
	}
	clear(a.seenSplits)
}

func lerpColorF(a, b ColorF, t float32) ColorF {
	return ColorF{
		R: a.R + (b.R-a.R)*t,
		G: a.G + (b.G-a.G)*t,
		B: a.B + (b.B-a.B)*t,
		A: a.A + (b.A-a.A)*t,
	}
}
