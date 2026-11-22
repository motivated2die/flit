package transcript

import (
	"strings"
	"time"
	"unicode"
)

// EchoParams tune SuppressEcho.
//
// Without a headset the microphone picks up the meeting from the
// loudspeakers, so the Me track repeats words Them said a moment earlier.
// A Me word is taken for such an echo when it is part of a run of at least
// MinRun consecutive words that Them said in the same order at about the
// same time. Matching runs of words, not similar texts, matters: two long
// stretches of German share most of their common words and letter
// combinations without being the same speech (measured on the synthetic
// German meeting, docs/testing/test-data.md).
type EchoParams struct {
	// MaxOffset is how far apart in time an echoed word and its original
	// may be. The acoustic delay is milliseconds; the rest is the two
	// transcriptions placing word times differently.
	MaxOffset time.Duration
	MinRun    int // consecutive matching words
	// Fill marks up to this many unmatched words between two runs as echo
	// too: the two tracks transcribe the same speech slightly differently.
	Fill int
}

// DefaultEcho returns the starting parameters.
func DefaultEcho() EchoParams {
	return EchoParams{MaxOffset: 2 * time.Second, MinRun: 3, Fill: 2}
}

// SuppressEcho removes echoed words from the Me segments and returns what
// is left; segments left without words are dropped. Me segments without
// word times are kept as they are.
func SuppressEcho(me, them []Segment, p EchoParams) []Segment {
	type tw struct {
		norm string
		at   time.Duration
	}
	var themWords []tw
	for _, s := range them {
		for _, w := range s.Words {
			if n := normWord(w.Text); n != "" {
				themWords = append(themWords, tw{n, w.Start})
			}
		}
	}
	if len(themWords) == 0 {
		return me
	}
	out := make([]Segment, 0, len(me))
	for _, s := range me {
		if len(s.Words) == 0 {
			out = append(out, s)
			continue
		}
		norm := make([]string, len(s.Words))
		for i, w := range s.Words {
			norm[i] = normWord(w.Text)
		}
		echo := make([]bool, len(s.Words))
		// For each start position in Me, the longest run matching Them
		// words in order, starting at a Them word close in time.
		for i := range s.Words {
			if norm[i] == "" {
				continue
			}
			for j, t := range themWords {
				if t.norm != norm[i] || absDur(s.Words[i].Start-t.at) > p.MaxOffset {
					continue
				}
				k := 0
				for i+k < len(norm) && j+k < len(themWords) && norm[i+k] == themWords[j+k].norm && norm[i+k] != "" {
					k++
				}
				if k >= p.MinRun {
					for x := i; x < i+k; x++ {
						echo[x] = true
					}
				}
			}
		}
		fill(echo, p.Fill)
		out = append(out, keepWords(s, echo)...)
	}
	return out
}

// fill marks short unmarked stretches between marked ones.
func fill(marks []bool, maxGap int) {
	last := -1
	for i, m := range marks {
		if !m {
			continue
		}
		if last >= 0 && i-last-1 <= maxGap {
			for x := last + 1; x < i; x++ {
				marks[x] = true
			}
		}
		last = i
	}
}

// keepWords returns the unmarked words of s as segments, split where
// marked words were removed.
func keepWords(s Segment, drop []bool) []Segment {
	var out []Segment
	var cur *Segment
	for i, w := range s.Words {
		if drop[i] {
			if cur != nil {
				out = append(out, *cur)
				cur = nil
			}
			continue
		}
		if cur == nil {
			cur = &Segment{Track: s.Track, Speaker: s.Speaker, Start: w.Start, Text: w.Text}
		} else {
			cur.Text += " " + w.Text
		}
		cur.End = w.End
		cur.Words = append(cur.Words, w)
	}
	if cur != nil {
		out = append(out, *cur)
	}
	// Untouched segments keep their own text (which may be spaced or
	// punctuated better than joined words).
	if len(out) == 1 && len(out[0].Words) == len(s.Words) {
		return []Segment{s}
	}
	return out
}

// normWord lowercases a word and strips punctuation around it.
func normWord(s string) string {
	return strings.TrimFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

func absDur(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
