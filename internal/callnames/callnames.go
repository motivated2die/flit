// Package callnames names the meeting track's speakers from what the
// meeting app showed during the call: who it marked as talking, who was in
// the call, and its live captions (docs/research/call-app-names.md).
//
// Sortformer has already told the voices apart; this package only decides
// which voice is whom. Evidence from the app votes for each voice over the
// whole meeting, and a voice gets a name only when the vote is clear. A
// failure or too little evidence leaves the speaker unnamed, never named
// wrongly on purpose. Pure Go: the readings come from elsewhere.
package callnames

import (
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/motivated2die/flit/internal/transcript"
)

// Source is where a name came from.
type Source string

const (
	FromMarks    Source = "marks"    // the app marked the person as talking
	FromCaptions Source = "captions" // the app's live captions said who said the words
	FromRoster   Source = "roster"   // the call had only one other person
)

// Reading is one look at the call app, on the session's time line.
type Reading struct {
	At           time.Duration
	Speaking     []string // who the app marks as talking now
	Participants []string // everyone it shows in the call, the user too
	Me           string   // the user, when the app says which one that is
}

// Caption is a finished line of the app's live captions.
type Caption struct {
	At   time.Duration // when the line was seen (after its words were said)
	Name string
	Text string
}

// Evidence says that Name was speaking from Start to End.
type Evidence struct {
	Start, End time.Duration
	Name       string
	Source     Source
}

// Options are the thresholds; DefaultOptions has the starting values.
type Options struct {
	// Lag: the app marks a speaker this long after they start (Yogurt
	// measured 0.75–1.2 s for Zoom). Hold: a reading counts until the next
	// one, but never longer than this.
	Lag, Hold time.Duration
	// MyNames are the user's own names: never a name for the meeting track.
	MyNames []string
	// Onset: the first stretch of each run of one voice isn't counted, as
	// the app may still show the previous person there.
	Onset time.Duration
	// A voice gets a name with at least MinEvidence of its speech marked
	// with it, at least MinShare of all the marked speech of that voice, and
	// MinMargin times the runner-up.
	MinEvidence time.Duration
	MinShare    float64
	MinMargin   float64
	// FlagShare: a runner-up with MinEvidence and this share means the
	// voice is probably two people.
	FlagShare float64
	// Captions: their words are looked for in the transcript up to
	// CaptionBefore before and CaptionAfter after the caption was seen;
	// at least MinCaptionWords and MinCaptionShare of its words must match.
	CaptionBefore, CaptionAfter time.Duration
	MinCaptionWords             int
	MinCaptionShare             float64
	// MinRosterReadings: readings needed before "only one other person in
	// the call" counts.
	MinRosterReadings int
	// LineShare: Refine moves a line when one name covers more than this
	// share of it.
	LineShare float64
}

// DefaultOptions are starting values, to be tuned on real calls.
func DefaultOptions() Options {
	return Options{
		Lag:               750 * time.Millisecond,
		Hold:              2 * time.Second,
		Onset:             time.Second,
		MinEvidence:       5 * time.Second,
		MinShare:          0.6,
		MinMargin:         2,
		FlagShare:         0.25,
		CaptionBefore:     30 * time.Second,
		CaptionAfter:      5 * time.Second,
		MinCaptionWords:   3,
		MinCaptionShare:   0.4,
		MinRosterReadings: 10,
		LineShare:         0.5,
	}
}

// fold is a name or word for comparison.
func fold(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }

func (o Options) isMe(name string, r Reading) bool {
	n := fold(name)
	if r.Me != "" && fold(r.Me) == n {
		return true
	}
	for _, m := range o.MyNames {
		if m != "" && fold(m) == n {
			return true
		}
	}
	return false
}

// Marks turns readings into evidence: the stretches where exactly one
// person other than the user was marked talking, moved back by the app's
// lag. Consecutive stretches of one person are joined.
func Marks(rs []Reading, o Options) []Evidence {
	rs = slices.Clone(rs)
	slices.SortStableFunc(rs, func(a, b Reading) int { return int(a.At - b.At) })
	var out []Evidence
	for i, r := range rs {
		who := ""
		n := 0
		seen := map[string]bool{}
		for _, s := range r.Speaking {
			if o.isMe(s, r) || seen[fold(s)] {
				continue
			}
			seen[fold(s)] = true
			who, n = s, n+1
		}
		if n != 1 {
			continue
		}
		end := r.At + o.Hold
		if i+1 < len(rs) && rs[i+1].At < end {
			end = rs[i+1].At
		}
		start, end := r.At-o.Lag, end-o.Lag
		if end <= start {
			continue
		}
		if k := len(out) - 1; k >= 0 && fold(out[k].Name) == fold(who) && out[k].End >= start {
			out[k].End = max(out[k].End, end)
			continue
		}
		out = append(out, Evidence{Start: start, End: end, Name: who, Source: FromMarks})
	}
	return out
}

// word is one word of the meeting track, flattened.
type word struct {
	start, end time.Duration
	speaker    int
	tok        string
}

func words(segs []transcript.Segment) []word {
	var ws []word
	for _, s := range segs {
		if s.Track != transcript.Them {
			continue
		}
		if len(s.Words) == 0 {
			ws = append(ws, word{s.Start, s.End, s.Speaker, ""})
			continue
		}
		for _, w := range s.Words {
			ws = append(ws, word{w.Start, w.End, s.Speaker, token(w.Text)})
		}
	}
	slices.SortStableFunc(ws, func(a, b word) int { return int(a.start - b.start) })
	return ws
}

// token is a word reduced to lower-case letters and digits.
func token(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func tokens(s string) []string {
	var out []string
	for _, f := range strings.Fields(s) {
		if t := token(f); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// same: equal, or one letter off in a word of five letters or more
// (speech-to-text and captions disagree on endings and spellings).
func same(a, b string) bool {
	if a == b {
		return a != ""
	}
	ra, rb := []rune(a), []rune(b)
	if len(ra) < 5 || len(rb) < 5 || abs(len(ra)-len(rb)) > 1 {
		return false
	}
	// edit distance ≤ 1
	i, j, edits := 0, 0, 0
	for i < len(ra) && j < len(rb) {
		if ra[i] == rb[j] {
			i, j = i+1, j+1
			continue
		}
		if edits++; edits > 1 {
			return false
		}
		switch {
		case len(ra) > len(rb):
			i++
		case len(rb) > len(ra):
			j++
		default:
			i, j = i+1, j+1
		}
	}
	return edits+(len(ra)-i)+(len(rb)-j) <= 1
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// Captions turns caption lines into evidence by finding their words in the
// transcript: a local alignment of the caption's words against the meeting
// track's words around the time it was seen. Timing then comes from the
// transcript, not from the app, so the captions' delay doesn't matter.
func Captions(caps []Caption, segs []transcript.Segment, o Options) []Evidence {
	ws := words(segs)
	var out []Evidence
	for _, c := range caps {
		ct := tokens(c.Text)
		if len(ct) < o.MinCaptionWords || c.Name == "" {
			continue
		}
		lo, _ := slices.BinarySearchFunc(ws, c.At-o.CaptionBefore, func(w word, t time.Duration) int { return int(w.start - t) })
		hi, _ := slices.BinarySearchFunc(ws, c.At+o.CaptionAfter, func(w word, t time.Duration) int { return int(w.start - t) })
		matched := align(ct, ws[lo:hi])
		if len(matched) < o.MinCaptionWords || float64(len(matched)) < o.MinCaptionShare*float64(len(ct)) {
			continue
		}
		for _, k := range matched {
			w := ws[lo+k]
			if n := len(out) - 1; n >= 0 && out[n].Name == c.Name && w.start-out[n].End < time.Second && w.start >= out[n].Start {
				out[n].End = max(out[n].End, w.end)
				continue
			}
			out = append(out, Evidence{Start: w.start, End: w.end, Name: c.Name, Source: FromCaptions})
		}
	}
	return out
}

// align is a Smith-Waterman local alignment of caption tokens a against
// transcript words b; it returns the indices of b's words matched exactly
// (or nearly, see same) in the best local alignment.
func align(a []string, b []word) []int {
	const match, mismatch, gap = 2, -1, -1
	n, m := len(a), len(b)
	if n == 0 || m == 0 {
		return nil
	}
	h := make([][]int, n+1)
	for i := range h {
		h[i] = make([]int, m+1)
	}
	bi, bj, best := 0, 0, 0
	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			d := h[i-1][j-1] + mismatch
			if same(a[i-1], b[j-1].tok) {
				d = h[i-1][j-1] + match
			}
			v := max(0, d, h[i-1][j]+gap, h[i][j-1]+gap)
			h[i][j] = v
			if v > best {
				bi, bj, best = i, j, v
			}
		}
	}
	var out []int
	for i, j := bi, bj; i > 0 && j > 0 && h[i][j] > 0; {
		ok := same(a[i-1], b[j-1].tok)
		switch {
		case ok && h[i][j] == h[i-1][j-1]+match:
			out = append(out, j-1)
			i, j = i-1, j-1
		case !ok && h[i][j] == h[i-1][j-1]+mismatch:
			i, j = i-1, j-1
		case h[i][j] == h[i-1][j]+gap:
			i--
		default:
			j--
		}
	}
	slices.Reverse(out)
	return out
}

// Pick is the name chosen for one voice.
type Pick struct {
	Name     string
	Source   Source
	Evidence time.Duration // this voice's speech marked with the name
	Share    float64       // of all its marked speech, or of all its speech while the app was read
}

// Vote is how the evidence fell for one voice, for the debug view and the
// log (counts only there).
type Vote struct {
	// Heard: the voice's counted speech while the app was being read and
	// marking people; Marked: the part of it some name covered.
	Heard, Marked time.Duration
	Best, Second  string
	For, Against  time.Duration // evidence for Best and for Second
}

// Result is what Assign decided.
type Result struct {
	// Speakers: names for the meeting track's speakers by number.
	Speakers map[int]Pick
	// Them: the name for an undiarized meeting track (speaker 0), when
	// the call had only one other person.
	Them Pick
	// Flagged: speakers whose evidence names two people about equally
	// (Sortformer merged them); they get no name.
	Flagged []int
	// Suggest: people seen in the call who got no voice, in the order
	// first seen, for the rename field.
	Suggest []string
	// Votes: the evidence per voice.
	Votes map[int]Vote
}

// Span is a stretch of the session's time line.
type Span struct{ Start, End time.Duration }

// readable: the stretches the app's marks speak for, when it marks anyone
// at all in the meeting: each reading until the next (at most Hold), moved
// back by Lag, joined. With no marks anywhere (Teams, or Zoom with two
// people) the app says nothing about who talks, and there is none.
func readable(rs []Reading, o Options) []Span {
	rs = slices.Clone(rs)
	slices.SortStableFunc(rs, func(a, b Reading) int { return int(a.At - b.At) })
	marks := false
	for _, r := range rs {
		marks = marks || len(r.Speaking) > 0
	}
	if !marks {
		return nil
	}
	var out []Span
	for i, r := range rs {
		end := r.At + o.Hold
		if i+1 < len(rs) && rs[i+1].At < end {
			end = rs[i+1].At
		}
		sp := Span{r.At - o.Lag, end - o.Lag}
		if k := len(out) - 1; k >= 0 && out[k].End >= sp.Start {
			out[k].End = max(out[k].End, sp.End)
			continue
		}
		out = append(out, sp)
	}
	return out
}

// within: how much of [a, b] the sorted, disjoint spans cover.
func within(spans []Span, a, b time.Duration) time.Duration {
	i, _ := slices.BinarySearchFunc(spans, a, func(s Span, t time.Duration) int { return int(s.End - t) })
	var d time.Duration
	for ; i < len(spans) && spans[i].Start < b; i++ {
		if o := min(b, spans[i].End) - max(a, spans[i].Start); o > 0 {
			d += o
		}
	}
	return d
}

// Assign names the meeting track's voices. Each word of a voice counts
// toward the names whose evidence covers it (the first Onset of each run of
// the voice left out); a voice takes the name with clearly the most. Two
// voices may take the same name (Sortformer split one person). A call with
// only the user and one other person names the whole meeting track.
//
// While the app marks people, a name must also explain most of the voice's
// speech during the readings, not just most of the marked part: a voice
// that is two people, one of whom the app never marks, would otherwise
// take the other's name for both.
func Assign(segs []transcript.Segment, ev []Evidence, rs []Reading, o Options) Result {
	res := Result{Speakers: map[int]Pick{}, Votes: map[int]Vote{}}
	ws := words(segs)
	read := readable(rs, o)

	// Evidence by the seconds it covers, for lookup.
	bySec := map[int64][]int{}
	for i, e := range ev {
		for s := int64(e.Start / time.Second); s <= int64(e.End/time.Second); s++ {
			bySec[s] = append(bySec[s], i)
		}
	}
	type tally struct {
		by     map[string]time.Duration
		src    map[string]map[Source]time.Duration
		spell  map[string]string
		marked time.Duration
		heard  time.Duration
	}
	score := map[int]*tally{}
	runStart, prevSpk, prevEnd := time.Duration(0), -1, time.Duration(0)
	for _, w := range ws {
		if w.speaker != prevSpk || w.start-prevEnd > 1500*time.Millisecond {
			runStart = w.start
		}
		prevSpk, prevEnd = w.speaker, w.end
		a := max(w.start, runStart+o.Onset)
		if w.speaker == 0 || w.end <= a {
			continue
		}
		t := score[w.speaker]
		if t == nil {
			t = &tally{by: map[string]time.Duration{}, src: map[string]map[Source]time.Duration{}, spell: map[string]string{}}
			score[w.speaker] = t
		}
		t.heard += within(read, a, w.end)
		seen := map[int]bool{}
		for s := int64(a / time.Second); s <= int64(w.end/time.Second); s++ {
			for _, i := range bySec[s] {
				if seen[i] {
					continue
				}
				seen[i] = true
				e := ev[i]
				if ov := min(w.end, e.End) - max(a, e.Start); ov > 0 {
					k := fold(e.Name)
					t.by[k] += ov
					if t.src[k] == nil {
						t.src[k] = map[Source]time.Duration{}
					}
					t.src[k][e.Source] += ov
					t.spell[k] = e.Name
					t.marked += ov
				}
			}
		}
	}
	spks := make([]int, 0, len(score))
	for s := range score {
		spks = append(spks, s)
	}
	slices.Sort(spks)
	for _, spk := range spks {
		t := score[spk]
		var n1, n2 string
		var s1, s2 time.Duration
		for k, d := range t.by {
			switch {
			case d > s1 || (d == s1 && k < n1):
				n2, s2 = n1, s1
				n1, s1 = k, d
			case d > s2 || (d == s2 && k < n2):
				n2, s2 = k, d
			}
		}
		den := max(t.marked, t.heard, 1)
		share := float64(s1) / float64(den)
		res.Votes[spk] = Vote{Heard: t.heard, Marked: t.marked, Best: t.spell[n1], Second: t.spell[n2], For: s1, Against: s2}
		if s1 >= o.MinEvidence && share >= o.MinShare && float64(s1) >= o.MinMargin*float64(s2) {
			src, best := FromMarks, time.Duration(-1)
			for sr, d := range t.src[n1] {
				if d > best || (d == best && sr < src) {
					src, best = sr, d
				}
			}
			res.Speakers[spk] = Pick{Name: t.spell[n1], Source: src, Evidence: s1, Share: share}
			continue
		}
		if s2 >= o.MinEvidence && float64(s2) >= o.FlagShare*float64(den) {
			res.Flagged = append(res.Flagged, spk)
		}
	}

	// Who was in the call.
	var order []string
	spelled := map[string]string{}
	only, onlyCount := "", 0
	for _, r := range rs {
		var others []string
		for _, p := range r.Participants {
			if o.isMe(p, r) {
				continue
			}
			k := fold(p)
			if _, ok := spelled[k]; !ok {
				spelled[k] = p
				order = append(order, k)
			}
			if !slices.Contains(others, k) {
				others = append(others, k)
			}
		}
		switch {
		case len(others) == 0: // nobody else shown (the panel loading): says nothing
		case len(others) == 1 && (only == "" || only == others[0]) && onlyCount >= 0:
			only, onlyCount = others[0], onlyCount+1
		default:
			onlyCount = -1 // more than one other person at some point
		}
	}
	if only != "" && onlyCount >= o.MinRosterReadings {
		pick := Pick{Name: spelled[only], Source: FromRoster}
		diarized := false
		for _, w := range ws {
			if w.speaker != 0 {
				diarized = true
				res.Speakers[w.speaker] = pick
			}
		}
		if !diarized {
			res.Them = pick
		}
		res.Flagged = nil
	}
	used := map[string]bool{fold(res.Them.Name): true}
	for _, p := range res.Speakers {
		used[fold(p.Name)] = true
	}
	for _, k := range order {
		if !used[k] {
			res.Suggest = append(res.Suggest, spelled[k])
		}
	}
	return res
}

// Refine applies line-level evidence on top of the voices' names. A line
// whose time the evidence gives, for more than LineShare of it, to one name
// takes that name: it moves to the voice already carrying the name, or to a
// new speaker number for it. This repairs what diarization merged (a voice
// flagged as two people, or one that holds a few lines of someone else),
// and names clear lines of voices that had too little evidence overall.
// Other lines keep their voice and its name, if any. The segments are
// returned as a copy; res gets the new speakers.
func Refine(segs []transcript.Segment, ev []Evidence, res *Result, o Options) []transcript.Segment {
	out := slices.Clone(segs)
	next := 0
	voiceOf := map[string]int{} // a name's voice: the lowest number carrying it
	for _, s := range segs {
		next = max(next, s.Speaker)
	}
	for v, p := range res.Speakers {
		if k := fold(p.Name); voiceOf[k] == 0 || v < voiceOf[k] {
			voiceOf[k] = v
		}
	}
	spell := map[string]string{}
	for i, s := range out {
		if s.Track != transcript.Them || s.End <= s.Start {
			continue
		}
		by := map[string]time.Duration{}
		for _, e := range ev {
			if ov := min(s.End, e.End) - max(s.Start, e.Start); ov > 0 {
				by[fold(e.Name)] += ov
				spell[fold(e.Name)] = e.Name
			}
		}
		best, d := "", time.Duration(0)
		for k, x := range by {
			if x > d || x == d && k < best {
				best, d = k, x
			}
		}
		if best == "" || float64(d) <= o.LineShare*float64(s.End-s.Start) {
			continue
		}
		if cur, ok := res.Speakers[s.Speaker]; ok && fold(cur.Name) == best {
			continue
		}
		v := voiceOf[best]
		if v == 0 {
			next++
			v = next
			voiceOf[best] = v
			res.Speakers[v] = Pick{Name: spell[best], Source: FromMarks}
		}
		out[i].Speaker = v
	}
	return out
}
