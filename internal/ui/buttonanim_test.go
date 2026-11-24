package ui

import (
	"testing"
	"time"
)

func TestButtonMorphWidthIconSlideAndPrune(t *testing.T) {
	var anims ButtonAnims
	now := time.Now()

	// Frame 0: icon-only cancel button snaps immediately with no animation.
	bIcon := Button{ID: "home:cancel", Glyph: GlyphDelete, Large: true}
	c0 := NewCtx(1, fakeMetrics{}, now, "", "", nil)
	c0.Buttons = &anims
	w0 := bIcon.Width(c0)
	bIcon.Layout(c0, 0, 0, w0)
	anims.prune()
	if c0.AnimatingFast {
		t.Fatalf("initial button render should snap, not animate")
	}
	if w0 != 40 {
		t.Fatalf("icon-only Large button width = %v, want 40", w0)
	}

	// Frame 1 (after 16 ms): morph to "Discard recording" (Danger).
	bExpanded := Button{ID: "home:cancel", Label: "Discard recording", Glyph: GlyphDelete, Danger: true, Large: true}
	targetW := bExpanded.naturalWidth(c0)
	now = now.Add(16 * time.Millisecond)
	c1 := NewCtx(1, fakeMetrics{}, now, "", "", nil)
	c1.Buttons = &anims
	w1 := bExpanded.Width(c1)
	bExpanded.Layout(c1, 0, 0, w1)
	anims.prune()
	if !c1.AnimatingFast {
		t.Fatalf("expected AnimatingFast=true while morphing width/label")
	}
	if w1 <= w0 || w1 >= targetW {
		t.Fatalf("mid-morph width = %v, want strictly between %v and %v", w1, w0, targetW)
	}

	// Step forward at 144 Hz until settled (~140 ms).
	settled := false
	for i := 0; i < 60; i++ {
		now = now.Add(7 * time.Millisecond)
		c := NewCtx(1, fakeMetrics{}, now, "", "", nil)
		c.Buttons = &anims
		w := bExpanded.Width(c)
		bExpanded.Layout(c, 0, 0, w)
		anims.prune()
		if !c.AnimatingFast {
			if w != targetW {
				t.Fatalf("settled width = %v, want %v", w, targetW)
			}
			settled = true
			break
		}
	}
	if !settled {
		t.Fatalf("button morph did not settle within 420 ms")
	}

	// Morph across distinct IDs sharing a MorphKey (Start recording -> Transcribe).
	bStart := Button{ID: "home:start", MorphKey: "home:primary", Label: "Start recording", Glyph: GlyphRecord, Primary: true, Large: true}
	bStop := Button{ID: "home:stop", MorphKey: "home:primary", Label: "Transcribe", Glyph: GlyphStop, Primary: true, Large: true}
	cA := NewCtx(1, fakeMetrics{}, now, "", "", nil)
	cA.Buttons = &anims
	wStart := bStart.Width(cA)
	bStart.Layout(cA, 0, 0, wStart)
	anims.prune()
	// home:cancel was omitted in cA, so prune must drop it.
	if _, ok := anims.items["home:cancel"]; ok {
		t.Fatalf("expected unseen button home:cancel to be pruned")
	}

	now = now.Add(14 * time.Millisecond)
	cB := NewCtx(1, fakeMetrics{}, now, "", "", nil)
	cB.Buttons = &anims
	wMid := bStop.Width(cB)
	bStop.Layout(cB, 0, 0, wMid)
	anims.prune()
	wStopTarget := bStop.naturalWidth(cB)
	if !cB.AnimatingFast {
		t.Fatalf("expected MorphKey transition to animate")
	}
	if wMid >= wStart || wMid <= wStopTarget {
		t.Fatalf("shrink morph width = %v, want between %v and %v", wMid, wStopTarget, wStart)
	}
}

func TestSplitActionThreeStatesAndFixedWidth(t *testing.T) {
	var anims ButtonAnims
	now := time.Now()
	makeSplit := func(st SplitState) SplitAction {
		return SplitAction{
			ID: "home:record-split", State: st,
			IdleID: "home:start", IdleLabel: "Start recording", IdleGlyph: GlyphRecord, OnIdle: func() {},
			LeftID: "home:cancel", LeftGlyph: GlyphDelete, ConfirmLabel: "Discard recording", OnLeft: func() {}, OnConfirm: func() {},
			RightID: "home:stop", RightLabel: "Transcribe", RightGlyph: GlyphStop, OnRight: func() {},
		}
	}

	// All three states must have identical fixed width.
	cProbe := NewCtx(1, fakeMetrics{}, now, "", "", nil)
	wIdle := makeSplit(SplitIdle).Width(cProbe)
	wLive := makeSplit(SplitLive).Width(cProbe)
	wConf := makeSplit(SplitConfirm).Width(cProbe)
	if wIdle != wLive || wLive != wConf || wIdle != splitActionW {
		t.Fatalf("widths differ across states: idle=%v live=%v confirm=%v want %v", wIdle, wLive, wConf, float32(splitActionW))
	}

	// Step Idle -> Live -> Confirm -> Live and verify each transition animates and settles cleanly.
	stepUntilSettled := func(st SplitState, expectAnim bool) {
		t.Helper()
		b := makeSplit(st)
		sawAnim := false
		for i := 0; i < 60; i++ {
			now = now.Add(7 * time.Millisecond)
			c := NewCtx(1, fakeMetrics{}, now, "", "", nil)
			c.Buttons = &anims
			b.Layout(c, 0, 0, b.Width(c))
			anims.prune()
			if c.AnimatingFast {
				sawAnim = true
			} else if i > 0 {
				if expectAnim && !sawAnim {
					t.Fatalf("state %v never set AnimatingFast", st)
				}
				return
			}
		}
		t.Fatalf("state %v did not settle", st)
	}

	// Initial Idle snaps immediately.
	c0 := NewCtx(1, fakeMetrics{}, now, "", "", nil)
	c0.Buttons = &anims
	b0 := makeSplit(SplitIdle)
	b0.Layout(c0, 0, 0, b0.Width(c0))
	anims.prune()
	if c0.AnimatingFast {
		t.Fatalf("initial SplitIdle should snap")
	}

	stepUntilSettled(SplitLive, true)
	stepUntilSettled(SplitConfirm, true)
	stepUntilSettled(SplitLive, true)
	stepUntilSettled(SplitIdle, true)
}
