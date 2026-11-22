// Package capture records the session's tracks: the microphone, and the
// meeting's audio through endpoint or per-process loopback
// (docs/research/audio-capture.md). Each device stream runs on its own
// locked COM thread and hands mono samples to a Track, which aligns them
// to the session's time line, resamples them to 16 kHz and writes a WAV
// file off the audio thread.
package capture

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// Format is a device's sample format.
type Format struct {
	Rate       int
	Channels   int
	Bits       int
	Float      bool
	BlockAlign int
}

const (
	tagPCM        = 1
	tagFloat      = 3
	tagExtensible = 0xFFFE
)

// ErrFormat means a device format flit can't read.
var ErrFormat = errors.New("unsupported audio format")

// ParseWaveFormat reads a WAVEFORMATEX (or WAVEFORMATEXTENSIBLE) given as
// bytes.
func ParseWaveFormat(b []byte) (Format, error) {
	if len(b) < 16 {
		return Format{}, ErrFormat
	}
	tag := binary.LittleEndian.Uint16(b[0:])
	f := Format{
		Channels:   int(binary.LittleEndian.Uint16(b[2:])),
		Rate:       int(binary.LittleEndian.Uint32(b[4:])),
		BlockAlign: int(binary.LittleEndian.Uint16(b[12:])),
		Bits:       int(binary.LittleEndian.Uint16(b[14:])),
	}
	if tag == tagExtensible {
		if len(b) < 40 {
			return Format{}, fmt.Errorf("%w: short WAVEFORMATEXTENSIBLE", ErrFormat)
		}
		tag = binary.LittleEndian.Uint16(b[24:]) // SubFormat GUID's first two bytes
	}
	f.Float = tag == tagFloat
	switch {
	case f.Channels <= 0 || f.Rate <= 0 || f.BlockAlign != f.Channels*f.Bits/8:
		return Format{}, fmt.Errorf("%w: %+v", ErrFormat, f)
	case f.Float && f.Bits == 32, !f.Float && tag == tagPCM && (f.Bits == 16 || f.Bits == 24 || f.Bits == 32):
		return f, nil
	}
	return Format{}, fmt.Errorf("%w: tag %d, %d bits", ErrFormat, tag, f.Bits)
}

// ToMono decodes interleaved device samples and averages the channels,
// appending to dst.
func ToMono(dst []float32, data []byte, f Format) []float32 {
	frames := len(data) / f.BlockAlign
	bps := f.Bits / 8
	scale := 1 / float32(f.Channels)
	for i := 0; i < frames; i++ {
		var sum float32
		for c := 0; c < f.Channels; c++ {
			o := i*f.BlockAlign + c*bps
			sum += sample(data[o:o+bps], f)
		}
		dst = append(dst, sum*scale)
	}
	return dst
}

func sample(b []byte, f Format) float32 {
	switch {
	case f.Float:
		return math.Float32frombits(binary.LittleEndian.Uint32(b))
	case f.Bits == 16:
		return float32(int16(binary.LittleEndian.Uint16(b))) / 32768
	case f.Bits == 24:
		return float32(int32(uint32(b[0])<<8|uint32(b[1])<<16|uint32(b[2])<<24)>>8) / 8388608
	default:
		return float32(float64(int32(binary.LittleEndian.Uint32(b))) / 2147483648)
	}
}

// FloatStereo48k is the format flit asks a process-loopback client for
// (it has no mix format of its own; Windows converts).
func FloatStereo48k() []byte {
	b := make([]byte, 18)
	binary.LittleEndian.PutUint16(b[0:], tagFloat)
	binary.LittleEndian.PutUint16(b[2:], 2)
	binary.LittleEndian.PutUint32(b[4:], 48000)
	binary.LittleEndian.PutUint32(b[8:], 48000*8)
	binary.LittleEndian.PutUint16(b[12:], 8)
	binary.LittleEndian.PutUint16(b[14:], 32)
	return b
}
