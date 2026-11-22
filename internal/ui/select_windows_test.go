package ui

import (
	"runtime"
	"testing"
	"time"
)

// TestSelectingWithThePointer drives a real window's message handling with
// pointer messages over transcript paragraphs: press, drag and release
// across two of them; double click a word; triple click a paragraph.
func TestSelectingWithThePointer(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	w, err := NewWindow("flit selection test")
	if err != nil {
		t.Skip("no window: ", err)
	}
	defer procDestroyWindow.Call(w.hwnd)
	texts := []string{
		"Hallo zusammen, lasst uns mit dem Release-Plan anfangen.",
		"Gerne. Ich habe gestern noch die Zahlen aktualisiert.",
		"Von meiner Seite gibt es noch zwei offene Punkte.",
	}
	var blocks []Node
	for i, s := range texts {
		blocks = append(blocks, TranscriptBlock{ID: "p" + string(rune('0'+i)), Speaker: "Anna", Text: s, OnPlay: func() {}, OnCopy: func() {}})
	}
	w.Build = func() Frame {
		return Frame{Nav: []NavEntry{{ID: "home", Label: "Home", Glyph: GlyphHome}}, Selected: "home", PageID: "p", Page: VStack{Items: blocks}}
	}
	relayout := func() {
		cw, ch := w.clientSize()
		c := NewCtx(w.scale, w.metrics, time.Now(), w.hover, w.pressed, w.scroll)
		c.Sel = w.sel
		w.layout(c, float32(cw), float32(ch))
		w.last = c
	}
	relayout()
	at := func(x, y float32) uintptr { return uintptr(uint16(int16(x))) | uintptr(uint16(int16(y)))<<16 }
	text := func(i int) RectF {
		h, ok := w.last.HitByID("p" + string(rune('0'+i)) + ":text")
		if !ok {
			t.Fatalf("no text hit for paragraph %d", i)
		}
		return h.R
	}
	// What a press on the text finds: the text, not the paragraph's hover
	// area (it was covered once, which broke selecting entirely).
	r0 := text(0)
	if h, _ := w.last.HitAt(r0.L+20, (r0.T+r0.B)/2); !h.text {
		t.Fatalf("a press on the text hits %q", h.ID)
	}

	// Press in the first paragraph, drag into the second, let go.
	w.handle(wmLButtonDown, 0, at(r0.L+2, (r0.T+r0.B)/2))
	if !w.selecting || w.sel == nil {
		t.Fatal("pressing on text doesn't start a selection")
	}
	r1 := text(1)
	w.handle(wmMouseMove, 0, at(r1.L+60, (r1.T+r1.B)/2))
	relayout()
	w.handle(wmLButtonUp, 0, at(r1.L+60, (r1.T+r1.B)/2))
	if w.selecting || w.sel == nil {
		t.Fatalf("after the drag: selecting %v, selection %v", w.selecting, w.sel)
	}
	got := w.last.SelectedText(*w.sel)
	if len(got) < len(texts[0]) || got[:5] != "Hallo" || got[len(texts[0]):len(texts[0])+2] != "\n\n" {
		t.Fatalf("dragged across two paragraphs: %q", got)
	}

	// A click elsewhere on text lets go; a double click selects a word.
	time.Sleep(10 * time.Millisecond)
	r2 := text(2)
	word := r2.L + 30 // inside "meiner"
	y2 := (r2.T + r2.B) / 2
	w.clickAt = time.Time{} // a fresh click
	for range 2 {
		w.handle(wmLButtonDown, 0, at(word, y2))
		w.handle(wmLButtonUp, 0, at(word, y2))
	}
	relayout()
	if w.sel == nil {
		t.Fatal("double click: nothing selected")
	}
	if got := w.last.SelectedText(*w.sel); got != "meiner" {
		t.Fatalf("double click selected %q", got)
	}
	// A third click: the whole paragraph.
	w.handle(wmLButtonDown, 0, at(word, y2))
	w.handle(wmLButtonUp, 0, at(word, y2))
	relayout()
	if w.sel == nil || w.last.SelectedText(*w.sel) != texts[2] {
		t.Fatalf("triple click selected %v", w.sel)
	}
	// Esc lets go.
	w.handle(wmKeyDown, vkEscape, 0)
	if w.sel != nil {
		t.Fatal("Esc kept the selection")
	}
}

// TestSelectingScrollsAtTheEdge: held below the view while selecting, the
// page scrolls, and the selection follows what comes under the pointer.
func TestSelectingScrollsAtTheEdge(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	w, err := NewWindow("flit selection scroll test")
	if err != nil {
		t.Skip("no window: ", err)
	}
	defer procDestroyWindow.Call(w.hwnd)
	var blocks []Node
	for i := range 40 {
		blocks = append(blocks, TranscriptBlock{ID: "p" + string(rune('A'+i)), Speaker: "Anna", Text: "Ein Absatz mit ein paar Worten darin, damit die Seite lang wird."})
	}
	w.Build = func() Frame {
		return Frame{Nav: []NavEntry{{ID: "home", Label: "Home", Glyph: GlyphHome}}, Selected: "home", PageID: "p", Page: VStack{Items: blocks}}
	}
	relayout := func() {
		cw, ch := w.clientSize()
		c := NewCtx(w.scale, w.metrics, time.Now(), "", "", w.scroll)
		c.Sel = w.sel
		w.layout(c, float32(cw), float32(ch))
		w.last = c
	}
	relayout()
	at := func(x, y float32) uintptr { return uintptr(uint16(int16(x))) | uintptr(uint16(int16(y)))<<16 }
	h, _ := w.last.HitByID("pA:text")
	w.handle(wmLButtonDown, 0, at(h.R.L+2, (h.R.T+h.R.B)/2))
	_, ch := w.clientSize()
	w.handle(wmMouseMove, 0, at(h.R.L+40, float32(ch)+40)) // held below the window
	before := w.scroll[FrameScrollID("p")]
	focus := w.sel.FocusArea
	for range 20 {
		w.scrollWhileSelecting()
		relayout()
		w.selectAt(w.selX, w.selY)
	}
	if got := w.scroll[FrameScrollID("p")]; got <= before {
		t.Fatalf("the page didn't scroll: %v", got)
	}
	if w.sel.FocusArea <= focus {
		t.Fatalf("the selection stayed at paragraph %d", w.sel.FocusArea)
	}
	w.handle(wmLButtonUp, 0, at(h.R.L+40, float32(ch)+40))
}
