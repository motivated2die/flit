package tcpp

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Sortformer on the German test meeting's Them track: three speakers, each
// turn 10-20 s (testdata/meetings, go run ./cmd/flitdev testdata).
func TestDiarizeThemTrack(t *testing.T) {
	m := testModel(t, "sortformer-4spk-v2.1")
	if _, err := os.Stat(filepath.Join(repo(), "testdata", "meetings", "de-synth-4spk", "them.wav")); err != nil {
		t.Skip("no test meetings (go run ./cmd/flitdev testdata)")
	}
	pcm := fixture(t, filepath.Join("meetings", "de-synth-4spk", "them.wav"))
	d, err := m.NewDiarizer(0)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if d.MaxSpeakers != 4 || d.SampleRate != 16000 || !d.sortformer {
		t.Fatalf("info: %+v", d)
	}
	start := time.Now()
	turns, err := d.Run(pcm)
	if err != nil {
		t.Fatal(err)
	}
	took := time.Since(start)
	speakers := map[int]time.Duration{}
	for i, tr := range turns {
		if tr.T1 <= tr.T0 || (i > 0 && tr.T0 < turns[i-1].T0) {
			t.Fatalf("turn %d: %+v", i, tr)
		}
		speakers[tr.Speaker] += time.Duration(tr.T1-tr.T0) * time.Millisecond
	}
	audio := time.Duration(len(pcm)) * time.Second / 16000
	t.Logf("%d turns, speakers %v, %.1f s audio in %v (%.0fx real time)", len(turns), speakers, audio.Seconds(), took.Round(time.Millisecond), audio.Seconds()/took.Seconds())
	// Three people speak; Sortformer splits one of them in two on this
	// meeting (docs/research/diarization.md, measured 2026-11-21).
	if len(speakers) < 3 || len(speakers) > 4 {
		t.Errorf("%d speakers, want 3 (or 4 with the known split)", len(speakers))
	}
}
