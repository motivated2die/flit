# Architecture (proposal)

Status: **partly built** (2026-11-21). The engine path is real and measured end to end; capture, detection and the notch are next. The table below marks what exists. This document becomes the living architecture description, in the style of Resound's `docs/ARCHITECTURE.md`, as the parts land.

## Processes

```
 ┌──────────────── tray process (always running, small) ────────────────┐
 │ UI thread: tray icon, notch, popovers, settings panel, app state      │
 │ detector ─ polls capture sessions + window titles every 2–3 s         │
 │ recorder ─ mic thread ─┐                                              │
 │          ─ loopback ───┴─▶ 16 kHz mono WAV per track ─▶ live chunker  │
 │ pipeline ─ job queue (persisted) ─▶ merge, echo filter, format        │
 └────────────────────────────┬──────────────────────────────────────────┘
                              │ framed requests/responses over pipes
 ┌────────────────────────────▼─────────── worker process (on demand) ──┐
 │ transcribe.dll + ggml: VAD, ASR, diarization; one model at a time      │
 │ exits after an idle timeout → all model memory returned               │
 └───────────────────────────────────────────────────────────────────────┘
```

- **Tray process**: no engine code, no model in memory. Idle cost: one message loop, a poll every few seconds.
- **Worker process**: the same exe with a worker flag, started when there is work, loads the DLLs and one model, serves requests from stdin, answers on stdout (stdout claimed before any native code runs; ggml's prints go to stderr → log). Exits after N minutes without work, or when killed. See [decisions/0001](../decisions/0001-stay-with-go.md).

## Packages

| Package | Responsibility | State |
|---|---|---|
| `cmd/flit` | entry point → `app.Run`, roles by first argument: tray app, `--engine-worker`, `--uninstall`, and the console tools `transcribe`, `record`, `notch-demo` | built |
| `cmd/flitdev` | dev setup: engine DLLs, models, test meetings, app icon; `release-server`, a stand-in for GitHub's release API to test updates | built |
| `internal/app` | wiring; the tray app's state on the UI thread: detection → prompt → recording → transcription queue → ready/copy; model download in the background; the main window's pages (welcome, home, recordings, a session, audio, settings) and the tray panel; console tools | built (no text input yet: vocabulary and microphone are edited in `config.json`) |
| `internal/ui` | tray icon (feather with state badges) and native menu, notifications, clipboard; Direct2D/DirectWrite without cgo; **the notch**; **the main window** (`window_windows.go`) and its portable view framework and widgets (`view.go`, `widgets.go`, `frame.go`); the vector rasterizer and the feather mark; palette (`theme.go`, see [design.md](../design.md)); **the notes editor** (`noteseditor.go`: styled DirectWrite paragraphs, keyboard focus for editors) | built |
| `internal/coreaudio` | Core Audio: COM threads, endpoints, a correct `Activate`, active session PIDs, **process loopback** (`ActivateAudioInterfaceAsync` with a Go COM handler) | built, live-tested |
| `internal/capture` | device streams on MMCSS threads (mic, endpoint loopback, process loopback; a device refusing its mix format is retried with Windows converting, and the meeting side waits for a device that fails for a moment instead of failing the recording, [research](../research/audio-capture.md#a-device-refusing-its-own-format-2026-11-22)), QPC alignment onto the session time line, reopen after device loss, 16 kHz track writer, two-track recorder; each track's voice bands for the live meter (a 512-point FFT on the writer goroutine) | built, live-tested |
| `internal/detect` | snapshots (processes, mic users, window titles), provider table, process-tree targets, start/end hysteresis | built; real Teams/Zoom/Meet still to verify |
| `internal/session` | session folders and state, crash recovery, the transcription job (both tracks, echo, merge, .txt/.md/.json), the notes (`notes.md`, `notes.json`) | built |
| `internal/notes` | a recording's notes: blocks ↔ Markdown, plain text, HTML, `@flit` mentions, typing times, the editing rules and undo ([research/notes-editor.md](../research/notes-editor.md)) | built |
| `internal/instance` | single-instance mutex, finding and signalling the running copy (registered window messages) | built |
| `internal/install` | dropper install to `%LOCALAPPDATA%\Programs\flit`, Installed-apps entry, Start Menu shortcut, autostart, uninstall; HKCU only | built (registry/shortcut untested by design) |
| `internal/config`, `internal/logx` | `config.json` (atomic save, broken file kept), scrubbed log with an in-memory tail of the last 200 lines for the Debug page | built |
| `internal/sysinfo` | memory (`K32GetProcessMemoryInfo`) and CPU (`GetProcessTimes`) of flit and its engine worker, for the Debug page | built |
| `internal/download` | resumable, size- and SHA-256-checked downloads | built |
| `internal/update` | the latest full release on GitHub, version comparison, the exe's download checked against `SHA256SUMS.txt`, hosts held to GitHub; the app side (`internal/app/update_windows.go`) checks twice a day and offers it as an *Update available* pill at the bottom of the sidebar ([ADR 0010](../decisions/0010-updates-from-github-releases.md)) | built; releases from `.github/workflows/release.yml` ([RELEASING.md](../RELEASING.md)) |
| `internal/models` | pinned model catalog (Parakeet Ultra/v3, Silero, Sortformer, test models) | built |
| `internal/engine/native` | engine DLLs embedded in the exe, unpacked to `%LOCALAPPDATA%\flit\engine\<version>` with an ABI contract check | built |
| `internal/engine/tcpp` | transcribe.dll binding without cgo: ASR runs with word times, VAD, Sortformer diarization, vocabulary, abort, logging; struct sizes checked against the DLL | built, tested on the real DLL |
| `internal/engine` | worker protocol (framed JSON + PCM over pipes), worker loop, supervisor (start on demand, crash retry, idle exit, cancel, an optional hard CPU cap and low priority through a job object); diarization requests (model loaded on first use) | built |
| `internal/chunk` | pause-aligned chunk planning, live and final | built |
| `internal/pipeline` | track → VAD → chunks → ASR → segments in track time, split at pauses; `Progressive`: the same chunk by chunk from a growing file, equal to the batch result ([live-transcription.md](../research/live-transcription.md)) | built |
| `internal/transcript` | segments, merge, word-level echo suppression, speakers for words (`AssignSpeakers`), speaker summaries, names, plain/Markdown formatting | built |
| `internal/callnames` | naming the meeting track's voices from the call app's evidence (active-speaker marks, live captions matched by text, the roster): votes per voice, lines corrected where the evidence is clear ([speaker-names.md](speaker-names.md)) | prototype, tested, simulated (`flitdev callnames-sim`); not wired into the app |
| `internal/audio/wav`, `internal/audio/dsp` | WAV read/write (crash-safe header), reading frames in place for playback (`wav.File`), resampling to 16 kHz, downmix | built |
| `internal/calendar` | calendar subscription feeds (ICS): parsing, zones, recurrence, meeting links, matching recordings to meetings, a refresh service with cache ([calendar.md](calendar.md)) | built |
| `internal/playback` | playing a recording through waveOut: both tracks mixed or one alone, from any point or a list of stretches, pause/resume, the position on the timeline | built, live-tested |
| `internal/eval` | test meetings (`meeting.json`, RTTM), WER, DER, word attribution | built |

Not built yet: text input in the window (vocabulary, microphone choice), a settings panel, following a changed default device without it disappearing.

Proposed, not built: a notes editor on each recording and summaries on request by a local LLM in an on-demand `llama-server` process ([summaries.md](summaries.md)), speaker names from the call app's window ([speaker-names.md](speaker-names.md)).

## A session's life

```
 detected ──(user: Transcribe / auto)──▶ recording ──(meeting ends / Stop)──▶ finishing
     │                                       │ chunks decoded as their cuts become certain
     └─(Not now / timeout)─▶ dismissed        ▼
                                         transcribing ──▶ ready (copy) ──▶ archived
                                              │
                                              └─▶ failed (retry on start, up to 3 times)
```

- Each state is written to the session's `meta.json`, so a crash or reboot resumes: recording files are finalised, pending chunks re-queued.
- Recording and transcription overlap: the live chunker hands each pause-aligned chunk (~30 s) of each track to the job queue as soon as its cut is certain; the worker decodes them in order. When the meeting ends, only the tails are left.
- Diarization of the "Them" track (Settings → Tell speakers apart, on by default) runs once over the whole track after its transcription; words are assigned to speakers and segments split ([ADR 0006](../decisions/0006-speakers-with-sortformer.md)). A recording whose tracks were kept can find its speakers again from its page.

## Data (`%LOCALAPPDATA%\flit\`)

| Path | Content |
|---|---|
| `config.json` | settings |
| `sessions\<date>_<time>_<provider>\meta.json` | provider, window title, start/end, language, state, chunk progress |
| `sessions\…\me.wav`, `them.wav` | 16 kHz mono 16-bit tracks (deleted after transcription unless kept) |
| `sessions\…\transcript.json` | segments: track, speaker, t0/t1, text, words |
| `sessions\…\speakers.json` | the names the user gave the speakers (`me`, `them`, `speakers` by number); `.txt`/`.md` are rewritten with them |
| `models\<id>\<file>.gguf` | downloaded models |
| `engine\<version>\` | transcribe.cpp DLLs (if unpacked from the exe) |
| `debug.log`, `running.json` | as in Resound; never transcript text |

## UI surfaces

| Surface | What it shows | Technique |
|---|---|---|
| **Notch** (top centre of the main screen) | collapsed (after 3 s untouched): a click-through capsule with colour cues; expanded after a 1 s hover or on events: provider, elapsed time, level, Stop; after the meeting: progress, then "Ready · Copy / Close" until handled ([design.md](../design.md#the-notch-while-recording-and-after)) | layered D2D window, non-activating; `WS_EX_TRANSPARENT` while collapsed |
| **Detection popover** (below the notch) | "Teams meeting detected · Transcribe / Not now", auto-hides after ~30 s | same, clickable |
| **Tray icon** | idle, meeting detected, recording, transcribing with a progress ring, ready, error; light/dark taskbar | icons rendered at runtime (`CreateIconIndirect`) |
| **Tray menu / panel** | recent sessions (copy, open folder, retranscribe), settings: language, mic, model and downloads, vocabulary, providers, autostart, keep audio | Resound's GDI panel and rows |

Everything that appears on its own also leaves on its own; nothing takes the focus from the meeting.

## Output format (first draft)

```
Teams meeting · 2026-11-20 14:02–14:47 · German

[00:00] Me: Hallo zusammen, lasst uns mit dem Release-Plan anfangen.
[00:06] Them 1: Gerne. Ich habe gestern noch die Zahlen aktualisiert …
[00:31] Them 2: Kurze Frage zum Rollout in Q1 …
```

Consecutive segments of one speaker are joined into a paragraph; timestamps per paragraph; plain text and Markdown variants. Exact rules belong in `internal/format` with tests.

## Threads

As in Resound: one UI thread owning UI state (`Tray.Post` from elsewhere), one locked COM thread per capture stream, COM calls through `wincom.Run`. Pipeline and supervisor run on goroutines and only post state changes to the UI thread.
