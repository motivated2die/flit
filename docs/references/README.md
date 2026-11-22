# Reference projects

| Project | What we take from it | Where | Analysed at |
|---|---|---|---|
| [Resound](resound.md) | Application scaffolding: tray, UI framework, install/update, audio COM layer, build and CI | `C:\dev\motivated2die\resound` (local, **read only**) | `ec8a344` (v0.13.0-dev) |
| [Handy](handy.md) | How a mature local STT app embeds its engine: models, VAD, chunking, process isolation | [cjpais/Handy](https://github.com/cjpais/Handy), MIT | `4807c21` (v0.10.1, 2026-11-19) |
| [AutoDoc](autodoc.md) | How a meeting-notes app detects meetings, records tracks and merges them | [DuetDisplay/AutoDoc](https://github.com/DuetDisplay/AutoDoc), **AGPL-3.0** | `5082757` (v1.3.4, 2026-11-17) |
| [transcribe.cpp](transcribe-cpp.md) | The inference engine Handy uses; our planned engine | [handy-computer/transcribe.cpp](https://github.com/handy-computer/transcribe.cpp), MIT | `cfa237c` (after v0.4.2, 2026-11-21) |

Not cloned, surveyed from public pages: [how other apps tell speakers apart](speaker-apps.md) (MacWhisper, Argmax SpeakerKit, FluidAudio, Char; NVIDIA's Nemotron 3 Diarization).

## Local clones

The online references are shallow clones in `references/` (git-ignored). To re-create them:

```powershell
mkdir references; cd references
git clone --depth 1 https://github.com/cjpais/Handy.git handy
git clone --depth 1 https://github.com/DuetDisplay/AutoDoc.git autodoc
git clone --depth 1 https://github.com/handy-computer/transcribe.cpp.git transcribe-cpp
```

To look at the exact commits analysed, fetch them (`git fetch --depth 1 origin <hash>; git checkout <hash>`).

They are study material only: never build or run them from this repo. AutoDoc's license (AGPL-3.0) means **no code is copied from it**, only ideas, which these documents describe in our own words.

## Similar tools not studied in depth (yet)

- [sherpa-onnx](https://github.com/k2-fsa/sherpa-onnx): ONNX-based ASR, VAD and diarization (pyannote segmentation + speaker embeddings + clustering), with official Go bindings (cgo). The main alternative engine; see [decisions/0001](../decisions/0001-stay-with-go.md).
- [whisper.cpp](https://github.com/ggml-org/whisper.cpp): Whisper only; transcribe.cpp covers Whisper too.
- [parakeet.cpp](https://github.com/mudler/parakeet.cpp): a separate ggml port of Parakeet with a flat C API (`parakeet_capi.h`), Parakeet only.
