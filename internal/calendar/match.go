package calendar

import (
	"math"
	"sort"
	"strings"
	"time"
	"unicode"
)

// Match is the calendar's answer to "which meeting was this recording?":
// one clear meeting, or the ones to choose from when several fit about
// equally well (parallel meetings), or neither.
type Match struct {
	Best    *Meeting
	Choices []Meeting
}

// Matching thresholds.
const (
	joinEarly   = 20 * time.Minute // a recording may start this long before its meeting
	joinLate    = 15 * time.Minute // or this long after its scheduled end (meetings overrun)
	minScore    = 0.25
	closeScores = 0.15 // a runner-up this close makes it a choice
	maxChoices  = 4
)

// MatchRecording finds the meeting a recording belongs to. end may be zero
// (the recording just started). provider is the meeting app detected
// (flit's detect IDs; "" for a recording by hand), windowTitle what its
// window showed (Teams shows the meeting's name).
func MatchRecording(meetings []Meeting, start, end time.Time, provider, windowTitle string) Match {
	provider = strings.TrimSuffix(provider, "-web")
	type scored struct {
		m     Meeting
		score float64
	}
	var cands []scored
	for _, m := range meetings {
		if m.AllDay || !m.End.After(m.Start) {
			continue
		}
		if start.Before(m.Start.Add(-joinEarly)) || start.After(m.End.Add(joinLate)) {
			continue
		}
		// How close to its start the recording began (within an hour); a
		// recording begun while the meeting runs fits however late.
		startFit := 1 - math.Min(1, math.Abs(start.Sub(m.Start).Minutes())/60)
		switch {
		case !start.Before(m.Start) && start.Before(m.End):
			startFit = math.Max(startFit, 0.6)
		case !start.Before(m.End): // within joinLate of its end: it overran
			startFit = math.Max(startFit, 0.45)
		}
		score := startFit
		if !end.IsZero() && end.Sub(start) > time.Minute {
			// How much of the shorter of the two the other covers.
			ov := earlier(end, m.End).Sub(later(start, m.Start))
			shorter := min(end.Sub(start), m.End.Sub(m.Start))
			cover := math.Max(0, ov.Seconds()/shorter.Seconds())
			score = 0.5*startFit + 0.5*cover
		}
		if provider != "" && m.Provider != "" {
			if m.Provider == provider {
				score += 0.4
			} else {
				score -= 0.4
			}
		}
		score += 0.6 * titleSimilarity(windowTitle, m.Title)
		if m.Free {
			score -= 0.1
		}
		cands = append(cands, scored{m, score})
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].score > cands[j].score })
	if len(cands) == 0 || cands[0].score < minScore {
		return Match{}
	}
	if len(cands) == 1 || cands[1].score < cands[0].score-closeScores {
		best := cands[0].m
		return Match{Best: &best}
	}
	var out Match
	for _, c := range cands {
		if c.score >= cands[0].score-2*closeScores && len(out.Choices) < maxChoices {
			out.Choices = append(out.Choices, c.m)
		}
	}
	return out
}

// titleSimilarity is the share of a meeting title's words the window title
// contains (words of three letters or more, case ignored), 0..1.
func titleSimilarity(window, title string) float64 {
	words := func(s string) map[string]bool {
		out := map[string]bool{}
		for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
			if len([]rune(w)) >= 3 {
				out[w] = true
			}
		}
		return out
	}
	tw, ww := words(title), words(window)
	if len(tw) == 0 || len(ww) == 0 {
		return 0
	}
	hit := 0
	for w := range tw {
		if ww[w] {
			hit++
		}
	}
	return float64(hit) / float64(len(tw))
}

func earlier(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
