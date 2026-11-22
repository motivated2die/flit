# Open questions

Grouped by who or what can answer them. Remove a question once answered, and note the answer where it belongs (an ADR, a research or architecture document). Answered on 2026-11-21: name, license, the Go decision, keeping audio, mute, language, providers, distribution ([decisions/](decisions/)).

## For the author

1. **Code signing**: apply for SignPath Foundation signing now that releases are public? The release workflow's `sign` job is ready and waits for the repository variables ([RELEASING.md](RELEASING.md#code-signing-later)). Until then, SmartScreen warns on the first download.
2. **Outside contributions**: CONTRIBUTING.md says pull requests aren't accepted (as Resound's). Keep that?

## For measurement (needs code and recordings)

- Chunk length and cutting strategy vs. WER and term recall ([research/long-form-chunking.md](research/long-form-chunking.md#experiment-plan)).
- parakeet-ultra vs. tdt-v3 vs. canary-1b-v2 on German/mixed meetings ([research/models.md](research/models.md)).
- Speakers on real meetings: how often Sortformer splits one person in two or merges two, German vs English, more than four people ([research/diarization.md](research/diarization.md#next-steps)). Synthetic and AMI results: 79–89% of words with the right speaker.
- Recognising people across recordings: how well speaker embeddings match the same person across meetings (AMI IS1009a→b–d, then own recurring meetings), and at what threshold. Plan and options: [research/speaker-identity.md](research/speaker-identity.md#experiment-plan).
- Text-level echo suppression with and without a headset.
- Worker memory and real-time factor on the author's PC, CPU vs. Vulkan.

## For verification on a real PC

- Calendar feeds from Google, iCloud and Outlook.com (so far: Exchange's format and synthetic ones), and Outlook's "Customized Time Zone" ([architecture/calendar.md](architecture/calendar.md#checked)).
- Matching recordings to meetings in real use: how often the choice question comes up, and whether it picks wrong.

- Process names and capture-session behaviour of Teams (new), Zoom, Webex, Slack, Meet in browsers: lobby, in call, muted, ended ([research/meeting-detection.md](research/meeting-detection.md#to-verify-on-a-real-pc)).
- Per-process loopback with each of them, minimum Windows build ([research/audio-capture.md](research/audio-capture.md#to-verify)).
- Clock drift between mic and loopback over an hour.
- The window's look on screen (so far only offscreen renders): smooth scrolling and the player's slide on a slow laptop at 200% ([research/window-rendering.md](research/window-rendering.md)); the tray panel's painted border against Windows 11's rounding, and on Windows 10 (square corners, no DWM border); Tabler's icons at 100% next to text (thin strokes at 14 px).
