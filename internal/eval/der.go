package eval

import (
	"time"
)

// DER is the diarization error rate of a hypothesis against a reference,
// scored in 10 ms frames over the whole recording: no collar, overlapping
// speech scored, speakers mapped one-to-one so that the most time agrees
// (the standard md-eval definition).
type DER struct {
	Speech     time.Duration     // reference speaker time (overlap counts per speaker)
	Missed     time.Duration     // reference speech with no hypothesis speaker
	FalseAlarm time.Duration     // hypothesis speech where the reference has none
	Confusion  time.Duration     // speech given to the wrong speaker
	Mapping    map[string]string // hypothesis speaker → reference speaker
}

// Rate is (missed + false alarm + confusion) / speech.
func (d DER) Rate() float64 {
	if d.Speech == 0 {
		return 0
	}
	return float64(d.Missed+d.FalseAlarm+d.Confusion) / float64(d.Speech)
}

const derFrame = 10 * time.Millisecond

// ScoreDER scores hyp against ref.
func ScoreDER(ref, hyp []Turn) DER {
	var end time.Duration
	for _, t := range append(append([]Turn(nil), ref...), hyp...) {
		end = max(end, t.End())
	}
	n := int(end/derFrame) + 1
	index := func(turns []Turn) ([]string, [][]bool) {
		var names []string
		ids := map[string]int{}
		var act [][]bool
		for _, t := range turns {
			i, ok := ids[t.Speaker]
			if !ok {
				i = len(names)
				ids[t.Speaker] = i
				names = append(names, t.Speaker)
				act = append(act, make([]bool, n))
			}
			for f := int(t.Start / derFrame); f < int(t.End()/derFrame) && f < n; f++ {
				act[i][f] = true
			}
		}
		return names, act
	}
	refNames, refAct := index(ref)
	hypNames, hypAct := index(hyp)

	// Co-occurrence of each hypothesis and reference speaker.
	co := make([][]int, len(hypNames))
	for h := range co {
		co[h] = make([]int, len(refNames))
		for r := range refNames {
			for f := 0; f < n; f++ {
				if hypAct[h][f] && refAct[r][f] {
					co[h][r]++
				}
			}
		}
	}
	mapping := bestMapping(co)

	var d DER
	d.Mapping = map[string]string{}
	for h, r := range mapping {
		if r >= 0 {
			d.Mapping[hypNames[h]] = refNames[r]
		}
	}
	for f := 0; f < n; f++ {
		nRef, nHyp, correct := 0, 0, 0
		for r := range refNames {
			if refAct[r][f] {
				nRef++
			}
		}
		for h := range hypNames {
			if hypAct[h][f] {
				nHyp++
				if r := mapping[h]; r >= 0 && refAct[r][f] {
					correct++
				}
			}
		}
		d.Speech += time.Duration(nRef) * derFrame
		if nRef > nHyp {
			d.Missed += time.Duration(nRef-nHyp) * derFrame
		} else {
			d.FalseAlarm += time.Duration(nHyp-nRef) * derFrame
		}
		d.Confusion += time.Duration(min(nRef, nHyp)-correct) * derFrame
	}
	return d
}

// bestMapping assigns each row (hypothesis speaker) at most one column
// (reference speaker), each column used once, maximising the sum; -1 means
// unmapped. Exhaustive: diarization has a handful of speakers.
func bestMapping(co [][]int) []int {
	rows := len(co)
	cols := 0
	if rows > 0 {
		cols = len(co[0])
	}
	best := make([]int, rows)
	cur := make([]int, rows)
	for i := range best {
		best[i] = -1
	}
	used := make([]bool, cols)
	bestScore := -1
	var walk func(row, score int)
	walk = func(row, score int) {
		if row == rows {
			if score > bestScore {
				bestScore = score
				copy(best, cur)
			}
			return
		}
		cur[row] = -1
		walk(row+1, score)
		for c := 0; c < cols; c++ {
			if !used[c] {
				used[c] = true
				cur[row] = c
				walk(row+1, score+co[row][c])
				used[c] = false
			}
		}
	}
	walk(0, 0)
	return best
}

// Attribution is how many words went to the right speaker.
type Attribution struct {
	Words, Correct int
	Mapping        map[string]string // hypothesis → reference speaker
}

// Rate is the share of words with the right speaker.
func (a Attribution) Rate() float64 {
	if a.Words == 0 {
		return 0
	}
	return float64(a.Correct) / float64(a.Words)
}

// ScoreAttribution scores word labels: each pair is (reference speaker,
// hypothesis speaker) of one word; hypothesis speakers are mapped
// one-to-one to reference speakers so that the most words agree. Words a
// hypothesis speaker beyond the reference's count gets are wrong.
func ScoreAttribution(pairs [][2]string) Attribution {
	var refNames, hypNames []string
	refID, hypID := map[string]int{}, map[string]int{}
	id := func(names *[]string, ids map[string]int, s string) int {
		if i, ok := ids[s]; ok {
			return i
		}
		ids[s] = len(*names)
		*names = append(*names, s)
		return ids[s]
	}
	for _, p := range pairs {
		id(&refNames, refID, p[0])
		id(&hypNames, hypID, p[1])
	}
	co := make([][]int, len(hypNames))
	for h := range co {
		co[h] = make([]int, len(refNames))
	}
	for _, p := range pairs {
		co[hypID[p[1]]][refID[p[0]]]++
	}
	m := bestMapping(co)
	a := Attribution{Words: len(pairs), Mapping: map[string]string{}}
	for h, r := range m {
		if r >= 0 {
			a.Mapping[hypNames[h]] = refNames[r]
			a.Correct += co[h][r]
		}
	}
	return a
}
