package ui

import (
	"fmt"
	"math"
	"strings"
)

const (
	scrollbarHoverW = 8 // extended handle width and subheading dot diameter (logical px)
)

// scrollbarDrag keeps the pointer's grab offset inside the handle while
// dragging so the handle never jumps to the cursor (UI thread only).
var scrollbarDrag struct {
	id     string
	grabY  float32
	active bool
}

// scrollbarConfig describes one vertical scrollbar.
type scrollbarConfig struct {
	ScrollID   string   // key in Ctx.Scroll
	RightX     float32  // right edge of the handle (fixed; expands inward on hover)
	TrackTop   float32  // top of the track (device px)
	TrackH     float32  // height of the track (device px)
	ViewH      float32  // visible viewport height (device px)
	ContentH   float32  // total scrollable content height (device px)
	ContentTop float32  // top Y of content at scroll=0 (e.g. pad, device px)
	Offset     float32  // current scroll offset (device px)
	MaxScroll  float32  // maximum scroll offset (device px)
	Anchors    []Anchor // optional subheadings for jump dots (>3 view heights)
}

// drawScrollbar draws a slim, transparent-track vertical scrollbar whose
// handle expands inward on hover (keeping right padding and main content
// fixed) and can be dragged to scrub. When the content is more than 3 view
// heights tall, subheading anchors are shown as dots along the track (hidden
// whenever overlapped by the handle) with click-to-scroll and a hover tooltip
// to the left.
func drawScrollbar(c *Ctx, cfg scrollbarConfig) {
	if cfg.MaxScroll <= 0 || cfg.TrackH <= 0 || cfg.ContentH <= 0 {
		return
	}
	barID := "scrollbar:" + cfg.ScrollID
	trackID := "scrolltrack:" + cfg.ScrollID
	dotPrefix := "scrolldot:" + cfg.ScrollID + ":"
	th := max(c.Px(28), cfg.TrackH*cfg.ViewH/cfg.ContentH)
	if th > cfg.TrackH {
		th = cfg.TrackH
	}
	travel := cfg.TrackH - th
	ty := cfg.TrackTop
	if travel > 0 {
		ty += travel * max(0, min(1, cfg.Offset/cfg.MaxScroll))
	}

	extW := c.Px(scrollbarHoverW)
	restW := c.Px(scrollbarW)
	dotR := extW / 2
	dotCX := cfg.RightX - dotR

	// Register a proximity hover zone covering the track and just beside it
	// (no visual background, no layout shift). Dots and handle register on top.
	trackZone := RectF{cfg.RightX - c.Px(24), cfg.TrackTop - c.Px(4), cfg.RightX + c.Px(4), cfg.TrackTop + cfg.TrackH + c.Px(4)}
	c.HoverArea(trackID, trackZone)

	// Decide whether subheading dots should appear (> 3 view heights tall).
	type dotItem struct {
		id     string
		label  string
		y      float32
		target float32
	}
	var dots []dotItem
	if len(cfg.Anchors) > 0 && cfg.ViewH > 0 && cfg.ContentH > 3*cfg.ViewH {
		minGap := extW * 1.4
		lastY := cfg.TrackTop - minGap
		for i, a := range cfg.Anchors {
			// Window Y = ContentTop - Offset + localContentY  =>  localContentY = a.Y + Offset - ContentTop.
			contentY := a.Y + cfg.Offset - cfg.ContentTop
			var target float32
			if i == 0 && contentY <= c.Px(64) {
				target = 0 // page title always jumps cleanly to the very top
			} else {
				target = max(0, min(cfg.MaxScroll, contentY-c.Px(12)))
			}
			frac := target / cfg.MaxScroll
			dy := cfg.TrackTop + dotR + max(0, cfg.TrackH-2*dotR)*frac
			if dy < lastY+minGap {
				dy = lastY + minGap
			}
			if dy > cfg.TrackTop+cfg.TrackH-dotR {
				continue
			}
			// Hide any dot that the scrollbar handle overlaps.
			if dy+dotR >= ty && dy-dotR <= ty+th {
				continue
			}
			lastY = dy
			dots = append(dots, dotItem{
				id:     fmt.Sprintf("%s%d", dotPrefix, i),
				label:  a.Label,
				y:      dy,
				target: target,
			})
		}
	}

	dragging := c.Pressed == barID
	trackActive := c.Hover == trackID || c.Hover == barID || dragging ||
		strings.HasPrefix(c.Hover, dotPrefix) || strings.HasPrefix(c.Pressed, dotPrefix)
	w := restW
	handleCol := RGBA(ColText, 0.18)
	extendedCol := RGBA(ColText, 0.30)
	if trackActive {
		w = extW
		handleCol = extendedCol
	}
	if dragging {
		handleCol = RGBA(ColText, 0.44)
	}
	bar := RectF{cfg.RightX - w, ty, cfg.RightX, ty + th}

	// Register hit targets for visible dots first, then the handle.
	var hoveredDot *dotItem
	for i := range dots {
		d := dots[i]
		if c.Hover == d.id || c.Pressed == d.id {
			dd := d
			hoveredDot = &dd
		}
		hitPad := c.Px(5)
		hit := RectF{dotCX - dotR - hitPad, d.y - dotR - hitPad, cfg.RightX + c.Px(4), d.y + dotR + hitPad}
		target := d.target
		scrollID := cfg.ScrollID
		c.Click(d.id, hit, func() {
			if c.ScrollTo != nil {
				c.ScrollTo(scrollID, target)
			} else if c.Scroll != nil {
				c.Scroll[scrollID] = target
			}
		})
	}

	// Generous horizontal hit area for the handle (expands inward, never shifts content).
	handleHit := RectF{cfg.RightX - max(extW, c.Px(14)), ty - c.Px(2), cfg.RightX + c.Px(4), ty + th + c.Px(2)}
	c.Drag(barID, handleHit, func(_, py float32, done bool) {
		if !scrollbarDrag.active || scrollbarDrag.id != barID {
			scrollbarDrag.id = barID
			scrollbarDrag.grabY = py - ty
			scrollbarDrag.active = true
		}
		if travel > 0 {
			newTY := py - scrollbarDrag.grabY
			t := max(0, min(1, (newTY-cfg.TrackTop)/travel))
			to := t * cfg.MaxScroll
			if c.SetScroll != nil {
				c.SetScroll(cfg.ScrollID, to)
			} else if c.Scroll != nil {
				c.Scroll[cfg.ScrollID] = to
			}
		}
		if done {
			scrollbarDrag.active = false
		}
	})

	c.Draw(func(cv Canvas) {
		// Dots appear only while hovering/near the scrollbar track, sharing the
		// exact width and colour of the extended handle.
		if trackActive {
			for _, d := range dots {
				colDot := extendedCol
				if hoveredDot != nil && hoveredDot.id == d.id {
					colDot = RGBA(ColText, 0.55)
				}
				cv.Circle(dotCX, d.y, dotR, colDot)
			}
		}
		// Scrollbar handle (transparent track: no track background).
		cv.RoundRect(bar, bar.W()/2, handleCol)

		// Hover popover tooltip to the left of the hovered dot.
		if hoveredDot != nil {
			font := c.F(fontSmall)
			padX := c.Px(8)
			tipH := c.Px(24)
			tw := c.M.TextWidth(hoveredDot.label, font)
			tipW := tw + 2*padX
			tipR := dotCX - dotR - c.Px(8)
			tipL := tipR - tipW
			tipT := float32(math.Round(float64(hoveredDot.y - tipH/2)))
			tipT = max(cfg.TrackTop, min(cfg.TrackTop+cfg.TrackH-tipH, tipT))
			tip := RectF{tipL, tipT, tipR, tipT + tipH}
			rad := c.Px(6)
			edge := max(1, c.Px(1))
			// Subtle drop shadow + hairline border + card surface.
			cv.RoundRect(RectF{tip.L - c.Px(1), tip.T + c.Px(1), tip.R + c.Px(1), tip.B + c.Px(3)}, rad+c.Px(1), ColorF{0, 0, 0, 0.14})
			cv.RoundRect(tip, rad, col(ColEdge))
			cv.RoundRect(tip.Inset(edge), rad-edge, col(ColCard))
			cv.Text(hoveredDot.label, RectF{tip.L + padX, tip.T, tip.R - padX, tip.B}, font, col(ColText))
		}
	})
}
