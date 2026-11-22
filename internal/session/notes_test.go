package session

import (
	"os"
	"testing"
	"time"

	"github.com/motivated2die/flit/internal/notes"
)

func TestNotesSaveLoad(t *testing.T) {
	s, err := Create(t.TempDir(), Meta{Provider: "teams", Language: "de", Started: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if d, err := s.Notes(); err != nil || len(d.Blocks) != 0 {
		t.Fatalf("no notes yet: %+v, %v", d, err)
	}
	d := notes.Doc{Blocks: []notes.Block{
		{Kind: notes.H2, Text: "Agenda", Typed: notes.Unknown},
		{Kind: notes.Bullet, Text: "Budget [@flit 3:00: final?]", Typed: 3 * time.Minute},
		{Kind: notes.Task, Text: "send slides", Typed: 95 * time.Second},
	}}
	if err := s.SaveNotes(d); err != nil {
		t.Fatal(err)
	}
	md, _ := os.ReadFile(s.Path(NotesFile))
	if string(md) != "## Agenda\n\n- Budget [@flit 3:00: final?]\n- [ ] send slides\n" {
		t.Errorf("notes.md %q", md)
	}
	got, err := s.Notes()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Blocks) != 3 || got.Blocks[0].Typed != notes.Unknown || got.Blocks[1].Typed != 3*time.Minute || got.Blocks[2].Typed != 95*time.Second {
		t.Errorf("read back %+v", got.Blocks)
	}
	// Edited outside flit: the lines that are unchanged keep their times.
	os.WriteFile(s.Path(NotesFile), []byte("## Agenda\n\n- new line\n- [ ] send slides\n"), 0o644)
	got, _ = s.Notes()
	if got.Blocks[1].Typed != notes.Unknown || got.Blocks[2].Typed != 95*time.Second {
		t.Errorf("after an outside edit %+v", got.Blocks)
	}
	// Empty notes leave no files behind.
	if err := s.SaveNotes(notes.Doc{Blocks: []notes.Block{{Kind: notes.Para}}}); err != nil {
		t.Fatal(err)
	}
	if s.Has(NotesFile) || s.Has(NotesTimesFile) {
		t.Error("empty notes kept their files")
	}
}
