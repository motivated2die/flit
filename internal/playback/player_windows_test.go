package playback

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/motivated2die/flit/internal/audio/wav"
)

func tone(t *testing.T, path string, seconds float64, hz float64) {
	t.Helper()
	w, err := wav.Create(path, wav.Format{SampleRate: Rate, Channels: 1})
	if err != nil {
		t.Fatal(err)
	}
	pcm := make([]float32, int(seconds*Rate))
	for i := range pcm {
		pcm[i] = float32(0.05 * math.Sin(2*math.Pi*hz*float64(i)/Rate))
	}
	w.Write(pcm)
	w.Close()
}

// Plays a quiet tone on the default output: FLIT_LIVE_AUDIO=1.
func TestLivePlayerPosition(t *testing.T) {
	if os.Getenv("FLIT_LIVE_AUDIO") != "1" {
		t.Skip("set FLIT_LIVE_AUDIO=1 to play through the speakers")
	}
	dir := t.TempDir()
	tone(t, filepath.Join(dir, "me.wav"), 3, 440)
	tone(t, filepath.Join(dir, "them.wav"), 3, 660)
	p, err := Open(filepath.Join(dir, "me.wav"), filepath.Join(dir, "them.wav"))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	ended := make(chan struct{}, 1)
	p.OnChange = func() { ended <- struct{}{} }
	if err := p.PlayFrom(Rate); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	pos := p.Position()
	if pos < Rate+Rate/5 || pos > Rate+Rate*8/10 {
		t.Fatalf("after 0.5 s from 1 s: at %.2f s", float64(pos)/Rate)
	}
	p.Pause()
	paused := p.Position()
	time.Sleep(200 * time.Millisecond)
	if p.Playing() || p.Position() != paused {
		t.Fatal("still moving after Pause")
	}
	if err := p.Resume(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ended:
	case <-time.After(4 * time.Second):
		t.Fatal("didn't end")
	}
	if p.Playing() || p.Position() < 3*Rate-Rate/10 {
		t.Fatalf("at the end: playing %v, %.2f s", p.Playing(), float64(p.Position())/Rate)
	}
	// Clips of one track with a gap.
	if err := p.Play([]Range{{0, Rate / 4}, {2 * Rate, 2*Rate + Rate/4}}, []bool{false, true}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ended:
	case <-time.After(3 * time.Second):
		t.Fatal("clips didn't end")
	}
}
