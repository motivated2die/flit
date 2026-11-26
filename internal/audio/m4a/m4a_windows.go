package m4a

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// defaultPriming is the net sample delay of Windows' CLSID_AACMFTEncoder +
// CLSID_CMSAACDecMFT round-trip (measured 0 samples in TestPrimingAlignment:
// Windows' MFT trims its own MDCT priming internally).
const defaultPriming = 0

// ScratchSuffix is appended to an M4A path while recording crash-safe ADTS
// frames before Close remuxes them into the final .m4a file.
const ScratchSuffix = ".aac"

type guid struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

var (
	ole32              = windows.NewLazySystemDLL("ole32.dll")
	procCoInitEx       = ole32.NewProc("CoInitializeEx")
	procCoCreateInst   = ole32.NewProc("CoCreateInstance")
	mfplat             = windows.NewLazySystemDLL("mfplat.dll")
	procMFStartup      = mfplat.NewProc("MFStartup")
	procMFCreateMT     = mfplat.NewProc("MFCreateMediaType")
	procMFCreateSample = mfplat.NewProc("MFCreateSample")
	procMFCreateMemBuf = mfplat.NewProc("MFCreateMemoryBuffer")

	clsidAACEnc  = guid{0x93af0c51, 0x2275, 0x45d2, [8]byte{0xa3, 0x5b, 0xf2, 0xba, 0x21, 0xca, 0xed, 0x00}}
	clsidAACDec  = guid{0x32d186a7, 0x218f, 0x4c75, [8]byte{0x88, 0x76, 0xdd, 0x77, 0x27, 0x3a, 0x89, 0x99}}
	iidIMFTrans  = guid{0xbf94c121, 0x5b05, 0x4e6f, [8]byte{0x80, 0x00, 0xba, 0x59, 0x89, 0x61, 0x41, 0x4d}}
	mtMajorType  = guid{0x48eba18e, 0xf8c9, 0x4687, [8]byte{0xbf, 0x11, 0x0a, 0x74, 0xc9, 0xf9, 0x6a, 0x8f}}
	mtSubtype    = guid{0xf7e34c9a, 0x42e8, 0x4714, [8]byte{0xb7, 0x4b, 0xcb, 0x29, 0xd7, 0x2c, 0x35, 0xe5}}
	mtAudioCh    = guid{0x37e48bf5, 0x645e, 0x4c5b, [8]byte{0x89, 0xde, 0xad, 0xa9, 0xe2, 0x9b, 0x69, 0x6a}}
	mtAudioRate  = guid{0x5faeeae7, 0x0290, 0x4c31, [8]byte{0x9e, 0x8a, 0xc5, 0x34, 0xf6, 0x8d, 0x9d, 0xba}}
	mtAudioBits  = guid{0xf2deb57f, 0x40fa, 0x4764, [8]byte{0xaa, 0x33, 0xed, 0x4f, 0x2d, 0x1f, 0xf6, 0x69}}
	mtAudioBps   = guid{0x1aab75c8, 0xcfef, 0x451c, [8]byte{0xab, 0x95, 0xac, 0x03, 0x4b, 0x8e, 0x17, 0x31}}
	mtAudioAlign = guid{0x322de230, 0x9eeb, 0x43bd, [8]byte{0xab, 0x7a, 0xff, 0x41, 0x22, 0x51, 0x54, 0x1d}}
	mtAACPayload = guid{0xbfbabe79, 0x7434, 0x4d1c, [8]byte{0x94, 0xf0, 0x72, 0xa3, 0xb9, 0xe1, 0x71, 0x88}}
	mtUserData   = guid{0xb6bc765f, 0x4c3b, 0x40a4, [8]byte{0xbd, 0x51, 0x25, 0x35, 0xb6, 0x6f, 0xe0, 0x9d}}
	mediaAudio   = guid{0x73647561, 0x0000, 0x0010, [8]byte{0x80, 0x00, 0x00, 0xaa, 0x00, 0x38, 0x9b, 0x71}}
	subPCM       = guid{0x00000001, 0x0000, 0x0010, [8]byte{0x80, 0x00, 0x00, 0xaa, 0x00, 0x38, 0x9b, 0x71}}
	subAAC       = guid{0x00001610, 0x0000, 0x0010, [8]byte{0x80, 0x00, 0x00, 0xaa, 0x00, 0x38, 0x9b, 0x71}}
)

const (
	clsctxInprocServer = 1
	mfVersion          = 0x00020070 // MF_VERSION on Win7+
	mftMsgCommandFlush = 0x00000000
	mftMsgCommandDrain = 0x00000001
	mftMsgBeginStream  = 0x10000001
	mftOutProvidesSamp = 0x00000100
)

type mftOutputBuf struct {
	StreamID uint32
	_        uint32
	Sample   uintptr
	Status   uint32
	_        uint32
	Events   uintptr
}

type mftOutputStreamInfo struct {
	Flags     uint32
	Size      uint32
	Alignment uint32
}

var (
	mfOnce  sync.Once
	mfErr   error
	availMu sync.Once
	availOK bool
)

// initMF starts a permanent MTA anchor thread and calls MFStartup once.
// Keeping one thread initialized in the MTA lets any Go goroutine create and
// call free-threaded Media Foundation MFTs without apartment mismatches.
func initMF() error {
	mfOnce.Do(func() {
		if err := mfplat.Load(); err != nil {
			mfErr = fmt.Errorf("mfplat.dll unavailable: %w", err)
			return
		}
		ready := make(chan error, 1)
		go func() {
			runtime.LockOSThread()
			// COINIT_MULTITHREADED = 0; S_OK (0) or S_FALSE (1) are success.
			hr, _, _ := procCoInitEx.Call(0, 0)
			if int32(hr) < 0 && uint32(hr) != 0x80010106 { // RPC_E_CHANGED_MODE
				ready <- fmt.Errorf("CoInitializeEx: %#x", uint32(hr))
				return
			}
			hr, _, _ = procMFStartup.Call(mfVersion, 0)
			if int32(hr) < 0 {
				ready <- fmt.Errorf("MFStartup: %#x", uint32(hr))
				return
			}
			ready <- nil
			select {} // hold MTA + MF alive for process lifetime
		}()
		mfErr = <-ready
	})
	return mfErr
}

// Available reports whether Windows Media Foundation's AAC encoder and decoder
// are available on this system (false on Windows N without Media Feature Pack).
func Available() bool {
	availMu.Do(func() {
		if initMF() != nil {
			return
		}
		var enc, dec uintptr
		hr1, _, _ := procCoCreateInst.Call(uintptr(unsafe.Pointer(&clsidAACEnc)), 0, clsctxInprocServer, uintptr(unsafe.Pointer(&iidIMFTrans)), uintptr(unsafe.Pointer(&enc)))
		hr2, _, _ := procCoCreateInst.Call(uintptr(unsafe.Pointer(&clsidAACDec)), 0, clsctxInprocServer, uintptr(unsafe.Pointer(&iidIMFTrans)), uintptr(unsafe.Pointer(&dec)))
		if enc != 0 {
			release(enc)
		}
		if dec != 0 {
			release(dec)
		}
		availOK = int32(hr1) >= 0 && enc != 0 && int32(hr2) >= 0 && dec != 0
	})
	return availOK
}

// cptr converts a COM pointer address returned by Windows into unsafe.Pointer.
func cptr(p uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&p)) }

func vtbl(obj uintptr, slot int) uintptr {
	vt := *(*uintptr)(cptr(obj))
	return *(*uintptr)(cptr(vt + uintptr(slot)*unsafe.Sizeof(uintptr(0))))
}

// call invokes COM vtable method slot on obj. uintptrescapes keeps addresses
// of Go values passed as uintptr arguments escaped and pinned for the call;
// without it, stack growth before SyscallN can move stack-allocated out-params
// while args still holds the stale address.
//
//go:uintptrescapes
func call(obj uintptr, slot int, args ...uintptr) uint32 {
	var buf [8]uintptr
	all := buf[:0]
	if 1+len(args) > len(buf) {
		all = make([]uintptr, 0, 1+len(args))
	}
	all = append(all, obj)
	all = append(all, args...)
	r, _, _ := syscall.SyscallN(vtbl(obj, slot), all...)
	return uint32(r)
}

func release(obj uintptr) {
	if obj != 0 {
		call(obj, 2)
	}
}

func newMT() (uintptr, error) {
	var mt uintptr
	hr, _, _ := procMFCreateMT.Call(uintptr(unsafe.Pointer(&mt)))
	if int32(hr) < 0 {
		return 0, fmt.Errorf("MFCreateMediaType: %#x", uint32(hr))
	}
	return mt, nil
}

func setGUID(mt uintptr, k, v *guid) {
	call(mt, 24, uintptr(unsafe.Pointer(k)), uintptr(unsafe.Pointer(v)))
}

func setU32(mt uintptr, k *guid, v uint32) {
	call(mt, 21, uintptr(unsafe.Pointer(k)), uintptr(v))
}

func setBlob(mt uintptr, k *guid, b []byte) {
	if len(b) == 0 {
		return
	}
	call(mt, 26, uintptr(unsafe.Pointer(k)), uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)))
}

func getBlob(mt uintptr, k *guid) []byte {
	var sz uint32
	if call(mt, 14, uintptr(unsafe.Pointer(k)), uintptr(unsafe.Pointer(&sz))) != 0 || sz == 0 {
		return nil
	}
	b := make([]byte, sz)
	call(mt, 15, uintptr(unsafe.Pointer(k)), uintptr(unsafe.Pointer(&b[0])), uintptr(sz), uintptr(unsafe.Pointer(&sz)))
	return b[:sz]
}

func makeSample(data []byte, ts, dur int64) (uintptr, error) {
	var s, buf uintptr
	if hr, _, _ := procMFCreateSample.Call(uintptr(unsafe.Pointer(&s))); int32(hr) < 0 {
		return 0, fmt.Errorf("MFCreateSample: %#x", uint32(hr))
	}
	if hr, _, _ := procMFCreateMemBuf.Call(uintptr(len(data)), uintptr(unsafe.Pointer(&buf))); int32(hr) < 0 {
		release(s)
		return 0, fmt.Errorf("MFCreateMemoryBuffer: %#x", uint32(hr))
	}
	var p uintptr
	if h := call(buf, 3, uintptr(unsafe.Pointer(&p)), 0, 0); h != 0 {
		release(buf)
		release(s)
		return 0, fmt.Errorf("IMFMediaBuffer.Lock: %#x", h)
	}
	if len(data) > 0 {
		if p == 0 {
			call(buf, 4)
			release(buf)
			release(s)
			return 0, errors.New("IMFMediaBuffer.Lock returned nil pointer")
		}
		copy(unsafe.Slice((*byte)(cptr(p)), len(data)), data)
	}
	call(buf, 4)
	call(buf, 6, uintptr(len(data)))
	call(s, 42, buf)
	call(s, 36, uintptr(ts))
	call(s, 38, uintptr(dur))
	release(buf)
	return s, nil
}

func readSampleBytes(s uintptr) []byte {
	var buf uintptr
	if call(s, 41, uintptr(unsafe.Pointer(&buf))) != 0 {
		return nil
	}
	defer release(buf)
	var p uintptr
	var cur uint32
	if call(buf, 3, uintptr(unsafe.Pointer(&p)), 0, uintptr(unsafe.Pointer(&cur))) != 0 {
		return nil
	}
	if cur == 0 || p == 0 {
		call(buf, 4)
		return nil
	}
	out := make([]byte, cur)
	copy(out, unsafe.Slice((*byte)(cptr(p)), cur))
	call(buf, 4)
	return out
}

// Writer encodes interleaved float32 PCM at 48 kHz (or 44.1 kHz) into
// crash-safe ADTS AAC frames while recording, then remuxes them losslessly into
// a standard seekable .m4a file on Close.
type Writer struct {
	dstPath string
	aacPath string
	f       *os.File
	bw      *bufio.Writer
	format  Format
	rateIdx int
	asc     []byte

	enc      uintptr
	outInfo  mftOutputStreamInfo
	pending  []int16 // buffered interleaved PCM waiting for a 1024-frame block
	pcmBytes []byte
	inFrames int64 // true input frames written
	aacOff   int64 // bytes written to aacPath so far
	frames   []FrameEntry
	closed   bool
}

// Create creates a crash-safe AAC writer that finalizes into path (.m4a).
func Create(path string, format Format) (*Writer, error) {
	if err := initMF(); err != nil {
		return nil, err
	}
	if format.SampleRate != 48000 && format.SampleRate != 44100 {
		return nil, fmt.Errorf("m4a: sample rate %d Hz unsupported (use 48000 or 44100)", format.SampleRate)
	}
	if format.Channels != 1 && format.Channels != 2 {
		return nil, fmt.Errorf("m4a: channels %d unsupported (use 1 or 2)", format.Channels)
	}
	rateIdx, _ := sampleRateIndex(format.SampleRate)
	if format.Bitrate <= 0 {
		if format.Channels == 1 {
			format.Bitrate = 64000
		} else {
			format.Bitrate = 96000
		}
	}

	var enc uintptr
	hr, _, _ := procCoCreateInst.Call(uintptr(unsafe.Pointer(&clsidAACEnc)), 0, clsctxInprocServer, uintptr(unsafe.Pointer(&iidIMFTrans)), uintptr(unsafe.Pointer(&enc)))
	if int32(hr) < 0 || enc == 0 {
		return nil, fmt.Errorf("m4a: AAC encoder unavailable (%#x)", uint32(hr))
	}

	// Negotiate output bitrate: try requested bitrate first, falling back to
	// Windows' baseline guaranteed bitrates (96, 128 kbps) on older Win10 builds.
	candidates := []int{format.Bitrate}
	for _, b := range []int{96000, 128000} {
		if b != format.Bitrate {
			candidates = append(candidates, b)
		}
	}
	var setOutErr uint32
	for _, br := range candidates {
		outMT, err := newMT()
		if err != nil {
			release(enc)
			return nil, err
		}
		setGUID(outMT, &mtMajorType, &mediaAudio)
		setGUID(outMT, &mtSubtype, &subAAC)
		setU32(outMT, &mtAudioBits, 16)
		setU32(outMT, &mtAudioCh, uint32(format.Channels))
		setU32(outMT, &mtAudioRate, uint32(format.SampleRate))
		setU32(outMT, &mtAudioBps, uint32(br/8))
		setU32(outMT, &mtAACPayload, 0) // raw AAC frames
		h := call(enc, 16, 0, outMT, 0)
		release(outMT)
		if h == 0 {
			format.Bitrate = br
			setOutErr = 0
			break
		}
		setOutErr = h
	}
	if setOutErr != 0 {
		release(enc)
		return nil, fmt.Errorf("m4a: AAC SetOutputType failed (%#x)", setOutErr)
	}

	inMT, err := newMT()
	if err != nil {
		release(enc)
		return nil, err
	}
	setGUID(inMT, &mtMajorType, &mediaAudio)
	setGUID(inMT, &mtSubtype, &subPCM)
	setU32(inMT, &mtAudioBits, 16)
	setU32(inMT, &mtAudioCh, uint32(format.Channels))
	setU32(inMT, &mtAudioRate, uint32(format.SampleRate))
	setU32(inMT, &mtAudioAlign, uint32(format.Channels*2))
	setU32(inMT, &mtAudioBps, uint32(format.SampleRate*format.Channels*2))
	h := call(enc, 15, 0, inMT, 0)
	release(inMT)
	if h != 0 {
		release(enc)
		return nil, fmt.Errorf("m4a: AAC SetInputType failed (%#x)", h)
	}

	var curOut uintptr
	var asc []byte
	if call(enc, 18, 0, uintptr(unsafe.Pointer(&curOut))) == 0 && curOut != 0 {
		if ud := getBlob(curOut, &mtUserData); len(ud) >= 14 {
			asc = append([]byte(nil), ud[12:]...)
		}
		release(curOut)
	}
	if len(asc) < 2 {
		a, _ := AudioSpecificConfig(format.SampleRate, format.Channels)
		asc = a[:]
	}

	var outInfo mftOutputStreamInfo
	call(enc, 7, 0, uintptr(unsafe.Pointer(&outInfo)))
	call(enc, 23, mftMsgBeginStream, 0)

	aacPath := path + ScratchSuffix
	f, err := os.Create(aacPath)
	if err != nil {
		release(enc)
		return nil, err
	}
	return &Writer{
		dstPath:  path,
		aacPath:  aacPath,
		f:        f,
		bw:       bufio.NewWriterSize(f, 64<<10),
		format:   format,
		rateIdx:  rateIdx,
		asc:      asc,
		enc:      enc,
		outInfo:  outInfo,
		pcmBytes: make([]byte, aacFrameSamples*format.Channels*2),
	}, nil
}

// Write appends interleaved float32 samples in [-1, 1].
func (w *Writer) Write(samples []float32) error {
	if w.closed {
		return errors.New("m4a: writer closed")
	}
	ch := w.format.Channels
	nFrames := len(samples) / ch
	if nFrames == 0 {
		return nil
	}
	samples = samples[:nFrames*ch]
	for _, s := range samples {
		v := math.Round(float64(s) * 32767)
		w.pending = append(w.pending, int16(max(-32768, min(32767, v))))
	}
	w.inFrames += int64(nFrames)

	block := aacFrameSamples * ch
	for len(w.pending) >= block {
		if err := w.encodeBlock(w.pending[:block]); err != nil {
			return err
		}
		w.pending = append(w.pending[:0], w.pending[block:]...)
	}
	return nil
}

func (w *Writer) encodeBlock(pcm []int16) error {
	for i, v := range pcm {
		binary.LittleEndian.PutUint16(w.pcmBytes[2*i:], uint16(v))
	}
	nFrames := len(pcm) / w.format.Channels
	ts := (w.inFrames - int64(len(w.pending)/w.format.Channels)) * 10_000_000 / int64(w.format.SampleRate)
	dur := int64(nFrames) * 10_000_000 / int64(w.format.SampleRate)
	s, err := makeSample(w.pcmBytes[:len(pcm)*2], ts, dur)
	if err != nil {
		return err
	}
	h := call(w.enc, 24, 0, s, 0)
	release(s)
	if h != 0 {
		return fmt.Errorf("m4a: ProcessInput failed (%#x)", h)
	}
	return w.drain()
}

func (w *Writer) drain() error {
	var hdr [7]byte
	for {
		var s, b uintptr
		if w.outInfo.Flags&mftOutProvidesSamp == 0 {
			if hr, _, _ := procMFCreateSample.Call(uintptr(unsafe.Pointer(&s))); int32(hr) < 0 {
				return fmt.Errorf("m4a: MFCreateSample: %#x", uint32(hr))
			}
			sz := max(uint32(8192), w.outInfo.Size)
			if hr, _, _ := procMFCreateMemBuf.Call(uintptr(sz), uintptr(unsafe.Pointer(&b))); int32(hr) < 0 {
				release(s)
				return fmt.Errorf("m4a: MFCreateMemoryBuffer: %#x", uint32(hr))
			}
			call(s, 42, b)
			release(b)
		}
		ob := mftOutputBuf{Sample: s}
		var status uint32
		h := call(w.enc, 25, 0, 1, uintptr(unsafe.Pointer(&ob)), uintptr(unsafe.Pointer(&status)))
		if h != 0 {
			release(s)
			return nil // MF_E_TRANSFORM_NEED_MORE_INPUT
		}
		raw := readSampleBytes(ob.Sample)
		release(ob.Sample)
		if len(raw) == 0 {
			continue
		}
		writeADTSHeader(hdr[:], w.rateIdx, w.format.Channels, len(raw))
		if _, err := w.bw.Write(hdr[:]); err != nil {
			return err
		}
		if _, err := w.bw.Write(raw); err != nil {
			return err
		}
		w.frames = append(w.frames, FrameEntry{Offset: w.aacOff + 7, Size: uint32(len(raw))})
		w.aacOff += int64(7 + len(raw))
	}
}

// Frames returns the number of audio frames (samples per channel) written.
func (w *Writer) Frames() int64 { return w.inFrames }

// Sync flushes buffered ADTS frames to disk so a crash loses at most seconds.
func (w *Writer) Sync() error {
	if w.closed {
		return nil
	}
	if err := w.bw.Flush(); err != nil {
		return err
	}
	return w.f.Sync()
}

// Close flushes remaining samples, drains the encoder, remuxes the crash-safe
// ADTS stream into the final .m4a file, and removes the temporary .aac file.
func (w *Writer) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	defer func() {
		release(w.enc)
		w.enc = 0
	}()

	var firstErr error
	// Pad any partial trailing block + enough silence to flush the encoder's
	// lookahead (~defaultPriming samples) cleanly.
	ch := w.format.Channels
	block := aacFrameSamples * ch
	if len(w.pending) > 0 {
		for len(w.pending) < block {
			w.pending = append(w.pending, 0)
		}
		if err := w.encodeBlock(w.pending[:block]); err != nil {
			firstErr = err
		}
		w.pending = w.pending[:0]
	}
	// Feed two zero blocks so the final audio samples clear the encoder delay
	// before MFT_MESSAGE_COMMAND_DRAIN.
	zeros := make([]int16, block)
	for range 2 {
		if err := w.encodeBlock(zeros); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	call(w.enc, 23, mftMsgCommandDrain, 0)
	if err := w.drain(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := w.bw.Flush(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := w.f.Sync(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := w.f.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	if firstErr != nil {
		return firstErr
	}
	// Remux the crash-safe ADTS file into the final fast-start .m4a file.
	src, err := os.Open(w.aacPath)
	if err != nil {
		return err
	}
	meta := Meta{
		SampleRate:   w.format.SampleRate,
		Channels:     w.format.Channels,
		Bitrate:      w.format.Bitrate,
		Priming:      defaultPriming,
		TotalSamples: w.inFrames,
		ASC:          w.asc,
		Frames:       w.frames,
	}
	err = WriteM4A(w.dstPath, meta, src)
	src.Close()
	if err != nil {
		return err
	}
	_ = os.Remove(w.aacPath)
	return nil
}
