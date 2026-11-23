package playback

import (
	"errors"
	"fmt"
	"io"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/motivated2die/flit/internal/audio/dsp"
	"github.com/motivated2die/flit/internal/audio/track"
	"github.com/motivated2die/flit/internal/audio/wav"
)

// The device is the waveOut API (winmm): 48 kHz stereo 16-bit PCM to the
// default output, matching Windows' native mix rate so 48 kHz recordings play
// without legacy 16->48 kHz upsampling and meeting tracks keep their stereo
// image. A few short buffers live in memory Windows can keep pointers to
// (VirtualAlloc); a goroutine refills them as the device hands them back (an
// event per finished buffer). Every waveOut call and all state are under one
// mutex.

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
	outChannels   = 2
	bufFrames     = Rate / 10 // 100 ms (4800 stereo frames)
	bufSamples    = bufFrames * outChannels
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

// Player plays one session's tracks (.m4a, .aac, or legacy .wav). Methods are
// safe from any goroutine.
type Player struct {
	// OnChange is called (on the player's goroutine) when playback stops
	// by itself at the end.
	OnChange func()

	tracks []track.File
	frames int64 // timeline length in Rate (48 kHz) frames

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

// Open opens a session's tracks (.m4a, .aac, or legacy .wav at any sample
// rate). The device opens on the first Play.
func Open(paths ...string) (*Player, error) {
	p := &Player{exited: make(chan struct{})}
	for _, path := range paths {
		f, err := track.Open(path)
		if err != nil {
			p.closeFiles()
			return nil, err
		}
		if f.SampleRate() <= 0 || f.Channels() <= 0 {
			f.Close()
			p.closeFiles()
			return nil, fmt.Errorf("%s: invalid audio format", path)
		}
		p.tracks = append(p.tracks, f)
		p.frames = max(p.frames, trackFrames(f))
	}
	if len(p.tracks) == 0 {
		return nil, errors.New("no recording to play")
	}
	return p, nil
}

func trackFrames(f track.File) int64 {
	if f.SampleRate() == Rate {
		return f.Frames()
	}
	return f.Frames() * Rate / int64(f.SampleRate())
}

func (p *Player) closeFiles() {
	for _, f := range p.tracks {
		f.Close()
	}
}

// Length is the timeline's length in frames (at Rate = 48 kHz).
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

// PlayFrom plays all tracks from timeline frame at to the end.
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

// MoveTo moves to timeline frame at, playing on if it was playing.
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

// Position is the timeline frame now audible.
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

// openDevice opens waveOut (48 kHz stereo 16-bit) and starts the feeding goroutine (mu held).
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
	const blockAlign = outChannels * 2
	wf := waveFormatEx{FormatTag: 1, Channels: outChannels, SamplesPerSec: Rate, AvgBytesPerSec: blockAlign * Rate, BlockAlign: blockAlign, BitsPerSample: 16}
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
				p.hdrs[i].BufferLength = uint32(outChannels * 2 * n)
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

// fill puts the next piece into buffer i and returns its frame count (mu held).
func (p *Player) fill(i int, scratch [][]float32, mixed []float32) int {
	if p.cur == nil {
		return 0
	}
	tl, n, silent := p.cur.next(bufFrames)
	if n == 0 {
		return 0
	}
	nSamples := int(n) * outChannels
	out := p.data[i][:nSamples]
	if silent {
		clear(out)
	} else {
		var srcs [][]float32
		for t, f := range p.tracks {
			if p.which != nil && (t >= len(p.which) || !p.which[t]) {
				continue
			}
			got := readStereoAt(f, scratch[t][:nSamples], tl)
			clear(scratch[t][got*outChannels : nSamples])
			srcs = append(srcs, scratch[t][:nSamples])
		}
		mix(mixed[:nSamples], srcs)
		for k := range nSamples {
			out[k] = wav.FloatToInt16(mixed[k])
		}
	}
	p.tl.add(tl, n, silent)
	return int(n)
}

// readStereoAt reads up to len(dst)/2 stereo frames at Rate (48 kHz) starting
// at timeline frame tl. Native 48 kHz tracks pass through with zero resampling;
// legacy 16 kHz WAV tracks are upsampled cleanly with Kaiser-sinc warmup.
func readStereoAt(f track.File, dst []float32, tl int64) int {
	want := len(dst) / outChannels
	if want <= 0 {
		return 0
	}
	inRate := f.SampleRate()
	ch := f.Channels()
	if inRate == Rate {
		if ch == 2 {
			got, _ := f.ReadInterleaved(dst, tl)
			return got
		}
		mono := make([]float32, want)
		got, _ := f.ReadFrames(mono, tl)
		for i := range got {
			dst[2*i], dst[2*i+1] = mono[i], mono[i]
		}
		return got
	}

	// Resample from inRate (e.g. legacy 16 kHz WAV) to 48 kHz stereo.
	totalOut := trackFrames(f)
	if tl < 0 || tl >= totalOut {
		return 0
	}
	want64 := min(int64(want), totalOut-tl)
	const marginOut = 128
	startOut := max(int64(0), tl-marginOut)
	endOut := min(totalOut, tl+want64+marginOut)

	inStart := startOut * int64(inRate) / Rate
	inEnd := (endOut*int64(inRate) + Rate - 1) / Rate
	inEnd = min(f.Frames(), inEnd+8)
	inCount := inEnd - inStart
	if inCount <= 0 {
		return 0
	}
	raw := make([]float32, int(inCount)*ch)
	got, err := f.ReadInterleaved(raw, inStart)
	if (err != nil && err != io.EOF) || got == 0 {
		return 0
	}
	skip := int(tl - startOut)
	if ch == 1 {
		rs := dsp.NewResampler(inRate, Rate)
		out := append(rs.Process(raw[:got]), rs.Flush()...)
		if skip < 0 || skip >= len(out) {
			return 0
		}
		n := min(int(want64), len(out)-skip)
		for i := range n {
			v := out[skip+i]
			dst[2*i], dst[2*i+1] = v, v
		}
		return n
	}
	lIn := make([]float32, got)
	rIn := make([]float32, got)
	for i := range got {
		lIn[i], rIn[i] = raw[i*ch], raw[i*ch+1]
	}
	rsL := dsp.NewResampler(inRate, Rate)
	rsR := dsp.NewResampler(inRate, Rate)
	lOut := append(rsL.Process(lIn), rsL.Flush()...)
	rOut := append(rsR.Process(rIn), rsR.Flush()...)
	avail := min(len(lOut), len(rOut))
	if skip < 0 || skip >= avail {
		return 0
	}
	n := min(int(want64), avail-skip)
	for i := range n {
		dst[2*i], dst[2*i+1] = lOut[skip+i], rOut[skip+i]
	}
	return n
}

// ptr turns an address of memory Windows allocated into a pointer.
func ptr(a uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&a)) }
