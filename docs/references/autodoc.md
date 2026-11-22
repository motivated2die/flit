# AutoDoc: how a meeting-notes app works

[AutoDoc](https://github.com/DuetDisplay/AutoDoc) (AGPL-3.0, by Duet Display) records screen, mic and system audio, transcribes locally and writes LLM notes with Ollama. Analysed at `5082757` (v1.3.4, 2026-11-17). Its `PRODUCT.md` is a detailed technical description; paths below are relative to the repo root.

> **License:** AGPL-3.0. Nothing is copied from AutoDoc into this project. This document describes its behaviour so we can design our own.

## Stack

- Electron + React (TypeScript), `src/main` (main process services), `src/renderer` (UI).
- Capture in the renderer with Chromium's `MediaRecorder`: `screen.webm` (VP9), `mic.webm` and `system.webm` (Opus), chunks written every 100 ms (`PRODUCT.md` "Multi-Track Capture").
- Windows transcription: downloaded **Python runtimes** (python-build-standalone CPython 3.11) with `onnx-asr` for Parakeet TDT 0.6B v3 (FP32 on DirectML for GPUs with 4 GB+ VRAM, INT8 on CPU), `faster-whisper`/CTranslate2 for Whisper turbo, Canary-1B-v2 on CUDA or CPU for other European languages, and a signed whisper.cpp Vulkan build as fallback (`docs/windows-transcription-assets.md`, `resources/transcription-worker.py`).
- ffmpeg converts the WebM tracks to 16 kHz mono WAV before transcription.
- Notes, search and "Ask AI" through a managed local Ollama (`qwen3:4b-instruct` or `llama3.2:3b`).
- AES-256-GCM encryption at rest for every recording and transcript; the key protected with DPAPI through a small native helper.
- First-run downloads: **~10 GB**. Minimum 8 GB RAM; 8+ logical processors recommended for CPU-only.

That footprint is the opposite of ours, but the meeting logic is mature and worth studying.

## Meeting detection (`src/main/services/detection.ts`, `native/win-meeting-detector/`)

- A poll every **3 s**.
- **Signal 1, who is using a microphone.** A native helper exe (`win-meeting-detector.cpp`, ~270 lines of C++) enumerates every active capture endpoint, activates `IAudioSessionManager2` on each, walks the sessions, keeps those in `AudioSessionStateActive`, maps `IAudioSessionControl2::GetProcessId` to the exe name (`QueryFullProcessImageNameW`) and prints a JSON list. The helper is spawned on every poll (2 s timeout).
- **Signal 2, which provider that is.** Exe names are matched against a provider table: Zoom (`zoom.exe`), Teams (`teams.exe`, `ms-teams.exe`), Slack, Webex, Discord, each with title tokens and "generic" window titles (e.g. "Microsoft Teams" alone isn't a meeting window).
- **Browser meetings** (Google Meet, Teams web) are matched by window titles (`MEETING_APP_PATTERNS` in `src/shared/constants.ts`: `google meet|meet\.google\.com|^meet\s*[-–—]`, `microsoft teams|teams\.microsoft`, …) from Electron's `desktopCapturer` source list.
- **Prompt**: a floating notification at the **top centre** of the work area with a pulsing green dot, the calendar title or "Meeting detected", a "Start AI Notes" button; auto-dismisses after 30 s. If a calendar event is in progress and set to auto-record, recording starts without a prompt.
- **Suppression**: after a recording ends, the same provider signal is suppressed so the prompt doesn't come back for the meeting just recorded; an upcoming calendar event within 10 minutes postpones ad-hoc prompts.
- **Auto-stop**: the recorded window closed, the provider gone for 2 polls, or (macOS only) the mic idle for 30 s (`AUTO_STOP_GRACE_MS`), with counters so one missed poll doesn't end a recording. On Windows the mic-idle signal isn't used (`micActive` is `null` off macOS).

## Recording and storage

- `recordings/{meetingId}/` with `screen.webm`, `mic.webm`, `system.webm`, `metadata.json` (source name, start/stop time, duration); after stopping, the meeting is queued for transcription and the files are encrypted.
- The capture source is chosen from windows/screens; its name becomes the title when no calendar event matches.
- System audio is the **whole system's** output (Chromium loopback), not the meeting app's.

## Transcription pipeline (`src/main/services/transcription.ts`)

1. Convert each track to 16 kHz mono WAV.
2. Transcribe **mic and system separately**; one recording at a time, others queued. Low-spec PCs transcribe the two sources one after the other.
3. Long recordings: above 20 minutes, transcription runs in **90 s windows with 5 s overlap** (`CHUNKED_TRANSCRIPTION_*`); the Python worker itself works in **600 s windows with 15 s overlap** (`WORKER_CHUNK_*`). Parakeet in `onnx-asr` runs with its own VAD.
4. **Echo suppression** (`suppressAcousticEchoes`): without a headset, the mic picks up the remote side, so the "Me" track contains the remote speakers' words too. A mic segment of at least 3 words / 15 characters is dropped when a system segment from 4 s before to 2 s after it matches it closely: word Jaccard ≥ 0.5, containment ≥ 0.8 or trigram similarity ≥ 0.55, against up to 3 nearby segments.
5. **Cross-speaker duplicates** (`mergeTranscriptStreams`): after merging both streams chronologically, a segment that overlaps an earlier one by ≥ 100 ms and ≥ 30% and shares ≥ 3 words with ≥ 65% containment, within 6 s, is a duplicate.
6. **Repetition guard**: windows of 24 segments with at most 4 unique texts (≥ 80%) are treated as a hallucination loop. **Stitching**: adjacent segments of a speaker less than 900 ms apart are joined up to 240 characters.
7. Status: pending, queued, downloading, transcribing (with %), diarizing (reserved), complete, failed; up to 3 automatic retries on start-up, tracked in a `transcript.error` file.

## Speakers

**Diarization is disabled.** `PRODUCT.md` "Audio Source Labels": mic segments are **Me**, system segments are **Them**, "including all remote participants on that track". An experimental pyannote.audio 4 pipeline (`resources/diarize.py`, `diarization-requirements.txt`: torch, torchaudio) exists but is switched off. Users can rename labels; calendar attendees are offered as names.

## Notes (studied 2026-11-22, same commit)

Ideas only (AGPL). The notes path has grown into ~60 files in `src/main/services/` (`notes-*.ts`, `llm.ts` 4.3k lines, `ollama-*.ts`, `windows-notes-*.ts`).

- **Runtime**: its own Ollama, downloaded from Ollama's GitHub releases and run as a child on port 11435 with its own model directory (`PRODUCT.md` "Ollama Management"). Model `qwen3:4b-instruct`, or `llama3.2:3b` on 8 GB or low-spec profiles (`src/shared/constants.ts`).
- **Context by platform** (`llm.ts`): 32,768 tokens standard, **8,192 on Windows**, 4,096 on low-memory profiles and Macs. On Windows the Ollama runner is recycled above ~3.5 GiB resident on 16 GB PCs and ~2.5 GiB on 8 GB ones ("16 GB boxes share RAM with GPU drivers", `ollama-manager.ts`).
- **Small chunks, extraction before writing** (`notes-evidence-validate.ts`): the transcript is cut into chunks of ≤ 6,000 characters; each returns a JSON array of decisions, commitments and quantities, **each with a verbatim quote**; items whose quote isn't found in the chunk (`quoteIsInChunk`, after normalising) are dropped. A topic pass groups the rest into sections (`notes-scan-pipeline.ts`, `notes-topic-grouper.ts`).
- **A structured document** (`notes-schema.ts`, `PRODUCT.md` "Persisted V2 document"): overview, key takeaways, topic sections with key points and details, decisions, next steps with owner and deadline; **every item carries source time ranges** that seek the recording, and a provenance (generated, user-created, user-edited).
- **Checking with the same small model was tried and mostly removed** (`notes-claim-verification.ts`, its header comment): "A per-note transcript check was evaluated on 30 saved meetings (run 6) and removed: the same small model that writes the notes judged faithful notes unsupported more often than it caught real inventions, and it added the most processing time." What remains checks only the overview's sentences against the notes (4k context, 24 tokens out, a two-value enum, fail-open).
- Temperature 0 and a fixed seed on extraction and checks; JSON constrained by Ollama's `format` schema.
- Windows-only hardening (`docs/windows-notes-model-readiness-verification.md`, 2026-09-16): the model is checked before each job, downloads and clean-up never run under a job, a missing model fails the job once instead of retrying every chunk.

What it shows: on Windows laptops a 3–4 B model at 8k context is what a mature competitor ships; verbatim-quote grounding is checkable in plain code; asking the same small model to judge its own claims cost more than it caught.

## What we take

- The **detection model**: "a known meeting app holds an active capture session" plus window titles for browsers, polled every few seconds, with hysteresis for the end. We can do it in-process in Go without a helper exe (go-wca has `IAudioSessionManager2`).
- The **prompt** design: top centre, live dot, one action, auto-dismiss; suppress re-prompting for a meeting already handled.
- **Separate tracks transcribed separately, merged by time**, labelled Me/Them.
- The **text-level echo and duplicate suppression** as a cheap first answer to speaker bleed, with these thresholds as a starting point to tune.
- Status states and retry-on-start for failed jobs.

## What we leave

Electron, Python runtimes, ffmpeg, Ollama (a heavy runtime of its own; see [research/local-summaries.md](../research/local-summaries.md) for the notes ideas we keep), screen recording, calendars, encryption at rest (worth revisiting; DPAPI would be the light way), and the 10 GB footprint. AutoDoc's own gaps point at our differentiators: whole-system audio instead of the meeting app's, and no speaker separation within "Them".
