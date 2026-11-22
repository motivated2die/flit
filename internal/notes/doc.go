// Package notes is a recording's notes: a list of blocks (paragraphs,
// headings 2 and 3, bullets, numbered items, tasks) whose text keeps inline
// Markdown as typed (**bold**, *italic*, [@flit …] mentions), stored as
// Markdown (docs/research/notes-editor.md). Editor edits a document;
// nothing here draws or touches Windows.
package notes

import (
	"fmt"
	"html"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Kind is what a block is.
type Kind uint8

const (
	Para Kind = iota
	H2
	H3
	Bullet
	Numbered
	Task
)

// List reports whether blocks of this kind are list items (they indent).
func (k Kind) List() bool { return k == Bullet || k == Numbered || k == Task }

// MaxIndent is the deepest a list item nests (0 is not nested).
const MaxIndent = 4

// Unknown is a time that isn't known: a line not typed during a recording.
const Unknown time.Duration = -1

// Block is one line of notes.
type Block struct {
	Kind   Kind
	Indent int    // list items only
	Done   bool   // tasks only
	Text   string // inline Markdown, one line
	// Typed is when the line was first typed during the recording, on the
	// session's time line (Unknown: before or after it, or pasted).
	Typed time.Duration
}

// Doc is a recording's notes.
type Doc struct {
	Blocks []Block
}

// Empty reports whether there is no text at all.
func (d Doc) Empty() bool {
	for _, b := range d.Blocks {
		if strings.TrimSpace(b.Text) != "" {
			return false
		}
	}
	return true
}

// clone copies the blocks (they are values; the slice is new).
func (d Doc) clone() Doc { return Doc{Blocks: append([]Block(nil), d.Blocks...)} }

// --- reading Markdown -------------------------------------------------------

var (
	reHeading  = regexp.MustCompile(`^(#{1,6})[ \t]+(.*?)(?:[ \t]+#+)?[ \t]*$`)
	reTask     = regexp.MustCompile(`^[-*+][ \t]+\[([ xX])\](?:[ \t]+(.*)|$)`)
	reBullet   = regexp.MustCompile(`^[-*+•◦▪‣][ \t]+(.*)$`)
	reNumbered = regexp.MustCompile(`^(\d{1,9})[.)][ \t]+(.*)$`)
	rePlainBox = regexp.MustCompile(`^([☐☑☒✓✔])[ \t]*(.*)$`) // tasks as plain text gives them
)

// Parse reads Markdown into blocks. Each non-blank line is a block (as
// note apps treat lines; lists nest by indentation); an indented line under
// a list item that isn't one itself continues it. `#` and `##` are
// headings 2, `###` and deeper headings 3; `*`, `+`, `•` bullets; `1.` and
// `1)` numbered items; `- [ ]`, `- [x]` and ☐/☑ tasks. Everything else is a
// paragraph, with a leading backslash (an escaped marker) removed.
func Parse(md string) Doc {
	md = strings.ReplaceAll(strings.ReplaceAll(md, "\r\n", "\n"), "\r", "\n")
	var d Doc
	// Open list levels: the column each level's marker sits at.
	var levels []int
	blank := true // the previous line was blank (or there was none)
	for _, line := range strings.Split(md, "\n") {
		if strings.TrimSpace(line) == "" {
			blank = true
			continue
		}
		col, rest := leading(line)
		b, isList := lineBlock(rest)
		switch {
		case isList:
			// Deeper than the open level: a child; else back to the level
			// whose marker is at (or before) this column.
			for len(levels) > 0 && col < levels[len(levels)-1] {
				levels = levels[:len(levels)-1]
			}
			if len(levels) == 0 || col > levels[len(levels)-1] {
				levels = append(levels, col)
			}
			b.Indent = min(len(levels)-1, MaxIndent)
		case b.Kind == Para && !blank && col >= 2 && len(d.Blocks) > 0 && d.Blocks[len(d.Blocks)-1].Kind.List():
			// An indented line right under an item continues it.
			last := &d.Blocks[len(d.Blocks)-1]
			last.Text = strings.TrimSpace(last.Text + " " + b.Text)
			blank = false
			continue
		default:
			levels = nil
		}
		d.Blocks = append(d.Blocks, b)
		blank = false
	}
	return d
}

// leading is a line's indentation in columns (a tab is 4) and the rest.
func leading(line string) (int, string) {
	col := 0
	for i, r := range line {
		switch r {
		case ' ':
			col++
		case '\t':
			col += 4 - col%4
		default:
			return col, line[i:]
		}
	}
	return col, ""
}

// lineBlock reads one line without its indentation.
func lineBlock(s string) (Block, bool) {
	s = strings.TrimRight(s, " \t")
	b := Block{Typed: Unknown}
	if m := reHeading.FindStringSubmatch(s); m != nil {
		b.Kind, b.Text = H2, m[2]
		if len(m[1]) >= 3 {
			b.Kind = H3
		}
		return b, false
	}
	if m := reTask.FindStringSubmatch(s); m != nil {
		b.Kind, b.Done, b.Text = Task, m[1] != " ", m[2]
		return b, true
	}
	if m := rePlainBox.FindStringSubmatch(s); m != nil {
		b.Kind, b.Done, b.Text = Task, m[1] != "☐", m[2]
		return b, true
	}
	if m := reBullet.FindStringSubmatch(s); m != nil && !isRule(s) {
		b.Kind, b.Text = Bullet, m[1]
		return b, true
	}
	if m := reNumbered.FindStringSubmatch(s); m != nil {
		b.Kind, b.Text = Numbered, m[2]
		return b, true
	}
	b.Kind, b.Text = Para, unescapeLead(s)
	return b, false
}

// isRule reports a thematic break ("- - -", "* * *"), which isn't a bullet.
func isRule(s string) bool {
	t := strings.ReplaceAll(strings.ReplaceAll(s, " ", ""), "\t", "")
	return len(t) >= 3 && (strings.Trim(t, "-") == "" || strings.Trim(t, "*") == "")
}

// unescapeLead removes the backslash Markdown() puts before a paragraph
// that would otherwise read as a heading or a list item.
func unescapeLead(s string) string {
	if strings.HasPrefix(s, `\`) && len(s) > 1 && strings.ContainsRune(`#-*+•0123456789☐☑`, []rune(s[1:])[0]) {
		return s[1:]
	}
	return s
}

// --- writing Markdown ---------------------------------------------------------

// Markdown writes the notes: `## `, `### `, `- `, `1. `, `- [ ] `/`- [x] `,
// list items nested under their parent's text (two spaces under a bullet or
// task, under the number's width under a numbered item), numbers counted per
// list, a blank line between blocks except between list items.
func (d Doc) Markdown() string {
	var sb strings.Builder
	var stack []listLevel // the open list levels: where each one's text starts
	for i, b := range d.Blocks {
		if i > 0 {
			if b.Kind.List() && d.Blocks[i-1].Kind.List() {
				sb.WriteByte('\n')
			} else {
				sb.WriteString("\n\n")
			}
		}
		text := strings.TrimRight(b.Text, " \t") // trailing spaces would mean a line break
		switch b.Kind {
		case H2:
			sb.WriteString("## " + text)
			stack = nil
		case H3:
			sb.WriteString("### " + text)
			stack = nil
		case Para:
			sb.WriteString(escapeLead(text))
			stack = nil
		default:
			stack = openLevel(stack, b)
			lv := stack[len(stack)-1]
			sb.WriteString(strings.Repeat(" ", lv.col) + marker(b, lv.n) + text)
		}
	}
	if sb.Len() > 0 {
		sb.WriteByte('\n')
	}
	return sb.String()
}

// listLevel is an open list level: the column its markers sit at, the kind
// of list (numbered or not) and its last number.
type listLevel struct {
	col      int
	numbered bool
	n        int
	width    int // the marker's width, where the item's text starts
}

// openLevel updates the open levels for list item b and counts its number.
func openLevel(stack []listLevel, b Block) []listLevel {
	depth := b.Indent
	if depth > len(stack) {
		depth = len(stack) // can't skip a level
	}
	stack = stack[:min(len(stack), depth+1)]
	numbered := b.Kind == Numbered
	if len(stack) == depth+1 && stack[depth].numbered != numbered {
		stack = stack[:depth] // another kind of list starts here
	}
	if len(stack) == depth {
		col := 0
		if depth > 0 {
			col = stack[depth-1].col + stack[depth-1].width
		}
		stack = append(stack, listLevel{col: col, numbered: numbered})
	}
	lv := &stack[depth]
	lv.n++
	lv.width = len(marker(b, lv.n))
	if b.Kind == Task {
		lv.width = 2 // children of a task sit under its "- "
	}
	return stack
}

// marker is a list item's marker (with the space after it).
func marker(b Block, n int) string {
	switch b.Kind {
	case Numbered:
		return fmt.Sprintf("%d. ", n)
	case Task:
		if b.Done {
			return "- [x] "
		}
		return "- [ ] "
	}
	return "- "
}

// escapeLead keeps a paragraph that starts like a heading or a list item a
// paragraph.
func escapeLead(s string) string {
	if s == "" {
		return s
	}
	if b, isList := lineBlock(s); isList || b.Kind != Para || strings.HasPrefix(s, `\`) {
		return `\` + s
	}
	return s
}

// Numbers are the numbers numbered items show (0 for other blocks): counted
// per list as Markdown() writes them.
func (d Doc) Numbers() []int {
	out := make([]int, len(d.Blocks))
	var stack []listLevel
	for i, b := range d.Blocks {
		if !b.Kind.List() {
			stack = nil
			continue
		}
		stack = openLevel(stack, b)
		if b.Kind == Numbered {
			out[i] = stack[len(stack)-1].n
		}
	}
	return out
}

// --- plain text and HTML ---------------------------------------------------------

// Plain writes the notes as text without Markdown: headings as their text,
// bullets as "•", numbered items with their numbers, tasks as ☐ and ☑,
// emphasis markers removed, two spaces per indentation level.
func (d Doc) Plain() string {
	nums := d.Numbers()
	var sb strings.Builder
	for i, b := range d.Blocks {
		if i > 0 {
			if b.Kind.List() && d.Blocks[i-1].Kind.List() {
				sb.WriteByte('\n')
			} else {
				sb.WriteString("\n\n")
			}
		}
		text := PlainInline(b.Text)
		switch b.Kind {
		case Bullet:
			sb.WriteString(strings.Repeat("  ", b.Indent) + "• " + text)
		case Numbered:
			sb.WriteString(strings.Repeat("  ", b.Indent) + fmt.Sprintf("%d. ", nums[i]) + text)
		case Task:
			box := "☐ "
			if b.Done {
				box = "☑ "
			}
			sb.WriteString(strings.Repeat("  ", b.Indent) + box + text)
		default:
			sb.WriteString(text)
		}
	}
	if sb.Len() > 0 {
		sb.WriteByte('\n')
	}
	return sb.String()
}

// HTML writes the notes as an HTML fragment (for pasting into Outlook,
// OneNote or Word): headings, nested lists, tasks as ☐/☑ items, emphasis.
func (d Doc) HTML() string {
	var sb strings.Builder
	type open struct {
		tag    string
		indent int
	}
	var lists []open
	closeTo := func(indent int) {
		for len(lists) > 0 && lists[len(lists)-1].indent > indent {
			sb.WriteString("</li></" + lists[len(lists)-1].tag + ">")
			lists = lists[:len(lists)-1]
		}
	}
	for _, b := range d.Blocks {
		text := InlineHTML(b.Text)
		if !b.Kind.List() {
			closeTo(-1)
			switch b.Kind {
			case H2:
				sb.WriteString("<h2>" + text + "</h2>")
			case H3:
				sb.WriteString("<h3>" + text + "</h3>")
			default:
				sb.WriteString("<p>" + text + "</p>")
			}
			continue
		}
		tag := "ul"
		if b.Kind == Numbered {
			tag = "ol"
		}
		closeTo(b.Indent)
		switch {
		case len(lists) > 0 && lists[len(lists)-1].indent == b.Indent && lists[len(lists)-1].tag == tag:
			sb.WriteString("</li><li>")
		case len(lists) > 0 && lists[len(lists)-1].indent == b.Indent:
			sb.WriteString("</li></" + lists[len(lists)-1].tag + "><" + tag + "><li>")
			lists[len(lists)-1].tag = tag
		default:
			sb.WriteString("<" + tag + "><li>")
			lists = append(lists, open{tag, b.Indent})
		}
		if b.Kind == Task {
			if b.Done {
				text = "☑ " + text
			} else {
				text = "☐ " + text
			}
		}
		sb.WriteString(text)
	}
	closeTo(-1)
	return sb.String()
}

// --- inline Markdown -------------------------------------------------------------

// Role is what a part of a line's text is.
type Role uint8

const (
	Text    Role = iota
	Marker       // emphasis markers (** _ *), a mention's brackets: shown faint
	Mention      // a mention's "@flit"
	Meta         // a mention's time and colon
	Note         // a mention's instruction
)

// Span is a styled part of a line's text: bytes [From, To).
type Span struct {
	From, To     int
	Role         Role
	Bold, Italic bool
	InMention    bool
}

// reMention is a mention as stored: [@flit: instruction] or, typed during a
// recording, [@flit 12:40: instruction].
var reMention = regexp.MustCompile(`\[@flit(?: (\d{1,2}:\d{2}(?::\d{2})?))?:[ ]?([^\]]*)\]`)

// MentionRef is a mention in a line: where it is, its time (Unknown when
// none), and its instruction.
type MentionRef struct {
	From, To    int // the whole [@flit …]
	At          time.Duration
	Instruction string
	Body        int // where the instruction starts
}

// Mentions finds the @flit mentions in a line.
func Mentions(s string) []MentionRef {
	var out []MentionRef
	for _, m := range reMention.FindAllStringSubmatchIndex(s, -1) {
		r := MentionRef{From: m[0], To: m[1], At: Unknown, Body: m[4], Instruction: s[m[4]:m[5]]}
		if m[2] >= 0 {
			r.At = parseClock(s[m[2]:m[3]])
		}
		out = append(out, r)
	}
	return out
}

// Inline splits a line into styled spans covering all of it, in order.
func Inline(s string) []Span {
	var out []Span
	at := 0
	for _, m := range Mentions(s) {
		out = append(out, emphasis(s, at, m.From)...)
		meta := m.From + len("[@flit")
		out = append(out,
			Span{From: m.From, To: m.From + 1, Role: Marker, InMention: true},
			Span{From: m.From + 1, To: meta, Role: Mention, InMention: true},
			Span{From: meta, To: m.Body, Role: Meta, InMention: true})
		if m.To-1 > m.Body {
			out = append(out, Span{From: m.Body, To: m.To - 1, Role: Note, InMention: true})
		}
		out = append(out, Span{From: m.To - 1, To: m.To, Role: Marker, InMention: true})
		at = m.To
	}
	return append(out, emphasis(s, at, len(s))...)
}

// emphasis styles s[from:to]: **bold** (or __bold__) and *italic* (or
// _italic_), markers as Marker spans. A delimiter opens before a non-space
// and closes after one; underscores don't work inside words (snake_case).
func emphasis(s string, from, to int) []Span {
	if from >= to {
		return nil
	}
	type mark struct{ at, n int }
	var out []Span
	bold, italic := false, false
	// Find matching pairs first, so an unclosed delimiter stays text.
	var pairs [][2]mark
	var stack []mark
	sub := s[:to]
	for i := from; i < to; {
		c := s[i]
		if c != '*' && c != '_' {
			i++
			continue
		}
		n := 1
		for i+n < to && s[i+n] == c && n < 2 {
			n++
		}
		prev, _ := utf8.DecodeLastRuneInString(s[from:i])
		next, _ := utf8.DecodeRuneInString(sub[i+n:])
		if i == from {
			prev = ' '
		}
		if i+n >= to {
			next = ' '
		}
		canOpen := !unicode.IsSpace(next) && (c == '*' || !isWordRune(prev))
		canClose := !unicode.IsSpace(prev) && i > from && (c == '*' || !isWordRune(next))
		m := mark{i, n}
		closed := false
		if canClose {
			for k := len(stack) - 1; k >= 0; k-- {
				if s[stack[k].at] == c && stack[k].n == n {
					pairs = append(pairs, [2]mark{stack[k], m})
					stack = stack[:k]
					closed = true
					break
				}
			}
		}
		if !closed && canOpen {
			stack = append(stack, m)
		}
		i += n
	}
	starts := map[int][2]mark{}
	for _, p := range pairs {
		starts[p[0].at] = p
	}
	ends := map[int]mark{}
	for _, p := range pairs {
		ends[p[1].at] = p[0]
	}
	text := func(a, b int) {
		if b > a {
			out = append(out, Span{From: a, To: b, Role: Text, Bold: bold, Italic: italic})
		}
	}
	last := from
	for i := from; i < to; i++ {
		if p, ok := starts[i]; ok {
			text(last, i)
			out = append(out, Span{From: i, To: i + p[0].n, Role: Marker, Bold: bold, Italic: italic})
			if p[0].n == 2 {
				bold = true
			} else {
				italic = true
			}
			i += p[0].n - 1
			last = i + 1
			continue
		}
		if o, ok := ends[i]; ok {
			text(last, i)
			if o.n == 2 {
				bold = false
			} else {
				italic = false
			}
			out = append(out, Span{From: i, To: i + o.n, Role: Marker, Bold: bold, Italic: italic})
			i += o.n - 1
			last = i + 1
		}
	}
	text(last, to)
	return out
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// PlainInline is a line's text without emphasis markers; a mention reads
// "@flit (12:40): instruction".
func PlainInline(s string) string {
	var sb strings.Builder
	for _, sp := range Inline(s) {
		switch sp.Role {
		case Marker:
		case Meta:
			sb.WriteString(metaText(s[sp.From:sp.To]))
		default:
			sb.WriteString(s[sp.From:sp.To])
		}
	}
	return strings.TrimSpace(sb.String())
}

// metaText is a mention's time and colon in plain text: " (12:40): ", or
// ": " without a time.
func metaText(meta string) string {
	if t := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(meta), ":")); t != "" {
		return " (" + t + "): "
	}
	return ": "
}

// InlineHTML is a line's text as HTML: escaped, emphasis as <strong> and
// <em>, a mention as plain text.
func InlineHTML(s string) string {
	var sb strings.Builder
	bold, italic := false, false
	set := func(b, it bool) {
		if italic && !it {
			sb.WriteString("</em>")
		}
		if bold && !b {
			sb.WriteString("</strong>")
		}
		if b && !bold {
			sb.WriteString("<strong>")
		}
		if it && !italic {
			sb.WriteString("<em>")
		}
		bold, italic = b, it
	}
	for _, sp := range Inline(s) {
		switch sp.Role {
		case Marker:
			continue
		case Meta:
			set(false, false)
			sb.WriteString(html.EscapeString(metaText(s[sp.From:sp.To])))
			continue
		}
		set(sp.Bold, sp.Italic)
		sb.WriteString(html.EscapeString(s[sp.From:sp.To]))
	}
	set(false, false)
	return sb.String()
}

// Clock writes a session time as m:ss (or h:mm:ss).
func Clock(d time.Duration) string {
	s := int(d / time.Second)
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// parseClock reads m:ss or h:mm:ss.
func parseClock(s string) time.Duration {
	var parts []int
	for _, p := range strings.Split(s, ":") {
		n := 0
		for _, c := range p {
			n = n*10 + int(c-'0')
		}
		parts = append(parts, n)
	}
	sec := 0
	for _, p := range parts {
		sec = sec*60 + p
	}
	return time.Duration(sec) * time.Second
}
