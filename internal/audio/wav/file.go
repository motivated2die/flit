package wav

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

// File is a 16-bit PCM WAV file read in place, any frame at a time (for
// playback, without loading an hour of audio into memory).
type File struct {
	f      *os.File
	Format Format
	data   int64 // offset of the first sample
	frames int64
}

// Open opens a 16-bit PCM WAV file. A data chunk whose size runs past the
// end of the file (a header that wasn't updated) ends at the file's end.
func Open(path string) (*File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	w, err := openFile(f)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return w, nil
}

func openFile(f *os.File) (*File, error) {
	var riff [12]byte
	if _, err := io.ReadFull(f, riff[:]); err != nil || string(riff[0:4]) != "RIFF" || string(riff[8:12]) != "WAVE" {
		return nil, fmt.Errorf("%w: not RIFF/WAVE", ErrFormat)
	}
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	var format Format
	var tag uint16
	bits := 0
	pos := int64(12)
	for {
		var ch [8]byte
		if _, err := f.ReadAt(ch[:], pos); err != nil {
			return nil, fmt.Errorf("%w: no data chunk", ErrFormat)
		}
		id, size := string(ch[0:4]), int64(binary.LittleEndian.Uint32(ch[4:]))
		pos += 8
		switch id {
		case "fmt ":
			if size < 16 || size > 1024 {
				return nil, fmt.Errorf("%w: fmt chunk of %d bytes", ErrFormat, size)
			}
			b := make([]byte, size)
			if _, err := f.ReadAt(b, pos); err != nil {
				return nil, err
			}
			tag = binary.LittleEndian.Uint16(b[0:])
			format.Channels = int(binary.LittleEndian.Uint16(b[2:]))
			format.SampleRate = int(binary.LittleEndian.Uint32(b[4:]))
			bits = int(binary.LittleEndian.Uint16(b[14:]))
			if tag == formatExtensible && size >= 40 {
				tag = binary.LittleEndian.Uint16(b[24:])
			}
		case "data":
			if tag != formatPCM || bits != 16 || format.Channels <= 0 || format.SampleRate <= 0 {
				return nil, fmt.Errorf("%w: only 16-bit PCM (tag %d, %d bits)", ErrFormat, tag, bits)
			}
			if pos+size > st.Size() || size == 0 {
				size = st.Size() - pos
			}
			frameBytes := int64(2 * format.Channels)
			return &File{f: f, Format: format, data: pos, frames: size / frameBytes}, nil
		}
		pos += size + size%2
	}
}

// Frames is the file's length in frames.
func (w *File) Frames() int64 { return w.frames }

// ReadFrames reads mono samples (channels averaged) from frame on into dst
// and returns how many it read (fewer at the end of the file).
func (w *File) ReadFrames(dst []float32, frame int64) (int, error) {
	if frame >= w.frames || frame < 0 {
		return 0, io.EOF
	}
	n := min(int64(len(dst)), w.frames-frame)
	ch := w.Format.Channels
	buf := make([]byte, n*int64(2*ch))
	got, err := w.f.ReadAt(buf, w.data+frame*int64(2*ch))
	n = int64(got / (2 * ch))
	for i := int64(0); i < n; i++ {
		var sum float32
		for c := 0; c < ch; c++ {
			sum += float32(int16(binary.LittleEndian.Uint16(buf[(i*int64(ch)+int64(c))*2:]))) / 32768
		}
		dst[i] = sum / float32(ch)
	}
	if err == io.EOF && n > 0 {
		err = nil
	}
	return int(n), err
}

// Close closes the file.
func (w *File) Close() error { return w.f.Close() }
