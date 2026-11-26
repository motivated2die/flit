package ui

import "syscall"

//go:uintptrescapes
func syscallN(p uintptr, args ...uintptr) uintptr {
	r, _, _ := syscall.SyscallN(p, args...)
	return r
}
