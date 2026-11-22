package ui

import (
	"runtime"
	"testing"
	"time"

	"github.com/motivated2die/flit/internal/notes"
)

// TestNotesEditorInAWindow drives a real window's message handling: a
// click focuses the editor, characters and keys arrive as Windows sends
// them (a key down and, for Enter, Tab and Backspace, a control character
// too, which must not act twice), a toolbar click keeps the keyboard, Esc
// and a click elsewhere let go of it.
func TestNotesEditorInAWindow(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	w, err := NewWindow("flit notes test")
	if err != nil {
		t.Skip("no window: ", err)
	}
	defer procDestroyWindow.Call(w.hwnd)
	ed := notes.NewEditor(notes.Doc{})
	changes := 0
	w.Build = func() Frame {
		return Frame{Nav: []NavEntry{{ID: "home", Label: "Home", Glyph: GlyphHome}}, Selected: "home", PageID: "p",
			Page: VStack{Gap: 12, Items: []Node{
				Button{ID: "elsewhere", Label: "Elsewhere", OnClick: func() {}},
				Card{Items: []Node{NotesEditor{ID: "notes", Ed: ed, Toolbar: true, MinH: 80, OnChange: func() { changes++ }}}},
			}}}
	}
	relayout := func() {
		cw, ch := w.clientSize()
		c := NewCtx(w.scale, w.metrics, time.Now(), w.hover, w.pressed, w.scroll)
		c.Focus = w.focus
		w.layout(c, float32(cw), float32(ch))
		w.last = c
	}
	at := func(x, y float32) uintptr { return uintptr(uint16(int16(x))) | uintptr(uint16(int16(y)))<<16 }
	click := func(r RectF) {
		w.handle(wmLButtonDown, 0, at(r.L+4, r.T+4))
		w.handle(wmLButtonUp, 0, at(r.L+4, r.T+4))
		relayout()
	}
	key := func(vk uintptr, char uint16) {
		w.handle(wmKeyDown, vk, 0)
		if char != 0 {
			w.handle(wmChar, uintptr(char), 0)
		}
		relayout()
	}
	typeText := func(s string) {
		for _, r := range s {
			w.handle(wmChar, uintptr(r), 0)
		}
		relayout()
	}
	relayout()

	area, ok := w.last.editorByID("notes")
	if !ok {
		t.Fatal("no editor")
	}
	click(area.R)
	if w.focus != "notes" {
		t.Fatalf("a click didn't focus the editor: %q", w.focus)
	}
	typeText("## Plan")
	key(vkReturn, 0x0D) // Enter: a key and a carriage return
	typeText("- one")
	key(vkReturn, 0x0D)
	key(vkTab, 0x09) // indent the new item
	typeText("two")
	key(vkBack, 0x08) // one character, not two
	if got := ed.Doc.Markdown(); got != "## Plan\n\n- one\n  - tw\n" {
		t.Errorf("typed %q", got)
	}
	if changes == 0 {
		t.Error("OnChange never heard")
	}
	// A character outside the BMP comes as two halves.
	w.handle(wmChar, 0xD83D, 0)
	w.handle(wmChar, 0xDC4D, 0) // 👍
	relayout()
	if got := ed.Doc.Blocks[2].Text; got != "tw👍" {
		t.Errorf("surrogates %q", got)
	}
	// The toolbar keeps the keyboard and acts on the caret's line.
	box, _ := w.last.HitByID("notes:tool:task")
	click(box.R)
	if w.focus != "notes" || ed.Doc.Blocks[2].Kind != notes.Task {
		t.Errorf("toolbar: focus %q, kind %v", w.focus, ed.Doc.Blocks[2].Kind)
	}
	// Esc lets go of the keyboard (outside a mention); a click brings it back.
	key(vkEscape, 0x1B)
	if w.focus != "" {
		t.Error("Esc kept the keyboard")
	}
	click(area.R)
	other, _ := w.last.HitByID("elsewhere")
	click(other.R)
	if w.focus != "" {
		t.Error("a click elsewhere kept the keyboard")
	}
	// Characters typed without focus go nowhere.
	before := ed.Doc.Markdown()
	typeText("x")
	if ed.Doc.Markdown() != before {
		t.Error("typed without focus")
	}
}

// A title field followed by the notes: Enter or Tab saves the title and
// gives the notes the keyboard; a field asking for focus starts editing.
func TestTitleThenNotes(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	w, err := NewWindow("flit title test")
	if err != nil {
		t.Skip("no window: ", err)
	}
	defer procDestroyWindow.Call(w.hwnd)
	ed := notes.NewEditor(notes.Doc{})
	title, focusTitle := "", true
	w.Build = func() Frame {
		f := focusTitle
		focusTitle = false
		return Frame{Nav: []NavEntry{{ID: "home", Label: "Home", Glyph: GlyphHome}}, Selected: "home", PageID: "p",
			Page: VStack{Gap: 12, Items: []Node{
				TextField{ID: "title", Title: true, Value: title, Placeholder: "Recording", Focus: f, Next: "notes", OnCommit: func(s string) { title = s }},
				NotesEditor{ID: "notes", Ed: ed, MinH: 80},
			}}}
	}
	w.paint() // the field asks to be edited: editing starts after this pass
	if w.edit == nil || w.edit.ID != "title" {
		t.Fatal("the title isn't being edited")
	}
	for _, r := range "Design review" {
		w.handle(wmChar, uintptr(r), 0)
	}
	w.handle(wmKeyDown, vkReturn, 0)
	w.handle(wmChar, 0x0D, 0) // Enter's carriage return
	w.paint()
	if title != "Design review" || w.edit != nil || w.focus != "notes" {
		t.Fatalf("after Enter: title %q, editing %v, focus %q", title, w.edit != nil, w.focus)
	}
	w.handle(wmChar, 'x', 0)
	if ed.Doc.Blocks[0].Text != "x" {
		t.Errorf("the notes didn't get the keyboard: %q", ed.Doc.Blocks[0].Text)
	}
	// Tab does the same.
	w.startEdit("title", title, 80) // all selected, as after a click
	w.handle(wmKeyDown, vkEnd, 0)
	w.handle(wmChar, '!', 0)
	w.handle(wmKeyDown, vkTab, 0)
	w.handle(wmChar, 0x09, 0)
	if title != "Design review!" || w.focus != "notes" || ed.Doc.Blocks[0].Text != "x" {
		t.Errorf("after Tab: title %q, focus %q, notes %q", title, w.focus, ed.Doc.Blocks[0].Text)
	}
}
