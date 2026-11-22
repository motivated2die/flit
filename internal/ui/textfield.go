package ui

import (
	"strings"
	"time"
	"unicode"
)

// Editing is the text being edited in a TextField: the window keeps it
// between layout passes and feeds it keys.
type Editing struct {
	ID    string
	Text  []rune
	Caret int  // in runes
	All   bool // everything selected (typing replaces it)
	Since time.Time
	Max   int    // runes; 0 = no limit
	Orig  string // the value before editing (Esc goes back to it)
}

// Insert types s at the caret (replacing a full selection). Line breaks
// and control characters are dropped: a field is one line.
func (e *Editing) Insert(s string) {
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	if e.All {
		e.Text, e.Caret, e.All = nil, 0, false
	}
	in := []rune(s)
	if e.Max > 0 {
		in = in[:max(0, min(len(in), e.Max-len(e.Text)))]
	}
	e.Text = append(e.Text[:e.Caret], append(in, e.Text[e.Caret:]...)...)
	e.Caret += len(in)
}

// Backspace deletes before the caret (or the selection).
func (e *Editing) Backspace() {
	if e.All {
		e.Text, e.Caret, e.All = nil, 0, false
		return
	}
	if e.Caret > 0 {
		e.Text = append(e.Text[:e.Caret-1], e.Text[e.Caret:]...)
		e.Caret--
	}
}

// Delete deletes after the caret (or the selection).
func (e *Editing) Delete() {
	if e.All {
		e.Backspace()
		return
	}
	if e.Caret < len(e.Text) {
		e.Text = append(e.Text[:e.Caret], e.Text[e.Caret+1:]...)
	}
}

// Move puts the caret at pos (clamped), ending a selection.
func (e *Editing) Move(pos int) {
	if e.All {
		e.All = false
	}
	e.Caret = max(0, min(len(e.Text), pos))
}

// TextField is a one-line text input: a click starts editing with all
// text selected; Enter, or clicking anywhere else, saves (OnCommit); Esc
// cancels. While not edited it shows Value, or Placeholder when empty.
type TextField struct {
	ID          string
	Value       string
	Placeholder string
	W           float32 // logical pixels; 0 = all the width there is
	Round       bool    // fully rounded ends (a pill), as inside another pill
	H           float32 // logical pixels; 0 = 32
	MaxLen      int
	// Title shows the field as a page title that is edited in place: no
	// box until hovered or edited.
	Title bool
	// Glyph sits before the text (a search field's magnifier).
	Glyph    string
	OnCommit func(string)
	// OnChange, when set, hears the text as it is typed (a search that
	// filters while typing); Esc gives it the old value back.
	OnChange func(string)
	// OnClear, when set, puts a small × at the right end while there is
	// text, which empties the field.
	OnClear func()
	// Focus starts editing the field as it is shown (once).
	Focus bool
	// Next is an editor (Ctx.Editor's ID) that gets the keyboard when
	// typing here ends with Enter or Tab: a recording's title, then its
	// notes.
	Next string
}

// Width is W, or 220 logical pixels (as a SettingRow control).
func (t TextField) Width(c *Ctx) float32 {
	if t.W > 0 {
		return c.Px(t.W)
	}
	return c.Px(220)
}

func (t TextField) Layout(c *Ctx, x, y, w float32) float32 {
	if t.W > 0 {
		w = min(w, c.Px(t.W))
	}
	h := c.Px(32)
	if t.H > 0 {
		h = c.Px(t.H)
	}
	pad := c.Px(10)
	f := c.F(fontBody)
	if t.Title {
		h, f = c.Px(42), c.F(fontTitle)
		x -= pad // the text lines up with the page, the box reaches out
		w += pad
	}
	r := RectF{x, y, x + w, y + h}
	radius := c.Px(8)
	if t.Round {
		radius = h / 2
	}
	lead := float32(0) // room for the glyph before the text
	if t.Glyph != "" {
		lead = c.Px(22)
	}
	var edit *Editing
	if c.Edit != nil && c.Edit.ID == t.ID {
		edit = c.Edit
		c.Animating = true // the caret blinks
	}
	hover := c.Hover == t.ID
	text, color := t.Value, col(ColText)
	if edit != nil {
		text = string(edit.Text)
	} else if text == "" {
		text, color = t.Placeholder, col(ColFaint)
	}
	clear := RectF{}
	hasText := t.Value != "" || (edit != nil && len(edit.Text) > 0)
	if t.OnClear != nil && hasText {
		clear = RectF{r.R - c.Px(30), r.T, r.R - c.Px(4), r.B}
	}
	// Keep the caret in view: shift long text left.
	var shift, caretX, selW float32
	if edit != nil {
		caretX = c.M.TextWidth(string(edit.Text[:edit.Caret]), f)
		if room := w - 2*pad - lead - clear.W(); caretX > room {
			shift = caretX - room
		}
		if edit.All {
			selW = c.M.TextWidth(text, f)
		}
	}
	blinkOn := edit != nil && c.Now.Sub(edit.Since)%time.Second < 600*time.Millisecond
	c.Draw(func(cv Canvas) {
		bg := col(ColControl)
		if edit != nil || hover {
			bg = col(ColControlHi)
		}
		switch {
		case t.Title && edit != nil:
			cv.RoundRect(r, c.Px(8), col(ColControl))
		case t.Title && hover:
			cv.RoundRect(r, c.Px(8), RGBA(ColCardHover, 0.8))
		case !t.Title:
			cv.RoundRect(r, radius, bg)
		}
		if edit != nil {
			cv.RoundRect(RectF{r.L + radius, r.B - max(1, c.Px(1.5)), r.R - radius, r.B}, 0, RGBA(ColText, 0.7))
		}
		if t.Glyph != "" {
			gc := col(ColFaint)
			if edit != nil || hover {
				gc = col(ColMuted)
			}
			DrawIcon(cv, t.Glyph, RectF{r.L + pad - c.Px(2), r.T, r.L + pad + c.Px(16), r.B}, c.Px(16), gc)
		}
		if clear.W() > 0 {
			gc := col(ColMuted)
			if c.Hover == t.ID+":clear" {
				gc = col(ColText)
			}
			DrawIcon(cv, glyphClose, clear, c.Px(14), gc)
		}
		inner := RectF{r.L + pad + lead, r.T, r.R - pad - clear.W(), r.B}
		cv.PushClip(inner)
		tx := inner.L - shift
		if selW > 0 {
			cv.RoundRect(RectF{tx - c.Px(1), r.T + c.Px(7), tx + selW + c.Px(1), r.B - c.Px(7)}, c.Px(3), RGBA(ColText, 0.22))
		}
		cv.Text(text, RectF{tx, r.T, tx + max(inner.W(), c.M.TextWidth(text, f)+c.Px(4)), r.B}, f, color)
		if t.Title && hover && edit == nil {
			// A pencil after the title says it can be renamed.
			px := tx + c.M.TextWidth(text, f) + c.Px(10)
			DrawIcon(cv, GlyphEdit, RectF{px, r.T, px + c.Px(20), r.B}, c.Px(16), col(ColMuted))
		}
		if blinkOn {
			cx := tx + caretX
			cv.RoundRect(RectF{cx, r.T + c.Px(8), cx + max(1, c.Px(1.5)), r.B - c.Px(8)}, 0, col(ColText))
		}
		cv.PopClip()
	})
	if c.StartEdit != nil {
		value, max_ := t.Value, t.MaxLen
		c.Click(t.ID, r, func() { c.StartEdit(t.ID, value, max_) })
		c.inputs = append(c.inputs, input{id: t.ID, commit: t.OnCommit, change: t.OnChange, next: t.Next})
		if t.Focus && edit == nil {
			c.wantEdit = &input{id: t.ID, value: value, max: max_}
		}
	}
	if clear.W() > 0 {
		c.Click(t.ID+":clear", clear, t.OnClear) // over the field
	}
	return h
}

type input struct {
	id     string
	commit func(string)
	change func(string)
	next   string // the editor after Enter or Tab
	// A field asking to be edited (wantEdit): its value and length.
	value string
	max   int
}

// nextOf is the editor that follows field id (Enter, Tab), or "".
func (c *Ctx) nextOf(id string) string {
	for _, in := range c.inputs {
		if in.id == id {
			return in.next
		}
	}
	return ""
}

// changer returns the OnChange of field id in this pass.
func (c *Ctx) changer(id string) func(string) {
	for _, in := range c.inputs {
		if in.id == id {
			return in.change
		}
	}
	return nil
}

// committer returns the OnCommit of field id in this pass.
func (c *Ctx) committer(id string) func(string) {
	for _, in := range c.inputs {
		if in.id == id {
			return in.commit
		}
	}
	return nil
}
