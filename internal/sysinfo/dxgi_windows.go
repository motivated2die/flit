package sysinfo

import (
	"fmt"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// GPUAdapter is one hardware graphics adapter reported by DXGI.
type GPUAdapter struct {
	Name          string
	VendorID      uint32
	DedicatedVRAM uint64
	SharedRAM     uint64
	Integrated    bool // true when dedicated VRAM is under 1 GiB (typical iGPU carve-out)
}

// GPUInfo is what this PC's graphics hardware can support for Vulkan inference.
type GPUInfo struct {
	Adapters  []GPUAdapter
	Best      GPUAdapter
	Vulkan    bool   // vulkan-1.dll is present in System32
	SystemRAM uint64 // physical RAM in bytes
	Ready     bool   // hardware + Vulkan + enough memory for speech models
	Reason    string // why Ready is false ("" when Ready)
}

// Minimum memory for Vulkan speech transcription (Parakeet Ultra Q8_0 is
// ~706 MiB plus ~300–500 MiB compute scratch; Sortformer adds ~139 MiB).
const (
	minDedicatedVRAM = 1536 << 20 // 1.5 GiB discrete VRAM
	minSharedGPU     = 2 << 30    // 2 GiB usable GPU memory on shared/integrated
	minSystemRAMiGPU = 8 << 30    // 8 GiB system RAM when GPU shares memory
	liveGPUVRAM      = 2 << 30    // 2 GiB dedicated VRAM to recommend GPU during calls too
)

// RecommendedTranscribeGPU suggests the default transcription GPU mode for
// this hardware: "both" (live + after meetings) on a discrete GPU with at
// least 2 GiB of dedicated VRAM, "final" (after meetings only, avoiding
// call-video contention) on an integrated or smaller GPU, and "off" when
// the PC is not ready for Vulkan.
func (g GPUInfo) RecommendedTranscribeGPU() string {
	if !g.Ready {
		return "off"
	}
	if !g.Best.Integrated && g.Best.DedicatedVRAM >= liveGPUVRAM {
		return "both"
	}
	return "final"
}

// Summary describes the detected graphics hardware in a short phrase for
// Welcome, Settings and Debug.
func (g GPUInfo) Summary() string {
	if g.Best.Name == "" {
		return "None"
	}
	if !g.Best.Integrated && g.Best.DedicatedVRAM > 0 {
		return fmt.Sprintf("%s (%s VRAM)", g.Best.Name, Bytes(g.Best.DedicatedVRAM))
	}
	return g.Best.Name + " (integrated)"
}

// DetectGPU inspects Windows' DXGI hardware adapters, checks for the Vulkan
// loader (vulkan-1.dll), and evaluates whether the PC is ready for Vulkan
// transcription. Safe to call at start-up (no Vulkan device is created, so
// broken GPU drivers or overlay layers cannot crash the tray process).
func DetectGPU() GPUInfo {
	return evaluateGPU(enumDXGIAdapters(), hasVulkanLoader(), totalPhysicalRAM())
}

func evaluateGPU(adapters []GPUAdapter, vulkan bool, sysRAM uint64) GPUInfo {
	g := GPUInfo{Adapters: adapters, Vulkan: vulkan, SystemRAM: sysRAM}
	for _, a := range adapters {
		if betterAdapter(a, g.Best) {
			g.Best = a
		}
	}
	switch {
	case len(adapters) == 0:
		g.Reason = "no hardware graphics processor found"
	case !vulkan:
		g.Reason = "no Vulkan graphics driver found"
	case !g.Best.Integrated && g.Best.DedicatedVRAM >= minDedicatedVRAM:
		g.Ready = true
	case g.Best.DedicatedVRAM+g.Best.SharedRAM >= minSharedGPU && (sysRAM == 0 || sysRAM >= minSystemRAMiGPU):
		g.Ready = true
	default:
		g.Reason = "not enough graphics memory"
	}
	return g
}

func betterAdapter(a, b GPUAdapter) bool {
	if b.Name == "" {
		return a.Name != ""
	}
	if a.Integrated != b.Integrated {
		return !a.Integrated // prefer discrete over integrated
	}
	if a.DedicatedVRAM != b.DedicatedVRAM {
		return a.DedicatedVRAM > b.DedicatedVRAM
	}
	return a.SharedRAM > b.SharedRAM
}

var (
	dxgiDLL                  = windows.NewLazySystemDLL("dxgi.dll")
	procCreateDXGIFactory1   = dxgiDLL.NewProc("CreateDXGIFactory1")
	procGlobalMemoryStatusEx = windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")
)

// IID_IDXGIFactory1 {770aae78-f26f-4dba-a829-253c83d1b387}.
var iidIDXGIFactory1 = windows.GUID{
	Data1: 0x770aae78,
	Data2: 0xf26f,
	Data3: 0x4dba,
	Data4: [8]byte{0xa8, 0x29, 0x25, 0x3c, 0x83, 0xd1, 0xb3, 0x87},
}

const (
	dxgiErrorNotFound       = 0x887A0002
	dxgiAdapterFlagSoftware = 2
)

// dxgiAdapterDesc1 mirrors DXGI_ADAPTER_DESC1 on Windows x64 (312 bytes).
type dxgiAdapterDesc1 struct {
	Description           [128]uint16
	VendorID              uint32
	DeviceID              uint32
	SubSysID              uint32
	Revision              uint32
	DedicatedVideoMemory  uintptr
	DedicatedSystemMemory uintptr
	SharedSystemMemory    uintptr
	AdapterLuidLow        uint32
	AdapterLuidHigh       int32
	Flags                 uint32
	_                     uint32
}

// cptr turns a COM pointer returned by DXGI into an unsafe.Pointer (the
// memory is COM-owned, never Go's heap, so vet's uintptr warning doesn't
// apply).
func cptr(p uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&p)) }

func vtblProc(obj uintptr, index uintptr) uintptr {
	vtbl := *(*uintptr)(cptr(obj))
	return *(*uintptr)(cptr(vtbl + index*unsafe.Sizeof(uintptr(0))))
}

func enumDXGIAdapters() []GPUAdapter {
	if err := procCreateDXGIFactory1.Find(); err != nil {
		return nil
	}
	var factory uintptr
	hr, _, _ := procCreateDXGIFactory1.Call(uintptr(unsafe.Pointer(&iidIDXGIFactory1)), uintptr(unsafe.Pointer(&factory)))
	if int32(hr) < 0 || factory == 0 {
		return nil
	}
	defer syscall.SyscallN(vtblProc(factory, 2), factory) // IUnknown::Release

	var out []GPUAdapter
	enumProc := vtblProc(factory, 12) // IDXGIFactory1::EnumAdapters1
	for i := uintptr(0); ; i++ {
		var adapter uintptr
		hr, _, _ = syscall.SyscallN(enumProc, factory, i, uintptr(unsafe.Pointer(&adapter)))
		if uint32(hr) == dxgiErrorNotFound || int32(hr) < 0 || adapter == 0 {
			break
		}
		var desc dxgiAdapterDesc1
		dhr, _, _ := syscall.SyscallN(vtblProc(adapter, 10), adapter, uintptr(unsafe.Pointer(&desc))) // IDXGIAdapter1::GetDesc1
		syscall.SyscallN(vtblProc(adapter, 2), adapter)                                               // IUnknown::Release
		if int32(dhr) < 0 {
			continue
		}
		if desc.Flags&dxgiAdapterFlagSoftware != 0 {
			continue // skip Microsoft Basic Render Driver
		}
		name := strings.Join(strings.Fields(windows.UTF16ToString(desc.Description[:])), " ")
		if name == "" {
			continue
		}
		vram := uint64(desc.DedicatedVideoMemory)
		out = append(out, GPUAdapter{
			Name:          name,
			VendorID:      desc.VendorID,
			DedicatedVRAM: vram,
			SharedRAM:     uint64(desc.SharedSystemMemory),
			Integrated:    vram < 1<<30,
		})
	}
	return out
}

func hasVulkanLoader() bool {
	h, err := windows.LoadLibraryEx("vulkan-1.dll", 0, windows.LOAD_LIBRARY_SEARCH_SYSTEM32)
	if err != nil {
		return false
	}
	windows.FreeLibrary(h)
	return true
}

type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

func totalPhysicalRAM() uint64 {
	var m memoryStatusEx
	m.Length = uint32(unsafe.Sizeof(m))
	if r, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&m))); r == 0 {
		return 0
	}
	return m.TotalPhys
}
