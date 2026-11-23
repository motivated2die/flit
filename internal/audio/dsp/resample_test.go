package dsp

import (
	"math"
	"math/rand"
	"testing"
)

func sine(rate int, freq float64, seconds float64, amp float64) []float32 {
	n := int(float64(rate) * seconds)
	out := make([]float32, n)
	for i := range out {
		out[i] = float32(amp * math.Sin(2*math.Pi*freq*float64(i)/float64(rate)))
	}
	return out
}

// amplitude measures the amplitude of freq in x (single-bin DFT over the
// middle of the signal, away from the edges).
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

func resampleAll(in []float32, from, to int) []float32 {
	r := NewResampler(from, to)
	out := r.Process(in)
	return append(out, r.Flush()...)
}

func TestLength(t *testing.T) {
	for _, c := range []struct{ from, to, n, want int }{
		{48000, 16000, 48000, 16000},
		{44100, 16000, 44100, 16000},
		{16000, 16000, 1234, 1234},
		{8000, 16000, 8000, 16000},
		{48000, 16000, 10, 4},
	} {
		got := len(resampleAll(make([]float32, c.n), c.from, c.to))
		if got != c.want {
			t.Errorf("%d→%d of %d samples: %d, want %d", c.from, c.to, c.n, got, c.want)
		}
	}
}

func TestPassbandKeepsSpeech(t *testing.T) {
	for _, from := range []int{48000, 44100, 32000, 24000, 8000} {
		for _, f := range []float64{200, 1000, 3000, 6000, 6500} {
			if f >= float64(from)/2*0.9 {
				continue
			}
			out := resampleAll(sine(from, f, 1, 0.5), from, 16000)
			a := amplitude(out, 16000, f)
			if math.Abs(a-0.5) > 0.5*0.02 { // within 0.2 dB
				t.Errorf("%d Hz from %d: amplitude %.4f, want 0.5", int(f), from, a)
			}
		}
	}
}

func TestStopbandRemovesAliases(t *testing.T) {
	// 10 kHz and 13 kHz can't exist at 16 kHz; without filtering they would
	// fold to 6 kHz and 3 kHz, right in the speech band.
	for _, from := range []int{48000, 44100} {
		for _, f := range []float64{10000, 13000} {
			out := resampleAll(sine(from, f, 1, 0.5), from, 16000)
			alias := 16000 - f
			if a := amplitude(out, 16000, alias); a > 0.5*1e-3 { // below -60 dB
				t.Errorf("%d Hz from %d: alias at %d Hz has amplitude %.5f", int(f), from, int(alias), a)
			}
		}
	}
}

func TestStreamingEqualsOneShot(t *testing.T) {
	in := sine(44100, 440, 0.5, 0.3)
	want := resampleAll(in, 44100, 16000)
	rng := rand.New(rand.NewSource(1))
	r := NewResampler(44100, 16000)
	var got []float32
	for rest := in; len(rest) > 0; {
		n := min(len(rest), 1+rng.Intn(900))
		got = append(got, r.Process(rest[:n])...)
		rest = rest[n:]
	}
	got = append(got, r.Flush()...)
	if len(got) != len(want) {
		t.Fatalf("streaming gave %d samples, one shot %d", len(got), len(want))
	}
	for i := range want {
		if math.Abs(float64(got[i]-want[i])) > 1e-6 {
			t.Fatalf("sample %d: %v vs %v", i, got[i], want[i])
		}
	}
}

func TestConstantStaysConstant(t *testing.T) {
	in := make([]float32, 4800)
	for i := range in {
		in[i] = 0.25
	}
	out := resampleAll(in, 48000, 16000)
	for i := 40; i < len(out)-40; i++ { // away from the zero-padded edges
		if math.Abs(float64(out[i])-0.25) > 1e-4 {
			t.Fatalf("sample %d = %v", i, out[i])
		}
	}
}

func TestDownmix(t *testing.T) {
	got := Downmix(nil, []float32{1, 0, 0.5, 0.5, -1, 1}, 2)
	want := []float32{0.5, 0.5, 0}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Downmix = %v, want %v", got, want)
		}
	}
	if got := Downmix(nil, []float32{1, 2}, 1); len(got) != 2 {
		t.Fatal("mono must pass through")
	}
}

func BenchmarkResample48k(b *testing.B) {
	in := sine(48000, 440, 1, 0.5)
	b.SetBytes(int64(len(in) * 4))
	for b.Loop() {
		resampleAll(in, 48000, 16000)
	}
}
