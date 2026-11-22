package ui

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procRegisterClassExW       = user32.NewProc("RegisterClassExW")
	procCreateWindowExW        = user32.NewProc("CreateWindowExW")
	procDefWindowProcW         = user32.NewProc("DefWindowProcW")
	procDestroyWindow          = user32.NewProc("DestroyWindow")
	procGetMessageW            = user32.NewProc("GetMessageW")
	procTranslateMessage       = user32.NewProc("TranslateMessage")
	procDispatchMessageW       = user32.NewProc("DispatchMessageW")
	procPostMessageW           = user32.NewProc("PostMessageW")
	procPostQuitMessage        = user32.NewProc("PostQuitMessage")
	procRegisterWindowMessageW = user32.NewProc("RegisterWindowMessageW")
	procSetForegroundWindow    = user32.NewProc("SetForegroundWindow")
	procGetCursorPos           = user32.NewProc("GetCursorPos")
	procCreatePopupMenu        = user32.NewProc("CreatePopupMenu")
	procAppendMenuW            = user32.NewProc("AppendMenuW")
	procTrackPopupMenu         = user32.NewProc("TrackPopupMenu")
	procDestroyMenu            = user32.NewProc("DestroyMenu")
	procGetSystemMetrics       = user32.NewProc("GetSystemMetrics")
	procCreateIconIndirect     = user32.NewProc("CreateIconIndirect")
	procDestroyIcon            = user32.NewProc("DestroyIcon")
	procMessageBoxW            = user32.NewProc("MessageBoxW")
	procSetProcessDpiAwareCtx  = user32.NewProc("SetProcessDpiAwarenessContext")
	procCreateDIBSection       = gdi32.NewProc("CreateDIBSection")
	procCreateBitmap           = gdi32.NewProc("CreateBitmap")
	procDeleteObject           = gdi32.NewProc("DeleteObject")
	procShellNotifyIconW       = shell32.NewProc("Shell_NotifyIconW")
	procGetModuleHandleW       = kernel32.NewProc("GetModuleHandleW")
)

const (
	wmNull        = 0x0000
	wmEndSession  = 0x0016
	wmContextMenu = 0x007B
	wmCommand     = 0x0111
	wmLButtonUp   = 0x0202
	wmRButtonUp   = 0x0205
	wmApp         = 0x8000

	smCxSmIcon = 49

	mfString    = 0x0000
	mfGrayed    = 0x0001
	mfChecked   = 0x0008
	mfSeparator = 0x0800

	tpmRightButton = 0x0002
	tpmReturnCmd   = 0x0100
	tpmBottomAlign = 0x0020

	nimAdd     = 0
	nimModify  = 1
	nimDelete  = 2
	nifMessage = 0x01
	nifIcon    = 0x02
	nifTip     = 0x04
	nifInfo    = 0x10

	niifNone             = 0x00
	niifRespectQuietTime = 0x80
	ninBalloonUserClick  = 0x0405 // WM_USER + 5

	mbOK          = 0x0000
	mbYesNo       = 0x0004
	mbIconInfo    = 0x0040
	mbIconWarning = 0x0030
	mbIconError   = 0x0010
	mbDefButton2  = 0x0100
	idYes         = 6
)

type point struct{ X, Y int32 }

type msg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
	_       uint32
}

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

type notifyIconData struct {
	Size             uint32
	Wnd              uintptr
	ID               uint32
	Flags            uint32
	CallbackMessage  uint32
	Icon             uintptr
	Tip              [128]uint16
	State            uint32
	StateMask        uint32
	Info             [256]uint16
	TimeoutOrVersion uint32
	InfoTitle        [64]uint16
	InfoFlags        uint32
	GUIDItem         windows.GUID
	BalloonIcon      uintptr
}

func u16(s string) *uint16 {
	p, err := windows.UTF16PtrFromString(s)
	if err != nil {
		p, _ = windows.UTF16PtrFromString("?")
	}
	return p
}

func moduleHandle() uintptr {
	h, _, _ := procGetModuleHandleW.Call(0)
	return h
}

func defWindowProc(hwnd uintptr, m uint32, w, l uintptr) uintptr {
	r, _, _ := procDefWindowProcW.Call(hwnd, uintptr(m), w, l)
	return r
}

// InitDPIAwareness opts into per-monitor v2 DPI awareness (the manifest
// does too; this covers builds without resources).
func InitDPIAwareness() {
	if procSetProcessDpiAwareCtx.Find() == nil {
		procSetProcessDpiAwareCtx.Call(^uintptr(3)) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2
	}
}

// MessageBox shows a plain Windows message box and reports whether the
// user chose Yes (for yesNo boxes).
func MessageBox(title, text string, yesNo, warning bool) bool {
	flags := uintptr(mbOK | mbIconInfo)
	if warning {
		flags = mbOK | mbIconWarning
	}
	if yesNo {
		flags = flags&^mbOK | mbYesNo | mbDefButton2
	}
	r, _, _ := procMessageBoxW.Call(0, uintptr(unsafe.Pointer(u16(text))), uintptr(unsafe.Pointer(u16(title))), flags)
	return r == idYes
}

// ShowError shows an error message box.
func ShowError(title, text string) {
	procMessageBoxW.Call(0, uintptr(unsafe.Pointer(u16(text))), uintptr(unsafe.Pointer(u16(title))), mbOK|mbIconError)
}

// allowDarkMenus asks Windows to draw this process's native menus dark
// when the system uses dark apps, through uxtheme's unnamed exports
// (SetPreferredAppMode = 135, FlushMenuThemes = 136; Windows 10 1903+).
// Where they don't exist the menus simply stay light.
func allowDarkMenus() {
	h, err := windows.LoadLibrary("uxtheme.dll")
	if err != nil {
		return
	}
	if p, err := windows.GetProcAddressByOrdinal(h, 135); err == nil {
		const allowDark = 1
		syscallN(p, allowDark)
	}
	if p, err := windows.GetProcAddressByOrdinal(h, 136); err == nil {
		syscallN(p)
	}
}
