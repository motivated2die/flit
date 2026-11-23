package notes

import (
	"strings"
	"time"
)

// Times are when the notes' lines were typed during the recording, kept
// beside the Markdown (a session's notes.json): Markdown has no place for
// them, and the summary uses them to find what a line refers to
// (docs/research/summary-pipeline.md). Lines are matched back by their
// text, so an edit elsewhere in the file keeps the others' times.
type Times struct {
	Version int        `json:"version"`
	Lines   []LineTime `json:"lines"`
}

// LineTime is one line's text and when it was first typed, in seconds of
// the session.
type LineTime struct {
	Text  string  `json:"text"`
	Typed float64 `json:"typed"`
}

// Times are the times the doc's lines have (lines without one left out).
func (d Doc) Times() Times {
	t := Times{Version: 1, Lines: []LineTime{}}
	for _, b := range d.Blocks {
		if b.Typed != Unknown {
			t.Lines = append(t.Lines, LineTime{Text: lineKey(b.Text), Typed: b.Typed.Seconds()})
		}
	}
	return t
}

// ApplyTimes gives each block the time of the first unused line with the
// same text (others stay Unknown).
func (d *Doc) ApplyTimes(t Times) {
	used := make([]bool, len(t.Lines))
	for i := range d.Blocks {
		d.Blocks[i].Typed = Unknown
		for k, l := range t.Lines {
			if !used[k] && lineKey(l.Text) == lineKey(d.Blocks[i].Text) {
				used[k] = true
				d.Blocks[i].Typed = time.Duration(l.Typed * float64(time.Second))
				break
			}
		}
	}
}

// lineKey is a line's text as it is matched: without the spaces around it
// (Markdown keeps no trailing ones).
func lineKey(s string) string { return strings.TrimSpace(s) }
