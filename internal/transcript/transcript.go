// Package transcript holds a session's transcript: segments from the two
// tracks, merged in time, with speaker bleed removed, and formatted as text
// to paste into notes.
package transcript

import (
	"slices"
	"time"
)

// Track is where a segment was heard.
type Track string

const (
	Me   Track = "me"   // the local microphone
	Them Track = "them" // the meeting's audio
)

// Segment is a stretch of one speaker's speech.
type Segment struct {
	Track   Track         `json:"track"`
	Speaker int           `json:"speaker,omitempty"` // within Them, 1-based; 0 = not diarized
	Start   time.Duration `json:"start"`
	End     time.Duration `json:"end"`
	Text    string        `json:"text"`
	Words   []Word        `json:"words,omitempty"`
}

// Word is one word with its times.
type Word struct {
	Start time.Duration `json:"start"`
	End   time.Duration `json:"end"`
	Text  string        `json:"text"`
}

// Merge combines segments of both tracks in order of start time (Me first
// on a tie), leaving the inputs untouched.
func Merge(tracks ...[]Segment) []Segment {
	var out []Segment
	for _, t := range tracks {
		out = append(out, t...)
	}
	slices.SortStableFunc(out, func(a, b Segment) int {
		if a.Start != b.Start {
			return int(a.Start - b.Start)
		}
		if a.Track != b.Track {
			if a.Track == Me {
				return -1
			}
			return 1
		}
		return 0
	})
	return out
}
