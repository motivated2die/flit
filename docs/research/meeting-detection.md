# Noticing when a meeting starts and ends

## The signal

A meeting is under way when **a known meeting app holds an active audio capture session** (it is using a microphone). This is AutoDoc's approach ([references/autodoc.md](../references/autodoc.md#meeting-detection-srcmainservicesdetectionts-nativewin-meeting-detector)), and the right one: an app running in the tray (Teams, Slack, Discord) is not a meeting; an app using the mic is.

How to read it on Windows (Core Audio, no admin rights):

1. Enumerate active capture endpoints (`IMMDeviceEnumerator::EnumAudioEndpoints(eCapture, DEVICE_STATE_ACTIVE)`).
2. On each, activate `IAudioSessionManager2`, get its `IAudioSessionEnumerator`.
3. For each session in `AudioSessionStateActive`, `IAudioSessionControl2::GetProcessId` → process image name (`QueryFullProcessImageNameW`).

All of this is in go-wca v0.3.0 and implemented (not stubs; checked `IAudioSessionManager2_windows.go`, `IAudioSessionControl2_windows.go`). It runs in-process on a COM thread (`wincom.Run`), no helper exe. **Our own process must be filtered out**: once we record, our capture session is active too.

The same enumeration on render endpoints tells which apps are *playing* audio, useful to pick the process for per-process loopback ([audio-capture.md](audio-capture.md)).

## Identifying the app

| Provider | Process names | Notes |
|---|---|---|
| Microsoft Teams | `ms-teams.exe` (new Teams), `teams.exe` (classic) | Unverified whether Teams holds a capture session outside calls (e.g. in the pre-join lobby) |
| Zoom | `zoom.exe` | |
| Webex | `webex.exe`, `ciscowebexstart.exe`, `atmgr.exe` (older) | To verify on a real install |
| Slack (huddles) | `slack.exe` | |
| Discord | `discord.exe` | Gaming voice chat is not a meeting: off by default |
| Browsers (Google Meet, Teams/Zoom web) | `chrome.exe`, `msedge.exe`, `firefox.exe`, `brave.exe`, … | Need the window title (below) |

Process names are a user-editable list in settings, so new apps don't need a release.

**Browsers** hold one capture session for the whole browser. A meeting is recognised by a visible window title matching patterns such as `Meet – …`, `meet.google.com`, `… | Microsoft Teams`, `Zoom` (AutoDoc's patterns: `google meet|meet\.google\.com|^meet\s*[-–—]`, `microsoft teams|teams\.microsoft`). Resound's `input.WindowApps` enumerates titled top-level windows; we need it per process with titles. Titles are read locally and never logged in full.

## Start

- Poll every 2–3 s (one enumeration takes milliseconds; event-driven `IAudioSessionNotification` could come later, but sessions created before registration are missed and need a poll anyway).
- A meeting starts when a known provider's capture session becomes active and stays active for two polls (avoids prompting for a mic test or a voice message).
- Then show the **detection popover** under the notch: provider name (and, for browsers, the window title), "Transcribe" and "Not now", auto-dismiss after ~30 s. Option: always transcribe a given provider without asking.
- Don't prompt again for the same meeting after "Not now" or after its recording ended (AutoDoc's suppression), until that provider's session has been inactive for a while (e.g. 2 minutes).

## End

- A meeting ends when the provider's capture session has been inactive (or gone) for N consecutive polls (e.g. 10 s), or its process exited.
- Teams and Zoom keep a window open after a call ends; window presence isn't a reliable end signal, but the capture session is.
- Mute in the meeting app usually does *not* end the app's capture session (apps keep the stream open and mute internally), so a muted user doesn't end the recording. To verify per app.
- On end: finish both tracks, show "Transcribing…" in the notch and the tray, and start (or finish) transcription.
- Manual stop and start from the notch and the tray always work.

## Privacy points

- While we record, the mic is "in use" by our app too, and Windows 11 shows the microphone indicator for it. Expected and correct.
- Muting in the meeting app doesn't mute our recording: the user's words while muted in Teams are still recorded and transcribed. Options: detect app mute (no general API), offer a hotkey/notch button to pause "Me", or document it clearly. See [open-questions.md](../open-questions.md).

## Implemented (2026-11-21)

`internal/detect`: a snapshot (Toolhelp processes, active capture sessions of every active microphone through `IAudioSessionManager2`, visible titled top-level windows) takes about 20 ms; polled every 2.5 s; 2 polls to start, 4 to end. The capturing process is followed up the process tree to the top-most process of a provider's exe, which becomes the process-loopback target (so Teams' WebView2 helpers lead to `ms-teams.exe`). Browser providers additionally need a matching window title. The prompt goes to the notch with Transcribe / Not now; "Not now" holds until that provider's meeting ends.

First real observation: a Discord voice channel shows up as `discord.exe` capturing (child of the main `discord.exe`, which becomes the target), and Windows' own `svchost.exe` holding a capture session (ignored: not a provider). Discord is off by default.

## To verify on a real PC

- The process names and session behaviour of each provider (idle, pre-join lobby, in call, muted, call ended, minimized).
- Whether new Teams runs its media in a child process (`msedgewebview2.exe`?), which would change both detection and per-process loopback.
- Browser behaviour: does the capture session stay active between calls in a tab that keeps the mic?
