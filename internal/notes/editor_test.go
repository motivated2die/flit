package notes

import (
	"reflect"
	"testing"
	"time"
)

// typeKeys types s key by key: "\n" is Enter, "\t" Tab, "\b" Backspace.
func typeKeys(e *Editor, s string) {
	for _, r := range s {
		switch r {
		case '\n':
			e.Enter()
		case '\t':
			e.Indent(1)
		case '\b':
			e.Backspace(false)
		default:
			e.Type(string(r))
		}
	}
}

func newEd() *Editor {
	e := NewEditor(Doc{})
	at := time.Unix(0, 0)
	e.now = func() time.Time { at = at.Add(100 * time.Millisecond); return at }
	return e
}

func TestTypingShortcuts(t *testing.T) {
	e := newEd()
	typeKeys(e, "## Agenda\n### Detail\nText\n- one\n- two\n\nAfter\n1. first\nsecond\n[] task\n- [x] done\n* star\n[ ] made a task")
	want := []string{"h2 Agenda", "h3 Detail", "p Text", "b0 one", "b0 two", "p After", "n0 first", "n0 second", "t0 task", "x0 done", "b0 star", "t0 made a task"}
	if got := kinds(e.Doc); !reflect.DeepEqual(got, want) {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestShortcutOnlyAtStart(t *testing.T) {
	e := newEd()
	typeKeys(e, "a - b ## c")
	if got := kinds(e.Doc); !reflect.DeepEqual(got, []string{"p a - b ## c"}) {
		t.Errorf("got %q", got)
	}
}

func TestListEnterAndExit(t *testing.T) {
	e := newEd()
	typeKeys(e, "- a\n\tb\n\nc\n\n\nd")
	// Enter on the empty nested item moves it out a level, then out of the list.
	want := []string{"b0 a", "b1 b", "b0 c", "p ", "p d"}
	if got := kinds(e.Doc); !reflect.DeepEqual(got, want) {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestIndentLimits(t *testing.T) {
	e := newEd()
	typeKeys(e, "- a")
	if e.Indent(1) {
		t.Error("the first item indented under nothing")
	}
	typeKeys(e, "\n\tb\n\tc")
	if got := kinds(e.Doc); !reflect.DeepEqual(got, []string{"b0 a", "b1 b", "b2 c"}) {
		t.Errorf("got %q", got)
	}
	e.Indent(1) // c can't go deeper than b+1
	if e.Doc.Blocks[2].Indent != 2 {
		t.Errorf("indent %d", e.Doc.Blocks[2].Indent)
	}
	e.SelectAll()
	e.Indent(-1)
	if got := kinds(e.Doc); !reflect.DeepEqual(got, []string{"b0 a", "b0 b", "b1 c"}) {
		t.Errorf("outdent all: %q", got)
	}
	e.MoveTo(Pos{0, 0}, false)
	if e.Indent(1); e.Doc.Blocks[0].Indent != 0 {
		t.Error("Tab on a paragraph-like first item")
	}
}

func TestBackspaceAtStart(t *testing.T) {
	e := newEd()
	typeKeys(e, "Intro\n- a\n\tb")
	e.Home(false, false)
	e.Backspace(false) // nested → out a level
	e.Backspace(false) // bullet → paragraph
	e.Backspace(false) // paragraph → joins "a"
	if got := kinds(e.Doc); !reflect.DeepEqual(got, []string{"p Intro", "b0 ab"}) {
		t.Errorf("got %q", got)
	}
	if e.Caret != (Pos{1, 1}) {
		t.Errorf("caret %v", e.Caret)
	}
	e.MoveTo(Pos{0, 0}, false)
	e.Backspace(false) // nothing before the first line
	if len(e.Doc.Blocks) != 2 {
		t.Error("backspace at the doc's start changed it")
	}
}

func TestEnterSplitsAndAbove(t *testing.T) {
	e := newEd()
	typeKeys(e, "## Heading")
	e.MoveTo(Pos{0, 3}, false)
	e.Enter() // a heading splits into a heading and a paragraph
	e.MoveTo(Pos{0, 0}, false)
	e.Enter() // at the start: an empty line above, caret stays with the text
	if got := kinds(e.Doc); !reflect.DeepEqual(got, []string{"p ", "h2 Hea", "p ding"}) {
		t.Errorf("got %q", got)
	}
	if e.Caret != (Pos{1, 0}) {
		t.Errorf("caret %v", e.Caret)
	}
	e2 := newEd()
	typeKeys(e2, "- [x] done task")
	e2.Enter()
	if b := e2.Doc.Blocks[1]; b.Kind != Task || b.Done {
		t.Errorf("after a done task: %+v", b)
	}
}

func TestTasks(t *testing.T) {
	e := newEd()
	typeKeys(e, "buy milk")
	e.ToggleDone(0) // Ctrl+Enter on a paragraph: an open task
	if b := e.Doc.Blocks[0]; b.Kind != Task || b.Done {
		t.Fatalf("%+v", b)
	}
	e.ToggleDone(0)
	if !e.Doc.Blocks[0].Done {
		t.Error("not ticked")
	}
	if md := e.Doc.Markdown(); md != "- [x] buy milk\n" {
		t.Errorf("md %q", md)
	}
}

func TestMentionTyped(t *testing.T) {
	e := newEd()
	clock := 12*time.Minute + 40*time.Second
	e.Clock = func() time.Duration { return clock }
	typeKeys(e, "Budget @flit is it final?\nafter")
	b := e.Doc.Blocks
	if len(b) != 1 || b[0].Text != "Budget [@flit 12:40: is it final?] after" {
		t.Fatalf("got %q", kinds(e.Doc))
	}
	m := Mentions(b[0].Text)
	if len(m) != 1 || m[0].At != clock || m[0].Instruction != "is it final?" {
		t.Errorf("mention %+v", m)
	}
	if b[0].Typed != clock {
		t.Errorf("typed %v", b[0].Typed)
	}
}

func TestMentionWithoutRecording(t *testing.T) {
	e := newEd()
	typeKeys(e, "@flit: check owners]x")
	if got := e.Doc.Blocks[0].Text; got != "[@flit: check owners] x" {
		t.Errorf("got %q", got)
	}
	if e.Doc.Blocks[0].Typed != Unknown {
		t.Error("a time without a recording")
	}
}

func TestMentionEdges(t *testing.T) {
	e := newEd()
	typeKeys(e, "mail@flit ok")
	if got := e.Doc.Blocks[0].Text; got != "mail@flit ok" {
		t.Errorf("mid-word mention: %q", got)
	}
	e = newEd()
	typeKeys(e, "a @flit \n") // empty: Enter removes it
	if got := e.Doc.Blocks[0].Text; got != "a" || len(e.Doc.Blocks) != 1 {
		t.Errorf("empty mention: %q", kinds(e.Doc))
	}
	e = newEd()
	typeKeys(e, "@flit keep this")
	e.MoveTo(Pos{0, len("[@flit: ")}, false)
	e.Backspace(false) // at the instruction's start: unwraps it
	if got := e.Doc.Blocks[0].Text; got != "keep this" {
		t.Errorf("unwrap: %q", got)
	}
	e = newEd()
	typeKeys(e, "@flit x")
	if !e.InMention() || !e.ConfirmMention() || e.InMention() {
		t.Error("Esc should confirm the mention")
	}
}

func TestTypedTimes(t *testing.T) {
	e := newEd()
	now := time.Duration(Unknown)
	e.Clock = func() time.Duration { return now }
	typeKeys(e, "before\n")
	now = 90 * time.Second
	typeKeys(e, "during")
	now = 200 * time.Second
	e.MoveTo(Pos{0, 6}, false)
	typeKeys(e, " more") // the first line keeps having no time? it gets one: first typed during
	if got := []time.Duration{e.Doc.Blocks[0].Typed, e.Doc.Blocks[1].Typed}; !reflect.DeepEqual(got, []time.Duration{200 * time.Second, 90 * time.Second}) {
		t.Errorf("typed %v", got)
	}
	e.MoveTo(Pos{1, 3}, false)
	e.Enter() // the moved half keeps its line's time
	if e.Doc.Blocks[2].Typed != 90*time.Second || e.Doc.Blocks[2].Text != "ing" {
		t.Errorf("split: %+v", e.Doc.Blocks[2])
	}
}

func TestUndoGroupsWords(t *testing.T) {
	e := newEd()
	typeKeys(e, "hello world")
	e.Undo()
	if got := e.Doc.Blocks[0].Text; got != "hello " {
		t.Errorf("after one undo: %q", got)
	}
	e.Undo()
	if got := e.Doc.Blocks[0].Text; got != "" {
		t.Errorf("after two: %q", got)
	}
	e.Redo()
	e.Redo()
	if got := e.Doc.Blocks[0].Text; got != "hello world" {
		t.Errorf("redo: %q", got)
	}
	e.Undo()
	e.Undo()
	if e.Undo() {
		t.Error("undid more than there was")
	}
}

func TestSelectionDeleteAndType(t *testing.T) {
	e := newEd()
	typeKeys(e, "## Title\n- one\n- two\nend")
	e.MoveTo(Pos{1, 1}, false)
	e.MoveTo(Pos{3, 1}, true)
	e.Type("X")
	if got := kinds(e.Doc); !reflect.DeepEqual(got, []string{"h2 Title", "b0 oXnd"}) {
		t.Errorf("got %q", got)
	}
	e.SelectAll()
	e.Backspace(false)
	if got := kinds(e.Doc); !reflect.DeepEqual(got, []string{"p "}) {
		t.Errorf("all deleted: %q", got)
	}
}

func TestSelectedAndPaste(t *testing.T) {
	e := newEd()
	typeKeys(e, "- a\n\tb\n\tc\nd")
	e.MoveTo(Pos{1, 0}, false)
	e.MoveTo(Pos{2, 1}, true)
	if md := e.Selected().Markdown(); md != "- b\n  - c\n" { // c is nested under b
		t.Errorf("selected %q", md)
	}
	e.MoveTo(Pos{3, 0}, false)
	e.MoveTo(Pos{3, 1}, true)
	if md := e.Selected().Markdown(); md != "- d\n" { // a whole line keeps its kind
		t.Errorf("a whole line %q", md)
	}
	e.MoveTo(Pos{0, 0}, false)
	e.MoveTo(Pos{0, 1}, true)
	e.Type("Ab")
	e.MoveTo(Pos{0, 1}, false)
	e.MoveTo(Pos{0, 2}, true)
	if md := e.Selected().Markdown(); md != "b\n" { // part of one: just its text
		t.Errorf("part of a line %q", md)
	}

	p := newEd()
	typeKeys(p, "start end")
	p.MoveTo(Pos{0, 6}, false)
	p.Paste("one\n\n## Two\n- three\n")
	want := []string{"p start one", "h2 Two", "b0 threeend"}
	if got := kinds(p.Doc); !reflect.DeepEqual(got, want) {
		t.Errorf("paste: %q", got)
	}
	if p.Caret != (Pos{2, len("three")}) {
		t.Errorf("caret %v", p.Caret)
	}
	q := newEd()
	q.Paste("- [ ] task one\n- [ ] task two")
	if got := kinds(q.Doc); !reflect.DeepEqual(got, []string{"t0 task one", "t0 task two"}) {
		t.Errorf("into an empty doc: %q", got)
	}
	q.Paste(" plain words")
	if got := q.Doc.Blocks[1].Text; got != "task two plain words" {
		t.Errorf("one line: %q", got)
	}
}

func TestEmphasize(t *testing.T) {
	e := newEd()
	typeKeys(e, "make this bold")
	e.MoveTo(Pos{0, 5}, false)
	e.MoveTo(Pos{0, 9}, true)
	e.Emphasize("**")
	if got := e.Doc.Blocks[0].Text; got != "make **this** bold" {
		t.Fatalf("wrap: %q", got)
	}
	e.Emphasize("**")
	if got := e.Doc.Blocks[0].Text; got != "make this bold" {
		t.Errorf("unwrap: %q", got)
	}
	e.MoveTo(Pos{0, 0}, false)
	e.Emphasize("*")
	typeKeys(e, "x")
	if got := e.Doc.Blocks[0].Text; got != "*x*make this bold" {
		t.Errorf("empty pair: %q", got)
	}
}

func TestWordMoves(t *testing.T) {
	e := newEd()
	typeKeys(e, "one, two  three")
	e.Left(true, false)
	if e.Caret.O != 10 {
		t.Errorf("ctrl+left %d", e.Caret.O)
	}
	e.Left(true, false)
	e.Left(true, false)
	if e.Caret.O != 3 {
		t.Errorf("onto the comma %d", e.Caret.O)
	}
	e.Right(true, false)
	if e.Caret.O != 5 {
		t.Errorf("ctrl+right %d", e.Caret.O)
	}
	e.Backspace(true)
	if got := e.Doc.Blocks[0].Text; got != "onetwo  three" {
		t.Errorf("ctrl+backspace %q", got)
	}
}

func TestSetKind(t *testing.T) {
	e := newEd()
	typeKeys(e, "a\nb")
	e.SelectAll()
	e.SetKind(Numbered)
	if got := kinds(e.Doc); !reflect.DeepEqual(got, []string{"n0 a", "n0 b"}) {
		t.Errorf("got %q", got)
	}
	e.SetKind(Numbered) // all already: back to paragraphs
	if got := kinds(e.Doc); !reflect.DeepEqual(got, []string{"p a", "p b"}) {
		t.Errorf("toggle %q", got)
	}
}

func TestMentionSuggestion(t *testing.T) {
	e := newEd()
	now := 45 * time.Second
	e.Clock = func() time.Duration { return now }

	typeKeys(e, "Ask @")
	if suf, ok := e.MentionSuggestion(); !ok || suf != "flit" {
		t.Fatalf("after @: got %q, %v; want flit, true", suf, ok)
	}
	typeKeys(e, "f")
	if suf, ok := e.MentionSuggestion(); !ok || suf != "lit" {
		t.Fatalf("after @f: got %q, %v; want lit, true", suf, ok)
	}
	typeKeys(e, "l")
	if suf, ok := e.MentionSuggestion(); !ok || suf != "it" {
		t.Fatalf("after @fl: got %q, %v; want it, true", suf, ok)
	}
	// Typing anything outside "flit" dismisses the suggestion.
	typeKeys(e, "o")
	if suf, ok := e.MentionSuggestion(); ok {
		t.Fatalf("after @flo: got %q, true; want dismissed", suf)
	}
	// Backspace back to @fl brings it back, and AcceptMentionSuggestion opens a mention.
	e.Backspace(false)
	if !e.AcceptMentionSuggestion() {
		t.Fatal("AcceptMentionSuggestion returned false")
	}
	if got := e.Doc.Blocks[0].Text; got != "Ask [@flit 0:45: ]" {
		t.Fatalf("accepted: %q", got)
	}
	if _, ok := e.MentionSuggestion(); ok {
		t.Fatal("suggestion stayed active inside mention")
	}
	// Mid-word @ does not trigger a suggestion.
	e2 := newEd()
	typeKeys(e2, "name@")
	if suf, ok := e2.MentionSuggestion(); ok {
		t.Fatalf("mid-word @: got %q, true", suf)
	}
	// Undo reverts the accepted mention cleanly.
	e.Undo()
	if got := e.Doc.Blocks[0].Text; got != "Ask @fl" {
		t.Fatalf("after undo: %q", got)
	}
}
