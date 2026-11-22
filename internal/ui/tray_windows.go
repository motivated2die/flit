package ui

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/motivated2die/flit/internal/instance"
)

const (
	wmTray   = wmApp + 1 // tray icon callback
	wmInvoke = wmApp + 2 // run queued functions on the UI thread
)

// MenuItem is one entry of the tray's right-click menu. A nil OnClick and
// an empty label make a separator.
type MenuItem struct {
	Label    string
	Checked  bool
	Disabled bool
	OnClick  func()
}

// Tray owns the notification-area icon and the hidden message window that
// other flit processes find (instance.WindowClass). Create it and call Run
// on the same goroutine, which becomes the UI thread.
type Tray struct {
	hwnd           uintptr
	nid            notifyIconData
	icon           uintptr
	state          TrayIcon
	taskbarCreated uint32

	qmu   sync.Mutex
	queue []func()

	// Menu builds the right-click menu each time it opens.
	Menu func() []MenuItem
	// OnClick runs on a left click.
	OnClick func()
	// OnShow runs when another process asks to show flit (a second start).
	OnShow func()
	// OnQuitRequest runs when the installer or uninstaller asks flit to quit.
	OnQuitRequest func()
	// OnEndSession runs when Windows signs out or shuts down; the process
	// ends right after it returns.
	OnEndSession func()
	// OnNotificationClick runs when the user clicks the last notification.
	OnNotificationClick func()
	// OnRightClick, if set, replaces the native menu (x, y: the cursor).
	OnRightClick func(x, y int32)
}

var trayClassRegistered bool

// NewTray creates the window and the icon. It locks the calling goroutine
// to its OS thread.
func NewTray(tooltip string) (*Tray, error) {
	runtime.LockOSThread()
	allowDarkMenus()
	t := &Tray{state: TrayIcon{State: TrayIdle}}
	if !trayClassRegistered {
		wc := wndClassEx{WndProc: windows.NewCallback(t.wndProc), Instance: moduleHandle(), ClassName: u16(instance.WindowClass)}
		wc.Size = uint32(unsafe.Sizeof(wc))
		if r, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
			return nil, fmt.Errorf("registering the window class: %w", err)
		}
		trayClassRegistered = true
	}
	h, _, err := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(u16(instance.WindowClass))), uintptr(unsafe.Pointer(u16("flit"))),
		0, 0, 0, 0, 0, 0, 0, moduleHandle(), 0)
	if h == 0 {
		return nil, fmt.Errorf("creating the message window: %w", err)
	}
	t.hwnd = h
	r, _, _ := procRegisterWindowMessageW.Call(uintptr(unsafe.Pointer(u16("TaskbarCreated"))))
	t.taskbarCreated = uint32(r)

	t.nid = notifyIconData{Wnd: h, ID: 1, Flags: nifMessage | nifIcon | nifTip, CallbackMessage: wmTray}
	t.nid.Size = uint32(unsafe.Sizeof(t.nid))
	t.setTip(tooltip)
	t.renderIcon()
	if err := t.notify(nimAdd); err != nil {
		return nil, fmt.Errorf("adding the tray icon: %w", err)
	}
	return t, nil
}

// Post runs fn on the UI thread. Safe from any goroutine.
func (t *Tray) Post(fn func()) {
	t.qmu.Lock()
	t.queue = append(t.queue, fn)
	t.qmu.Unlock()
	procPostMessageW.Call(t.hwnd, wmInvoke, 0, 0)
}

// SetState changes the icon (on the UI thread).
func (t *Tray) SetState(state TrayState, progress float64) {
	if t.state.State == state && t.state.Progress == progress {
		return
	}
	t.state.State, t.state.Progress = state, progress
	t.renderIcon()
	_ = t.notify(nimModify)
}

// SetTooltip changes the hover text (on the UI thread; 127 characters).
func (t *Tray) SetTooltip(s string) {
	t.setTip(s)
	_ = t.notify(nimModify)
}

func (t *Tray) setTip(s string) {
	u, _ := windows.UTF16FromString(s)
	n := copy(t.nid.Tip[:len(t.nid.Tip)-1], u)
	t.nid.Tip[n] = 0
}

func (t *Tray) renderIcon() {
	t.state.Size = trayIconSize()
	t.state.Light = lightTaskbar()
	icon := newIcon(t.state.Draw())
	if icon == 0 {
		return
	}
	old := t.icon
	t.icon = icon
	t.nid.Icon = icon
	if old != 0 {
		defer procDestroyIcon.Call(old) // after the shell took the new one
	}
}

// Notify shows a Windows notification from the tray icon (title up to 63
// characters, text up to 255). A click on it runs OnNotificationClick.
func (t *Tray) Notify(title, text string) {
	nid := t.nid
	nid.Flags = nifInfo
	tt, _ := windows.UTF16FromString(title)
	tx, _ := windows.UTF16FromString(text)
	copy(nid.InfoTitle[:len(nid.InfoTitle)-1], tt)
	copy(nid.Info[:len(nid.Info)-1], tx)
	nid.InfoFlags = niifNone | niifRespectQuietTime
	procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&nid)))
}

// Hwnd is the message window, usable as a clipboard owner.
func (t *Tray) Hwnd() uintptr { return t.hwnd }

// Run pumps messages until Quit.
func (t *Tray) Run() {
	var m msg
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			return
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

// Quit removes the icon and ends Run (on the UI thread).
func (t *Tray) Quit() {
	_ = t.notify(nimDelete)
	if t.icon != 0 {
		procDestroyIcon.Call(t.icon)
		t.icon = 0
	}
	procDestroyWindow.Call(t.hwnd)
	procPostQuitMessage.Call(0)
}

func (t *Tray) notify(op uintptr) error {
	if r, _, err := procShellNotifyIconW.Call(op, uintptr(unsafe.Pointer(&t.nid))); r == 0 {
		if err == nil || errors.Is(err, windows.ERROR_SUCCESS) {
			return errors.New("Shell_NotifyIcon failed")
		}
		return err
	}
	return nil
}

func (t *Tray) runQueue() {
	t.qmu.Lock()
	q := t.queue
	t.queue = nil
	t.qmu.Unlock()
	for _, fn := range q {
		fn()
	}
}

func (t *Tray) wndProc(hwnd uintptr, m uint32, w, l uintptr) uintptr {
	switch {
	case m == wmTray:
		switch uint32(l) {
		case wmLButtonUp:
			if t.OnClick != nil {
				t.OnClick()
			}
		case wmRButtonUp, wmContextMenu:
			if t.OnRightClick != nil {
				var pt point
				procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
				t.OnRightClick(pt.X, pt.Y)
			} else {
				t.showMenu()
			}
		case ninBalloonUserClick:
			if t.OnNotificationClick != nil {
				t.OnNotificationClick()
			}
		}
		return 0
	case m == wmInvoke:
		t.runQueue()
		return 0
	case m == instance.MsgShow:
		if t.OnShow != nil {
			t.OnShow()
		}
		return 0
	case m == instance.MsgQuit:
		if t.OnQuitRequest != nil {
			t.OnQuitRequest()
		} else {
			t.Quit()
		}
		return 0
	case m == wmEndSession:
		if w != 0 && t.OnEndSession != nil {
			t.OnEndSession()
		}
		return 0
	case m == t.taskbarCreated && m != 0:
		t.renderIcon() // Explorer restarted; also picks up a theme change
		_ = t.notify(nimAdd)
		return 0
	}
	return defWindowProc(hwnd, m, w, l)
}

func (t *Tray) showMenu() {
	if t.Menu == nil {
		return
	}
	items := t.Menu()
	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer procDestroyMenu.Call(menu)
	for i, it := range items {
		if it.Label == "" && it.OnClick == nil {
			procAppendMenuW.Call(menu, mfSeparator, 0, 0)
			continue
		}
		flags := uintptr(mfString)
		if it.Checked {
			flags |= mfChecked
		}
		if it.Disabled {
			flags |= mfGrayed
		}
		procAppendMenuW.Call(menu, flags, uintptr(i+1), uintptr(unsafe.Pointer(u16(it.Label))))
	}
	var pt point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	// The menu only closes on a click elsewhere if its owner is in front.
	procSetForegroundWindow.Call(t.hwnd)
	cmd, _, _ := procTrackPopupMenu.Call(menu, tpmRightButton|tpmReturnCmd|tpmBottomAlign, uintptr(pt.X), uintptr(pt.Y), 0, t.hwnd, 0)
	procPostMessageW.Call(t.hwnd, wmNull, 0, 0)
	if i := int(cmd) - 1; i >= 0 && i < len(items) && items[i].OnClick != nil {
		items[i].OnClick()
	}
}
