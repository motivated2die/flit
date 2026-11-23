package ui

import (
	"errors"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procOpenClipboard    = user32.NewProc("OpenClipboard")
	procCloseClipboard   = user32.NewProc("CloseClipboard")
	procEmptyClipboard   = user32.NewProc("EmptyClipboard")
	procSetClipboardData = user32.NewProc("SetClipboardData")
	procGlobalAlloc      = kernel32.NewProc("GlobalAlloc")
	procGlobalLock       = kernel32.NewProc("GlobalLock")
	procGlobalUnlock     = kernel32.NewProc("GlobalUnlock")
	procGlobalFree       = kernel32.NewProc("GlobalFree")
)

// SetClipboard puts text on the clipboard (CF_UNICODETEXT). Another app
// holding the clipboard is waited for briefly.
func SetClipboard(owner uintptr, text string) error {
	const cfUnicodeText, gmemMoveable = 13, 0x0002
	u, err := windows.UTF16FromString(text)
	if err != nil {
		return err
	}
	opened := false
	for i := 0; i < 10; i++ {
		if r, _, _ := procOpenClipboard.Call(owner); r != 0 {
			opened = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !opened {
		return errors.New("the clipboard is busy")
	}
	defer procCloseClipboard.Call()
	procEmptyClipboard.Call()
	h, _, _ := procGlobalAlloc.Call(gmemMoveable, uintptr(len(u)*2))
	if h == 0 {
		return errors.New("GlobalAlloc failed")
	}
	p, _, _ := procGlobalLock.Call(h)
	if p == 0 {
		procGlobalFree.Call(h)
		return errors.New("GlobalLock failed")
	}
	copy(unsafe.Slice((*uint16)(*(*unsafe.Pointer)(unsafe.Pointer(&p))), len(u)), u) // memory Windows allocated
	procGlobalUnlock.Call(h)
	if r, _, _ := procSetClipboardData.Call(cfUnicodeText, h); r == 0 {
		procGlobalFree.Call(h)
		return errors.New("SetClipboardData failed")
	}
	return nil // the clipboard owns h now
}

var procGetClipboardData = user32.NewProc("GetClipboardData")

// ClipboardText reads text from the clipboard ("" if there is none).
func ClipboardText(owner uintptr) string {
	const cfUnicodeText = 13
	if r, _, _ := procOpenClipboard.Call(owner); r == 0 {
		return ""
	}
	defer procCloseClipboard.Call()
	h, _, _ := procGetClipboardData.Call(cfUnicodeText)
	if h == 0 {
		return ""
	}
	p, _, _ := procGlobalLock.Call(h)
	if p == 0 {
		return ""
	}
	defer procGlobalUnlock.Call(h)
	return windows.UTF16PtrToString((*uint16)(*(*unsafe.Pointer)(unsafe.Pointer(&p)))) // memory Windows owns
}

var procRegisterClipboardFormatW = user32.NewProc("RegisterClipboardFormatW")

// SetClipboardRich puts text on the clipboard as Unicode text and, when
// fragment isn't empty, also as HTML ("HTML Format"), so apps that take
// formatting (Outlook, OneNote, Word) paste headings and lists.
func SetClipboardRich(owner uintptr, text, fragment string) error {
	if fragment == "" {
		return SetClipboard(owner, text)
	}
	if err := SetClipboard(owner, text); err != nil {
		return err
	}
	name, _ := windows.UTF16PtrFromString("HTML Format")
	cf, _, _ := procRegisterClipboardFormatW.Call(uintptr(unsafe.Pointer(name)))
	if cf == 0 {
		return nil // the text is there
	}
	if r, _, _ := procOpenClipboard.Call(owner); r == 0 {
		return nil
	}
	defer procCloseClipboard.Call()
	b := []byte(cfHTML(fragment))
	h, _, _ := procGlobalAlloc.Call(0x0002, uintptr(len(b)+1))
	if h == 0 {
		return nil
	}
	p, _, _ := procGlobalLock.Call(h)
	if p == 0 {
		procGlobalFree.Call(h)
		return nil
	}
	copy(unsafe.Slice((*byte)(*(*unsafe.Pointer)(unsafe.Pointer(&p))), len(b)+1), append(b, 0))
	procGlobalUnlock.Call(h)
	if r, _, _ := procSetClipboardData.Call(cf, h); r == 0 {
		procGlobalFree.Call(h)
	}
	return nil
}
