// Package m4a reads and writes AAC-LC audio in standard M4A (MP4) files using
// Windows Media Foundation (mfplat.dll, no cgo, no external binaries).
//
// While recording, Writer streams crash-safe ADTS AAC frames to <path>.aac
// (flushed periodically via Sync) so a crash loses at most seconds of audio.
// Close wraps the encoded frames into a standard fast-start .m4a file without
// re-encoding and removes the .aac file. Recover turns any leftover .aac file
// into .m4a after a crash. File reads both finalized .m4a and growing .aac
// files with random-access seeking.
package m4a

import (
	"fmt"
	"io"
)

// aacFrameSamples is the fixed sample count per AAC-LC frame.
const aacFrameSamples = 1024

// FrameEntry locates one raw AAC payload (excluding any ADTS header).
type FrameEntry struct {
	Offset int64
	Size   uint32
}

// Format describes an audio stream's parameters.
type Format struct {
	SampleRate int
	Channels   int
	Bitrate    int // target bits/s (0: default 64 kbps mono / 96 kbps stereo)
}

// sampleRates maps ISO-14496-3 sampling_frequency_index to Hz.
var sampleRates = [13]int{
	96000, 88200, 64000, 48000, 44100, 32000, 24000, 22050, 16000, 12000, 11025, 8000, 7350,
}

func sampleRateIndex(rate int) (int, bool) {
	for i, r := range sampleRates {
		if r == rate {
			return i, true
		}
	}
	return 0, false
}

// AudioSpecificConfig builds the 2-byte ISO-14496-3 AAC-LC AudioSpecificConfig.
func AudioSpecificConfig(rate, channels int) ([2]byte, error) {
	idx, ok := sampleRateIndex(rate)
	if !ok {
		return [2]byte{}, fmt.Errorf("m4a: unsupported sample rate %d", rate)
	}
	if channels < 1 || channels > 7 {
		return [2]byte{}, fmt.Errorf("m4a: unsupported channel count %d", channels)
	}
	// audioObjectType = 2 (AAC-LC, 5 bits), samplingFrequencyIndex (4 bits),
	// channelConfiguration (4 bits), frameLengthFlag/dependsOnCoreCoder/ext (3 bits = 0).
	v := uint16(2<<11) | uint16(idx<<7) | uint16(channels<<3)
	return [2]byte{byte(v >> 8), byte(v)}, nil
}

// heaacWaveInfo builds the 14-byte MF_MT_USER_DATA blob expected by Windows'
// CLSID_CMSAACDecMFT for raw AAC (payloadType=0): 12-byte HEAACWAVEINFO header
// followed by the 2-byte AudioSpecificConfig.
func heaacWaveInfo(asc []byte) []byte {
	out := make([]byte, 12+len(asc))
	// wPayloadType = 0 (raw AAC), wAudioProfileLevelIndication = 0x29 (AAC-LC L2)
	out[2] = 0x29
	copy(out[12:], asc)
	return out
}

// writeADTSHeader writes a 7-byte ADTS header (no CRC) for one raw AAC-LC frame.
func writeADTSHeader(dst []byte, rateIdx, channels, payloadLen int) {
	frameLen := payloadLen + 7
	// Syncword 0xFFF (12 bits), ID=0 (MPEG-4), layer=00, protection_absent=1
	dst[0] = 0xFF
	dst[1] = 0xF1
	// profile=1 (AAC-LC = objectType-1, 2 bits), freqIdx (4 bits), private=0 (1 bit), ch bit 2 (1 bit)
	dst[2] = byte((1 << 6) | ((rateIdx & 0xF) << 2) | ((channels >> 2) & 1))
	// ch bits 1..0 (2 bits), orig/home/copyright (4 bits = 0), frameLen bits 12..11 (2 bits)
	dst[3] = byte(((channels & 3) << 6) | ((frameLen >> 11) & 3))
	// frameLen bits 10..3
	dst[4] = byte((frameLen >> 3) & 0xFF)
	// frameLen bits 2..0 (3 bits), buffer fullness 0x7FF top 5 bits
	dst[5] = byte(((frameLen & 7) << 5) | 0x1F)
	// buffer fullness bottom 6 bits (0x3F), number_of_raw_data_blocks_in_frame = 0 (2 bits)
	dst[6] = 0xFC
}

// parseADTSHeader parses a 7-byte (or 9-byte with CRC) ADTS header.
func parseADTSHeader(h []byte) (rate, channels, hdrLen, frameLen int, ok bool) {
	if len(h) < 7 || h[0] != 0xFF || (h[1]&0xF6) != 0xF0 {
		return 0, 0, 0, 0, false
	}
	rateIdx := int((h[2] >> 2) & 0xF)
	if rateIdx >= len(sampleRates) {
		return 0, 0, 0, 0, false
	}
	channels = int(((h[2] & 1) << 2) | ((h[3] >> 6) & 3))
	frameLen = (int(h[3]&3) << 11) | (int(h[4]) << 3) | int((h[5]>>5)&7)
	hdrLen = 7
	if h[1]&1 == 0 {
		hdrLen = 9 // CRC present
	}
	if frameLen <= hdrLen || channels == 0 {
		return 0, 0, 0, 0, false
	}
	return sampleRates[rateIdx], channels, hdrLen, frameLen, true
}

// scanADTS scans complete ADTS frames in r starting at fromOffset up to size,
// appending entries to frames. Any trailing partial frame is ignored (safe for
// files currently being written or cut off by a crash).
func scanADTS(r io.ReaderAt, size, fromOffset int64, frames []FrameEntry) (rate, channels int, nextOffset int64, out []FrameEntry) {
	out = frames
	pos := fromOffset
	var hdr [7]byte
	for pos+7 <= size {
		n, err := r.ReadAt(hdr[:], pos)
		if n < 7 || (err != nil && err != io.EOF) {
			break
		}
		sr, ch, hdrLen, frameLen, ok := parseADTSHeader(hdr[:])
		if !ok {
			// Scan forward one byte if resyncing after corruption.
			pos++
			continue
		}
		if pos+int64(frameLen) > size {
			break // trailing partial frame
		}
		if rate == 0 {
			rate, channels = sr, ch
		}
		out = append(out, FrameEntry{Offset: pos + int64(hdrLen), Size: uint32(frameLen - hdrLen)})
		pos += int64(frameLen)
	}
	return rate, channels, pos, out
}
