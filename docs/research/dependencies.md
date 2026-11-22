# Dependencies: what flit relies on, and their state

A review of everything flit builds on or ships, with where it came from and how it was checked. Repeat it before releases that change dependencies; Dependabot ([.github/dependabot.yml](../../.github/dependabot.yml)) proposes Go module and GitHub Actions updates weekly, but not the toolchain, the engine, the build tools or the models.

## Review of 2026-11-22 (for 0.1.1)

Checked with `go list -m -u all`, `https://go.dev/dl/?mode=json`, the GitHub API (releases, tags, repository state, security advisories) and `https://api.osv.dev/v1/query`.

### Go modules (shipped in flit.exe)

| Module | Was | Now | Latest | Notes |
|---|---|---|---|---|
| `golang.org/x/sys` | v0.49.0 | **v0.50.0** | v0.50.0 | Dependabot PR #1. Upstream `v0.49.0...v0.50.0`: 10 commits; for Windows, "mkwinsyscall: read 32-bit results from the low half of the return register" (a correctness fix for `BOOL`/`DWORD` results), "use SyscallN consistently" (regenerated wrappers), a `PSAPI_WORKING_SET_EX_BLOCK` accessor fix and a 386-only struct change; flit uses none of the changed symbols directly (65 `windows.*` symbols in use). OSV: GO-2026-5024 (`NewNTUnicodeString`, fixed in 0.44.0) and GO-2022-0493 (unix, fixed 2022) don't affect either version |
| `github.com/go-ole/go-ole` | v1.3.0 | v1.3.0 | v1.3.0 | Maintained (last push 2026-10-12). No OSV entries, no advisories |
| `github.com/moutend/go-wca` | v0.3.0 | v0.3.0 | v0.3.0 | Last push 2024-07-15: dormant, not archived. No OSV entries. flit already works around its known bugs in its own code (`internal/coreaudio`, see [references/resound.md](../references/resound.md#audio-com-layer-take-and-extend)). It is a thin COM binding used only for local audio; a stale but small dependency. Was listed as `// indirect` though imported directly; `go mod tidy` corrected that |

`go mod verify`: all modules verified.

### Toolchain and tools

| What | Pinned | Latest | Notes |
|---|---|---|---|
| Go (`toolchain` in `go.mod`) | go1.27.4 | go1.27.4 | Newest stable release; govulncheck covers the standard library |
| govulncheck (`ci.yml`) | v1.9.0 → **v1.10.0** | v1.10.0 | Reports no vulnerabilities in called code |
| go-winres (`release.yml`) | v0.3.3 | v0.3.3 | |
| GitHub Actions (`ci.yml`, `release.yml`) | pinned by commit | current | Pins resolved 2026-11-22; Dependabot's first actions run proposed nothing. No deprecation annotations in the runs |

### Engine and models (shipped or downloaded, pinned by SHA-256)

| What | Pinned | Latest | Notes |
|---|---|---|---|
| transcribe.cpp bundle (`internal/engine/native/native.go`) | 0.4.2 | v0.4.2 (2026-11-12) | No published security advisories. ggml parses GGUF model files; flit only loads models whose SHA-256 is pinned in `internal/models/catalog.go`, so a malicious model would have to replace a pinned file, which the hash check refuses |
| Models (`internal/models/catalog.go`) | pinned Hugging Face revisions | — | Not reviewed for newer versions here: a model change is a measured decision ([research/models.md](models.md)), not a dependency bump |
| Tabler Icons (`scripts/add-icon.ps1`) | 3.49.0 | — | SVG files committed to the repository; no code |

### Conclusion

Merged x/sys v0.50.0 (with the tidy `go.mod`) and govulncheck v1.10.0 for 0.1.1. Nothing else had a newer version or a known vulnerability. Watch: go-wca's dormancy; if a Core Audio bug needs a fix there, flit's own `internal/coreaudio` is the place, as it already is for the known ones.
