package ui

import (
	"testing"
	"time"
)

func TestCalendarBadgePopoverLayoutAndClicks(t *testing.T) {
	clicked := ""
	unlinked := false
	badge := CalendarBadge{
		ID:        "session:cal",
		Glyph:     GlyphCalendarRepeat,
		Header:    "Recurring meeting",
		Title:     "Weekly Sync",
		When:      "Mon 20 Jan, 10:00–10:30",
		Place:     "Microsoft Teams",
		Action:    "Not this meeting",
		OnAction:  func() { unlinked = true },
		PrevTitle: "Previous meetings",
		Prev: []PopoverLink{
			{ID: "s3", Title: "Weekly Sync (Q1 Kickoff)", Detail: "13 Jan", OnClick: func() { clicked = "s3" }},
			{ID: "s2", Title: "Weekly Sync", Detail: "6 Jan", OnClick: func() { clicked = "s2" }},
		},
	}

	// Below the title row sits another clickable button that must be covered by
	// the overlay popover when open.
	underClicked := false
	page := VStack{Gap: 10, Items: []Node{
		HStack{Gap: 10, Items: []Node{
			badge,
			VStack{Items: []Node{TextField{ID: "session:title", Title: true, Value: "Weekly Sync"}}},
		}},
		Button{ID: "under:btn", Label: "Copy", OnClick: func() { underClicked = true }},
	}}

	// 1. At rest: no popover hits registered.
	c0 := NewCtx(1, fakeMetrics{}, time.Now(), "", "", nil)
	Root(c0, page, 500, 400)
	if _, ok := c0.HitByID("session:cal:pop"); ok {
		t.Fatalf("popover should not be open when not hovered")
	}
	if _, ok := c0.HitByID("session:cal"); !ok {
		t.Fatalf("calendar icon hover area missing")
	}

	// 2. Hovered over calendar icon: popover opens left-aligned with icon,
	// directly below it, and its links win hit-testing over page content.
	c1 := NewCtx(1, fakeMetrics{}, time.Now(), "session:cal", "", nil)
	c1.PushClip(RectF{0, 0, 500, 400})
	page.Layout(c1, 32, 20, 436)
	c1.FlushOverlays()
	c1.PopClip()

	pop, ok := c1.HitByID("session:cal:pop")
	if !ok {
		t.Fatalf("popover should open when calendar icon is hovered")
	}
	if pop.R.L != 32 {
		t.Errorf("popover left edge = %v, want 32 (aligned with icon left edge)", pop.R.L)
	}
	if pop.R.T < 20+calBadgeH {
		t.Errorf("popover top = %v, want below title row (%v)", pop.R.T, 20+calBadgeH)
	}

	// Clicking a previous meeting link hits the popover link, not the button below.
	linkHit, ok := c1.HitByID("session:cal:prev:s3")
	if !ok {
		t.Fatalf("previous meeting link hit missing")
	}
	topHit, ok := c1.HitAt((linkHit.R.L+linkHit.R.R)/2, (linkHit.R.T+linkHit.R.B)/2)
	if !ok || topHit.ID != "session:cal:prev:s3" {
		t.Fatalf("expected top hit at link to be session:cal:prev:s3, got %+v", topHit)
	}
	topHit.OnClick()
	if clicked != "s3" || underClicked {
		t.Fatalf("clicked=%q underClicked=%v, want s3/false", clicked, underClicked)
	}

	// 3. Hovering a link inside the popover keeps the popover open.
	c2 := NewCtx(1, fakeMetrics{}, time.Now(), "session:cal:prev:s2", "", nil)
	Root(c2, page, 500, 400)
	if _, ok := c2.HitByID("session:cal:pop"); !ok {
		t.Fatalf("popover should stay open while hovering a link inside it")
	}
	act, ok := c2.HitByID("session:cal:action")
	if !ok {
		t.Fatalf("unlink action hit missing")
	}
	act.OnClick()
	if !unlinked {
		t.Fatalf("unlink action callback did not fire")
	}
}
