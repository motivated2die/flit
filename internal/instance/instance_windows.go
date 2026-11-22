// Package instance keeps flit to one running copy per user and lets other
// processes (a second start, the installer, the uninstaller) talk to it.
package instance

import (
	"errors"
	"os"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// suffix separates a development run from an installed flit: with
// FLIT_INSTANCE=dev both run side by side, each with its own lock and
// message window.
var suffix = func() string {
	if s := os.Getenv("FLIT_INSTANCE"); s != "" {
		return "." + s
	}
	return ""
}()

// Suffix is "" for the normal flit and ".<name>" for a side-by-side
// instance (FLIT_INSTANCE), whose registry entries must not touch the real
// ones either.
func Suffix() string { return suffix }

// WindowClass is the class of the running instance's hidden message
// window, by which other processes find it.
var WindowClass = "flit.MessageWindow" + suffix

var (
	user32                  = windows.NewLazySystemDLL("user32.dll")
	procFindWindowW         = user32.NewProc("FindWindowW")
	procPostMessageW        = user32.NewProc("PostMessageW")
	procRegisterWindowMsg   = user32.NewProc("RegisterWindowMessageW")
	procAllowSetForegroundW = user32.NewProc("AllowSetForegroundWindow")
)

// Messages the running instance handles, registered system-wide by name so
// their numbers can't collide with another app's.
var (
	MsgShow = registerMessage("flit.Show") // open the UI
	MsgQuit = registerMessage("flit.Quit") // quit (installer, uninstaller)
)

func registerMessage(name string) uint32 {
	r, _, _ := procRegisterWindowMsg.Call(uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(name))))
	return uint32(r)
}

// Lock holds the single-instance mutex.
type Lock struct{ h windows.Handle }

// ErrRunning means another instance holds the lock.
var ErrRunning = errors.New("flit is already running")

// Acquire takes the per-user single-instance lock.
func Acquire() (*Lock, error) {
	h, err := windows.CreateMutex(nil, false, windows.StringToUTF16Ptr(`Local\flit.SingleInstance`+suffix))
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		windows.CloseHandle(h)
		return nil, ErrRunning
	}
	if err != nil {
		return nil, err
	}
	return &Lock{h: h}, nil
}

// Release gives the lock up.
func (l *Lock) Release() {
	if l != nil && l.h != 0 {
		windows.CloseHandle(l.h)
		l.h = 0
	}
}

func window() uintptr {
	h, _, _ := procFindWindowW.Call(uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(WindowClass))), 0)
	return h
}

// Show asks the running instance to show itself. It reports whether one
// was found.
func Show() bool {
	h := window()
	if h == 0 {
		return false
	}
	procAllowSetForegroundW.Call(^uintptr(0)) // ASFW_ANY: let it take the foreground
	procPostMessageW.Call(h, uintptr(MsgShow), 0, 0)
	return true
}

// Quit asks the running instance to quit and waits up to timeout for its
// window to go. It reports whether none is running afterwards.
func Quit(timeout time.Duration) bool {
	h := window()
	if h == 0 {
		return true
	}
	procPostMessageW.Call(h, uintptr(MsgQuit), 0, 0)
	for end := time.Now().Add(timeout); time.Now().Before(end); {
		time.Sleep(100 * time.Millisecond)
		if window() == 0 {
			time.Sleep(300 * time.Millisecond) // let the process finish exiting
			return true
		}
	}
	return false
}
