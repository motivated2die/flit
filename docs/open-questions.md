# Open questions

Grouped by who or what can answer them. Remove a question once answered, and note the answer where it belongs (an ADR, a research or architecture document). Answered on 2026-11-21: name, license, the Go decision, keeping audio, mute, language, providers, distribution ([decisions/](decisions/)).

## For the author

1. **Code signing**: apply for SignPath Foundation signing now that releases are public? The release workflow's `sign` job is ready and waits for the repository variables ([RELEASING.md](RELEASING.md#code-signing-later)). Until then, SmartScreen warns on the first download.
2. **Outside contributions**: CONTRIBUTING.md says pull requests aren't accepted (as Resound's). Keep that?
3. **Summaries** ([ADR 0011](decisions/0011-meeting-notes-with-llama-server.md), proposed). Answered 2026-11-22: about 15 min per hour of meeting on a weaker laptop is the target; summaries in the transcript's main language; a "For you" section and intent matching only if measurements show a gain ([research/summary-pipeline.md](research/summary-pipeline.md#decisions-by-the-author-2026-11-22)). Open: the model and runtime choice after the measurements.
4. **Names from the call app** ([ADR 0012](decisions/0012-speaker-names-from-the-call-app.md), proposed): on or off by default? flit would read the Teams/Zoom/Meet window during recordings (only the call's window, every 1–2 s). And would you turn on live captions in calls, if that gives the best names?

## For measurement (needs code and recordings)

- Chunk length and cutting strategy vs. WER and term recall ([research/long-form-chunking.md](research/long-form-chunking.md#experiment-plan)).
- parakeet-ultra vs. tdt-v3 vs. canary-1b-v2 on German/mixed meetings ([research/models.md](research/models.md)).
- Speakers on real meetings: how often Sortformer splits one person in two or merges two, German vs English, more than four people ([research/diarization.md](research/diarization.md#next-steps)). Synthetic and AMI results: 79–89% of words with the right speaker.
- Recognising people across recordings: how well speaker embeddings match the same person across meetings (AMI IS1009a→b–d, then own recurring meetings), and at what threshold. Plan and options: [research/speaker-identity.md](research/speaker-identity.md#experiment-plan-before-any-ui).
- Text-level echo suppression with and without a headset.
- Speaker names from call evidence on real calls: `callnames.Assign` + `Refine` against hand-labelled speakers; thresholds and reading interval (so far simulated only).
- Local LLM on the author's laptop (CPU-only, Vulkan with `-ngl 0` and with full offload on the AMD iGPU): prompt reading and writing speed, memory with Teams running, for Qwen3.5 2B/4B and Gemma 4 E2B ([research/summary-experiments.md](research/summary-experiments.md)); `flitdev summary -speed -device cpu,gpu-prefill,gpu -threads 4` gives the first two in a minute per model. Decides whether the laptop gets the 4B and whether "Use the graphics processor" should mean full offload or `-ngl 0` there.
- Why Vulkan fails with `ErrorDeviceLost` at `createDevice` on the desktop's RX 7800 XT (driver 32.0.32029.2010, Windows build 26300), in llama.cpp `b12633` and `b11000` alike: a driver update, a reboot, or this Windows build ([summary-experiments.md](research/summary-experiments.md#the-gpu-on-the-desktop)).
- Summary quality per approach (one pass vs extract-then-write vs classify-first) on AMI IS1009a–d against AMI's human summaries, with and without the attention cues; German on FAME or own meetings ([research/summary-pipeline.md](research/summary-pipeline.md#experiment-plan)).
- Whether boosting the user's name in the speech model's vocabulary fixes its recognition in German meetings.
- Worker memory and real-time factor on the author's PC, CPU vs. Vulkan.

## For verification on a real PC

- Calendar feeds from Google, iCloud and Outlook.com (so far: Exchange's format and synthetic ones), and Outlook's "Customized Time Zone" ([architecture/calendar.md](architecture/calendar.md#checked)).
- Matching recordings to meetings in real use: how often the choice question comes up, and whether it picks wrong.

- Process names and capture-session behaviour of Teams (new), Zoom, Webex, Slack, Meet in browsers: lobby, in call, muted, ended ([research/meeting-detection.md](research/meeting-detection.md#to-verify-on-a-real-pc)).
- What the call apps expose through UI Automation on Windows today: roster, active-speaker marks, live captions (Teams, Zoom, Meet in Edge/Chrome), elements and milliseconds per read, empty web trees. Run `scripts/uia-probe.ps1` during real calls ([research/call-app-names.md](research/call-app-names.md#experiment-plan)).
- Per-process loopback with each of them, minimum Windows build ([research/audio-capture.md](research/audio-capture.md#to-verify)).
- Clock drift between mic and loopback over an hour.
- The window's look on screen (so far only offscreen renders): smooth scrolling and the player's slide on a slow laptop at 200% ([research/window-rendering.md](research/window-rendering.md)); the tray panel's painted border against Windows 11's rounding, and on Windows 10 (square corners, no DWM border); Tabler's icons at 100% next to text (thin strokes at 14 px).
