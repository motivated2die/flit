package app

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/motivated2die/flit/internal/capture"
	"github.com/motivated2die/flit/internal/notes"
	"github.com/motivated2die/flit/internal/session"
	"github.com/motivated2die/flit/internal/ui"
)

// typeInto drives the editor of the current page like the window does:
// characters, and "\n" as Enter.
func typeInto(t *testing.T, a *App, id, text string) {
	t.Helper()
	c, err := ui.LayoutFrame(a.frame(), 960, 640, 1, "notes:"+id)
	if err != nil {
		t.Skip("no Direct2D:", err)
	}
	ed, ok := c.EditorTarget("notes:" + id)
	if !ok {
		t.Fatal("no notes editor on the page")
	}
	goal := float32(-1)
	env := &ui.EditorEnv{GoalX: &goal, Blur: func() {}}
	for _, r := range text {
		if r == '\n' {
			ed.Key(0x0D, ui.Mods{}, env)
			continue
		}
		ed.Char(r, env)
	}
}

func TestNotesDuringRecording(t *testing.T) {
	a := testApp(t)
	s := a.sessions[0]
	a.rec = &recording{sess: s, r: &capture.Recording{}, started: time.Now().Add(-(12*time.Minute + 40*time.Second))}

	// The notch while recording offers Notes beside Stop.
	a.onNotchButton("notes") // no window in tests: openPage only navigates
	if a.page != pageSession+s.Meta.ID || a.focusNotes != s.Meta.ID {
		t.Fatalf("page %q, focus %q", a.page, a.focusNotes)
	}
	f := a.frame()
	if a.focusNotes != "" {
		t.Error("the focus request wasn't used up by the page")
	}
	c, err := ui.LayoutFrame(f, 960, 640, 1, "")
	if err != nil {
		t.Skip("no Direct2D:", err)
	}
	if tg, ok := c.EditorTarget("notes:" + s.Meta.ID); !ok || !tg.WantFocus {
		t.Fatalf("the editor didn't ask for the keyboard: %v", ok)
	}

	// Enter confirms the mention (the caret stays on its line); the next
	// Enter ends the line.
	typeInto(t, a, s.Meta.ID, "## Budget\nfinal at 25 € @flit was this decided or only proposed?\n\n[] send the slides")
	a.saveAllNotes()
	md, err := os.ReadFile(s.Path(session.NotesFile))
	if err != nil {
		t.Fatal(err)
	}
	want := "## Budget\n\nfinal at 25 € [@flit 12:40: was this decided or only proposed?]\n\n- [ ] send the slides\n"
	if string(md) != want {
		t.Errorf("notes.md:\n%q\nwant\n%q", md, want)
	}
	// Each line remembers when it was typed (about 12:40 into the meeting).
	doc, _ := s.Notes()
	for _, b := range doc.Blocks {
		if b.Typed < 12*time.Minute || b.Typed > 14*time.Minute {
			t.Errorf("%q typed at %v", b.Text, b.Typed)
		}
	}
	if dir := os.Getenv("FLIT_RENDER_DIR"); dir != "" {
		img, err := ui.RenderFrameFocused(a.frame(), 960, 640, 1, "notes:"+s.Meta.ID, nil)
		if err == nil {
			fh, _ := os.Create(filepath.Join(dir, "window-session-notes.png"))
			png.Encode(fh, img)
			fh.Close()
		}
		a.page = pageHome
		img, err = ui.RenderFrame(a.frame(), 960, 640, 1, "home:live", nil)
		if err == nil {
			fh, _ := os.Create(filepath.Join(dir, "window-home-recording-notes.png"))
			png.Encode(fh, img)
			fh.Close()
		}
	}
	// Home's recording card opens the running meeting's notes.
	a.page = pageHome
	c, _ = ui.LayoutFrame(a.frame(), 960, 640, 1, "")
	hit, ok := c.HitByID("home:live")
	if !ok {
		t.Fatal("no home:live on Home")
	}
	hit.OnClick()
	if a.page != pageSession+s.Meta.ID {
		t.Errorf("home:live went to %q", a.page)
	}
}

func TestNotesAfterRecordingHaveNoTimes(t *testing.T) {
	a := testApp(t)
	s := a.sessions[0]
	a.go_(pageSession + s.Meta.ID)
	typeInto(t, a, s.Meta.ID, "afterwards")
	a.go_(pageHome) // leaving the page saves
	doc, _ := s.Notes()
	if len(doc.Blocks) != 1 || doc.Blocks[0].Text != "afterwards" || doc.Blocks[0].Typed != notes.Unknown {
		t.Errorf("notes %+v", doc.Blocks)
	}
	if s.Has(session.NotesTimesFile) {
		if b, _ := os.ReadFile(s.Path(session.NotesTimesFile)); strings.Contains(string(b), "afterwards") {
			t.Error("a time for a line typed after the meeting")
		}
	}
	// Deleting the recording forgets its notes.
	a.deleteRecording(s)
	if _, ok := a.notes[s.Meta.ID]; ok {
		t.Error("notes kept for a deleted recording")
	}
}
