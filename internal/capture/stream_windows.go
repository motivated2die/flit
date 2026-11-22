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
	// patient: a transient failure to open at the start doesn't fail
	// Start; the stream keeps trying, as after losing its device.
	patient bool
	lastErr string // the last error reported, so a retry repeats none
	once    sync.Once
	endAt   atomic.Int64 // StopAt's end (QPC, 100 ns); 0 = not stopping
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
	return start(src, track, al, onEvent, false)
}

// StartWaiting is StartOn for a source the recording mustn't fail over
// (the meeting's audio): when the device fails to open in a way that
// passes (see transient), it returns a running stream anyway that keeps
// trying every half second, reporting the error through onEvent, with
// silence on the track until the device opens. Other errors fail as with
// StartOn.
func StartWaiting(src Source, track Sink, al *Aligner, onEvent func(Event)) (*Stream, error) {
	return start(src, track, al, onEvent, true)
}

func start(src Source, track Sink, al *Aligner, onEvent func(Event), patient bool) (*Stream, error) {
	s := &Stream{src: src, track: track, al: al, onEv: onEvent, patient: patient, stop: make(chan struct{}), done: make(chan struct{})}
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
			s.lastErr = ""
			s.emit(Event{Source: s.src, Device: dev})
		})
		if first {
			if !s.patient || !transient(err) {
				ready <- err
				return
			}
			first = false
			ready <- nil // recording; the device joins when it opens
		}
		select {
		case <-s.stop:
			s.pad(al, s.end())
			return
		default:
		}
		if msg := err.Error(); msg != s.lastErr {
			s.lastErr = msg // a device failing the same way on every retry is reported once
			s.emit(Event{Source: s.src, Err: err})
		}
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
		if !s.patient {
			return
		}
		// Never opened yet: silence at the track's own rate, rebased
		// once the device's rate is known.
		al.Rebase(Rate)
	}
	if n := al.PadTo(at); n > 0 {
		s.track.Write(al.Rate, make([]float32, n))
	}
}

// Stream flags for a shared-mode client that lets Windows convert from
// whatever the device runs at to the format asked for.
const autoConvert = 0x80000000 | 0x08000000 // AUTOCONVERTPCM | SRC_DEFAULT_QUALITY

// initClient opens the device and initializes an audio client on it, in the
// device's own format (its mix format), or for a process 48 kHz float
// stereo that Windows converts to. When a device refuses its own mix
// format (AUDCLNT_E_UNSUPPORTED_FORMAT: its format changed between the two
// calls, or it isn't ready yet, as a wireless headset waking up), a fresh
// client asks for 48 kHz float stereo with conversion instead.
func (s *Stream) initClient() (*wca.IAudioClient, string, Format, error) {
	client, device, f, err := s.initFormat(s.src.Kind == Process)
	if hresult(err) == errUnsupportedFormat && s.src.Kind != Process {
		var err2 error
		client, device, f, err2 = s.initFormat(true)
		if err2 == nil {
			s.emit(Event{Source: s.src, Err: fmt.Errorf("%w; recording with Windows converting the format instead", err)})
			return client, device, f, nil
		}
	}
	return client, device, f, err
}

// initFormat is one attempt of initClient: the mix format, or with convert
// 48 kHz float stereo converted by Windows.
func (s *Stream) initFormat(convert bool) (*wca.IAudioClient, string, Format, error) {
	client, device, err := s.open()
	if err != nil {
		return nil, "", Format{}, err
	}
	fail := func(err error) (*wca.IAudioClient, string, Format, error) {
		client.Release()
		return nil, "", Format{}, err
	}
	var f Format
	flags := uint32(wca.AUDCLNT_STREAMFLAGS_EVENTCALLBACK | wca.AUDCLNT_STREAMFLAGS_NOPERSIST)
	if s.src.Kind != Mic {
		flags |= wca.AUDCLNT_STREAMFLAGS_LOOPBACK
	}
	var wfx *wca.WAVEFORMATEX
	if convert {
		raw := FloatStereo48k()
		f, _ = ParseWaveFormat(raw)
		wfx = (*wca.WAVEFORMATEX)(unsafe.Pointer(&raw[0]))
		flags |= autoConvert
		if s.src.Kind == Process {
			flags &^= wca.AUDCLNT_STREAMFLAGS_NOPERSIST // as before for process loopback
		}
		defer runtime.KeepAlive(raw)
	} else {
		if err := client.GetMixFormat(&wfx); err != nil {
			return fail(fmt.Errorf("GetMixFormat: %w", err))
		}
		defer ole.CoTaskMemFree(uintptr(unsafe.Pointer(wfx)))
		b := unsafe.Slice((*byte)(unsafe.Pointer(wfx)), 18+int(wfx.CbSize))
		if f, err = ParseWaveFormat(b); err != nil {
			return fail(err)
		}
	}
	if err := client.Initialize(wca.AUDCLNT_SHAREMODE_SHARED, flags, wca.REFERENCE_TIME(bufferDur/100), 0, wfx, nil); err != nil {
		return fail(describe("Initialize", err))
	}
	return client, device, f, nil
}

// session opens the device once and records until stop (nil) or failure.
func (s *Stream) session(al *Aligner, started func(device string)) error {
	client, device, f, err := s.initClient()
	if err != nil {
		return err
	}
	defer client.Release()
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

// Core Audio's errors that Windows leaves without message text.
const (
	errUnsupportedFormat = 0x88890008 // AUDCLNT_E_UNSUPPORTED_FORMAT
	errDeviceInUse       = 0x8889000A // AUDCLNT_E_DEVICE_IN_USE
	errEndpointCreate    = 0x8889000F // AUDCLNT_E_ENDPOINT_CREATE_FAILED
	errServiceNotRunning = 0x88890010 // AUDCLNT_E_SERVICE_NOT_RUNNING
)

var hresultNames = map[uint32]string{
	errInvalided:         "the device was removed or changed",
	errUnsupportedFormat: "the device refused its own audio format (AUDCLNT_E_UNSUPPORTED_FORMAT)",
	errDeviceInUse:       "another app uses the device exclusively (AUDCLNT_E_DEVICE_IN_USE)",
	errEndpointCreate:    "Windows couldn't set up the device (AUDCLNT_E_ENDPOINT_CREATE_FAILED)",
	errServiceNotRunning: "the Windows audio service isn't running (AUDCLNT_E_SERVICE_NOT_RUNNING)",
}

// hrError is a failed Core Audio step with its HRESULT.
type hrError struct {
	step string
	code uint32
}

func (e *hrError) Error() string {
	if name, ok := hresultNames[e.code]; ok {
		return fmt.Sprintf("%s: %s", e.step, name)
	}
	return fmt.Sprintf("%s: HRESULT %#x", e.step, e.code)
}

func describe(step string, err error) error {
	var oe *ole.OleError
	if errors.As(err, &oe) {
		if uint32(oe.Code()) == 0x80070005 {
			return coreaudio.ErrMicBlocked
		}
		return &hrError{step: step, code: uint32(oe.Code())}
	}
	return fmt.Errorf("%s: %w", step, err)
}

// hresult is err's HRESULT, 0 if it has none.
func hresult(err error) uint32 {
	var he *hrError
	if errors.As(err, &he) {
		return he.code
	}
	return 0
}

// transient reports whether a device failed in a way that passes by
// itself: its format changing under us, another app holding it for a
// moment, the device being reset. Such a failure is retried instead of
// given up on (a meeting's audio, see StartWaiting).
func transient(err error) bool {
	switch hresult(err) {
	case errUnsupportedFormat, errDeviceInUse, errInvalided, errEndpointCreate:
		return true
	}
	return false
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
