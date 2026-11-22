package coreaudio

import "syscall"

func callN(fn uintptr, args ...uintptr) (uintptr, uintptr, error) {
	r1, r2, err := syscall.SyscallN(fn, args...)
	return r1, r2, err
}
