package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/motivated2die/flit/internal/config"
	"github.com/motivated2die/flit/internal/engine"
	"github.com/motivated2die/flit/internal/engine/native"
	"github.com/motivated2die/flit/internal/models"
	"github.com/motivated2die/flit/internal/sysinfo"
	"github.com/motivated2die/flit/internal/ui"
)

func sampleDiscreteGPU() sysinfo.GPUInfo {
	a := sysinfo.GPUAdapter{Name: "AMD Radeon RX 7800 XT", VendorID: 0x1002, DedicatedVRAM: 16 << 30, SharedRAM: 16 << 30}
	return sysinfo.GPUInfo{Adapters: []sysinfo.GPUAdapter{a}, Best: a, Vulkan: true, SystemRAM: 32 << 30, Ready: true}
}

func sampleIntegratedGPU() sysinfo.GPUInfo {
	a := sysinfo.GPUAdapter{Name: "AMD Radeon 780M", VendorID: 0x1002, DedicatedVRAM: 512 << 20, SharedRAM: 8 << 30, Integrated: true}
	return sysinfo.GPUInfo{Adapters: []sysinfo.GPUAdapter{a}, Best: a, Vulkan: true, SystemRAM: 16 << 30, Ready: true}
}

func TestWelcomeGPURecommendationAndDownloadCard(t *testing.T) {
	a := testApp(t)
	a.cfg.SetupComplete = false
	a.cfg.TranscribeGPU = ""
	a.gpu = sampleDiscreteGPU()

	if got := a.transcribeGPU(); got != config.GPUBoth {
		t.Fatalf("dGPU default %q, want %q", got, config.GPUBoth)
	}
	if !a.wantVulkan() || !a.gpuForFinal() || !a.gpuForLive() {
		t.Fatalf("expected GPU enabled for both")
	}

	// Welcome shows the GPU picker (pre-selected on "Live & after", so "Off" and "After" are clickable)
	// and includes the Vulkan download card.
	a.page = pageWelcome
	c, err := ui.LayoutFrame(a.frame(), 960, 2000, 1, "")
	if err != nil {
		t.Skip("no Direct2D:", err)
	}
	if _, ok := c.HitByID("welcome:gpu:Off"); !ok {
		t.Error("welcome:gpu:Off segment missing")
	}
	if _, ok := c.HitByID("welcome:gpu:After"); !ok {
		t.Error("welcome:gpu:After segment missing")
	}
	if _, ok := c.HitByID("welcome:gpu:Live & after"); ok {
		t.Error("welcome:gpu:Live & after should be selected (not an unselected click target)")
	}
	var hasVK bool
	for _, m := range a.neededModels() {
		if m.ID == vulkanEngineItem.ID {
			hasVK = true
		}
	}
	if !hasVK {
		t.Error("Vulkan graphics engine missing from neededModels while GPU enabled")
	}

	// Switching to Off removes the Vulkan download card.
	a.setTranscribeGPU(config.GPUOff)
	if a.wantVulkan() {
		t.Error("wantVulkan still true after Off")
	}
	for _, m := range a.neededModels() {
		if m.ID == vulkanEngineItem.ID {
			t.Error("Vulkan graphics engine still in neededModels when Off")
		}
	}

	// Integrated GPU recommends "final" (after meetings only).
	a.cfg.TranscribeGPU = ""
	a.gpu = sampleIntegratedGPU()
	if got := a.transcribeGPU(); got != config.GPUFinal || !a.gpuForFinal() || a.gpuForLive() {
		t.Fatalf("iGPU default %q (final=%v live=%v), want final", got, a.gpuForFinal(), a.gpuForLive())
	}

	// Unready PC clamps to Off and shows no broken picker.
	a.gpu = sysinfo.GPUInfo{Reason: "no Vulkan graphics driver found"}
	a.cfg.TranscribeGPU = config.GPUBoth
	if got := a.transcribeGPU(); got != config.GPUOff || a.wantVulkan() {
		t.Fatalf("unready PC didn't clamp to off: %q", got)
	}
}

func TestSettingsGPUIntegratesWithLiveTranscribe(t *testing.T) {
	a := testApp(t)
	a.gpu = sampleDiscreteGPU()
	a.cfg.TranscribeGPU = config.GPUBoth

	// Live ON, mode=Off: both "After" and "Live & after" are clickable unselected segments.
	a.cfg.LiveTranscribe = true
	a.cfg.TranscribeGPU = config.GPUOff
	a.page = pageSettings
	c, err := ui.LayoutFrame(a.frame(), 960, 2600, 1, "")
	if err != nil {
		t.Skip("no Direct2D:", err)
	}
	if _, ok := c.HitByID("set:transcribegpu:After"); !ok {
		t.Error("expected After segment when LiveTranscribe is on")
	}
	if _, ok := c.HitByID("set:transcribegpu:Live & after"); !ok {
		t.Error("expected 3rd segment (Live & after) when LiveTranscribe is on")
	}

	// Live OFF, mode=Off: simplifies to 2 segments (Off, After) — no "Live & after".
	a.cfg.LiveTranscribe = false
	a.cfg.TranscribeGPU = config.GPUBoth
	if a.gpuForLive() {
		t.Error("gpuForLive true while LiveTranscribe is off")
	}
	if !a.gpuForFinal() {
		t.Error("gpuForFinal should stay true when TranscribeGPU was both")
	}
	a.cfg.TranscribeGPU = config.GPUOff
	c, _ = ui.LayoutFrame(a.frame(), 960, 2600, 1, "")
	if _, ok := c.HitByID("set:transcribegpu:After"); !ok {
		t.Error("expected After segment when LiveTranscribe is off")
	}
	if _, ok := c.HitByID("set:transcribegpu:Live & after"); ok {
		t.Error("3rd segment should not appear when LiveTranscribe is off")
	}
}

func TestTranscriptionProgressAndGPUMeters(t *testing.T) {
	a := testApp(t)
	a.gpu = sampleDiscreteGPU()
	a.cfg.TranscribeGPU = config.GPUBoth
	s := a.sessions[0]
	a.working = s
	a.progress = 0.55

	// Simulate an active speech engine worker running on Vulkan.
	m, _ := models.Default(models.RoleASR, "de")
	a.dbg = debugState{
		at:     time.Now(),
		loaded: true,
		engine: engineState{
			ASR: m,
			PID: os.Getpid(),
			Info: engine.Info{
				Version:     "0.4.2",
				Backend:     "vulkan",
				DeviceName:  "Vulkan0",
				DeviceDesc:  "AMD Radeon RX 7800 XT",
				DeviceType:  "gpu",
				DiarBackend: "vulkan",
			},
		},
		engineCPU: 8.0,
		gpu:       0.72,
		gpuOK:     true,
	}
	a.dbg.engines = []engineState{a.dbg.engine}

	if !a.onGPU() {
		t.Fatal("onGPU false while speech worker runs on Vulkan")
	}
	if gpu, shown := a.debugGPU(); !shown || gpu < 0.71 || gpu > 0.73 {
		t.Fatalf("debugGPU (%v, %v), want (0.72, true)", gpu, shown)
	}
	// Sidebar debug line shows GPU% when GPU load exceeds CPU load.
	if lines := a.debugLines(); !strings.Contains(lines[1], "72% GPU") {
		t.Errorf("sidebar debug line %q, want 72%% GPU", lines[1])
	}
	// When CPU is busier than GPU, the sidebar switches back to CPU%.
	a.dbg.engineCPU, a.dbg.gpu = 32.0, 0.12
	if lines := a.debugLines(); !strings.Contains(lines[1], "32% CPU") {
		t.Errorf("sidebar debug line %q, want 32%% CPU", lines[1])
	}
	// Sub-0.5% GPU load doesn't flicker "0% GPU".
	a.dbg.engineCPU, a.dbg.gpu = 0.0, 0.003
	if lines := a.debugLines(); !strings.Contains(lines[1], "0% CPU") {
		t.Errorf("sidebar debug line %q, want 0%% CPU", lines[1])
	}
	a.dbg.engineCPU, a.dbg.gpu = 8.0, 0.72

	// Progress card says "on the graphics processor" and omits the CPU cap text.
	card := a.transcribeCard(s, "home:working", "Transcribing "+sessionTitle(s)).(ui.ProgressCard)
	if !strings.Contains(card.Detail, "graphics processor") || strings.Contains(card.Detail, "at most") {
		t.Errorf("GPU transcribe card detail %q", card.Detail)
	}

	// Also verify Debug page builds cleanly with the GPU worker & hardware row.
	a.page = pageDebug
	if _, err := ui.LayoutFrame(a.frame(), 960, 2400, 1, ""); err != nil {
		t.Skip("no Direct2D:", err)
	}

	// Once transcription stops (warm worker idle), onGPU is false and sidebar falls back to CPU.
	a.working = nil
	if a.onGPU() {
		t.Error("onGPU true while warm GPU worker is idle")
	}
	if lines := a.debugLines(); !strings.Contains(lines[1], "CPU") {
		t.Errorf("idle warm worker debug line %q, want CPU", lines[1])
	}
	a.working = s

	// When worker runs on CPU, no GPU square and CPU cap text returns.
	a.dbg.engine.Info.Backend = "cpu"
	a.dbg.engine.Info.DiarBackend = ""
	a.dbg.engines = []engineState{a.dbg.engine}
	if a.onGPU() {
		t.Error("onGPU true while speech worker runs on CPU")
	}
	if _, shown := a.debugGPU(); shown {
		t.Error("GPU square shown on CPU transcription")
	}
	card = a.transcribeCard(s, "home:working", "Transcribing "+sessionTitle(s)).(ui.ProgressCard)
	if !strings.Contains(card.Detail, "on the processor") || !strings.Contains(card.Detail, "at most") {
		t.Errorf("CPU transcribe card detail %q", card.Detail)
	}
}

func TestVulkanDownloadOnDiskCheck(t *testing.T) {
	a := testApp(t)
	if a.onDisk(vulkanEngineItem) {
		t.Fatal("vulkan reported onDisk before file exists")
	}
	p := native.VulkanPath(a.dataDir)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("dll"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !a.onDisk(vulkanEngineItem) {
		t.Fatal("vulkan not reported onDisk after writing ggml-vulkan.dll")
	}
}
