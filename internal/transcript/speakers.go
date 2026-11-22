package transcript

import (
	"slices"
	"strings"
	"time"
)

// Turn is a stretch of one speaker's speech from diarization.
type Turn struct {
	Start, End time.Duration
	Speaker    int // 1-based
}

// Diarization cleanup thresholds.
const (
	// A word without a turn over it takes the nearest turn this close.
	nearTurn = time.Second
	// A run of at most this many words of another speaker inside one
	// segment, with the same speaker on both sides, is noise.
	flickerWords = 2
	// A speaker with less speech than this, and less than minShare of the
	// track, is a fragment of someone else (or a cough): its words go to
	// the speakers around them.
	minSpeech = 3 * time.Second
	minShare  = 0.02
)

// AssignSpeakers labels the segments of one track (Them) with the
// speakers of diarization turns and returns them split where the speaker
// changes. Each word takes the speaker it overlaps most; words outside
// every turn take the nearest turn within a second, or their neighbours'
// speaker. Brief flickers inside a segment are smoothed away, fragments of
// speakers too small to be a participant are given to their neighbours,
// and speakers are renumbered 1, 2, … by first appearance. With fewer than
// two speakers left, every segment keeps Speaker 0 (plain "Them").
func AssignSpeakers(segs []Segment, turns []Turn) []Segment {
	if len(turns) == 0 || len(segs) == 0 {
		return clearSpeakers(segs)
	}
	turns = slices.Clone(turns)
	slices.SortFunc(turns, func(a, b Turn) int { return int(a.Start - b.Start) })

	// Every word (or wordless segment) as a unit with its speaker.
	type unit struct {
		seg, word  int // word -1: the whole segment
		start, end time.Duration
		spk        int
	}
	var units []unit
	for si, s := range segs {
		if len(s.Words) == 0 {
			units = append(units, unit{si, -1, s.Start, s.End, 0})
			continue
		}
		for wi, w := range s.Words {
			units = append(units, unit{si, wi, w.Start, w.End, 0})
		}
	}
	for i := range units {
		units[i].spk = speakerAt(turns, units[i].start, units[i].end)
	}
	// Unknown: the previous unit's speaker within the segment, else the
	// next one's, else the nearest known in time.
	for pass := 0; pass < 2; pass++ {
		for i := range units {
			if units[i].spk != 0 {
				continue
			}
			if i > 0 && units[i-1].seg == units[i].seg && units[i-1].spk != 0 {
				units[i].spk = units[i-1].spk
			} else if i+1 < len(units) && units[i+1].seg == units[i].seg && units[i+1].spk != 0 {
				units[i].spk = units[i+1].spk
			} else if pass == 1 {
				units[i].spk = nearestKnown(len(units), i, func(j int) int { return units[j].spk })
			}
		}
	}
	// Flickers: a short run inside a segment between two runs of one
	// other speaker.
	for i := 0; i < len(units); {
		j := i
		for j < len(units) && units[j].seg == units[i].seg && units[j].spk == units[i].spk {
			j++
		}
		if i > 0 && j < len(units) && j-i <= flickerWords &&
			units[i-1].seg == units[i].seg && units[j].seg == units[i].seg &&
			units[i-1].spk == units[j].spk {
			for k := i; k < j; k++ {
				units[k].spk = units[j].spk
			}
		}
		i = j
	}
	// Fragments: speakers with too little speech, smallest first.
	for {
		talk := map[int]time.Duration{}
		var total time.Duration
		for _, u := range units {
			talk[u.spk] += u.end - u.start
			total += u.end - u.start
		}
		victim, least := 0, time.Duration(1<<62)
		for spk, d := range talk {
			if len(talk) > 1 && d < minSpeech && float64(d) < minShare*float64(total) && d < least {
				victim, least = spk, d
			}
		}
		if victim == 0 {
			break
		}
		for i := range units {
			if units[i].spk == victim {
				units[i].spk = nearestKnown(len(units), i, func(j int) int {
					if units[j].spk == victim {
						return 0
					}
					return units[j].spk
				})
			}
		}
	}
	// Renumber by first appearance.
	number := map[int]int{}
	for i := range units {
		if _, ok := number[units[i].spk]; !ok {
			number[units[i].spk] = len(number) + 1
		}
		units[i].spk = number[units[i].spk]
	}
	if len(number) < 2 {
		return clearSpeakers(segs)
	}

	// Rebuild the segments, split at speaker changes.
	var out []Segment
	for i := 0; i < len(units); {
		s := segs[units[i].seg]
		if units[i].word < 0 {
			s.Speaker = units[i].spk
			out = append(out, s)
			i++
			continue
		}
		j := i
		for j < len(units) && units[j].seg == units[i].seg && units[j].spk == units[i].spk {
			j++
		}
		if units[i].word == 0 && (j == len(units) || units[j].seg != units[i].seg) {
			s.Speaker = units[i].spk // the whole segment: keep its own text
			out = append(out, s)
		} else {
			part := Segment{Track: s.Track, Speaker: units[i].spk, Start: s.Words[units[i].word].Start}
			for k := i; k < j; k++ {
				w := s.Words[units[k].word]
				if part.Text != "" {
					part.Text += " "
				}
				part.Text += w.Text
				part.End = w.End
				part.Words = append(part.Words, w)
			}
			out = append(out, part)
		}
		i = j
	}
	return out
}

// speakerAt is the speaker whose turns overlap [start, end] most, or the
// nearest turn's within nearTurn, or 0.
func speakerAt(turns []Turn, start, end time.Duration) int {
	if end <= start {
		end = start + time.Millisecond
	}
	over := map[int]time.Duration{}
	best, bestOver := 0, time.Duration(0)
	nearest, nearDist := 0, nearTurn+1
	for _, t := range turns {
		if t.Start > end+nearTurn {
			break
		}
		if o := min(end, t.End) - max(start, t.Start); o > 0 {
			over[t.Speaker] += o
			if over[t.Speaker] > bestOver {
				best, bestOver = t.Speaker, over[t.Speaker]
			}
			continue
		}
		d := max(t.Start-end, start-t.End)
		if d < nearDist {
			nearest, nearDist = t.Speaker, d
		}
	}
	if best != 0 {
		return best
	}
	if nearDist <= nearTurn {
		return nearest
	}
	return 0
}

// nearestKnown returns the non-zero value of at(j) closest to i, looking
// back first.
func nearestKnown(n, i int, at func(int) int) int {
	for d := 1; d < n; d++ {
		if i-d >= 0 && at(i-d) != 0 {
			return at(i - d)
		}
		if i+d < n && at(i+d) != 0 {
			return at(i + d)
		}
	}
	return 0
}

func clearSpeakers(segs []Segment) []Segment {
	out := slices.Clone(segs)
	for i := range out {
		out[i].Speaker = 0
	}
	return out
}

// SpeakerSummary is what the transcript says about one speaker.
type SpeakerSummary struct {
	Track   Track
	Speaker int           // 0 for Me and an undiarized Them
	Talk    time.Duration // time speaking
	Words   int
	First   time.Duration // first words at
	Quote   string        // a characteristic stretch of what they said
}

// Speakers lists who speaks in a transcript: Me first, then Them's
// speakers by number. The quote is their longest segment, cut to about
// twelve words, to recognise them by.
func Speakers(segs []Segment) []SpeakerSummary {
	type key struct {
		track Track
		spk   int
	}
	byKey := map[key]*SpeakerSummary{}
	longest := map[key]Segment{}
	for _, s := range segs {
		k := key{s.Track, s.Speaker}
		if s.Track == Me {
			k.spk = 0
		}
		sum := byKey[k]
		if sum == nil {
			sum = &SpeakerSummary{Track: k.track, Speaker: k.spk, First: s.Start}
			byKey[k] = sum
		}
		sum.Talk += s.End - s.Start
		sum.Words += len(strings.Fields(s.Text))
		if s.End-s.Start > longest[k].End-longest[k].Start {
			longest[k] = s
		}
	}
	var out []SpeakerSummary
	for k, sum := range byKey {
		sum.Quote = quote(longest[k].Text, 12)
		out = append(out, *sum)
	}
	slices.SortFunc(out, func(a, b SpeakerSummary) int {
		if a.Track != b.Track {
			if a.Track == Me {
				return -1
			}
			return 1
		}
		return a.Speaker - b.Speaker
	})
	return out
}

func quote(text string, words int) string {
	w := strings.Fields(text)
	if len(w) <= words {
		return strings.Join(w, " ")
	}
	return strings.Join(w[:words], " ") + " …"
}

// Clip is a stretch of the timeline to listen to.
type Clip struct{ Start, End time.Duration }

// Clips picks up to n stretches where one speaker can be heard clearly:
// their longest segments that nobody else talks into (on either track,
// with half a second of margin), each cut to maxLen, in time order. With
// no such segment, their longest ones are taken anyway.
func Clips(segs []Segment, track Track, speaker int, n int, maxLen time.Duration) []Clip {
	const margin = 500 * time.Millisecond
	same := func(s Segment) bool {
		return s.Track == track && (track == Me || s.Speaker == speaker)
	}
	var own, clear []Segment
	for _, s := range segs {
		if !same(s) || s.End <= s.Start {
			continue
		}
		own = append(own, s)
		alone := true
		for _, o := range segs {
			if !same(o) && o.Start < s.End+margin && o.End > s.Start-margin {
				alone = false
				break
			}
		}
		if alone {
			clear = append(clear, s)
		}
	}
	pick := clear
	if len(pick) == 0 {
		pick = own
	}
	slices.SortStableFunc(pick, func(a, b Segment) int { return int((b.End - b.Start) - (a.End - a.Start)) })
	var out []Clip
	for _, s := range pick[:min(n, len(pick))] {
		out = append(out, Clip{s.Start, min(s.End, s.Start+maxLen)})
	}
	slices.SortFunc(out, func(a, b Clip) int { return int(a.Start - b.Start) })
	return out
}
