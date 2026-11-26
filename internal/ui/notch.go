package ui

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// The notch is flit's one surface on screen: a dark capsule hanging from
// the top edge of the main screen, centred, that appears only when it has
// something to say and leaves again. It never takes the focus.
//
// This file is the notch's portable part: what it shows (NotchView), where
// things go (Layout), what a click hits, and the animation curves. The
// window and drawing are in notch_windows.go.

// NotchMode is what the notch is showing.
type NotchMode int

const (
	NotchHidden    NotchMode = iota
	NotchPrompt              // a meeting was detected (or about to start)
	NotchRecording           // recording, with a live dot, the time and levels
	NotchWorking             // transcribing, with progress
	NotchReady               // the transcript is ready
	NotchMessage             // a short note (an error, a download)
	NotchStashDrop           // dragging the floating notes window over the top drop zone
	NotchStashed             // floating notes are stashed in the notch (idle)
)

// NotchButton is a clickable pill (or circular icon button when Label is empty).
type NotchButton struct {
	ID      string
	Label   string
	Glyph   string
	Primary bool
}

// NotchView is what to show.
type NotchView struct {
	Mode     NotchMode
	Title    string
	Detail   string    // muted text after the title ("45%", a meeting name)
	Since    time.Time // Recording: the clock counts from here
	Progress float64   // Working: 0..1; negative = indeterminate
	Warning  bool      // Message: an error
	Buttons  []NotchButton
	// TitleID, when set, makes the title (and detail) a link: a click on
	// it reports this ID like a button's (Ready: open the transcript).
	TitleID string
	// AutoHide hides the notch after this long (0 = stays).
	AutoHide time.Duration
	// Collapsible views shrink to a small capsule when left alone (see
	// Collapser). HoverDelay is how long the pointer must rest on the
	// collapsed capsule before it expands (0 = expand immediately).
	// HideCollapsed hides the collapsed capsule completely until the pointer
	// rests at the top-centre edge of the screen for HoverDelay.
	Collapsible   bool
	HoverDelay    time.Duration
	HideCollapsed bool
	// StashedNotes marks that the floating notes window is stashed in the
	// notch: the collapsed capsule stays taller with a grab handle and
	// accepts clicks/drags to unstash instead of letting clicks through.
	StashedNotes bool
	// StartCollapsed opens or switches into the view directly in its
	// collapsed state (e.g. right after stashing notes during a recording).
	StartCollapsed bool
	// Highlight slightly lightens the notch background (e.g. when a dragged
	// notes window is close enough to be stashed).
	Highlight bool
	// CountFrom/CountFor fill the capsule from left to right over CountFor
	// from CountFrom (a countdown, e.g. to recording automatically).
	CountFrom time.Time
	CountFor  time.Duration
}

// CountdownAt is how far the countdown has come at now (0..1), or -1.
func (v NotchView) CountdownAt(now time.Time) float64 {
	if v.CountFor <= 0 || v.CountFrom.IsZero() {
		return -1
	}
	return math.Max(0, math.Min(1, float64(now.Sub(v.CountFrom))/float64(v.CountFor)))
}

// Notch geometry in logical pixels (scaled by the screen's DPI).
const (
	notchHeight         = 34
	notchPadX           = 14
	notchIcon           = 14 // the dot / ring
	notchGap            = 9
	notchButtonH        = 24
	notchButtonPad      = 12
	notchButtonGap      = 6
	notchLevelW         = 16
	notchMaxW           = 560
	notchShadow         = 14 // room for the shadow around the capsule
	notchFlareMax       = 10 // max concave flare radius at the top edge
	notchHighlightGrowX = 10 // extra horizontal padding on each side when highlighted
	notchHighlightGrowH = 6  // extra height when highlighted
)

var (
	notchTitleFont  = Font{Family: "Segoe UI", Size: 12.5, Weight: 600}
	notchDetailFont = Font{Family: "Segoe UI", Size: 12, Weight: 400}
	notchButtonFont = Font{Family: "Segoe UI", Size: 12, Weight: 600, Center: true}
)

// NotchLayout is where everything goes, in device pixels, with the
// capsule's top-left corner at (0, 0).
type NotchLayout struct {
	W, H       float32
	Icon       RectF
	Title      RectF
	TitleText  string // fitted title text (truncated with "…" if needed)
	Detail     RectF
	DetailText string // fitted detail text (truncated with "…" if needed)
	Levels     RectF  // recording level bars; empty if none
	Buttons    []RectF
	IDs        []string
	Primary    []bool
	Labels     []string
	Glyphs     []string
	// Link is the title and detail when they are a link (TitleID).
	Link   RectF
	LinkID string
	// Finished: the collapsed Ready capsule's check and word.
	Check, Word RectF
}

// Measurer measures text width in device pixels.
type Measurer func(s string, f Font) float32

// DetailText is the muted text shown, the recording clock included.
func (v NotchView) DetailText(now time.Time) string {
	if v.Mode == NotchRecording && !v.Since.IsZero() {
		return Clock(now.Sub(v.Since))
	}
	if v.Mode == NotchWorking && v.Detail == "" && v.Progress >= 0 {
		return fmt.Sprintf("%d%%", int(v.Progress*100+0.5))
	}
	return v.Detail
}

// Layout places the view's parts at scale s (1 = 96 DPI).
func Layout(v NotchView, s float32, now time.Time, measure Measurer) NotchLayout {
	if v.Mode == NotchStashed {
		return StashedLayout(false, s)
	}
	px := func(f float32) float32 { return float32(math.Round(float64(f * s))) }
	scaled := func(f Font) Font { f.Size *= s; return f }
	growX, growH := float32(0), float32(0)
	if v.Mode == NotchStashDrop && v.Highlight {
		growX, growH = px(notchHighlightGrowX), px(notchHighlightGrowH)
	}
	h := px(notchHeight) + growH
	x := px(notchPadX) + growX
	l := NotchLayout{H: h}
	l.Icon = RectF{x, (h - px(notchIcon)) / 2, x + px(notchIcon), (h + px(notchIcon)) / 2}
	x = l.Icon.R + px(notchGap)

	titleFont := scaled(notchTitleFont)
	detailFont := scaled(notchDetailFont)
	btnFont := scaled(notchButtonFont)
	bh := px(notchButtonH)

	// Measure fixed-width right-side items (levels, buttons, trailing margin)
	// first so title/detail can be fitted to the remaining budget inside
	// notchMaxW and never push buttons off the right edge of the capsule.
	var rightW float32
	if v.Mode == NotchRecording {
		rightW += px(notchGap) + px(notchLevelW)
	}
	btnWidths := make([]float32, len(v.Buttons))
	for i, b := range v.Buttons {
		if i == 0 {
			rightW += px(notchGap + 4)
		} else {
			rightW += px(notchButtonGap)
		}
		w := bh
		if b.Label != "" || b.Glyph == "" {
			w = measure(b.Label, btnFont) + 2*px(notchButtonPad)
		}
		btnWidths[i] = w
		rightW += w
	}
	if len(v.Buttons) > 0 {
		rightW += (h - bh) / 2 // the same margin right of a button as above it
	} else {
		rightW += px(notchPadX+2) + growX
	}

	titleText := v.Title
	detailText := v.DetailText(now)
	var tw, dw float32
	if titleText != "" {
		tw = measure(titleText, titleFont) + 1
	}
	if detailText != "" {
		dw = measure(detailText, detailFont)
		if v.Mode == NotchRecording {
			// Room for the widest the clock gets, so it doesn't jiggle.
			dw = max(dw, measure("00:00", detailFont))
		}
		dw += 1
	}
	textGap := float32(0)
	if titleText != "" && detailText != "" {
		textGap = px(6)
	}
	textAvail := max(float32(0), px(notchMaxW)-x-rightW)
	if tw+textGap+dw > textAvail {
		if titleText != "" && detailText != "" {
			// Preserve short status details ("starts in 1 min", "00:00") while
			// guaranteeing the title keeps at least a readable slice.
			minTitle := min(px(80), max(float32(0), (textAvail-textGap)/2))
			maxDetail := max(float32(0), textAvail-textGap-minTitle)
			if dw > maxDetail {
				var fitW float32
				detailText, fitW = fitNotchText(detailText, detailFont, max(0, maxDetail-1), measure)
				dw = fitW + 1
			}
			maxTitle := max(float32(0), textAvail-textGap-dw)
			var fitW float32
			titleText, fitW = fitNotchText(titleText, titleFont, max(0, maxTitle-1), measure)
			tw = fitW + 1
		} else if titleText != "" {
			var fitW float32
			titleText, fitW = fitNotchText(titleText, titleFont, max(0, textAvail-1), measure)
			tw = fitW + 1
		} else if detailText != "" {
			var fitW float32
			detailText, fitW = fitNotchText(detailText, detailFont, max(0, textAvail-1), measure)
			dw = fitW + 1
		}
	}

	if titleText != "" {
		l.Title = RectF{x, 0, x + tw, h}
		l.TitleText = titleText
		x = l.Title.R
		if detailText != "" {
			x += textGap
		}
	}
	if detailText != "" {
		l.Detail = RectF{x, 0, x + dw, h}
		l.DetailText = detailText
		x = l.Detail.R
	}
	if v.Mode == NotchRecording {
		x += px(notchGap)
		l.Levels = RectF{x, (h - px(12)) / 2, x + px(notchLevelW), (h + px(12)) / 2}
		x = l.Levels.R
	}
	for i, b := range v.Buttons {
		if i == 0 {
			x += px(notchGap + 4)
		} else {
			x += px(notchButtonGap)
		}
		w := btnWidths[i]
		l.Buttons = append(l.Buttons, RectF{x, (h - bh) / 2, x + w, (h + bh) / 2})
		l.IDs = append(l.IDs, b.ID)
		l.Primary = append(l.Primary, b.Primary)
		l.Labels = append(l.Labels, b.Label)
		l.Glyphs = append(l.Glyphs, b.Glyph)
		x += w
	}
	if v.TitleID != "" && titleText != "" {
		l.Link, l.LinkID = RectF{l.Title.L, 0, max(l.Title.R, l.Detail.R), h}, v.TitleID
	}
	if len(l.Buttons) > 0 {
		x += (h - bh) / 2 // the same margin right of a button as above it
	} else {
		x += px(notchPadX+2) + growX
	}
	l.W = min(x, px(notchMaxW))
	return l
}

// fitNotchText trims s with a trailing ellipsis so its measured width fits
// within maxW pixels.
func fitNotchText(s string, f Font, maxW float32, measure Measurer) (string, float32) {
	if s == "" || maxW <= 0 {
		return "", 0
	}
	if w := measure(s, f); w <= maxW {
		return s, w
	}
	const ell = "…"
	ew := measure(ell, f)
	if ew > maxW {
		return "", 0
	}
	r := []rune(s)
	lo, hi := 0, len(r)
	best, bestW := ell, ew
	for lo < hi {
		mid := (lo + hi + 1) / 2
		cand := strings.TrimRight(string(r[:mid]), " \t-–—·:,") + ell
		if w := measure(cand, f); w <= maxW {
			best, bestW = cand, w
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return best, bestW
}

// HitButton returns the ID of the button (or the title link) at (x, y),
// "" if none.
func (l NotchLayout) HitButton(x, y float32) string {
	for i, r := range l.Buttons {
		if r.Contains(x, y) {
			return l.IDs[i]
		}
	}
	if l.LinkID != "" && l.Link.Contains(x, y) {
		return l.LinkID
	}
	return ""
}

// Easing and timing of the notch's movements: drop-in and shape morphing use
// analytical 2nd-order springs (spring1D, inspired by Dynamic Island's snappy
// liquid micro-overshoot with continuous velocity inheritance on interruptions);
// leaving tucks away briskly with a cubic ease-in.
const (
	notchSlideIn  = 250 * time.Millisecond
	notchSlideOut = 155 * time.Millisecond
	notchResize   = 185 * time.Millisecond
	notchPulse    = 1400 * time.Millisecond // the live dot's breathing period

	notchBounceIn     = 1.5 // EaseOutBack's s for dropping in (kept for callers/tests)
	notchBounceResize = 1.0

	// Spring tuning (omega in rad/s, zeta = damping ratio):
	// zeta 0.75..0.78 gives a ~2.5% liquid micro-overshoot; collapsing uses a
	// slightly tighter zeta so it tucks cleanly without pinching too small.
	notchRiseOmega    = 23.0
	notchRiseZeta     = 0.75
	notchExpandWOmega = 23.0
	notchExpandWZeta  = 0.78
	notchExpandHOmega = 27.0
	notchExpandHZeta  = 0.76
	notchShrinkWOmega = 25.0
	notchShrinkWZeta  = 0.88
	notchShrinkHOmega = 27.0
	notchShrinkHZeta  = 0.86
)

// spring1D is an analytical 2nd-order spring (underdamped when zeta < 1,
// critically damped when zeta >= 1). Unconditionally stable at any refresh
// rate and preserves velocity across mid-flight target changes.
type spring1D struct {
	pos, vel, target float64
}

// snap jumps immediately to v with zero velocity.
func (s *spring1D) snap(v float64) { s.pos, s.vel, s.target = v, 0, v }

// step advances the spring by dt seconds and reports whether it is still moving.
func (s *spring1D) step(dt, omega, zeta, epsPos, epsVel float64) bool {
	if dt <= 0 {
		return math.Abs(s.pos-s.target) >= epsPos || math.Abs(s.vel) >= epsVel
	}
	if dt > 0.1 {
		dt = 0.1
	}
	x0 := s.pos - s.target
	v0 := s.vel
	var x, v float64
	if zeta < 1 {
		wd := omega * math.Sqrt(1-zeta*zeta)
		e := math.Exp(-zeta * omega * dt)
		c1 := x0
		c2 := (v0 + zeta*omega*x0) / wd
		cos, sin := math.Cos(wd*dt), math.Sin(wd*dt)
		x = (c1*cos + c2*sin) * e
		v = ((-zeta*omega*c1+wd*c2)*cos + (-zeta*omega*c2-wd*c1)*sin) * e
	} else {
		e := math.Exp(-omega * dt)
		c2 := v0 + omega*x0
		x = (x0 + c2*dt) * e
		v = (c2 - omega*(x0+c2*dt)) * e
	}
	s.pos = s.target + x
	s.vel = v
	if math.Abs(x) < epsPos && math.Abs(v) < epsVel {
		s.pos = s.target
		s.vel = 0
		return false
	}
	return true
}

// EaseOutBack eases out past the end and back (an overshoot of about 10%
// at s = 1.70158, less for smaller s), over t in [0, 1].
func EaseOutBack(t, s float64) float64 {
	t = math.Max(0, math.Min(1, t))
	u := t - 1
	return 1 + (s+1)*u*u*u + s*u*u
}

// Smoothstep is 0 below a, 1 above b, and an S-curve between.
func Smoothstep(a, b, t float64) float64 {
	t = math.Max(0, math.Min(1, (t-a)/(b-a)))
	return t * t * (3 - 2*t)
}

// EaseOut is a cubic ease-out over t in [0, 1].
func EaseOut(t float64) float64 {
	t = math.Max(0, math.Min(1, t))
	return 1 - math.Pow(1-t, 3)
}

// EaseIn is a cubic ease-in on 0..1: slow start, fast end (leaving).
func EaseIn(t float64) float64 {
	t = math.Max(0, math.Min(1, t))
	return t * t * t
}

// Pulse is a smooth 0..1..0 wave with the given period.
func Pulse(elapsed, period time.Duration) float64 {
	return 0.5 - 0.5*math.Cos(2*math.Pi*float64(elapsed%period)/float64(period))
}

// Lerp interpolates from a to b by t.
func Lerp(a, b, t float32) float32 { return a + (b-a)*t }

// Clock formats a duration as m:ss, or h:mm:ss from an hour on.
func Clock(d time.Duration) string {
	s := int(d / time.Second)
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// Collapsing: a view marked Collapsible (recording, transcribing, a ready
// transcript) shrinks to a small capsule after CollapseAfter without
// interaction; collapsed, it lets clicks through to what lies behind, and
// the pointer resting on it for ExpandHover expands it again.
const (
	CollapseAfter = 3 * time.Second
	ExpandHover   = time.Second

	notchCollapsedW = 46 // logical
	notchCollapsedH = 11
	notchStashedW   = 56 // logical (idle stashed handle)
	notchStashedRW  = 74 // logical (recording dot + stashed handle)
	notchStashedH   = 22 // logical (easy to grab)
)

// CollapsePolicy controls how a collapsible notch reacts to idle time and hover.
type CollapsePolicy struct {
	Collapsible bool
	// HoverDelay is how long the pointer must rest on the collapsed capsule
	// before expanding (0 = expand immediately on hover).
	HoverDelay time.Duration
}

// Collapser decides when the notch collapses and expands.
type Collapser struct {
	Collapsed  bool
	lastActive time.Time
	hoverStart time.Time
}

// Touch counts as interaction: the notch stays expanded another
// CollapseAfter.
func (c *Collapser) Touch(now time.Time) { c.lastActive = now }

// Expand opens the notch (new information to show) and touches it.
func (c *Collapser) Expand(now time.Time) {
	c.Collapsed, c.hoverStart = false, time.Time{}
	c.Touch(now)
}

// Collapse puts the notch straight into its compact state.
func (c *Collapser) Collapse() {
	c.Collapsed, c.hoverStart = true, time.Time{}
}

// Step advances the state with the default ExpandHover delay.
func (c *Collapser) Step(now time.Time, collapsible, hover, over bool) float64 {
	return c.StepWithPolicy(now, CollapsePolicy{Collapsible: collapsible, HoverDelay: ExpandHover}, hover, over)
}

// StepWithPolicy advances the state according to policy. hover: the pointer is
// over the expanded notch; over: the pointer rests on the collapsed capsule.
// It returns how far the hover to expand has come (0..1), for the capsule to
// show it. When policy.HoverDelay <= 0, hovering over the collapsed capsule
// expands it immediately.
func (c *Collapser) StepWithPolicy(now time.Time, policy CollapsePolicy, hover, over bool) float64 {
	if !policy.Collapsible {
		c.Collapsed, c.hoverStart = false, time.Time{}
		return 0
	}
	if !c.Collapsed {
		if hover {
			c.lastActive = now
		} else if now.Sub(c.lastActive) >= CollapseAfter {
			c.Collapsed, c.hoverStart = true, time.Time{}
		}
		return 0
	}
	if !over {
		c.hoverStart = time.Time{}
		return 0
	}
	if policy.HoverDelay <= 0 {
		c.Expand(now)
		return 1
	}
	if c.hoverStart.IsZero() {
		c.hoverStart = now
	}
	p := float64(now.Sub(c.hoverStart)) / float64(policy.HoverDelay)
	if p >= 1 {
		c.Expand(now)
		return 1
	}
	return p
}

// CollapsedLayout is the small capsule's size.
func CollapsedLayout(scale float32) NotchLayout {
	px := func(f float32) float32 { return float32(math.Round(float64(f * scale))) }
	return NotchLayout{W: px(notchCollapsedW), H: px(notchCollapsedH)}
}

// StashedLayout is the grab-handle capsule's size when notes are stashed.
func StashedLayout(recording bool, scale float32) NotchLayout {
	px := func(f float32) float32 { return float32(math.Round(float64(f * scale))) }
	w := float32(notchStashedW)
	if recording {
		w = notchStashedRW
	}
	return NotchLayout{W: px(w), H: px(notchStashedH)}
}

// notchTinyFont is the collapsed capsule's word: as small as still reads.
var notchTinyFont = Font{Family: "Segoe UI", Size: 8, Weight: 600}

// CollapsedLayoutFor is the small capsule for v: stashed notes keep a taller
// grab-handle capsule; a finished transcript widens it just enough for a check
// mark and "Finished".
func CollapsedLayoutFor(v NotchView, scale float32, measure Measurer) NotchLayout {
	if v.StashedNotes || v.Mode == NotchStashed {
		return StashedLayout(v.Mode == NotchRecording, scale)
	}
	l := CollapsedLayout(scale)
	if v.HideCollapsed {
		l.H = 0
		return l
	}
	if v.Mode != NotchReady {
		return l
	}
	px := func(f float32) float32 { return float32(math.Round(float64(f * scale))) }
	f := notchTinyFont
	f.Size *= scale
	x := px(9)
	l.Check = RectF{x, 0, x + px(8), l.H}
	x = l.Check.R + px(3)
	l.Word = RectF{x, 0, x + measure(NotchFinished, f) + 1, l.H}
	l.W = max(l.W, l.Word.R+px(9))
	return l
}

// NotchFinished is the collapsed Ready capsule's word.
const NotchFinished = "Finished"
