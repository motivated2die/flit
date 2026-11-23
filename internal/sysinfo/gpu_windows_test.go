package sysinfo

import (
	"errors"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestEngineTypes(t *testing.T) {
	by := map[string]float64{}
	for name, v := range map[string]float64{
		"pid_7_luid_0x0_0x1_phys_0_eng_0_engtype_3D":        30,
		"pid_7_luid_0x0_0x1_phys_0_eng_1_engtype_3D":        25,
		"pid_7_luid_0x0_0x1_phys_0_eng_5_engtype_Compute 0": 40,
	} {
		by[engineType(name)] += v
	}
	if got := busiest(by); got < 0.549 || got > 0.551 {
		t.Errorf("busiest %.3f, want 0.55 (3D, summed)", got)
	}
	if busiest(map[string]float64{"Compute 0": 180}) != 1 {
		t.Error("not capped at 1")
	}
}

// The Desktop Window Manager draws the screen, so the counters have it
// (unless the PC has no GPU counters at all, as in some VMs).
func TestGPUMeterReadsAProcess(t *testing.T) {
	pid := processID(t, "dwm.exe")
	var m GPUMeter
	defer m.Close()
	if _, err := m.Read(pid); !errors.Is(err, ErrNotReady) {
		t.Skip("no GPU counters:", err)
	}
	time.Sleep(300 * time.Millisecond)
	v, err := m.Read(pid)
	if err != nil {
		t.Fatal(err)
	}
	if v < 0 || v > 1 {
		t.Errorf("use %.3f out of range", v)
	}
	t.Logf("dwm.exe GPU use %.1f%%", v*100)
}

func processID(t *testing.T, exe string) uint32 {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		t.Skip(err)
	}
	defer windows.CloseHandle(snap)
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		if windows.UTF16ToString(e.ExeFile[:]) == exe {
			return e.ProcessID
		}
	}
	t.Skip("no " + exe)
	return 0
}
