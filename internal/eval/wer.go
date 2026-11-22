package eval

import (
	"strings"
	"unicode"
)

// Normalize prepares text for word error rate: lower case, punctuation
// removed (apostrophes inside words kept), hyphens split, German ß kept.
// Numbers are left as written; reference sets that spell them out need
// that accounted for when comparing models, not within one.
func Normalize(s string) []string {
	s = strings.ToLower(s)
	return strings.FieldsFunc(s, func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '\'')
	})
}

// WER is word error rate of hyp against ref: (substitutions + deletions +
// insertions) / words in ref.
type WER struct {
	Ref, Sub, Del, Ins int
}

// Rate returns the error rate (0 for an empty reference).
func (w WER) Rate() float64 {
	if w.Ref == 0 {
		return 0
	}
	return float64(w.Sub+w.Del+w.Ins) / float64(w.Ref)
}

// Add sums two counts (to pool several files).
func (w WER) Add(o WER) WER {
	return WER{w.Ref + o.Ref, w.Sub + o.Sub, w.Del + o.Del, w.Ins + o.Ins}
}

// Score aligns hyp to ref by Levenshtein distance over words.
func Score(ref, hyp []string) WER {
	n, m := len(ref), len(hyp)
	// cost[j] and op counts for the current row, rolling.
	type cell struct{ cost, sub, del, ins int }
	prev := make([]cell, m+1)
	cur := make([]cell, m+1)
	for j := 1; j <= m; j++ {
		prev[j] = cell{cost: j, ins: j}
	}
	for i := 1; i <= n; i++ {
		cur[0] = cell{cost: i, del: i}
		for j := 1; j <= m; j++ {
			if ref[i-1] == hyp[j-1] {
				cur[j] = prev[j-1]
				continue
			}
			s, d, in := prev[j-1], prev[j], cur[j-1]
			best := cell{cost: s.cost + 1, sub: s.sub + 1, del: s.del, ins: s.ins}
			if d.cost+1 < best.cost {
				best = cell{cost: d.cost + 1, sub: d.sub, del: d.del + 1, ins: d.ins}
			}
			if in.cost+1 < best.cost {
				best = cell{cost: in.cost + 1, sub: in.sub, del: in.del, ins: in.ins + 1}
			}
			cur[j] = best
		}
		prev, cur = cur, prev
	}
	c := prev[m]
	return WER{Ref: n, Sub: c.sub, Del: c.del, Ins: c.ins}
}

// ScoreText normalises and scores two texts.
func ScoreText(ref, hyp string) WER { return Score(Normalize(ref), Normalize(hyp)) }
