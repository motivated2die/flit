package capture

import (
	"math"
	"math/cmplx"
	"sync/atomic"
)

// Bands of a track's sound for the live voice meter: how loud the voice
// range is, in a few bands, about 31 times a second. The idea is Handy's
// overlay (references/handy.md): a short FFT, bands spaced more narrowly
// low than high over the voice's range, levels in a dB window calibrated
// on real speech, lightly smoothed across neighbours. Unlike Handy's, the
// higher bands are lifted (speech is weaker there) and each band is
// measured from just above its own noise floor. flit's own code, run on the track's
// writer goroutine (never the capture thread); the UI reads the latest
// levels from atomics.

// NBands is how many bands a track reports.
const NBands = 9

// voiceBands is the layout the meter uses.
// Measured on the repository's speech (docs/research/voice-meter-bands.md):
// 400-4000 Hz with a +6 dB/octave tilt puts the voice's weight in the
// middle of the bars instead of the first three, and the 6 dB gate over
// each band's noise floor keeps hiss that the tilt lifts from moving them.
var voiceBands = bandLayout{lo: 400, hi: 4000, spacing: 1.5, tilt: 6, gate: 6}

const (
	specWindow = 512 // samples at 16 kHz: 32 ms
	// The dB window that maps to 0..1. The level is the bands' average
	// magnitude over the window length, which lands well below true dBFS
	// for speech; Handy measured a window of -68..-30 to put dictation
	// near the top and room tone at the bottom, which we follow.
	specDBMin = -68.0
	specDBMax = -30.0
	specGain  = 1.3
	specCurve = 0.7
)

// bandLayout is where the bands sit: from lo to hi Hz, spaced by a power
// curve (1: even; 2: narrow low, wide high), and a tilt in dB per octave
// above lo that lifts the higher bands (speech falls off with frequency).
type bandLayout struct {
	lo, hi, spacing, tilt float64
	// gate, when > 0, measures each band from this many dB above its
	// noise floor (the band's quietest level over the last floorFrames
	// windows), so steady noise reads as nothing; 0 = no gate.
	gate float64
}

// floorFrames is how far back a band's noise floor looks: 64 windows,
// about 2 s; speech pauses at least that often, steady noise doesn't.
const floorFrames = 64

// spectrum turns 16 kHz audio into NBands levels.
type spectrum struct {
	gain   [NBands]float64 // the tilt, in dB, per band
	gate   float64
	hist   [floorFrames][NBands]float64 // recent levels (dB), for the noise floor
	histN  int
	buf    []float32
	window [specWindow]float64
	ranges [NBands][2]int // FFT bins per band, [from, to)
	fft    [specWindow]complex128
	out    [NBands]atomic.Uint32 // float32 bits, 0..1
}

func newSpectrum() *spectrum { return newSpectrumWith(voiceBands) }

func newSpectrumWith(l bandLayout) *spectrum {
	s := &spectrum{buf: make([]float32, 0, specWindow), gate: l.gate}
	for i := range s.window {
		s.window[i] = 0.5 * (1 - math.Cos(2*math.Pi*float64(i)/specWindow)) // Hann
	}
	hz := float64(Rate) / specWindow // per bin
	for b := range NBands {
		f0 := l.lo + (l.hi-l.lo)*math.Pow(float64(b)/NBands, l.spacing)
		f1 := l.lo + (l.hi-l.lo)*math.Pow(float64(b+1)/NBands, l.spacing)
		s.gain[b] = l.tilt * math.Log2((f0+f1)/2/l.lo)
		lo, hi := int(f0/hz), int(f1/hz)
		if hi <= lo {
			hi = lo + 1
		}
		s.ranges[b] = [2]int{lo, min(hi, specWindow/2)}
	}
	return s
}

// feed adds samples; each full window updates the levels.
func (s *spectrum) feed(pcm []float32) {
	for len(pcm) > 0 {
		n := min(len(pcm), specWindow-len(s.buf))
		s.buf = append(s.buf, pcm[:n]...)
		pcm = pcm[n:]
		if len(s.buf) == specWindow {
			s.analyse()
			s.buf = s.buf[:0]
		}
	}
}

func (s *spectrum) analyse() {
	mean := 0.0
	for _, v := range s.buf {
		mean += float64(v)
	}
	mean /= specWindow
	for i, v := range s.buf {
		s.fft[i] = complex((float64(v)-mean)*s.window[i], 0)
	}
	fftInPlace(s.fft[:])
	var lv, dbs [NBands]float64
	for b, r := range s.ranges {
		p := 0.0
		for k := r[0]; k < r[1]; k++ {
			m := cmplx.Abs(s.fft[k])
			p += m * m
		}
		p /= float64(r[1] - r[0])
		db := -80.0
		if p > 1e-12 {
			db = 20*math.Log10(math.Sqrt(p)/specWindow) + s.gain[b]
		}
		lo := specDBMin
		if s.gate > 0 && s.histN > 0 {
			// The floor is the band's quietest level in the windows before
			// this one (this one may be speech).
			floor := math.Inf(1)
			for i := range min(s.histN, floorFrames) {
				floor = min(floor, s.hist[i][b])
			}
			lo = min(max(lo, floor+s.gate), specDBMax-12) // keep some range
		}
		dbs[b] = db
		x := max(0, min(1, (db-lo)/(specDBMax-lo)))
		lv[b] = min(1, math.Pow(x*specGain, specCurve))
	}
	if s.gate > 0 { // this window joins the floor's history
		s.hist[s.histN%floorFrames] = dbs
		s.histN++
	}
	// A little smoothing across neighbours, so the curve doesn't jitter.
	for b := range NBands {
		v := lv[b]
		if b > 0 && b < NBands-1 {
			v = 0.7*lv[b] + 0.15*lv[b-1] + 0.15*lv[b+1]
		}
		s.out[b].Store(math.Float32bits(float32(v)))
	}
}

// bands returns the latest levels (any goroutine).
func (s *spectrum) bands() [NBands]float32 {
	var out [NBands]float32
	for i := range out {
		out[i] = math.Float32frombits(s.out[i].Load())
	}
	return out
}

// fftInPlace is an iterative radix-2 FFT (len(a) a power of two).
func fftInPlace(a []complex128) {
	n := len(a)
	for i, j := 1, 0; i < n; i++ { // bit-reversed order
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j |= bit
		if i < j {
			a[i], a[j] = a[j], a[i]
		}
	}
	for size := 2; size <= n; size <<= 1 {
		step := cmplx.Exp(complex(0, -2*math.Pi/float64(size)))
		for start := 0; start < n; start += size {
			w := complex(1, 0)
			for k := 0; k < size/2; k++ {
				u, v := a[start+k], a[start+k+size/2]*w
				a[start+k], a[start+k+size/2] = u+v, u-v
				w *= step
			}
		}
	}
}
