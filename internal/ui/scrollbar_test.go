package ui

import (
	"strings"
	"testing"
	"time"
)

type sbRecorder struct {
	recorder
	rects   []RectF
	circles int
}

func (r *sbRecorder) RoundRect(rc RectF, _ float32, _ ColorF) { r.rects = append(r.rects, rc) }
func (r *sbRecorder) Circle(_, _, _ float32, _ ColorF)        { r.circles++ }

func TestScrollbarHoverAndDrag(t *testing.T) {
	scroll := map[string]float32{"page:test": 0}
	cfg := scrollbarConfig{
		ScrollID:   "page:test",
		RightX:     796,
		TrackTop:   10,
		TrackH:     500,
		ViewH:      500,
		ContentH:   1500,
		ContentTop: 32,
		Offset:     0,
		MaxScroll:  1000,
	}

	// Resting: 4 px wide, right edge fixed at 796.
	c := NewCtx(1, fakeMetrics{}, time.Now(), "", "", scroll)
	drawScrollbar(c, cfg)
	rRest := &sbRecorder{}
	c.Paint(rRest)
	if len(rRest.rects) != 1 {
		t.Fatalf("resting rects = %d, want 1", len(rRest.rects))
	}
	if rRest.rects[0].W() != scrollbarW || rRest.rects[0].R != 796 {
		t.Errorf("resting bar = %+v, want width %v right 796", rRest.rects[0], scrollbarW)
	}

	// Hovering the track itself also expands the handle inward to 8 px.
	cTrack := NewCtx(1, fakeMetrics{}, time.Now(), "scrolltrack:page:test", "", scroll)
	drawScrollbar(cTrack, cfg)
	rTrack := &sbRecorder{}
	cTrack.Paint(rTrack)
	if len(rTrack.rects) != 1 || rTrack.rects[0].W() != scrollbarHoverW || rTrack.rects[0].R != 796 {
		t.Errorf("track-hovered bar = %+v, want width %v right 796", rTrack.rects, scrollbarHoverW)
	}

	// Hovered: expands inward to 8 px, right edge stays 796.
	cHov := NewCtx(1, fakeMetrics{}, time.Now(), "scrollbar:page:test", "", scroll)
	drawScrollbar(cHov, cfg)
	rHov := &sbRecorder{}
	cHov.Paint(rHov)
	if len(rHov.rects) != 1 || rHov.rects[0].W() != scrollbarHoverW || rHov.rects[0].R != 796 {
		t.Errorf("hovered bar = %+v, want width %v right 796", rHov.rects, scrollbarHoverW)
	}

	// Dragging the handle updates scroll smoothly without jumping on initial grab.
	hit, ok := cHov.HitByID("scrollbar:page:test")
	if !ok || hit.OnDrag == nil {
		t.Fatal("scrollbar handle missing OnDrag hit")
	}
	scrollbarDrag.active = false
	hit.OnDrag(794, 50, false) // grab 40 px below handle top (10)
	if scroll["page:test"] != 0 {
		t.Errorf("initial grab jumped scroll to %v", scroll["page:test"])
	}
	hit.OnDrag(794, 210, false) // move down 160 px
	if scroll["page:test"] <= 0 {
		t.Errorf("drag did not advance scroll: %v", scroll["page:test"])
	}
	hit.OnDrag(794, 210, true)
	if scrollbarDrag.active {
		t.Error("drag state stayed active after release")
	}
}

func TestScrollbarDotsThresholdOverlapAndTooltip(t *testing.T) {
	anchors := []Anchor{
		{Label: "Notes", Y: 40},
		{Label: "Summary", Y: 1000},
		{Label: "Transcript", Y: 2000},
	}

	// <= 3 view heights: no dots rendered.
	cShort := NewCtx(1, fakeMetrics{}, time.Now(), "", "", map[string]float32{"p": 0})
	drawScrollbar(cShort, scrollbarConfig{
		ScrollID: "p", RightX: 796, TrackTop: 10, TrackH: 500,
		ViewH: 500, ContentH: 1500, ContentTop: 32, Offset: 0, MaxScroll: 1000,
		Anchors: anchors,
	})
	if _, ok := cShort.HitByID("scrolldot:p:1"); ok {
		t.Error("dots rendered on page <= 3 view heights")
	}

	// > 3 view heights: dots rendered, top dot overlapped by handle is hidden,
	// lower dots are clickable and scroll to their section, hover shows tooltip.
	scroll := map[string]float32{"p": 0}
	var smoothTo float32 = -1
	cfg := scrollbarConfig{
		ScrollID: "p", RightX: 796, TrackTop: 10, TrackH: 500,
		ViewH: 500, ContentH: 2500, ContentTop: 32, Offset: 0, MaxScroll: 2000,
		Anchors: anchors,
	}
	c := NewCtx(1, fakeMetrics{}, time.Now(), "", "", scroll)
	c.ScrollTo = func(_ string, to float32) { smoothTo = to }
	drawScrollbar(c, cfg)

	if _, ok := c.HitByID("scrolldot:p:0"); ok {
		t.Error("dot 0 overlapped by handle at top should be hidden")
	}
	d1, ok := c.HitByID("scrolldot:p:1")
	if !ok {
		t.Fatal("expected visible dot 1 (Summary)")
	}
	d1.OnClick()
	if smoothTo <= 0 {
		t.Errorf("clicking dot 1 did not scroll: smoothTo=%v", smoothTo)
	}

	// Not hovering the track: dots are NOT drawn.
	rIdle := &sbRecorder{}
	c.Paint(rIdle)
	if rIdle.circles != 0 {
		t.Errorf("dots drawn while not hovering track: %d circles", rIdle.circles)
	}

	// Hovering the track draws the visible (non-overlapped) dots.
	cTrackHov := NewCtx(1, fakeMetrics{}, time.Now(), "scrolltrack:p", "", scroll)
	drawScrollbar(cTrackHov, cfg)
	rTrackHov := &sbRecorder{}
	cTrackHov.Paint(rTrackHov)
	if rTrackHov.circles != 2 {
		t.Errorf("track-hovered dots = %d, want 2 (Summary, Transcript)", rTrackHov.circles)
	}

	// Scrolled down: the top page-title dot is no longer overlapped and jumps to 0.
	cfgMid := cfg
	cfgMid.Offset = 1000
	cfgMid.Anchors = []Anchor{
		{Label: "Meeting Title", Y: 32 - 1000},
		{Label: "Summary", Y: 1000 - 1000},
	}
	var topTo float32 = -1
	cMid := NewCtx(1, fakeMetrics{}, time.Now(), "scrolltrack:p", "", scroll)
	cMid.ScrollTo = func(_ string, to float32) { topTo = to }
	drawScrollbar(cMid, cfgMid)
	dTop, ok := cMid.HitByID("scrolldot:p:0")
	if !ok {
		t.Fatal("expected top page-title dot when scrolled down")
	}
	dTop.OnClick()
	if topTo != 0 {
		t.Errorf("clicking top page-title dot scrolled to %v, want 0", topTo)
	}

	// Hovering dot 1 draws the "Summary" popover tooltip to the left of the dot.
	cTip := NewCtx(1, fakeMetrics{}, time.Now(), "scrolldot:p:1", "", scroll)
	drawScrollbar(cTip, cfg)
	rTip := &recorder{}
	cTip.Paint(rTip)
	if !strings.Contains(strings.Join(rTip.texts, "|"), "Summary") {
		t.Errorf("hovered dot tooltip missing 'Summary': %q", rTip.texts)
	}
}
