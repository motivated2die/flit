package capture

import (
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/motivated2die/flit/internal/audio/dsp"
	"github.com/motivated2die/flit/internal/audio/m4a"
	"github.com/motivated2die/flit/internal/audio/wav"
)

// Rate is the speech engine's sample rate (16 kHz, used for the voice meter,
// Tap, and legacy WAV tracks).
const Rate = 16000

// StoreRate is the fullband sample rate (48 kHz) used for recorded M4A tracks.
const StoreRate = 48000

// Sink receives a stream's mono samples at the device's rate, on the
// capture thread: it must not block.
type Sink interface {
	Write(rate int, pcm []float32)
}

// StereoSink is optionally implemented by a Sink that records stereo audio
// (interleaved L, R samples) directly without collapsing to mono.
type StereoSink interface {
	Channels() int
	WriteInterleaved(rate int, pcm []float32)
}

// Level is a Sink that only measures: the loudest sample since the last
// TakePeak. For level meters of devices that aren't recorded.
type Level struct {
	peak atomic.Uint32
}

// Write records the block's peak.
func (l *Level) Write(rate int, pcm []float32) { raisePeak(&l.peak, peakOf(pcm)) }

// TakePeak returns the loudest sample since the last call (0..1).
func (l *Level) TakePeak() float32 { return math.Float32frombits(l.peak.Swap(0)) }

func peakOf(pcm []float32) float32 {
	var p float32
	for _, v := range pcm {
		p = max(p, float32(math.Abs(float64(v))))
	}
	return p
}

func raisePeak(a *atomic.Uint32, p float32) {
	for {
		old := a.Load()
		if p <= math.Float32frombits(old) || a.CompareAndSwap(old, math.Float32bits(p)) {
			return
		}
	}
}

type trackWriter interface {
	Write(samples []float32) error
	Sync() error
	Close() error
}

// Track writes one recorded track to disk (either 48 kHz AAC .m4a or 16 kHz
// WAV). Write/WriteInterleaved are called from the capture thread and only
// queue; a goroutine resamples, encodes and writes, so disk I/O never stalls
// the device.
type Track struct {
	w        trackWriter
	rate     int
	channels int
	in       chan block
	done     chan struct{}
	err      error
	peak     atomic.Uint32 // float32 bits of the loudest sample since TakePeak
	max      atomic.Uint32 // the loudest sample of the whole track
	frames   atomic.Int64  // frames written at t.rate
	spec     *spectrum     // the voice meter's bands (writer goroutine, 16 kHz mono)

	// Tap, if set before the first Write, receives every 16 kHz mono block as
	// written (on the writer goroutine), for live processing.
	Tap func(pcm []float32)

	closeOnce sync.Once
}

type block struct {
	rate int
	pcm  []float32 // interleaved t.channels samples
}

// SyncEvery is how often the output file is flushed to disk, bounding what a
// crash can lose.
var SyncEvery = 2 * time.Second

// NewTrack creates a 16 kHz mono WAV track (or 48 kHz mono M4A if path ends
// with .m4a).
func NewTrack(path string) (*Track, error) {
	if strings.HasSuffix(strings.ToLower(path), ".m4a") {
		return NewTrackFormat(path, StoreRate, 1, 64000)
	}
	return NewTrackFormat(path, Rate, 1, 0)
}

// NewTrackFormat creates a track at the given sample rate, channel count (1 or
// 2), and target bitrate (used when path ends with .m4a).
func NewTrackFormat(path string, rate, channels, bitrate int) (*Track, error) {
	if channels != 1 && channels != 2 {
		channels = 1
	}
	var w trackWriter
	var err error
	if strings.HasSuffix(strings.ToLower(path), ".m4a") {
		w, err = m4a.Create(path, m4a.Format{SampleRate: rate, Channels: channels, Bitrate: bitrate})
	} else {
		w, err = wav.Create(path, wav.Format{SampleRate: rate, Channels: channels})
	}
	if err != nil {
		return nil, err
	}
	t := &Track{w: w, rate: rate, channels: channels, in: make(chan block, 512), done: make(chan struct{}), spec: newSpectrum()}
	go t.run()
	return t, nil
}

// Channels returns the track's channel count (1 or 2).
func (t *Track) Channels() int { return t.channels }

// Write queues mono samples at rate. If the track is stereo, samples are
// duplicated to both channels.
func (t *Track) Write(rate int, pcm []float32) {
	if t.channels == 2 {
		st := make([]float32, len(pcm)*2)
		for i, v := range pcm {
			st[2*i], st[2*i+1] = v, v
		}
		t.WriteInterleaved(rate, st)
		return
	}
	t.WriteInterleaved(rate, pcm)
}

// WriteInterleaved queues interleaved samples (t.channels per frame) at rate.
// It never blocks the caller for long: if the writer falls far behind, the
// block is dropped (and counted as silence by the aligner's next packet).
func (t *Track) WriteInterleaved(rate int, pcm []float32) {
	p := peakOf(pcm)
	raisePeak(&t.peak, p)
	raisePeak(&t.max, p)
	select {
	case t.in <- block{rate, pcm}:
	default:
	}
}

// TakePeak returns the loudest sample since the last call (0..1).
func (t *Track) TakePeak() float32 { return math.Float32frombits(t.peak.Swap(0)) }

// Bands returns the latest levels of the voice range in NBands bands
// (0..1, updated about 31 times a second), for the live voice meter.
func (t *Track) Bands() [NBands]float32 { return t.spec.bands() }

// MaxPeak is the loudest sample of the whole track so far (0..1): near
// zero means the track is silent, which the log notes.
func (t *Track) MaxPeak() float32 { return math.Float32frombits(t.max.Load()) }

// Seconds is the length written so far.
func (t *Track) Seconds() float64 { return float64(t.frames.Load()) / float64(t.rate) }

// Close writes what is queued, finishes the file and reports the first
// write error.
func (t *Track) Close() error {
	t.closeOnce.Do(func() { close(t.in) })
	<-t.done
	return t.err
}

func (t *Track) run() {
	defer close(t.done)
	ch := t.channels
	var rs [2]*dsp.Resampler
	var specRS *dsp.Resampler
	if t.rate != Rate {
		specRS = dsp.NewResampler(t.rate, Rate)
	}
	inRate := 0
	last := time.Now()

	emit := func(pcm []float32) {
		if len(pcm) == 0 || t.err != nil {
			return
		}
		if err := t.w.Write(pcm); err != nil {
			t.err = err
			return
		}
		t.frames.Add(int64(len(pcm) / ch))
		mono := pcm
		if ch == 2 {
			mono = dsp.Downmix(nil, pcm, 2)
		}
		if specRS != nil {
			mono = specRS.Process(mono)
		}
		if len(mono) > 0 {
			t.spec.feed(mono)
			if t.Tap != nil {
				t.Tap(mono)
			}
		}
	}

	flushResamplers := func() {
		if rs[0] == nil {
			return
		}
		if ch == 1 {
			emit(rs[0].Flush())
		} else {
			l, r := rs[0].Flush(), rs[1].Flush()
			emit(interleave2(l, r))
		}
		rs = [2]*dsp.Resampler{}
	}

	for b := range t.in {
		if b.rate != inRate {
			flushResamplers()
			inRate = b.rate
			if inRate != t.rate {
				for c := range ch {
					rs[c] = dsp.NewResampler(inRate, t.rate)
				}
			}
		}
		if rs[0] != nil {
			if ch == 1 {
				emit(rs[0].Process(b.pcm))
			} else {
				lIn, rIn := deinterleave2(b.pcm)
				emit(interleave2(rs[0].Process(lIn), rs[1].Process(rIn)))
			}
		} else {
			emit(append([]float32(nil), b.pcm...))
		}
		if time.Since(last) >= SyncEvery {
			last = time.Now()
			if err := t.w.Sync(); err != nil && t.err == nil {
				t.err = err
			}
		}
	}
	flushResamplers()
	if specRS != nil {
		if tail := specRS.Flush(); len(tail) > 0 {
			t.spec.feed(tail)
			if t.Tap != nil {
				t.Tap(tail)
			}
		}
	}
	if err := t.w.Close(); err != nil && t.err == nil {
		t.err = err
	}
}

func deinterleave2(in []float32) (l, r []float32) {
	n := len(in) / 2
	l = make([]float32, n)
	r = make([]float32, n)
	for i := range n {
		l[i] = in[2*i]
		r[i] = in[2*i+1]
	}
	return l, r
}

func interleave2(l, r []float32) []float32 {
	n := min(len(l), len(r))
	if n == 0 {
		return nil
	}
	out := make([]float32, n*2)
	for i := range n {
		out[2*i] = l[i]
		out[2*i+1] = r[i]
	}
	return out
}
