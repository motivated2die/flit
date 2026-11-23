# Documentation

flit is past research and being built (see the package table in [architecture/overview.md](architecture/overview.md)). These documents hold what we know, what we decided and why, and what is still open. They are kept up to date as work goes on; a finding that isn't written down here is lost.

## Map

| Folder / file | What goes there |
|---|---|
| [vision.md](vision.md) | What the app is for, its features, and what it deliberately doesn't do |
| [references/](references/) | One document per reference project: how it works, what to take from it, what not |
| [research/](research/) | Investigations of single topics: models, chunking, diarization, detection, capture. Include sources, measurements and an experiment plan where useful |
| [architecture/](architecture/) | How our app is built (first a proposal, later the real thing, like Resound's `ARCHITECTURE.md`) |
| [decisions/](decisions/) | Architecture decision records (ADRs): one choice per file, numbered, never rewritten after acceptance, only superseded |
| [design.md](design.md) | Visual design: palette, type, components, motion, the feather mark, the window's structure |
| [testing/](testing/) | Test data (sources, licenses, how it's built) and measurements |
| [BUILDING.md](BUILDING.md) | Setting up, building, testing, transcribing files, testing updates |
| [RELEASING.md](RELEASING.md) | Versions, tags, the release workflow, publishing on GitHub, the repository's one-time setup |
| [open-questions.md](open-questions.md) | Unresolved questions, with who or what can answer them |

## Reading order

1. [vision.md](vision.md)
2. [decisions/0001-stay-with-go.md](decisions/0001-stay-with-go.md): the language and engine choice, the most consequential one
3. [architecture/overview.md](architecture/overview.md): the proposed shape of the app
4. The research documents, as needed:
   - [research/models.md](research/models.md): which speech model for German and English
   - [research/long-form-chunking.md](research/long-form-chunking.md): does transcribing longer chunks improve accuracy?
   - [research/diarization.md](research/diarization.md): Me/Them, echo, and telling remote speakers apart
   - [research/speaker-identity.md](research/speaker-identity.md): recognising the same person across recordings
   - [research/meeting-detection.md](research/meeting-detection.md): noticing a meeting start and end
   - [research/audio-capture.md](research/audio-capture.md): recording mic and meeting audio on Windows
   - [research/audio-storage-and-quality.md](research/audio-storage-and-quality.md): why 16 kHz WAV sounded muffled, 7.1 downmix fix, and 48 kHz M4A (Windows MF AAC) benchmarks
   - [research/window-rendering.md](research/window-rendering.md): what a frame of the window costs, and whether smooth scrolling fits
   - [research/voice-meter-bands.md](research/voice-meter-bands.md): where the voice meter's bands sit, measured on speech and noise
   - [research/engine-cores.md](research/engine-cores.md): how many cores the engine uses, and what limiting it costs
   - [research/gpu-transcription.md](research/gpu-transcription.md): GPU-assisted transcription and diarization via Vulkan (runtime reuse, RX 7800 XT benchmarks, hardware check)
   - [research/dependencies.md](research/dependencies.md): every library, tool and engine flit relies on, their versions and known vulnerabilities
   - [research/local-summaries.md](research/local-summaries.md): a local LLM on Windows laptops: cost, models, accelerators (Vulkan, iGPU), picking a model for the PC (proposed)
   - [research/summary-pipeline.md](research/summary-pipeline.md): from transcript and the user's notes to notes: approaches, context, attention cues, prompts, evaluation (proposed)
   - [research/notes-editor.md](research/notes-editor.md): a small Markdown notes editor in flit's own UI (proposed)
   - [research/call-app-names.md](research/call-app-names.md): naming speakers from what Teams, Zoom and Meet show (proposed)
5. The reference analyses: [Resound](references/resound.md), [Handy](references/handy.md), [AutoDoc](references/autodoc.md), [transcribe.cpp](references/transcribe-cpp.md), [Yogurt](references/yogurt.md), [anarlog](references/anarlog.md)
6. Proposed designs, not built: [architecture/summaries.md](architecture/summaries.md), [architecture/speaker-names.md](architecture/speaker-names.md)

## Conventions

- Facts cite their source: a file path in a reference project (pinned commits in [references/README.md](references/README.md)), a URL, or a measurement with how it was taken.
- Guesses and expectations are labelled as such ("expected", "unverified").
- Numbers from someone else's benchmark say whose, and on what.
- A decision gets an ADR when there was a real alternative. Use [decisions/template.md](decisions/template.md).
