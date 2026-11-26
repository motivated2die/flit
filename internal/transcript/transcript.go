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
	Speaker int           `json:"speaker,omitempty"` // within the track, 1-based; 0 = not diarized
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

// Trim returns the subset of segs falling within [start, end] (non-destructive;
// end <= 0 means unbounded upper end). Segments completely inside the range
// keep their exact text; boundary segments with word timestamps are trimmed at
// word midpoints.
func Trim(segs []Segment, start, end time.Duration) []Segment {
	if start <= 0 && end <= 0 {
		return segs
	}
	inRange := func(a, b time.Duration) bool {
		mid := a + (b-a)/2
		if start > 0 && mid < start {
			return false
		}
		if end > 0 && mid > end {
			return false
		}
		return true
	}
	out := make([]Segment, 0, len(segs))
	for _, s := range segs {
		if end > 0 && s.Start > end {
			continue
		}
		if start > 0 && s.End < start {
			continue
		}
		if (start <= 0 || s.Start >= start) && (end <= 0 || s.End <= end) {
			out = append(out, s)
			continue
		}
		if len(s.Words) == 0 {
			if inRange(s.Start, s.End) {
				out = append(out, s)
			}
			continue
		}
		var kept []Word
		for _, w := range s.Words {
			if inRange(w.Start, w.End) {
				kept = append(kept, w)
			}
		}
		if len(kept) == 0 {
			continue
		}
		if len(kept) == len(s.Words) {
			out = append(out, s)
			continue
		}
		part := Segment{
			Track:   s.Track,
			Speaker: s.Speaker,
			Start:   kept[0].Start,
			End:     kept[len(kept)-1].End,
			Words:   kept,
		}
		for i, w := range kept {
			if i > 0 {
				part.Text += " "
			}
			part.Text += w.Text
		}
		out = append(out, part)
	}
	return out
}
