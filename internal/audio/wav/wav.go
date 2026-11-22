// Package wav reads and writes RIFF WAVE files.
//
// The Writer streams 16-bit PCM and keeps the header valid as it goes
// (Sync), so a recording cut off by a crash is still a readable file up to
// its last sync. The reader takes 8/16/24/32-bit integer and 32/64-bit float
// PCM, including WAVE_FORMAT_EXTENSIBLE, and returns float32 samples.
package wav

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
)

const (
	formatPCM        = 1
	formatFloat      = 3
	formatExtensible = 0xFFFE
	headerSize       = 44
)

// Format describes interleaved PCM audio.
type Format struct {
	SampleRate int
	Channels   int
}

// Writer writes 16-bit PCM to a file.
type Writer struct {
	f       *os.File
	bw      *bufio.Writer
	format  Format
	samples int64 // interleaved samples written
	buf     []byte
}

// Create creates (or truncates) path and writes a header for an empty file.
func Create(path string, format Format) (*Writer, error) {
	if format.SampleRate <= 0 || format.Channels <= 0 {
		return nil, fmt.Errorf("wav: bad format %+v", format)
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	w := &Writer{f: f, bw: bufio.NewWriterSize(f, 64<<10), format: format}
	if _, err := f.Write(header(format, 0)); err != nil {
		f.Close()
		return nil, err
	}
	if _, err := f.Seek(headerSize, io.SeekStart); err != nil {
		f.Close()
		return nil, err
	}
	return w, nil
}

// Write appends interleaved float32 samples in [-1, 1] (clipped beyond).
func (w *Writer) Write(samples []float32) error {
	if cap(w.buf) < 2*len(samples) {
		w.buf = make([]byte, 2*len(samples))
	}
	b := w.buf[:2*len(samples)]
	for i, s := range samples {
		binary.LittleEndian.PutUint16(b[2*i:], uint16(FloatToInt16(s)))
	}
	if _, err := w.bw.Write(b); err != nil {
		return err
	}
	w.samples += int64(len(samples))
	return nil
}

// Frames returns the number of frames (samples per channel) written.
func (w *Writer) Frames() int64 { return w.samples / int64(w.format.Channels) }

// Sync flushes buffered samples and rewrites the header's sizes, so the
// file is complete up to here.
func (w *Writer) Sync() error {
	if err := w.bw.Flush(); err != nil {
		return err
	}
	if _, err := w.f.WriteAt(header(w.format, w.samples*2), 0); err != nil {
		return err
	}
	return w.f.Sync()
}

// Close syncs and closes the file.
func (w *Writer) Close() error {
	err := w.Sync()
	if cerr := w.f.Close(); err == nil {
		err = cerr
	}
	return err
}

func header(f Format, dataBytes int64) []byte {
	if dataBytes > math.MaxUint32-headerSize {
		dataBytes = math.MaxUint32 - headerSize // RIFF's limit (~4 GB)
	}
	h := make([]byte, headerSize)
	copy(h[0:], "RIFF")
	binary.LittleEndian.PutUint32(h[4:], uint32(36+dataBytes))
	copy(h[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(h[16:], 16)
	binary.LittleEndian.PutUint16(h[20:], formatPCM)
	binary.LittleEndian.PutUint16(h[22:], uint16(f.Channels))
	binary.LittleEndian.PutUint32(h[24:], uint32(f.SampleRate))
	binary.LittleEndian.PutUint32(h[28:], uint32(f.SampleRate*f.Channels*2))
	binary.LittleEndian.PutUint16(h[32:], uint16(f.Channels*2))
	binary.LittleEndian.PutUint16(h[34:], 16)
	copy(h[36:], "data")
	binary.LittleEndian.PutUint32(h[40:], uint32(dataBytes))
	return h
}

// FloatToInt16 converts a sample in [-1, 1] to 16 bits, clipping.
func FloatToInt16(s float32) int16 {
	v := math.Round(float64(s) * 32767)
	return int16(max(-32768, min(32767, v)))
}

// Audio is a decoded file.
type Audio struct {
	Format
	Samples []float32 // interleaved
}

// Frames returns the number of frames.
func (a Audio) Frames() int { return len(a.Samples) / a.Channels }

// Duration in seconds.
func (a Audio) Seconds() float64 { return float64(a.Frames()) / float64(a.SampleRate) }

// ReadFile decodes a whole WAV file.
func ReadFile(path string) (Audio, error) {
	f, err := os.Open(path)
	if err != nil {
		return Audio{}, err
	}
	defer f.Close()
	return Read(bufio.NewReaderSize(f, 256<<10))
}

// ErrFormat means the file isn't a WAV this package reads.
var ErrFormat = errors.New("wav: unsupported format")

// Read decodes a WAV stream. A data chunk whose size runs past the end of
// the stream (a file whose header wasn't updated) is read to the end.
func Read(r io.Reader) (Audio, error) {
	var riff [12]byte
	if _, err := io.ReadFull(r, riff[:]); err != nil {
		return Audio{}, fmt.Errorf("%w: %v", ErrFormat, err)
	}
	if string(riff[0:4]) != "RIFF" || string(riff[8:12]) != "WAVE" {
		return Audio{}, fmt.Errorf("%w: not RIFF/WAVE", ErrFormat)
	}
	var (
		tag      uint16
		channels int
		rate     int
		bits     int
		haveFmt  bool
	)
	for {
		var ch [8]byte
		if _, err := io.ReadFull(r, ch[:]); err != nil {
			return Audio{}, fmt.Errorf("%w: no data chunk", ErrFormat)
		}
		id, size := string(ch[0:4]), int64(binary.LittleEndian.Uint32(ch[4:]))
		switch id {
		case "fmt ":
			if size < 16 || size > 1024 {
				return Audio{}, fmt.Errorf("%w: fmt chunk of %d bytes", ErrFormat, size)
			}
			b := make([]byte, size+size%2)
			if _, err := io.ReadFull(r, b); err != nil {
				return Audio{}, err
			}
			tag = binary.LittleEndian.Uint16(b[0:])
			channels = int(binary.LittleEndian.Uint16(b[2:]))
			rate = int(binary.LittleEndian.Uint32(b[4:]))
			bits = int(binary.LittleEndian.Uint16(b[14:]))
			if tag == formatExtensible && size >= 40 {
				tag = binary.LittleEndian.Uint16(b[24:]) // SubFormat GUID's first two bytes
			}
			haveFmt = true
		case "data":
			if !haveFmt {
				return Audio{}, fmt.Errorf("%w: data before fmt", ErrFormat)
			}
			if channels <= 0 || rate <= 0 {
				return Audio{}, fmt.Errorf("%w: %d channels at %d Hz", ErrFormat, channels, rate)
			}
			data, err := io.ReadAll(io.LimitReader(r, size))
			if err != nil {
				return Audio{}, err
			}
			samples, err := decode(data, tag, bits)
			if err != nil {
				return Audio{}, err
			}
			// Drop a trailing partial frame.
			samples = samples[:len(samples)/channels*channels]
			return Audio{Format: Format{SampleRate: rate, Channels: channels}, Samples: samples}, nil
		default:
			if _, err := io.CopyN(io.Discard, r, size+size%2); err != nil {
				return Audio{}, fmt.Errorf("%w: no data chunk", ErrFormat)
			}
		}
	}
}

func decode(b []byte, tag uint16, bits int) ([]float32, error) {
	switch {
	case tag == formatPCM && bits == 16:
		out := make([]float32, len(b)/2)
		for i := range out {
			out[i] = float32(int16(binary.LittleEndian.Uint16(b[2*i:]))) / 32768
		}
		return out, nil
	case tag == formatPCM && bits == 24:
		out := make([]float32, len(b)/3)
		for i := range out {
			v := int32(uint32(b[3*i])<<8|uint32(b[3*i+1])<<16|uint32(b[3*i+2])<<24) >> 8
			out[i] = float32(v) / 8388608
		}
		return out, nil
	case tag == formatPCM && bits == 32:
		out := make([]float32, len(b)/4)
		for i := range out {
			out[i] = float32(float64(int32(binary.LittleEndian.Uint32(b[4*i:]))) / 2147483648)
		}
		return out, nil
	case tag == formatPCM && bits == 8:
		out := make([]float32, len(b))
		for i, v := range b {
			out[i] = (float32(v) - 128) / 128
		}
		return out, nil
	case tag == formatFloat && bits == 32:
		out := make([]float32, len(b)/4)
		for i := range out {
			out[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:]))
		}
		return out, nil
	case tag == formatFloat && bits == 64:
		out := make([]float32, len(b)/8)
		for i := range out {
			out[i] = float32(math.Float64frombits(binary.LittleEndian.Uint64(b[8*i:])))
		}
		return out, nil
	}
	return nil, fmt.Errorf("%w: format tag %d, %d bits", ErrFormat, tag, bits)
}
