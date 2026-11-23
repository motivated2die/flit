package notes

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Pos is a place in a doc: a block and a byte offset in its text.
type Pos struct{ B, O int }

// Before reports whether p comes before q.
func (p Pos) Before(q Pos) bool { return p.B < q.B || (p.B == q.B && p.O < q.O) }

// Editor edits a Doc: a caret, an optional selection (from Anchor to
// Caret), undo and redo, and the editing rules of the notes editor
// (docs/research/notes-editor.md): Markdown shortcuts at the start of a
// line, Enter and Backspace that know lists, Tab to indent, @flit mentions.
// The widget that draws it (internal/ui) turns keys and clicks into calls.
type Editor struct {
	Doc           Doc
	Caret, Anchor Pos
	// Clock is the session time while the recording runs (Unknown, or nil,
	// otherwise): lines typed then remember when, and mentions show it.
	Clock func() time.Duration
	// Rev counts changes (the app saves when it moves).
	Rev int
	// Reveal asks the widget drawing the editor to bring the caret into
	// view (set as keys are handled, cleared when it has).
	Reveal bool

	undo, redo []snapshot
	group      string    // the last edit's undo group ("type": typing goes together)
	groupAt    time.Time // when it was
	now        func() time.Time
}

type snapshot struct {
	doc           Doc
	caret, anchor Pos
}

// maxUndo is how many steps undo keeps.
const maxUndo = 300

// NewEditor edits d (an empty doc gets one empty paragraph), caret at the end.
func NewEditor(d Doc) *Editor {
	if len(d.Blocks) == 0 {
		d.Blocks = []Block{{Kind: Para, Typed: Unknown}}
	}
	e := &Editor{Doc: d, now: time.Now}
	e.Caret = e.end()
	e.Anchor = e.Caret
	return e
}

func (e *Editor) clock() time.Duration {
	if e.Clock == nil {
		return Unknown
	}
	if t := e.Clock(); t >= 0 {
		return t
	}
	return Unknown
}

func (e *Editor) block(i int) *Block { return &e.Doc.Blocks[i] }

func (e *Editor) end() Pos {
	last := len(e.Doc.Blocks) - 1
	return Pos{last, len(e.Doc.Blocks[last].Text)}
}

// clamp keeps p inside the doc, on a character boundary.
func (e *Editor) clamp(p Pos) Pos {
	p.B = max(0, min(p.B, len(e.Doc.Blocks)-1))
	t := e.Doc.Blocks[p.B].Text
	p.O = max(0, min(p.O, len(t)))
	for p.O > 0 && p.O < len(t) && !utf8.RuneStart(t[p.O]) {
		p.O--
	}
	return p
}

// --- selection and movement --------------------------------------------------

// HasSel reports whether text is selected.
func (e *Editor) HasSel() bool { return e.Caret != e.Anchor }

// Sel is the selection's start and end (equal: none).
func (e *Editor) Sel() (Pos, Pos) {
	if e.Anchor.Before(e.Caret) {
		return e.Anchor, e.Caret
	}
	return e.Caret, e.Anchor
}

// MoveTo puts the caret at p; extend keeps the anchor (selecting).
func (e *Editor) MoveTo(p Pos, extend bool) {
	e.Caret = e.clamp(p)
	if !extend {
		e.Anchor = e.Caret
	}
	e.group = "" // typing after a move is a new undo step
}

// SelectAll selects everything.
func (e *Editor) SelectAll() {
	e.Anchor, e.Caret = Pos{}, e.end()
	e.group = ""
}

// SelectWord selects the word at p (a double click).
func (e *Editor) SelectWord(p Pos) {
	p = e.clamp(p)
	t := e.Doc.Blocks[p.B].Text
	from, to := p.O, p.O
	for from > 0 {
		r, n := utf8.DecodeLastRuneInString(t[:from])
		if !wordRune(r) {
			break
		}
		from -= n
	}
	for to < len(t) {
		r, n := utf8.DecodeRuneInString(t[to:])
		if !wordRune(r) {
			break
		}
		to += n
	}
	e.Anchor, e.Caret = Pos{p.B, from}, Pos{p.B, to}
	e.group = ""
}

// SelectBlock selects block p.B's text (a triple click).
func (e *Editor) SelectBlock(p Pos) {
	p = e.clamp(p)
	e.Anchor, e.Caret = Pos{p.B, 0}, Pos{p.B, len(e.Doc.Blocks[p.B].Text)}
	e.group = ""
}

// Left moves the caret a character (or a word) back; without extend, a
// selection collapses to its start.
func (e *Editor) Left(word, extend bool) {
	if e.HasSel() && !extend {
		a, _ := e.Sel()
		e.MoveTo(a, false)
		return
	}
	p := e.Caret
	switch {
	case p.O > 0 && word:
		p.O = prevWord(e.Doc.Blocks[p.B].Text, p.O)
	case p.O > 0:
		_, n := utf8.DecodeLastRuneInString(e.Doc.Blocks[p.B].Text[:p.O])
		p.O -= n
	case p.B > 0:
		p = Pos{p.B - 1, len(e.Doc.Blocks[p.B-1].Text)}
	}
	e.MoveTo(p, extend)
}

// Right moves the caret a character (or to the next word) on.
func (e *Editor) Right(word, extend bool) {
	if e.HasSel() && !extend {
		_, b := e.Sel()
		e.MoveTo(b, false)
		return
	}
	p := e.Caret
	t := e.Doc.Blocks[p.B].Text
	switch {
	case p.O < len(t) && word:
		p.O = nextWord(t, p.O)
	case p.O < len(t):
		_, n := utf8.DecodeRuneInString(t[p.O:])
		p.O += n
	case p.B < len(e.Doc.Blocks)-1:
		p = Pos{p.B + 1, 0}
	}
	e.MoveTo(p, extend)
}

// Home and End go to the start and end of the line (doc: of the notes).
func (e *Editor) Home(doc, extend bool) {
	if doc {
		e.MoveTo(Pos{}, extend)
		return
	}
	e.MoveTo(Pos{e.Caret.B, 0}, extend)
}

func (e *Editor) End(doc, extend bool) {
	if doc {
		e.MoveTo(e.end(), extend)
		return
	}
	e.MoveTo(Pos{e.Caret.B, len(e.Doc.Blocks[e.Caret.B].Text)}, extend)
}

func wordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' }

// prevWord is where the word before offset o starts (spaces skipped).
func prevWord(s string, o int) int {
	for o > 0 {
		r, n := utf8.DecodeLastRuneInString(s[:o])
		if !unicode.IsSpace(r) {
			break
		}
		o -= n
	}
	if o == 0 {
		return 0
	}
	r, _ := utf8.DecodeLastRuneInString(s[:o])
	inWord := wordRune(r)
	for o > 0 {
		r, n := utf8.DecodeLastRuneInString(s[:o])
		if unicode.IsSpace(r) || wordRune(r) != inWord {
			break
		}
		o -= n
	}
	return o
}

// nextWord is where the next word after offset o starts.
func nextWord(s string, o int) int {
	if o < len(s) {
		r, _ := utf8.DecodeRuneInString(s[o:])
		if !unicode.IsSpace(r) {
			inWord := wordRune(r)
			for o < len(s) {
				r, n := utf8.DecodeRuneInString(s[o:])
				if unicode.IsSpace(r) || wordRune(r) != inWord {
					break
				}
				o += n
			}
		}
	}
	for o < len(s) {
		r, n := utf8.DecodeRuneInString(s[o:])
		if !unicode.IsSpace(r) {
			break
		}
		o += n
	}
	return o
}

// --- undo ----------------------------------------------------------------

// edit runs a change as an undo step: group "type" joins consecutive
// typing (until a pause, a space or a move). f reports whether it changed
// anything; if not, nothing is recorded.
func (e *Editor) edit(group string, f func() bool) bool {
	before := snapshot{e.Doc.clone(), e.Caret, e.Anchor}
	if !f() {
		return false
	}
	now := e.now()
	joins := group == "type" && e.group == "type" && now.Sub(e.groupAt) < 2*time.Second
	if !joins {
		e.undo = append(e.undo, before)
		if len(e.undo) > maxUndo {
			e.undo = e.undo[len(e.undo)-maxUndo:]
		}
	}
	e.redo = nil
	e.group, e.groupAt = group, now
	e.Rev++
	return true
}

// Undo takes the last change back.
func (e *Editor) Undo() bool {
	if len(e.undo) == 0 {
		return false
	}
	s := e.undo[len(e.undo)-1]
	e.undo = e.undo[:len(e.undo)-1]
	e.redo = append(e.redo, snapshot{e.Doc.clone(), e.Caret, e.Anchor})
	e.Doc, e.Caret, e.Anchor = s.doc, s.caret, s.anchor
	e.group = ""
	e.Rev++
	return true
}

// Redo does an undone change again.
func (e *Editor) Redo() bool {
	if len(e.redo) == 0 {
		return false
	}
	s := e.redo[len(e.redo)-1]
	e.redo = e.redo[:len(e.redo)-1]
	e.undo = append(e.undo, snapshot{e.Doc.clone(), e.Caret, e.Anchor})
	e.Doc, e.Caret, e.Anchor = s.doc, s.caret, s.anchor
	e.group = ""
	e.Rev++
	return true
}

// --- typing --------------------------------------------------------------

// Type types text at the caret (one line; a selection is replaced). A
// space after a Markdown marker at the start of a line turns the line into
// that block ("## ", "### ", "- ", "1. ", "[] "); a space or colon after
// "@flit" starts a mention.
func (e *Editor) Type(s string) {
	s = oneLine(s)
	if s == "" {
		return
	}
	if s == " " && !e.HasSel() {
		// "@flit:" then a space: the mention already has its space.
		if m, ok := e.mentionAt(e.Caret); ok && e.Caret.O == m.Body && m.Instruction == "" {
			return
		}
	}
	if s == "]" {
		if m, ok := e.mentionAt(e.Caret); ok {
			e.confirmMention(m) // the bracket closes the mention, as Enter does
			return
		}
	}
	e.edit("type", func() bool {
		e.deleteSel()
		e.insertText(s)
		if s == " " {
			e.blockShortcut()
		}
		if s == " " || s == ":" {
			e.startMention()
		}
		return true
	})
	if strings.TrimSpace(s) == "" {
		e.group = "" // a word ends: the next one is its own undo step
	}
}

// oneLine replaces line breaks and tabs with spaces and drops control
// characters.
func oneLine(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			return ' '
		case unicode.IsControl(r):
			return -1
		}
		return r
	}, s)
}

// insertText puts s at the caret of its block, marking when the line was
// typed if the recording runs.
func (e *Editor) insertText(s string) {
	b := e.block(e.Caret.B)
	b.Text = b.Text[:e.Caret.O] + s + b.Text[e.Caret.O:]
	e.Caret.O += len(s)
	e.Anchor = e.Caret
	if b.Typed == Unknown {
		b.Typed = e.clock()
	}
}

// shortcuts turn the start of a paragraph into a block when a space follows.
var shortcuts = []struct {
	prefix string
	kind   Kind
	done   bool
}{
	{"#", H2, false}, {"##", H2, false}, {"###", H3, false},
	{"-", Bullet, false}, {"*", Bullet, false}, {"+", Bullet, false},
	{"[]", Task, false}, {"[ ]", Task, false}, {"[x]", Task, true},
	{"- []", Task, false}, {"- [ ]", Task, false}, {"- [x]", Task, true},
}

// blockShortcut applies a shortcut if the text before the caret is one
// (plus the space just typed): at the start of any line, so a continued
// list item can become a task or a heading.
func (e *Editor) blockShortcut() {
	b := e.block(e.Caret.B)
	head := b.Text[:e.Caret.O-1] // without the space
	kind, done, ok := Para, false, false
	for _, sc := range shortcuts {
		if head == sc.prefix {
			kind, done, ok = sc.kind, sc.done, true
		}
	}
	if !ok && len(head) >= 2 && (head[len(head)-1] == '.' || head[len(head)-1] == ')') && allDigits(head[:len(head)-1]) && len(head) <= 4 {
		kind, ok = Numbered, true
	}
	if !ok {
		return
	}
	indent := b.Indent
	if !b.Kind.List() {
		indent = e.indentAfter(e.Caret.B)
	}
	b.Kind, b.Done, b.Indent = kind, done, indent
	if !kind.List() {
		b.Indent = 0
	}
	b.Text = b.Text[e.Caret.O:]
	e.Caret.O, e.Anchor = 0, Pos{e.Caret.B, 0}
}

func allDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return s != ""
}

// indentAfter is the indent a new list item at block i starts with: the
// previous item's, when it is one.
func (e *Editor) indentAfter(i int) int {
	if i > 0 && e.Doc.Blocks[i-1].Kind.List() {
		return e.Doc.Blocks[i-1].Indent
	}
	return 0
}

// --- @flit mentions ---------------------------------------------------------

// startMention turns "@flit" just typed (followed by a space or colon, at
// the start of a word) into an open mention, [@flit 12:40: |], the caret
// before its closing bracket. Enter (or "]") confirms it.
func (e *Editor) startMention() {
	if _, ok := e.mentionAt(e.Caret); ok {
		return
	}
	b := e.block(e.Caret.B)
	head := b.Text[:e.Caret.O]
	const word = "@flit"
	if len(head) < len(word)+1 || !strings.EqualFold(head[len(head)-len(word)-1:len(head)-1], word) {
		return
	}
	at := len(head) - len(word) - 1
	if at > 0 {
		if r, _ := utf8.DecodeLastRuneInString(head[:at]); !unicode.IsSpace(r) && r != '(' {
			return
		}
	}
	open := "[@flit: "
	if t := e.clock(); t != Unknown {
		open = "[@flit " + Clock(t) + ": "
	}
	b.Text = head[:at] + open + "]" + b.Text[e.Caret.O:]
	e.Caret.O = at + len(open)
	e.Anchor = e.Caret
}

// mentionAt is the mention whose inside (after its "[", up to its "]")
// holds p.
func (e *Editor) mentionAt(p Pos) (MentionRef, bool) {
	for _, m := range Mentions(e.Doc.Blocks[p.B].Text) {
		if p.O > m.From && p.O < m.To {
			return m, true
		}
	}
	return MentionRef{}, false
}

// mentionPrefix finds a partial or complete "@flit" token immediately before
// the caret (starting at line start, whitespace, or '(') when not already
// inside a mention or selection and not followed by a word character.
// Returns the start byte offset of '@' and the remaining suffix of "flit".
func (e *Editor) mentionPrefix() (at int, suffix string, ok bool) {
	if e.HasSel() || e.InMention() {
		return 0, "", false
	}
	b := e.Doc.Blocks[e.Caret.B]
	head := b.Text[:e.Caret.O]
	tail := b.Text[e.Caret.O:]
	if tail != "" {
		if r, _ := utf8.DecodeRuneInString(tail); wordRune(r) {
			return 0, "", false
		}
	}
	const target = "@flit"
	for n := min(len(target), len(head)); n >= 1; n-- {
		start := len(head) - n
		if head[start] != '@' {
			continue
		}
		if start > 0 {
			if r, _ := utf8.DecodeLastRuneInString(head[:start]); !unicode.IsSpace(r) && r != '(' {
				return 0, "", false
			}
		}
		if strings.EqualFold(head[start:], target[:n]) {
			return start, target[n:], true
		}
		return 0, "", false
	}
	return 0, "", false
}

// MentionSuggestion returns the ghost completion suffix (e.g. "flit" right
// after "@", "lit" after "@f") when the caret sits at an @flit prefix.
func (e *Editor) MentionSuggestion() (string, bool) {
	_, suffix, ok := e.mentionPrefix()
	return suffix, ok
}

// AcceptMentionSuggestion turns an @flit prefix before the caret ("@", "@f",
// ..., "@flit") into an open [@flit: |] mention in one undo step.
func (e *Editor) AcceptMentionSuggestion() bool {
	at, _, ok := e.mentionPrefix()
	if !ok {
		return false
	}
	return e.edit("mention", func() bool {
		b := e.block(e.Caret.B)
		open := "[@flit: "
		if t := e.clock(); t != Unknown {
			open = "[@flit " + Clock(t) + ": "
		}
		b.Text = b.Text[:at] + open + "]" + b.Text[e.Caret.O:]
		e.Caret.O = at + len(open)
		e.Anchor = e.Caret
		if b.Typed == Unknown {
			b.Typed = e.clock()
		}
		return true
	})
}

// InMention reports whether the caret is inside a mention (the widget
// shows it as being edited; Enter confirms it).
func (e *Editor) InMention() bool {
	_, ok := e.mentionAt(e.Caret)
	return ok && !e.HasSel()
}

// confirmMention ends editing mention m: the caret goes after it, with a
// space; an empty one is removed.
func (e *Editor) confirmMention(m MentionRef) {
	e.edit("mention", func() bool {
		b := e.block(e.Caret.B)
		if strings.TrimSpace(m.Instruction) == "" {
			before := strings.TrimRight(b.Text[:m.From], " ")
			after := strings.TrimLeft(b.Text[m.To:], " ")
			sep := ""
			if before != "" && after != "" {
				sep = " "
			}
			b.Text = before + sep + after
			e.Caret.O = len(before) + len(sep)
			e.Anchor = e.Caret
			return true
		}
		o := m.To
		if o >= len(b.Text) || b.Text[o] != ' ' {
			b.Text = b.Text[:o] + " " + b.Text[o:]
		}
		e.Caret.O, e.Anchor = o+1, Pos{e.Caret.B, o + 1}
		return true
	})
	e.group = ""
}

// ConfirmMention confirms the mention the caret is in (Esc); false if
// there is none.
func (e *Editor) ConfirmMention() bool {
	if m, ok := e.mentionAt(e.Caret); ok && !e.HasSel() {
		e.confirmMention(m)
		return true
	}
	return false
}

// --- lines -----------------------------------------------------------------

// Enter ends a line: in a mention it confirms it; on an empty list item it
// steps out of the list (a level at a time); otherwise the line splits, the
// new one continuing a list (an open task after a task), a heading going on
// with a paragraph. At the start of a line, an empty line goes above it.
func (e *Editor) Enter() {
	if m, ok := e.mentionAt(e.Caret); ok && !e.HasSel() {
		e.confirmMention(m)
		return
	}
	e.edit("enter", func() bool {
		e.deleteSel()
		b := e.block(e.Caret.B)
		if b.Kind.List() && b.Text == "" {
			if b.Indent > 0 {
				b.Indent--
			} else {
				b.Kind, b.Done = Para, false
			}
			return true
		}
		o := e.Caret.O
		next := Block{Kind: b.Kind, Indent: b.Indent, Typed: Unknown}
		if next.Kind == H2 || next.Kind == H3 {
			next.Kind = Para
		}
		if !next.Kind.List() {
			next.Indent = 0
		}
		if o == 0 && b.Text != "" {
			// An empty line above; the caret stays with the text.
			e.insertBlock(e.Caret.B, next)
			e.Caret = Pos{e.Caret.B + 1, 0}
			e.Anchor = e.Caret
			return true
		}
		next.Text = b.Text[o:]
		if next.Text != "" {
			next.Typed = b.Typed // moved, not typed now
		}
		b.Text = b.Text[:o]
		e.insertBlock(e.Caret.B+1, next)
		e.Caret = Pos{e.Caret.B + 1, 0}
		e.Anchor = e.Caret
		return true
	})
	e.group = ""
}

func (e *Editor) insertBlock(at int, b Block) {
	e.Doc.Blocks = append(e.Doc.Blocks, Block{})
	copy(e.Doc.Blocks[at+1:], e.Doc.Blocks[at:])
	e.Doc.Blocks[at] = b
}

func (e *Editor) removeBlock(at int) {
	e.Doc.Blocks = append(e.Doc.Blocks[:at], e.Doc.Blocks[at+1:]...)
	if len(e.Doc.Blocks) == 0 {
		e.Doc.Blocks = []Block{{Kind: Para, Typed: Unknown}}
	}
}

// Backspace deletes before the caret (a word, with word): at the start of
// a line, a nested item moves out a level, another block becomes a
// paragraph, and a paragraph joins the line above. At the start of a
// mention's instruction it unwraps the mention, keeping its text.
func (e *Editor) Backspace(word bool) {
	e.edit("delete", func() bool {
		if e.deleteSel() {
			return true
		}
		b := e.block(e.Caret.B)
		o := e.Caret.O
		for _, m := range Mentions(b.Text) {
			if o == m.Body {
				b.Text = b.Text[:m.From] + b.Text[m.Body:m.To-1] + b.Text[m.To:]
				e.Caret.O = m.From
				e.Anchor = e.Caret
				return true
			}
		}
		switch {
		case o > 0:
			from := o
			if word {
				from = prevWord(b.Text, o)
			} else {
				_, n := utf8.DecodeLastRuneInString(b.Text[:o])
				from -= n
			}
			b.Text = b.Text[:from] + b.Text[o:]
			e.Caret.O = from
		case b.Kind.List() && b.Indent > 0:
			b.Indent--
		case b.Kind != Para:
			b.Kind, b.Done, b.Indent = Para, false, 0
		case e.Caret.B > 0:
			prev := e.block(e.Caret.B - 1)
			at := len(prev.Text)
			prev.Text += b.Text
			if prev.Typed == Unknown {
				prev.Typed = b.Typed
			}
			e.removeBlock(e.Caret.B)
			e.Caret = Pos{e.Caret.B - 1, at}
		default:
			return false
		}
		e.Anchor = e.Caret
		return true
	})
}

// Delete deletes after the caret (a word, with word); at the end of a line
// the next line joins it.
func (e *Editor) Delete(word bool) {
	e.edit("delete", func() bool {
		if e.deleteSel() {
			return true
		}
		b := e.block(e.Caret.B)
		o := e.Caret.O
		switch {
		case o < len(b.Text):
			to := o
			if word {
				to = nextWord(b.Text, o)
			} else {
				_, n := utf8.DecodeRuneInString(b.Text[o:])
				to += n
			}
			b.Text = b.Text[:o] + b.Text[to:]
		case e.Caret.B < len(e.Doc.Blocks)-1:
			b.Text += e.Doc.Blocks[e.Caret.B+1].Text
			e.removeBlock(e.Caret.B + 1)
		default:
			return false
		}
		return true
	})
}

// deleteSel removes the selected text (blocks between go; the first keeps
// its kind); everything selected leaves one empty paragraph.
func (e *Editor) deleteSel() bool {
	if !e.HasSel() {
		return false
	}
	a, b := e.Sel()
	if a == (Pos{}) && b == e.end() {
		e.Doc.Blocks = []Block{{Kind: Para, Typed: Unknown}}
		e.Caret, e.Anchor = Pos{}, Pos{}
		return true
	}
	first := e.block(a.B)
	last := e.Doc.Blocks[b.B]
	first.Text = first.Text[:a.O] + last.Text[b.O:]
	e.Doc.Blocks = append(e.Doc.Blocks[:a.B+1], e.Doc.Blocks[b.B+1:]...)
	e.Caret, e.Anchor = a, a
	return true
}

// --- blocks ------------------------------------------------------------------

// selBlocks are the block indexes the selection (or the caret) touches.
func (e *Editor) selBlocks() (int, int) {
	a, b := e.Sel()
	if b.B > a.B && b.O == 0 {
		return a.B, b.B - 1 // a selection ending at a line's start leaves it out
	}
	return a.B, b.B
}

// Indent moves the list items touched by the caret or selection d levels
// in (1) or out (-1). An item goes at most one level deeper than the item
// above it. It reports whether anything was a list item.
func (e *Editor) Indent(d int) bool {
	from, to := e.selBlocks()
	return e.edit("indent", func() bool {
		changed := false
		for i := from; i <= to; i++ {
			b := e.block(i)
			if !b.Kind.List() {
				continue
			}
			limit := 0
			if i > 0 && e.Doc.Blocks[i-1].Kind.List() {
				limit = e.Doc.Blocks[i-1].Indent + 1
			}
			n := max(0, min(b.Indent+d, limit, MaxIndent))
			if n != b.Indent {
				b.Indent, changed = n, true
			}
		}
		return changed
	})
}

// SetKind makes the touched blocks kind k; if they all are already, they
// become paragraphs (a toolbar button toggles).
func (e *Editor) SetKind(k Kind) {
	from, to := e.selBlocks()
	all := true
	for i := from; i <= to; i++ {
		all = all && e.Doc.Blocks[i].Kind == k
	}
	if all {
		k = Para
	}
	e.edit("kind", func() bool {
		for i := from; i <= to; i++ {
			b := e.block(i)
			if b.Kind.List() && k.List() {
				// stays nested
			} else if k.List() {
				b.Indent = e.indentAfter(i)
			} else {
				b.Indent = 0
			}
			if b.Kind != Task {
				b.Done = false
			}
			b.Kind = k
		}
		return true
	})
}

// ToggleDone ticks or unticks task i (a click on its box); a block that
// isn't a task becomes an open one (Ctrl+Enter).
func (e *Editor) ToggleDone(i int) {
	if i < 0 || i >= len(e.Doc.Blocks) {
		return
	}
	e.edit("done", func() bool {
		b := e.block(i)
		if b.Kind == Task {
			b.Done = !b.Done
		} else {
			if !b.Kind.List() {
				b.Indent = e.indentAfter(i)
			}
			b.Kind, b.Done = Task, false
		}
		return true
	})
}

// Emphasize wraps the selection (within one line) in marker ("**" bold,
// "*" italic), or unwraps it if it already is; without a selection it puts
// an empty pair at the caret, the caret between.
func (e *Editor) Emphasize(marker string) {
	a, b := e.Sel()
	if a.B != b.B {
		return
	}
	e.edit("emphasis", func() bool {
		t := e.block(a.B)
		s := t.Text
		if a.O >= len(marker) && b.O+len(marker) <= len(s) && s[a.O-len(marker):a.O] == marker && s[b.O:b.O+len(marker)] == marker &&
			(marker != "*" || !(a.O >= 2 && s[a.O-2:a.O] == "**")) {
			t.Text = s[:a.O-len(marker)] + s[a.O:b.O] + s[b.O+len(marker):]
			e.Anchor, e.Caret = Pos{a.B, a.O - len(marker)}, Pos{a.B, b.O - len(marker)}
			return true
		}
		t.Text = s[:a.O] + marker + s[a.O:b.O] + marker + s[b.O:]
		e.Anchor, e.Caret = Pos{a.B, a.O + len(marker)}, Pos{a.B, b.O + len(marker)}
		if t.Typed == Unknown {
			t.Typed = e.clock()
		}
		return true
	})
}

// --- the clipboard -----------------------------------------------------------

// Selected is the selection as a doc: within one line just its text (a
// paragraph), across lines whole blocks (the first and last cut), list
// indentation made relative.
func (e *Editor) Selected() Doc {
	if !e.HasSel() {
		return Doc{}
	}
	a, b := e.Sel()
	if a.B == b.B {
		blk := e.Doc.Blocks[a.B]
		if a.O == 0 && b.O == len(blk.Text) {
			blk.Indent = 0
			return Doc{Blocks: []Block{blk}}
		}
		return Doc{Blocks: []Block{{Kind: Para, Text: blk.Text[a.O:b.O], Typed: Unknown}}}
	}
	var d Doc
	for i := a.B; i <= b.B; i++ {
		blk := e.Doc.Blocks[i]
		switch i {
		case a.B:
			blk.Text = blk.Text[a.O:]
		case b.B:
			blk.Text = blk.Text[:b.O]
		}
		d.Blocks = append(d.Blocks, blk)
	}
	if last := d.Blocks[len(d.Blocks)-1]; b.O == 0 && last.Text == "" {
		d.Blocks = d.Blocks[:len(d.Blocks)-1] // the selection ended at a line's start
	}
	least := MaxIndent
	for _, blk := range d.Blocks {
		if blk.Kind.List() {
			least = min(least, blk.Indent)
		}
	}
	for i := range d.Blocks {
		if d.Blocks[i].Kind.List() {
			d.Blocks[i].Indent -= least
		}
	}
	return d
}

// Cut removes the selection and returns it.
func (e *Editor) Cut() Doc {
	d := e.Selected()
	if len(d.Blocks) > 0 {
		e.edit("cut", e.deleteSel)
	}
	return d
}

// Paste inserts text: one line goes in as text; several lines (or one
// that starts like a block) are read as Markdown and go in as blocks, the
// first joining the caret's line, the last taking what followed the caret.
// Pasted lines have no typing time.
func (e *Editor) Paste(text string) {
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	text = strings.Trim(text, "\n")
	if text == "" {
		return
	}
	d := Parse(text)
	if !strings.Contains(text, "\n") && len(d.Blocks) == 1 && d.Blocks[0].Kind == Para {
		e.edit("paste", func() bool {
			e.deleteSel()
			b := e.block(e.Caret.B)
			s := oneLine(text)
			b.Text = b.Text[:e.Caret.O] + s + b.Text[e.Caret.O:]
			e.Caret.O += len(s)
			e.Anchor = e.Caret
			return true
		})
		e.group = ""
		return
	}
	if len(d.Blocks) == 0 {
		return
	}
	e.edit("paste", func() bool {
		e.deleteSel()
		at := e.Caret.B
		cur := e.Doc.Blocks[at]
		left, right := cur.Text[:e.Caret.O], cur.Text[e.Caret.O:]
		shift := 0
		if cur.Kind.List() {
			shift = cur.Indent
		}
		in := make([]Block, len(d.Blocks))
		for i, blk := range d.Blocks {
			if blk.Kind.List() {
				blk.Indent = min(blk.Indent+shift, MaxIndent)
			}
			blk.Typed = Unknown
			in[i] = blk
		}
		first := cur
		if strings.TrimSpace(left) == "" {
			first = in[0] // an empty line takes the pasted line's kind
			first.Text = left + in[0].Text
		} else {
			first.Text = left + in[0].Text
		}
		out := append([]Block{first}, in[1:]...)
		last := &out[len(out)-1]
		caret := Pos{at + len(out) - 1, len(last.Text)}
		last.Text += right
		blocks := append([]Block(nil), e.Doc.Blocks[:at]...)
		blocks = append(blocks, out...)
		e.Doc.Blocks = append(blocks, e.Doc.Blocks[at+1:]...)
		e.Caret, e.Anchor = caret, caret
		return true
	})
	e.group = ""
}
