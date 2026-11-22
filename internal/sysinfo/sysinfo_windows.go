package sysinfo

import (
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var procGetProcessMemoryInfo = windows.NewLazySystemDLL("kernel32.dll").NewProc("K32GetProcessMemoryInfo")

// processMemoryCountersEx mirrors PROCESS_MEMORY_COUNTERS_EX.
type processMemoryCountersEx struct {
	Cb                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
	PrivateUsage               uintptr
}

// Process measures the process pid (0 = this one).
func Process(pid uint32) (Stats, error) {
	if pid == 0 {
		pid = windows.GetCurrentProcessId()
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return Stats{}, err
	}
	defer windows.CloseHandle(h)
	var st Stats
	var c processMemoryCountersEx
	c.Cb = uint32(unsafe.Sizeof(c))
	if r, _, err := procGetProcessMemoryInfo.Call(uintptr(h), uintptr(unsafe.Pointer(&c)), uintptr(c.Cb)); r == 0 {
		return st, err
	}
	st.WorkingSet, st.Private = uint64(c.WorkingSetSize), uint64(c.PrivateUsage)
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &creation, &exit, &kernel, &user); err == nil {
		ft := func(f windows.Filetime) time.Duration {
			return time.Duration(uint64(f.HighDateTime)<<32|uint64(f.LowDateTime)) * 100
		}
		st.CPU = ft(kernel) + ft(user)
	}
	return st, nil
}

// systemThreadInformation mirrors SYSTEM_THREAD_INFORMATION (x64), which
// follows each process's SYSTEM_PROCESS_INFORMATION.
type systemThreadInformation struct {
	KernelTime      int64
	UserTime        int64
	CreateTime      int64
	WaitTime        uint32
	_               uint32
	StartAddress    uintptr
	UniqueProcess   uintptr
	UniqueThread    uintptr
	Priority        int32
	BasePriority    int32
	ContextSwitches uint32
	ThreadState     uint32
	WaitReason      uint32
	_               uint32
}

// fileTime turns a FILETIME count (100 ns since 1601) into a time.
func fileTime(ft int64) time.Time {
	return time.Unix(0, (ft-116444736000000000)*100)
}

var (
	threadsMu  sync.Mutex
	threadsBuf []byte // kept between readings: the system's process list
)

// ReadThreads returns the CPU time of every thread of the processes pids,
// and of the processes as a whole: one
// NtQuerySystemInformation call lists all processes with their threads'
// times (a Toolhelp snapshot and a GetThreadTimes per thread took about
// 60 ms; this takes a few).
func ReadThreads(pids ...uint32) (Threads, error) {
	threadsMu.Lock()
	defer threadsMu.Unlock()
	if len(threadsBuf) == 0 {
		threadsBuf = make([]byte, 512<<10)
	}
	for {
		var n uint32
		err := windows.NtQuerySystemInformation(windows.SystemProcessInformation, unsafe.Pointer(&threadsBuf[0]), uint32(len(threadsBuf)), &n)
		if err == nil {
			break
		}
		if err != windows.STATUS_INFO_LENGTH_MISMATCH || len(threadsBuf) >= 64<<20 {
			return Threads{}, err
		}
		threadsBuf = make([]byte, max(int(n)+64<<10, 2*len(threadsBuf)))
	}
	want := map[uintptr]bool{}
	for _, p := range pids {
		want[uintptr(p)] = true
	}
	var out Threads
	for off := 0; ; {
		p := (*windows.SYSTEM_PROCESS_INFORMATION)(unsafe.Pointer(&threadsBuf[off]))
		if want[p.UniqueProcessID] {
			out.CPU += time.Duration(p.KernelTime+p.UserTime) * 100
			first := off + int(unsafe.Sizeof(*p))
			for i := range int(p.NumberOfThreads) {
				th := (*systemThreadInformation)(unsafe.Pointer(&threadsBuf[first+i*int(unsafe.Sizeof(systemThreadInformation{}))]))
				out.List = append(out.List, ThreadTime{ID: uint32(th.UniqueThread), CPU: time.Duration(th.KernelTime+th.UserTime) * 100,
					Started: fileTime(th.CreateTime)})
			}
		}
		if p.NextEntryOffset == 0 {
			break
		}
		off += int(p.NextEntryOffset)
	}
	return out, nil
}
