# GPU-assisted transcription and diarization (Vulkan)

Question (2026-11-23): can flit run speech transcription (Parakeet Ultra) and speaker diarization (Sortformer) on the user's graphics processor via Vulkan to lighten CPU load, can `llama.cpp`'s `ggml-vulkan.dll` from summaries be reused, and what does Vulkan actually gain on accuracy and speed? Decision: [ADR 0014](../decisions/0014-gpu-assisted-transcription.md).

## Reusing `llama.cpp`'s `ggml-vulkan.dll`: no

Both `transcribe.cpp 0.4.2` and `llama.cpp b12633` load `ggml-vulkan.dll` dynamically (`GGML_BACKEND_DL`), but `ggml` has **no cross-version plugin ABI**:

- `transcribe.cpp 0.4.2` vendors `ggml` at commit `417a024b` with four downstream patches (`references/transcribe-cpp/ggml/UPSTREAM`, including `0002-backend-reg-filter.patch`): `ggml.dll` is 61,952 bytes, `ggml-base.dll` 675,328 bytes, `ggml-vulkan.dll` 43,652,608 bytes (13.1 MiB gzipped).
- `llama.cpp b12633` ships a newer upstream `ggml`: `ggml.dll` is 78,848 bytes, `ggml-base.dll` 840,704 bytes, `ggml-vulkan.dll` 44,867,072 bytes.

**Measured (2026-11-23):** copying `.cache/llama-b12633/ggml-vulkan.dll` beside `transcribe.dll 0.4.2` and running `flit-debug.exe transcribe --cpu=false`: `transcribe_init_backends` silently rejected the mismatched module and registered only `1 compute device: CPU`.

By contrast, flit's already-pinned engine bundle (`transcribe-native-0.4.2-windows-x86_64-cpu-vulkan.tar.gz`, 18 MB compressed, `internal/engine/native/native.go`) already contains `transcribe.cpp`'s own matching `ggml-vulkan.dll`, and its `contract.json` lists `"backends": ["vulkan", "cpu"]`. Extracting `ggml-vulkan.dll` beside `transcribe.dll` immediately registered `Vulkan0, CPU` (with `VK_LOADER_LAYERS_DISABLE=~implicit~` already set by `internal/engine/supervisor.go`).

## Measured on the desktop (Ryzen 7 5800X3D + Radeon RX 7800 XT)

`flit-debug.exe transcribe` with `transcribe.cpp 0.4.2`, `parakeet-ultra-Q8_0.gguf` (705.8 MiB copied to `Vulkan0`), `silero-vad-v6.2-F32.gguf` (always CPU, 1.2 MiB), and `diar_streaming_sortformer_4spk-v2.1-Q8_0.gguf` (132.8 MiB copied to `Vulkan0`).

### ASR only: `ami-IS1009a-5min` (two 300 s tracks = 10 min audio)

| Mode | `me` (300 s) | `them` (300 s) | Total | Combined speed | WER `me` / `them` |
|---|---:|---:|---:|---:|---|
| CPU unlimited (8 threads) | 19.9 s (15.0×) | 24.0 s (12.5×) | **43.9 s** | 13.7× RT | 15.6% / 19.3% |
| CPU Balanced (50%, 4 threads) | 25.4 s (11.8×) | 25.0 s (12.0×) | **50.4 s** | 11.9× RT | 15.6% / 19.3% |
| Vulkan GPU unlimited (cold shader compile on `me`) | 6.0 s (49.8×) | 2.5 s (119.9×) | **8.5 s** | 70.6× RT | **15.6% / 19.3%** |
| Vulkan GPU + Balanced (50%, 4 threads, warm shaders) | **3.0 s (100.7×)** | **2.0 s (150.6×)** | **5.0 s** | **120× RT** | **15.6% / 19.3%** |

WER, substitution, deletion and insertion counts were identical across CPU and Vulkan (`me`: 23/29/4; `them`: 12/20/10). Flit's Balanced 50% CPU cap caused zero slowdown on Vulkan.

### ASR + Sortformer diarization (`--speakers`)

| Meeting | Setup | `me` ASR | `them` ASR | Sortformer | Total | WER `me`/`them` | DER `them` | Words right |
|---|---|---:|---:|---:|---:|---|---:|---:|
| `ami-IS1009a-5min` (5 min) | CPU Balanced (50%, 4t) | 24.5 s (12.2×) | 24.0 s (12.5×) | 15.3 s (19.7×) | **63.8 s** | 15.6% / 19.3% | 51.6% | 78.2% (140/179) |
| `ami-IS1009a-5min` (5 min) | Vulkan GPU | **3.5 s (85.4×)** | **2.3 s (129.6×)** | **1.0 s (292.5×)** | **6.8 s** | **15.6% / 19.3%** | **51.6%** | **78.2% (140/179)** |
| `ami-IS1009a` (14 min, 839 s) | Vulkan GPU | **9.4 s (89.5×)** | **7.0 s (120.2×)** | **3.6 s (233.9×)** | **20.0 s** | 12.6% / 23.7% | 55.2% | 85.1% (490/576) |

Sortformer on Vulkan was **15.3× faster** than CPU Balanced on `ami-IS1009a-5min` with **bit-identical** turn count (65 turns), DER (`51.6%`: 24 s missed, 7 s false alarm, 15 s confusion), and speaker mapping (`1:FIO087 2:FIO084 3:FIO089`).

### Integrated GPU reference (not yet measured on the author's laptop)

`transcribe.cpp`'s published benchmark (`references/transcribe-cpp/docs/models/parakeet-ultra.md`) on an AMD Ryzen 7 PRO 4750U (Radeon iGPU) reports `parakeet-ultra-Q8_0` at **21.8× RT on Vulkan vs. 11.6× RT on CPU** (~1.9× faster). Real-call iGPU smoothness during live video calls on the author's laptop remains to be measured.

## Model placement & hardware readiness checks

- **Silero VAD**: always CPU (1.2 MiB, ~1,900× RT on 1 thread; `silero-vad-v6.2.md` recommends CPU for reference fidelity).
- **Parakeet Ultra & Sortformer**: Vulkan (`BackendAuto`) when enabled, totaling ~840 MiB of weights plus compute scratch (~1.2–1.5 GiB peak).
- **First-launch check (`internal/sysinfo/dxgi_windows.go`)**: safe in the tray process (no Vulkan device creation):
  1. Enumerate non-software DXGI adapters (`IDXGIFactory1::EnumAdapters1`, excluding `DXGI_ADAPTER_FLAG_SOFTWARE`).
  2. Check that `vulkan-1.dll` is loadable from `System32`.
  3. Require either `≥ 1.5 GiB` dedicated VRAM (discrete GPU) or `≥ 2 GiB` combined graphics memory with `≥ 8 GiB` system RAM (integrated GPU).
  4. **Recommendations:** discrete GPU with `≥ 2 GiB` VRAM → **Live & after** (`"both"`); integrated / smaller GPU → **After** (`"final"`, keeping live calls free of iGPU contention by default); not ready → **Off** (`"off"`).
