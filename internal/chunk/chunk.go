// Package chunk plans where to cut a track into pieces for transcription:
// at pauses, about 30 seconds apart (docs/research/long-form-chunking.md).
//
// Input is a voice-activity probability per frame (Silero VAD scores 512
// samples per frame at 16 kHz). Speech segments come from hysteresis on the
// probabilities; every pause between them is a cut candidate at its middle.
// The planner walks left to right and, in each window [start+Min,
// start+Max], picks the candidate that balances a long pause against a
// chunk length near Target. Where no pause exists it cuts hard at Max.
//
// The same planner serves a growing recording: with final=false it only
// returns cuts that more audio can't change, so chunks can be transcribed
// while the meeting goes on and the result equals planning the whole track.
package chunk

import "math"

// Params tune the planner. All durations are in samples.
type Params struct {
	Rate int // samples per second (16000)

	Target, Min, Max int // chunk lengths

	// Hysteresis on the probabilities (Silero's defaults).
	Threshold, NegThreshold float32
	MinSpeech, MinSilence   int
	SpeechPad               int
}

// Defaults returns the parameters for audio at rate Hz: chunks of 30 s
// (5–40 s), Silero's speech thresholds.
func Defaults(rate int) Params {
	ms := func(v int) int { return v * rate / 1000 }
	return Params{
		Rate:   rate,
		Target: ms(30_000), Min: ms(5_000), Max: ms(40_000),
		Threshold: 0.5, NegThreshold: 0.35,
		MinSpeech: ms(250), MinSilence: ms(100), SpeechPad: ms(30),
	}
}

// Span is a half-open range of samples.
type Span struct{ Start, End int }

// Len is the span's length in samples.
func (s Span) Len() int { return s.End - s.Start }

// Speech finds speech segments in per-frame probabilities over n samples
// (frame samples per probability).
func Speech(probs []float32, frame, n int, p Params) []Span {
	var segs []Span
	in := false
	start := 0
	silenceAt := -1 // where the current dip below NegThreshold began
	for i, pr := range probs {
		at := i * frame
		switch {
		case pr >= p.Threshold:
			silenceAt = -1
			if !in {
				in, start = true, at
			}
		case pr < p.NegThreshold && in:
			if silenceAt < 0 {
				silenceAt = at
			}
			if at-silenceAt >= p.MinSilence {
				if silenceAt-start >= p.MinSpeech {
					segs = append(segs, Span{start, silenceAt})
				}
				in, silenceAt = false, -1
			}
		}
	}
	if in && n-start >= p.MinSpeech {
		segs = append(segs, Span{start, n})
	}
	// Pad each segment, splitting the pad where pauses are short.
	for i := range segs {
		if i == 0 {
			segs[i].Start = max(0, segs[i].Start-p.SpeechPad)
		}
		if i == len(segs)-1 {
			segs[i].End = min(n, segs[i].End+p.SpeechPad)
			break
		}
		gap := segs[i+1].Start - segs[i].End
		pad := min(p.SpeechPad, gap/2)
		segs[i].End += pad
		segs[i+1].Start -= pad
	}
	return segs
}

// pause is a cut candidate at at, in a pause of gap samples.
type pause struct{ at, gap int }

// pauses returns one candidate per pause: its middle, but at most 1 s after
// the pause began. A long pause's candidate is then fixed once 2 s of it are
// known, which a live recording needs (the user listening in silence for
// minutes is common on the mic track). final includes a pause running to
// the end of the audio; live, such a pause counts only once its candidate
// is fixed.
func pauses(speech []Span, n, rate int, final bool) []pause {
	var out []pause
	prev := 0
	for _, s := range append(speech, Span{n, n}) {
		if s.Start > prev {
			gap := s.Start - prev
			open := s.Start == n // the pause hasn't ended yet
			if !open || final || gap >= 2*rate {
				out = append(out, pause{at: prev + min(gap/2, rate), gap: gap})
			}
		}
		prev = max(prev, s.End)
	}
	return out
}

// Plan returns cut positions (sample indices, increasing) for n samples
// scored by probs. With final=false the audio is still growing: only cuts
// that later audio can't change are returned.
func Plan(probs []float32, frame, n int, final bool, p Params) []int {
	return PlanFrom(probs, frame, n, 0, final, p)
}

// PlanFrom is Plan for a window of a track whose first chunk starts at
// sample start of the window (a cut made before), so a track can be
// planned a window at a time with the same result as all at once.
func PlanFrom(probs []float32, frame, n, start int, final bool, p Params) []int {
	cands := pauses(Speech(probs, frame, n, p), n, p.Rate, final)
	var cuts []int
	for n-start > p.Max {
		windowEnd := start + p.Max
		// Live, a choice in this window is only certain once audio reaches
		// Min past the window: then every candidate is known, and so is
		// whether a cut leaves at least Min after it.
		if !final && n < windowEnd+p.Min {
			break
		}
		cut, ok := best(cands, start, n, p)
		if !ok {
			cut = windowEnd // no pause: hard cut
			if n-cut < p.Min {
				cut = start + (n-start)/2
			}
		}
		cuts = append(cuts, cut)
		start = cut
	}
	return cuts
}

// best picks the candidate in [start+Min, start+Max] with the best score;
// candidates leaving less than Min after them are a fallback only.
func best(cands []pause, start, n int, p Params) (int, bool) {
	var (
		bestAt, fallAt       int
		bestScore, fallScore = math.Inf(-1), math.Inf(-1)
	)
	for _, c := range cands {
		if c.at < start+p.Min {
			continue
		}
		if c.at > start+p.Max {
			break
		}
		sc := score(c, c.at-start, p)
		if n-c.at < p.Min {
			if sc > fallScore {
				fallAt, fallScore = c.at, sc
			}
			continue
		}
		if sc > bestScore {
			bestAt, bestScore = c.at, sc
		}
	}
	switch {
	case !math.IsInf(bestScore, -1):
		return bestAt, true
	case !math.IsInf(fallScore, -1):
		return fallAt, true
	}
	return 0, false
}

// score rates a cut: pauses count up to 1.5 s (longer ones are equally good
// boundaries), and closeness to the target length counts as much as a
// pause of 1.5 s does.
func score(c pause, length int, p Params) float64 {
	sat := float64(3 * p.Rate / 2)
	pauseScore := math.Min(float64(c.gap), sat) / sat
	closeness := 1 - math.Abs(float64(length-p.Target))/float64(p.Max-p.Min)
	return pauseScore + closeness
}

// Spans turns cuts into consecutive chunks covering [0, n).
func Spans(cuts []int, n int) []Span {
	out := make([]Span, 0, len(cuts)+1)
	prev := 0
	for _, c := range cuts {
		out = append(out, Span{prev, c})
		prev = c
	}
	if prev < n {
		out = append(out, Span{prev, n})
	}
	return out
}
