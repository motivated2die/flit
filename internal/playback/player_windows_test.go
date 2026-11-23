package playback

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/motivated2die/flit/internal/audio/m4a"
	"github.com/motivated2die/flit/internal/audio/wav"
)

func TestOpenM4AAndLegacy16kWAV(t *testing.T) {
	dir := t.TempDir()
	// Legacy 16 kHz mono WAV track.
	wavPath := filepath.Join(dir, "legacy.wav")
	ww, err := wav.Create(wavPath, wav.Format{SampleRate: 16000, Channels: 1})
	if err != nil {
		t.Fatal(err)
	}
	w16 := make([]float32, 16000)
	for i := range w16 {
		w16[i] = float32(0.4 * math.Sin(2*math.Pi*440*float64(i)/16000))
	}
	ww.Write(w16)
	ww.Close()

	p, err := Open(wavPath)
	if err != nil {
		t.Fatal(err)
	}
	if p.Length() != Rate {
		t.Fatalf("legacy 16k wav length %d, want %d (1 s at 48 kHz)", p.Length(), Rate)
	}
	buf := make([]float32, 2000*outChannels)
	if got := readStereoAt(p.tracks[0], buf, Rate/4); got != 2000 {
		t.Fatalf("readStereoAt legacy wav got %d", got)
	}
	p.Close()

	if !m4a.Available() {
		return
	}
	// 48 kHz stereo M4A + 48 kHz mono M4A opened together.
	mePath := filepath.Join(dir, "me.m4a")
	themPath := filepath.Join(dir, "them.m4a")
	mw, err := m4a.Create(mePath, m4a.Format{SampleRate: 48000, Channels: 1, Bitrate: 64000})
	if err != nil {
		t.Fatal(err)
	}
	tw, err := m4a.Create(themPath, m4a.Format{SampleRate: 48000, Channels: 2, Bitrate: 96000})
	if err != nil {
		t.Fatal(err)
	}
	mono := make([]float32, 48000)
	stereo := make([]float32, 48000*2)
	for i := range 48000 {
		mono[i] = float32(0.3 * math.Sin(2*math.Pi*440*float64(i)/48000))
		stereo[2*i] = float32(0.5 * math.Sin(2*math.Pi*660*float64(i)/48000))
		stereo[2*i+1] = float32(0.2 * math.Sin(2*math.Pi*880*float64(i)/48000))
	}
	mw.Write(mono)
	mw.Close()
	tw.Write(stereo)
	tw.Close()

	p2, err := Open(mePath, themPath)
	if err != nil {
		t.Fatal(err)
	}
	defer p2.Close()
	if p2.Length() != 48000 || p2.Tracks() != 2 {
		t.Fatalf("m4a player len=%d tracks=%d", p2.Length(), p2.Tracks())
	}
	if got := readStereoAt(p2.tracks[1], buf, 12000); got != 2000 {
		t.Fatalf("readStereoAt m4a got %d", got)
	}
}

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
