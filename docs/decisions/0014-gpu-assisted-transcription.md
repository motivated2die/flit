# 0014: Optional Vulkan GPU acceleration for transcription and diarization

- **Status:** accepted
- **Date:** 2026-11-23

## Context

flit's speech engine (`transcribe.cpp 0.4.2`) previously ran CPU-only (`internal/engine/native/native.go` excluded `ggml-vulkan.dll` to keep `flit.exe` small). After enabling Vulkan for local summaries (`llama-server`), the author asked to evaluate GPU-assisted transcription and speaker diarization to reduce CPU load during and after meetings. Research and RX 7800 XT measurements: [research/gpu-transcription.md](../research/gpu-transcription.md).

## Options

### Runtime delivery
1. **Share `llama.cpp`'s `ggml-vulkan.dll`.** Ruled out empirically: `transcribe.cpp 0.4.2` and `llama.cpp b12633` ship incompatible `ggml` builds; `transcribe.dll` rejects `llama.cpp`'s `ggml-vulkan.dll`.
2. **Download `transcribe.cpp`'s own `ggml-vulkan.dll` on demand** from the already-pinned 18 MB release bundle (`native.BundleURL` / `BundleSHA256`) into `%LOCALAPPDATA%\flit\engine\<version>\`. Keeps `flit.exe` at ~22 MB and avoids shipping 43.7 MB (13.1 MiB gzipped) to CPU-only PCs.
3. **Embed `ggml-vulkan.dll` inside `flit.exe`.** Works offline immediately, but grows every installer and update by ~13 MiB.

### Controls & defaults
- Hardware readiness is checked safely at start-up via DXGI (`IDXGIFactory1::EnumAdapters1`, excluding software adapters) + `vulkan-1.dll` presence + memory thresholds (`internal/sysinfo/dxgi_windows.go`), without creating a Vulkan device in the tray process.
- Three user-facing modes in `config.TranscribeGPU`: **`off`**, **`final`** (after meetings and speaker analysis), and **`both`** (live during meetings + after meetings + speaker analysis). When *Transcribe during meetings* is off, Settings simplifies to **Off / After**.
- Welcome recommends **`both`** on discrete GPUs with ≥ 2 GiB VRAM, **`final`** on integrated/smaller ready GPUs, and **`off`** when hardware/Vulkan isn't ready, downloading the 18 MB runtime alongside the speech models with matching progress cards.

## Decision

Option **2** (on-demand download of `ggml-vulkan.dll` via `native.EnsureVulkan`), with first-launch DXGI + Vulkan readiness checks, the 3-mode setting, **Parakeet ASR and Sortformer diarization on Vulkan** (Silero VAD stays strictly on CPU), and automatic GPU → CPU fallback in `engine.Supervisor` (falling back to a CPU-only worker that passes `TRANSCRIBE_BACKEND_MASK_CPU` on load failure, crash, `StatusBackend`, or `StatusOOM`).

## Consequences

- `flit.exe` stays ~22 MB; users with Vulkan-ready GPUs download 18 MB once alongside the speech models on first run.
- On the desktop's RX 7800 XT, transcription + diarization runs ~9–10× faster than Balanced CPU (`ami-IS1009a-5min`: 63.8 s → 6.8 s; Sortformer 15.3 s → 1.0 s) with identical WER and DER.
- Sidebar GPU square and Debug page (`sysinfo.GPUMeter`, extended to multi-PID) cover both transcription workers and the summary server.
- VRAM never overlaps with summaries (`writeSummary` already releases the speech worker before starting `llama-server`).

## Revisit when

- Measurements on the author's laptop show integrated-GPU contention during live Teams/Zoom calls even when the user opts into `both`, or if `transcribe.cpp` later splits out a standalone Vulkan-only download archive smaller than 18 MB.
