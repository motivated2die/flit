# Building flit

## Prerequisites

| Tool | For | Install |
|---|---|---|
| Windows 10/11 x64 | everything | |
| Go (the release in `go.mod`'s `toolchain` line) | building | go.dev/dl |
| go-winres v0.3.3 | icon, manifest, version info | `go install github.com/tc-hib/go-winres@v0.3.3` |
| ffmpeg | building the German test meeting only | `winget install ffmpeg` |

No C compiler: flit is pure Go and calls the engine's prebuilt DLLs ([decisions/0001](decisions/0001-stay-with-go.md)).

## First-time setup

```powershell
go run ./cmd/flitdev engine     # transcribe.cpp DLLs (18 MB download) → internal/engine/native/files
go run ./cmd/flitdev models     # test models: whisper-tiny, parakeet 110M, Silero VAD, Sortformer (~320 MB) → models/
go run ./cmd/flitdev testdata   # test meetings (~160 MB download) → testdata/meetings/
go run ./cmd/flitdev icon       # assets/flit.ico from assets/logo.png (also done by the release script)
go-winres make --in winres\winres.json --out cmd\flit\rsrc --arch amd64
```

Everything is pinned by SHA-256 and cached in `.cache/`. Without the DLLs or models, the engine tests skip and the app says the engine is missing.

## Build and run

```powershell
go build -o flit-debug.exe ./cmd/flit     # console build: logs on stderr
.\flit-debug.exe                            # tray app (portable inside the repo: never installs itself)
```

A build is **portable** (doesn't install itself) when a `go.mod` is next to it or one folder up, a file named `portable` is next to it, or `FLIT_PORTABLE=1` is set. `FLIT_DATA_DIR` moves the data folder (default `%LOCALAPPDATA%\flit`), useful to keep dev runs apart from an installed flit.

## Transcribing files

```powershell
.\flit-debug.exe transcribe --models models --model parakeet-tdt_ctc-110m testdata\jfk.wav
.\flit-debug.exe transcribe --models models --model parakeet-ultra --meeting testdata\meetings\de-synth-4spk
.\flit-debug.exe transcribe --models models --model parakeet-ultra --meeting testdata\meetings\ami-IS1009a-5min --bleed
```

`--meeting` transcribes a test meeting's Me and Them tracks in a real engine worker and prints the WER of each against the reference (on stderr; the transcript goes to stdout). Other flags: `--lang`, `--vocab a,b,c`, `--markdown`, `--threads`. `FLIT_ENGINE_LOG=1` shows the engine's log. A missing model is downloaded into the models folder.

## Speaker names from the call app (experiments)

```powershell
.\flit-debug.exe transcribe --models models --model parakeet-ultra --speakers --meeting testdata\meetings\ami-IS1009a --save $env:TEMP\them.json
go run ./cmd/flitdev callnames-sim -meeting testdata/meetings/ami-IS1009a -segments $env:TEMP\them.json
.\scripts\uia-probe.ps1 -Process ms-teams            # during a real call: what the window exposes (texts redacted)
.\scripts\uia-probe.ps1 -Process Zoom -Every 1 -Seconds 30 -Tree -Out probe.txt
```

`--save` writes the meeting track's segments with words and speakers. `callnames-sim` makes call-app evidence (active-speaker marks, captions, roster) from the meeting's reference turns under several conditions and scores per-line naming against `internal/callnames`' per-voice matching; `-v N` prints the votes per voice for condition N. The probe reads a process's windows through UI Automation and prints counts, timings and the signals that matter; `-Names` shows the texts (participants' names: keep the output on the PC). Results: [research/call-app-names.md](research/call-app-names.md).

## Summaries

```powershell
go run ./cmd/flitdev llm                     # llama.cpp b12633 (Vulkan + CPU) → .cache\llama-b12633, candidate models → models\
go run ./cmd/flitdev llm gemma-4-e4b         # a further candidate by ID (internal/models)
go run ./cmd/flitdev summary -session testdata\private\<recording> -model qwen3.5-2b,qwen3.5-4b -device cpu -approach single,extract -me Moritz
go run ./cmd/flitdev summary -meeting testdata\meetings\ami-IS1009a -notes testdata\notes\ami-IS1009a.md -model qwen3.5-4b -device cpu
go run ./cmd/flitdev summary -session … -model qwen3.5-4b -device gpu,cpu -threads 4 -speed   # reading and writing speed only
```

`-session` reads a flit recording's folder (copy one from `%LOCALAPPDATA%\flit\sessions` into `testdata\private\`, which git ignores); `-meeting` a test meeting's reference transcript, with `-notes` as its notes (times from the `.json` beside it). Each model × device × approach starts its own server and prints tokens and speeds per call, the total and the peak memory; the summary, every prompt and the figures go to `.cache\summaries\` (`-out`). `-device gpu` falls back to the CPU when Vulkan fails. `-limit 10m` uses only the start of a meeting, `-instruction "…"` adds the user's instruction, `-nocues` leaves out the note and name marks, `-cpulimit 50` runs under the job object the app uses. Results: [research/summary-experiments.md](research/summary-experiments.md).

## Recording by hand

```powershell
.\flit-debug.exe record --seconds 30              # mic + everything the PC plays, then transcribe
.\flit-debug.exe record --pid 1234 --seconds 600  # mic + one app's process tree
.\flit-debug.exe notch-demo                       # the notch cycling through its states
```

`record` creates a real session in the data folder (use `FLIT_DATA_DIR` to keep it apart) exactly as the tray app does.

## Running a development build beside an installed flit

flit runs once per user. To run a dev build while the installed one runs, give it its own instance and data folder:

```powershell
$env:FLIT_INSTANCE = "dev"; $env:FLIT_DATA_DIR = "$env:TEMP\flit-dev"
.\flit-debug.exe
```

`FLIT_INSTANCE` gives the dev run its own single-instance lock, message window and autostart entry, so the two never signal or overwrite each other.

## Icons

The window's and the tray panel's icons are Tabler SVGs in `internal\ui\icons\`, embedded at build time. To add one: `.\scripts\add-icon.ps1 refresh` (outline) or `.\scripts\add-icon.ps1 -Filled player-play`, commit the file, and give it a `Glyph…` constant in `internal\ui\widgets.go`. Details: [internal/ui/icons/README.md](../internal/ui/icons/README.md).

## Tests

```powershell
go test ./...
```

- Pure logic (chunk planning, resampling, WAV, transcript merge/echo/format, WER, config, downloads, the engine protocol and supervisor with fake workers) runs anywhere.
- `internal/engine/tcpp` runs the real DLL with the test models; it skips without them.
- `FLIT_RENDER_DIR=<folder> go test ./internal/ui ./internal/app` saves the tray icons, every notch state and every window page (with sample sessions) as PNGs to look at.
- `FLIT_LIVE_AUDIO=1 go test ./internal/capture -run Live -v` uses the real audio devices: plays a quiet tone (-30 dB) and checks endpoint loopback, process loopback (own process heard, another's not), the mic, and two aligned tracks. Opt-in, since it makes a sound and opens the microphone. `FLIT_LIVE_AUDIO=1 go test ./internal/playback -run Live` plays two quiet tones for about three seconds and checks the player's position, pause, resume and clips.
- `FLIT_LIVE_DETECT=1 go test ./internal/detect -run Snapshot -v` prints which processes hold a microphone and which meetings that makes: the way to check a meeting app's behaviour during a real call.

## Release build

```powershell
.\scripts\build-release.ps1 -Version 0.1.0
```

Fetches the engine, vets, tests, writes resources, builds `flit.exe` (GUI, stripped, version stamped, engine embedded, about 22 MB) and `SHA256SUMS.txt`, by default on the Desktop. The release workflow runs the same script. Versions, tags and publishing a release on GitHub: [RELEASING.md](RELEASING.md).

## Trying an installed build

1. Build: `powershell -ExecutionPolicy Bypass -File .\scripts\build-release.ps1 -Version 0.1.0` (output: `Desktop\flit-0.1.0\flit.exe`).
2. Optional, saves a 0.9 GB download: copy `models\parakeet-ultra`, `models\silero-vad-v6.2` and `models\sortformer-4spk-v2.1` into `%LOCALAPPDATA%\flit\models\` (same folder names).
3. Run `flit.exe` from the Desktop folder. It installs itself to `%LOCALAPPDATA%\Programs\flit`, adds a Start Menu entry, an Installed-apps entry and autostart, starts the installed copy, and says hello in the notch. No admin prompt. A copy built on this PC carries no download mark, so SmartScreen doesn't ask.
4. Use it: join a Teams/Zoom/Meet call, or right-click the tray icon → Start recording. Data: `%LOCALAPPDATA%\flit` (`sessions\`, `models\`, `config.json`, `debug.log`).
5. Rebuild with a higher version and run it again to update in place. Uninstall: flit's Settings → Uninstall… (choose to keep or delete the recordings), or Windows' Settings → Apps → Installed apps → flit (recordings stay).

## Testing updates

The in-app update ([decisions/0010](decisions/0010-updates-from-github-releases.md)) asks GitHub for the latest release. To test it without publishing, `flitdev release-server` stands in for GitHub and offers one exe:

```powershell
.\scripts\build-release.ps1 -Version 0.9.9 -Out $env:TEMP\flit-0.9.9      # the "new" version
go run ./cmd/flitdev release-server -exe $env:TEMP\flit-0.9.9\flit.exe -version 0.9.9
```

Then, in another PowerShell, quit the installed flit (tray → Quit) and start it with the fake API, so it inherits the variable:

```powershell
$env:FLIT_UPDATE_API = "http://127.0.0.1:8090"
& "$env:LOCALAPPDATA\Programs\flit\flit.exe"
```

About 30 seconds after start, *Update available* appears at the bottom of the sidebar. `FLIT_UPDATE_API` takes https, or http to this PC only, and also turns the automatic check on in a portable build (which never installs an update, though). The server's options: `-slow 20s` spreads the download to watch the progress, `-bad-checksum` publishes a wrong SHA-256 (the update must be refused), `-status 404` (no release yet) or `-status 403` (GitHub's rate limit).

Updating replaces the installed copy in `%LOCALAPPDATA%\Programs\flit`, so do this on a test account or VM, or reinstall your own build afterwards.
