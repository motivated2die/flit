package pipeline

import (
	"context"
	"fmt"

	"github.com/motivated2die/flit/internal/chunk"
	"github.com/motivated2die/flit/internal/transcript"
)

// Source is a track to read from anywhere: a WAV file still being written
// (wav.File) or samples in memory (Samples).
type Source interface {
	Frames() int64
	ReadFrames(dst []float32, frame int64) (int, error)
}

// Samples is an in-memory Source.
type Samples []float32

// Frames is the number of samples.
func (s Samples) Frames() int64 { return int64(len(s)) }

// ReadFrames copies samples from frame on.
func (s Samples) ReadFrames(dst []float32, frame int64) (int, error) {
	if frame >= int64(len(s)) {
		return 0, nil
	}
	return copy(dst, s[frame:]), nil
}

// Progressive transcribes one track chunk by chunk: during a recording
// (each Step transcribes the chunks whose cut is certain, so the result
// equals transcribing the whole track afterwards) and at its end (a final
// Step does the rest). Done is always a cut; everything before it is in
// Segments. The state can be saved between steps and taken up again.
type Progressive struct {
	Track    transcript.Track     `json:"track"`
	Done     int64                `json:"done"` // samples
	Segments []transcript.Segment `json:"segments,omitempty"`
}

// Window sizes (samples).
const (
	// decideWindow is the audio past Done one cut is decided on: the
	// longest chunk (40 s), the lookahead (5 s) and room for the pause
	// there. Each chunk is decided on a fresh window from the same place,
	// so a recording transcribed while it grows and one transcribed
	// afterwards see the same voice activity and get the same chunks
	// (Silero is causal: a frame's score depends on earlier audio only).
	decideWindow = 60 * Rate
	// vadFrame is the VAD's frame (Silero: 512 samples); vadContext is
	// audio before the window given to it to warm up its state, its
	// probabilities dropped.
	vadFrame   = 512
	vadContext = 16 * vadFrame
)

// StepReport says how far a step has come: samples done of the track's
// samples so far.
type StepReport func(done, total int64)

// Step transcribes the chunks of src beyond Done that more audio can't
// change, or, with final, everything to the end. After each chunk it calls
// saved (to keep the state) and report. It returns how many chunks it
// transcribed.
func (p *Progressive) Step(ctx context.Context, e Engine, src Source, final bool, opt Options, saved func(), report StepReport) (int, error) {
	n := src.Frames()
	params := chunk.Defaults(Rate)
	chunks := 0
	for p.Done < n {
		if err := ctx.Err(); err != nil {
			return chunks, err
		}
		// The window starts on the track's grid of VAD frames, a few frames
		// before Done to warm up the VAD's state; planning starts at Done.
		grid := p.Done / vadFrame * vadFrame
		warm := min(grid, vadContext)
		from := grid - warm
		end := min(n, p.Done+decideWindow)
		last := end == n
		buf := make([]float32, end-from)
		got, err := src.ReadFrames(buf, from)
		if err != nil {
			return chunks, fmt.Errorf("reading the track: %w", err)
		}
		buf = buf[:got]
		probs, frame, err := e.VAD(ctx, buf)
		if err != nil {
			return chunks, fmt.Errorf("voice activity: %w", err)
		}
		if frame != vadFrame {
			return chunks, fmt.Errorf("voice activity: frames of %d samples, want %d", frame, vadFrame)
		}
		win := buf[warm:]
		winProbs := probs[min(len(probs), int(warm)/frame):]
		startAt := int(p.Done - grid)
		isFinal := final && last
		cuts := chunk.PlanFrom(winProbs, frame, len(win), startAt, isFinal, params)
		var spans []chunk.Span
		prev := startAt
		for _, c := range cuts {
			spans = append(spans, chunk.Span{Start: prev, End: c})
			prev = c
		}
		if isFinal && prev < len(win) {
			spans = append(spans, chunk.Span{Start: prev, End: len(win)})
		}
		if !isFinal && len(spans) > 1 {
			spans = spans[:1] // the next cut is decided on a window from this one
		}
		if len(spans) == 0 {
			return chunks, nil // nothing certain yet
		}
		speech := chunk.Speech(winProbs, frame, len(win), params)
		base := grid
		for _, s := range spans {
			if err := ctx.Err(); err != nil {
				return chunks, err
			}
			if hasSpeech(s, speech) {
				segs, err := e.Transcribe(ctx, win[s.Start:s.End], opt)
				if err != nil {
					return chunks, fmt.Errorf("chunk at %s: %w", at(int(base)+s.Start), err)
				}
				p.Segments = append(p.Segments, Shift(segs, p.Track, at(int(base)+s.Start))...)
				chunks++
			}
			p.Done = base + int64(s.End)
			if saved != nil {
				saved()
			}
			if report != nil {
				report(p.Done, n)
			}
		}
		if !last && len(cuts) == 0 {
			break // a window without a certain cut: wait for more audio
		}
	}
	if final && report != nil {
		report(n, n)
	}
	return chunks, nil
}
