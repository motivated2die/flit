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

### Measured on the work laptop (Ryzen 5 5500U + Radeon Vega 7 iGPU, 2026-11-23)

Hardware: Lenovo ThinkPad `20YHS02900` (Windows 11 Enterprise 26200, non-admin), **AMD Ryzen 5 5500U** (6C/12T), **16 GB RAM** (14.82 GiB Windows-visible physical RAM after 1 GiB UMA carve-out), **AMD Radeon(TM) Graphics** (Vega 7, `deviceID 0x164c`, AMD proprietary driver `23.19.23.13`, Vulkan 1.3.260, `shaderFloat16 = true`, 768 MiB + 256 MiB device-local heaps + 7.16 GiB shared heap).

- **`sysinfo.DetectGPU()`**: `ready=true`, `recommended=final`, `vulkan=true`, `ram=14.82 GB`, `best="AMD Radeon(TM) Graphics (integrated)"`.
- **`sysinfo.GPUMeter`**: `\GPU Engine(*)\Utilization Percentage` PDH counters report live per-process GPU load (`TestGPUMeterReadsAProcess` passes).
- **Model loading**: both `parakeet-ultra-Q8_0.gguf` (705.8 MiB) and `diar_streaming_sortformer_4spk-v2.1-Q8_0.gguf` (132.8 MiB) load simultaneously into `Vulkan0` without OOM or CPU fallback; `silero-vad-v6.2-F32.gguf` (1.2 MiB) stays strictly on CPU.

5-minute dual-track benchmark (two 300 s tracks = 10 min audio interleaved from `testdata/jfk.wav` and `testdata/fleurs-de.wav`, 25 turns + 300 s Sortformer diarization; Balanced cap on 12 logical processors = 50%, 6 threads):

| Setup | `me` (300 s) | `them` (300 s) | Sortformer (300 s) | Total | vs. CPU Balanced |
|---|---:|---:|---:|---:|---:|
| CPU Balanced (50%, 6 threads) | 26.5 s (11.3× RT) | 39.8 s (7.5× RT) | 22.0 s (13.7× RT) | **88.3 s** | 1.0× |
| CPU unlimited | 21.0 s (14.3× RT) | 32.7 s (9.2× RT) | 19.2 s (15.6× RT) | **72.9 s** | 1.21× |
| Vulkan iGPU unlimited (cold shaders) | 12.8 s (23.4× RT) | 14.8 s (20.3× RT) | 10.3 s (29.3× RT) | **37.9 s** | 2.33× |
| **Vulkan iGPU + Balanced (50%, 6 threads, warm)** | **10.7 s (28.0× RT)** | **14.5 s (20.6× RT)** | **8.8 s (34.2× RT)** | **34.0 s** | **2.60×** |

Live-step simulation (`--live-step 15`, 25% CPU cap / 3 threads, matching Live Balanced):

| Live setup | `me` (300 s) | `them` (300 s) | End-of-call tail |
|---|---:|---:|---:|
| CPU Live Balanced (25%, 3t) | 33.6 s (8.9× RT) | 54.9 s (5.5× RT) | 10.9 s (3.9 s + 7.0 s) |
| Vulkan iGPU Live (25%, 3t) | 11.5 s (26.1× RT) | 15.3 s (19.6× RT) | 3.2 s (1.1 s + 2.1 s) |

Takeaways:
- **Post-meeting & speakers (`final`, default on iGPU)**: Vulkan is **2.60× faster** than CPU Balanced (`88.3 s → 34.0 s`; an hour-long meeting drops from ~17.7 min to ~6.8 min) with zero slowdown from the 50% CPU cap and matches `transcribe.cpp`'s published Ryzen 7 PRO 4750U reference (`21.8× RT` Vulkan vs. `11.6× RT` CPU).
- **Live (`final` vs. `both`)**: CPU Live Balanced already keeps up easily in real time (5.5–8.9× RT, 10.9 s tail) while leaving the iGPU untouched for Teams/Zoom video decode/encode, confirming `"final"` as the right default on integrated GPUs while `"both"` also works cleanly for users who opt in.

## Model placement & hardware readiness checks

- **Silero VAD**: always CPU (1.2 MiB, ~1,900× RT on 1 thread; `silero-vad-v6.2.md` recommends CPU for reference fidelity).
- **Parakeet Ultra & Sortformer**: Vulkan (`BackendAuto`) when enabled, totaling ~840 MiB of weights plus compute scratch (~1.2–1.5 GiB peak).
- **First-launch check (`internal/sysinfo/dxgi_windows.go`)**: safe in the tray process (no Vulkan device creation):
  1. Enumerate non-software DXGI adapters (`IDXGIFactory1::EnumAdapters1`, excluding `DXGI_ADAPTER_FLAG_SOFTWARE`).
  2. Check that `vulkan-1.dll` is loadable from `System32`.
  3. Require either `≥ 1.5 GiB` dedicated VRAM (discrete GPU) or `≥ 2 GiB` combined graphics memory with `≥ 8 GiB` system RAM (integrated GPU).
  4. **Recommendations:** discrete GPU with `≥ 2 GiB` VRAM → **Live & after** (`"both"`); integrated / smaller GPU → **After** (`"final"`, keeping live calls free of iGPU contention by default); not ready → **Off** (`"off"`).
