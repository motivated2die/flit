# How other apps tell speakers apart

Surveyed 2026-11-21 from public pages (web search; no code read or run). These are the apps and libraries that label speakers in local transcripts, most of them macOS-only. Facts here are what their makers or reviews state; anything not confirmed is marked as such.

| App / library | Platform | How | Limits |
|---|---|---|---|
| **MacWhisper** (Pro) | macOS | Since v12 (2025): Argmax's WhisperKit Pro + SpeakerKit, on device; transcript grouped by speaker, default names "Speaker 1, 2", **rename or merge** afterwards. v15.2 (2026-09): up to **8 speakers**, "powered by the Nvidia Nemotron Diarization model", plus live meeting transcripts ([9to5Mac](https://9to5mac.com/2026/09/23/macwhisper-update-brings-real-time-meeting-transcripts-improved-speaker-recognition-more/), [help article](https://macwhisper.helpscoutdocs.com/article/32-automatic-speaker-recognition-in-macwhisper)) | Only with WhisperKit models (or cloud providers); not for per-track podcast recordings, where each track is one speaker |
| **Argmax SpeakerKit** | macOS/iOS (Core ML) | pyannote v3 pipeline (segmentation + speaker embeddings + clustering) re-engineered for Apple hardware; ~9.6× faster than pyannote 3.1 at comparable DER ([SDBench paper](https://arxiv.org/pdf/2507.16136), [Argmax blog](https://www.argmaxinc.com/blog/speakerkit)) | Core ML only; Pro weights gated; "speaker identification" across recordings announced, not shipped |
| **FluidAudio** | macOS/iOS (Swift, Core ML) | Offline pipeline with pyannote-style segmentation, speaker embeddings and VBx clustering, plus a streaming pipeline ([repo](https://github.com/FluidInference/FluidAudio)); the exact embedding model was not confirmed | Core ML only |
| **Char** (formerly Hyprnote; open-source part now "anarlog", MIT) | macOS | Has a diarization crate in its Rust workspace ([README](https://raw.githubusercontent.com/fastrepl/anarlog/main/README.md)); whether the local build labels speakers today is unclear (a 2025 review: no speaker labels) | — |
| **AutoDoc** | Electron | Me/Them by track only; a pyannote.audio experiment is disabled ([autodoc.md](autodoc.md#speakers)) | — |
| **NVIDIA Nemotron 3 Diarization** (model) | — | Successor of Streaming Sortformer: **8 speakers**, streaming and offline, same arrival-order speaker cache; released 2026-09-23 under OpenMDW-1.1 ([arXiv 2611.12246](https://arxiv.org/pdf/2611.12246)) | Not in transcribe.cpp (as of `cfa237c`); one source calls the checkpoint a preview |

## What this means for flit

- Two families: **end-to-end neural diarizers** (Sortformer, Nemotron 3 Diarization: one model outputs who-spoke-when for a fixed number of slots) and **embedding + clustering pipelines** (pyannote, SpeakerKit, FluidAudio: segment, embed each stretch, cluster the voices; no hard speaker cap, but three models and more tuning).
- The Mac apps' edge is Core ML, which doesn't exist on Windows. On Windows, flit can run what transcribe.cpp ships: Sortformer (4 speakers) today.
- Every one of them lets the user **rename and merge** speakers afterwards: automatic labels are a starting point, not the answer. flit does the same (merge by giving two speakers one name).
- The 8-speaker Nemotron model is the natural upgrade once transcribe.cpp supports it; the binding talks to the DIARIZE role, not to Sortformer specifically.
