package ui

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

func mustLazy(name string) *windows.LazyDLL { return windows.NewLazySystemDLL(name) }

func windowsNewCallback(fn func(hwnd uintptr, m uint32, w, l uintptr) uintptr) uintptr {
	return windows.NewCallback(fn)
}

// cptrUI turns an address Windows handed over (a message's lParam pointing
// at Windows' own memory) into a pointer.
func cptrUI(p uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&p)) }
