package ui

import (
	"fmt"
	"math"
	"strings"

	"github.com/motivated2die/flit/internal/notes"
)

// NotesEditor edits a recording's notes in place (docs/research/notes-editor.md):
// each block drawn as Markdown usually renders it (headings 2 and 3,
// bullets and numbers by level, checkboxes), inline emphasis styled with
// its markers hidden, @flit mentions as chips. The text is edited by a
// notes.Editor the app keeps; the window routes keys and the pointer here
// while the editor has the keyboard (Ctx.Editor).
type NotesEditor struct {
	ID          string
	Ed          *notes.Editor
	Placeholder string
	// Focus asks for the keyboard as the editor is shown (once).
	Focus bool
	// OnChange hears every change (the app saves a moment later).
	OnChange func()
	// MinH is the least height (logical pixels).
	MinH float32
	// Toolbar shows a row of buttons above the text: headings, lists, a
	// task, bold, italic and @flit (the same as the Markdown shortcuts).
	Toolbar bool
	// ToolbarOnFocus hides the toolbar until the editor has the keyboard,
	// expanding the container upward above the text (compensated by page
	// scroll) so the text below does not shift.
	ToolbarOnFocus bool
	// OnPin, when set, shows a pin button at the right end of the toolbar
	// swim lane (popping the notes out into an always-on-top window, or
	// returning them to the main window).
	OnPin func()
	// Pinned marks the editor as currently popped out in the floating window:
	// highlights the pin button and shows a 6-dot (3×2) drag handle beside it.
	Pinned bool
}

// tool is a toolbar button.
type tool struct {
	id, glyph string
	act       func(ed *notes.Editor)
	active    func(ed *notes.Editor) bool
	gapBefore bool
}

var notesTools = []tool{
	{id: "h2", glyph: GlyphH2, act: func(ed *notes.Editor) { ed.SetKind(notes.H2) }, active: kindIs(notes.H2)},
	{id: "h3", glyph: GlyphH3, act: func(ed *notes.Editor) { ed.SetKind(notes.H3) }, active: kindIs(notes.H3)},
	{id: "bullet", glyph: GlyphList, act: func(ed *notes.Editor) { ed.SetKind(notes.Bullet) }, active: kindIs(notes.Bullet), gapBefore: true},
	{id: "numbered", glyph: GlyphNumbered, act: func(ed *notes.Editor) { ed.SetKind(notes.Numbered) }, active: kindIs(notes.Numbered)},
	{id: "task", glyph: GlyphTask, act: func(ed *notes.Editor) { ed.SetKind(notes.Task) }, active: kindIs(notes.Task)},
	{id: "bold", glyph: GlyphBold, act: func(ed *notes.Editor) { ed.Emphasize("**") }, gapBefore: true},
	{id: "italic", glyph: GlyphItalic, act: func(ed *notes.Editor) { ed.Emphasize("*") }},
	{id: "flit", glyph: GlyphAt, act: insertMention, gapBefore: true},
}

func kindIs(k notes.Kind) func(ed *notes.Editor) bool {
	return func(ed *notes.Editor) bool { return ed.Doc.Blocks[min(ed.Caret.B, len(ed.Doc.Blocks)-1)].Kind == k }
}

// insertMention types "@flit " at the caret (with a space before it if
// needed), which opens a mention.
func insertMention(ed *notes.Editor) {
	if ed.InMention() {
		return
	}
	b := ed.Doc.Blocks[ed.Caret.B]
	if ed.Caret.O > 0 && b.Text[ed.Caret.O-1] != ' ' && !ed.HasSel() {
		ed.Type(" ")
	}
	for _, r := range "@flit " {
		ed.Type(string(r))
	}
}

var (
	fontH2 = Font{Family: uiFont, Size: 17, Weight: 600}
	fontH3 = Font{Family: uiFont, Size: 14.5, Weight: 600}
)

const (
	vkBack   = 0x08
	vkTab    = 0x09
	vkReturn = 0x0D
	vkPrior  = 0x21
	vkNext   = 0x22
	vkUp     = 0x26
	vkDown   = 0x28
)

// blockGeom is where a block's text was laid out (window pixels).
type blockGeom struct {
	x, top, w, bottom float32
	lines             *TextLines // nil for an empty line
	lineH             float32
	disp              blockDisplay
}

func (g blockGeom) toDisp(srcO int) int {
	if len(g.disp.srcToDisp) == 0 {
		return srcO
	}
	if srcO < 0 {
		return 0
	}
	if srcO >= len(g.disp.srcToDisp) {
		return g.disp.srcToDisp[len(g.disp.srcToDisp)-1]
	}
	return g.disp.srcToDisp[srcO]
}

func (g blockGeom) toSrc(dispO int) int {
	if len(g.disp.dispToSrc) == 0 {
		return dispO
	}
	if dispO < 0 {
		return 0
	}
	if dispO >= len(g.disp.dispToSrc) {
		return g.disp.dispToSrc[len(g.disp.dispToSrc)-1]
	}
	return g.disp.dispToSrc[dispO]
}

// caret is where source offset o sits in the block: x, and its line's top
// and height (relative to the window).
func (g blockGeom) caret(o int) (x, top, h float32) {
	if g.lines == nil || len(g.lines.Lines) == 0 {
		return g.x, g.top, g.lineH
	}
	d := g.toDisp(o)
	ls := g.lines.Lines
	l := ls[len(ls)-1]
	for _, ln := range ls {
		if d >= ln.From && d < ln.To {
			l = ln
			break
		}
	}
	return g.x + l.x(d), g.top + l.Top, l.H
}

// lineIndex is the visual line of the block source offset o is on.
func (g blockGeom) lineIndex(o int) int {
	if g.lines == nil {
		return 0
	}
	d := g.toDisp(o)
	for i, ln := range g.lines.Lines {
		if d >= ln.From && d < ln.To {
			return i
		}
	}
	return max(0, len(g.lines.Lines)-1)
}

func (g blockGeom) lineCount() int {
	if g.lines == nil || len(g.lines.Lines) == 0 {
		return 1
	}
	return len(g.lines.Lines)
}

// posAt is the source offset nearest (x, y) in the block.
func (g blockGeom) posAt(x, y float32) int {
	if g.lines == nil {
		return 0
	}
	return g.toSrc(g.lines.PosAt(x-g.x, y-g.top))
}

func blockFont(k notes.Kind) Font {
	switch k {
	case notes.H2:
		return fontH2
	case notes.H3:
		return fontH3
	}
	return fontBody
}

// gapBefore is the space above block b after block prev (logical pixels):
// a heading stands apart, list items sit close, paragraphs have room.
func gapBefore(prev, b notes.Block) float32 {
	switch {
	case b.Kind == notes.H2:
		return 18
	case b.Kind == notes.H3:
		return 12
	case prev.Kind == notes.H2 || prev.Kind == notes.H3:
		return 6
	case prev.Kind.List() && b.Kind.List():
		return 3
	}
	return 10
}

// blockDisplay is a block's visible text and styles with emphasis markers
// (** * __ _) hidden, plus byte-offset maps between the stored Markdown
// (b.Text) and the drawn string.
type blockDisplay struct {
	text      string
	styles    []TextStyle
	srcToDisp []int // len(b.Text)+1
	dispToSrc []int // len(text)+1
}

// buildDisplay builds the drawn text, inline styles (bold, italic, mentions,
// done-task strike) and offset maps for one block.
func buildDisplay(b notes.Block) blockDisplay {
	faint, muted, text := col(ColFaint), col(ColMuted), col(ColText)
	clear := RGBA(ColText, 0)
	srcToDisp := make([]int, len(b.Text)+1)
	dispToSrc := make([]int, 0, len(b.Text)+1)
	var disp strings.Builder
	disp.Grow(len(b.Text))
	var out []TextStyle
	for _, sp := range notes.Inline(b.Text) {
		if sp.Role == notes.Marker && !sp.InMention {
			// Hide emphasis markers (** * __ _) completely.
			d := disp.Len()
			for k := sp.From; k < sp.To; k++ {
				srcToDisp[k] = d
			}
			continue
		}
		dFrom := disp.Len()
		for k := sp.From; k < sp.To; k++ {
			srcToDisp[k] = disp.Len()
			dispToSrc = append(dispToSrc, k)
			disp.WriteByte(b.Text[k])
		}
		dTo := disp.Len()
		st := TextStyle{From: dFrom, To: dTo, Bold: sp.Bold, Italic: sp.Italic}
		switch sp.Role {
		case notes.Marker:
			st.Color = &clear // the chip's brackets: room, not ink
		case notes.Mention:
			st.Bold, st.Color = true, &text
		case notes.Meta:
			st.Color = &faint
		}
		if st.Bold || st.Italic || st.Color != nil {
			out = append(out, st)
		}
	}
	srcToDisp[len(b.Text)] = disp.Len()
	dispToSrc = append(dispToSrc, len(b.Text))
	if b.Kind == notes.Task && b.Done && disp.Len() > 0 {
		out = append([]TextStyle{{From: 0, To: disp.Len(), Strike: true, Color: &muted}}, out...)
	}
	return blockDisplay{text: disp.String(), styles: out, srcToDisp: srcToDisp, dispToSrc: dispToSrc}
}

// editorLines lays a line out (nil for an empty one, or without metrics
// that can).
func editorLines(c *Ctx, s string, f Font, w float32, styles []TextStyle) *TextLines {
	if s == "" {
		return nil
	}
	if sm, ok := c.M.(StyledMetrics); ok {
		return sm.StyledLines(s, f, w, styles)
	}
	if lm, ok := c.M.(LineMetrics); ok {
		return lm.ParagraphLines(s, f, w)
	}
	return nil
}

func (n NotesEditor) Layout(c *Ctx, x, y, w float32) float32 {
	ed := n.Ed
	doc := ed.Doc
	nums := doc.Numbers()
	focused := c.Focus == n.ID
	selA, selB := ed.Sel()
	indentW, markW := c.Px(22), c.Px(22)
	geoms := make([]blockGeom, len(doc.Blocks))
	y0 := y
	laneH := c.Px(38) // 30 px buttons + 4 px padding above and below
	barSpace := laneH + c.Px(10)
	showBar := n.Toolbar && (!n.ToolbarOnFocus || focused)
	if showBar {
		y += barSpace
	}
	top := y
	for i, b := range doc.Blocks {
		if i > 0 {
			top += c.Px(gapBefore(doc.Blocks[i-1], b))
		}
		f := c.F(blockFont(b.Kind))
		inset := float32(0)
		if b.Kind.List() {
			inset = float32(b.Indent)*indentW + markW
		}
		tx, tw := x+inset, max(c.Px(40), w-inset)
		disp := buildDisplay(b)
		lines := editorLines(c, disp.text, f, tw, disp.styles)
		lineH := c.M.ParagraphHeight("Ag", f, c.Px(1000))
		h := lineH
		if lines != nil && len(lines.Lines) > 0 {
			last := lines.Lines[len(lines.Lines)-1]
			h = max(h, last.Top+last.H)
		}
		g := blockGeom{x: tx, top: top, w: tw, bottom: top + h, lines: lines, lineH: lineH, disp: disp}
		geoms[i] = g
		if c.Visible(RectF{x, top, x + w, top + h}) {
			n.drawBlock(c, i, b, g, f, nums[i], x+float32(b.Indent)*indentW, markW, focused, selA, selB)
		}
		top += h
	}
	height := max(top-y, c.Px(n.MinH))
	area := RectF{x, y, x + w, y + height}
	height += y - y0 // the toolbar's

	// The placeholder, on an empty page.
	if len(doc.Blocks) == 1 && doc.Blocks[0].Text == "" && doc.Blocks[0].Kind == notes.Para && n.Placeholder != "" {
		g := geoms[0]
		c.Draw(func(cv Canvas) {
			cv.Paragraph(n.Placeholder, RectF{g.x + c.Px(1), g.top, x + w, y + height}, c.F(fontBody), col(ColFaint))
		})
	}
	// The caret, steady (no blinking: nothing repaints while nothing
	// changes), and kept in view while typing or moving with keys.
	if focused {
		bi := min(ed.Caret.B, len(geoms)-1)
		g := geoms[bi]
		cx, ct, ch := g.caret(ed.Caret.O)
		if !ed.HasSel() {
			c.Draw(func(cv Canvas) {
				cv.RoundRect(RectF{cx, ct + c.Px(1), cx + max(1, c.Px(1.5)), ct + ch - c.Px(1)}, 0, col(ColText))
			})
			if suffix, ok := ed.MentionSuggestion(); ok && suffix != "" {
				f := c.F(blockFont(doc.Blocks[bi].Kind))
				sw := c.M.TextWidth(suffix, f)
				if ed.Caret.O == len(doc.Blocks[bi].Text) {
					c.Draw(func(cv Canvas) {
						cv.Text(suffix, RectF{cx + c.Px(2), ct, cx + c.Px(4) + sw, ct + ch}, f, col(ColFaint))
					})
				} else {
					// Text follows on the same line: show a compact ghost chip above.
					padX := c.Px(6)
					pr := RectF{cx, ct - ch - c.Px(2), cx + sw + 2*padX, ct - c.Px(2)}
					c.Draw(func(cv Canvas) {
						cv.RoundRect(pr, c.Px(5), col(ColControl))
						cv.Text(suffix, RectF{pr.L + padX, pr.T, pr.R - padX, pr.B}, f, col(ColMuted))
					})
				}
			}
		}
		if ed.Reveal {
			keepTop := ct
			if showBar {
				// Keep the caret clear below a docked toolbar (8 px top pad +
				// 38 px swim lane + 8 px breathing room, vs Frame's 24 px margin).
				keepTop = ct - c.Px(30)
			}
			c.KeepInView(keepTop, ct+ch)
		}
	}
	ed.Reveal = false

	posAt := func(px, py float32) notes.Pos {
		if len(geoms) == 0 {
			return notes.Pos{}
		}
		i := len(geoms) - 1
		for k, g := range geoms {
			if py < g.bottom+c.Px(4) {
				i = k
				break
			}
		}
		return notes.Pos{B: i, O: geoms[i].posAt(px, py)}
	}
	changed := func(rev int) {
		if ed.Rev != rev && n.OnChange != nil {
			n.OnChange()
		}
	}
	// vertical moves the caret d lines up or down, aiming at the goal x.
	// Keys can arrive faster than WM_PAINT rebuilds geoms (e.g. Enter then
	// Down/Up before a repaint), so ed.Caret.B may be past len(geoms)-1 or
	// ed.Doc.Blocks may have shrunk since this layout pass.
	vertical := func(d int, extend bool, env *EditorEnv) {
		if len(geoms) == 0 || len(ed.Doc.Blocks) == 0 {
			return
		}
		p := ed.Caret
		if p.B >= len(geoms) {
			// The caret sits in a block added after the last paint: treat each
			// unlaid-out block as one line until it steps back into geoms.
			if d > 0 {
				b := min(p.B+d, len(ed.Doc.Blocks)-1)
				ed.MoveTo(notes.Pos{B: b, O: len(ed.Doc.Blocks[b].Text)}, extend)
				return
			}
			extra := p.B - (len(geoms) - 1)
			if p.B+d >= len(geoms) {
				ed.MoveTo(notes.Pos{B: p.B + d, O: 0}, extend)
				return
			}
			d += extra
			p = notes.Pos{B: len(geoms) - 1, O: len(doc.Blocks[len(geoms)-1].Text)}
		}
		g := geoms[p.B]
		cx, _, _ := g.caret(p.O)
		if env.GoalX != nil && *env.GoalX >= 0 {
			cx = *env.GoalX
		}
		line := g.lineIndex(p.O) + d
		b := p.B
		for line < 0 && b > 0 {
			b--
			line += geoms[b].lineCount()
		}
		for b < len(geoms)-1 && line >= geoms[b].lineCount() {
			line -= geoms[b].lineCount()
			b++
		}
		var to notes.Pos
		switch {
		case line < 0:
			to = notes.Pos{B: 0, O: 0}
		case line >= geoms[b].lineCount():
			bi := min(b, len(ed.Doc.Blocks)-1)
			to = notes.Pos{B: bi, O: len(ed.Doc.Blocks[bi].Text)}
		default:
			gb := geoms[b]
			ly := gb.top + gb.lineH/2
			if gb.lines != nil && line < len(gb.lines.Lines) {
				ln := gb.lines.Lines[line]
				ly = gb.top + ln.Top + ln.H/2
			}
			to = notes.Pos{B: b, O: gb.posAt(cx, ly)}
		}
		ed.MoveTo(to, extend)
		if env.GoalX != nil {
			*env.GoalX = cx
		}
	}
	var expandTop float32
	if n.Toolbar && n.ToolbarOnFocus {
		expandTop = barSpace
	}
	c.Editor(EditorTarget{
		ID: n.ID, R: area, WantFocus: n.Focus, ExpandTop: expandTop,
		Press: func(px, py float32, shift bool, clicks int) {
			p := posAt(px, py)
			switch clicks {
			case 2:
				ed.SelectWord(p)
			case 3:
				ed.SelectBlock(p)
			default:
				ed.MoveTo(p, shift)
			}
		},
		Drag: func(px, py float32) { ed.MoveTo(posAt(px, py), true); ed.Reveal = true },
		Char: func(r rune, env *EditorEnv) {
			rev := ed.Rev
			ed.Type(string(r))
			ed.Reveal = true
			if env.GoalX != nil {
				*env.GoalX = -1
			}
			changed(rev)
		},
		Key: func(vk uintptr, m Mods, env *EditorEnv) bool {
			rev := ed.Rev
			ctrl := m.Ctrl && !m.Alt // AltGr is Ctrl+Alt: characters, not shortcuts
			keepGoal := false
			switch {
			case vk == vkLeft:
				prevB := ed.Caret.B
				prevD := -1
				if !ctrl && prevB < len(geoms) {
					prevD = geoms[prevB].toDisp(ed.Caret.O)
				}
				ed.Left(ctrl, m.Shift)
				if !ctrl && prevD >= 0 && ed.Caret.B == prevB && ed.Caret.B < len(geoms) {
					for ed.Caret.B == prevB && ed.Caret.O > 0 && geoms[prevB].toDisp(ed.Caret.O) == prevD {
						ed.Left(false, m.Shift)
					}
				}
			case vk == vkRight:
				prevB := ed.Caret.B
				prevD := -1
				if !ctrl && prevB < len(geoms) {
					prevD = geoms[prevB].toDisp(ed.Caret.O)
				}
				ed.Right(ctrl, m.Shift)
				if !ctrl && prevD >= 0 && ed.Caret.B == prevB && ed.Caret.B < len(geoms) {
					g := geoms[prevB]
					// Skip past hidden emphasis markers so each Right press moves one visible char.
					if g.toDisp(ed.Caret.O) == prevD {
						for ed.Caret.B == prevB && ed.Caret.O < len(ed.Doc.Blocks[prevB].Text) && g.toDisp(ed.Caret.O) == prevD {
							ed.Right(false, m.Shift)
						}
					}
					if ed.Caret.B == prevB {
						d := g.toDisp(ed.Caret.O)
						if target := g.toSrc(d); target > ed.Caret.O {
							ed.MoveTo(notes.Pos{B: prevB, O: target}, m.Shift)
						}
					}
				}
			case vk == vkUp || vk == vkDown:
				d := -1
				if vk == vkDown {
					d = 1
				}
				vertical(d, m.Shift, env)
				keepGoal = true
			case vk == vkPrior || vk == vkNext:
				d := -12
				if vk == vkNext {
					d = 12
				}
				vertical(d, m.Shift, env)
				keepGoal = true
			case vk == vkHome:
				ed.Home(ctrl, m.Shift)
			case vk == vkEnd:
				ed.End(ctrl, m.Shift)
			case vk == vkBack:
				ed.Backspace(ctrl)
			case vk == vkDelete:
				ed.Delete(ctrl)
			case vk == vkReturn && ctrl:
				ed.ToggleDone(ed.Caret.B)
			case vk == vkReturn:
				ed.Enter()
			case vk == vkTab:
				if !m.Shift && ed.AcceptMentionSuggestion() {
					break
				}
				d := 1
				if m.Shift {
					d = -1
				}
				ed.Indent(d)
			case vk == vkEscape:
				if !ed.ConfirmMention() && env.Blur != nil {
					env.Blur()
				}
			case ctrl && vk == 'A':
				ed.SelectAll()
			case ctrl && (vk == 'C' || vk == 'X'):
				copyNotes(ed.Selected(), m.Shift, env)
				if vk == 'X' {
					ed.Cut()
				}
			case ctrl && vk == 'V':
				if env.Clipboard != nil {
					ed.Paste(env.Clipboard())
				}
			case ctrl && vk == 'Z' && !m.Shift:
				ed.Undo()
			case ctrl && (vk == 'Y' || vk == 'Z'):
				ed.Redo()
			case ctrl && vk == 'B':
				ed.Emphasize("**")
			case ctrl && vk == 'I':
				ed.Emphasize("*")
			default:
				return false
			}
			if !keepGoal && env.GoalX != nil {
				*env.GoalX = -1
			}
			ed.Reveal = true
			changed(rev)
			return true
		},
	})
	if showBar {
		clipTop := c.ClipRect().T
		barY := y0
		if clipTop > -1e5 {
			// Dock the toolbar with 8 px top padding when scrolled past the
			// top of the container, stopping at the bottom of the text area.
			barY = max(y0, min(clipTop+c.Px(8), area.B-laneH))
		}
		stuck := barY > y0
		n.toolbar(c, x, barY, w, laneH, y0, area.B, clipTop, stuck, changed)
	}
	// Checkboxes, over the editor: a click ticks the task.
	for i, b := range doc.Blocks {
		if b.Kind != notes.Task {
			continue
		}
		g := geoms[i]
		box := RectF{g.x - markW, g.top, g.x - c.Px(2), g.top + g.lineH}
		i := i
		c.Click(fmt.Sprintf("%s:box:%d", n.ID, i), box, func() {
			rev := ed.Rev
			ed.ToggleDone(i)
			changed(rev)
		})
	}
	return height
}

// toolbar draws the formatting buttons inside a full-width swim lane inside
// the container; when scrolled past its resting top (stuck), it stays docked
// at the top of the view with a soft card-coloured backing so scrolled text
// recedes cleanly behind it.
func (n NotesEditor) toolbar(c *Ctx, x, y, w, laneH, y0, areaB, clipTop float32, stuck bool, changed func(int)) {
	pad := c.Px(4)
	lane := RectF{x, y, x + w, y + laneH}
	rad := c.Px(10)
	border := max(1, c.Px(1))
	if stuck {
		cardPad := c.Px(16)
		cardRect := RectF{x - cardPad, y0 - cardPad, x + w + cardPad, areaB + cardPad}
		fade := c.Px(18)
		halo := RectF{lane.L - fade, lane.T - fade, lane.R + fade, lane.B + fade}
		hw, hh := int(math.Ceil(float64(halo.W()))), int(math.Ceil(float64(halo.H())))
		key := fmt.Sprintf("toolhalo:%dx%d:%v", hw, hh, fade)
		c.PushClip(cardRect)
		c.Draw(func(cv Canvas) {
			// Cover the 8 px top margin above the docked lane so scrolled lines
			// never peek through above the toolbar.
			if clipTop < lane.T+rad {
				cv.RoundRect(RectF{x - c.Px(4), clipTop, x + w + c.Px(4), lane.T + rad}, 0, col(ColCard))
			}
			cv.Bitmap(key, halo.L, halo.T, hw, hh, 1, func() []byte {
				return haloPixels(hw, hh, float64(fade), float64(rad), ColCard, 0.85)
			})
		})
		c.PopClip()
	}
	c.Draw(func(cv Canvas) {
		if stuck {
			cv.RoundRect(RectF{lane.L - border, lane.T - border, lane.R + border, lane.B + border}, rad+border, col(ColEdge))
		} else {
			cv.RoundRect(RectF{lane.L - border, lane.T - border, lane.R + border, lane.B + border}, rad+border, col(ColDivider))
		}
		cv.RoundRect(lane, rad, col(ColSidebar))
	})
	// Clicking empty space in the swim lane (or the docked top margin) keeps
	// focus in the editor and blocks clicks from reaching text beneath.
	hitRect := lane
	if stuck && clipTop < lane.T {
		hitRect.T = clipTop
	}
	c.Click(n.ID+":bar", hitRect, func() {})

	bw, bh := c.Px(30), c.Px(30)
	bx, by := x+pad, y+(laneH-bh)/2
	focused := c.Focus == n.ID
	for _, t := range notesTools {
		if t.gapBefore {
			bx += c.Px(10)
		}
		r := RectF{bx, by, bx + bw, by + bh}
		id := n.ID + ":tool:" + t.id
		hover := c.Hover == id
		active := focused && t.active != nil && t.active(n.Ed)
		glyph := t.glyph
		c.Draw(func(cv Canvas) {
			switch {
			case hover:
				cv.RoundRect(r, c.Px(7), col(ColControlHi))
			case active:
				cv.RoundRect(r, c.Px(7), col(ColControl))
			}
			k := col(ColMuted)
			if hover || active {
				k = col(ColText)
			}
			DrawIcon(cv, glyph, r, c.Px(17), k)
		})
		act := t.act
		c.Click(id, r, func() {
			rev := n.Ed.Rev
			act(n.Ed)
			n.Ed.Reveal = true
			changed(rev)
		})
		bx += bw + c.Px(2)
	}
	if n.OnPin != nil {
		pr := RectF{x + w - pad - bw, by, x + w - pad, by + bh}
		pinID := n.ID + ":tool:pin"
		pinHover := c.Hover == pinID
		pinned := n.Pinned
		c.Draw(func(cv Canvas) {
			switch {
			case pinHover:
				cv.RoundRect(pr, c.Px(7), col(ColControlHi))
			case pinned:
				cv.RoundRect(pr, c.Px(7), col(ColControl))
			}
			k := col(ColMuted)
			if pinHover || pinned {
				k = col(ColText)
			}
			DrawIcon(cv, GlyphPin, pr, c.Px(17), k)
		})
		c.Click(pinID, pr, n.OnPin)

		if pinned {
			// Six dots arranged 3 wide × 2 high directly to the left of the pin
			// button: FloatWindow's WM_NCHITTEST turns this area into HTCAPTION.
			gw := c.Px(26)
			gr := RectF{pr.L - c.Px(4) - gw, by, pr.L - c.Px(4), by + bh}
			dragID := n.ID + ":drag"
			dragHover := c.Hover == dragID
			c.Draw(func(cv Canvas) {
				k := col(ColFaint)
				if dragHover {
					k = col(ColMuted)
				}
				step := c.Px(5)
				rad := max(1.2, c.Px(1.5))
				cx := (gr.L + gr.R) / 2
				cy := (gr.T + gr.B) / 2
				for row := range 2 {
					dy := (float32(row) - 0.5) * step
					for colIdx := range 3 {
						dx := (float32(colIdx) - 1) * step
						cv.Circle(cx+dx, cy+dy, rad, k)
					}
				}
			})
			c.HoverArea(dragID, gr)
		}
	}
}

// drawBlock draws one block: selection, mention chips, its marker, its text.
func (n NotesEditor) drawBlock(c *Ctx, i int, b notes.Block, g blockGeom, f Font, num int, markX, markW float32, focused bool, selA, selB notes.Pos) {
	ed := n.Ed
	var sel []RectF
	if ed.HasSel() && i >= selA.B && i <= selB.B {
		from, to := 0, len(b.Text)
		if i == selA.B {
			from = selA.O
		}
		if i == selB.B {
			to = selB.O
		}
		if g.lines != nil {
			for _, r := range g.lines.Rects(g.toDisp(from), g.toDisp(to)) {
				sel = append(sel, RectF{g.x + r.L, g.top + r.T, g.x + r.R, g.top + r.B})
			}
		}
		if i < selB.B { // the line's end is selected too: a sliver for it
			ex, et, eh := g.caret(len(b.Text))
			sel = append(sel, RectF{ex, et, ex + c.Px(5), et + eh})
		}
	}
	var chips []RectF
	editing := false
	for _, m := range notes.Mentions(b.Text) {
		if g.lines == nil {
			break
		}
		if focused && ed.Caret.B == i && ed.Caret.O > m.From && ed.Caret.O < m.To {
			editing = true
		}
		for _, r := range g.lines.Rects(g.toDisp(m.From), g.toDisp(m.To)) {
			chips = append(chips, RectF{g.x + r.L - c.Px(1), g.top + r.T + c.Px(1), g.x + r.R + c.Px(1), g.top + r.B - c.Px(1)})
		}
	}
	hoverBox := c.Hover == fmt.Sprintf("%s:box:%d", n.ID, i)
	selColor := RGBA(ColText, 0.22)
	if !focused {
		selColor = RGBA(ColText, 0.12)
	}
	c.Draw(func(cv Canvas) {
		for _, r := range sel {
			cv.RoundRect(r, c.Px(2), selColor)
		}
		chip := RGBA(ColText, 0.09)
		if editing {
			chip = RGBA(ColText, 0.15)
		}
		for _, r := range chips {
			cv.RoundRect(r, c.Px(5), chip)
		}
		line := RectF{markX, g.top, markX + markW, g.top + g.lineH}
		switch b.Kind {
		case notes.Bullet:
			dot := []string{"•", "◦", "▪"}[b.Indent%3]
			cv.Text(dot, RectF{line.L, line.T, line.R - c.Px(6), line.B}, Font{Family: f.Family, Size: f.Size, Weight: 600, Center: true}, col(ColMuted))
		case notes.Numbered:
			cv.Text(fmt.Sprintf("%d.", num), RectF{line.L - c.Px(10), line.T, line.R - c.Px(5), line.B}, Font{Family: f.Family, Size: f.Size, Weight: f.Weight, Right: true}, col(ColMuted))
		case notes.Task:
			icon, color := GlyphTask, col(ColMuted)
			if b.Done {
				icon = GlyphTaskDone
			}
			if hoverBox {
				color = col(ColText)
			}
			DrawIcon(cv, icon, RectF{line.L, line.T, line.R - c.Px(4), line.B}, c.Px(16), color)
		}
		if g.disp.text != "" {
			cv.StyledParagraph(g.disp.text, RectF{g.x, g.top, g.x + g.w, g.bottom + c.Px(2)}, f, col(ColText), g.disp.styles)
		}
	})
}

// copyNotes puts a selection on the clipboard: Markdown (and HTML for apps
// that paste formatting), or plain text.
func copyNotes(d notes.Doc, plain bool, env *EditorEnv) {
	if len(d.Blocks) == 0 || env.SetClipboard == nil {
		return
	}
	if plain {
		env.SetClipboard(strings.TrimSuffix(d.Plain(), "\n"), "")
		return
	}
	env.SetClipboard(strings.TrimSuffix(d.Markdown(), "\n"), d.HTML())
}
