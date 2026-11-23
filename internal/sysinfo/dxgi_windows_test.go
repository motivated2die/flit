package sysinfo

import (
	"testing"
	"unsafe"
)

func TestDXGIDescSize(t *testing.T) {
	if got := unsafe.Sizeof(dxgiAdapterDesc1{}); got != 312 {
		t.Fatalf("dxgiAdapterDesc1 is %d bytes, want 312 (Windows x64 DXGI_ADAPTER_DESC1)", got)
	}
}

func TestEvaluateGPU(t *testing.T) {
	dgpu := GPUAdapter{Name: "AMD Radeon RX 7800 XT", VendorID: 0x1002, DedicatedVRAM: 16 << 30, SharedRAM: 16 << 30}
	igpu := GPUAdapter{Name: "AMD Radeon 780M", VendorID: 0x1002, DedicatedVRAM: 512 << 20, SharedRAM: 8 << 30, Integrated: true}
	tiny := GPUAdapter{Name: "Old Adapter", VendorID: 0x8086, DedicatedVRAM: 128 << 20, SharedRAM: 512 << 20, Integrated: true}

	if g := evaluateGPU(nil, true, 32<<30); g.Ready || g.RecommendedTranscribeGPU() != "off" {
		t.Errorf("no adapters: %+v", g)
	}
	if g := evaluateGPU([]GPUAdapter{dgpu}, false, 32<<30); g.Ready || g.RecommendedTranscribeGPU() != "off" {
		t.Errorf("no vulkan: %+v", g)
	}
	if g := evaluateGPU([]GPUAdapter{igpu, dgpu}, true, 32<<30); !g.Ready || g.Best.Name != dgpu.Name || g.RecommendedTranscribeGPU() != "both" {
		t.Errorf("hybrid dgpu+igpu: %+v", g)
	}
	if g := evaluateGPU([]GPUAdapter{igpu}, true, 16<<30); !g.Ready || g.Best.Name != igpu.Name || g.RecommendedTranscribeGPU() != "final" {
		t.Errorf("16 GB iGPU laptop: %+v", g)
	}
	if g := evaluateGPU([]GPUAdapter{igpu}, true, 4<<30); g.Ready || g.RecommendedTranscribeGPU() != "off" {
		t.Errorf("4 GB iGPU laptop shouldn't be ready: %+v", g)
	}
	if g := evaluateGPU([]GPUAdapter{tiny}, true, 16<<30); g.Ready || g.RecommendedTranscribeGPU() != "off" {
		t.Errorf("tiny adapter: %+v", g)
	}
}

func TestDetectGPU(t *testing.T) {
	g := DetectGPU()
	t.Logf("DetectGPU: ready=%v rec=%s vulkan=%v ram=%s best=%q (%s) adapters=%d reason=%q",
		g.Ready, g.RecommendedTranscribeGPU(), g.Vulkan, Bytes(g.SystemRAM), g.Best.Name, g.Summary(), len(g.Adapters), g.Reason)
}
