package engine

import (
	"os/exec"
	"syscall"
)

// HideWindow keeps a console window from flashing up for the worker when
// flit runs without a console.
func HideWindow(cmd *exec.Cmd) {
	const createNoWindow = 0x08000000
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
