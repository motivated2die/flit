# Third-party notices

## Shipped in flit.exe

| Component | Use | License |
|---|---|---|
| [transcribe.cpp](https://github.com/handy-computer/transcribe.cpp) 0.4.2 (`transcribe.dll`) | speech engine | MIT |
| [ggml](https://github.com/ggml-org/ggml) (`ggml*.dll`) | tensor runtime of the engine | MIT |
| miniz (inside transcribe.dll) | compression | MIT |
| Silero VAD code (inside transcribe.dll) | voice activity | MIT |
| [golang.org/x/sys](https://pkg.go.dev/golang.org/x/sys) | Windows API | BSD-3-Clause |
| [go-ole](https://github.com/go-ole/go-ole) | COM | MIT |
| [go-wca](https://github.com/moutend/go-wca) | Core Audio | MIT |
| [Tabler Icons](https://tabler.io/icons) 3.49.0 (`internal/ui/icons/`, © Paweł Kuna) | the window's and panel's icons | MIT ([license](internal/ui/icons/LICENSE)) |

The engine bundle's own license files are unpacked with the DLLs (`%LOCALAPPDATA%\flit\engine\<version>\licenses\`).

## Downloaded on first use

| Model | License |
|---|---|
| Parakeet Ultra, Parakeet TDT 0.6B v3 (NVIDIA, GGUF conversions by handy-computer) | CC-BY-4.0 |
| Silero VAD v6.2 | MIT |
| Streaming Sortformer 4spk v2.1 (NVIDIA) | NVIDIA Open Model License |

## Development only (not shipped)

Test fixtures and test meetings and their licenses: [testdata/README.md](testdata/README.md).
