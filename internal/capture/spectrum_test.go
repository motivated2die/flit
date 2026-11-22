package capture

import (
	"math"
	"math/cmplx"
	"math/rand/v2"
	"testing"

	"github.com/motivated2die/flit/internal/audio/wav"
)

func TestFFTMatchesDFT(t *testing.T) {
	n := 64
	a := make([]complex128, n)
	for i := range a {
		a[i] = complex(math.Sin(float64(i)*0.7)+0.3*math.Cos(float64(i)*2.1), 0)
	}
	want := make([]complex128, n)
	for k := range want {
		for i, v := range a {
			want[k] += v * cmplx.Exp(complex(0, -2*math.Pi*float64(k*i)/float64(n)))
		}
	}
	fftInPlace(a)
	for k := range a {
		if cmplx.Abs(a[k]-want[k]) > 1e-9 {
			t.Fatalf("bin %d: %v, want %v", k, a[k], want[k])
		}
	}
}

func tone(hz, amp float64, n int) []float32 {
	out := make([]float32, n)
	for i := range out {
		out[i] = float32(amp * math.Sin(2*math.Pi*hz*float64(i)/Rate))
	}
	return out
}

func TestSpectrumBands(t *testing.T) {
	s := newSpectrum()
	// Silence: nothing moves.
	s.feed(make([]float32, specWindow*2))
	for b, v := range s.bands() {
		if v != 0 {
			t.Fatalf("silence: band %d at %v", b, v)
		}
	}
	// A voice-loud tone at 1 kHz lights its band, much more than the
	// bands far from it.
	s.feed(tone(1000, 0.1, specWindow))
	bands := s.bands()
	hz := float64(Rate) / specWindow
	at := -1
	for b, r := range s.ranges {
		if float64(r[0])*hz <= 1000 && 1000 < float64(r[1])*hz {
			at = b
		}
	}
	if at < 0 || bands[at] < 0.6 {
		t.Fatalf("1 kHz band %d at %v (%v)", at, bands[at], bands)
	}
	if bands[0] > bands[at]/2 || bands[NBands-1] > bands[at]/2 {
		t.Fatalf("not shaped: %v", bands)
	}
	// Blocks of any length add up to windows.
	s2 := newSpectrum()
	pcm := tone(1000, 0.1, specWindow)
	s2.feed(pcm[:100])
	s2.feed(pcm[100:])
	if s2.bands() != bands {
		t.Fatalf("split feed %v, whole %v", s2.bands(), bands)
	}
}

// TestSpectrumSpeechCentred feeds real speech (FLEURS, German) and checks
// the voice's weight sits in the middle of the bars, not the first three.
func TestSpectrumSpeechCentred(t *testing.T) {
	a, err := wav.ReadFile("../../testdata/fleurs-de.wav")
	if err != nil || a.SampleRate != Rate {
		t.Skip("testdata/fleurs-de.wav:", err)
	}
	s := newSpectrum()
	var sum [NBands]float64
	frames := 0
	for i := 0; i+specWindow <= len(a.Samples); i += specWindow {
		s.feed(a.Samples[i : i+specWindow])
		b := s.bands()
		if max(b[0], b[1], b[2], b[3], b[4], b[5], b[6], b[7], b[8]) < 0.25 {
			continue // nobody speaks
		}
		frames++
		for k, v := range b {
			sum[k] += float64(v)
		}
	}
	tot, cm := 0.0, 0.0
	for k, v := range sum {
		tot += v
		cm += float64(k) * v
	}
	if frames < 50 || cm/tot < 3.4 {
		t.Fatalf("%d speaking frames, centre of the bars %.2f (want 3.4 or more; 4 is the middle)", frames, cm/tot)
	}
}

// TestSpectrumIgnoresSteadyNoise: hiss at -50 dBFS (a noisy room or a
// cheap microphone) must not move the bars once its floor is known.
func TestSpectrumIgnoresSteadyNoise(t *testing.T) {
	amp := math.Pow(10, -50.0/20) * math.Sqrt(3)
	rng := rand.New(rand.NewPCG(1, 2))
	pcm := make([]float32, Rate*6)
	for i := range pcm {
		pcm[i] = float32(amp * (2*rng.Float64() - 1))
	}
	s := newSpectrum()
	s.feed(pcm[:Rate*3]) // the floor settles within about 2 s
	var top float32
	for i := Rate * 3; i+specWindow <= len(pcm); i += specWindow {
		s.feed(pcm[i : i+specWindow])
		for _, v := range s.bands() {
			top = max(top, v)
		}
	}
	if top > 0.12 {
		t.Fatalf("steady noise moves the bars up to %.2f", top)
	}
}

func BenchmarkSpectrumWindow(b *testing.B) {
	s := newSpectrum()
	pcm := tone(700, 0.1, specWindow)
	for b.Loop() {
		s.feed(pcm)
	}
}
