# Notes and summaries (proposal)

Status: **notes built** (2026-11-22, branch `feat/notes-and-summaries`), summaries proposed, not built. Research: [research/notes-editor.md](../research/notes-editor.md) (the editor), [research/summary-pipeline.md](../research/summary-pipeline.md) (context, prompts, steps, evaluation), [research/local-summaries.md](../research/local-summaries.md) (runtime, models, accelerators, cost). Decisions: [ADR 0011](../decisions/0011-meeting-notes-with-llama-server.md) (proposed), [ADR 0013](../decisions/0013-own-markdown-notes-editor.md) (accepted, built). References: [Yogurt](../references/yogurt.md), [AutoDoc](../references/autodoc.md), [anarlog](../references/anarlog.md).

Two features, the first useful without the second:

1. **Notes**: a small editor on each recording (H2, H3, nested bullets, tasks, paragraphs), usable during and after the meeting; paste Markdown in, copy Markdown or plain text out.
2. **Summary**: when the user asks for it on a recording, after the meeting, a small local model writes notes from the transcript and the user's notes: topics, decisions, tasks with owners, open questions. Never automatic, never during a recording.

## Shape

```
 tray process                                                    on demand, exits when idle
 ┌───────────────────────────────────────────────────┐          ┌──────────────────────────────┐
 │ recording page: Notes editor · "Write summary"     │          │ llama-server.exe (pinned)     │
 │ internal/notes: blocks ↔ Markdown, line times      │          │ CPU or Vulkan (iGPU/GPU)      │
 │ internal/summary: compact transcript, sections,    │──HTTP───▶│ one model, prompt cache       │
 │   cues, prompts, checks, merge, writer             │ loopback │ job object: CPU cap,          │
 │ internal/llm: hardware, model choice, speed check, │ port+key │ below-normal priority         │
 │   supervisor, OpenAI-compatible client             │          └──────────────────────────────┘
 └───────────────────────────────────────────────────┘
```

The tray process never holds a model (as with transcription). The LLM process starts when a summary is asked for and exits a minute or two after it finishes; its memory goes with it.

## Packages

| Package | Responsibility |
|---|---|
| `internal/notes` | **Built.** The notes document: blocks (paragraph, H2, H3, bullet, numbered, task; indent; done; inline Markdown text; when typed), `Parse`, `Markdown`, `Plain`, `HTML`, `Mentions` (the `@flit` instructions with their times), `Times` (`notes.json`); the `Editor` with every editing rule and undo ([notes-editor.md](../research/notes-editor.md#built-2026-11-22-a-deliberately-small)). Shared by the editor, the summary's input and its output. |
| `internal/ui` | The editor widget on Direct2D/DirectWrite ([notes-editor.md](../research/notes-editor.md)): rendering, caret and selection across lines, keys and Markdown shortcuts, undo, clipboard (Unicode text + HTML). |
| `internal/llm` | **Hardware**: RAM, cores, graphics adapters (DXGI: integrated or dedicated, memory), power source. **Choice**: a candidate list and the memory cap; the lighter model by default. **Supervisor**: the pinned llama.cpp build (CPU + Vulkan, downloaded on first use, SHA-256-checked), started on `127.0.0.1` with a random port and `--api-key`, `--no-webui`, context and threads; health, idle exit, cancel, job object. **Client**: `/v1/chat/completions` with streaming, `max_tokens`, JSON schema, token probabilities (`n_probs`), `timings`; `/tokenize`. **Speed check** per model and adapter. **GPU policy**: Vulkan where it measured faster, CPU fallback on hangs or device loss, remembered per adapter. An "other local server" URL (localhost only) uses the same client. |
| `internal/summary` | The pipeline ([summary-pipeline.md](../research/summary-pipeline.md)): compact transcript (respelled with vocabulary and names), sections of ~3k tokens at pauses, **attention cues** (note anchors by typing time and word overlap, mentions of the user's name, commitment and decision cues in German and English), section prompts → JSON items with quotes, checks (quotes found, owners known), merge, the user's tasks matched, one writer call → a `notes.Doc`; progress per step; everything testable with a fake client. |
| `internal/models` | The LLM candidates (pinned URLs, SHA-256, size, parameters, context, license) and the llama.cpp build. |
| `internal/session` | `notes.md`/`notes.json` and `summary.md`/`summary.json` in the session folder; the summary's state in `meta.json` (`none / queued / running / ready / failed / stale`). |
| `internal/config` | Your name (first, last, nickname), the notes model choice, "use graphics" (Auto/Off), the model folder. |
| `internal/app` | The recording page's Notes and Summary sections, the "Write summary" flow, Settings → Summaries. |

## A summary's run

```
 user: "Write summary" on a recording (not while it records)
   ├─ no model yet → read the PC → pick the lighter fitting model → show what downloads
   │     (runtime ~37 MB, model ~1.5–3 GB) and the expected time → download → speed check
   ├─ queued behind transcription and speakers of that recording, if still running
   └─ run: compact transcript → sections → cues
          → for each section: one call, JSON out (≤ ~300 tokens)      "Reading part 2 of 4"
          → code: quotes, owners, merge, the user's tasks
          → one writer call with the user's notes + items (≤ ~1,000 tokens)   "Writing"
          → code: form checks, what came from the user marked → summary.md
```

- **Cancel** at any point; a cancelled or failed run leaves the notes and the transcript untouched, and finished sections are kept for a retry.
- **Load**: the same CPU limit as the transcription after a meeting ([ADR 0009](../decisions/0009-quiet-transcription-by-default.md)); below-normal priority; one model in memory at a time.
- **On battery**: asks before starting.
- **Names changed** after the summary was written: the summary is marked stale with a "Write again" button (people appear as labels in `summary.json`, so renaming may be done without the model later).
- **Write again** with an instruction ("shorter", "in English") reuses the cached sections: only the writer runs again.

## Files (per session)

| Path | Content |
|---|---|
| `notes.md` | the user's notes, Markdown with the five elements |
| `notes.json` | when each line was first typed (session time), for anchoring; nothing else |
| `summary.md` | the written summary, editable like the notes |
| `summary.json` | the section items (topics, decisions, actions with owner, due, quote, time), which model and llama.cpp build, timings, the names used, whether the user edited it |

Like transcript text, notes and summaries never go into `debug.log`: the log gets counts and timings ("summary: 4 sections, 15,210 tokens read in 61 s, 1,940 written in 188 s, 2 quotes not found, Vulkan").

## UI

- **Recording page**: a Notes section (editor; during the recording too, with a small "typing at 12:40" hint), then a Summary section: empty with "Write summary" and the expected time; while running, progress and Cancel; when done, the summary in the same editor, with Copy (Markdown, plain text) and Write again. Items with a time link to the player.
- **Settings → Summaries**: your name (also used for the speech model's vocabulary and as "Me"); the model ("Lighter (recommended)" / "Better, slower", each with size and measured minutes per hour of meeting); "Use the graphics processor" (Auto / Off); "Use another local server" (advanced).
- **Without a model**: "Copy transcript with summary prompt" puts the compact transcript, the user's notes and the prompt on the clipboard.

## Phases

1. **Notes editor** (`internal/notes`, the widget, autosave, copy/paste, `@flit` mentions, typing times). **Done** (2026-11-22).
2. **Your name** in Settings, into the speech vocabulary and "Me"'s label; "Copy transcript with summary prompt".
3. **`internal/llm`**: hardware read, supervisor, client, speed check, against a fake server in tests and the real llama-server in a dev command.
4. **`internal/summary`** approach C (and A as its one-section case), `flitdev notes-eval` on AMI IS1009a–d; pick the default model and section size from the numbers.
5. **Summary UI**, Settings → Summaries.
6. Later: label readout (approach D) if it saves time; HTML paste; IME.
