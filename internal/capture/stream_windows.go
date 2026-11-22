package capture

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/moutend/go-wca/pkg/wca"
	"golang.org/x/sys/windows"

	"github.com/motivated2die/flit/internal/coreaudio"
)

// Kind is what a stream records.
type Kind int

const (
	Mic      Kind = iota // a capture device
	Loopback             // everything a render device plays
	Process              // what one process tree plays (Windows 11 / build 20348+)
)

// Source selects a stream's audio.
type Source struct {
	Kind       Kind
	DeviceID   string // Mic, Loopback: "" = Windows' default device
	DeviceName string // found again by name when the ID changed
	PID        uint32 // Process
}

func (s Source) String() string {
	switch s.Kind {
	case Loopback:
		return "loopback"
	case Process:
		return fmt.Sprintf("process %d", s.PID)
	}
	return "mic"
}

// Event reports a stream's state changes.
type Event struct {
	Source Source
	Device string // friendly name, when known
	Err    error  // nil: (re)started
}

// Stream records one Source into a Track until Stop.
type Stream struct {
	src   Source
	track Sink
	al    *Aligner
	onEv  func(Event)
	stop  chan struct{}
	done  chan struct{}
	once  sync.Once
	endAt atomic.Int64 // StopAt's end (QPC, 100 ns); 0 = not stopping
}

const (
	bufferDur    = 200 * time.Millisecond
	idlePad      = 300 * time.Millisecond // no packet this long: pad silence up to now
	reopenPause  = 500 * time.Millisecond
	errInvalided = 0x88890004 // AUDCLNT_E_DEVICE_INVALIDATED
)

// Start opens the source and records into track, with times relative to
// origin (a QPC time in 100 ns units, shared by the session's streams).
// It returns once the device is open, or with the error that kept it from
// opening; after that, failures are retried and reported through onEvent.
func Start(src Source, track Sink, origin int64, onEvent func(Event)) (*Stream, error) {
	return StartOn(src, track, &Aligner{Origin: origin}, onEvent)
}

// StartOn records src into track on an existing time line: a stream that
// replaces another (a switched microphone) continues where it stopped,
// silence filling the moment between. Only one stream may use al at a
// time.
func StartOn(src Source, track Sink, al *Aligner, onEvent func(Event)) (*Stream, error) {
	s := &Stream{src: src, track: track, al: al, onEv: onEvent, stop: make(chan struct{}), done: make(chan struct{})}
	ready := make(chan error, 1)
	go s.run(ready)
	if err := <-ready; err != nil {
		<-s.done
		return nil, err
	}
	return s, nil
}

// Stop ends recording; the track is padded with silence up to now.
func (s *Stream) Stop() { s.StopAt(QPC()) }

// StopAt ends recording at end (QPC, 100 ns): audio captured after it is
// dropped and the track is padded with silence up to it, so streams
// stopped one after the other with the same end still end together.
func (s *Stream) StopAt(end int64) {
	s.endAt.CompareAndSwap(0, end)
	s.once.Do(func() { close(s.stop) })
	<-s.done
}

// end is when the stream stops: StopAt's time, else now.
func (s *Stream) end() int64 {
	if e := s.endAt.Load(); e != 0 {
		return e
	}
	return QPC()
}

func (s *Stream) emit(ev Event) {
	if s.onEv != nil {
		s.onEv(ev)
	}
}

func (s *Stream) run(ready chan<- error) {
	defer close(s.done)
	runtime.LockOSThread()
	if err := coreaudio.Init(); err != nil {
		ready <- err
		return
	}
	defer ole.CoUninitialize()
	if revert := proAudioPriority(); revert != nil {
		defer revert()
	}
	al := s.al
	first := true
	for {
		err := s.session(al, func(dev string) {
			if first {
				first = false
				ready <- nil
			}
			s.emit(Event{Source: s.src, Device: dev})
		})
		if first {
			ready <- err
			return
		}
		select {
		case <-s.stop:
			s.pad(al, s.end())
			return
		default:
		}
		s.emit(Event{Source: s.src, Err: err})
		// The device went (unplugged, default changed, driver restarted):
		// keep the time line going and try again.
		select {
		case <-s.stop:
			s.pad(al, s.end())
			return
		case <-time.After(reopenPause):
		}
		s.pad(al, QPC())
	}
}

// pad writes silence up to at.
func (s *Stream) pad(al *Aligner, at int64) {
	if al.Rate == 0 {
		return
	}
	if n := al.PadTo(at); n > 0 {
		s.track.Write(al.Rate, make([]float32, n))
	}
}

// session opens the device once and records until stop (nil) or failure.
func (s *Stream) session(al *Aligner, started func(device string)) error {
	client, device, err := s.open()
	if err != nil {
		return err
	}
	defer client.Release()

	var f Format
	flags := uint32(wca.AUDCLNT_STREAMFLAGS_EVENTCALLBACK | wca.AUDCLNT_STREAMFLAGS_NOPERSIST)
	var wfx *wca.WAVEFORMATEX
	switch s.src.Kind {
	case Process:
		raw := FloatStereo48k()
		f, _ = ParseWaveFormat(raw)
		wfx = (*wca.WAVEFORMATEX)(unsafe.Pointer(&raw[0]))
		flags = wca.AUDCLNT_STREAMFLAGS_EVENTCALLBACK | wca.AUDCLNT_STREAMFLAGS_LOOPBACK |
			0x80000000 | 0x08000000 // AUTOCONVERTPCM | SRC_DEFAULT_QUALITY
		defer runtime.KeepAlive(raw)
	default:
		if err := client.GetMixFormat(&wfx); err != nil {
			return fmt.Errorf("GetMixFormat: %w", err)
		}
		defer ole.CoTaskMemFree(uintptr(unsafe.Pointer(wfx)))
		b := unsafe.Slice((*byte)(unsafe.Pointer(wfx)), 18+int(wfx.CbSize))
		if f, err = ParseWaveFormat(b); err != nil {
			return err
		}
		if s.src.Kind == Loopback {
			flags |= wca.AUDCLNT_STREAMFLAGS_LOOPBACK
		}
	}
	if err := client.Initialize(wca.AUDCLNT_SHAREMODE_SHARED, flags, wca.REFERENCE_TIME(bufferDur/100), 0, wfx, nil); err != nil {
		return describe("Initialize", err)
	}
	event, err := windows.CreateEvent(nil, 0, 0, nil)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(event)
	if err := client.SetEventHandle(uintptr(event)); err != nil {
		return describe("SetEventHandle", err)
	}
	var cc *wca.IAudioCaptureClient
	if err := client.GetService(wca.IID_IAudioCaptureClient, &cc); err != nil {
		return describe("GetService", err)
	}
	defer cc.Release()
	if err := client.Start(); err != nil {
		return describe("Start", err)
	}
	defer client.Stop()
	al.Rebase(f.Rate)
	started(device)

	lastPacket := time.Now()
	var mono []float32
	for {
		select {
		case <-s.stop:
			return nil
		default:
		}
		r, err := windows.WaitForSingleObject(event, 100)
		if err != nil {
			return err
		}
		if r == uint32(windows.WAIT_TIMEOUT) {
			// Loopback delivers nothing while nothing plays; keep the
			// time line moving so silence is real silence in the track.
			if time.Since(lastPacket) > idlePad {
				s.pad(al, QPC()-int64(idlePad/100))
			}
			continue
		}
		for {
			var next uint32
			if err := cc.GetNextPacketSize(&next); err != nil {
				return describe("GetNextPacketSize", err)
			}
			if next == 0 {
				break
			}
			var data *byte
			var frames, bflags uint32
			var devPos, qpc uint64
			if err := cc.GetBuffer(&data, &frames, &bflags, &devPos, &qpc); err != nil {
				return describe("GetBuffer", err)
			}
			if frames > 0 {
				mono = mono[:0]
				if bflags&wca.AUDCLNT_BUFFERFLAGS_SILENT != 0 || data == nil {
					mono = append(mono, make([]float32, frames)...)
				} else {
					mono = ToMono(mono, unsafe.Slice(data, int(frames)*f.BlockAlign), f)
				}
				if e := s.endAt.Load(); e != 0 {
					// Stopping: keep only what was captured before the end.
					keep := (e - int64(qpc)) * int64(f.Rate) / 10_000_000
					mono = mono[:max(0, min(int64(len(mono)), keep))]
				}
				silence, skip := al.Place(int64(qpc), len(mono))
				if silence > 0 {
					s.track.Write(f.Rate, make([]float32, silence))
				}
				if skip < len(mono) {
					s.track.Write(f.Rate, append([]float32(nil), mono[skip:]...))
				}
				lastPacket = time.Now()
			}
			if err := cc.ReleaseBuffer(frames); err != nil {
				return describe("ReleaseBuffer", err)
			}
		}
	}
}

// open activates an audio client for the source.
func (s *Stream) open() (*wca.IAudioClient, string, error) {
	if s.src.Kind == Process {
		c, err := coreaudio.ActivateProcessLoopback(s.src.PID)
		return c, fmt.Sprintf("process %d", s.src.PID), err
	}
	en, err := coreaudio.NewEnumerator()
	if err != nil {
		return nil, "", err
	}
	defer en.Release()
	flow := coreaudio.Capture
	if s.src.Kind == Loopback {
		flow = coreaudio.Render
	}
	// Windows' default device (the console role): what "Same as system"
	// in meeting apps follows.
	dev, err := en.Device(flow, coreaudio.Console, s.src.DeviceID)
	if errors.Is(err, coreaudio.ErrNotFound) && s.src.DeviceName != "" {
		if id := en.DeviceByName(flow, s.src.DeviceName); id != "" {
			dev, err = en.Device(flow, coreaudio.Console, id)
		}
	}
	if errors.Is(err, coreaudio.ErrNotFound) && s.src.DeviceID != "" {
		dev, err = en.Device(flow, coreaudio.Console, "") // chosen device gone: the default
	}
	if err != nil {
		return nil, "", err
	}
	defer dev.Release()
	var c *wca.IAudioClient
	if err := coreaudio.Activate(dev, wca.IID_IAudioClient, unsafe.Pointer(&c)); err != nil {
		if coreaudio.IsAccessDenied(err) {
			return nil, "", coreaudio.ErrMicBlocked
		}
		return nil, "", describe("Activate", err)
	}
	return c, coreaudio.FriendlyName(dev), nil
}

func describe(step string, err error) error {
	var oe *ole.OleError
	if errors.As(err, &oe) {
		switch uint32(oe.Code()) {
		case errInvalided:
			return fmt.Errorf("%s: the device was removed or changed", step)
		case 0x80070005:
			return coreaudio.ErrMicBlocked
		}
		return fmt.Errorf("%s: HRESULT %#x", step, uint32(oe.Code()))
	}
	return fmt.Errorf("%s: %w", step, err)
}

var (
	kernel32         = windows.NewLazySystemDLL("kernel32.dll")
	procQPC          = kernel32.NewProc("QueryPerformanceCounter")
	procQPF          = kernel32.NewProc("QueryPerformanceFrequency")
	avrt             = windows.NewLazySystemDLL("avrt.dll")
	procAvSetMmThr   = avrt.NewProc("AvSetMmThreadCharacteristicsW")
	procAvRevertMmTh = avrt.NewProc("AvRevertMmThreadCharacteristics")
	qpcFreq          int64
	qpcOnce          sync.Once
)

// QPC is the current QueryPerformanceCounter time in 100 ns units, the
// clock of the capture packets' timestamps.
func QPC() int64 {
	qpcOnce.Do(func() { procQPF.Call(uintptr(unsafe.Pointer(&qpcFreq))) })
	var c int64
	procQPC.Call(uintptr(unsafe.Pointer(&c)))
	if qpcFreq == 0 {
		return time.Now().UnixNano() / 100
	}
	return int64(float64(c) * 1e7 / float64(qpcFreq))
}

// proAudioPriority gives the thread MMCSS "Audio" priority; the returned
// function reverts it.
func proAudioPriority() func() {
	if procAvSetMmThr.Find() != nil {
		return nil
	}
	var idx uint32
	name, _ := windows.UTF16PtrFromString("Audio")
	h, _, _ := procAvSetMmThr.Call(uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&idx)))
	if h == 0 {
		return nil
	}
	return func() { procAvRevertMmTh.Call(h) }
}
