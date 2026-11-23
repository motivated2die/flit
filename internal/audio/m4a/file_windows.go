package m4a

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"sync"
	"unsafe"
)

// File reads an M4A or growing ADTS AAC file with sample-accurate random-access
// seeking, decoding on demand via Windows' CLSID_CMSAACDecMFT.
type File struct {
	mu     sync.Mutex
	f      *os.File
	Format Format
	meta   Meta
	isADTS bool
	adtsAt int64 // byte offset scanned up to so far (for growing .aac files)

	dec     uintptr
	outInfo mftOutputStreamInfo
	// Sequential decode state: decNext is the logical output frame index
	// (0 = after trimming meta.Priming) of the first sample in buf.
	decValid bool
	decFrame int   // next AAC frame index to feed to dec
	decPos   int64 // logical frame index of buf[0]
	buf      []float32
	rawBuf   []byte
}

// Open opens a finalized .m4a file or a (possibly growing) ADTS .aac file.
func Open(path string) (*File, error) {
	if err := initMF(); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	var head [12]byte
	n, _ := f.ReadAt(head[:], 0)
	if n < 7 {
		f.Close()
		return nil, fmt.Errorf("%s: %w: file too short", path, ErrFormat)
	}

	var meta Meta
	var isADTS bool
	var adtsAt int64
	if n >= 8 && string(head[4:8]) == "ftyp" {
		meta, err = ParseM4A(f, st.Size())
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	} else if head[0] == 0xFF && (head[1]&0xF6) == 0xF0 {
		isADTS = true
		sr, ch, next, frames := scanADTS(f, st.Size(), 0, nil)
		if sr == 0 || ch == 0 {
			f.Close()
			return nil, fmt.Errorf("%s: %w: no valid ADTS frames", path, ErrFormat)
		}
		asc, err := AudioSpecificConfig(sr, ch)
		if err != nil {
			f.Close()
			return nil, err
		}
		adtsAt = next
		meta = Meta{
			SampleRate:   sr,
			Channels:     ch,
			Priming:      defaultPriming,
			TotalSamples: max(0, int64(len(frames))*aacFrameSamples-int64(defaultPriming)),
			ASC:          asc[:],
			Frames:       frames,
		}
	} else {
		f.Close()
		return nil, fmt.Errorf("%s: %w", path, ErrFormat)
	}

	dec, outInfo, err := newDecoder(meta)
	if err != nil {
		f.Close()
		return nil, err
	}
	return &File{
		f:       f,
		Format:  Format{SampleRate: meta.SampleRate, Channels: meta.Channels},
		meta:    meta,
		isADTS:  isADTS,
		adtsAt:  adtsAt,
		dec:     dec,
		outInfo: outInfo,
	}, nil
}

func newDecoder(meta Meta) (uintptr, mftOutputStreamInfo, error) {
	var dec uintptr
	hr, _, _ := procCoCreateInst.Call(uintptr(unsafe.Pointer(&clsidAACDec)), 0, clsctxInprocServer, uintptr(unsafe.Pointer(&iidIMFTrans)), uintptr(unsafe.Pointer(&dec)))
	if int32(hr) < 0 || dec == 0 {
		return 0, mftOutputStreamInfo{}, fmt.Errorf("m4a: AAC decoder unavailable (%#x)", uint32(hr))
	}
	inMT, err := newMT()
	if err != nil {
		release(dec)
		return 0, mftOutputStreamInfo{}, err
	}
	setGUID(inMT, &mtMajorType, &mediaAudio)
	setGUID(inMT, &mtSubtype, &subAAC)
	setU32(inMT, &mtAudioCh, uint32(meta.Channels))
	setU32(inMT, &mtAudioRate, uint32(meta.SampleRate))
	setU32(inMT, &mtAACPayload, 0)
	setBlob(inMT, &mtUserData, heaacWaveInfo(meta.ASC))
	h := call(dec, 15, 0, inMT, 0)
	release(inMT)
	if h != 0 {
		release(dec)
		return 0, mftOutputStreamInfo{}, fmt.Errorf("m4a: decoder SetInputType failed (%#x)", h)
	}
	var outMT uintptr
	if h := call(dec, 14, 0, 0, uintptr(unsafe.Pointer(&outMT))); h != 0 {
		release(dec)
		return 0, mftOutputStreamInfo{}, fmt.Errorf("m4a: decoder GetOutputAvailableType failed (%#x)", h)
	}
	h = call(dec, 16, 0, outMT, 0)
	release(outMT)
	if h != 0 {
		release(dec)
		return 0, mftOutputStreamInfo{}, fmt.Errorf("m4a: decoder SetOutputType failed (%#x)", h)
	}
	var outInfo mftOutputStreamInfo
	call(dec, 7, 0, uintptr(unsafe.Pointer(&outInfo)))
	call(dec, 23, mftMsgBeginStream, 0)
	return dec, outInfo, nil
}

// refreshADTS scans any newly appended frames when reading a growing .aac file.
func (w *File) refreshADTS() {
	if !w.isADTS {
		return
	}
	st, err := w.f.Stat()
	if err != nil || st.Size() <= w.adtsAt {
		return
	}
	_, _, next, frames := scanADTS(w.f, st.Size(), w.adtsAt, w.meta.Frames)
	w.adtsAt = next
	w.meta.Frames = frames
	w.meta.TotalSamples = max(0, int64(len(frames))*aacFrameSamples-int64(w.meta.Priming))
}

// Frames returns the file's length in frames (samples per channel).
func (w *File) Frames() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.refreshADTS()
	return w.meta.TotalSamples
}

// ReadInterleaved reads up to len(dst)/Channels frames starting at frame into
// dst (interleaved float32), returning the number of frames read.
func (w *File) ReadInterleaved(dst []float32, frame int64) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.refreshADTS()
	ch := w.Format.Channels
	want := min(int64(len(dst)/ch), w.meta.TotalSamples-frame)
	if frame < 0 || want <= 0 {
		return 0, io.EOF
	}
	if err := w.seekTo(frame); err != nil {
		return 0, err
	}
	written := int64(0)
	for written < want {
		avail := int64(len(w.buf) / ch)
		if avail == 0 {
			if err := w.decodeNextFrame(); err != nil {
				if written > 0 {
					return int(written), nil
				}
				return 0, err
			}
			continue
		}
		take := min(want-written, avail)
		copy(dst[written*int64(ch):(written+take)*int64(ch)], w.buf[:take*int64(ch)])
		w.buf = append(w.buf[:0], w.buf[take*int64(ch):]...)
		w.decPos += take
		written += take
	}
	return int(written), nil
}

// ReadFrames reads mono samples (channels averaged) starting at frame into dst,
// returning how many frames it read. Implements pipeline.Source.
func (w *File) ReadFrames(dst []float32, frame int64) (int, error) {
	ch := w.Format.Channels
	if ch == 1 {
		return w.ReadInterleaved(dst, frame)
	}
	tmp := make([]float32, len(dst)*ch)
	n, err := w.ReadInterleaved(tmp, frame)
	scale := 1 / float32(ch)
	for i := range n {
		var sum float32
		for c := range ch {
			sum += tmp[i*ch+c]
		}
		dst[i] = sum * scale
	}
	return n, err
}

func (w *File) seekTo(target int64) error {
	ch := w.Format.Channels
	// Fast path: target already sits inside or right at the buffered sequential position.
	if w.decValid {
		bufFrames := int64(len(w.buf) / ch)
		if target >= w.decPos && target <= w.decPos+bufFrames {
			skip := target - w.decPos
			if skip > 0 {
				w.buf = append(w.buf[:0], w.buf[skip*int64(ch):]...)
				w.decPos = target
			}
			return nil
		}
		// Small forward skip (<= 4 AAC frames): just decode forward instead of flushing.
		if target > w.decPos+bufFrames && target-(w.decPos+bufFrames) <= 4*aacFrameSamples {
			w.buf = w.buf[:0]
			w.decPos += bufFrames
			for w.decPos < target {
				if err := w.decodeNextFrame(); err != nil {
					return err
				}
				avail := int64(len(w.buf) / ch)
				skip := min(avail, target-w.decPos)
				w.buf = append(w.buf[:0], w.buf[skip*int64(ch):]...)
				w.decPos += skip
			}
			return nil
		}
	}

	// Random seek: flush decoder and back up 2 AAC frames for MDCT overlap-add priming.
	call(w.dec, 23, mftMsgCommandFlush, 0)
	rawTarget := target + int64(w.meta.Priming)
	startAAC := max(0, int(rawTarget/aacFrameSamples)-2)
	w.decFrame = startAAC
	w.decPos = int64(startAAC)*aacFrameSamples - int64(w.meta.Priming)
	w.buf = w.buf[:0]
	w.decValid = true

	// Decode and discard until w.decPos reaches target.
	for w.decPos < target {
		if err := w.decodeNextFrame(); err != nil {
			return err
		}
		avail := int64(len(w.buf) / ch)
		if avail == 0 {
			continue
		}
		skip := min(avail, target-w.decPos)
		w.buf = append(w.buf[:0], w.buf[skip*int64(ch):]...)
		w.decPos += skip
	}
	return nil
}

func (w *File) decodeNextFrame() error {
	if w.decFrame >= len(w.meta.Frames) {
		return io.EOF
	}
	fr := w.meta.Frames[w.decFrame]
	w.decFrame++
	if int(fr.Size) > cap(w.rawBuf) {
		w.rawBuf = make([]byte, fr.Size)
	}
	raw := w.rawBuf[:fr.Size]
	if _, err := w.f.ReadAt(raw, fr.Offset); err != nil {
		return err
	}
	ts := int64(w.decFrame-1) * aacFrameSamples * 10_000_000 / int64(w.Format.SampleRate)
	dur := int64(aacFrameSamples) * 10_000_000 / int64(w.Format.SampleRate)
	s, err := makeSample(raw, ts, dur)
	if err != nil {
		return err
	}
	h := call(w.dec, 24, 0, s, 0)
	release(s)
	if h != 0 {
		return fmt.Errorf("m4a: decoder ProcessInput failed (%#x)", h)
	}
	for {
		var osamp, obuf uintptr
		if w.outInfo.Flags&mftOutProvidesSamp == 0 {
			if hr, _, _ := procMFCreateSample.Call(uintptr(unsafe.Pointer(&osamp))); int32(hr) < 0 {
				return fmt.Errorf("m4a: MFCreateSample: %#x", uint32(hr))
			}
			sz := max(uint32(32768), w.outInfo.Size)
			if hr, _, _ := procMFCreateMemBuf.Call(uintptr(sz), uintptr(unsafe.Pointer(&obuf))); int32(hr) < 0 {
				release(osamp)
				return fmt.Errorf("m4a: MFCreateMemoryBuffer: %#x", uint32(hr))
			}
			call(osamp, 42, obuf)
			release(obuf)
		}
		ob := mftOutputBuf{Sample: osamp}
		var status uint32
		h := call(w.dec, 25, 0, 1, uintptr(unsafe.Pointer(&ob)), uintptr(unsafe.Pointer(&status)))
		if h != 0 {
			release(osamp)
			break
		}
		pcmBytes := readSampleBytes(ob.Sample)
		release(ob.Sample)
		for i := 0; i+4 <= len(pcmBytes); i += 4 {
			w.buf = append(w.buf, math.Float32frombits(binary.LittleEndian.Uint32(pcmBytes[i:])))
		}
	}
	return nil
}

// Close releases the decoder and closes the underlying file.
func (w *File) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.dec != 0 {
		release(w.dec)
		w.dec = 0
	}
	if w.f != nil {
		err := w.f.Close()
		w.f = nil
		return err
	}
	return nil
}

// ReadFile decodes an entire M4A/AAC file into mono float32 samples at its native rate.
func ReadFile(path string) (Format, []float32, error) {
	f, err := Open(path)
	if err != nil {
		return Format{}, nil, err
	}
	defer f.Close()
	n := f.Frames()
	out := make([]float32, n)
	got, err := f.ReadFrames(out, 0)
	if err != nil && err != io.EOF {
		return Format{}, nil, err
	}
	return f.Format, out[:got], nil
}
