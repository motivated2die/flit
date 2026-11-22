# flit

A small Windows tray app that notices when a meeting starts, records your microphone and the meeting's audio as separate tracks, and turns them into a clean, speaker-labelled transcript you can copy into your notes. German and English. Everything runs locally.

It is deliberately narrow:

- **Windows only**, one small program that lives in the tray and starts with Windows. No admin rights, ever.
- **Accuracy over live captions.** Audio is transcribed in long, pause-aligned chunks with a fixed language and your own vocabulary, not in tiny live slices.
- **Me / Them, and who among Them.** Your mic and the meeting audio are separate tracks; a small diarization model splits the remote side into speakers you can name.
- **Transcript and formatting only.** No summaries, no LLM. The output is formatted text, ready to paste.
- **Out of the way.** A notch at the top of the screen, short-lived popovers, and a tray icon that shows state and progress.
- **Private.** Recordings and transcripts never leave your PC. No account, no telemetry ([PRIVACY.md](PRIVACY.md)).

Notices calls in Teams, Zoom, Webex and Slack, and in Teams, Zoom and Google Meet in the browser; any other audio can be recorded by hand.

## Install

1. Download **flit.exe** from the [latest release](https://github.com/motivated2die/flit/releases/latest).
2. Run it. It installs itself for your Windows user to `%LOCALAPPDATA%\Programs\flit` and starts with Windows.
3. Choose the meeting language; flit downloads its models (about 0.9 GB) once.

flit isn't code-signed yet, so Windows SmartScreen may say "Windows protected your PC": choose **More info → Run anyway**. Each release lists the file's SHA-256 and can be checked with `gh attestation verify` ([how](docs/RELEASING.md#verifying-a-release)).

New versions show up as **Update** in flit's sidebar. To uninstall: Settings → Uninstall… (keeping or deleting your recordings), or Windows' Installed apps.

Requirements: Windows 10 or 11, 64-bit. Transcription runs on the processor; no graphics card needed.

## Status

**0.1, the first public release.** Recording, meeting detection, transcription during and after the meeting, speakers, the calendar and the window all work. How each meeting app behaves in real calls is still being checked; [open-questions.md](docs/open-questions.md) lists what is unverified. Bug reports are welcome ([CONTRIBUTING.md](CONTRIBUTING.md)).

## For developers

Building: [docs/BUILDING.md](docs/BUILDING.md). Releasing: [docs/RELEASING.md](docs/RELEASING.md). Design, research and decisions: [docs/README.md](docs/README.md).

flit is written in Go without cgo and draws its own UI with Direct2D. Its approach to UI and infrastructure follows the author's [Resound](https://github.com/motivated2die/resound), re-implemented in flit's own code. The transcription engine is [transcribe.cpp](https://github.com/handy-computer/transcribe.cpp), the engine behind [Handy](https://github.com/cjpais/Handy).

License: [MIT](LICENSE). Third-party components: [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
