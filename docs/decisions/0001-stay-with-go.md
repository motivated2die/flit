# 0001: Go with Resound's base, transcribe.cpp as a DLL in a worker process

- **Status:** accepted (2026-11-21)
- **Date:** 2026-11-20

## Context

- Resound gives us, in Go and without cgo: tray, single instance, install/update/autostart, logging, config, a dark custom-drawn panel, Direct2D layered windows with animation (one already a top-centre capsule), WASAPI capture and COM plumbing, process and window inspection, a CI and release pipeline ([references/resound.md](../references/resound.md)). Roughly 15k of its 37k lines are relevant.
- The functional references are written differently: Handy in Rust/Tauri ([references/handy.md](../references/handy.md)), AutoDoc in Electron/TypeScript with Python runtimes ([references/autodoc.md](../references/autodoc.md)).
- Speech inference is C++ either way. Handy's engine, transcribe.cpp, runs every model we want (Parakeet, Canary, Whisper, Silero VAD, Sortformer diarization) behind one C ABI built for bindings, and publishes prebuilt Windows DLLs ([references/transcribe-cpp.md](../references/transcribe-cpp.md)).
- The app must be light while idle all day and should look and behave natively.

## Options

### A. Go + transcribe.dll loaded at runtime (no cgo)

Call the C API through `golang.org/x/sys/windows` lazy DLL procs, as Resound calls Win32 and Direct2D. Ship the pinned release's DLLs (≈12 MB CPU-only, +44 MB Vulkan).

- Keeps Resound's whole base and its build (plain `go build`, no C toolchain).
- The C ABI suits it: size-versioned structs passed by pointer, `_init` functions, plain function-pointer callbacks, a `contract.json` with an ABI hash to check before loading.
- Costs: hand-written mirror structs (~15) and function declarations (~30 of ~150), re-checked on every engine upgrade; a pre-1.0 ABI that may break between minor versions; DLLs next to the exe (or embedded and unpacked on first run) instead of one self-contained exe.

### B. Go + cgo, transcribe.cpp linked statically

One exe, generated bindings. But needs a C/C++ toolchain matching the library's build (MSVC-built ggml doesn't link into MinGW-built cgo easily), a slower and more fragile CI, and gives up Resound's "no C compiler" build. No real gain over A once a worker process exists.

### C. Go + sherpa-onnx (official Go bindings)

ONNX Runtime-based ASR, VAD and diarization (no 4-speaker limit). But its Go bindings use cgo, it brings ONNX Runtime's DLLs, it doesn't run the GGUF models (Parakeet-ultra) Handy's catalog benchmarks, and it would be a second engine if we keep transcribe.cpp for ASR. Kept as a fallback for diarization only.

### D. Rust + Tauri (start from Handy)

The best-trodden path for the engine (the `transcribe-cpp` crate by the same authors, Handy's supervisor and chunker to learn from). But WebView2 and React are exactly the footprint and UI style we don't want, Resound's native UI would be thrown away, and Handy's code is dictation-shaped and cross-platform throughout.

### E. Rust without Tauri (windows-rs + Direct2D + transcribe-cpp crate)

Technically sound: first-class engine binding, native UI possible. But it means re-implementing Resound's UI framework, tray, install and update machinery in Rust before the first meeting is transcribed: many weeks of work that A gets for free.

## Decision

**A.** Build the app in Go on Resound's base. Run transcribe.cpp through its prebuilt DLLs, loaded only in a **worker process** (our exe re-launched with a worker flag, as Resound already does for helper roles and Handy does for its engine). The tray process never loads the engine.

## Consequences

- An `engine` package with: the DLL binding (mirror structs checked against each `_init`'s `struct_size` at start-up, `contract.json` hash check), the worker's main loop, and a framed request/response protocol over stdin/stdout pipes. Pattern: Handy's `engine_supervisor` ([references/handy.md](../references/handy.md#1-the-engine-runs-in-a-child-process)).
- The worker exits when idle, which frees the model's memory; a crash or GPU fault kills only the worker, and the job is retried (on CPU if the GPU was at fault).
- The release bundles the DLLs of one pinned transcribe.cpp version. A CI test loads them and checks every struct size and the ABI hash. Upgrading the engine is a deliberate change with its own commit.
- Distribution is no longer a single file. Options: a folder in the install location, or the DLLs embedded in the exe and unpacked to `%LOCALAPPDATA%\<App>\engine\<version>\` on first run (keeps "download one exe"). To decide when packaging.
- Vulkan is optional: CPU-only first; GPU DLL as an optional download if measurements justify it.

## Revisit when

- The binding without cgo hits a wall (a by-value struct or float argument we can't pass, callback problems from ggml threads).
- The engine's ABI churns so much between versions that maintaining mirror structs costs more than it saves.
- A feature we need exists only in Rust code we can't reasonably port.

In those cases, option E (Rust, native UI) is the fallback, not D.
