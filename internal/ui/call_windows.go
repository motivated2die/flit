package ui

import "syscall"

func syscallN(p uintptr, args ...uintptr) uintptr {
	r, _, _ := syscall.SyscallN(p, args...)
	return r
}
