package engine

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// LimitProcess caps a worker's CPU use: a Windows job object with a hard
// CPU rate (percent of all processors; the OS enforces it, so the load
// never rises above it, however busy the engine is) and below-normal
// priority (the meeting app and everything in front of the user come
// first). The returned function releases the job; call it once the
// process has ended.
func LimitProcess(pid, percent int, background bool) (func(), error) {
	access := uint32(windows.PROCESS_SET_QUOTA | windows.PROCESS_TERMINATE | windows.PROCESS_SET_INFORMATION | windows.PROCESS_QUERY_LIMITED_INFORMATION)
	h, err := windows.OpenProcess(access, false, uint32(pid))
	if err != nil {
		return func() {}, err
	}
	defer windows.CloseHandle(h)
	if background {
		if err := windows.SetPriorityClass(h, windows.BELOW_NORMAL_PRIORITY_CLASS); err != nil {
			return func() {}, fmt.Errorf("priority: %w", err)
		}
	}
	if percent <= 0 || percent >= 100 {
		return func() {}, nil
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return func() {}, err
	}
	release := func() { windows.CloseHandle(job) }
	info := cpuRateControl{ControlFlags: cpuRateEnable | cpuRateHardCap, CPURate: uint32(percent * 100)}
	if _, err := windows.SetInformationJobObject(job, jobObjectCPURateControlInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		release()
		return func() {}, fmt.Errorf("CPU limit: %w", err)
	}
	if err := windows.AssignProcessToJobObject(job, h); err != nil {
		release()
		return func() {}, fmt.Errorf("CPU limit: %w", err)
	}
	return release, nil
}

// JOBOBJECT_CPU_RATE_CONTROL_INFORMATION (CpuRate in hundredths of a
// percent of all processors).
type cpuRateControl struct {
	ControlFlags uint32
	CPURate      uint32
}

const (
	jobObjectCPURateControlInformation = 15
	cpuRateEnable                      = 0x1
	cpuRateHardCap                     = 0x4
)
