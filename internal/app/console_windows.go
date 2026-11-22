package app

import (
	"os"

	"golang.org/x/sys/windows"
)

// attachConsole connects a GUI build's output to the terminal it was
// started from, so console roles print there.
func attachConsole() {
	if _, err := os.Stdout.Stat(); err == nil {
		return // already has a console or a redirect
	}
	const attachParentProcess = ^uint32(0)
	k := windows.NewLazySystemDLL("kernel32.dll").NewProc("AttachConsole")
	if r, _, _ := k.Call(uintptr(attachParentProcess)); r == 0 {
		return
	}
	if f, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
		os.Stdout, os.Stderr = f, f
	}
}
