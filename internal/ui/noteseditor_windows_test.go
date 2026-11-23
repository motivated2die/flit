package ui

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/motivated2die/flit/internal/notes"
)

// The notes editor drawn by DirectWrite, focused, with every kind of block
// (FLIT_RENDER_DIR=<folder> saves notes-editor.png). Without the folder it
// still lays out and paints with the real metrics.
func TestNotesEditorPicture(t *testing.T) {
	md := "## Release plan\n\nWe ship on **Friday** if the *review* passes. A longer line that wraps onto a second line to show how wrapped text and its caret stops line up.\n\n" +
		"- Budget is 25 € [@flit 12:40: was the budget final or only proposed?]\n  - check with finance\n    - before the call\n" +
		"1. Draft the changelog\n2. Send it round\n\n### Tasks\n\n- [ ] Book the room\n- [x] Invite the team\n"
	ed := notes.NewEditor(notes.Parse(md))
	ed.MoveTo(notes.Pos{B: 1, O: 12}, false)
	ed.MoveTo(notes.Pos{B: 1, O: 20}, true)
	layout := func(c *Ctx, w, h float32) {
		c.Focus = "notes"
		Card{Items: []Node{NotesEditor{ID: "notes", Ed: ed, Placeholder: "Notes"}}}.Layout(c, c.Px(16), c.Px(16), w-c.Px(32))
	}
	s, err := render(layout, 640, 560, 1, col(ColBase), "", nil)
	if err != nil {
		t.Skip("no Direct2D:", err)
	}
	defer s.Free()
	if dir := os.Getenv("FLIT_RENDER_DIR"); dir != "" {
		f, _ := os.Create(filepath.Join(dir, "notes-editor.png"))
		png.Encode(f, SurfaceImage(s))
		f.Close()
	}
	// With real metrics, bold text is wider than regular: the caret stops
	// come from the styled layout.
	m, _ := D2DMetrics()
	sm := m.(StyledMetrics)
	plain := sm.StyledLines("wwww", fontBody, 500, nil)
	bold := sm.StyledLines("wwww", fontBody, 500, []TextStyle{{From: 0, To: 4, Bold: true}})
	if pl, bl := plain.Lines[0].Stops, bold.Lines[0].Stops; bl[len(bl)-1].X <= pl[len(pl)-1].X {
		t.Errorf("bold %v not wider than regular %v", bl[len(bl)-1].X, pl[len(pl)-1].X)
	}
}
