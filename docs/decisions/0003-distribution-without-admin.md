# 0003: One exe, dropper install, engine unpacked on demand, never admin

- **Status:** accepted
- **Date:** 2026-11-21

## Context

- The author wants Resound's install model: download one exe, run it, it copies itself to `%LOCALAPPDATA%\Programs\<app>` and registers itself for the current user.
- **No admin rights, ever**: not to install, not to run, not for any feature. flit must work on managed work machines.
- Unlike Resound, flit needs native code: transcribe.cpp's DLLs (~12 MB for CPU; +44 MB for Vulkan GPU) and models of 0.1–0.7 GB each ([decisions/0001](0001-stay-with-go.md)).

## Does anything in flit need admin?

No, as far as the research shows:

| Part | Rights |
|---|---|
| Install to `%LOCALAPPDATA%\Programs\flit`, Installed-apps entry, Start Menu shortcut, autostart | `HKCU` and the user's own folders only (as Resound) |
| Mic capture, endpoint loopback, per-process loopback, audio session enumeration | Standard user (Core Audio); the mic needs the Windows privacy setting "Let desktop apps access your microphone" |
| Engine DLLs and models in `%LOCALAPPDATA%\flit` | User's own folder |
| Network | Outgoing HTTPS only (models, updates). flit opens **no listening port**, so unlike Resound there is no firewall rule and no firewall prompt |

## Options for the engine DLLs

### A. Embedded in the exe, unpacked on first use (chosen)

The CPU DLLs (~12 MB, compressing well) are embedded in `flit.exe` (`go:embed`). The first time the engine worker starts, they are unpacked to `%LOCALAPPDATA%\flit\engine\<engine-version>\` and checked against their SHA-256. Older versions' folders are removed after an update.

- The download stays one file (~15–20 MB), the install stays Resound's dropper.
- Exe and DLLs can never mismatch: an update of flit brings its own engine version.
- Works offline once a model is downloaded.

### B. Downloaded on first use, like models

The exe stays small (~5–8 MB), the engine bundle comes from transcribe.cpp's GitHub release (pinned version, SHA-256) together with the first model.

- One more download source to keep reachable; exe/DLL version pairing must be managed. Better suited to the **optional Vulkan DLL** (44 MB), which only GPU users need.

### C. Zip or folder distribution (exe + DLLs side by side)

Simple, but unpacking a zip is one more step for the user and breaks the "run the download" dropper.

### D. MSIX (per-user package)

No admin needed in principle, clean uninstall, but needs a trusted code-signing certificate and is often blocked on managed machines by sideloading policies. Too much machinery for this app.

## Decision

- **A** for the CPU engine, **B** later for the optional Vulkan DLL.
- Models are downloaded on first use, after the user picks the meeting language (default model for it pre-selected), with a size shown and progress in the notch/tray, resumable, SHA-256 checked, from the pinned Hugging Face revision (mirror fallback possible).
- Dev builds (portable) unpack to the same place, or use `FLIT_ENGINE_DIR` to point at a folder of DLLs.

## Sensible defaults

- After install: autostart **on** (flit is useless if not running when a meeting starts), shown in the first-run setup with a switch to turn it off.
- Recordings kept by default (for benchmarking), with a setting to delete audio after transcription (see [vision.md](../vision.md)).

## Risks on work machines

These don't need admin but can still stop flit, and no install method avoids them:

- **SmartScreen** warns about unsigned downloads ("More info → Run anyway"). Code signing (e.g. SignPath Foundation, as planned for Resound) fixes the warning over time.
- **AppLocker / WDAC** policies may forbid running executables or loading DLLs from user-writable folders such as `%LOCALAPPDATA%`. Then neither the exe nor the unpacked DLLs can run, whatever the install method, short of IT whitelisting it.
- **Microphone privacy policy** set by IT can block desktop apps from the mic.

flit should detect the last two and say so clearly instead of failing silently.
