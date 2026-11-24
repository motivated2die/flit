package ui

import (
	"math"
	"strings"
)

// PopoverLink is a clickable row inside a hover popover (for example, a
// previous recorded meeting in a recurring series).
type PopoverLink struct {
	ID      string
	Title   string
	Detail  string // right-aligned date/time, muted
	OnClick func()
}

// CalendarBadge is a calendar icon placed to the left of a page title.
// Hovering over it opens a popover directly below the icon, left-aligned with
// the icon's left edge and extending downward and to the right.
type CalendarBadge struct {
	ID        string
	Glyph     string  // GlyphMeeting or GlyphCalendarRepeat
	H         float32 // logical row height (0 = 42, matching Title TextField)
	Header    string  // small caption at top of popover (e.g. "From your calendar")
	Title     string  // original calendar event title
	When      string  // scheduled time window
	Place     string  // optional location / provider
	Action    string  // optional footer link (e.g. "Not this meeting")
	OnAction  func()
	PrevTitle string        // caption above Prev (e.g. "Previous meetings")
	Prev      []PopoverLink // up to 3 earlier recordings in the same recurring series
}

const (
	calBadgeIcon = 22
	calBadgeH    = 42
	calPopMaxW   = 320
	calPopMinW   = 220
	calPopPadX   = 14
	calPopPadY   = 12
	calPopGap    = 2
	calPopRowH   = 28
)

// Width is the visual width of the calendar icon so it lines up with page text.
func (b CalendarBadge) Width(c *Ctx) float32 { return c.Px(calBadgeIcon) }

func (b CalendarBadge) Layout(c *Ctx, x, y, w float32) float32 {
	h := c.Px(calBadgeH)
	if b.H > 0 {
		h = c.Px(b.H)
	}
	iconSize := c.Px(calBadgeIcon)
	iconRect := RectF{x, y, x + iconSize, y + h}
	hitPad := c.Px(5)
	hitRect := RectF{x - hitPad, y + hitPad, x + iconSize + hitPad, y + h - hitPad}

	prefix := b.ID + ":"
	open := b.ID != "" && (c.Hover == b.ID || strings.HasPrefix(c.Hover, prefix) ||
		c.Pressed == b.ID || strings.HasPrefix(c.Pressed, prefix))

	glyph := b.Glyph
	if glyph == "" {
		glyph = GlyphMeeting
	}

	c.Draw(func(cv Canvas) {
		fg := col(ColMuted)
		if open {
			cv.RoundRect(hitRect, c.Px(7), RGBA(ColCardHover, 0.85))
			fg = col(ColText)
		}
		DrawIcon(cv, glyph, iconRect, iconSize, fg)
	})
	if b.ID != "" {
		c.HoverArea(b.ID, hitRect)
	}

	if open {
		clip := c.ClipRect()
		c.Overlay(func(oc *Ctx) {
			b.layoutPopover(oc, x, hitRect, y+h, clip)
		})
	}
	return h
}

func (b CalendarBadge) layoutPopover(c *Ctx, iconLeft float32, hitRect RectF, rowBottom float32, clip RectF) {
	padX := c.Px(calPopPadX)
	padY := c.Px(calPopPadY)
	popL := iconLeft
	popT := rowBottom + c.Px(calPopGap)

	popW := c.Px(calPopMaxW)
	if avail := clip.R - popL - c.Px(16); avail > 0 && avail < popW {
		popW = max(c.Px(calPopMinW), avail)
	}
	inW := popW - 2*padX

	fCap := c.F(fontCaption)
	fTitle := c.F(fontBodySemi)
	fSmall := c.F(fontSmall)

	// Measure popover height before painting the card background.
	curY := popT + padY
	var headerRect, titleRect, whenRect, placeRect, divRect, prevCapRect, actionRect RectF
	if b.Header != "" {
		headerRect = RectF{popL + padX, curY, popL + popW - padX, curY + c.Px(16)}
		curY = headerRect.B + c.Px(4)
	}
	if b.Title != "" {
		th := c.M.ParagraphHeight(b.Title, fTitle, inW)
		titleRect = RectF{popL + padX, curY, popL + popW - padX, curY + th}
		curY = titleRect.B + c.Px(3)
	}
	if b.When != "" {
		wh := c.M.ParagraphHeight(b.When, fSmall, inW)
		whenRect = RectF{popL + padX, curY, popL + popW - padX, curY + wh}
		curY = whenRect.B + c.Px(2)
	}
	if b.Place != "" {
		ph := c.M.ParagraphHeight(b.Place, fSmall, inW)
		placeRect = RectF{popL + padX, curY, popL + popW - padX, curY + ph}
		curY = placeRect.B + c.Px(2)
	}

	type linkLayout struct {
		id   string
		link PopoverLink
		r    RectF
	}
	var links []linkLayout
	if len(b.Prev) > 0 {
		curY += c.Px(8)
		lineH := max(1, c.Px(1))
		divRect = RectF{popL + padX, curY, popL + popW - padX, curY + lineH}
		curY = divRect.B + c.Px(8)
		if b.PrevTitle != "" {
			prevCapRect = RectF{popL + padX, curY, popL + popW - padX, curY + c.Px(16)}
			curY = prevCapRect.B + c.Px(4)
		}
		rowH := c.Px(calPopRowH)
		rowPad := c.Px(6)
		for _, p := range b.Prev {
			rr := RectF{popL + padX - rowPad, curY, popL + popW - padX + rowPad, curY + rowH}
			links = append(links, linkLayout{id: b.ID + ":prev:" + p.ID, link: p, r: rr})
			curY += rowH + c.Px(2)
		}
	}

	if b.Action != "" && b.OnAction != nil {
		curY += c.Px(6)
		aw := c.M.TextWidth(b.Action, fSmall)
		actionRect = RectF{popL + padX, curY, popL + padX + aw + c.Px(4), curY + c.Px(20)}
		curY = actionRect.B
	}

	popRect := RectF{popL, popT, popL + popW, curY + padY}
	rad := c.Px(10)
	edge := max(1, c.Px(1))

	// Bridge the small gap between the icon hit box and the popover top so
	// moving the pointer straight down keeps the popover open.
	bridge := RectF{hitRect.L, hitRect.B, max(hitRect.R, popL+c.Px(36)), popT}
	c.HoverArea(b.ID+":bridge", bridge)
	c.HoverArea(b.ID+":pop", popRect)

	actionID := b.ID + ":action"
	c.Draw(func(cv Canvas) {
		// Soft drop shadow, hairline border, card surface.
		for i, a := range []float32{0.10, 0.08, 0.06} {
			d := c.Px(float32(3 + 3*i))
			cv.RoundRect(RectF{popRect.L - d/2, popRect.T + c.Px(1), popRect.R + d/2, popRect.B + d}, rad+d/2, ColorF{0, 0, 0, a})
		}
		cv.RoundRect(popRect, rad, col(ColEdge))
		cv.RoundRect(popRect.Inset(edge), rad-edge, col(ColCard))

		if b.Header != "" {
			cv.Text(b.Header, headerRect, fCap, col(ColFaint))
		}
		if b.Title != "" {
			cv.Paragraph(b.Title, titleRect, fTitle, col(ColText))
		}
		if b.When != "" {
			cv.Paragraph(b.When, whenRect, fSmall, col(ColMuted))
		}
		if b.Place != "" {
			cv.Paragraph(b.Place, placeRect, fSmall, col(ColMuted))
		}
		if divRect.W() > 0 {
			cv.RoundRect(divRect, 0, col(ColDivider))
		}
		if b.PrevTitle != "" && prevCapRect.W() > 0 {
			cv.Text(b.PrevTitle, prevCapRect, fCap, col(ColFaint))
		}
		for _, it := range links {
			hover := c.Hover == it.id
			pressed := c.Pressed == it.id
			if pressed {
				cv.RoundRect(it.r, c.Px(6), col(ColControl))
			} else if hover {
				cv.RoundRect(it.r, c.Px(6), col(ColCardHover))
			}
			inL := it.r.L + c.Px(6)
			inR := it.r.R - c.Px(6)
			icoR := RectF{inL, it.r.T, inL + c.Px(14), it.r.B}
			icoCol := col(ColMuted)
			if hover {
				icoCol = col(ColText)
			}
			DrawIcon(cv, GlyphArticle, icoR, c.Px(13), icoCol)
			textL := icoR.R + c.Px(6)
			textR := inR
			if it.link.Detail != "" {
				dw := float32(math.Ceil(float64(c.M.TextWidth(it.link.Detail, fSmall))))
				detL := inR - dw
				cv.Text(it.link.Detail, RectF{detL, it.r.T, inR, it.r.B}, fSmall, col(ColMuted))
				textR = detL - c.Px(8)
			}
			if textR > textL {
				cv.Text(it.link.Title, RectF{textL, it.r.T, textR, it.r.B}, fSmall, col(ColText))
			}
		}
		if b.Action != "" && b.OnAction != nil {
			ac := col(ColMuted)
			if c.Hover == actionID || c.Pressed == actionID {
				ac = col(ColText)
			}
			cv.Text(b.Action, actionRect, fSmall, ac)
		}
	})

	for _, it := range links {
		if it.link.OnClick != nil {
			c.Click(it.id, it.r, it.link.OnClick)
		}
	}
	if b.Action != "" && b.OnAction != nil {
		c.Click(actionID, actionRect, b.OnAction)
	}
}
