# 0010: Releases on GitHub from a version tag; updates offered in the sidebar, installed by the new exe

- **Status:** accepted
- **Date:** 2026-11-22

## Context

- flit is going public on GitHub as `motivated2die/flit` (the module path since the start), first release 0.1.0.
- [ADR 0003](0003-distribution-without-admin.md) settled the install: one `flit.exe` that copies itself to `%LOCALAPPDATA%\Programs\flit` (`install.Ensure`). [vision.md](../vision.md) promises self-update and network only for models and updates.
- Resound (the author's other app, `../resound` at `ec8a344`) has a working pipeline for the same kind of app: CI on `windows-latest`, `scripts/build-release.ps1` shared by local and CI builds, `release.yml` on `v*.*.*` tags creating a **draft** release with the exe and `SHA256SUMS.txt`, build provenance attestation, a dormant SignPath job, and an in-app updater (`internal/update`, `internal/app/update.go`) that asks GitHub's `releases/latest`, downloads the exe, checks its SHA-256 and starts it ([references/resound.md](../references/resound.md#build-ci-release-take-as-is)).
- Unlike Resound, flit can be **recording a meeting** when an update arrives; quitting cuts the recording at that point.

## Options

### A. Resound's model: draft releases from tags, in-app check of `releases/latest`, the new exe installs itself (chosen)

- Release = push a tag on `main`; the workflow builds with the same script as a local build and leaves a draft; publishing it by hand is what ships.
- In the app: a periodic check against `https://api.github.com/repos/motivated2die/flit/releases/latest` (unauthenticated, 60 requests an hour per IP; flit makes two a day), download from GitHub's hosts only, SHA-256 against `SHA256SUMS.txt`, then the downloaded exe runs and `install.Ensure` does what it does for any downloaded copy: ask the running flit to quit, swap the exe, start it with "flit was updated".
- Reuses the installer as is; nothing new to trust besides GitHub and the checksum file published next to the exe.
- The checksum guards against a broken download, not against a compromised GitHub account or release; code signing (below) and the provenance attestation are what address that.

### B. A static update manifest (JSON on GitHub Pages or in the repository)

- Independent of GitHub's API and its rate limit; could carry minimum versions or staged roll-outs.
- One more thing to publish per release and to keep consistent with the release. Nothing flit needs now.

### C. A general updater framework (WinSparkle, Squirrel/Velopack, MSIX App Installer)

- Delta updates, signatures, scheduled installs.
- Native code or an installer framework, against [ADR 0001](0001-stay-with-go.md) (no cgo) and ADR 0003 (one exe, dropper install). MSIX needs a trusted signature. Far more machinery than one 22 MB exe needs.

## Decision

**A**, re-implemented in flit's own code ([ADR 0002](0002-name-license-independence.md)), with these differences from Resound:

- **The update is a sidebar entry.** "Update" (with a small white dot) appears in the main window's navigation only while a newer full release is known; its page shows the version, "What's new" (the release page), **Update now**, the download on a progress card, and errors. Settings → App has *Check for updates* (on by default) and *Check now*.
- **Never during a recording.** The download can run, but the new exe is started only when no recording is on; a download that finishes during one waits ("Restart and update", disabled) and starts by itself when the recording is stopped. A transcription that is cut off by the restart is taken up again by the new version (`session.Recover`).
- **The download reuses `internal/download`**: resumable, size- and SHA-256-checked, into `update\<version>\flit.exe` in the data folder (removed at a later start). Redirects are held to GitHub's hosts.
- **Portable (development) builds don't check by themselves** (a `0.1.0-dev` build would always see 0.1.0 as newer) and never install an update; *Check now* works, and `FLIT_UPDATE_API` turns checks on against a local fake (`go run ./cmd/flitdev release-server`).
- Pre-releases (`v0.2.0-rc.1`) are never offered in the app; testers download them from the release page.

## Consequences

- Every published release reaches every installed copy within 12 hours of its next start. **Publishing the draft is the release**; the checklist in [RELEASING.md](../RELEASING.md) comes before it.
- The release assets' names are a contract: `flit.exe` and `SHA256SUMS.txt` (`update.AssetName`, `update.SumsName`, `release.yml`). Renaming them strands installed copies.
- The repository must stay at `motivated2die/flit` (GitHub redirects renamed repositories, but the API base is compiled in), and must stay public, or installed copies stop finding updates.
- Unsigned until SignPath is set up: SmartScreen warns on the first download from the browser. Updates fetched by flit itself carry no download mark, so they don't trigger SmartScreen (expected; the Mark of the Web is set by browsers, not by Go's HTTP client).
- GitHub sees the IP address and `User-Agent: flit/<version>` twice a day; [PRIVACY.md](../../PRIVACY.md) says so.

## Revisit when

- Signing is set up (then: check the signature of the downloaded exe before starting it, not only the checksum).
- A release needs to force a minimum version or be withdrawn from some users (then a manifest, option B).
- GitHub's API rate limit becomes a problem in practice (many users behind one IP).
