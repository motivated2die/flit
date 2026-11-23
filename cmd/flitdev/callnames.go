package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/motivated2die/flit/internal/callnames"
	"github.com/motivated2die/flit/internal/eval"
	"github.com/motivated2die/flit/internal/transcript"
)

// callnamesSim measures naming the meeting track's speakers from a call
// app's evidence, without a call app: the evidence is made from a test
// meeting's reference turns under several conditions (how often the app
// marks someone, its lag, mistakes, captions), and applied to flit's real
// diarized transcript of that meeting (flit transcribe --speakers --save).
// Two ways are compared: naming each line by the marks during it (Yogurt's
// rule) and naming each voice from all its evidence (callnames.Assign).
// docs/research/call-app-names.md has the results.
//
//	flitdev callnames-sim -meeting testdata/meetings/ami-IS1009a -segments them.json
func callnamesSim(args []string) error {
	fs := flag.NewFlagSet("callnames-sim", flag.ContinueOnError)
	dir := fs.String("meeting", "", "test meeting folder")
	segFile := fs.String("segments", "", "the meeting track's segments from flit transcribe --speakers --save")
	seeds := fs.Int("seeds", 20, "runs per random condition")
	verbose := fs.Int("v", 0, "print each voice's votes for this condition (1-based; 0: none)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dir == "" || *segFile == "" {
		return fmt.Errorf("usage: flitdev callnames-sim -meeting <dir> -segments <file>")
	}
	m, err := eval.Load(*dir)
	if err != nil {
		return err
	}
	f, err := os.Open(filepath.Join(*dir, "reference.rttm"))
	if err != nil {
		return err
	}
	turns, err := eval.ReadRTTM(f)
	f.Close()
	if err != nil {
		return err
	}
	b, err := os.ReadFile(*segFile)
	if err != nil {
		return err
	}
	var segs []transcript.Segment
	if err := json.Unmarshal(b, &segs); err != nil {
		return err
	}
	sim := newSim(m, turns, segs)
	fmt.Printf("%s: %.0f s, %d people on the meeting track, %d voices after diarization, %d scored words\n",
		m.Name, m.Seconds, len(sim.them), sim.voices(), sim.scored())
	fmt.Printf("best possible per voice (each voice named after its majority speaker): %s\n\n", sim.oracle())
	fmt.Println("| Condition | Per line (Yogurt): right / wrong / none | Per voice: right / wrong / none | Per voice + lines where clear | Voices named (wrong) |")
	fmt.Println("|---|---|---|---|---|")
	for ci, c := range conditions() {
		runs := 1
		if c.random {
			runs = *seeds
		}
		var line, voice, split outcome
		var named, wrongVoices float64
		for seed := range runs {
			rng := rand.New(rand.NewPCG(uint64(seed)+1, 7))
			rs, caps := sim.evidence(c, rng)
			o := callnames.DefaultOptions()
			ev := append(callnames.Marks(rs, o), callnames.Captions(caps, segs, o)...)
			line = line.add(sim.score(sim.perLine(ev)))
			res := callnames.Assign(segs, ev, rs, o)
			if *verbose == ci+1 && seed == 0 {
				maj := sim.majority(sim.segs)
				for v := range sim.voices() + 1 {
					if vt, ok := res.Votes[v]; ok {
						fmt.Printf("voice %d (mostly %s): heard %.1f s, marked %.1f s, best %s %.1f s, second %s %.1f s, named %q\n",
							v, maj[v], vt.Heard.Seconds(), vt.Marked.Seconds(), vt.Best, vt.For.Seconds(), vt.Second, vt.Against.Seconds(), res.Speakers[v].Name)
					}
				}
			}
			voice = voice.add(sim.score(sim.perVoice(sim.segs, res)))
			splitSegs := callnames.Refine(segs, ev, &res, o)
			split = split.add(sim.score(sim.perVoice(splitSegs, res)))
			n, w := sim.voicesNamed(splitSegs, res)
			named += float64(n)
			wrongVoices += float64(w)
		}
		fmt.Printf("| %s | %s | %s | %s | %.1f of %d (%.1f) |\n", c.name, line, voice, split, named/float64(runs), sim.voices(), wrongVoices/float64(runs))
	}
	return nil
}

// condition is how the call app behaves in one simulated run.
type condition struct {
	name   string
	random bool
	// marks: the app marks who talks (Zoom with 3+ people); lag, the share
	// of the meeting the reader could read (windows of a minute), the share
	// of readings naming a wrong person.
	marks    bool
	lag      time.Duration
	cover    float64
	wrong    float64
	unmarked bool // one person is never marked (no tile, or on a phone line)
	// captions: the share of the meeting with live captions on.
	captions float64
	roster   bool // participants shown
}

func conditions() []condition {
	full := condition{marks: true, lag: time.Second, cover: 1, roster: true}
	c := func(name string, f func(*condition)) condition {
		x := full
		x.name = name
		f(&x)
		x.random = x.cover < 1 || x.wrong > 0 || x.captions > 0 && x.captions < 1
		return x
	}
	return []condition{
		c("Marks all along, 1 s lag", func(*condition) {}),
		c("Marks, 2 s lag, 10% of readings wrong", func(x *condition) { x.lag, x.wrong = 2*time.Second, 0.1 }),
		c("Marks for 25% of the meeting", func(x *condition) { x.cover = 0.25 }),
		c("Marks for 10% of the meeting", func(x *condition) { x.cover = 0.10 }),
		c("Marks, one person never marked", func(x *condition) { x.unmarked = true }),
		c("No marks, captions for 30%", func(x *condition) { x.marks, x.captions = false, 0.3 }),
		c("No marks, captions all along", func(x *condition) { x.marks, x.captions = false, 1 }),
		c("Roster only (Teams, panel open)", func(x *condition) { x.marks = false }),
	}
}

type sim struct {
	m     eval.Manifest
	turns []eval.Turn
	segs  []transcript.Segment
	me    string
	them  []string // in order of first speech
	end   time.Duration
}

func newSim(m eval.Manifest, turns []eval.Turn, segs []transcript.Segment) *sim {
	s := &sim{m: m, turns: turns, segs: segs, end: time.Duration(m.Seconds * float64(time.Second))}
	for _, t := range turns {
		switch {
		case m.TrackOf(t.Speaker) == "me":
			s.me = t.Speaker
		case !slices.Contains(s.them, t.Speaker):
			s.them = append(s.them, t.Speaker)
		}
	}
	return s
}

// talking: the reference speakers (both tracks) speaking at t.
func (s *sim) talking(t time.Duration) []string {
	var out []string
	for _, x := range s.turns {
		if x.Start <= t && t < x.End() && !slices.Contains(out, x.Speaker) {
			out = append(out, x.Speaker)
		}
	}
	return out
}

// window: which minutes of the meeting the reader could read.
func (s *sim) window(cover float64, rng *rand.Rand) func(time.Duration) bool {
	if cover >= 1 {
		return func(time.Duration) bool { return true }
	}
	n := int(math.Ceil(s.end.Minutes()))
	k := max(1, int(math.Round(cover*float64(n))))
	on := map[int]bool{}
	for _, i := range rng.Perm(n)[:k] {
		on[i] = true
	}
	return func(t time.Duration) bool { return on[int(t/time.Minute)] }
}

func (s *sim) evidence(c condition, rng *rand.Rand) ([]callnames.Reading, []callnames.Caption) {
	people := append([]string{s.me}, s.them...)
	var rs []callnames.Reading
	if c.marks || c.roster {
		ok := s.window(c.cover, rng)
		unmarked := ""
		if c.unmarked {
			unmarked = s.them[len(s.them)-1]
		}
		active := ""
		for t := time.Duration(0); t < s.end; t += 400 * time.Millisecond {
			// the app's active speaker: who talked lag ago; kept while
			// they still talk or nobody does (Zoom keeps the last one)
			now := s.talking(t - c.lag)
			if len(now) > 0 && !slices.Contains(now, active) {
				active = now[0]
			}
			if !ok(t) {
				continue
			}
			r := callnames.Reading{At: t, Participants: people, Me: s.me}
			if c.marks && active != "" && active != unmarked {
				who := active
				if c.wrong > 0 && rng.Float64() < c.wrong {
					who = s.them[rng.IntN(len(s.them))]
				}
				r.Speaking = []string{who}
			}
			rs = append(rs, r)
		}
	}
	var caps []callnames.Caption
	if c.captions > 0 {
		ok := s.window(c.captions, rng)
		for _, x := range s.m.Segments {
			if ok(x.End) {
				caps = append(caps, callnames.Caption{At: x.End + 1500*time.Millisecond, Name: x.Speaker, Text: x.Text})
			}
		}
	}
	return rs, caps
}

// named is the name each meeting-track word got ("" for none), in words order.
type named []string

func (s *sim) words() []transcript.Word {
	var ws []transcript.Word
	for _, g := range s.segs {
		ws = append(ws, g.Words...)
	}
	return ws
}

func (s *sim) perVoice(segs []transcript.Segment, res callnames.Result) named {
	var out named
	for _, g := range segs {
		name := res.Them.Name
		if g.Speaker != 0 {
			name = res.Speakers[g.Speaker].Name
		}
		for range g.Words {
			out = append(out, name)
		}
	}
	return out
}

// perLine: Yogurt's rule (callNames/attribute.ts nameFor): a line takes the
// name marked over more than half of it.
func (s *sim) perLine(ev []callnames.Evidence) named {
	var out named
	for _, g := range s.segs {
		by := map[string]time.Duration{}
		for _, e := range ev {
			if o := min(g.End, e.End) - max(g.Start, e.Start); o > 0 {
				by[e.Name] += o
			}
		}
		name := ""
		for n, d := range by {
			if d*2 > g.End-g.Start && (name == "" || d > by[name]) {
				name = n
			}
		}
		for range g.Words {
			out = append(out, name)
		}
	}
	return out
}

// ref: the reference speaker at each word's middle, "" where it isn't one
// meeting-track person (silence, overlap).
func (s *sim) ref() []string {
	var out []string
	for _, w := range s.words() {
		var them []string
		for _, p := range s.talking((w.Start + w.End) / 2) {
			if p != s.me {
				them = append(them, p)
			}
		}
		r := ""
		if len(them) == 1 {
			r = them[0]
		}
		out = append(out, r)
	}
	return out
}

type outcome struct{ right, wrong, none, n float64 }

func (a outcome) add(b outcome) outcome {
	return outcome{a.right + b.right, a.wrong + b.wrong, a.none + b.none, a.n + b.n}
}

func (o outcome) String() string {
	if o.n == 0 {
		return "–"
	}
	p := func(x float64) string { return fmt.Sprintf("%.0f%%", 100*x/o.n) }
	return p(o.right) + " / " + p(o.wrong) + " / " + p(o.none)
}

func (s *sim) score(got named) outcome {
	var o outcome
	for i, r := range s.ref() {
		if r == "" {
			continue
		}
		o.n++
		switch got[i] {
		case "":
			o.none++
		case r:
			o.right++
		default:
			o.wrong++
		}
	}
	return o
}

func (s *sim) scored() int { return int(s.score(make(named, len(s.words()))).n) }

func (s *sim) voices() int {
	n := map[int]bool{}
	for _, g := range s.segs {
		n[g.Speaker] = true
	}
	return len(n)
}

// majority is each voice's most frequent reference speaker.
func (s *sim) majority(segs []transcript.Segment) map[int]string {
	count := map[int]map[string]int{}
	ref := s.ref()
	i := 0
	for _, g := range segs {
		for range g.Words {
			if ref[i] != "" {
				if count[g.Speaker] == nil {
					count[g.Speaker] = map[string]int{}
				}
				count[g.Speaker][ref[i]]++
			}
			i++
		}
	}
	out := map[int]string{}
	for v, c := range count {
		best := ""
		for p, n := range c {
			if best == "" || n > c[best] || n == c[best] && p < best {
				best = p
			}
		}
		out[v] = best
	}
	return out
}

func (s *sim) oracle() outcome {
	maj := s.majority(s.segs)
	var got named
	for _, g := range s.segs {
		for range g.Words {
			got = append(got, maj[g.Speaker])
		}
	}
	return s.score(got)
}

// voicesNamed: voices given a name, and how many of those not after their
// majority speaker.
func (s *sim) voicesNamed(segs []transcript.Segment, res callnames.Result) (n, wrong int) {
	maj := s.majority(segs)
	if res.Them.Name != "" {
		n++
		if res.Them.Name != maj[0] {
			wrong++
		}
	}
	for v, p := range res.Speakers {
		n++
		if !strings.EqualFold(p.Name, maj[v]) {
			wrong++
		}
	}
	return n, wrong
}
