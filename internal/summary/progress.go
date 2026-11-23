package summary

import "github.com/motivated2die/flit/internal/llm"

// The answers' caps, and what each call is expected to write (measured,
// docs/research/summary-experiments.md: questions 20–80 tokens, a
// section's JSON ~330, the notes 500–700).
const (
	askMax     = 160
	extractMax = 700

	askWrites     = 50
	extractWrites = 330
	writerWrites  = 600

	// writeCost is what writing a token costs against reading one: the
	// measured speeds differ about tenfold on a CPU (85 vs 8.4 tokens/s
	// for Qwen3.5 4B, 205 vs 20 for 2B).
	writeCost = 10

	// promptExtra is the instructions, notes and header of a prompt beyond
	// its transcript (tokens, about).
	promptExtra = 400
)

// tracker turns each call's progress into the whole run's: every call
// planned gets a share by the tokens it should read and write, and its
// share fills as the server reports reading and writing.
type tracker struct {
	fn    func(Progress)
	calls []plannedCall
	total float64
	done  float64 // the shares of the calls finished
	at    int     // the next call
}

type plannedCall struct {
	read, write float64
}

func (c plannedCall) cost() float64 { return c.read + writeCost*c.write }

// plan lists the calls Run will make, in order: optional classify, a question
// each, the sections (Extract), the writer.
func plan(o Options, ts []Turn, qs []Question, sections []Section, transcript int, tokens func(Turn) int, withClassify bool) *tracker {
	t := &tracker{fn: o.Progress}
	if withClassify {
		t.calls = append(t.calls, plannedCall{600, classifyWrites})
	}
	sum := func(from, to int) float64 {
		n := 0
		for _, tr := range ts[from:to] {
			n += tokens(tr)
		}
		return float64(n)
	}
	for _, q := range qs {
		from, to, ok := excerpt(ts, q)
		if ok {
			t.calls = append(t.calls, plannedCall{sum(from, to) + promptExtra/2, askWrites})
		} else {
			t.calls = append(t.calls, plannedCall{})
		}
	}
	if o.Approach == Extract {
		for _, s := range sections {
			t.calls = append(t.calls, plannedCall{sum(s.From, s.To) + promptExtra, extractWrites})
		}
		t.calls = append(t.calls, plannedCall{float64(len(sections))*extractWrites + promptExtra*2, writerWrites})
	} else {
		t.calls = append(t.calls, plannedCall{float64(transcript) + promptExtra*2, writerWrites})
	}
	for _, c := range t.calls {
		t.total += c.cost()
	}
	return t
}

// call is the progress callback for the next call (step, i of n), and
// reports its start.
func (t *tracker) call(step string, i, n int) func(llm.CallProgress) {
	if t.at > 0 && t.at <= len(t.calls) {
		t.done += t.calls[t.at-1].cost()
	}
	var c plannedCall
	if t.at < len(t.calls) {
		c = t.calls[t.at]
	}
	t.at++
	if t.fn == nil {
		return nil
	}
	report := func(within float64) {
		f := 0.0
		if t.total > 0 {
			f = (t.done + within) / t.total
		}
		t.fn(Progress{Step: step, Done: i, Total: n, Fraction: min(1, f)})
	}
	report(0)
	return func(p llm.CallProgress) {
		within := 0.0
		if p.ReadTotal > 0 {
			within += c.read * float64(p.Read) / float64(p.ReadTotal)
		}
		within += writeCost * min(c.write, float64(p.Wrote))
		report(min(within, c.cost()))
	}
}

// finish reports the run as done.
func (t *tracker) finish() {
	if t.fn != nil {
		t.fn(Progress{Step: "write", Done: 0, Total: 1, Fraction: 1}) // the writer's step, complete
	}
}
