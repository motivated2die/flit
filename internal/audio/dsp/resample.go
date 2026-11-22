// Package dsp holds the small signal-processing steps between a capture
// device and the speech engine: downmixing to mono and resampling to the
// engine's 16 kHz.
package dsp

import "math"

// Resampler converts a mono stream from one sample rate to another with a
// Kaiser-windowed sinc filter, evaluated in a polyphase table for the exact
// rational ratio (48000→16000 is 1:3, 44100→16000 is 160:441). The cutoff
// sits a little below the lower Nyquist frequency, so content the target
// rate can't hold is filtered out instead of aliasing into the speech band.
//
// It is streaming: Process may be called with blocks of any size, and the
// output equals that of one call with all the input. Flush ends the stream.
type Resampler struct {
	up, down int         // output/input ratio up:down, reduced
	half     int         // filter half-width in input samples
	table    [][]float32 // [phase][2*half] weights
	buf      []float32   // input not fully consumed, starting at bufStart
	bufStart int64       // absolute input index of buf[0]
	next     int64       // index of the next output sample
	inTotal  int64       // input samples seen
	passthru bool
}

// defaultHalfWidth is the filter half-width in samples of the lower rate.
// 32 keeps the passband flat to ~0.8 of the target's Nyquist frequency
// (6.5 kHz at 16 kHz) with a stopband below -80 dB.
const defaultHalfWidth = 32

// NewResampler converts from inRate to outRate.
func NewResampler(inRate, outRate int) *Resampler {
	g := gcd(inRate, outRate)
	r := &Resampler{up: outRate / g, down: inRate / g, half: defaultHalfWidth}
	if r.down > r.up {
		// Downsampling: the filter's length must grow with the ratio to
		// keep the same transition width at the output rate.
		r.half = (defaultHalfWidth*r.down + r.up - 1) / r.up
	}
	if r.up == r.down {
		r.passthru = true
		return r
	}
	// Cutoff relative to the input rate's Nyquist frequency.
	cutoff := 1.0
	if r.up < r.down {
		cutoff = float64(r.up) / float64(r.down)
	}
	cutoff *= 0.94
	const beta = 8.6 // Kaiser window shape: ~-85 dB sidelobes
	i0b := besselI0(beta)
	r.table = make([][]float32, r.up)
	for phase := range r.table {
		frac := float64(phase) / float64(r.up) // output position past the input sample
		w := make([]float32, 2*r.half)
		var sum float64
		for k := range w {
			// Tap k covers input sample (i - half + 1 + k); its distance
			// from the output position:
			x := float64(k-r.half+1) - frac
			v := cutoff * sinc(cutoff*x)
			u := x / float64(r.half)
			if u <= -1 || u >= 1 {
				v = 0
			} else {
				v *= besselI0(beta*math.Sqrt(1-u*u)) / i0b
			}
			w[k] = float32(v)
			sum += v
		}
		// Normalise each phase to unity gain at DC, so a constant stays
		// constant whatever the phase.
		for k := range w {
			w[k] = float32(float64(w[k]) / sum)
		}
		r.table[phase] = w
	}
	// Start as if half the filter's input were silence before the stream.
	r.buf = make([]float32, r.half-1)
	r.bufStart = -int64(r.half - 1)
	return r
}

// Process consumes in and returns the output samples that are complete.
// The returned slice is newly allocated.
func (r *Resampler) Process(in []float32) []float32 {
	if r.passthru {
		return append([]float32(nil), in...)
	}
	r.buf = append(r.buf, in...)
	r.inTotal += int64(len(in))
	return r.drain(r.inTotal)
}

// Flush returns the remaining output, treating the input as followed by
// silence, so the output has round(inputLength × out/in) samples.
func (r *Resampler) Flush() []float32 {
	if r.passthru {
		return nil
	}
	r.buf = append(r.buf, make([]float32, r.half)...)
	return r.drain(r.inTotal + int64(r.half))
}

// drain produces every output sample whose filter window lies within the
// first `avail` input samples, never past the input's real length.
func (r *Resampler) drain(avail int64) []float32 {
	want := (r.inTotal*int64(r.up) + int64(r.down) - 1) / int64(r.down) // ceil: outputs for the real input
	var out []float32
	for r.next < want {
		pos := r.next * int64(r.down) // in units of 1/up input samples
		i := pos / int64(r.up)
		phase := int(pos % int64(r.up))
		last := i + int64(r.half) // last input index the filter reads
		if last >= avail {
			break
		}
		first := i - int64(r.half) + 1
		w := r.table[phase]
		x := r.buf[first-r.bufStart : last-r.bufStart+1]
		var acc float32
		for k, v := range w {
			acc += v * x[k]
		}
		out = append(out, acc)
		r.next++
	}
	// Drop input no future output needs.
	need := (r.next*int64(r.down))/int64(r.up) - int64(r.half) + 1
	if drop := need - r.bufStart; drop > 0 && drop <= int64(len(r.buf)) {
		r.buf = append(r.buf[:0], r.buf[drop:]...)
		r.bufStart += drop
	}
	return out
}

func sinc(x float64) float64 {
	if x == 0 {
		return 1
	}
	return math.Sin(math.Pi*x) / (math.Pi * x)
}

// besselI0 is the zeroth-order modified Bessel function of the first kind.
func besselI0(x float64) float64 {
	sum, term := 1.0, 1.0
	for k := 1; k < 50; k++ {
		term *= (x / 2) / float64(k)
		sum += term * term
		if term*term < 1e-12*sum {
			break
		}
	}
	return sum
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// Downmix averages interleaved channels into mono, appending to dst.
func Downmix(dst, in []float32, channels int) []float32 {
	if channels <= 1 {
		return append(dst, in...)
	}
	frames := len(in) / channels
	scale := 1 / float32(channels)
	for f := 0; f < frames; f++ {
		var s float32
		for c := 0; c < channels; c++ {
			s += in[f*channels+c]
		}
		dst = append(dst, s*scale)
	}
	return dst
}
