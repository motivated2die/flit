package ui

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// Widgets for the pages built around recordings: a page header with its
// main action, recording rows, the audio player bar, round icon buttons
// and transcript blocks.

// PageHeader is a page's title with its main actions on the right,
// centred on the title's line.
type PageHeader struct {
	Title   string
	Actions []Node // Sized nodes, laid out right to left
}

func (p PageHeader) Layout(c *Ctx, x, y, w float32) float32 {
	h := c.Px(40)
	right := x + w
	for i := len(p.Actions) - 1; i >= 0; i-- {
		a := p.Actions[i]
		if a == nil {
			continue
		}
		aw := w / 2
		if sz, ok := a.(Sized); ok {
			aw = sz.Width(c)
		}
		right -= aw
		dy := float32(0)
		if b, ok := a.(Button); ok && !b.Large {
			dy = c.Px(4) // a regular button on the title's centre line
		}
		ah := a.Layout(c, right, y+dy, aw) + dy
		if ah > h {
			h = ah
		}
		right -= c.Px(8)
	}
	title := RectF{x, y, right, y + h}
	c.Draw(func(cv Canvas) { cv.Text(p.Title, title, c.F(fontTitle), col(ColText)) })
	return h
}

// CaptionRow is a section caption with a link (or other small node) on
// the right, both on one centre line.
type CaptionRow struct {
	Text  string
	Right Node // Sized
}

func (r CaptionRow) Layout(c *Ctx, x, y, w float32) float32 {
	h := c.Px(28)
	if r.Right != nil {
		rw := w / 3
		if sz, ok := r.Right.(Sized); ok {
			rw = sz.Width(c)
		}
		h = max(h, r.Right.Layout(c, x+w-rw, y, rw))
	}
	t := RectF{x + c.Px(2), y, x + w, y + h}
	c.Draw(func(cv Canvas) { cv.Text(strings.ToUpper(r.Text), t, c.F(fontCaption), col(ColFaint)) })
	return h
}

// RecordingRow is a recording in a list: a document icon (or a live red
// dot), its title, a line of facts (when, how long, words, speakers) and
// optionally the beginning of its transcript. On the right, on the title's
// line: its state and a chevron; or Actions, round buttons centred on the
// row. Every line ends at the same right edge, so the row reads as one
// column. NoIcon leaves the icon out (the tray panel).
type RecordingRow struct {
	ID       string
	Glyph    string // icon name (defaults to GlyphArticle)
	Live     bool
	Spinning bool // animated loading spinner instead of a static icon
	Title    string
	Facts    string
	Snippet  string
	Trailing string
	NoIcon   bool
	Actions  []Node // Sized, the last one rightmost; instead of the chevron
	OnClick  func()
}

// Row geometry (logical pixels). Recording and meeting rows share an icon
// column and a text column: a meeting's times start where a recording's
// title does, so lists of both line up.
const (
	rowPadX    = 12
	rowPadY    = 8
	rowLineH   = 20 // title line
	rowSmallH  = 18 // facts and snippet lines
	rowIcon    = 18 // the icon's size
	rowIconGap = 12 // between the icon and the text
	rowTimeW   = 40 // a meeting's times
	rowTimeGap = 12 // between the times and the meeting's title
)

// rowText is where a row's text starts: after the icon, or at the padding
// without one.
func rowText(c *Ctx, x float32, noIcon bool) float32 {
	if noIcon {
		return x + c.Px(rowPadX)
	}
	return x + c.Px(rowPadX+rowIcon+rowIconGap)
}

// rowActions lays out Sized nodes right to left, ending at right, centred
// on the row (y, h), and returns where they begin.
func rowActions(c *Ctx, actions []Node, right, y, h float32) float32 {
	for i := len(actions) - 1; i >= 0; i-- {
		a := actions[i]
		aw := c.Px(roundButtonSize)
		if sz, ok := a.(Sized); ok {
			aw = sz.Width(c)
		}
		ah := c.Px(roundButtonSize)
		if b, ok := a.(Button); ok {
			ah = c.Px(32)
			if b.Large {
				ah = c.Px(40)
			}
		}
		a.Layout(c, right-aw, y+(h-ah)/2, aw)
		right -= aw + c.Px(8)
	}
	if len(actions) > 0 {
		right -= c.Px(4)
	}
	return right
}

func (l RecordingRow) Layout(c *Ctx, x, y, w float32) float32 {
	lines := float32(rowLineH + rowSmallH)
	if l.Snippet != "" {
		lines += rowSmallH + 2
	}
	h := c.Px(2*rowPadY + lines)
	r := RectF{x, y, x + w, y + h}
	if !c.Visible(r) {
		return h
	}
	hover := (c.Hover == l.ID || strings.HasPrefix(c.Hover, l.ID+":")) && l.OnClick != nil
	left := x + c.Px(rowPadX)
	top := y + c.Px(rowPadY)
	if l.OnClick != nil {
		c.Click(l.ID, r, l.OnClick) // under the actions
	}
	// The row's own drawing goes under its buttons: keep its place first.
	slot := len(c.ops)
	c.Draw(nil)
	right := rowActions(c, l.Actions, x+w-c.Px(rowPadX), y, h)
	// The icon is centred on the title and facts.
	iconS := c.Px(rowIcon)
	iconCY := top + c.Px(rowLineH+rowSmallH)/2
	icon := RectF{left, iconCY - iconS/2, left + iconS, iconCY + iconS/2}
	tx := rowText(c, x, l.NoIcon)

	// Right column on the title line: the chevron, then the state.
	chevron := len(l.Actions) == 0
	titleLine := RectF{tx, top, right, top + c.Px(rowLineH)}
	chev := RectF{right, titleLine.T, right, titleLine.B}
	if chevron {
		chev.L = right - c.Px(14)
	}
	sf := c.F(fontSmall)
	sf.Right = true
	stateW := c.M.TextWidth(l.Trailing, sf)
	stateR := chev.L
	if chevron {
		stateR -= c.Px(8)
	}
	state := RectF{stateR - stateW, titleLine.T, stateR, titleLine.B}
	if l.Live || l.Spinning {
		c.Animating = true
	}
	pulse := float32(Pulse(time.Duration(c.Now.UnixNano()), notchPulse))
	spinAngle := float32(2 * math.Pi * float64(c.Now.UnixNano()%int64(time.Second)) / float64(time.Second))
	c.ops[slot] = func(cv Canvas) {
		if hover {
			cv.RoundRect(r, c.Px(8), col(ColCardHover))
		}
		switch {
		case l.NoIcon:
		case l.Live:
			cx, cy := (icon.L+icon.R)/2, (icon.T+icon.B)/2
			cv.Circle(cx, cy, c.Px(9)*(0.85+0.3*pulse), RGBA(ColLive, 0.22*pulse))
			cv.Circle(cx, cy, c.Px(5.5), col(ColLive))
		case l.Spinning:
			cx, cy := (icon.L+icon.R)/2, (icon.T+icon.B)/2
			outer := iconS * 0.44
			inner := outer * 0.64
			cv.Arc(cx, cy, inner, outer, 0, 2*math.Pi, RGBA(ColText, 0.18))
			cv.Arc(cx, cy, inner, outer, spinAngle, spinAngle+math.Pi*0.65, col(ColText))
		default:
			glyph := l.Glyph
			if glyph == "" {
				glyph = GlyphArticle
			}
			DrawIcon(cv, glyph, icon, iconS, col(ColText))
		}
		cv.Text(l.Title, RectF{tx, titleLine.T, state.L - c.Px(12), titleLine.B}, c.F(fontBodySemi), col(ColText))
		cv.Text(l.Trailing, state, sf, col(ColMuted))
		if chevron {
			gc := col(ColFaint)
			if hover {
				gc = col(ColMuted)
			}
			DrawIcon(cv, GlyphChevron, chev, c.Px(14), gc)
		}
		ly := titleLine.B
		cv.Text(l.Facts, RectF{tx, ly, right, ly + c.Px(rowSmallH)}, c.F(fontSmall), col(ColMuted))
		if l.Snippet != "" {
			ly += c.Px(rowSmallH + 2)
			cv.Text(l.Snippet, RectF{tx, ly, right, ly + c.Px(rowSmallH)}, c.F(fontSmall), col(ColFaint))
		}
	}
	return h
}

// BrandLink is flit's mark and name with a line under it, clickable as a
// whole (the tray panel's way into the app).
type BrandLink struct {
	ID      string
	Detail  string
	OnClick func()
}

func (b BrandLink) Width(c *Ctx) float32 {
	return c.Px(20+10) + max(c.M.TextWidth("flit", c.F(fontBodySemi)), c.M.TextWidth(b.Detail, c.F(fontSmall))) + c.Px(20)
}

func (b BrandLink) Layout(c *Ctx, x, y, w float32) float32 {
	h := c.Px(40)
	r := RectF{x, y, x + w, y + h}
	s := c.Px(20)
	fx, fy := x+c.Px(8), y+(h-s)/2
	fill, cuts := FeatherPolys(s, fx, fy)
	tx := fx + s + c.Px(10)
	hover := c.Hover == b.ID
	c.Draw(func(cv Canvas) {
		if hover {
			cv.RoundRect(r, c.Px(8), col(ColCardHover))
		}
		cv.Polygons(fill, col(ColText))
		if len(cuts) > 0 {
			cv.Polygons(cuts, col(ColSidebar))
		}
		cv.Text("flit", RectF{tx, y + c.Px(3), x + w, y + c.Px(21)}, c.F(fontBodySemi), col(ColText))
		dc := col(ColMuted)
		if hover {
			dc = col(ColText)
		}
		cv.Text(b.Detail, RectF{tx, y + c.Px(20), x + w, y + c.Px(37)}, c.F(fontSmall), dc)
	})
	if b.OnClick != nil {
		c.Click(b.ID, r, b.OnClick)
	}
	return h
}

// RoundButton is a small circular icon button (play/pause beside a
// speaker), centred vertically in a row of height H (logical; 0 = its own
// size).
type RoundButton struct {
	ID      string
	Glyph   string
	Active  bool // filled white (playing)
	H       float32
	OnClick func()
}

const roundButtonSize = 30

func (b RoundButton) Width(c *Ctx) float32 { return c.Px(roundButtonSize) }

func (b RoundButton) Layout(c *Ctx, x, y, w float32) float32 {
	s := c.Px(roundButtonSize)
	h := max(s, c.Px(b.H))
	cy := y + h/2
	r := RectF{x, cy - s/2, x + s, cy + s/2}
	fill, fg := col(ColControl), col(ColText)
	switch {
	case b.Active && c.Hover == b.ID:
		fill, fg = col(rgb(0xDC, 0xDC, 0xE0)), col(ColInk)
	case b.Active:
		fill, fg = col(ColText), col(ColInk)
	case c.Hover == b.ID:
		fill = col(ColControlHi)
	}
	c.Draw(func(cv Canvas) {
		cv.Circle((r.L+r.R)/2, cy, s/2, fill)
		DrawIcon(cv, b.Glyph, r, c.Px(12), fg)
	})
	if b.OnClick != nil {
		c.Click(b.ID, r, b.OnClick)
	}
	return h
}

// PlayerBar is the audio player: play/pause, a bar showing where playback
// is (click to jump there, or drag the knob along it), and the time.
type PlayerBar struct {
	ID       string
	Playing  bool
	Pos, Len time.Duration
	Note     string // instead of the time, e.g. "Speaker 2's clearest moments"
	OnToggle func()
	// OnScrub follows the pointer while it is pressed on the bar (done
	// false), then says where it was let go (done true): the place to
	// play from. Pos shows where the pointer is meanwhile.
	OnScrub func(fraction float32, done bool)
}

func (p PlayerBar) Layout(c *Ctx, x, y, w float32) float32 {
	h := c.Px(44)
	if p.Playing {
		c.Animating = true
	}
	glyph := GlyphPlay
	if p.Playing {
		glyph = GlyphPause
	}
	btn := RoundButton{ID: p.ID + ":toggle", Glyph: glyph, Active: true, H: 44, OnClick: p.OnToggle}
	bw := btn.Width(c) + c.Px(6)
	btn.Layout(c, x, y, bw)

	timeText := clockOf(p.Pos) + " / " + clockOf(p.Len)
	tf := c.F(fontSmall)
	tw := c.M.TextWidth("00:00:00 / 00:00:00", tf)
	timeR := RectF{x + w - tw, y, x + w, y + h}
	bar := RectF{x + bw + c.Px(10), y + h/2 - c.Px(2), timeR.L - c.Px(14), y + h/2 + c.Px(2)}
	frac := float32(0)
	if p.Len > 0 {
		frac = max(0, min(1, float32(p.Pos)/float32(p.Len)))
	}
	hover := c.Hover == p.ID+":bar" || c.Pressed == p.ID+":bar"
	hit := RectF{bar.L - c.Px(6), y, bar.R + c.Px(6), y + h} // the knob's reach at both ends
	note := p.Note
	c.Draw(func(cv Canvas) {
		cv.RoundRect(bar, bar.H()/2, col(ColControl))
		fill := bar
		fill.R = bar.L + bar.W()*frac
		if fill.W() > 0 {
			cv.RoundRect(fill, bar.H()/2, col(ColText))
		}
		knob := c.Px(5)
		if hover {
			knob = c.Px(7)
		}
		cv.Circle(fill.R, (bar.T+bar.B)/2, knob, col(ColText))
		f := tf
		f.Right = true
		cv.Text(timeText, timeR, f, col(ColMuted))
		if note != "" {
			cv.Text(note, RectF{bar.L, y + h/2 + c.Px(6), bar.R, y + h}, c.F(fontSmall), col(ColFaint))
		}
	})
	if p.OnScrub != nil {
		c.Drag(p.ID+":bar", hit, func(px, _ float32, done bool) {
			p.OnScrub(max(0, min(1, (px-bar.L)/bar.W())), done)
		})
	}
	return h
}

// clockOf is m:ss or h:mm:ss.
func clockOf(d time.Duration) string { return Clock(d) }

// TranscriptBlock is one speaker's paragraph: the speaker and time over the
// text. Clicking it calls OnClick (play from here); Current marks the one
// playing now.
type TranscriptBlock struct {
	ID      string
	Speaker string
	Time    string
	Text    string
	Me      bool
	Current bool
	// Marks colour parts of Text (search results); ScrollTo asks the page
	// to scroll this block into view (the result being shown).
	Marks    []TextMark
	ScrollTo bool
	// Its text can be selected (across blocks); while the pointer is on
	// the block, play and copy buttons show in its top right corner
	// (they stay while Playing, or just after Copied).
	OnPlay  func()
	OnCopy  func()
	Playing bool
	Copied  bool
}

// Transcript block spacing (logical pixels): compact above and below, so
// more of the conversation fits on screen.
const (
	blockPadX  = 12
	blockPadY  = 6
	blockHeadH = 20
)

func (b TranscriptBlock) Layout(c *Ctx, x, y, w float32) float32 {
	pad, padY := c.Px(blockPadX), c.Px(blockPadY)
	headH := c.Px(blockHeadH)
	f := c.F(fontBody)
	textW := w - 2*pad
	th := c.M.ParagraphHeight(b.Text, f, textW)
	h := padY + headH + th + padY
	r := RectF{x, y, x + w, y + h}
	if b.ScrollTo {
		c.ScrollIntoView(y)
	}
	visible := c.Visible(r)
	// Hits are found topmost first, so the order matters: the block's
	// hover area at the bottom, its text over it, its buttons on top.
	if visible {
		c.HoverArea(b.ID, r)
	}
	// The text is selectable whether on screen or not, so a selection
	// keeps its ends while they scroll away; its lines are worked out only
	// when needed (drawn, or the pointer on it).
	textR := RectF{x + pad, y + padY + headH, x + w - pad, y + h}
	var lines func() *TextLines
	if lm, ok := c.M.(LineMetrics); ok {
		lines = func() *TextLines { return lm.ParagraphLines(b.Text, f, textW) }
	}
	from, to := 0, 0
	if lines != nil {
		from, to = c.Selectable(b.ID+":text", RectF{textR.L, textR.T, textR.L + textW, textR.T + th}, b.Text, lines)
	}
	if !visible {
		return h // off screen: room, but nothing to draw or click
	}
	hover := c.Hover == b.ID || strings.HasPrefix(c.Hover, b.ID+":")
	name := b.Speaker
	nf := c.F(fontBodySemi)
	nameW := c.M.TextWidth(name, nf)
	timeX := x + pad + nameW + c.Px(10)
	var sel []RectF
	if to > from {
		for _, q := range lines().Rects(from, to) {
			sel = append(sel, RectF{textR.L + q.L, textR.T + q.T, textR.L + q.R, textR.T + q.B})
		}
	}
	c.Draw(func(cv Canvas) {
		switch {
		case b.Current:
			cv.RoundRect(r, c.Px(8), col(ColCardHover))
			cv.RoundRect(RectF{r.L, r.T + c.Px(8), r.L + c.Px(3), r.B - c.Px(8)}, c.Px(1.5), col(ColText))
		case hover:
			cv.RoundRect(r, c.Px(8), RGBA(ColCardHover, 0.6))
		}
		nameColor := col(ColText)
		if b.Me {
			nameColor = col(ColMuted)
		}
		cv.Text(name, RectF{x + pad, y + padY, timeX, y + padY + headH}, nf, nameColor)
		tc := col(ColFaint)
		if hover || b.Current {
			tc = col(ColMuted)
		}
		cv.Text(b.Time, RectF{timeX, y + padY, x + w - pad, y + padY + headH}, c.F(fontSmall), tc)
		for _, q := range sel { // the selection, under the text
			cv.RoundRect(q, c.Px(2), RGBA(ColText, 0.24))
		}
		cv.MarkedParagraph(b.Text, textR, f, col(ColText), b.Marks)
	})
	if hover || b.Playing || b.Copied {
		// Play and copy, on the speaker's line at the right.
		bs := c.Px(iconButtonSize)
		by := y + padY + headH/2 - bs/2
		bx := x + w - c.Px(6) - bs
		copyGlyph := GlyphCopy
		if b.Copied {
			copyGlyph = GlyphCheck
		}
		if b.OnCopy != nil {
			IconButton{ID: b.ID + ":copy", Glyph: copyGlyph, OnClick: b.OnCopy}.Layout(c, bx, by, bs)
			bx -= bs + c.Px(2)
		}
		if b.OnPlay != nil {
			play := glyphPlayLine
			if b.Playing {
				play = glyphPauseLine
			}
			IconButton{ID: b.ID + ":play", Glyph: play, OnClick: b.OnPlay}.Layout(c, bx, by, bs)
		}
	}
	return h
}

// MeetingRow is a calendar meeting in a list: a calendar icon, its start
// and end in a narrow column where a recording row's title starts, the
// title with the place under it, and an action on the right (Join, or the
// recording). Its lines are a recording row's lines, so both line up. Now
// marks the one going on; Past dims the ones that are over. NoIcon leaves
// the icon out (the tray panel).
type MeetingRow struct {
	ID          string
	Time, Until string // "11:30", "12:00" ("All day", "")
	Title       string
	Place       string
	Now, Past   bool
	NoIcon      bool
	// Actions sit on the right (Sized, the last one rightmost);
	// HoverActions join them on their left while the pointer is on the row.
	Actions      []Node
	HoverActions []Node
	OnClick      func()
}

func (m MeetingRow) Layout(c *Ctx, x, y, w float32) float32 {
	lines := m.Place != "" || m.Until != "" // two lines: the place, or the end time
	h := c.Px(2*rowPadY + rowLineH + rowSmallH)
	if !lines {
		h = c.Px(2*rowPadY + rowLineH + 4)
	}
	r := RectF{x, y, x + w, y + h}
	if !c.Visible(r) {
		return h
	}
	left := x + c.Px(rowPadX)
	timeX := rowText(c, x, m.NoIcon)
	timeW := c.Px(rowTimeW)
	tx := timeX + c.Px(rowTimeW+rowTimeGap)
	// The row is a hover area under its buttons: hovering it or them
	// lights it and shows the hover actions.
	if m.OnClick != nil {
		c.Click(m.ID, r, m.OnClick)
	} else if len(m.HoverActions)+len(m.Actions) > 0 {
		c.HoverArea(m.ID, r)
	}
	rowHover := c.Hover == m.ID || strings.HasPrefix(c.Hover, m.ID+":")
	actions := m.Actions
	if rowHover {
		actions = append(append([]Node{}, m.HoverActions...), actions...)
	}
	// The row's own drawing goes under its buttons: keep its place first.
	slot := len(c.ops)
	c.Draw(nil)
	right := rowActions(c, actions, x+w-c.Px(rowPadX), y, h)
	hover := rowHover
	// Two lines as in a recording row (title, then the small line), or one
	// centred.
	top := y + c.Px(rowPadY)
	if !lines {
		top = y + (h-c.Px(rowLineH))/2
	}
	line1 := RectF{0, top, 0, top + c.Px(rowLineH)}
	line2 := RectF{0, line1.B, 0, line1.B + c.Px(rowSmallH)}
	iconS := c.Px(rowIcon)
	iconCY := (line1.T + line2.B) / 2
	if !lines {
		iconCY = (line1.T + line1.B) / 2
	}
	icon := RectF{left, iconCY - iconS/2, left + iconS, iconCY + iconS/2}
	span := func(l RectF, from, to float32) RectF { l.L, l.R = from, to; return l }
	c.ops[slot] = (func(cv Canvas) {
		if hover {
			cv.RoundRect(r, c.Px(8), col(ColCardHover))
		}
		if m.Now {
			// A thin white bar: going on now.
			cv.RoundRect(RectF{x + c.Px(3), y + c.Px(10), x + c.Px(5.5), y + h - c.Px(10)}, c.Px(1.25), col(ColText))
		}
		strong, muted := col(ColText), col(ColMuted)
		if m.Past {
			strong, muted = col(ColMuted), col(ColFaint)
		}
		if !m.NoIcon {
			DrawIcon(cv, GlyphMeeting, icon, iconS, strong)
		}
		switch {
		case m.Until == "": // all day
			cv.Text(m.Time, span(line1, timeX, timeX+timeW), c.F(fontSmall), muted)
		default:
			cv.Text(m.Time, span(line1, timeX, timeX+timeW), c.F(fontBodySemi), strong)
			cv.Text(m.Until, span(line2, timeX, timeX+timeW), c.F(fontSmall), muted)
		}
		cv.Text(m.Title, span(line1, tx, right), c.F(fontBodySemi), strong)
		if m.Place != "" {
			cv.Text(m.Place, span(line2, tx, right), c.F(fontSmall), muted)
		}
	})
	return h
}

// IconButton is a small round button with only an icon (a search's
// previous and next), quiet until hovered.
type IconButton struct {
	ID       string
	Glyph    string
	Disabled bool
	OnClick  func()
}

const iconButtonSize = 28

func (b IconButton) Width(c *Ctx) float32 { return c.Px(iconButtonSize) }

func (b IconButton) Layout(c *Ctx, x, y, w float32) float32 {
	s := c.Px(iconButtonSize)
	r := RectF{x, y, x + s, y + s}
	hover := c.Hover == b.ID && !b.Disabled
	fg := col(ColMuted)
	switch {
	case b.Disabled:
		fg = col(ColFaint)
	case hover:
		fg = col(ColText)
	}
	c.Draw(func(cv Canvas) {
		if hover {
			cv.Circle((r.L+r.R)/2, (r.T+r.B)/2, s/2, col(ColControlHi))
		}
		DrawIcon(cv, b.Glyph, r, c.Px(16), fg)
	})
	if !b.Disabled && b.OnClick != nil {
		c.Click(b.ID, r, b.OnClick)
	}
	return s
}

// CornerCard is a Card with a few Sized nodes in its top right corner (a
// search field with its result count and arrows), in a row of their own
// above the items. Sticky keeps the corner at the top of the visible part
// of the page while the card scrolls past it (until the card's end), on a
// raised backing so the text beneath doesn't show through.
type CornerCard struct {
	Corner []Node
	Sticky bool
	Items  []Node
	Gap    float32
}

const (
	cornerRowH = 34
	cornerGap  = 6
	cornerFade = 24   // how far the stuck pill's halo fades out beyond it
	cornerHalo = 0.76 // the halo's opacity at the pill's edge
)

func (k CornerCard) Layout(c *Ctx, x, y, w float32) float32 {
	pad := c.Px(16)
	rowH := c.Px(cornerRowH)
	slot := len(c.ops)
	c.Draw(nil) // the card's background, sized after its items
	itemsTop := y + pad + rowH + c.Px(8)
	h := VStack{Gap: k.Gap, Items: k.Items}.Layout(c, x+pad, itemsTop, w-2*pad) + (itemsTop - y) + pad
	card := RectF{x, y, x + w, y + h}
	radius := c.Px(10)
	c.ops[slot] = func(cv Canvas) { cv.RoundRect(card, radius, col(ColCard)) }

	// The corner's width, and where it is: its row, or stuck to the top
	// of the view.
	var cw float32
	gap := c.Px(cornerGap)
	for i, n := range k.Corner {
		if i > 0 {
			cw += gap
		}
		if sz, ok := n.(Sized); ok {
			cw += sz.Width(c)
		}
	}
	top := y + pad
	if k.Sticky {
		top = max(top, min(c.ClipRect().T+c.Px(8), y+h-pad-rowH))
	}
	stuck := top > y+pad
	right := x + w - pad
	if stuck {
		back := RectF{right - cw - c.Px(6), top - c.Px(4), right + c.Px(6), top + rowH + c.Px(4)}
		// A halo of the card colour around the pill, fading out a little
		// beyond its rounded edge, so the text scrolling beneath recedes
		// around it; one size, one cached bitmap; inside the card.
		fade := c.Px(cornerFade)
		halo := RectF{back.L - fade, back.T - fade, back.R + fade, back.B + fade}
		hw, hh := int(math.Ceil(float64(halo.W()))), int(math.Ceil(float64(halo.H())))
		key := fmt.Sprintf("cornerhalo:%dx%d:%v", hw, hh, fade)
		c.PushClip(card)
		c.Draw(func(cv Canvas) {
			cv.Bitmap(key, halo.L, halo.T, hw, hh, 1, func() []byte {
				return haloPixels(hw, hh, float64(fade), float64(back.H()/2), ColCard, cornerHalo)
			})
		})
		c.PopClip()
		c.Draw(func(cv Canvas) { cv.RoundRect(back, back.H()/2, col(ColCardHover)) })
		c.HoverArea("corner", back) // clicks on it don't reach the text beneath
	}
	cx := right - cw
	probe := NewCtx(c.Scale, c.M, c.Now, "", "", nil)
	for i, n := range k.Corner {
		nw := float32(0)
		if sz, ok := n.(Sized); ok {
			nw = sz.Width(c)
		}
		nh := n.Layout(probe, 0, 0, nw)
		n.Layout(c, cx, top+(rowH-nh)/2, nw)
		cx += nw
		if i < len(k.Corner)-1 {
			cx += gap
		}
	}
	return h
}

// ProgressCard is something under way (a model coming down, a recording
// being transcribed), on a card the width of the page whose background
// fills up from the left as it goes: the name, a small grey figure on its
// right (the percentage, "Ready", "Waiting") and a line about it under
// the name.
type ProgressCard struct {
	ID       string
	Title    string
	Detail   string
	Trailing string
	Value    float64 // 0..1
}

const progressCardH = 58

func (d ProgressCard) Layout(c *Ctx, x, y, w float32) float32 {
	h := c.Px(progressCardH)
	r := RectF{x, y, x + w, y + h}
	if !c.Visible(r) {
		return h
	}
	pad := c.Px(16)
	radius := c.Px(10)
	fill := float32(max(0, min(1, d.Value))) * w
	sf := c.F(fontSmall)
	sf.Right = true
	tw := c.M.TextWidth(d.Trailing, sf)
	mid := y + h/2
	c.Draw(func(cv Canvas) {
		cv.RoundRect(r, radius, col(ColCard))
		if fill > 0 {
			cv.PushClip(RectF{r.L, r.T, r.L + fill, r.B})
			cv.RoundRect(r, radius, col(ColCardHover))
			cv.PopClip()
		}
		cv.Text(d.Title, RectF{x + pad, mid - c.Px(19), x + w - pad - tw - c.Px(12), mid + c.Px(1)}, c.F(fontBodySemi), col(ColText))
		cv.Text(d.Detail, RectF{x + pad, mid + c.Px(1), x + w - pad, mid + c.Px(19)}, c.F(fontSmall), col(ColMuted))
		cv.Text(d.Trailing, RectF{x + w - pad - tw, mid - c.Px(19), x + w - pad, mid + c.Px(1)}, sf, col(ColMuted))
	})
	return h
}
