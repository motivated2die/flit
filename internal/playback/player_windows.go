package playback

import (
	"errors"
	"fmt"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/motivated2die/flit/internal/audio/wav"
)

// The device is the waveOut API (winmm): 16 kHz mono 16-bit PCM to the
// default output, converted by Windows. A few short buffers live in memory
// Windows can keep pointers to (VirtualAlloc); a goroutine refills them as
// the device hands them back (an event per finished buffer). Every waveOut
// call and all state are under one mutex.

var (
	winmm                = windows.NewLazySystemDLL("winmm.dll")
	procWaveOutOpen      = winmm.NewProc("waveOutOpen")
	procWaveOutClose     = winmm.NewProc("waveOutClose")
	procWaveOutPrepare   = winmm.NewProc("waveOutPrepareHeader")
	procWaveOutUnprepare = winmm.NewProc("waveOutUnprepareHeader")
	procWaveOutWrite     = winmm.NewProc("waveOutWrite")
	procWaveOutReset     = winmm.NewProc("waveOutReset")
	procWaveOutGetPos    = winmm.NewProc("waveOutGetPosition")
)

const (
	waveMapper    = 0xFFFFFFFF
	callbackEvent = 0x00050000
	whdrDone      = 0x00000001
	timeSamples   = 0x0002
	nBufs         = 4
	bufSamples    = Rate / 10 // 100 ms
)

type waveFormatEx struct {
	FormatTag      uint16
	Channels       uint16
	SamplesPerSec  uint32
	AvgBytesPerSec uint32
	BlockAlign     uint16
	BitsPerSample  uint16
	Size           uint16
}

type waveHdr struct {
	Data          uintptr
	BufferLength  uint32
	BytesRecorded uint32
	User          uintptr
	Flags         uint32
	Loops         uint32
	Next          uintptr
	Reserved      uintptr
}

type mmTime struct {
	Type  uint32
	Value uint32
	_     uint32
}

// Player plays one session's tracks. Methods are safe from any goroutine.
type Player struct {
	// OnChange is called (on the player's goroutine) when playback stops
	// by itself at the end.
	OnChange func()

	tracks []*wav.File
	frames int64

	mu      sync.Mutex
	hwo     uintptr
	mem     uintptr // VirtualAlloc'd headers and sample buffers
	hdrs    [nBufs]*waveHdr
	data    [nBufs][]int16
	queued  [nBufs]bool
	done    windows.Handle // signalled by the device per finished buffer
	wake    windows.Handle // signalled by commands
	cur     *cursor
	which   []bool // tracks playing
	tl      timeline
	playing bool
	pos     int64 // timeline position when not playing
	closed  bool
	exited  chan struct{}
}

// Open opens a session's tracks (missing files are left out; all must be
// 16 kHz). The device opens on the first Play.
func Open(paths ...string) (*Player, error) {
	p := &Player{exited: make(chan struct{})}
	for _, path := range paths {
		f, err := wav.Open(path)
		if err != nil {
			p.closeFiles()
			return nil, err
		}
		if f.Format.SampleRate != Rate {
			f.Close()
			p.closeFiles()
			return nil, fmt.Errorf("%s: %d Hz, want %d", path, f.Format.SampleRate, Rate)
		}
		p.tracks = append(p.tracks, f)
		p.frames = max(p.frames, f.Frames())
	}
	if len(p.tracks) == 0 {
		return nil, errors.New("no recording to play")
	}
	return p, nil
}

func (p *Player) closeFiles() {
	for _, f := range p.tracks {
		f.Close()
	}
}

// Length is the timeline's length in samples.
func (p *Player) Length() int64 { return p.frames }

// Tracks is how many tracks were opened.
func (p *Player) Tracks() int { return len(p.tracks) }

// Play plays ranges of the timeline one after another; which says which
// tracks to hear (nil: all). It replaces whatever was playing.
func (p *Player) Play(ranges []Range, which []bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return errors.New("player closed")
	}
	if err := p.openDevice(); err != nil {
		return err
	}
	p.resetDevice()
	for i := range ranges {
		ranges[i].From = max(0, min(ranges[i].From, p.frames))
		ranges[i].To = max(0, min(ranges[i].To, p.frames))
	}
	p.cur, p.which, p.playing = newCursor(ranges), which, true
	if len(ranges) > 0 {
		p.pos = ranges[0].From
	}
	windows.SetEvent(p.wake)
	return nil
}

// PlayFrom plays all tracks from timeline sample at to the end.
func (p *Player) PlayFrom(at int64) error { return p.Play([]Range{{at, p.frames}}, nil) }

// Pause stops playing and remembers where; Resume goes on from there.
func (p *Player) Pause() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.playing {
		return
	}
	p.pos = p.positionLocked()
	rest := p.cur.rest(p.pos)
	p.resetDevice()
	p.cur, p.playing = newCursor(rest), false
}

// Resume plays on after Pause (or from the last position).
func (p *Player) Resume() error {
	p.mu.Lock()
	cur, which, pos := p.cur, p.which, p.pos
	p.mu.Unlock()
	if cur != nil && cur.i < len(cur.ranges) {
		return p.Play(cur.rest(pos), which)
	}
	return p.PlayFrom(pos)
}

// MoveTo moves to timeline sample at, playing on if it was playing.
func (p *Player) MoveTo(at int64) error {
	p.mu.Lock()
	playing := p.playing
	p.mu.Unlock()
	if playing {
		return p.PlayFrom(at)
	}
	p.mu.Lock()
	p.pos, p.cur, p.which = max(0, min(at, p.frames)), nil, nil
	p.mu.Unlock()
	return nil
}

// Playing reports whether sound is playing.
func (p *Player) Playing() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.playing
}

// Solo reports which tracks play (nil: all).
func (p *Player) Solo() []bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.which
}

// Position is the timeline sample now audible.
func (p *Player) Position() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.positionLocked()
}

func (p *Player) positionLocked() int64 {
	if !p.playing || p.hwo == 0 {
		return p.pos
	}
	t := mmTime{Type: timeSamples}
	procWaveOutGetPos.Call(p.hwo, uintptr(unsafe.Pointer(&t)), unsafe.Sizeof(t))
	if at, ok := p.tl.at(int64(t.Value)); ok {
		return at
	}
	return p.pos
}

// Close stops playing and frees the device and files.
func (p *Player) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	started := p.hwo != 0
	if started {
		windows.SetEvent(p.wake)
	}
	p.mu.Unlock()
	if started {
		<-p.exited
	} else {
		close(p.exited)
	}
	p.closeFiles()
}

// openDevice opens waveOut and starts the feeding goroutine (mu held).
func (p *Player) openDevice() error {
	if p.hwo != 0 {
		return nil
	}
	var err error
	if p.done, err = windows.CreateEvent(nil, 0, 0, nil); err != nil {
		return err
	}
	if p.wake, err = windows.CreateEvent(nil, 0, 0, nil); err != nil {
		windows.CloseHandle(p.done)
		return err
	}
	wf := waveFormatEx{FormatTag: 1, Channels: 1, SamplesPerSec: Rate, AvgBytesPerSec: 2 * Rate, BlockAlign: 2, BitsPerSample: 16}
	var hwo uintptr
	if r, _, _ := procWaveOutOpen.Call(uintptr(unsafe.Pointer(&hwo)), waveMapper, uintptr(unsafe.Pointer(&wf)), uintptr(p.done), 0, callbackEvent); r != 0 {
		windows.CloseHandle(p.done)
		windows.CloseHandle(p.wake)
		return fmt.Errorf("opening the audio output: MMRESULT %d", r)
	}
	hdrSize := unsafe.Sizeof(waveHdr{})
	size := nBufs * (hdrSize + 2*bufSamples)
	mem, err := windows.VirtualAlloc(0, size, windows.MEM_COMMIT|windows.MEM_RESERVE, windows.PAGE_READWRITE)
	if err != nil {
		procWaveOutClose.Call(hwo)
		return err
	}
	p.hwo, p.mem = hwo, mem
	for i := 0; i < nBufs; i++ {
		base := mem + uintptr(i)*(hdrSize+2*bufSamples)
		p.hdrs[i] = (*waveHdr)(ptr(base))
		p.data[i] = unsafe.Slice((*int16)(ptr(base+hdrSize)), bufSamples)
		p.hdrs[i].Data = base + hdrSize
		p.hdrs[i].BufferLength = 2 * bufSamples
		procWaveOutPrepare.Call(hwo, uintptr(unsafe.Pointer(p.hdrs[i])), hdrSize)
	}
	go p.feed()
	return nil
}

// resetDevice stops the device and forgets what was queued (mu held).
func (p *Player) resetDevice() {
	if p.hwo != 0 {
		procWaveOutReset.Call(p.hwo) // marks every buffer done, position back to 0
	}
	p.queued = [nBufs]bool{}
	p.tl.reset()
}

// feed refills buffers until the player closes.
func (p *Player) feed() {
	defer close(p.exited)
	handles := []windows.Handle{p.done, p.wake}
	scratch := make([][]float32, len(p.tracks))
	for i := range scratch {
		scratch[i] = make([]float32, bufSamples)
	}
	mixed := make([]float32, bufSamples)
	for {
		windows.WaitForMultipleObjects(handles, false, 200)
		p.mu.Lock()
		if p.closed {
			procWaveOutReset.Call(p.hwo)
			for i := 0; i < nBufs; i++ {
				procWaveOutUnprepare.Call(p.hwo, uintptr(unsafe.Pointer(p.hdrs[i])), unsafe.Sizeof(waveHdr{}))
			}
			procWaveOutClose.Call(p.hwo)
			windows.VirtualFree(p.mem, 0, windows.MEM_RELEASE)
			windows.CloseHandle(p.done)
			windows.CloseHandle(p.wake)
			p.hwo = 0
			p.mu.Unlock()
			return
		}
		ended := false
		if p.playing {
			busy := 0
			for i := 0; i < nBufs; i++ {
				if p.queued[i] && p.hdrs[i].Flags&whdrDone == 0 {
					busy++
					continue
				}
				n := p.fill(i, scratch, mixed)
				if n == 0 {
					p.queued[i] = false
					continue
				}
				p.hdrs[i].BufferLength = uint32(2 * n)
				p.hdrs[i].Flags &^= whdrDone
				procWaveOutWrite.Call(p.hwo, uintptr(unsafe.Pointer(p.hdrs[i])), unsafe.Sizeof(waveHdr{}))
				p.queued[i] = true
				busy++
			}
			if busy == 0 { // nothing left to play, and everything played
				p.pos = p.positionLocked()
				p.playing, p.cur = false, nil
				p.resetDevice()
				ended = true
			}
		}
		onChange := p.OnChange
		p.mu.Unlock()
		if ended && onChange != nil {
			onChange()
		}
	}
}

// fill puts the next piece into buffer i and returns its samples (mu held).
func (p *Player) fill(i int, scratch [][]float32, mixed []float32) int {
	if p.cur == nil {
		return 0
	}
	tl, n, silent := p.cur.next(bufSamples)
	if n == 0 {
		return 0
	}
	out := p.data[i][:n]
	if silent {
		clear(out)
	} else {
		var srcs [][]float32
		for t, f := range p.tracks {
			if p.which != nil && (t >= len(p.which) || !p.which[t]) {
				continue
			}
			got, _ := f.ReadFrames(scratch[t][:n], tl)
			clear(scratch[t][got:n])
			srcs = append(srcs, scratch[t][:n])
		}
		mix(mixed[:n], srcs)
		for k := int64(0); k < n; k++ {
			out[k] = wav.FloatToInt16(mixed[k])
		}
	}
	p.tl.add(tl, n, silent)
	return int(n)
}

// ptr turns an address of memory Windows allocated into a pointer.
func ptr(a uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&a)) }
