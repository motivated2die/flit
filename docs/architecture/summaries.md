# Meeting notes (proposal)

Status: **proposed** (2026-11-22), nothing built. Research and numbers: [research/local-summaries.md](../research/local-summaries.md). Decision: [ADR 0011](../decisions/0011-meeting-notes-with-llama-server.md) (proposed). References: [Yogurt](../references/yogurt.md#notes-appsrcmainnotes), [AutoDoc](../references/autodoc.md#notes-studied-2026-11-22-same-commit).

After a meeting is transcribed and its speakers are told apart, flit can write **notes**: a short summary, the topics with their key points, decisions, action items with owner and due date, and open questions. Locally, by a small LLM, in the meeting's language, off by default. The transcript stays what it is; notes are an extra file beside it.

## Shape

```
 tray process                                             on demand, then exits
 ┌──────────────────────────────────────────┐            ┌─────────────────────────────┐
 │ job queue: … transcribe → speakers → notes│──HTTP────▶│ llama-server.exe (pinned)    │
 │ internal/notes: prompt, parts, checks,    │ 127.0.0.1 │ one model, one slot          │
 │   time ranges, Markdown                    │ random    │ CPU or Vulkan                │
 │ internal/llm: supervisor + client          │ port, key │ in a job object: CPU cap,    │
 └──────────────────────────────────────────┘            │ below-normal priority        │
                                                         └─────────────────────────────┘
```

- **The tray process holds no model**, as today. `internal/llm`'s supervisor starts `llama-server.exe` for a job, waits for `/health`, sends requests, and lets it exit after a short idle time (1–2 min: the notes of one meeting are one burst). Same pattern as `internal/engine`'s supervisor ([ADR 0001](../decisions/0001-stay-with-go.md)), same job object for the CPU cap ([ADR 0009](../decisions/0009-quiet-transcription-by-default.md)).
- **One model in memory at a time.** The notes job runs after the speech worker's last request; the speech worker is told to exit (or has gone idle) before the LLM loads on PCs under ~28 GB.
- **Never during a recording** in the first version. A recording that starts stops a running notes job; it is re-queued and starts over afterwards (Yogurt's rule, `queue.ts`).

## Packages

| Package | Responsibility |
|---|---|
| `internal/llm` | **Supervisor**: the runtime's files (downloaded like a model, SHA-256-checked, unpacked to `%LOCALAPPDATA%\flit\llm\<version>`), start with `--host 127.0.0.1 --port <free> --api-key <random> --no-webui -m <gguf> -c <ctx> -t <threads> [-ngl …]`, health, idle exit, kill on cancel, CPU cap by job object. **Client**: OpenAI-compatible `/v1/chat/completions` with streaming, `max_tokens`, `response_format` JSON schema, and the `timings` it returns. **GPU policy**: Vulkan first where the speed check said it is faster; a watchdog (no first token in N min) or a device-lost error restarts on CPU and remembers it for that GPU name (Yogurt's `engine.ts`). **Speed check**. An `Endpoint` setting points the same client at another local server instead. |
| `internal/notes` | The **compact transcript** (lever 3 in the research: no timestamps, joined turns, short labels), the **prompt**, **parts** when it doesn't fit, parsing and checking the answer, **time ranges** from quotes, rendering `.md` and plain text. Pure Go, tested with a fake client. |
| `internal/models` | New role `llm`: Gemma 4 E2B and E4B (QAT Q4_0) pinned by URL and SHA-256, with size, active parameters and context; later others ([research](../research/local-summaries.md#models)). |
| `internal/session` | Notes state in `meta.json` (`notes: none / queued / writing / ready / failed / stale`, progress as part n of m, error), retry on start like transcription (up to 3). |
| `internal/app` | The recording page's **Notes** section (copy, open, write again), Settings → Notes, the tray's "ready" can say "notes ready". |

## The prompt

Transcript first, behind a head that never changes, then everything that can change, then the rules (Yogurt's order, so a second question reads only what is new):

```
Here is the transcript of a meeting. Each line starts with who spoke: "Me" is the
person these notes are for; other names or "S1", "S2" are people on the call.
<transcript>
Me: …
S1: …
</transcript>

Meeting: <title> · <date> · about <N> minutes · language: <German|English>
People: <names from speakers.json, the call app's roster, calendar attendees>
Spelling: <vocabulary terms and names>

Write notes for "Me" in <language>. Answer only with JSON matching the schema.
- summary: at most 3 sentences.
- topics: one per subject, most important first, a specific title, up to 5 points each.
- decisions: what was agreed, each with "quote": up to 12 words copied exactly from the transcript.
- actions: who will do what, by when (only if said), each with "quote".
- questions: what was left open.
Refer to people by their label as written in the transcript ("Me", "S2", or a name).
Use only what was said. Leave a list empty rather than guess. No small talk.
```

The schema (short keys keep the answer short; `response_format` makes the model's grammar follow it):

```json
{"summary": "…",
 "topics": [{"title": "…", "points": ["…"]}],
 "decisions": [{"text": "…", "quote": "…"}],
 "actions": [{"who": "S2", "what": "…", "due": "", "quote": "…"}],
 "questions": ["…"]}
```

- `max_tokens` ~1,200, temperature low (0.2–0.3) with the family's top-p/top-k and a light repeat penalty (greedy decoding loops, per Yogurt's `sampling.ts`), reasoning off.
- **Labels, not names, in the answer.** People are written as their transcript labels ("S2"), and flit renders them with the current names from `speakers.json`. Renaming a speaker after the notes were written then changes the notes without running the model again. Free text may still contain a name the model saw; a label it invents is shown as written. (Unverified that small models keep to labels; to test. If they don't, fall back to names in the prompt and mark notes `stale` when names change, with a "Write again" button.)
- **Language**: the meeting's language setting, not detection.

## Long meetings

The compact transcript is counted with the model's tokenizer (llama-server's `/tokenize`). If prompt + answer fit the context (16k on 16 GB PCs, 32k above), one request. Otherwise: even parts of ≤ 12k tokens cut between turns (a turn longer than a part at sentences, keeping its label), each part asked for compact JSON of the same schema (≤ 500 tokens), then one final request over the parts' JSON. Progress shows "part 2 of 3".

## Checks in code

- **JSON**: parse; on failure, one retry with the same prompt (the cache makes it cheap); then fail with the transcript untouched.
- **Quotes**: each decision's and action's quote is searched in the transcript (case, punctuation and whitespace normalised; fuzzy enough for one wrong word). Found: the item gets the **time range** of the matching segment (seeking the player works as in search). Not found: the item is kept but marked unverified in the UI, or dropped (to decide on the quality test; AutoDoc drops).
- **Owners**: an action's `who` must be a label present in the transcript, otherwise it becomes unassigned.
- **Clean-up**: empty sections left out; duplicates (same text) merged.

## Files

| Path | Content |
|---|---|
| `sessions\…\notes.json` | the checked notes: the model's fields with labels, time ranges per item, which model and runtime wrote them, when, with which names; `edited` once the user changes them |
| `sessions\…\notes.md` | rendered with names; rewritten when names change |

Notes text, like transcript text, never goes into `debug.log`: the log gets counts and timings only ("notes: 1 request, 14,212 tokens read in 92 s, 846 written in 71 s, 2 actions unverified"). The server's own output is read line by line and only lines matching known status patterns are passed on (to check what the pinned version prints at its default verbosity).

## UI

- **Recording page**: a Notes section above the transcript once notes exist: summary, decisions, actions (owner · task · due), questions, topics folded. Each item with a time links to the player. Copy as Markdown or plain text; "Write again".
- **While writing**: "Writing notes · part 1 of 2" in the recording's row and page; the tray icon's existing transcribing state with its ring.
- **Settings → Notes**: Off / On; the model (Smaller, faster / Better) with its download size and the measured "about N min for a one-hour meeting on this PC"; "Use the graphics processor" (Auto / Off); the load shares the transcription limit. Advanced: "Use another local server" (URL; `localhost` only unless explicitly confirmed).
- **Without a model**: "Copy transcript with notes prompt" on the recording page puts the compact transcript and the prompt on the clipboard for the user's own assistant. Costs nothing and ships first.

## Downloads

Shown before anything downloads, with the measured estimate once the runtime is in:

| Part | Size | When |
|---|---:|---|
| llama.cpp runtime (CPU + Vulkan, pinned release) | to check (tens of MB) | when Notes is turned on |
| Gemma 4 E2B Q4_0 or E4B Q4_0 | 3.35 / 5.15 GB | after the speed check picks one |

## Phases

1. **Copy with prompt** (no model): the compact transcript and prompt; tests for the format.
2. **Notes on the CPU**: `internal/llm` supervisor and client against a fake server in tests, `internal/notes`, one model (E4B or E2B per the experiment), the recording page section, Settings → Notes.
3. **Vulkan and the speed check**: the GPU policy, watchdogs, the estimate in Settings.
4. **Another local server** (the endpoint setting).
5. Later, only if wanted: part notes during live transcription on PCs ≥ 32 GB; questions across meetings.
