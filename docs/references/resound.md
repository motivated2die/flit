# Resound: the scaffolding reference

Resound is the author's Windows soundboard (Go, GPL-3.0), analysed at `ec8a344` (v0.13.0-dev). Its own documentation is thorough: `docs/ARCHITECTURE.md` (576 lines of dense detail), `docs/BUILDING.md`, `docs/RELEASING.md`. This document doesn't repeat it. It answers one question: **what can a meeting transcription app take from Resound, and where does Resound's design fall short for it?**

Paths below are relative to `C:\dev\motivated2die\resound`.

## Shape of the project

- One Go module, one executable (`cmd/resound` → `app.Run()`), **no cgo**, no UI framework, no WebView. `go build` is the whole build; `go-winres` adds icon, manifest (per-monitor-v2 DPI, `asInvoker`, Win10+) and version info.
- Code size (non-test lines, counted at `ec8a344`): `internal/ui` 11.4k, `internal/app` 10.3k, `audio` 2.8k, `server` 2.3k, `library` 2.0k, `input` 1.8k, smaller packages below 1k each. About **37k lines** in total, plus ~13k lines of tests. (ARCHITECTURE.md still says "~9k lines"; that figure is long outdated.)
- Dependencies (`go.mod`): `golang.org/x/sys`, `go-ole` + `moutend/go-wca` (Core Audio COM), `gopxl/beep` + decoders (playback), `oksvg`/`rasterx` (SVG icons), `pion/webrtc` (phone remote), `goldmark`, `go-qrcode`. Only the first three groups matter to us.
- One executable, many roles selected by the first argument (`--apply-routing`, `--wake-cable`, `--uninstall`, …): helper roles run before the single-instance mutex. **This is exactly the pattern needed for an engine worker process** (see [architecture/overview.md](../architecture/overview.md)).

## What carries over well

### Application lifecycle (take almost as is)

| Piece | Where | Notes |
|---|---|---|
| Drop-in install to `%LOCALAPPDATA%\Programs\<App>`, Installed-apps entry, Start Menu shortcut, uninstall that deletes itself | `internal/install/install_windows.go` | No admin, no installer. Replacing a running exe by renaming to `.old` |
| Autostart via `HKCU\…\Run`, only once the user enables it | `install.SetAutoStart` | Our app wants it on by default after setup; add `--autostart` handling to start silent |
| Portable dev builds (a `go.mod` next to the exe means "don't install") | `install.Portable` | Keeps dev runs from installing themselves |
| Single instance (`Local\…SingleInstance` mutex) and "second start opens the panel" (`wmShowPanel`), "installer asks to quit" (`wmQuitApp`) | `app.Run`, `ui/tray_windows.go` | |
| Self-update from GitHub's latest release, SHA-256 checked against `SHA256SUMS.txt`, host allow-list | `internal/update`, `app/update.go` | Unit-tested against a fake GitHub (`cmd/devupdate`) |
| Logging with scrubbing of user names, computer names, e-mails; crash detection via `running.json` | `internal/logx` | Important for us: transcripts must never land in logs |
| `config.json` in `%LOCALAPPDATA%\<App>` | `internal/config` | Note the known gap: a broken config silently resets (Known issue 6) |
| One-shot elevated helpers through `runas` | `internal/elevate` | Probably not needed by us |
| Session end (`WM_ENDSESSION`) handling | `ui.Tray.OnEndSession` | Must finalize recordings on sign-out/shutdown |

### Threading model (take the pattern)

- One locked UI thread runs the message loop and owns all app state; other goroutines post closures with `Tray.Post(fn)` (`ui/tray_windows.go`).
- Every audio stream runs on its own locked OS thread with COM (MTA) and MMCSS priority, owning its COM objects (`audio/output_windows.go`, `audio/meter_windows.go`). One-off COM calls go through `wincom.Run` on a fresh MTA thread.
- Hooks run on their own thread with their own message loop and only post events.

This is the right model for us too: capture threads write samples to disk and post levels; the transcription pipeline runs on worker goroutines and posts progress.

### Audio COM layer (take and extend)

- `internal/wincom` wraps endpoint enumeration, lookup by ID or name, `Activate`, default device roles, and **fixes for go-wca bugs** (`IMMDevice.Activate` passes the wrong argument, `GetDevice` is a stub, `GetMute` corrupts memory, `IPropertyStore.SetValue` is a stub). Anything we write on go-wca must respect this list.
- `audio/meter_windows.go` `InputLevel.run` is an event-driven shared-mode WASAPI capture loop (`AUDCLNT_STREAMFLAGS_EVENTCALLBACK`, `GetNextPacketSize` / `GetBuffer` / `ReleaseBuffer`). It only computes a peak; a recorder is the same loop writing samples. It already maps "privacy settings block the mic" to a clear error (`ErrMicBlocked`).
- `audio/format_windows.go` parses the mix format (float32 and 16/24/32-bit int, 1–8 channels) and names `AUDCLNT_E_*` errors (`describeHRESULT`), which Windows leaves without message text.
- `audio/levels.go` peak meters with a dB scale, usable for a live level in the notch.

What's missing for us (none of it exists in Resound): loopback capture, per-process loopback (`ActivateAudioInterfaceAsync`, not in go-wca), resampling captured audio to 16 kHz mono, device-change notifications (`IMMNotificationClient`, Known issue 2), writing WAV/FLAC, timestamp-based alignment of two capture streams. See [research/audio-capture.md](../research/audio-capture.md).

### Process and window inspection (take for meeting detection)

- `input.RunningApps` (Toolhelp snapshot, ~1 ms), `input.WindowApps` (visible, titled, unowned top-level windows, without shell parts), `input.AppName`, `fileDescription` (display names from version resources) in `internal/input/apps_windows.go`.
- Foreground tracking via a WinEvent hook (`EVENT_SYSTEM_FOREGROUND`) in `input/hook_windows.go`.

Combined with go-wca's `IAudioSessionManager2` (implemented properly in go-wca v0.3.0: `GetSessionEnumerator`, `IAudioSessionControl2.GetProcessId`), this covers AutoDoc's whole detection approach in-process. See [research/meeting-detection.md](../research/meeting-detection.md).

### UI framework (take selectively; this is the most valuable and the most entangled part)

Two rendering stacks live side by side in `internal/ui`:

1. **The panel** (`popup_windows.go`, `rows_windows.go`, `controls_windows.go`, `canvas_windows.go`, …): one borderless, DPI-aware popup docked at the tray icon, drawn with **GDI**. Pages are `[]ui.Row` lists rebuilt from app state on every change (`Row` interface: `height`, `segments`, `paint`, `click`, `keepsOpen`; optional `tipper`, `measurer`, `dragger`, `fieldHost`). Row types include Toggle, Slider, Stepper, Dropdown (an overlay inside the popup, so focus never moves), TextField (a native EDIT control placed over the row, so IME and clipboard work), Nav, Buttons, Section, Paragraph, Docked footer, Offer cards, tooltips. Windows 11 rounded corners via DWM, a self-painted glowing border, a blurred drop shadow computed in Go (`shadow.go`) as a separate click-through layered window, a "tab" window for dragging. Icons are SVGs rasterized in pure Go into alpha masks (`icon.go`, `icons/`).
2. **Layered windows** (`layered_windows.go`, `d2d_windows.go`, `overlay_windows.go`, `badge_windows.go`): per-pixel-alpha windows drawn with **Direct2D + DirectWrite** through hand-written COM vtable calls (no cgo), topmost, tool windows, never activated (`WS_EX_NOACTIVATE`), click-through (`WS_EX_TRANSPARENT`). They slide in from a screen edge while fading (130 ms in, 100 ms out, `easeOut`), redraw only on change, have cached shadow masks, and render color emoji. The launchpad badge (`ui.LaunchpadBadge`) is already a dark capsule placed **at the top centre of the primary screen by default** with a coloured status dot and a label that changes and flashes.

**The launchpad badge is very close to the notch we want.** `layered` + `renderBadge` is the starting point; the notch adds an expanded state, a live dot, a progress ring and click handling.

Testing support: offscreen rendering tests that save PNGs (`RESOUND_RENDER_DIR`), opt-in live tests for window placement and hooks.

### Build, CI, release (take as is)

- `.github/workflows/ci.yml` on `windows-latest`: `gofmt -l`, `go vet`, `go test`, `govulncheck`, `go build`. Actions pinned by commit hash. Dependabot.
- `scripts/build-release.ps1`: vet + test, `go-winres` with the version, `go build -trimpath -ldflags "-H=windowsgui -s -w -X …Version=…"`, `SHA256SUMS.txt`. The release workflow runs the same script.
- `scripts/add-icon.ps1` and `scripts/icons` (icon generation).

Read 2026-11-22 in the working copy at `../resound` (`ec8a344`) for flit's own pipeline: `.github/workflows/release.yml` (tag `v*.*.*` on `main` → build with `scripts/build-release.ps1` → optional SignPath job, off until `vars.SIGNPATH_ORGANIZATION_ID` is set → draft release with the exe, `SHA256SUMS.txt`, provenance attestation on public repositories, notes from `git log` since the previous stable tag), `.github/dependabot.yml`, `.github/ISSUE_TEMPLATE/`, `docs/RELEASING.md`, and the updater `internal/update/update.go` (366 lines: `releases/latest`, host allow-list incl. redirects, `SHA256SUMS.txt`, `Newer` with pre-release rules) with `internal/app/update.go` (a header badge and rows on the App page) and `cmd/devupdate` (a fake API). flit takes the workflow shape and the update protocol, re-implemented; it differs in: the engine and test models in the build (cached), the download through flit's own resumable `internal/download`, the update as a sidebar entry, no automatic checks in portable builds, and no restart during a recording ([ADR 0010](../decisions/0010-updates-from-github-releases.md)). Resound pins `signpath/github-action-submit-signing-request` at `f6d0478…` as `v3`; on 2026-11-22 the `v3` tag pointed at `a2c5b60…`, which flit pins.

## Visual language

Colours from `internal/ui/canvas_windows.go` and `overlay_windows.go` (the base for our dark theme):

| Token | Value | Use |
|---|---|---|
| background | `#1C1D22` | panel |
| row hover / press | `#2A2C33` / `#343740` | |
| group band | `#26282F` | |
| text / muted / caption | `#ECECF0` / `#9A9CA6` / `#7E818C` | |
| divider / control | `#33353C` / `#3A3D46` | |
| accent (mint) | `#50DEA8`, text on it `#102A20` | |
| danger / warning | `#F2416F` / `#F2C14E` | a recording dot could use danger |
| overlay bar | `#22242A` at 96% | capsules over other apps |
| window border | `#383838` | |

Fonts: Segoe UI; a small 10 px semibold for labels (`fontSmall`), DirectWrite in the layered windows. Corner radius follows Windows 11 (8 logical px).

### The panel glow

Read 2026-11-22 in the working copy at `../resound` (not re-pinned; files as named). The panel and the tray menu have a soft light of the accent in their top left corner and a border lit by it:

- `internal/ui/glow.go`: `cornerGlow = glowSpec{radiusXL: 150, radiusYL: 140, strength: 0.18, falloff: 5.5}` (logical px). `glowPixels` renders the light as premultiplied RGBA: opacity `strength · (e^(−falloff·t) − e^(−falloff)) / (1 − e^(−falloff))` with `t` the squared elliptical distance, so it reaches exactly zero at the ellipse and has no edge; interleaved-gradient-noise dithering of the alpha hides banding. `borderPixels` paints the window's 1 px border (inner edge anti-aliased, the outer one left to Windows' rounding), mixed from the border colour towards a light accent tint (`#A8EFD3`) by up to `borderGlow = 0.52` with the same falloff over 1.4× the light's reach (`borderReach`).
- `internal/ui/icon_windows.go`: `glows()` blits the light's bottom right quarter at the panel's top left corner (GDI `AlphaBlend`), cached per DPI; `glowBorder` blends only the strips along the edges.
- `internal/ui/popup_windows.go:180`, `traymenu_windows.go:49`: `roundCorners(h, dwmColorNone)` keeps Windows 11's rounded corners but turns DWM's border off (`DWMWA_BORDER_COLOR = 0xFFFFFFFE`), so the painted, lit border shows; `paintTo` draws background, glow, rows, then the border last.
- The light belongs to the window, not the content: it stays put while the page scrolls.

### Icons

Read 2026-11-22 in the working copy: 40 SVGs in `internal/ui/icons/` (Tabler outline and filled, one Font Awesome, one of Resound's own), embedded with `//go:embed icons/*.svg` in `internal/ui/icon.go`, named by file (`Icon("player-play")`, constants beside). `rasterIcon` renders each with oksvg/rasterx as an anti-aliased alpha mask at the exact device-pixel size; it is tinted with the row's colour and `AlphaBlend`ed, cached per name, size and colour (`icon_windows.go`). `scripts/add-icon.ps1` downloads from unpkg at pinned versions (Tabler 3.49.0, Font Awesome Free 6.7.2). `internal/ui/icons/README.md` has the licenses; `THIRD_PARTY_NOTICES.md` lists Tabler (MIT). `docs/ARCHITECTURE.md:464` notes Tabler's outline blurring at 10 px, where it used a Font Awesome icon instead. flit embeds the same kind of files but draws them its own way ([decisions/0008](../decisions/0008-tabler-icons.md)).

### The live glow in flit

flit takes the idea for its live glow, in its own code and in Direct2D (`internal/ui/glow.go`, [design.md](../design.md)): the light in `ColLive`, 20% strength (red is darker than mint, so a little stronger), only while recording, breathing with the dot, in the tray panel's top left and the main window's sidebar corner by the live status line.

## Critical assessment

### Where Resound's structure doesn't fit as is

1. **`internal/ui` is not a library.** It mixes generic widgets with Resound's own rows: the audio flow diagram (`flow_windows.go`, 870 lines), keybind cards, folder drop fields, sound rows, launchpad and overlay previews, VB-Cable visuals. And as an `internal/` package of another module it can't be imported at all. Reuse means **copying and trimming**, file by file. Roughly a third of `internal/ui` is generic (tray, popup, rows, controls, dropdown, text field, canvas, icons, shadow, layered, d2d, dialogs, panel position); the rest stays behind. A shared module could come later, once both apps have settled what they share; extracting it now would slow both down.
2. **`internal/app` is a 10k-line god object.** All state lives on one `App` struct on the UI thread, which suits a soundboard reacting to clicks in milliseconds. A transcription app has long-running background work (minutes of inference, downloads, file I/O). Keep the UI-thread rule for UI state, but put the pipeline into its own packages with explicit job state, persisted so a crash or reboot can resume, and only post progress to the UI thread.
3. **Two rendering stacks.** GDI can't draw anti-aliased shapes, per-pixel alpha or emoji; Resound works around it (own SVG rasterizer, alpha-blended DIBs, per-pixel anti-aliased bends). The "small, deliberate animations" we want (an expanding notch, a pulsing dot, a progress ring, spinners) are natural in Direct2D and awkward in GDI. Recommendation: **all new surfaces in Direct2D**; keep the GDI panel for settings at first, since its rows already work, and decide later whether to move it.
4. **The layered windows are click-through.** `WS_EX_TRANSPARENT` passes every click to the window below, which is right over a game. Our meeting popover needs a button ("Transcribe this meeting") but must still not steal focus from the meeting. That needs a non-transparent, non-activating layered window with its own hit-testing (`WM_NCHITTEST`, `WM_MOUSEACTIVATE` → `MA_NOACTIVATE`, hover tracking). New work, but small.
5. **The tray icon is static.** `loadAppIcon` loads the exe's icon resource; there is no runtime icon rendering. A progress ring or state badge needs icons drawn at runtime (Direct2D or the existing rasterizer into a DIB, then `CreateIconIndirect`, at the tray's size per DPI, with light/dark taskbar variants). New, small, but needs care with GDI handle leaks.
6. **Animation timing.** Layered animations run on `WM_TIMER` at 10 ms (`overlayFrameMS`), which Windows rounds to its ~15.6 ms timer tick, and every frame is a CPU-rendered `UpdateLayeredWindow` (2.7 ms per overlay frame measured in Resound). Fine for 100–200 ms slides; for continuous animations (a pulsing dot, a spinner running for minutes) we must keep frames cheap (small surfaces, redraw only the changing layer, low frame rate such as 30 fps) or the idle footprint suffers.
7. **No cgo is a principle that meets its limit.** Speech inference is C++ (ggml). Calling a C DLL without cgo is possible (`syscall`/`x/sys/windows` lazy DLLs, as Resound already does for Win32 and Direct2D), but the "one exe, nothing else" promise becomes "one exe that brings its DLLs" (~11 MB CPU-only, +43 MB for Vulkan GPU), plus models of several hundred MB downloaded on first run. See [decisions/0001](../decisions/0001-stay-with-go.md).
8. **Known reliability gaps that matter more for us.** Resound doesn't follow device changes (Known issue 2): a headset unplugged mid-meeting must not silently end the mic track. And a broken `config.json` resets silently (Known issue 6).
9. **Large parts are irrelevant**: phone remote and WebRTC (`server`, `webapp`, pion), VB-Cable install and routing, push-to-talk and keyboard hooks, sound library, importers, backups of sound folders. None of it should be copied.

### Is Go a sound base for the other apps' features?

Feature by feature, against what Handy and AutoDoc do:

| Feature | Handy / AutoDoc use | In Go with Resound's base |
|---|---|---|
| Tray, autostart, single instance, update | Tauri plugins / Electron | Already done, smaller and with more control |
| Overlay / notch | Tauri webview window / Electron BrowserWindow | Layered D2D windows exist; extend |
| Settings UI | React | GDI rows exist; less flexible than HTML, much lighter |
| Mic capture | `handy-recorder` (cpal) / Chromium MediaRecorder | WASAPI loop exists (meter) |
| System audio | none / Chromium loopback | WASAPI loopback: small step. Per-process loopback: new COM work, feasible (Resound already builds COM objects with Go callbacks for `IDropTarget`) |
| Meeting detection | none / native C++ helper exe | go-wca session enumeration in-process; Resound has the process and window helpers |
| VAD | transcribe.cpp (Silero GGUF) | Same, through the DLL |
| ASR inference | `transcribe-cpp` Rust crate | `transcribe.dll` through a hand-written binding (C ABI) |
| Diarization | none / pyannote in Python (disabled) | Sortformer through the same DLL |
| Process isolation of the engine | Re-launched exe as worker, framed pipe protocol | Same pattern; Resound already re-launches itself in helper roles |
| Resampling, WAV/FLAC | rubato, hound / ffmpeg | Small pure-Go pieces to write or pick |

Nothing on the list needs Rust. The hardest piece is the engine binding, and it is a C ABI designed for bindings. The conclusion is recorded in [decisions/0001-stay-with-go.md](../decisions/0001-stay-with-go.md).

## License note

Resound is GPL-3.0. Code copied from it into this project carries that license unless its copyright holders relicense it (Resound doesn't accept outside pull requests, so that is presumably its author alone; unverified). Decide the new project's license before copying the first file (see [open-questions.md](../open-questions.md)).
