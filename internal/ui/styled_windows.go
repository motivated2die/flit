package ui

import (
	"fmt"
	"strings"
	"unsafe"
)

// Styled paragraphs: one DirectWrite text layout with weight, italics and
// strikethrough per range, used both to draw a paragraph and to find its
// caret stops, so a caret after bold text sits where the text ends.

const (
	tlSetFontWeight    = 32 // IDWriteTextLayout::SetFontWeight(weight, range)
	tlSetFontStyle     = 33 // SetFontStyle(style, range)
	tlSetStrikethrough = 37 // SetStrikethrough(BOOL, range)
	dwriteFontItalic   = 2
)

// styledLayout is s laid out wrapped to w in f with styles applied
// (nil if it couldn't be made; release it).
func (g *d2d) styledLayout(s string, f Font, w, h float32, styles []TextStyle) *comObj {
	tf, err := g.formatFor(fmtKey{Font: f, wrap: true})
	if err != nil || s == "" {
		return nil
	}
	u := utf16Units(s)
	var tl *comObj
	if hr(g.dwrite.call(dwCreateTextLayout, uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1), uintptr(unsafe.Pointer(tf)),
		f32(w), f32(max(h, 1)), uintptr(unsafe.Pointer(&tl))), "CreateTextLayout") != nil {
		return nil
	}
	for _, st := range styles {
		from, to := utf16Index(s, st.From), utf16Index(s, st.To)
		if to <= from {
			continue
		}
		rng := uintptr(uint32(from)) | uintptr(uint32(to-from))<<32 // DWRITE_TEXT_RANGE, by value
		if st.Bold {
			tl.call(tlSetFontWeight, 700, rng)
		}
		if st.Italic {
			tl.call(tlSetFontStyle, dwriteFontItalic, rng)
		}
		if st.Strike {
			tl.call(tlSetStrikethrough, 1, rng)
		}
	}
	return tl
}

// styleKey is a styled paragraph's cache key.
func styleKey(s string, f Font, w float32, styles []TextStyle) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s|%v|%g|%g|", f.Family, f.Weight, f.Size, w)
	for _, st := range styles {
		if st.Bold || st.Italic || st.Strike {
			fmt.Fprintf(&b, "%d-%d-%t%t%t;", st.From, st.To, st.Bold, st.Italic, st.Strike)
		}
	}
	return b.String() + "|" + s
}

// StyledLines is ParagraphLines for a styled paragraph (colours don't
// change metrics and are ignored).
func (g *d2d) StyledLines(s string, f Font, width float32, styles []TextStyle) *TextLines {
	k := styleKey(s, f, width, styles)
	if t, ok := g.styledLines[k]; ok {
		return t
	}
	t := &TextLines{}
	if tl := g.styledLayout(s, f, width, 1e6, styles); tl != nil {
		u := utf16Units(s)
		t = layoutLines(tl, s, len(u)-1)
		tl.release()
	}
	if g.styledLines == nil || len(g.styledLines) > 4096 {
		g.styledLines = map[string]*TextLines{}
	}
	g.styledLines[k] = t
	return t
}

// StyledParagraph draws s wrapped like Paragraph, with styles: weights,
// italics, strikethrough and colours per range.
func (p *Painter) StyledParagraph(s string, r RectF, f Font, c ColorF, styles []TextStyle) {
	tl := p.g.styledLayout(s, f, r.W(), r.H(), styles)
	if tl == nil {
		return
	}
	defer tl.release()
	for _, st := range styles {
		if st.Color == nil {
			continue
		}
		from, to := utf16Index(s, st.From), utf16Index(s, st.To)
		if to <= from {
			continue
		}
		rng := uintptr(uint32(from)) | uintptr(uint32(to-from))<<32
		var b *comObj
		col := *st.Color
		if hr(p.g.rt.call(rtCreateSolidColorBrush, uintptr(unsafe.Pointer(&col)), 0, uintptr(unsafe.Pointer(&b))), "CreateSolidColorBrush") != nil {
			continue
		}
		tl.call(tlSetDrawingEffect, uintptr(unsafe.Pointer(b)), rng) // the layout keeps its own reference
		b.release()
	}
	p.g.rt.call(rtDrawTextLayout, pt(r.L, r.T), uintptr(unsafe.Pointer(tl)), p.brush(c), 0)
}
