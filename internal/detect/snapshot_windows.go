package detect

import (
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/motivated2die/flit/internal/coreaudio"
)

var (
	user32                   = windows.NewLazySystemDLL("user32.dll")
	procIsWindowVisible      = user32.NewProc("IsWindowVisible")
	procGetWindowTextW       = user32.NewProc("GetWindowTextW")
	procGetWindowTextLengthW = user32.NewProc("GetWindowTextLengthW")
	procGetWindowThreadPID   = user32.NewProc("GetWindowThreadProcessId")
	procGetWindow            = user32.NewProc("GetWindow")
)

// Take snapshots the system: processes, microphone users, window titles.
func Take() (Snapshot, error) {
	s := Snapshot{Procs: map[uint32]Proc{}, Titles: map[uint32][]string{}, Self: windows.GetCurrentProcessId()}
	if h, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0); err == nil {
		e := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
		for err := windows.Process32First(h, &e); err == nil; err = windows.Process32Next(h, &e) {
			s.Procs[e.ProcessID] = Proc{PID: e.ProcessID, Parent: e.ParentProcessID, Exe: strings.ToLower(windows.UTF16ToString(e.ExeFile[:]))}
		}
		windows.CloseHandle(h)
	}
	s.MicOf = map[uint32]string{}
	err := coreaudio.Run(func() error {
		sessions, err := coreaudio.ActiveSessions(coreaudio.Capture)
		for _, a := range sessions {
			if _, ok := s.MicOf[a.PID]; !ok {
				s.Capturing = append(s.Capturing, a.PID)
				s.MicOf[a.PID] = a.DeviceID
			}
		}
		return err
	})
	s.Titles = windowTitles()
	return s, err
}

var (
	titlesMu  sync.Mutex
	titlesOut map[uint32][]string
	enumCB    = syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		if v, _, _ := procIsWindowVisible.Call(hwnd); v == 0 {
			return 1
		}
		if owner, _, _ := procGetWindow.Call(hwnd, 4 /* GW_OWNER */); owner != 0 {
			return 1
		}
		n, _, _ := procGetWindowTextLengthW.Call(hwnd)
		if n == 0 || n > 512 {
			return 1
		}
		buf := make([]uint16, n+1)
		procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), n+1)
		var pid uint32
		procGetWindowThreadPID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
		titlesOut[pid] = append(titlesOut[pid], windows.UTF16ToString(buf))
		return 1
	})
)

// windowTitles lists visible, unowned top-level windows' titles by process.
// Titles stay in memory only (they can name meetings and people) and are
// never logged in full.
func windowTitles() map[uint32][]string {
	titlesMu.Lock()
	defer titlesMu.Unlock()
	titlesOut = map[uint32][]string{}
	_ = windows.EnumWindows(enumCB, nil)
	out := titlesOut
	titlesOut = nil
	return out
}

// Watcher polls snapshots and reports meetings starting and ending.
type Watcher struct {
	stop chan struct{}
	done chan struct{}

	mu    sync.Mutex
	users []MicUser
}

// MicUsers returns who used a microphone at the last poll.
func (w *Watcher) MicUsers() []MicUser {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]MicUser(nil), w.users...)
}

// Watch polls every interval. enabled says which providers count (read on
// every poll, so settings apply at once); onEvent and onError run on the
// watcher's goroutine.
func Watch(interval time.Duration, enabled func(Provider) bool, onEvent func(Event), onError func(error)) *Watcher {
	w := &Watcher{stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(w.done)
		tr := NewTracker()
		t := time.NewTicker(interval)
		defer t.Stop()
		var lastErr string
		for {
			s, err := Take()
			if err != nil && err.Error() != lastErr && onError != nil {
				onError(err)
			}
			if err != nil {
				lastErr = err.Error()
			} else {
				lastErr = ""
			}
			users := MicUsers(s)
			w.mu.Lock()
			w.users = users
			w.mu.Unlock()
			for _, ev := range tr.Update(Find(s, enabled)) {
				onEvent(ev)
			}
			select {
			case <-w.stop:
				return
			case <-t.C:
			}
		}
	}()
	return w
}

// Stop ends polling.
func (w *Watcher) Stop() {
	close(w.stop)
	<-w.done
}
