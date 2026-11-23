package app

import (
	"fmt"

	"github.com/motivated2die/flit/internal/config"
	"github.com/motivated2die/flit/internal/engine/native"
	"github.com/motivated2die/flit/internal/logx"
	"github.com/motivated2die/flit/internal/models"
	"github.com/motivated2die/flit/internal/sysinfo"
	"github.com/motivated2die/flit/internal/ui"
)

// Graphics-assisted transcription: hardware check on start-up, Welcome
// recommendation, the 3-mode setting (Off / After / Live & after), and the
// on-demand 18 MB Vulkan backend download from the pinned transcribe.cpp
// bundle.

// vulkanEngineItem represents the optional ggml-vulkan.dll download in the
// same progress cards as the speech models.
var vulkanEngineItem = models.Model{
	ID:   "engine-vulkan",
	Name: "Graphics engine (Vulkan)",
	Size: native.BundleSize,
}

// initGPU probes DXGI adapters and vulkan-1.dll at start-up (safe: no
// Vulkan device is created in the tray process) and picks the hardware
// recommendation when the user hasn't chosen a mode yet.
func (a *App) initGPU() {
	a.gpu = sysinfo.DetectGPU()
	rec := a.gpu.RecommendedTranscribeGPU()
	if a.gpu.Ready {
		logx.Printf("graphics: %s (Vulkan ready; recommended transcription GPU: %s)", a.gpu.Summary(), rec)
	} else {
		logx.Printf("graphics: %s (not ready for Vulkan: %s)", a.gpu.Summary(), a.gpu.Reason)
	}
	if a.cfg.TranscribeGPU == "" {
		a.cfg.TranscribeGPU = rec
	}
}

// transcribeGPU is the effective GPU transcription mode ("off", "final",
// "both"), clamped to "off" when this PC isn't ready for Vulkan.
func (a *App) transcribeGPU() string {
	if !a.gpu.Ready {
		return config.GPUOff
	}
	switch a.cfg.TranscribeGPU {
	case config.GPUOff, config.GPUFinal, config.GPUBoth:
		return a.cfg.TranscribeGPU
	default:
		return a.gpu.RecommendedTranscribeGPU()
	}
}

// gpuForFinal reports whether post-meeting transcription and speaker
// diarization should use the graphics processor.
func (a *App) gpuForFinal() bool {
	m := a.transcribeGPU()
	return m == config.GPUFinal || m == config.GPUBoth
}

// gpuForLive reports whether live transcription during meetings should use
// the graphics processor.
func (a *App) gpuForLive() bool {
	return a.cfg.LiveTranscribe && a.transcribeGPU() == config.GPUBoth
}

// wantVulkan reports whether any transcription mode needs ggml-vulkan.dll.
func (a *App) wantVulkan() bool { return a.gpuForFinal() || a.gpuForLive() }

// applyGPU hands the GPU choice to the transcription jobs and kicks off the
// one-time Vulkan backend download when enabled.
func (a *App) applyGPU() {
	if a.jobs != nil {
		a.jobs.gpuFinal.Store(a.gpuForFinal())
		if a.wantVulkan() && !native.HasVulkan(a.dataDir) {
			a.jobs.prepare()
		}
	}
}

func (a *App) setTranscribeGPU(mode string) {
	a.cfg.TranscribeGPU = mode
	a.save()
	a.applyGPU()
	a.invalidate()
}

func gpuModeLabel(mode string) string {
	switch mode {
	case config.GPUBoth:
		return "Live & after"
	case config.GPUFinal:
		return "After"
	}
	return "Off"
}

// welcomeGPURow lets the user pick or adjust the hardware-recommended GPU
// runtime on the Welcome screen before downloads complete.
func (a *App) welcomeGPURow() ui.Node {
	if !a.gpu.Ready {
		reason := a.gpu.Reason
		if reason == "" {
			reason = "not available on this PC"
		}
		return ui.SettingRow{
			Label:  "Graphics processor",
			Detail: "Transcription runs on the processor (" + reason + ")",
		}
	}
	rec := a.gpu.RecommendedTranscribeGPU()
	detail := fmt.Sprintf("%s detected · recommended: %s", a.gpu.Summary(), gpuModeLabel(rec))
	if a.wantVulkan() && !native.HasVulkan(a.dataDir) {
		detail += " (downloads 18 MB once)"
	}
	modes := []string{config.GPUOff, config.GPUFinal, config.GPUBoth}
	labels := []string{"Off", "After", "Live & after"}
	cur := a.transcribeGPU()
	sel := 0
	for i, m := range modes {
		if m == cur {
			sel = i
		}
	}
	return ui.SettingRow{
		Label:  "Use the graphics processor",
		Detail: detail,
		Control: ui.Segmented{ID: "welcome:gpu", Options: labels, Selected: sel, OnSelect: func(i int) {
			a.setTranscribeGPU(modes[i])
		}},
	}
}

// transcribeGPUSettings is Settings → Transcription's graphics-processor
// control. When live transcription is on, it offers Off / After / Live &
// after; when live transcription is off, it simplifies to Off / After.
func (a *App) transcribeGPUSettings() []ui.Node {
	if !a.gpu.Ready {
		reason := a.gpu.Reason
		if reason == "" {
			reason = "not available on this PC"
		}
		return []ui.Node{ui.SettingRow{
			Label:  "Use the graphics processor",
			Detail: "Unavailable on this PC (" + reason + "); transcription runs on the processor",
		}}
	}
	cur := a.transcribeGPU()
	var modes, labels []string
	sel := 0
	if a.cfg.LiveTranscribe {
		modes = []string{config.GPUOff, config.GPUFinal, config.GPUBoth}
		labels = []string{"Off", "After", "Live & after"}
		for i, m := range modes {
			if m == cur {
				sel = i
			}
		}
	} else {
		modes = []string{config.GPUOff, config.GPUFinal}
		labels = []string{"Off", "After"}
		if cur == config.GPUFinal || cur == config.GPUBoth {
			sel = 1
		}
	}
	var detail string
	switch modes[sel] {
	case config.GPUOff:
		detail = fmt.Sprintf("Runs on the processor (%s available)", a.gpu.Summary())
	case config.GPUFinal:
		if a.cfg.LiveTranscribe {
			detail = fmt.Sprintf("Uses %s after meetings and for speakers; live transcription stays on the processor", a.gpu.Best.Name)
		} else {
			detail = fmt.Sprintf("Uses %s after meetings and for speakers (turn on Transcribe during meetings to use it live too)", a.gpu.Best.Name)
		}
	case config.GPUBoth:
		detail = fmt.Sprintf("Uses %s during and after meetings, and for speakers; falls back to the processor if it can't", a.gpu.Best.Name)
	}
	if modes[sel] != config.GPUOff && !native.HasVulkan(a.dataDir) {
		detail += " · downloads 18 MB once"
	}
	return []ui.Node{ui.SettingRow{
		Label:  "Use the graphics processor",
		Detail: detail,
		Control: ui.Segmented{ID: "set:transcribegpu", Options: labels, Selected: sel, OnSelect: func(i int) {
			a.setTranscribeGPU(modes[i])
		}},
	}}
}
