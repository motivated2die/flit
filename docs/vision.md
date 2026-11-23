# Vision

## The problem

Meeting transcription apps are everywhere, but they are built for a different user: cross-platform Electron or web apps with accounts, cloud or local LLM summaries, calendars and multi-gigabyte downloads. Their transcription is tuned for live captions, which cuts audio into small slices and gives up accuracy, especially on names, jargon and mixed German/English.

This app is for one person's workflow: be in a meeting, get an accurate, well-formatted transcript afterwards, paste it into notes. Nothing more.

## Principles

1. **Small and quiet.** One tray app, a few MB of code plus the model, near-zero CPU while idle, starts with Windows. The heavy model is only in memory while transcribing.
2. **Accuracy first, latency second.** Transcribe long, pause-aligned chunks offline (not live streaming), with the language set ahead and a personal vocabulary. A transcript ready a minute after the meeting is fine; a wrong one isn't. See [research/long-form-chunking.md](research/long-form-chunking.md).
3. **Separate tracks, honest labels.** The mic is "Me", the meeting is "Them". Where a model can tell remote speakers apart, "Them" is split further; where it can't, it stays "Them" rather than guessing.
4. **Out of the way.** UI appears only when it has something to say and leaves again. No main window.
5. **Local only.** Audio, transcripts and models stay on the PC. Network only for downloading models and updates.

## Features

### Must have (first usable version)

- Tray app with autostart at sign-in, single instance, self-install and self-update (Resound's machinery).
- **Meeting detection**: notice when a known meeting app (Teams, Zoom, Webex, Slack huddles, Discord, Google Meet in a browser) starts using the microphone, and offer to transcribe it in a small popover. Notice when it ends.
- **Recording** of two tracks: microphone ("Me") and meeting audio ("Them"), time-aligned.
- **Transcription** with a Parakeet-class model via transcribe.cpp, German and English, language chosen ahead (per default or per meeting).
- **Formatting**: speaker-labelled blocks with timestamps, copied to the clipboard as plain text or Markdown.
- **UI**: a notch at the top centre of the main screen (recording, live indicator, progress), short-lived popovers (meeting detected, transcript ready), a dynamic tray icon (idle, recording, transcribing with progress, ready, error), and a small settings panel (language, devices, model, vocabulary, autostart).
- **Settings**: transcription language, microphone, model, custom vocabulary, autostart, keep or delete recordings.

### Should have

- Speaker diarization of the "Them" track (up to 4 speakers) with a small model. See [research/diarization.md](research/diarization.md).
- Capturing only the meeting app's audio (per-process loopback) instead of everything the PC plays.
- Progressive transcription during the meeting, so the transcript is ready moments after it ends.
- Echo handling when the user has no headset (the mic picks up the remote side).
- A searchable list of past sessions to copy again.
- **Names of the meeting's speakers from the call app** (Teams, Zoom, Meet show who is in the call and often who talks), voted onto the voices flit already tells apart; never a name on a guess. Proposed: [research/call-app-names.md](research/call-app-names.md), [ADR 0012](decisions/0012-speaker-names-from-the-call-app.md).

### Exploring (on request)

- **Notes** (built): a small editor on each recording (headings 2 and 3, nested and numbered lists, tasks, bold, italics), during and after the meeting, opened from the notch or Home while recording; `@flit` mentions point flit at a moment; Markdown in, Markdown or plain text out. [research/notes-editor.md](research/notes-editor.md), [ADR 0013](decisions/0013-own-markdown-notes-editor.md).
- **Summaries**: topics, decisions, tasks with owners and open questions, written from the transcript and the user's notes by a small local LLM, **only when the user asks for it on a recording**, after the meeting, using the GPU where that is faster, with a model chosen for the PC that errs on the light side. Proposed: [research/summary-pipeline.md](research/summary-pipeline.md), [research/local-summaries.md](research/local-summaries.md), [ADR 0011](decisions/0011-meeting-notes-with-llama-server.md). Without a model: copy the transcript and notes with a ready prompt for the user's own assistant.

### Explicitly not (for now)

- Summaries or any LLM pass **automatically**, or during a meeting (see Exploring above: on request only).
- Live captions.
- Calendar integration, accounts, cloud anything.
- Screen recording.
- macOS or Linux.

## Look and feel

Dark by default; small, crisp text; tastefully rounded components; small, deliberate animations (the notch expanding, a pulsing live dot, a progress ring, spinners). The visual language continues Resound's (palette in [references/resound.md](references/resound.md#visual-language)).
