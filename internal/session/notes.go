package session

import (
	"encoding/json"
	"errors"
	"os"

	"github.com/motivated2die/flit/internal/notes"
)

// Notes reads the recording's notes: notes.md, with the lines' typing
// times from notes.json (an empty doc when there are none).
func (s *Session) Notes() (notes.Doc, error) {
	b, err := os.ReadFile(s.Path(NotesFile))
	if errors.Is(err, os.ErrNotExist) {
		return notes.Doc{}, nil
	}
	if err != nil {
		return notes.Doc{}, err
	}
	d := notes.Parse(string(b))
	var t notes.Times
	if tb, err := os.ReadFile(s.Path(NotesTimesFile)); err == nil && json.Unmarshal(tb, &t) == nil {
		d.ApplyTimes(t)
	}
	return d, nil
}

// SaveNotes writes the notes (each file replaced whole); empty notes
// remove both files.
func (s *Session) SaveNotes(d notes.Doc) error {
	if d.Empty() {
		for _, f := range []string{NotesFile, NotesTimesFile} {
			if err := os.Remove(s.Path(f)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		return nil
	}
	if err := writeAtomic(s.Path(NotesFile), []byte(d.Markdown())); err != nil {
		return err
	}
	b, err := json.MarshalIndent(d.Times(), "", " ")
	if err != nil {
		return err
	}
	return writeAtomic(s.Path(NotesTimesFile), append(b, '\n'))
}
