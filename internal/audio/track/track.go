// Package track provides a unified random-access reader over flit's audio
// track formats (.m4a, crash-safe growing .aac, and legacy .wav), plus an
// on-the-fly mono resampler for the 16 kHz speech engine.
package track

import (
	"fmt"
	"io"
	"os"

	"github.com/motivated2die/flit/internal/audio/dsp"
	"github.com/motivated2die/flit/internal/audio/m4a"
	"github.com/motivated2die/flit/internal/audio/wav"
)

// File is a seekable audio track (.m4a, .aac, or .wav).
type File interface {
	SampleRate() int
	Channels() int
	Frames() int64
	ReadInterleaved(dst []float32, frame int64) (int, error)
	ReadFrames(dst []float32, frame int64) (int, error) // mono
	Close() error
}

type wavAdapter struct{ *wav.File }

func (w wavAdapter) SampleRate() int { return w.Format.SampleRate }
func (w wavAdapter) Channels() int   { return w.Format.Channels }

type m4aAdapter struct{ *m4a.File }

func (m m4aAdapter) SampleRate() int { return m.Format.SampleRate }
func (m m4aAdapter) Channels() int   { return m.Format.Channels }

// Open opens a .wav, .m4a, or growing .aac file by inspecting its header.
func Open(path string) (File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	var hdr [12]byte
	n, _ := f.ReadAt(hdr[:], 0)
	f.Close()
	if n >= 12 && string(hdr[0:4]) == "RIFF" && string(hdr[8:12]) == "WAVE" {
		wf, err := wav.Open(path)
		if err != nil {
			return nil, err
		}
		return wavAdapter{wf}, nil
	}
	if n >= 7 && ((n >= 8 && string(hdr[4:8]) == "ftyp") || (hdr[0] == 0xFF && (hdr[1]&0xF6) == 0xF0)) {
		mf, err := m4a.Open(path)
		if err != nil {
			return nil, err
		}
		return m4aAdapter{mf}, nil
	}
	return nil, fmt.Errorf("%s: unrecognised audio format", path)
}

// MonoSource adapts any File to a mono stream at targetRate (e.g. 16 kHz for
// transcription), implementing pipeline.Source. When the file is already at
// targetRate it passes through with zero resampling overhead.
type MonoSource struct {
	f    File
	rate int
}

// OpenMono opens path as a mono source at targetRate Hz.
func OpenMono(path string, targetRate int) (*MonoSource, error) {
	f, err := Open(path)
	if err != nil {
		return nil, err
	}
	return &MonoSource{f: f, rate: targetRate}, nil
}

// Close closes the underlying file.
func (s *MonoSource) Close() error { return s.f.Close() }

// Frames returns the track length in targetRate frames.
func (s *MonoSource) Frames() int64 {
	n := s.f.Frames()
	if s.f.SampleRate() == s.rate {
		return n
	}
	return n * int64(s.rate) / int64(s.f.SampleRate())
}

// ReadFrames reads mono samples at targetRate from frame into dst.
func (s *MonoSource) ReadFrames(dst []float32, frame int64) (int, error) {
	inRate := s.f.SampleRate()
	if inRate == s.rate {
		return s.f.ReadFrames(dst, frame)
	}
	total := s.Frames()
	if frame < 0 || frame >= total {
		return 0, io.EOF
	}
	want := min(int64(len(dst)), total-frame)
	if want <= 0 {
		return 0, nil
	}
	// Read with a small margin on both sides so the Kaiser-sinc filter warms up
	// cleanly with no edge attenuation at window boundaries.
	const marginOut = 64
	startOut := max(int64(0), frame-marginOut)
	endOut := min(total, frame+want+marginOut)

	inStart := startOut * int64(inRate) / int64(s.rate)
	inEnd := (endOut*int64(inRate) + int64(s.rate) - 1) / int64(s.rate)
	inEnd = min(s.f.Frames(), inEnd+int64(inRate/s.rate)+4)
	inCount := inEnd - inStart
	if inCount <= 0 {
		return 0, io.EOF
	}

	raw := make([]float32, inCount)
	got, err := s.f.ReadFrames(raw, inStart)
	if err != nil && err != io.EOF {
		return 0, err
	}
	raw = raw[:got]
	if len(raw) == 0 {
		return 0, io.EOF
	}

	rs := dsp.NewResampler(inRate, s.rate)
	out := rs.Process(raw)
	out = append(out, rs.Flush()...)

	// Offset of requested `frame` inside `out` (inStart corresponds to out[0]).
	skip := int(frame - startOut)
	if skip < 0 || skip >= len(out) {
		return 0, io.EOF
	}
	copied := copy(dst[:want], out[skip:])
	return copied, nil
}

// ReadAllMono decodes an entire audio file (.m4a, .aac, or .wav) to mono
// float32 at targetRate Hz in one sequential pass.
func ReadAllMono(path string, targetRate int) ([]float32, error) {
	f, err := Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	inRate := f.SampleRate()
	n := f.Frames()
	if n == 0 {
		return nil, nil
	}
	if inRate == targetRate {
		out := make([]float32, n)
		got, err := f.ReadFrames(out, 0)
		if err != nil && err != io.EOF {
			return nil, err
		}
		return out[:got], nil
	}
	rs := dsp.NewResampler(inRate, targetRate)
	out := make([]float32, 0, n*int64(targetRate)/int64(inRate)+64)
	chunk := make([]float32, inRate) // 1 s chunks
	for pos := int64(0); pos < n; {
		got, err := f.ReadFrames(chunk, pos)
		if got > 0 {
			out = append(out, rs.Process(chunk[:got])...)
			pos += int64(got)
		}
		if err == io.EOF || got == 0 {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	out = append(out, rs.Flush()...)
	return out, nil
}
