# Handy: how a mature local STT app embeds its engine

[Handy](https://github.com/cjpais/Handy) (MIT) is a push-to-talk dictation app: hold a shortcut, speak, the text is pasted. Analysed at `4807c21` (v0.10.1, 2026-11-19). Paths are relative to the repo root; the Rust backend is `src-tauri/src/` (~38k lines), the UI is React in `src/`.

Handy is not a meeting app: it records the mic only, for seconds to minutes, and has no system-audio capture or diarization. What makes it valuable is how carefully it runs speech models on ordinary hardware.

## Stack

| Layer | Choice | Where |
|---|---|---|
| Shell | Tauri 2 (Rust + WebView2), plugins for tray, autostart, single instance, updater, global shortcuts, store, clipboard | `Cargo.toml`, `lib.rs` |
| UI | React 18, Tailwind, zustand, i18next (many locales) | `src/` |
| Inference | **`transcribe-cpp` 0.4.2**: every model (Whisper, Parakeet, Canary, Moonshine, Qwen3-ASR, …) runs through it, as GGUF on ggml | `managers/transcription.rs`, `engine_supervisor/` |
| GPU | Vulkan on Windows x64 (dynamic backend DLLs, per-ISA CPU modules), Metal on Apple Silicon, CPU-only on Windows ARM | `Cargo.toml` target tables |
| VAD | Silero VAD v6.2 as GGUF through transcribe.cpp (in-process, CPU), or `earshot` | `audio_toolkit/vad/` |
| Mic capture | `handy-recorder` crate (device selection, resampling to 16 kHz, failure detection) | `managers/audio.rs` |
| History | SQLite (`rusqlite`, bundled) | `managers/history.rs` |
| Models | Catalog compiled into the binary (`catalog/catalog.json`, from `@handy-computer/models`), download from Hugging Face with a mirror fallback, SHA-256 per file | `catalog/mod.rs`, `managers/model.rs`, `managers/model/download.rs` |

Earlier Handy versions used `transcribe-rs` (ONNX for Parakeet, whisper.cpp for Whisper). The move to one GGUF engine for everything is recent and is the main reason transcribe.cpp is interesting for us.

## Things worth copying (as ideas)

### 1. The engine runs in a child process

`engine_supervisor/mod.rs`: all native inference code runs in a worker, the Handy exe re-launched with `--transcribe-worker`, speaking a framed request/response protocol over stdin/stdout (`protocol.rs`). The parent (`supervisor.rs`, 2.1k lines) loads and unloads models, runs and cancels transcriptions, and recovers from crashes and hangs, retrying in a CPU-only worker (`--cpu-only`) when the GPU was at fault. Reasons given in the code:

- a GPU driver fault, `GGML_ASSERT` abort or hang kills only the worker;
- the worker lives exactly as long as its model, so **unloading returns all CPU and GPU memory to the OS**;
- a CPU-only worker never runs GPU driver code, so a driver that crashes during init can't take the fallback down.

The worker claims stdout for the protocol before any native code runs and points fd 1 at stderr, because ggml prints to stdout in places (`worker.rs` `take_stdout_for_protocol`).

For a tray app that idles all day, this is the single most important pattern: the model (0.5–2 GB) is only resident while transcribing.

### 2. Models are unloaded after a timeout

`settings.rs` `ModelUnloadTimeout`: never, immediately, 2/5/10/15/60 minutes. With a worker process, unloading is just ending the process.

### 3. Long audio is cut at pauses into ~30 s chunks

`audio_toolkit/chunking.rs` (ported from pi-voice), with the comment: *"long audio is decoded in chunks cut at pauses, because most models' accuracy degrades past 30-40 s of audio in one run."*

- Target 30 s, maximum 40 s, minimum 5 s. Audio of 40 s or less is never cut.
- Cut candidates are the middle of every pause found by Silero-style hysteresis on per-frame speech probabilities (threshold 0.5, off at 0.35, min speech 250 ms, min silence 100 ms, 30 ms padding).
- Greedy planning: each cut is the best-scoring pause 5–40 s after the previous one, preferring long pauses near 30 s and ones that leave at least 5 s after them. With no pause, a hard cut at 40 s.
- **Live**, a cut becomes final once 5 s of audio past its window are scored (`LOOKAHEAD_SAMPLES`), so live cuts equal the cuts planned for the whole clip. Chunks are decoded during the recording, leaving only the last one when it stops (`managers/transcription/chunked.rs`, `live.rs`).
- The first chunk picks the language for all chunks.
- `managers/transcription/policy.rs`: streaming models stream; non-streaming models are chunked live unless they keep accuracy on long audio by themselves (`long_form`, "whisper today"); batch clips are chunked "into chunks of about 30 s".

This is directly relevant to the long-chunk idea; see [research/long-form-chunking.md](../research/long-form-chunking.md).

### 4. A model catalog with honest numbers

`catalog/catalog.json` lists ~80 models with sizes, licenses, capabilities (streaming, timestamps, language detection, translate) and benchmark results (WER per language, real-time factor on CPU and GPU). Handy's curated ranking (`catalog/mod.rs` `HANDY_RANKING`) puts `parakeet-unified-en-0.6b`, `nemotron-3.5-asr-streaming-0.6b`, `canary-180m-flash`, `cohere-transcribe-03-2026` and `whisper-medium` in the onboarding set. Extracted numbers for German and English: [research/models.md](../research/models.md).

### 5. One way to report background errors

`notices.rs` and `AGENTS.md` "Error notices": the backend raises a notice *code* (never a display string), one notice per source, a newer one replaces the older, a success clears it; the UI maps codes to text and shows them as toast, overlay or tray title. A good discipline for an app without a main window.

### 6. Tray icon states

`tray.rs`: separate PNGs for idle, recording, transcribing, each in a dark and a light variant (Windows taskbar theme read from the registry, `windows_taskbar_theme`), plus a warning badge. Static images; no progress. We want a progress ring, so ours are drawn at runtime, but the light/dark handling applies.

### 7. Smaller details

- `VK_LOADER_LAYERS_DISABLE=~implicit~` is set for the process so implicit Vulkan layers (overlays, capture tools) can't crash inference (`AGENTS.md`, Platform notes). Our worker needs the same if we ship Vulkan.
- Handy pins a patched `tao` because a WNDPROC change made Windows' `WM_QUERYENDSESSION` return FALSE and veto shutdown (`Cargo.toml` comments). A reminder to test sign-out and shutdown with our own message loop.
- The recording overlay is a 266×50 px webview window placed 46 px from the top or 15/40 px from the bottom (`overlay.rs`). Ours replaces it with a native layered window.

### The recording overlay's waveform

Read 2026-11-22 at `4807c21`. The overlay's moving bars are a spectrum, not a level:

- `src-tauri/src/audio_toolkit/audio/visualizer.rs`: `AudioVisualiser` takes the 16 kHz mic stream in windows of 512 samples (`VISUALIZER_WINDOW`, `sink.rs:98`), removes the mean, applies a Hann window, runs an FFT (rustfft) and averages the power into 16 buckets (`VISUALIZER_BUCKETS`) spaced quadratically between 400 and 4000 Hz (`sink.rs:119`). A bucket's level is `20·log10(sqrt(avg power) / window)`, which the comment says lands about 20 dB below true dBFS for speech; it is mapped from a window of -68..-30 dB "calibrated against measured mic audio (dictation ~-32 dBFS, room tone ~-48 dBFS)", times a gain of 1.3, to the power 0.7, then smoothed across neighbours (0.7 own, 0.15 each side). A noise floor is tracked per bucket but not used in the output.
- `src/overlay/RecordingOverlay.tsx`: each `mic-level` event is smoothed per bucket (`prev·0.7 + new·0.3`) and the first 9 (`WAVE_BARS`) become bars of height `3 + v^0.7 · 15` px (3..18). `RecordingOverlay.css` `.swave i`: 4 px wide, 3 px gaps, `border-radius: 2px`, centred, `transition: height 80ms linear`; before audio arrives they pulse at 6 px ("arming").
- So the visible bars cover about 400–1540 Hz (the first 9 of 16 quadratic buckets).

flit's voice meter takes the idea in its own code ([design.md](../design.md), `internal/capture/spectrum.go`): 9 bands over 400–4000 Hz with the higher ones lifted and a gate over each band's noise floor (Handy's layout put flit's test speech on the first bars: [research/voice-meter-bands.md](../research/voice-meter-bands.md)), the same dB window, gain and curve (Handy's calibration on speech), and the bar geometry; it eases by time between frames instead of per event and CSS transitions.

## What doesn't fit us

- **Tauri + WebView2 + React** is the bulk of Handy's footprint and complexity (settings window, overlay window, i18n, generated bindings). Our UI is native and minimal.
- **Dictation features**: pasting into the focused app, push-to-talk shortcuts, LLM post-processing, the transcription hook. Not needed.
- **Mic only.** No loopback or process capture, no tracks, no diarization. Handy doesn't use the `diarize` role models the catalog lists (`catalog/mod.rs` filters `sortformer` out of transcription models).
- **Cross-platform code paths** (macOS NSPanel, Linux layer shell, Wayland quirks) everywhere.

## Takeaway

Handy shows the engine side done well: one GGUF engine, models from a verified catalog, a worker process that isolates crashes and frees memory, VAD-driven pause chunking at ~30 s, and incremental decoding of chunks while recording. All of it can be rebuilt in Go around `transcribe.dll`; none of it needs Tauri.
