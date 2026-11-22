// Package pipeline turns a recorded track into transcript segments: voice
// activity, pause-aligned chunks, one offline ASR run per chunk, results
// shifted back into the track's time line.
//
// It talks to the engine through the Engine interface, so it runs the same
// against the engine worker (internal/engine) and against fakes in tests.
package pipeline

import (
	"context"
	"strings"
	"time"

	"github.com/motivated2die/flit/internal/chunk"
	"github.com/motivated2die/flit/internal/transcript"
)

// Rate is the engine's sample rate.
const Rate = 16000

// Engine is what the pipeline needs from the speech engine.
type Engine interface {
	// VAD scores 16 kHz mono PCM: one speech probability per frame of
	// frameSamples samples.
	VAD(ctx context.Context, pcm []float32) (probs []float32, frameSamples int, err error)
	// Transcribe runs ASR on one chunk; times are relative to the chunk.
	Transcribe(ctx context.Context, pcm []float32, opt Options) ([]Segment, error)
}

// Diarizer is an engine that can also tell speakers apart.
type Diarizer interface {
	// Diarize finds who spoke when in a whole track, in one pass so that
	// speaker numbers hold across all of it.
	Diarize(ctx context.Context, pcm []float32) ([]SpeakerTurn, error)
}

// SpeakerTurn is a stretch of one speaker's speech, times in milliseconds;
// speakers are numbered from 1 in order of first appearance.
type SpeakerTurn struct {
	T0, T1  int64
	Speaker int
}

// Turns converts diarization turns to the transcript's time base.
func Turns(turns []SpeakerTurn) []transcript.Turn {
	out := make([]transcript.Turn, len(turns))
	for i, t := range turns {
		out[i] = transcript.Turn{Start: time.Duration(t.T0) * time.Millisecond, End: time.Duration(t.T1) * time.Millisecond, Speaker: t.Speaker}
	}
	return out
}

// Options for an ASR run.
type Options struct {
	Language   string   `json:"language,omitempty"`
	Vocabulary []string `json:"vocabulary,omitempty"`
	// Speakers asks the session job to tell the speakers on the meeting's
	// track apart (not sent to the engine).
	Speakers bool `json:"-"`
}

// Segment is an engine result, times in milliseconds.
type Segment struct {
	T0, T1 int64
	Text   string
	Words  []Word
}

// Word is a word with times in milliseconds.
type Word struct {
	T0, T1 int64
	Text   string
}

// Progress reports how far a track is (samples done of total).
type Progress func(done, total int)

// Track transcribes a whole track in memory: a Progressive run to its
// end. progress gets samples done of the track's samples.
func Track(ctx context.Context, e Engine, pcm []float32, track transcript.Track, opt Options, progress Progress) ([]transcript.Segment, error) {
	p := &Progressive{Track: track}
	var report StepReport
	if progress != nil {
		report = func(done, total int64) { progress(int(done), int(total)) }
	}
	_, err := p.Step(ctx, e, Samples(pcm), true, opt, nil, report)
	return p.Segments, err
}

func hasSpeech(s chunk.Span, speech []chunk.Span) bool {
	for _, sp := range speech {
		if sp.Start < s.End && sp.End > s.Start {
			return true
		}
	}
	return false
}

func at(sample int) time.Duration { return time.Duration(sample) * time.Second / Rate }

// Shift converts engine segments of a chunk starting at offset into
// transcript segments, dropping empty ones. Segments with word times are
// split where the speaker pauses (see Split): engines such as Parakeet
// return one segment per chunk, far too coarse for a transcript.
func Shift(segs []Segment, track transcript.Track, offset time.Duration) []transcript.Segment {
	ms := func(v int64) time.Duration { return offset + time.Duration(v)*time.Millisecond }
	var out []transcript.Segment
	for _, s := range segs {
		for _, part := range Split(s) {
			text := strings.TrimSpace(part.Text)
			if text == "" {
				continue
			}
			ts := transcript.Segment{Track: track, Start: ms(part.T0), End: ms(part.T1), Text: text}
			for _, w := range part.Words {
				ts.Words = append(ts.Words, transcript.Word{Start: ms(w.T0), End: ms(w.T1), Text: strings.TrimSpace(w.Text)})
			}
			out = append(out, ts)
		}
	}
	return out
}

// Pauses that end a segment: any pause this long, or a shorter one after
// a sentence ends.
const (
	splitPause    = 800  // ms
	sentencePause = 300  // ms
	maxSegment    = 20e3 // ms; longer stretches split at their next sentence end
)

// Split cuts a segment with word times at pauses. Without words it is
// returned as it is.
func Split(s Segment) []Segment {
	if len(s.Words) < 2 {
		return []Segment{s}
	}
	var out []Segment
	cur := Segment{T0: s.Words[0].T0}
	for i, w := range s.Words {
		if i > 0 {
			prev := s.Words[i-1]
			gap := w.T0 - prev.T1
			sentenceEnd := strings.ContainsAny(lastRune(prev.Text), ".?!")
			if gap >= splitPause || (sentenceEnd && (gap >= sentencePause || w.T0-cur.T0 >= maxSegment)) {
				out = append(out, cur)
				cur = Segment{T0: w.T0}
			}
		}
		cur.Words = append(cur.Words, w)
		cur.T1 = w.T1
		if cur.Text == "" {
			cur.Text = strings.TrimSpace(w.Text)
		} else {
			cur.Text += " " + strings.TrimSpace(w.Text)
		}
	}
	return append(out, cur)
}

func lastRune(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	r := []rune(s)
	return string(r[len(r)-1])
}
