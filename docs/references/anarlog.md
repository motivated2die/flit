# anarlog (formerly Hyprnote, Char): enhancing the user's notes, and a native editor

[anarlog](https://github.com/fastrepl/anarlog) (MIT; an `enterprise/` folder has its own license, `LICENSE.enterprise`) is the open-source, local-first meeting notetaker of the team behind Hyprnote and Char. The user takes notes during a meeting, the app transcribes locally, and an LLM "enhances" the notes with the transcript afterwards (the approach Granola made known). Analysed at `7c2a321` (2026-11-21). Paths relative to the repo root. Studied for two things flit needs now: how the user's notes and the transcript are combined in the prompt, and how a native note editor is built.

## Enhancing notes (`crates/template-app/assets/enhance.*.md.jinja`)

The prompt has three templates: a system prompt, a format block, and the user message.

- **Two snapshots of the user's notes.** "Pre-Meeting Notes are a snapshot of what the user had written before the meeting started — agenda items, discussion topics, preliminary questions", "Meeting Notes are the full current state", and "focus on what changed or was added in Meeting Notes compared to Pre-Meeting Notes to understand what the user captured during the meeting". The agenda tells the model the user's intent; what was added during the meeting tells it what the user found worth writing down.
- **The user's formatting is a signal.** "Pay close attention to emphasized text in notes. Users highlight information using four styles: bold, italic, underline, strikethrough." "Recognize H3 headers (### Header) in notes—these indicate highly important topics that the user wants to retain no matter what."
- **Errors on both sides.** "Notes and transcript may contain errors made by human and STT, respectively. Make the best out of every material."
- **Integrate, don't copy structure.** "Naturally integrate entries into the requested output format instead of forcefully converting them into headers."
- **Concrete, no filler.** "Preserve essential details; avoid excessive abstraction"; no "Overview", "Introduction", "Participants" sections unless asked; output only the Markdown, no "Here's the summary".
- **Order of the user message**: context (session, participants), pre-meeting notes, meeting notes, transcript, then an optional output template. The notes come *before* the transcript (Yogurt puts the transcript first, to reuse the model's cache, [yogurt.md](yogurt.md#notes-appsrcmainnotes)).
- **Default format** (`enhance.format.md.jinja`): H1 sections only, at least 3 detailed bullets each, at most one level of nesting, and a final "# Next Steps" with explicit owners, commitments and deadlines when any exist.
- Non-English output keeps technical terms and product names in English; a Korean rule shortens sentence endings.
- **Custom vocabulary** feeds both transcription and summaries (its changelog: "Custom Vocabularies are words that the LLM takes into account when creating meeting summaries").

## Local models (`crates/local-model`, `crates/local-llm-core`)

- Three GGUF models, **only on Apple Silicon** (`SUPPORTED_MODELS` is empty off `aarch64`, `crates/local-llm-core/src/model.rs`): "Anarlog LLM" (`hypr-llm-sm`, 1.1 GB Q4_K_M, "Experimental model trained by the Char team"), Gemma 3 4B Q4_K_M (2.5 GB) and Llama 3.2 3B Q4_K_M (2.0 GB), the last two "Deprecated. Exists only for backward compatibility."
- Elsewhere the user points it at Ollama, LM Studio or any OpenAI-compatible server (`README.md`), and it reads LM Studio's model folder (`crates/lmstudio`).
- So the team settled on **one small fine-tuned model** for notes rather than a catalogue, and didn't ship a local LLM for Windows at all.

## Testing prompts (`crates/template-eval`)

Templates are run against fixed inputs and checked with **structural expectations** in code (`src/eval/expectations.rs`): exact output, single line, must not contain, Markdown structure (headings, lists) parsed with a Markdown parser, JSON patches. Cheap, deterministic checks of the *form*, separate from judging content.

## The native editor (`apps/desktop-gpui/src/editor/`)

The app is moving from a web UI (TipTap/ProseMirror) to GPUI, Zed's native Rust UI framework, and wrote its own editor for it: ~15,600 lines in `editor/` including the transcript view and tests; the editing core is ~5,000 (`document.rs` 627, `model.rs` 1,852, `input.rs` 420, `surface.rs` 1,016, `clipboard.rs`, `html.rs` 281, `sequence.rs`).

- **Document**: a ProseMirror-compatible JSON tree (nodes with `type`, `attrs`, children, text), so documents stay compatible with the web editor's; tasks have a `status` (`todo`, `in_progress`, `done`).
- **Editing model** (`model.rs`): a selection as anchor and head offsets in UTF-16 units; **undo/redo as checkpoints** (the document root and the selection before and after, with a byte budget); an IME **composition** range; a mapping of old to new positions after each edit.
- **Clipboard** (`clipboard.rs`): a copy carries **plain text** (blocks joined by blank lines) and its own JSON slice; a paste reads the app's own slice or **HTML** (`html.rs`), so pasting from browsers and other apps keeps structure.

What it shows for flit: a team with a full editor schema still needed ~5,000 lines for the core. flit's needs are far narrower (H2, H3, nested bullets, tasks, paragraphs), which fit a line-based model rather than a tree ([research/notes-editor.md](../research/notes-editor.md)).

## What we take

- **Notes as the user's intent**: an agenda written before, lines added during; headings and emphasis as importance; "integrate, don't copy".
- **Vocabulary for the summary too**, not only for speech-to-text.
- **Structural checks of prompt output in code**, as a cheap first gate before judging content.
- **Undo as checkpoints, clipboard as plain text plus structure, HTML on paste** for the editor.

## What we leave

- The ProseMirror document schema and its 5,000-line editing core; GPUI; the cloud features (sync, CRM, calendar APIs); the fine-tuned model (Apple-Silicon only, unknown base, not evaluated on German).
