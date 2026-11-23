// Package playback plays a session's recording: both tracks mixed (the
// meeting as it sounded) or one of them, from any point of the timeline, or
// a list of stretches one after another (a speaker's clearest moments). It
// reports where on the timeline the sound now playing is, so a transcript
// can follow along.
//
// This file is the portable part: what to play next (cursor), mapping
// played samples back to the timeline (timeline), and mixing. The device is
// in player_windows.go.
package playback

import (
	"time"
)

// Rate is the playback sample rate (fullband 48 kHz stereo).
const Rate = 48000

// Range is a stretch of the timeline in samples, To exclusive.
type Range struct{ From, To int64 }

// gap is the silence between two ranges, so separate clips don't run into
// each other.
const gap = Rate * 35 / 100

// cursor walks through ranges, handing out pieces to play.
type cursor struct {
	ranges []Range
	i      int
	pos    int64 // within ranges[i]
	inGap  int64 // silence still to play before ranges[i]
}

func newCursor(ranges []Range) *cursor {
	var rs []Range
	for _, r := range ranges {
		if r.To > r.From {
			rs = append(rs, r)
		}
	}
	c := &cursor{ranges: rs}
	if len(rs) > 0 {
		c.pos = rs[0].From
	}
	return c
}

// next returns the next piece of at most max samples: timeline position tl
// and length n; silent pieces (between ranges) have silent set. n == 0:
// done.
func (c *cursor) next(max int64) (tl, n int64, silent bool) {
	if c.i >= len(c.ranges) {
		return 0, 0, false
	}
	if c.inGap > 0 {
		n = min(max, c.inGap)
		c.inGap -= n
		return c.ranges[c.i].From, n, true
	}
	r := c.ranges[c.i]
	n = min(max, r.To-c.pos)
	tl = c.pos
	c.pos += n
	if c.pos >= r.To {
		c.i++
		if c.i < len(c.ranges) {
			c.pos = c.ranges[c.i].From
			c.inGap = gap
		}
	}
	return tl, n, false
}

// rest returns the ranges still to play from timeline position at on (the
// current range cut there), for resuming after a pause.
func (c *cursor) rest(at int64) []Range {
	var out []Range
	for i := c.i; i < len(c.ranges); i++ {
		r := c.ranges[i]
		if i == c.i && at > r.From && at < r.To {
			r.From = at
		}
		out = append(out, r)
	}
	// The piece playing when paused may belong to an earlier range.
	if c.i > 0 {
		prev := c.ranges[c.i-1]
		if at >= prev.From && at < prev.To {
			out = append([]Range{{at, prev.To}}, out...)
		}
	}
	return out
}

// span says that out samples [out, out+n) play timeline [tl, tl+n) (or
// silence, which holds the position at tl).
type span struct {
	out, tl, n int64
	silent     bool
}

// timeline maps samples played by the device to the timeline.
type timeline struct {
	spans []span
	total int64 // samples handed to the device
}

func (t *timeline) reset() { t.spans, t.total = t.spans[:0], 0 }

func (t *timeline) add(tl, n int64, silent bool) {
	t.spans = append(t.spans, span{t.total, tl, n, silent})
	t.total += n
}

// at is the timeline position when the device has played `played` samples
// (and whether anything maps there).
func (t *timeline) at(played int64) (int64, bool) {
	if len(t.spans) == 0 {
		return 0, false
	}
	// Spans already played long ago can go.
	for len(t.spans) > 1 && t.spans[1].out <= played {
		t.spans = t.spans[1:]
	}
	s := t.spans[0]
	if s.silent {
		return s.tl, true
	}
	return s.tl + min(max(0, played-s.out), s.n), true
}

// mix adds the chosen tracks' samples and limits the sum to [-1, 1].
func mix(dst []float32, tracks [][]float32) {
	for i := range dst {
		var v float32
		for _, t := range tracks {
			if i < len(t) {
				v += t[i]
			}
		}
		dst[i] = max(-1, min(1, v))
	}
}

// ToSamples converts a time to a timeline sample.
func ToSamples(d time.Duration) int64 { return int64(d) * Rate / int64(time.Second) }

// ToTime converts a timeline sample to a time.
func ToTime(s int64) time.Duration { return time.Duration(s) * time.Second / Rate }
