package capture

import (
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/motivated2die/flit/internal/audio/dsp"
	"github.com/motivated2die/flit/internal/audio/wav"
)

// Rate is the sample rate of the written tracks (the engine's).
const Rate = 16000

// Sink receives a stream's mono samples at the device's rate, on the
// capture thread: it must not block.
type Sink interface {
	Write(rate int, pcm []float32)
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

// Track writes one recorded track to a 16 kHz mono WAV file. Write is
// called from the capture thread and only queues; a goroutine resamples
// and writes, so disk I/O never stalls the device.
type Track struct {
	w      *wav.Writer
	in     chan block
	done   chan struct{}
	err    error
	peak   atomic.Uint32 // float32 bits of the loudest sample since TakePeak
	max    atomic.Uint32 // the loudest sample of the whole track
	frames atomic.Int64  // 16 kHz frames written
	spec   *spectrum     // the voice meter's bands (writer goroutine)

	// Tap, if set before the first Write, receives every 16 kHz block as
	// written (on the writer goroutine), for live processing.
	Tap func(pcm []float32)

	closeOnce sync.Once
}

type block struct {
	rate int
	pcm  []float32
}

// SyncEvery is how often the WAV header is brought up to date, bounding
// what a crash can lose.
var SyncEvery = 2 * time.Second

// NewTrack creates the file.
func NewTrack(path string) (*Track, error) {
	w, err := wav.Create(path, wav.Format{SampleRate: Rate, Channels: 1})
	if err != nil {
		return nil, err
	}
	t := &Track{w: w, in: make(chan block, 512), done: make(chan struct{}), spec: newSpectrum()}
	go t.run()
	return t, nil
}

// Write queues mono samples at rate. It never blocks the caller for long:
// if the writer falls far behind, the block is dropped (and counted as
// silence by the aligner's next packet).
func (t *Track) Write(rate int, pcm []float32) {
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
func (t *Track) Seconds() float64 { return float64(t.frames.Load()) / Rate }

// Close writes what is queued, finishes the file and reports the first
// write error.
func (t *Track) Close() error {
	t.closeOnce.Do(func() { close(t.in) })
	<-t.done
	return t.err
}

func (t *Track) run() {
	defer close(t.done)
	var rs *dsp.Resampler
	rate := 0
	last := time.Now()
	emit := func(pcm []float32) {
		if len(pcm) == 0 || t.err != nil {
			return
		}
		if err := t.w.Write(pcm); err != nil {
			t.err = err
			return
		}
		t.frames.Add(int64(len(pcm)))
		t.spec.feed(pcm)
		if t.Tap != nil {
			t.Tap(pcm)
		}
	}
	for b := range t.in {
		if b.rate != rate {
			if rs != nil {
				emit(rs.Flush())
			}
			rs, rate = nil, b.rate
			if rate != Rate {
				rs = dsp.NewResampler(rate, Rate)
			}
		}
		if rs != nil {
			emit(rs.Process(b.pcm))
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
	if rs != nil {
		emit(rs.Flush())
	}
	if err := t.w.Close(); err != nil && t.err == nil {
		t.err = err
	}
}
