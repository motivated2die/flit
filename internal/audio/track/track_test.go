package track

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/motivated2die/flit/internal/audio/m4a"
	"github.com/motivated2die/flit/internal/audio/wav"
)

func amplitude(x []float32, rate int, freq float64) float64 {
	start, end := len(x)/4, 3*len(x)/4
	var re, im float64
	for i := start; i < end; i++ {
		ph := 2 * math.Pi * freq * float64(i) / float64(rate)
		re += float64(x[i]) * math.Cos(ph)
		im += float64(x[i]) * math.Sin(ph)
	}
	return 2 * math.Hypot(re, im) / float64(end-start)
}

func TestOpenMonoWAVAndM4A(t *testing.T) {
	dir := t.TempDir()

	// Legacy 16 kHz mono WAV passthrough.
	wavPath := filepath.Join(dir, "legacy.wav")
	ww, err := wav.Create(wavPath, wav.Format{SampleRate: 16000, Channels: 1})
	if err != nil {
		t.Fatal(err)
	}
	w16 := make([]float32, 16000)
	for i := range w16 {
		w16[i] = float32(0.5 * math.Sin(2*math.Pi*440*float64(i)/16000))
	}
	ww.Write(w16)
	ww.Close()

	src, err := OpenMono(wavPath, 16000)
	if err != nil {
		t.Fatal(err)
	}
	if src.Frames() != 16000 {
		t.Fatalf("wav frames %d", src.Frames())
	}
	buf := make([]float32, 16000)
	got, err := src.ReadFrames(buf, 0)
	src.Close()
	if err != nil || got != 16000 {
		t.Fatalf("wav read %d %v", got, err)
	}
	if a := amplitude(buf, 16000, 440); math.Abs(a-0.5) > 0.01 {
		t.Errorf("wav amp %.4f", a)
	}

	if !m4a.Available() {
		return
	}

	// 48 kHz stereo M4A -> 16 kHz mono source + ReadAllMono.
	m4aPath := filepath.Join(dir, "them.m4a")
	mw, err := m4a.Create(m4aPath, m4a.Format{SampleRate: 48000, Channels: 2, Bitrate: 96000})
	if err != nil {
		t.Fatal(err)
	}
	const frames48 = 48000 * 2
	st := make([]float32, frames48*2)
	for i := range frames48 {
		v := float32(0.5 * math.Sin(2*math.Pi*440*float64(i)/48000))
		st[2*i], st[2*i+1] = v, v
	}
	mw.Write(st)
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}

	ms, err := OpenMono(m4aPath, 16000)
	if err != nil {
		t.Fatal(err)
	}
	defer ms.Close()
	if ms.Frames() != 32000 {
		t.Fatalf("m4a 16k frames %d, want 32000", ms.Frames())
	}
	// Read a middle window via random seek.
	mid := make([]float32, 8000)
	got, err = ms.ReadFrames(mid, 12000)
	if err != nil || got != 8000 {
		t.Fatalf("m4a mid read %d %v", got, err)
	}
	if a := amplitude(mid, 16000, 440); math.Abs(a-0.5) > 0.02 {
		t.Errorf("m4a 16k seeked amp %.4f, want 0.5", a)
	}

	all, err := ReadAllMono(m4aPath, 16000)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) < 31990 || len(all) > 32010 {
		t.Fatalf("ReadAllMono len %d, want ~32000", len(all))
	}
	if a := amplitude(all, 16000, 440); math.Abs(a-0.5) > 0.02 {
		t.Errorf("ReadAllMono amp %.4f, want 0.5", a)
	}
}
