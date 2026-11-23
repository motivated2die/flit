# Decisions

Architecture decision records: one choice per file, numbered in order. A record is never rewritten once accepted; a later record supersedes it and both say so. New records start from [template.md](template.md).

| # | Decision | Status |
|---|---|---|
| [0001](0001-stay-with-go.md) | Go; transcribe.cpp as a DLL in a worker process | accepted |
| [0002](0002-name-license-independence.md) | Name "flit", MIT license, no code ties to Resound or others | accepted |
| [0003](0003-distribution-without-admin.md) | One exe, dropper install, engine DLLs embedded and unpacked, never admin | accepted |
| [0004](0004-product-defaults.md) | Product defaults: keep recordings, fixed meeting language, per-app prompts, record the mic unconditionally | accepted |
| [0005](0005-drawn-caption-buttons.md) | Drawn minimise/close in a native frame, caption folded into the client area | accepted |
| [0006](0006-speakers-with-sortformer.md) | Speakers on the meeting track with Sortformer (4), renamed and merged by the user | accepted |
| [0007](0007-live-transcription.md) | Transcribe during meetings in the batch chunks, capped by a Windows job object; speakers at the end | accepted |
| [0008](0008-tabler-icons.md) | Tabler icons everywhere, outlined into Direct2D polygons by flit's own code | accepted |
| [0009](0009-quiet-transcription-by-default.md) | Transcribe during meetings and limit the run after, by default (Balanced) | accepted |
| [0010](0010-updates-from-github-releases.md) | Draft releases on GitHub from version tags; updates offered in the sidebar, installed by the new exe, never during a recording | accepted |
| [0011](0011-meeting-notes-with-llama-server.md) | Summaries on request, by a small local LLM through llama-server (CPU + Vulkan), the lighter fitting model by default, extract-then-write | proposed |
| [0012](0012-speaker-names-from-the-call-app.md) | Speaker names from the call app's window (UI Automation), voted onto Sortformer's speakers | proposed (parked) |
| [0013](0013-own-markdown-notes-editor.md) | An own, line-based Markdown notes editor (headings, nested and numbered lists, tasks, bold, italics, @flit mentions) on flit's Direct2D framework | accepted |
| [0014](0014-gpu-assisted-transcription.md) | Optional Vulkan GPU acceleration for transcription and diarization (on-demand 18 MB download, DXGI check, 3-mode setting, CPU fallback) | accepted |
