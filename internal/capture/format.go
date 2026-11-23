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
	Rate        int
	Channels    int
	Bits        int
	Float       bool
	BlockAlign  int
	ChannelMask uint32
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
		f.ChannelMask = binary.LittleEndian.Uint32(b[20:])
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

const invSqrt2 = 0.70710678

// ToMono decodes interleaved device samples to mono, appending to dst.
// Multi-channel surround formats (5.1/7.1, e.g. virtual gaming headsets) use
// ITU-R BS.775 fold-down skipping LFE instead of dividing by channel count,
// avoiding a -12 dB drop when stereo speech plays on an 8-channel endpoint.
func ToMono(dst []float32, data []byte, f Format) []float32 {
	frames := len(data) / f.BlockAlign
	bps := f.Bits / 8
	ch := f.Channels
	for i := 0; i < frames; i++ {
		base := i * f.BlockAlign
		switch ch {
		case 1:
			dst = append(dst, sample(data[base:base+bps], f))
		case 2:
			l := sample(data[base:base+bps], f)
			r := sample(data[base+bps:base+2*bps], f)
			dst = append(dst, (l+r)*0.5)
		default:
			fl := sample(data[base:base+bps], f)
			fr := sample(data[base+bps:base+2*bps], f)
			var sum float32
			if ch == 4 {
				// Quad: FL, FR, BL, BR
				bl := sample(data[base+2*bps:base+3*bps], f)
				br := sample(data[base+3*bps:base+4*bps], f)
				sum = 0.5*(fl+fr) + 0.5*invSqrt2*(bl+br)
			} else if ch >= 6 {
				// 5.1 / 7.1: FL, FR, FC, LFE (skip), BL, BR, [SL, SR]
				fc := sample(data[base+2*bps:base+3*bps], f)
				sum = 0.5*(fl+fr) + invSqrt2*fc
				for c := 4; c < ch; c++ {
					sum += 0.5 * invSqrt2 * sample(data[base+c*bps:base+(c+1)*bps], f)
				}
			} else {
				for c := 0; c < ch; c++ {
					sum += sample(data[base+c*bps:base+(c+1)*bps], f)
				}
				sum /= float32(ch)
			}
			dst = append(dst, max(-1, min(1, sum)))
		}
	}
	return dst
}

// ToStereo decodes interleaved device samples to interleaved stereo (L, R),
// appending to dst. Stereo passes through untouched; mono duplicates to L and
// R; surround formats (5.1/7.1) fold down per ITU-R BS.775 skipping LFE so
// stereo speech on an 8-channel endpoint stays at unity gain.
func ToStereo(dst []float32, data []byte, f Format) []float32 {
	frames := len(data) / f.BlockAlign
	bps := f.Bits / 8
	ch := f.Channels
	for i := 0; i < frames; i++ {
		base := i * f.BlockAlign
		switch ch {
		case 1:
			v := sample(data[base:base+bps], f)
			dst = append(dst, v, v)
		case 2:
			l := sample(data[base:base+bps], f)
			r := sample(data[base+bps:base+2*bps], f)
			dst = append(dst, l, r)
		case 4:
			fl := sample(data[base:base+bps], f)
			fr := sample(data[base+bps:base+2*bps], f)
			bl := sample(data[base+2*bps:base+3*bps], f)
			br := sample(data[base+3*bps:base+4*bps], f)
			dst = append(dst, max(-1, min(1, fl+invSqrt2*bl)), max(-1, min(1, fr+invSqrt2*br)))
		default:
			if ch >= 6 {
				// 5.1 / 7.1: FL, FR, FC, LFE (skip), BL, BR, [SL, SR]
				fl := sample(data[base:base+bps], f)
				fr := sample(data[base+bps:base+2*bps], f)
				fc := sample(data[base+2*bps:base+3*bps], f)
				l := fl + invSqrt2*fc
				r := fr + invSqrt2*fc
				for c := 4; c+1 < ch; c += 2 {
					l += invSqrt2 * sample(data[base+c*bps:base+(c+1)*bps], f)
					r += invSqrt2 * sample(data[base+(c+1)*bps:base+(c+2)*bps], f)
				}
				dst = append(dst, max(-1, min(1, l)), max(-1, min(1, r)))
			} else {
				var sum float32
				for c := 0; c < ch; c++ {
					sum += sample(data[base+c*bps:base+(c+1)*bps], f)
				}
				v := sum / float32(ch)
				dst = append(dst, v, v)
			}
		}
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
