# Engine DLLs (not in git)

`go run ./cmd/flitdev engine` downloads the pinned transcribe.cpp release bundle (see `../native.go`), verifies its SHA-256 and extracts the CPU DLLs, `contract.json` and the licenses into this folder. They are embedded into `flit.exe` at build time and unpacked to `%LOCALAPPDATA%\flit\engine\<version>\` when the engine worker first starts.

This README is the only tracked file here; it keeps the folder (and the `go:embed` pattern) valid in a fresh clone.
