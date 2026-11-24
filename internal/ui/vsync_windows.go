package ui

import (
	"sync"
	"sync/atomic"
	"time"
)

const (
	wmVsync = wmApp + 3 // one VSync frame tick posted by vsyncPacer

	// Reasons a host wants VSync-paced frames (bitmask, UI thread only).
	vsyncScroll uint8 = 1 << iota
	vsyncTransition
	vsyncAnimate
	vsyncLive
	vsyncSelScroll
	vsyncRipple
)

var procDwmFlush = dwmapi.NewProc("DwmFlush")

// vsyncPacer wakes a window once per DWM composition VSync (matching the
// monitor's native refresh rate: 60/120/144/165/240 Hz) while active, and
// stops completely (zero goroutines, zero CPU) when idle. At most one
// wmVsync message is kept in flight so slow frames never flood the queue.
type vsyncPacer struct {
	mu      sync.Mutex
	stop    chan struct{}
	pending atomic.Bool

	// wait blocks until the next frame boundary; defaults to waitDWMVSync.
	// Overridable in tests.
	wait func()
	// post delivers a frame tick to hwnd; defaults to postVsync.
	// Overridable in tests.
	post func(hwnd uintptr)
}

// waitDWMVSync blocks until DWM's next vertical blank. If DwmFlush is
// unavailable, fails (e.g. RDP or locked desktop), or returns immediately
// (< 1 ms, indicating no active compositor), it falls back to a 16 ms sleep
// so the loop never busy-spins.
func waitDWMVSync() {
	if procDwmFlush.Find() != nil {
		time.Sleep(16 * time.Millisecond)
		return
	}
	t0 := time.Now()
	r, _, _ := procDwmFlush.Call()
	if int32(r) < 0 || time.Since(t0) < time.Millisecond {
		time.Sleep(16 * time.Millisecond)
	}
}

func postVsync(hwnd uintptr) {
	if hwnd != 0 {
		procPostMessageW.Call(hwnd, wmVsync, 0, 0)
	}
}

// start begins posting wmVsync ticks to hwnd if not already running.
func (p *vsyncPacer) start(hwnd uintptr) {
	p.mu.Lock()
	if p.stop != nil {
		p.mu.Unlock()
		return
	}
	ch := make(chan struct{})
	p.stop = ch
	waitFn := p.wait
	if waitFn == nil {
		waitFn = waitDWMVSync
	}
	postFn := p.post
	if postFn == nil {
		postFn = postVsync
	}
	p.mu.Unlock()

	go func() {
		for {
			waitFn()
			select {
			case <-ch:
				return
			default:
			}
			if p.pending.CompareAndSwap(false, true) {
				postFn(hwnd)
			}
		}
	}()
}

// stopPacer stops posting ticks and lets the pacer goroutine exit after its
// current wait finishes.
func (p *vsyncPacer) stopPacer() {
	p.mu.Lock()
	if p.stop != nil {
		close(p.stop)
		p.stop = nil
	}
	p.mu.Unlock()
	p.pending.Store(false)
}

// ack marks the in-flight wmVsync tick as handled so the next VSync can post.
func (p *vsyncPacer) ack() {
	p.pending.Store(false)
}
