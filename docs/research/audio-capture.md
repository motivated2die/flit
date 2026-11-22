# Recording the microphone and the meeting on Windows

Two tracks per session: **Me** (microphone) and **Them** (what the meeting app plays). Both written to disk as they come, so a crash loses seconds, not the meeting.

## Microphone

Shared-mode WASAPI capture, event-driven, exactly the loop of Resound's `audio/meter_windows.go` `InputLevel.run`, writing samples instead of only measuring peaks. Its error mapping (`ErrMicBlocked` when privacy settings block desktop apps) carries over.

- Device: the default communications capture device by default (what meeting apps use), or a chosen one, found again by name when its ID changes (Resound's `devicematch.go` idea).
- Format: whatever the mix format is (often 48 kHz float stereo or mono); converted later (below).

## Meeting audio: two ways

### A. Endpoint loopback (simple, everything)

`IAudioClient::Initialize` with `AUDCLNT_STREAMFLAGS_LOOPBACK` on the render endpoint the meeting plays to (default communications or console device). go-wca has the flag. Records **everything** played on that device: notifications, music, a video in another tab. This is what AutoDoc gets from Chromium.

### B. Per-process loopback (only the meeting app)

Windows 10 build 20348+ / Windows 11: `ActivateAudioInterfaceAsync` with `AUDIOCLIENT_ACTIVATION_TYPE_PROCESS_LOOPBACK` and `PROCESS_LOOPBACK_MODE_INCLUDE_TARGET_PROCESS_TREE` for the meeting app's process ID captures only that process tree's audio, whatever device it plays to. (Microsoft's "ApplicationLoopback" sample shows the flow; minimum build to verify against the docs.)

- Not in go-wca. Needs `mmdevapi.dll!ActivateAudioInterfaceAsync`, a `AUDIOCLIENT_ACTIVATION_PARAMS` blob in a `PROPVARIANT`, and a Go-implemented `IActivateAudioInterfaceCompletionHandler` (a static COM object with a Go callback, as Resound's `IDropTarget` in `ui/droptarget_windows.go`). The completion handler also needs `IAgileObject`.
- The activated client must use a format we give it (the process loopback client has no mix format of its own); 48 kHz float stereo or 16 kHz mono directly.
- Browsers: the tree is the whole browser, so other tabs' audio comes along. Still better than the whole system.
- Fallback to A when activation fails (older Windows, unusual process models).

B is the better default once it works; A stays as fallback and as a "record all system audio" option.

## Silence and gaps

Loopback delivers **no packets while nothing plays** (and capture can report `AUDCLNT_BUFFERFLAGS_DATA_DISCONTINUITY` or `SILENT`). If we only wrote what arrives, the Them track would be shorter than the meeting and drift against Me. Each packet's QPC timestamp (`GetBuffer`'s `pu64QPCPosition`) places it; gaps are filled with silence so both files share one timeline from the session's start.

## Aligning the tracks

- Both streams stamp packets with QPC time; the session records one QPC origin. Sample index = (packet QPC − origin) × rate, gaps filled, overlaps trimmed.
- Two devices run on different clocks (drift of tens of ppm, roughly 0.1–0.2 s per hour). For a transcript with second-level timestamps this is acceptable; for echo suppression across tracks it may need correcting (resample to the QPC timeline). To measure.

## Device changes

Resound doesn't follow device changes (its Known issue 2). For recording this is essential: a headset unplugged or a Bluetooth headset switching profiles mid-meeting must not end the Me track. `IMMNotificationClient` (default device changed, device removed) → reopen the stream on the new device, continue the same file with a silence gap. Needs a Go-implemented COM callback object, like the completion handler above.

### Switching devices mid-recording (built)

The user can pick another microphone or output on the Audio page while a meeting records (`capture.Recording.SwitchMic` / `SwitchThem`, `internal/capture/recorder_windows.go`). The old stream is stopped first (its last packets are written), then the new one starts on the **same** `Aligner` and track, so its samples land on the same session time line: the gap during the switch (tens of ms) becomes silence, and nothing shifts. If the new device fails to open, the old one is reopened. Each switch is recorded in `session.json` (`devices`: track, device, from-offset) so a transcript can later say which device spoke when. `TestLiveSwitchMicKeepsTimeline` switches between two real microphones and checks both tracks still end at the same length.

At the end, both streams stop at one shared QPC time (`Stream.StopAt`): packets captured after it are dropped. Before this, the two tracks of `TestLiveSwitchMicKeepsTimeline` differed by 66 ms because the second stream stopped later; now they are equal.

Following Windows' default device automatically (`IMMNotificationClient`) is still open; switching is manual for now.

## Format on disk and for the engine

- The engine wants 16 kHz mono float32 ([references/transcribe-cpp.md](../references/transcribe-cpp.md#api-essentials)).
- Convert while recording: downmix to mono, resample to 16 kHz with a proper windowed-sinc or polyphase filter (quality matters for ASR; a naive linear resampler aliases), write 16-bit PCM WAV. 16 kHz × 16 bit mono = 1.9 MB per minute per track, ~115 MB per hour per track. The WAV header is rewritten periodically so a crash leaves a readable file.
- Optional later: FLAC (roughly halves the size; a pure-Go encoder exists in `mewkiz/flac`, which Resound already depends on for decoding).
- Default after a successful transcription: delete the audio (privacy, disk space), with a setting to keep it.

## Threads

One locked OS thread per capture stream with COM (MTA) and MMCSS priority (Resound's pattern), doing only: read packets, convert, append to a ring buffer. A writer goroutine drains the ring to disk and feeds the live chunker ([long-form-chunking.md](long-form-chunking.md)). Levels for the notch's live indicator through atomics (Resound's `peakHold`).

## Implemented and verified (2026-11-21)

`internal/coreaudio` and `internal/capture`, on Windows 11 26H2 (build 26300), with SteelSeries Sonar's virtual devices as defaults. Live tests (`FLIT_LIVE_AUDIO=1 go test ./internal/capture -run Live`) play a quiet tone from the test process:

- endpoint loopback of the default communications output hears it;
- **process loopback of the test's own process hears it, and process loopback of explorer.exe's tree doesn't**: per-app capture works, with a completion handler implemented as a Go COM object (agile, so Windows may call it from any thread);
- mic and meeting tracks recorded together end within 10 ms of each other (QPC alignment, silence filled while loopback delivers nothing);
- the stream reopens a device that reports `AUDCLNT_E_DEVICE_INVALIDATED` (falling back to the default when a chosen device is gone) and keeps the time line going with silence.

Not handled yet: the default device *changing* while the old one still exists (needs `IMMNotificationClient`); clock drift over long meetings (alignment corrects it in steps of the 20 ms tolerance, unmeasured).

## Which microphone: the meeting app's (2026-11-21)

First real test with Zoom: the mic track stayed silent. On the author's PC, Windows' default microphone (both console and communications role) is SteelSeries Sonar's virtual microphone, and it measured **digital silence (−120 dBFS)**, while the BlackShark headset microphone delivered −18 dBFS. flit recorded the default; the meeting used the headset (directly, or through Sonar routing that wasn't active).

Fix: the capture sessions detection already enumerates tell **which device each process captures from**, so a detected meeting records the meeting app's own microphone (`detect.Candidate.Mic`). A user's choice on the Audio page overrides it; "Automatic" without a meeting (recording by hand) uses Windows' default device (console role, what "Same as System" in Zoom follows), no longer the communications default. A finished recording logs both tracks' peaks, and a mic track at digital silence (below −70 dBFS) warns in the notch. `FLIT_LIVE_AUDIO=1 go test ./internal/capture -run LiveMicLevels -v` prints every microphone's current peak, nothing recorded.

Lesson: with virtual audio routers (Sonar, Voicemeeter, VB-Cable) common, "the default device" is a guess; the device the meeting app really uses is a fact.

## A device refusing its own format (2026-11-22)

**Seen** in the author's log (`%LOCALAPPDATA%\flit\debug.log`), on 0.1.0-preview.2 shortly after an install: starting a recording failed with `meeting audio: Initialize: HRESULT 0x88890008` (`AUDCLNT_E_UNSUPPORTED_FORMAT`). The device was the configured output, "Lautsprecher (BlackShark V3 Pro XBOX - Game)" (`{af0cdf85-…}`), a wireless headset whose audio path includes THX Spatial Audio. The same endpoint:

| Time | What |
|---|---|
| 14:05 to 15:11 | endpoint loopback opened six times without error |
| 15:19:49 | the Audio page's level meter on it: the same `0x88890008` |
| 15:20:00 | the recording: `0x88890008`, recording not started |
| 15:20:06 | the next attempt: opened, recorded |

So for at least 11 s the endpoint refused the format that `GetMixFormat` had just returned (shared mode, the mix format passed back unchanged, `internal/capture/stream_windows.go`), then accepted it again. **Cause: unverified.** Expected: the endpoint's format was changing (a spatial-audio layer switching, the headset's dongle reconnecting), so the mix format read was stale for a moment. Unrelated and permanent on the same PC: `0x8889000A` (`AUDCLNT_E_DEVICE_IN_USE`) for the meter on "CABLE Input", which another app holds in exclusive mode.

**What flit does since** (`internal/capture/stream_windows.go`, `recorder_windows.go`):

- **Format fallback:** when `Initialize` refuses the mix format, a fresh client asks for 48 kHz float stereo with `AUTOCONVERTPCM | SRC_DEFAULT_QUALITY`, as process loopback always has; Windows converts from whatever the engine runs at. This applies to the mic, endpoint loopback and the level meters. The log says when it happened.
- **The meeting side waits instead of failing:** `capture.StartWaiting`. If endpoint loopback can't open at the start for a reason that passes (`transient`: unsupported format, device in use, device invalidated, endpoint creation failed), the recording starts anyway. The mic records, and the meeting track gets silence until the device opens; it is retried every 0.5 s like a device lost mid-recording, and a repeated identical error is logged once. Errors that don't pass (no output device at all, the audio service stopped) still fail the start. The microphone stays strict: a recording without it would be useless.
- Core Audio's errors are logged by name (`AUDCLNT_E_UNSUPPORTED_FORMAT`, …) instead of the bare number.

Not verified on the failing device: the condition couldn't be reproduced on demand. `TestLiveLoopbackHearsTone` doesn't hear its tone on this PC before or after the change: it plays on the default *communications* output ("Chat") and records the default *console* output ("Game"), which are different endpoints of this headset. Both versions opened the real endpoint with its mix format without error.

## To verify

- Per-process loopback with new Teams (does its audio come from `ms-teams.exe` or a WebView2 child?), Zoom, browsers.
- That our own capture streams don't trigger Windows' communications ducking ("reduce other sounds by 80%").
- Bluetooth headsets in hands-free mode (16 kHz or 8 kHz mic): quality impact on ASR.
