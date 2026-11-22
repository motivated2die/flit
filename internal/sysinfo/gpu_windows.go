package sysinfo

import (
	"errors"
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// GPUMeter reads how busy the graphics processor is for one process, from
// Windows' "GPU Engine" performance counters (what Task Manager shows):
// each engine type's utilisation summed over the process's instances,
// the busiest type counting. The first reading only primes the counters
// (they are rates); keep the meter between readings and Close it after.
type GPUMeter struct {
	query, counter uintptr
	primed         bool
}

var (
	pdh                          = windows.NewLazySystemDLL("pdh.dll")
	procPdhOpenQuery             = pdh.NewProc("PdhOpenQueryW")
	procPdhAddEnglishCounter     = pdh.NewProc("PdhAddEnglishCounterW")
	procPdhCollectQueryData      = pdh.NewProc("PdhCollectQueryData")
	procPdhGetFormattedCounterAr = pdh.NewProc("PdhGetFormattedCounterArrayW")
	procPdhCloseQuery            = pdh.NewProc("PdhCloseQuery")
)

const (
	pdhFmtDouble  = 0x00000200
	pdhFmtNoCap   = 0x00008000
	pdhMoreData   = 0x800007D2
	pdhCstatusNew = 0x00000001 // PDH_CSTATUS_NEW_DATA
)

// pdhFmtCounterValueItem is PDH_FMT_COUNTERVALUE_ITEM_W with a double.
type pdhFmtCounterValueItem struct {
	Name   *uint16
	Status uint32
	_      uint32
	Value  float64
}

// ErrNotReady means the meter was just primed: read again in a moment.
var ErrNotReady = errors.New("gpu meter: primed, read again")

// Read returns pid's GPU use (0..1). The first call primes the counters
// and returns ErrNotReady.
func (m *GPUMeter) Read(pid uint32) (float64, error) {
	if m.query == 0 {
		if r, _, _ := procPdhOpenQuery.Call(0, 0, uintptr(unsafe.Pointer(&m.query))); r != 0 {
			return 0, fmt.Errorf("PdhOpenQuery: %#x", r)
		}
		path, _ := windows.UTF16PtrFromString(`\GPU Engine(*)\Utilization Percentage`)
		if r, _, _ := procPdhAddEnglishCounter.Call(m.query, uintptr(unsafe.Pointer(path)), 0, uintptr(unsafe.Pointer(&m.counter))); r != 0 {
			m.Close()
			return 0, fmt.Errorf("PdhAddEnglishCounter: %#x", r)
		}
	}
	if r, _, _ := procPdhCollectQueryData.Call(m.query); r != 0 {
		return 0, fmt.Errorf("PdhCollectQueryData: %#x", r)
	}
	if !m.primed {
		m.primed = true
		return 0, ErrNotReady
	}
	var size, count uint32
	r, _, _ := procPdhGetFormattedCounterAr.Call(m.counter, pdhFmtDouble|pdhFmtNoCap, uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&count)), 0)
	if r != pdhMoreData {
		if r == 0 {
			return 0, nil // no instances
		}
		return 0, fmt.Errorf("PdhGetFormattedCounterArray: %#x", r)
	}
	buf := make([]byte, size)
	r, _, _ = procPdhGetFormattedCounterAr.Call(m.counter, pdhFmtDouble|pdhFmtNoCap, uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&count)), uintptr(unsafe.Pointer(&buf[0])))
	if r != 0 {
		return 0, fmt.Errorf("PdhGetFormattedCounterArray: %#x", r)
	}
	items := unsafe.Slice((*pdhFmtCounterValueItem)(unsafe.Pointer(&buf[0])), count)
	byType := map[string]float64{}
	prefix := fmt.Sprintf("pid_%d_", pid)
	for _, it := range items {
		if it.Status > pdhCstatusNew { // not valid data
			continue
		}
		name := windows.UTF16PtrToString(it.Name)
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		byType[engineType(name)] += it.Value
	}
	return busiest(byType), nil
}

// engineType is the "engtype_…" part of an instance name, such as
// "pid_1234_luid_0x0_0x1_phys_0_eng_0_engtype_Compute 0".
func engineType(name string) string {
	if i := strings.Index(name, "engtype_"); i >= 0 {
		return name[i+len("engtype_"):]
	}
	return name
}

// busiest is the highest of the engine types' utilisations, 0..1.
func busiest(byType map[string]float64) float64 {
	m := 0.0
	for _, v := range byType {
		m = max(m, v)
	}
	return min(1, m/100)
}

// Close releases the counters.
func (m *GPUMeter) Close() {
	if m.query != 0 {
		procPdhCloseQuery.Call(m.query)
	}
	*m = GPUMeter{}
}
