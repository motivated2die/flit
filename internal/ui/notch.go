package ui

import (
	"fmt"
	"math"
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

// NotchButton is a clickable pill.
type NotchButton struct {
	ID      string
	Label   string
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
	Collapsible bool
	HoverDelay  time.Duration
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
	notchHeight    = 34
	notchPadX      = 14
	notchIcon      = 14 // the dot / ring
	notchGap       = 9
	notchButtonH   = 24
	notchButtonPad = 12
	notchButtonGap = 6
	notchLevelW    = 16
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
	W, H    float32
	Icon    RectF
	Title   RectF
	Detail  RectF
	Levels  RectF // recording level bars; empty if none
	Buttons []RectF
	IDs     []string
	Primary []bool
	Labels  []string
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

	if v.Title != "" {
		tw := measure(v.Title, scaled(notchTitleFont))
		l.Title = RectF{x, 0, x + tw + 1, h}
		x = l.Title.R + px(6)
	}
	if d := v.DetailText(now); d != "" {
		// Room for the widest the clock gets, so it doesn't jiggle.
		width := measure(d, scaled(notchDetailFont))
		if v.Mode == NotchRecording {
			width = max(width, measure("00:00", scaled(notchDetailFont)))
		}
		l.Detail = RectF{x, 0, x + width + 1, h}
		x = l.Detail.R
	}
	if v.Mode == NotchRecording {
		x += px(notchGap)
		l.Levels = RectF{x, (h - px(12)) / 2, x + px(notchLevelW), (h + px(12)) / 2}
		x = l.Levels.R
	}
	bh := px(notchButtonH)
	for i, b := range v.Buttons {
		if i == 0 {
			x += px(notchGap + 4)
		} else {
			x += px(notchButtonGap)
		}
		w := measure(b.Label, scaled(notchButtonFont)) + 2*px(notchButtonPad)
		l.Buttons = append(l.Buttons, RectF{x, (h - bh) / 2, x + w, (h + bh) / 2})
		l.IDs = append(l.IDs, b.ID)
		l.Primary = append(l.Primary, b.Primary)
		l.Labels = append(l.Labels, b.Label)
		x += w
	}
	if v.TitleID != "" && v.Title != "" {
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

// Easing and timing of the notch's movements: it drops in and changes
// shape with a little overshoot that settles (playful, not wobbly), and
// leaves with a quick, smooth ease-in.
const (
	notchSlideIn  = 250 * time.Millisecond
	notchSlideOut = 200 * time.Millisecond
	notchResize   = 215 * time.Millisecond
	notchPulse    = 1400 * time.Millisecond // the live dot's breathing period

	notchBounceIn     = 1.5 // EaseOutBack's s for dropping in: about 8% past its place
	notchBounceResize = 1.0 // and for changing width and height: about 4%
)

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
