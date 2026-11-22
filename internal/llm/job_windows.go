package llm

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// jobProcess puts the server into a job object that Windows kills with
// flit: when flit's handle to the job closes (flit quits or crashes), the
// server ends too, and its gigabytes of memory with it. With a percent, the
// job also caps the server's CPU (hard, of all processors); background
// lowers its priority below normal. The returned function closes the job
// (and so ends the server, if it still runs).
func jobProcess(pid, percent int, background bool) (func(), error) {
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
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return func() {}, err
	}
	release := func() { windows.CloseHandle(job) }
	var ext windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	ext.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&ext)), uint32(unsafe.Sizeof(ext))); err != nil {
		release()
		return func() {}, fmt.Errorf("job: %w", err)
	}
	if percent > 0 && percent < 100 {
		info := cpuRateControl{ControlFlags: cpuRateEnable | cpuRateHardCap, CPURate: uint32(percent * 100)}
		if _, err := windows.SetInformationJobObject(job, jobObjectCPURateControlInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
			release()
			return func() {}, fmt.Errorf("CPU limit: %w", err)
		}
	}
	if err := windows.AssignProcessToJobObject(job, h); err != nil {
		release()
		return func() {}, fmt.Errorf("job: %w", err)
	}
	return release, nil
}

// JOBOBJECT_CPU_RATE_CONTROL_INFORMATION (CpuRate in hundredths of a
// percent of all processors), as in internal/engine.
type cpuRateControl struct {
	ControlFlags uint32
	CPURate      uint32
}

const (
	jobObjectCPURateControlInformation = 15
	cpuRateEnable                      = 0x1
	cpuRateHardCap                     = 0x4
)
