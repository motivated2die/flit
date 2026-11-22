package chunk

import (
	"math/rand"
	"reflect"
	"slices"
	"testing"
)

const (
	rate  = 16000
	frame = 512
)

// probs builds per-frame probabilities: speech (0.9) everywhere except the
// given pauses (0.05), each [startSeconds, endSeconds).
func probs(seconds float64, silences ...[2]float64) ([]float32, int) {
	n := int(seconds * rate)
	out := make([]float32, (n+frame-1)/frame)
	for i := range out {
		out[i] = 0.9
		t := float64(i*frame) / rate
		for _, s := range silences {
			if t >= s[0] && t < s[1] {
				out[i] = 0.05
			}
		}
	}
	return out, n
}

func sec(s float64) int { return int(s * rate) }

func TestSpeechHysteresis(t *testing.T) {
	p := Defaults(rate)
	pr, n := probs(10, [2]float64{0, 1}, [2]float64{4, 5}, [2]float64{6, 6.05})
	segs := Speech(pr, frame, n, p)
	// The 50 ms dip at 6 s is shorter than MinSilence: one segment 5–10 s.
	if len(segs) != 2 {
		t.Fatalf("segments %v", segs)
	}
	near := func(got, want int) bool { return abs(got-want) <= frame+p.SpeechPad }
	if !near(segs[0].Start, sec(1)) || !near(segs[0].End, sec(4)) || !near(segs[1].Start, sec(5)) || segs[1].End != n {
		t.Fatalf("segments %v", segs)
	}
}

func TestShortAudioIsOneChunk(t *testing.T) {
	pr, n := probs(39, [2]float64{20, 21})
	if cuts := Plan(pr, frame, n, true, Defaults(rate)); len(cuts) != 0 {
		t.Fatalf("cuts %v for 39 s", cuts)
	}
}

func TestCutsAtPauses(t *testing.T) {
	// Pauses at 12 s (short), 29 s (long), 55 s, 88 s; 100 s total.
	pr, n := probs(100, [2]float64{12, 12.3}, [2]float64{29, 30.5}, [2]float64{55, 56}, [2]float64{88, 88.5})
	p := Defaults(rate)
	cuts := Plan(pr, frame, n, true, p)
	if len(cuts) != 3 {
		t.Fatalf("cuts %v", cuts)
	}
	wantNear := []float64{29.75, 55.5, 88.25} // pause middles
	for i, c := range cuts {
		if abs(c-sec(wantNear[i])) > sec(0.2) {
			t.Errorf("cut %d at %.2f s, want ~%.2f s", i, float64(c)/rate, wantNear[i])
		}
	}
	for _, s := range Spans(cuts, n) {
		if s.Len() > p.Max || s.Len() < p.Min {
			t.Errorf("chunk %v of %.1f s", s, float64(s.Len())/rate)
		}
	}
}

func TestNoPauseHardCut(t *testing.T) {
	pr, n := probs(90) // continuous speech
	p := Defaults(rate)
	cuts := Plan(pr, frame, n, true, p)
	if !reflect.DeepEqual(cuts, []int{p.Max, 2 * p.Max}) {
		t.Fatalf("cuts %v", cuts)
	}
	// 82 s: a hard cut at 40 s would leave 2 s at the end after the second
	// one at 80 s; the second chunk is split in half instead.
	pr, n = probs(82)
	cuts = Plan(pr, frame, n, true, p)
	if len(cuts) != 2 || cuts[0] != p.Max || abs(cuts[1]-(p.Max+(n-p.Max)/2)) > 1 {
		t.Fatalf("cuts %v", cuts)
	}
}

func TestAvoidsShortRemainder(t *testing.T) {
	// 44 s with pauses at 20 s and 41 s: cutting at 41 s would leave 3 s.
	pr, n := probs(44, [2]float64{20, 20.5}, [2]float64{41, 41.5})
	cuts := Plan(pr, frame, n, true, Defaults(rate))
	if len(cuts) != 1 || abs(cuts[0]-sec(20.25)) > sec(0.2) {
		t.Fatalf("cuts %v", cuts)
	}
}

// Live planning must end with the same cuts as planning the whole track,
// and never return a cut it later takes back.
func TestLiveMatchesFinal(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 20; trial++ {
		total := 60 + rng.Float64()*300
		var sil [][2]float64
		for t := 2.0; t < total; t += 3 + rng.Float64()*25 {
			sil = append(sil, [2]float64{t, t + 0.15 + rng.Float64()*2})
		}
		pr, n := probs(total, sil...)
		p := Defaults(rate)
		want := Plan(pr, frame, n, true, p)
		var committed []int
		for have := frame; have < n; have += frame * (1 + rng.Intn(400)) {
			got := Plan(pr[:have/frame], frame, have, false, p)
			if len(got) < len(committed) || !slices.Equal(got[:len(committed)], committed) {
				t.Fatalf("trial %d at %d samples: live cuts %v changed earlier %v", trial, have, got, committed)
			}
			committed = got
		}
		final := Plan(pr, frame, n, true, p)
		if !slices.Equal(final[:len(committed)], committed) || !slices.Equal(final, want) {
			t.Fatalf("trial %d: live %v, final %v", trial, committed, want)
		}
	}
}

// A long silence (the user listening) straddling the window: its
// candidate sits 1 s into it, and live planning agrees with final planning
// while the silence is still going on.
func TestLongSilenceLive(t *testing.T) {
	pr, n := probs(120, [2]float64{25, 70})
	p := Defaults(rate)
	final := Plan(pr, frame, n, true, p)
	if len(final) == 0 || abs(final[0]-sec(26)) > sec(0.1) {
		t.Fatalf("final cuts %v, want the first ~26 s", final)
	}
	for _, at := range []float64{45, 50, 60, 69} {
		have := sec(at) / frame * frame
		live := Plan(pr[:have/frame], frame, have, false, p)
		if len(live) > 0 && live[0] != final[0] {
			t.Fatalf("at %.0f s live cut %v, final %v", at, live, final)
		}
	}
}

func TestSpans(t *testing.T) {
	got := Spans([]int{10, 25}, 30)
	want := []Span{{0, 10}, {10, 25}, {25, 30}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Spans = %v", got)
	}
	if got := Spans(nil, 5); !reflect.DeepEqual(got, []Span{{0, 5}}) {
		t.Fatalf("Spans(nil) = %v", got)
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
