package ui

import (
	"runtime"
	"testing"
	"time"
	"unsafe"

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

// The main window can be resized to any width while keeping the page content
// and docked audio player centred at pageMaxW inside the main area.
func TestWindowUnrestrictedWidthAndCenteredContent(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	w, err := NewWindow("flit width test")
	if err != nil {
		t.Skip("no window: ", err)
	}
	defer procDestroyWindow.Call(w.hwnd)
	var mmi struct {
		Reserved, MaxSize, MaxPos, MinTrack, MaxTrack point
	}
	mmi.MaxTrack, mmi.MaxSize = point{99999, 99999}, point{99999, 99999}
	windowProc(w.hwnd, wmGetMinMaxInfo, 0, uintptr(unsafe.Pointer(&mmi)))
	if mmi.MaxTrack.X != 99999 || mmi.MaxSize.X != 99999 {
		t.Fatalf("window max width restricted: MaxTrack=%+v MaxSize=%+v", mmi.MaxTrack, mmi.MaxSize)
	}

	// On a wide 1600 px frame (scale 1), page content (720 px) and dock are
	// centred inside the main area [208..1600] (equal left/right margins).
	c := NewCtx(1, fakeMetrics{}, time.Now(), "", "", nil)
	f := Frame{
		Page:   Button{ID: "p:btn", Label: "Action", OnClick: func() {}},
		PageID: "p",
		Dock:   Button{ID: "d:btn", Label: "Play", OnClick: func() {}},
		DockID: "d",
	}
	f.Layout(c, 1600, 800)
	wantColX := float32(sidebarW) + float32(1600-sidebarW-pageMaxW)/2
	btn, ok := c.HitByID("p:btn")
	if !ok || btn.R.L != wantColX {
		t.Fatalf("page button L=%v, want centred colX=%v", btn.R.L, wantColX)
	}
	dockBtn, ok := c.HitByID("d:btn")
	if !ok || dockBtn.R.L != wantColX {
		t.Fatalf("dock button L=%v, want centred colX=%v", dockBtn.R.L, wantColX)
	}

	// Mouse browser Back button (WM_XBUTTONUP XBUTTON1 and WM_APPCOMMAND) fires OnBack.
	backs := 0
	w.OnBack = func() { backs++ }
	windowProc(w.hwnd, wmXButtonUp, uintptr(xButton1)<<16, 0)
	windowProc(w.hwnd, wmAppCommand, 0, uintptr(appCommandBrowserBack)<<16)
	if backs != 2 {
		t.Fatalf("OnBack fired %d times, want 2", backs)
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

// Tab and Shift+Tab jump forward and backward between linked TextFields
// (such as speaker name fields), committing each field before moving.
func TestTextFieldTabNavigation(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	w, err := NewWindow("flit tab nav test")
	if err != nil {
		t.Skip("no window: ", err)
	}
	defer procDestroyWindow.Call(w.hwnd)
	names := []string{"", "", ""}
	w.Build = func() Frame {
		return Frame{Nav: []NavEntry{{ID: "home", Label: "Home", Glyph: GlyphHome}}, Selected: "home", PageID: "p",
			Page: VStack{Gap: 8, Items: []Node{
				TextField{ID: "sp:0", Value: names[0], TabNext: "sp:1", OnCommit: func(s string) { names[0] = s }},
				TextField{ID: "sp:1", Value: names[1], TabPrev: "sp:0", TabNext: "sp:2", OnCommit: func(s string) { names[1] = s }},
				TextField{ID: "sp:2", Value: names[2], TabPrev: "sp:1", OnCommit: func(s string) { names[2] = s }},
			}}}
	}
	w.paint()
	w.startEdit("sp:0", names[0], 60)
	for _, r := range "Alice" {
		w.handle(wmChar, uintptr(r), 0)
	}
	w.commitTab(false)
	w.paint()
	if names[0] != "Alice" || w.edit == nil || w.edit.ID != "sp:1" {
		t.Fatalf("after Tab 0->1: names=%v edit=%+v", names, w.edit)
	}
	for _, r := range "Bob" {
		w.handle(wmChar, uintptr(r), 0)
	}
	w.commitTab(false)
	w.paint()
	if names[1] != "Bob" || w.edit == nil || w.edit.ID != "sp:2" {
		t.Fatalf("after Tab 1->2: names=%v edit=%+v", names, w.edit)
	}
	for _, r := range "Carol" {
		w.handle(wmChar, uintptr(r), 0)
	}
	w.commitTab(true) // Shift+Tab back to sp:1
	w.paint()
	if names[2] != "Carol" || w.edit == nil || w.edit.ID != "sp:1" || string(w.edit.Text) != "Bob" {
		t.Fatalf("after Shift+Tab 2->1: names=%v edit=%+v", names, w.edit)
	}
	w.commitTab(true) // Shift+Tab back to sp:0
	w.paint()
	if w.edit == nil || w.edit.ID != "sp:0" || string(w.edit.Text) != "Alice" {
		t.Fatalf("after Shift+Tab 1->0: edit=%+v", w.edit)
	}
}

// Clicking inside a ToolbarOnFocus editor (Summary) expands its container
// upward by compensating page scroll so the text beneath does not shift on
// screen, and collapsing on blur restores the scroll cleanly.
func TestSummaryUpwardExpansionNoTextShift(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	w, err := NewWindow("flit summary expand test")
	if err != nil {
		t.Skip("no window: ", err)
	}
	defer procDestroyWindow.Call(w.hwnd)
	ed := notes.NewEditor(notes.Parse("## Summary\n\nKey decision made.\n"))
	w.Build = func() Frame {
		return Frame{
			Nav: []NavEntry{{ID: "home", Label: "Home", Glyph: GlyphHome}}, Selected: "home", PageID: "rec",
			Page: VStack{Gap: 12, Items: []Node{
				Space(180),
				Caption("Summary"),
				Card{Items: []Node{NotesEditor{ID: "sum", Ed: ed, Toolbar: true, ToolbarOnFocus: true, MinH: 80}}},
				Space(800),
			}},
		}
	}
	sid := FrameScrollID("rec")
	w.scroll[sid] = 60
	w.paint()
	before, ok := w.last.editorByID("sum")
	if !ok {
		t.Fatal("missing summary editor")
	}
	// Click inside the summary text.
	at := func(x, y float32) uintptr { return uintptr(uint16(int16(x))) | uintptr(uint16(int16(y)))<<16 }
	w.handle(wmLButtonDown, 0, at(before.R.L+6, before.R.T+6))
	w.handle(wmLButtonUp, 0, at(before.R.L+6, before.R.T+6))
	w.paint()
	if w.focus != "sum" {
		t.Fatalf("expected focus on sum, got %q", w.focus)
	}
	after, _ := w.last.editorByID("sum")
	if after.R.T != before.R.T {
		t.Errorf("summary text shifted on focus: before=%v after=%v", before.R.T, after.R.T)
	}
	if bar, ok := w.last.HitByID("sum:bar"); !ok || bar.R.B >= after.R.T {
		t.Errorf("toolbar not above summary text inside container: ok=%v bar=%+v textT=%v", ok, bar.R, after.R.T)
	}
	if w.scroll[sid] != 60+before.ExpandTop {
		t.Errorf("scroll = %v, want %v", w.scroll[sid], 60+before.ExpandTop)
	}
	// Esc collapses the toolbar and keeps the text at the exact same screen Y.
	w.handle(wmKeyDown, vkEscape, 0)
	w.paint()
	if w.focus != "" {
		t.Fatalf("Esc did not blur: %q", w.focus)
	}
	afterBlur, _ := w.last.editorByID("sum")
	if afterBlur.R.T != before.R.T {
		t.Errorf("summary text shifted on blur: before=%v afterBlur=%v", before.R.T, afterBlur.R.T)
	}
	if w.scroll[sid] != 60 {
		t.Errorf("scroll after blur = %v, want 60", w.scroll[sid])
	}
}
