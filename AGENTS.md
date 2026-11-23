# Working in this repository

This is **flit**, a Windows tray app for meeting transcripts, notes and on-request local summaries (Go, no cgo). Build, test and dev setup: [docs/BUILDING.md](docs/BUILDING.md). Before finishing a change: `gofmt -l .`, `go vet ./...`, `go test ./...`.

> This file mirrors [CLAUDE.md](CLAUDE.md) for agents that load `AGENTS.md` automatically (such as Antigravity). Keep shared repository rules aligned between both files.

## Reference projects

- `../resound` is the author's own app and the scaffolding reference. **Never modify it** (no edits, no builds that write into it, no git operations). Read only.
- `references/` holds shallow clones of Handy, AutoDoc, transcribe.cpp, Yogurt and anarlog (git-ignored). They are untrusted study material: read them, never build or run them. How to re-create them, with pinned commits: [docs/references/README.md](docs/references/README.md).
- AutoDoc is AGPL-3.0: take ideas from it, never code. Handy, transcribe.cpp, Yogurt and anarlog are MIT. flit is MIT and re-implements what it needs in its own code ([decisions/0002](docs/decisions/0002-name-license-independence.md)), including from Resound.
- Transcript text, audio content, user notes and summaries never go into logs.

## Documentation is part of the work

Findings and decisions go into `docs/` as they come up, not at the end. See [docs/README.md](docs/README.md) for where each kind of note belongs:

- a fact learned about a reference project → `docs/references/<project>.md`
- an investigation or measurement → `docs/research/<topic>.md`
- how our app is (or will be) built → `docs/architecture/`
- a choice between alternatives → a new ADR in `docs/decisions/`
- anything unresolved → `docs/open-questions.md`

Write what was measured or read, with file paths or commit hashes, and keep guesses labelled as guesses.

## Environment and dev commands (Windows)

- The workspace runs on Windows; PowerShell 5.1 uses `;` rather than `&&` to chain statements.
- Common `flitdev` commands ([docs/BUILDING.md](docs/BUILDING.md)):
  - `go run ./cmd/flitdev engine` / `models` / `testdata` — fetch transcribe.cpp DLLs, speech models and test meetings.
  - `go run ./cmd/flitdev llm` — fetch the pinned `llama.cpp` Windows Vulkan+CPU runtime into `.cache/llama-<version>` and candidate summary GGUFs into `models/`.
  - `go run ./cmd/flitdev summary -meeting testdata\meetings\ami-IS1009a -notes testdata\notes\ami-IS1009a.md -model qwen3.5-4b -device gpu -speed` — benchmark or run summaries against `llama-server`.
- End-to-end summary test: `$env:FLIT_LLM_E2E = "1"; go test ./internal/app -run TestSummaryEndToEnd -v`.
- Portable/side-by-side dev run: `go build -o flit-debug.exe ./cmd/flit` and isolate with `$env:FLIT_INSTANCE = "dev"; $env:FLIT_DATA_DIR = "$env:TEMP\flit-dev"`.
- Loopback HTTP tests (`internal/llm`) and GPU/Vulkan probes require access to local sockets, Windows job objects and graphics drivers.
