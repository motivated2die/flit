package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/motivated2die/flit/internal/notes"
)

// layoutEditor lays an editor out at (0, 0), 400 px wide, focused or not.
func layoutEditor(ed *notes.Editor, focus bool) (*Ctx, float32) {
	c := NewCtx(1, fakeMetrics{}, time.Now(), "", "", nil)
	if focus {
		c.Focus = "notes"
	}
	h := NotesEditor{ID: "notes", Ed: ed, Placeholder: "Notes"}.Layout(c, 0, 0, 400)
	return c, h
}

func env() *EditorEnv {
	goal := float32(-1)
	return &EditorEnv{GoalX: &goal, Blur: func() {}}
}

func TestNotesEditorTyping(t *testing.T) {
	ed := notes.NewEditor(notes.Doc{})
	c, _ := layoutEditor(ed, true)
	tg, ok := c.editorByID("notes")
	if !ok {
		t.Fatal("no editor target")
	}
	e := env()
	for _, r := range "## Plan" {
		tg.Char(r, e)
	}
	tg.Key(vkReturn, Mods{}, e)
	for _, r := range "- one" {
		tg.Char(r, e)
	}
	if got := ed.Doc.Markdown(); got != "## Plan\n\n- one\n" {
		t.Errorf("typed %q", got)
	}
	// Up goes to the heading line, Down back.
	c, _ = layoutEditor(ed, true)
	tg, _ = c.editorByID("notes")
	tg.Key(vkUp, Mods{}, e)
	if ed.Caret.B != 0 {
		t.Errorf("up: caret %v", ed.Caret)
	}
	tg.Key(vkDown, Mods{}, e)
	if ed.Caret.B != 1 {
		t.Errorf("down: caret %v", ed.Caret)
	}
	// AltGr (Ctrl+Alt) isn't a shortcut: Ctrl+Alt+B doesn't make bold.
	if tg.Key('B', Mods{Ctrl: true, Alt: true}, e) {
		t.Error("AltGr+B taken as a shortcut")
	}
}

func TestNotesEditorClickAndBox(t *testing.T) {
	ed := notes.NewEditor(notes.Parse("first line\n\n- [ ] a task\n"))
	c, _ := layoutEditor(ed, true)
	tg, _ := c.editorByID("notes")
	tg.Press(2, 2, false, 1) // the first line's start
	if ed.Caret != (notes.Pos{B: 0, O: 0}) {
		t.Errorf("press: caret %v", ed.Caret)
	}
	tg.Press(2, 2, false, 2) // a double click selects the word
	if a, b := ed.Sel(); a.O != 0 || b.O != len("first") {
		t.Errorf("double click: %v..%v", a, b)
	}
	box, ok := c.HitByID("notes:box:1")
	if !ok {
		t.Fatal("no checkbox")
	}
	box.OnClick()
	if !ed.Doc.Blocks[1].Done {
		t.Error("the box didn't tick the task")
	}
	// The checkbox lies inside the editor's area and is on top of it.
	if h, _ := c.HitAt(box.R.L+2, box.R.T+2); h.ID != "notes:box:1" {
		t.Errorf("hit at the box: %q", h.ID)
	}
}

func TestNotesEditorPlaceholderAndFocus(t *testing.T) {
	ed := notes.NewEditor(notes.Doc{})
	c := NewCtx(1, fakeMetrics{}, time.Now(), "", "", nil)
	NotesEditor{ID: "notes", Ed: ed, Placeholder: "Type notes", Focus: true, MinH: 120}.Layout(c, 0, 0, 300)
	tg, _ := c.editorByID("notes")
	if !tg.WantFocus {
		t.Error("focus not asked for")
	}
	r := &recorder{}
	c.Paint(r)
	if !strings.Contains(strings.Join(r.texts, "|"), "Type notes") {
		t.Errorf("no placeholder: %q", r.texts)
	}
	if tg.R.H() < 120 {
		t.Errorf("height %v, want at least 120", tg.R.H())
	}
}

func TestNotesEditorKeepsCaretInView(t *testing.T) {
	ed := notes.NewEditor(notes.Doc{})
	c, _ := layoutEditor(ed, true)
	tg, _ := c.editorByID("notes")
	tg.Char('x', env())
	c, _ = layoutEditor(ed, true)
	if c.keepView == nil {
		t.Error("typing didn't ask to keep the caret in view")
	}
	if c, _ = layoutEditor(ed, true); c.keepView != nil {
		t.Error("kept asking without a change")
	}
}

func TestCFHTML(t *testing.T) {
	s := cfHTML("<h2>A</h2>")
	at := func(key string) int {
		i := strings.Index(s, key)
		n := 0
		for _, c := range s[i+len(key) : i+len(key)+10] {
			n = n*10 + int(c-'0')
		}
		return n
	}
	if got := s[at("StartFragment:"):at("EndFragment:")]; got != "<h2>A</h2>" {
		t.Errorf("fragment %q", got)
	}
	if !strings.HasPrefix(s[at("StartHTML:"):], "<html>") || at("EndHTML:") != len(s) {
		t.Errorf("document offsets in %q", s)
	}
}

func TestNotesEditorToolbarSwimLaneAndFocus(t *testing.T) {
	ed := notes.NewEditor(notes.Parse("Summary body.\n"))

	// Always-on toolbar: full-width swim lane at top of editor.
	c1 := NewCtx(1, fakeMetrics{}, time.Now(), "", "", nil)
	NotesEditor{ID: "notes", Ed: ed, Toolbar: true}.Layout(c1, 16, 100, 400)
	bar, ok := c1.HitByID("notes:bar")
	if !ok {
		t.Fatal("missing toolbar swim lane")
	}
	if bar.R.L != 16 || bar.R.R != 416 {
		t.Errorf("swim lane x = [%v..%v], want [16..416]", bar.R.L, bar.R.R)
	}
	if _, ok := c1.HitByID("notes:tool:h2"); !ok {
		t.Error("missing toolbar button in always-on swim lane")
	}

	// ToolbarOnFocus: hidden while unfocused; expands inside the container on
	// focus and reports ExpandTop so the host compensates scroll with zero text shift.
	cOff := NewCtx(1, fakeMetrics{}, time.Now(), "", "", nil)
	hOff := NotesEditor{ID: "sum", Ed: ed, Toolbar: true, ToolbarOnFocus: true}.Layout(cOff, 16, 100, 400)
	if _, ok := cOff.HitByID("sum:bar"); ok {
		t.Error("toolbar shown while unfocused")
	}
	tgOff, _ := cOff.editorByID("sum")
	if tgOff.ExpandTop != 48 {
		t.Errorf("ExpandTop = %v, want 48", tgOff.ExpandTop)
	}

	cOn := NewCtx(1, fakeMetrics{}, time.Now(), "", "", nil)
	cOn.Focus = "sum"
	hOn := NotesEditor{ID: "sum", Ed: ed, Toolbar: true, ToolbarOnFocus: true}.Layout(cOn, 16, 100, 400)
	if hOn != hOff+48 {
		t.Errorf("height with toolbar inside container = %v, want %v", hOn, hOff+48)
	}
	tgOn, _ := cOn.editorByID("sum")
	if tgOn.R.T != tgOff.R.T+48 {
		t.Errorf("text top inside container = %v, want %v", tgOn.R.T, tgOff.R.T+48)
	}
	sumBar, ok := cOn.HitByID("sum:bar")
	if !ok {
		t.Fatal("toolbar did not appear on focus")
	}
	if sumBar.R.T != 100 || sumBar.R.B != 138 {
		t.Errorf("focus toolbar not inside container at [100..138]: R=%+v", sumBar.R)
	}
	if _, ok := cOn.HitByID("sum:tool:bold"); !ok {
		t.Error("missing bold button in focus toolbar")
	}

	// Sticky docking: when scrolled past the top of the clip rect (y=32),
	// the toolbar docks 8 px below the clip top (y=40) and stays within the card.
	tall := notes.NewEditor(notes.Parse(strings.Repeat("Line of notes.\n\n", 20)))
	cScroll := NewCtx(1, fakeMetrics{}, time.Now(), "", "", nil)
	cScroll.PushClip(RectF{0, 32, 500, 300})
	NotesEditor{ID: "notes", Ed: tall, Toolbar: true}.Layout(cScroll, 16, -120, 400)
	cScroll.PopClip()
	h2, ok := cScroll.HitByID("notes:tool:h2")
	if !ok {
		t.Fatal("docked toolbar button missing")
	}
	// Lane top = clipTop(32) + 8 = 40; 30 px button centered in 38 px lane -> T = 44.
	if h2.R.T != 44 {
		t.Errorf("docked h2 button top = %v, want 44", h2.R.T)
	}
}

func TestNotesEditorMentionTab(t *testing.T) {
	ed := notes.NewEditor(notes.Doc{})
	c, _ := layoutEditor(ed, true)
	tg, _ := c.editorByID("notes")
	e := env()
	tg.Char('@', e)

	c, _ = layoutEditor(ed, true)
	r := &recorder{}
	c.Paint(r)
	if !strings.Contains(strings.Join(r.texts, "|"), "flit") {
		t.Fatalf("expected ghost suggestion 'flit' in %q", r.texts)
	}

	// Tab accepts the suggestion and opens an @flit mention.
	tg, _ = c.editorByID("notes")
	tg.Key(vkTab, Mods{}, e)
	if got := ed.Doc.Blocks[0].Text; got != "[@flit: ]" {
		t.Fatalf("after Tab on @: %q", got)
	}

	// Typing something else dismisses the suggestion.
	ed2 := notes.NewEditor(notes.Doc{})
	c2, _ := layoutEditor(ed2, true)
	tg2, _ := c2.editorByID("notes")
	tg2.Char('@', e)
	tg2.Char('x', e)
	c2, _ = layoutEditor(ed2, true)
	r2 := &recorder{}
	c2.Paint(r2)
	if strings.Contains(strings.Join(r2.texts, "|"), "flit") {
		t.Fatalf("suggestion should disappear after @x: %q", r2.texts)
	}
}

func TestNotesEditorPinAndDragHandle(t *testing.T) {
	ed := notes.NewEditor(notes.Parse("Pinned note.\n"))
	pinnedClicks := 0

	// Unpinned: shows pin button on the right, no drag handle.
	c1 := NewCtx(1, fakeMetrics{}, time.Now(), "", "", nil)
	NotesEditor{ID: "notes", Ed: ed, Toolbar: true, OnPin: func() { pinnedClicks++ }}.Layout(c1, 16, 20, 400)
	pin, ok := c1.HitByID("notes:tool:pin")
	if !ok {
		t.Fatal("missing pin button")
	}
	if pin.R.R != 416-4 {
		t.Errorf("pin button right = %v, want %v", pin.R.R, 412)
	}
	if _, ok := c1.HitByID("notes:drag"); ok {
		t.Error("unpinned editor should not show drag handle")
	}
	pin.OnClick()
	if pinnedClicks != 1 {
		t.Errorf("pin click count = %d, want 1", pinnedClicks)
	}

	// Pinned: shows 6-dot drag handle directly to the left of the pin button.
	c2 := NewCtx(1, fakeMetrics{}, time.Now(), "", "", nil)
	NotesEditor{ID: "float", Ed: ed, Toolbar: true, Pinned: true, OnPin: func() {}}.Layout(c2, 12, 12, 396)
	fpin, ok := c2.HitByID("float:tool:pin")
	if !ok {
		t.Fatal("missing pinned button")
	}
	drag, ok := c2.HitByID("float:drag")
	if !ok {
		t.Fatal("missing 6-dot drag handle in pinned mode")
	}
	if drag.R.R >= fpin.R.L {
		t.Errorf("drag handle (%+v) should sit to the left of pin button (%+v)", drag.R, fpin.R)
	}
}

func TestNotesEditorVerticalBeforeRepaint(t *testing.T) {
	// Regression: keys can arrive faster than WM_PAINT rebuilds geoms.
	// Enter adds a block so ed.Caret.B == len(geoms); pressing Down/Up/PgDn/PgUp
	// on the stale layout must not panic with index out of range.
	ed := notes.NewEditor(notes.Parse("line one\n\nline two\n"))
	c, _ := layoutEditor(ed, true)
	tg, _ := c.editorByID("notes")
	e := env()

	// Add one block without re-laying out, then move Down and Up.
	tg.Key(vkReturn, Mods{}, e)
	if ed.Caret.B != 2 {
		t.Fatalf("after Enter: caret %v, want B=2", ed.Caret)
	}
	tg.Key(vkDown, Mods{}, e)
	if ed.Caret.B != 2 {
		t.Errorf("Down on new block: caret %v, want B=2", ed.Caret)
	}
	tg.Key(vkUp, Mods{}, e)
	if ed.Caret.B != 1 {
		t.Errorf("Up from new block: caret %v, want B=1", ed.Caret)
	}

	// Add two blocks without re-laying out, then step Up once, then PageUp.
	tg.Key(vkEnd, Mods{Ctrl: true}, e)
	tg.Key(vkReturn, Mods{}, e)
	tg.Key(vkReturn, Mods{}, e)
	tg.Key(vkUp, Mods{}, e)
	if ed.Caret.B != 3 {
		t.Errorf("Up from second unlaid block: caret %v, want B=3", ed.Caret)
	}
	tg.Key(vkPrior, Mods{}, e)
	if ed.Caret.B != 0 {
		t.Errorf("PageUp from unlaid block: caret %v, want B=0", ed.Caret)
	}

	// Shrink the document without re-laying out (Backspace merges blocks), then Down.
	c, _ = layoutEditor(ed, true)
	tg, _ = c.editorByID("notes")
	tg.Key(vkEnd, Mods{Ctrl: true}, e)
	tg.Key(vkBack, Mods{}, e)
	tg.Key(vkBack, Mods{}, e)
	tg.Key(vkDown, Mods{}, e)
	if ed.Caret.B >= len(ed.Doc.Blocks) {
		t.Errorf("Down after shrink: caret %v out of %d blocks", ed.Caret, len(ed.Doc.Blocks))
	}
}
