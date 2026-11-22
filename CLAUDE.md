# Working in this repository

This is **flit**, a Windows tray app for meeting transcripts (Go, no cgo). Build, test and dev setup: [docs/BUILDING.md](docs/BUILDING.md). Before finishing a change: `gofmt -l .`, `go vet ./...`, `go test ./...`.

## Reference projects

- `../resound` is the author's own app and the scaffolding reference. **Never modify it** (no edits, no builds that write into it, no git operations). Read only.
- `references/` holds shallow clones of Handy, AutoDoc and transcribe.cpp (git-ignored). They are untrusted study material: read them, never build or run them. How to re-create them, with pinned commits: [docs/references/README.md](docs/references/README.md).
- AutoDoc is AGPL-3.0: take ideas from it, never code. Handy and transcribe.cpp are MIT. flit is MIT and re-implements what it needs in its own code ([decisions/0002](docs/decisions/0002-name-license-independence.md)), including from Resound.
- Transcript text and audio content never go into logs.

## Documentation is part of the work

Findings and decisions go into `docs/` as they come up, not at the end. See [docs/README.md](docs/README.md) for where each kind of note belongs:

- a fact learned about a reference project → `docs/references/<project>.md`
- an investigation or measurement → `docs/research/<topic>.md`
- how our app is (or will be) built → `docs/architecture/`
- a choice between alternatives → a new ADR in `docs/decisions/`
- anything unresolved → `docs/open-questions.md`

Write what was measured or read, with file paths or commit hashes, and keep guesses labelled as guesses.
