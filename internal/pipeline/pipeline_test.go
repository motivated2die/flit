package pipeline

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/motivated2die/flit/internal/transcript"
)

// fakeEngine "hears" speech where the PCM is non-zero and answers each
// chunk with one segment naming the chunk's first sample value, so tests
// can see which chunk produced what.
type fakeEngine struct {
	chunks [][2]int // lengths and first values of the chunks transcribed
	fail   int      // fail on this chunk (1-based); 0 = never
}

func (f *fakeEngine) VAD(_ context.Context, pcm []float32) ([]float32, int, error) {
	const frame = 512
	probs := make([]float32, (len(pcm)+frame-1)/frame)
	for i := range probs {
		end := min(len(pcm), (i+1)*frame)
		for _, v := range pcm[i*frame : end] {
			if v != 0 {
				probs[i] = 0.95
				break
			}
		}
	}
	return probs, frame, nil
}

func (f *fakeEngine) Transcribe(_ context.Context, pcm []float32, opt Options) ([]Segment, error) {
	f.chunks = append(f.chunks, [2]int{len(pcm), int(pcm[len(pcm)/2] * 100)})
	if f.fail == len(f.chunks) {
		return nil, errors.New("boom")
	}
	return []Segment{
		{T0: 0, T1: 1000, Text: fmt.Sprintf(" chunk %d %s ", len(f.chunks), opt.Language), Words: []Word{{T0: 100, T1: 400, Text: " w "}}},
		{T0: 1000, T1: 1200, Text: "   "},
	}, nil
}

// track builds audio: value v during speech, 0 in pauses, seconds each.
func track(parts ...float64) []float32 {
	var out []float32
	for i := 0; i+1 < len(parts); i += 2 {
		n := int(parts[i] * Rate)
		for j := 0; j < n; j++ {
			out = append(out, float32(parts[i+1]))
		}
	}
	return out
}

func TestTrackChunksAndShifts(t *testing.T) {
	// 25 s speech, 1 s pause, 25 s speech, 60 s silence, 10 s speech.
	pcm := track(25, 0.1, 1, 0, 25, 0.2, 60, 0, 10, 0.3)
	e := &fakeEngine{}
	var last [2]int
	segs, err := Track(context.Background(), e, pcm, transcript.Them, Options{Language: "de"}, func(d, n int) { last = [2]int{d, n} })
	if err != nil {
		t.Fatal(err)
	}
	// The silent stretch is a chunk of its own (or several) and is skipped.
	if len(e.chunks) != 3 {
		t.Fatalf("transcribed %d chunks: %v", len(e.chunks), e.chunks)
	}
	if last != [2]int{len(pcm), len(pcm)} {
		t.Fatalf("progress ended at %v", last)
	}
	if len(segs) != 3 {
		t.Fatalf("segments %+v", segs)
	}
	// The second chunk starts in the 1 s pause after the first 25 s.
	if s := segs[1].Start; s < 25*time.Second || s > 26*time.Second {
		t.Fatalf("second chunk starts at %v", s)
	}
	if segs[1].Words[0].Start-segs[1].Start != 100*time.Millisecond || segs[1].Words[0].Text != "w" {
		t.Fatalf("word %+v in %+v", segs[1].Words[0], segs[1])
	}
	if segs[0].Track != transcript.Them || segs[0].Text != "chunk 1 de" {
		t.Fatalf("first %+v", segs[0])
	}
}

func TestTrackReportsChunkErrors(t *testing.T) {
	pcm := track(25, 0.1, 1, 0, 25, 0.2)
	e := &fakeEngine{fail: 2}
	segs, err := Track(context.Background(), e, pcm, transcript.Me, Options{}, nil)
	if err == nil || len(segs) != 1 {
		t.Fatalf("err %v, %d segments (want the first chunk's)", err, len(segs))
	}
}

func TestTrackSilence(t *testing.T) {
	segs, err := Track(context.Background(), &fakeEngine{}, make([]float32, 5*Rate), transcript.Me, Options{}, nil)
	if err != nil || len(segs) != 0 {
		t.Fatalf("%v %v", segs, err)
	}
}

func TestTrackCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Track(ctx, &fakeEngine{}, track(10, 0.1), transcript.Me, Options{}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
}

func TestSplitAtPauses(t *testing.T) {
	w := func(t0, t1 int64, s string) Word { return Word{T0: t0, T1: t1, Text: s} }
	seg := Segment{T0: 0, T1: 9000, Text: "ignored", Words: []Word{
		w(0, 300, "Hallo"), w(350, 700, "zusammen."), // sentence end, 400 ms pause: split
		w(1100, 1400, "Wir"), w(1450, 1800, "fangen"), w(1850, 2100, "an,"),
		w(2200, 2500, "oder"),   // 100 ms after a comma: no split
		w(4000, 4300, "nicht?"), // 1.5 s pause: split
	}}
	got := Split(seg)
	var texts []string
	for _, s := range got {
		texts = append(texts, s.Text)
	}
	want := []string{"Hallo zusammen.", "Wir fangen an, oder", "nicht?"}
	if fmt.Sprint(texts) != fmt.Sprint(want) {
		t.Fatalf("split %q, want %q", texts, want)
	}
	if got[1].T0 != 1100 || got[1].T1 != 2500 || len(got[1].Words) != 4 {
		t.Fatalf("second %+v", got[1])
	}
	if one := Split(Segment{T0: 5, T1: 9, Text: "no words"}); len(one) != 1 || one[0].Text != "no words" {
		t.Fatalf("without words: %+v", one)
	}
}

// A growing recording transcribed step by step (as during a meeting, 7 s
// of audio more each time, sometimes saved and taken up again) gives the
// same chunks and segments as transcribing it once at the end.
func TestProgressiveEqualsWhole(t *testing.T) {
	var parts []float64
	for i := 0; i < 40; i++ { // 40 turns of 3–17 s with pauses of 0.3–2.7 s
		parts = append(parts, float64(3+(i*7)%15), float64(i%9+1)/10, float64((i*3)%9)/3+0.3, 0)
	}
	parts = append(parts, 70, 0, 12, 0.5) // a long silence, then speech
	pcm := track(parts...)

	whole := &fakeEngine{}
	want, err := Track(context.Background(), whole, pcm, transcript.Me, Options{}, nil)
	if err != nil {
		t.Fatal(err)
	}

	live := &fakeEngine{}
	p := &Progressive{Track: transcript.Me}
	saves := 0
	for n := 7 * Rate; n < len(pcm); n += 7 * Rate {
		if _, err := p.Step(context.Background(), live, Samples(pcm[:n]), false, Options{}, func() { saves++ }, nil); err != nil {
			t.Fatal(err)
		}
		if n%(70*Rate) < 7*Rate { // now and then: saved and taken up again
			p = &Progressive{Track: p.Track, Done: p.Done, Segments: append([]transcript.Segment(nil), p.Segments...)}
		}
	}
	beforeEnd := len(live.chunks)
	if _, err := p.Step(context.Background(), live, Samples(pcm), true, Options{}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if len(live.chunks) != len(whole.chunks) || beforeEnd < len(whole.chunks)-2 {
		t.Fatalf("%d chunks live (%d before the end), %d whole", len(live.chunks), beforeEnd, len(whole.chunks))
	}
	for i := range whole.chunks {
		if live.chunks[i] != whole.chunks[i] {
			t.Fatalf("chunk %d: live %v, whole %v", i, live.chunks[i], whole.chunks[i])
		}
	}
	if len(p.Segments) != len(want) {
		t.Fatalf("%d segments live, %d whole", len(p.Segments), len(want))
	}
	for i := range want {
		if p.Segments[i].Start != want[i].Start || p.Segments[i].End != want[i].End {
			t.Fatalf("segment %d: live %v–%v, whole %v–%v", i, p.Segments[i].Start, p.Segments[i].End, want[i].Start, want[i].End)
		}
	}
	if saves == 0 || p.Done != int64(len(pcm)) {
		t.Fatalf("saves %d, done %d of %d", saves, p.Done, len(pcm))
	}
}
