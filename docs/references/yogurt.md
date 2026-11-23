# Yogurt: local meeting notes with a hardware-picked LLM, and names from the call app

[Yogurt](https://github.com/FrigadeHQ/yogurt) (MIT, by Frigade) records the microphone and the computer's sound, transcribes live with whisper.cpp, writes meeting notes with a local LLM through llama.cpp, answers questions across meetings, and names the people on the other side from what Zoom, Teams or Meet show in their own window. Electron, for macOS, Windows and Linux. Analysed at `71b095a` (2026-11-22, app version 0.3.64 in `app/package.json`). Paths below are relative to the repo root.

MIT, like flit, so code could be taken with attribution. As with Resound and Handy, flit re-implements what it needs in its own code ([decisions/0002](../decisions/0002-name-license-independence.md)); this document describes behaviour and numbers.

## Stack

- Electron 44 + React; main process in `app/src/main`, the notes model's worker in `app/src/engine` (an Electron utility process).
- **Speech**: whisper.cpp's `whisper-server` (v1.9.5, built by `app/scripts/whisper-build.mjs` with `GGML_BACKEND_DL=ON`, `GGML_CPU_ALL_VARIANTS=ON`, `GGML_VULKAN=ON`), spoken to over HTTP on localhost. Models from `catalog/catalog.json`: large-v3-turbo q5_0 (0.57 GB, `gpuOnly`), small q5_1 (0.19 GB), base and tiny as last resorts. On supported Macs, Apple SpeechAnalyzer gives live words.
- **Notes, titles, Ask**: `node-llama-cpp` 3.22.1 (llama.cpp bindings for Node) in the worker process; models in `catalog/catalog.json` (below).
- **Meaning search across meetings**: EmbeddingGemma 300M Q8_0 (334 MB, 512 of its dimensions kept), run by the same worker (`app/src/main/library/`).
- **Call detection and names**: small native helpers. `app/native/yogurt-system` (Rust, Windows and Linux) and `app/native/yogurt-mac` (Swift) answer `mic-apps`, `running-apps`, `browser-title` and `call-watch`, one JSON line per answer.
- Storage: SQLite (`node:sqlite`) in `%APPDATA%\Yogurt` on Windows.
- **No diarization.** The other side is "Them" unless the call app names who is talking. An earlier version told voices apart ("Speaker A, B") and named them with the LLM; that was removed: `app/src/main/store.ts` drops the old `speakers` table and blanks those lines to Them, `app/e2e/onboarding.spec.ts` asserts that no `models/diarizer` folder is created ("no voice models are fetched any more"). The code doesn't say why.

## The notes model: picked for the computer (`packages/picker/`)

The catalog lists LLMs with a hand-given `agentScore` and `chatScore` (1–10), size, total and active parameters:

| id | Build | File | Params (active) | Scores agent/chat | Note |
|---|---|---:|---|---|---|
| qwen3-1.7b | Q8_0 | 1.83 GB | 1.7 B | 2 / 3 | `lastResort` |
| gemma-4-e2b | QAT Q4_0 | 3.35 GB | 5.1 B (2.3) | 5 / 5 | |
| gemma-4-e4b | QAT Q4_0 | 5.15 GB | 8 B (4) | 7 / 7 | `starter` |
| gemma-4-e4b-compact | Q3_K_S (unsloth) | 3.86 GB | 8 B (4) | 6 / 6 | `squeezedOf` e4b |
| gpt-oss-20b | MXFP4 | 12.11 GB | 21 B (3.6) | 6 / 7 | MoE |
| gemma-4-26b-a4b | QAT Q4_0 | 14.44 GB | 26 B (4) | 8 / 8 | MoE |
| gemma-4-26b-a4b-compact | IQ2_XXS (bartowski) | 9.66 GB | 26 B (4) | 5 / 6 | |
| gpt-oss-120b | MXFP4 | 63.39 GB | 117 B (5.1) | 9 / 9 | |

All Apache-2.0 per the catalog; URLs pinned to Hugging Face commits with SHA-256.

`pick.ts` estimates each model on each memory pool and recommends the best that is fast enough:

- **Pools.** A Mac: one unified pool, 70% of memory but leaving 10 GB free above 20 GB. Windows/Linux: the processor's pool is **half the system memory** at an assumed **50 GB/s**; a discrete card adds a GPU pool (95% of its VRAM) and a split pool (layers that fit on the card, the rest from system memory). Bandwidths come from a table by name (`bandwidth.ts`: Apple M-series, RTX 30–50, RX 7600–7900); an unknown card is 300 GB/s, unknown unified graphics 100 GB/s.
- **Fit.** `file + KV cache + 1.5 GB ≤ pool`, with the KV cache estimated at 0.08 GB per 1k tokens, trying 16k then 8k context.
- **Speed.** Generation is treated as memory-bound: `tok/s = bandwidth / (file × active/total params) × 0.75`. **Prompt reading (prefill) isn't estimated at all.**
- **Choice.** Ranked by agent score, then context, chat score, speed; at least **15 tok/s** to be recommended, 5 tok/s to be offered as "smarter"; a last-resort model or a squeezed build only shows where nothing better runs well. `liteMode` below agent score 5.

What that gives for PCs like the author's work laptop (computed by hand from these formulas, not run):

| PC | Pool | Recommended | Also offered |
|---|---|---|---|
| 16 GB, no usable GPU | CPU 7.8 GB, 50 GB/s | Gemma 4 E4B Compact, 16k, ~19 tok/s | "smarter": E4B at 8k, ~15 tok/s |
| 32 GB, no usable GPU | CPU 15.8 GB | gpt-oss-20b, 16k, ~18 tok/s | "smarter": E4B |
| 16 GB, AMD integrated graphics | unified 11 GB, 100 GB/s (default) | Gemma 4 E4B, 16k, ~29 tok/s | |
| 8 GB | CPU ~3.9 GB | nothing fits, or Qwen3 1.7B at 8k depending on the exact memory reported | setup says the PC is too weak (`localCheck.ts`) |

The one public number found for a similar iGPU contradicts the 29 tok/s: Gemma 4 E4B Q4_K on a Ryzen AI 7 350's Radeon (ROCm) is listed with **pp512 291 tok/s, tg128 6.7 tok/s** (a community llama-bench table, [knightli.com](https://knightli.com/en/2026/04/23/llama-cpp-gpu-benchmark-cuda-rocm-vulkan-scoreboard/); unverified, one machine). Estimating by bandwidth alone is a weak guide on laptops; flit should measure ([research/local-summaries.md](../research/local-summaries.md#measure-dont-estimate)).

Other setup rules:

- **Speech model by measurement** (`asr.ts`, `asr/speedCheck.ts`): on Windows and Linux every candidate is timed once on a bundled 15.4 s clip (beam 5, the app's real launcher). It keeps up if it takes **≤ 0.5× the clip** (two tracks live); `small` may take up to 1.25×. Turbo is never tried on the CPU alone ("measured on 2 cores it took 4 to 9 times the clip's length"). The result is stored by CPU and GPU name and re-run when either changes.
- **Below 4 cores without a GPU**, setup refuses before downloading anything (`MIN_CPU_CORES`, `localCheck.ts`): with fewer cores than whisper's 4 threads it "slowed 5 to 200 times".
- **A small first model, the better one later** (`setup.ts`): when the catalog's `starter` is smaller than the pick, it downloads first so notes work sooner; the recommended model follows in the background and replaces it once it has loaded.

## The worker (`app/src/engine/`, `app/src/main/engine.ts`)

- One utility process, started on first use, **one request at a time** (`serial.ts`). Requests: load, unload, count tokens, chat (streamed), preload (read a prompt in without answering), embed, probe the GPU.
- **Loading falls back step by step** (`llm.ts`): all layers on the GPU; then only as many as leave room for the smallest accepted context (8k); then none; then a CPU-only llama.cpp build. After the load, one token is evaluated so the weights are paged in before the first real request.
- **GPU watchdogs** (Windows/Linux, `engine.ts`): a probe over 90 s, a load over 5 min or a first token over 5 min on the GPU counts as stuck; the worker is killed and restarted without the GPU (`YOGURT_ENGINE_GPU=off`). Vulkan's `ErrorDeviceLost` does the same. CPU limits are 15 and 30 min.
- **Threads** (`threads.ts`): off a Mac, the CPU's math cores, never more than it has. node-llama-cpp otherwise asks for at least 4, and llama.cpp's spinning threads "took 40 s a word" on a 2-core Windows PC.
- **Sampling per family** (`sampling.ts`): Gemma temp 1.0/top-p 0.95/top-k 64, gpt-oss 1.0, Qwen and unknown 0.6/0.95/20, all with a light repeat and presence penalty because greedy decoding "made long agent turns fall into word-for-word loops". Titles use 0.3, folder suggestions 0.
- **Thinking off.** Gemma 4's chat wrapper with `reasoning: false`, `thoughtBudget` 0; `control.ts` strips control tags (`<|channel>`, made-up `<tool_response>`) that small models sometimes write as text.
- **One context sequence for the model's whole life**, so a prompt that starts like the last one only reads what is new.

## Notes (`app/src/main/notes/`)

**Prompt shape** (`enhance.ts`): the transcript comes first, behind a fixed head (`transcriptHead`), and everything that can change after the meeting (title, invited people, the person's own typed notes, rules) comes after it. The head is never changed for one meeting, so a prompt read in earlier stays reusable. The rules ask for Markdown: one `##` section per topic named specifically, bullets of up to ~20 words "starting with the point", decisions and open questions in their topic, nothing off-topic (small talk, weekends), `**bold**` rarely, "never make anything up", then `## Next steps` as `- **Name:** task, by when` only if someone committed to something. "Them" is not a name; with exactly one other invitee, Them is that person. A meeting where under 10% of words came from the call is treated as a room on one mic.

**Budgets**: context 32k when memory allows (8k minimum); 2,048 tokens for the notes; long meetings in parts of at most 12,000 tokens, each summarised in ≤ 700 tokens ("compact notes on this part only … every topic with its concrete details"), then the notes are written from the part summaries. Parts are evened out ("even parts read better than one full part and a scrap"); a turn longer than a part is cut at sentences, keeping who said it.

**Follow-ups reuse the read prompt**: if the model stopped before `## Next steps`, it is asked for just that section as a continuation of the same conversation (≤ 400 tokens); the title is asked the same way (≤ 40 tokens, temp 0.3). Each costs a few tokens instead of re-reading the meeting.

**Clean-up in code, not by asking**: `tidyNotes` drops "Here are your notes" lines, code fences, closing remarks and blank lines; `withoutCopies` removes bullets that merely copied the person's own labels; `markMine` marks bullets that restate what the person typed; `cleanTitle` strips quotes, "Title:", and the owner's own name.

**Vocabulary first** (`vocab.ts`): names of invitees, companies from their email domains and the user's jargon are respelled into the transcript and given to the model as a spelling line before it writes.

**Queue and memory** (`queue.ts`): one queue for all model work, in priority order: a question someone waits for, then notes and titles, then reading ahead, then indexing and folder suggestions. **Recording always wins.** Below 28 GiB of RAM (`isSmallMac`, despite the name applied on every OS) nothing runs while a meeting records: a running job is stopped and re-run later, the model is unloaded, and whisper is let go before the notes model loads; the model loads with 16k context and leaves 1 minute after the last job (10 minutes on bigger machines). On bigger machines jobs run during recordings but stop when transcription falls 30 s behind and resume under 10 s. The notes wait up to 60 s for the call's names after the transcript is final (`NAMES_WAIT_MS`).

**Reading ahead** (`readAhead.ts`): while a meeting records, every 3 minutes of settled transcript (lines that ended ≥ 60 s ago, the last turn held back) is read into the model as the start of the notes prompt, in steps of ~8,000 characters so anything more urgent waits only seconds. A long meeting gets its finished parts summarised during the meeting instead. When it ends, only the tail and the instructions are new, so the notes "start within seconds of stopping instead of a minute later" (the code's comment: "an hour's transcript is ~22k tokens, read at ~360 a second", on a Mac). **Only on Macs with ≥ 32 GB**: off a Mac the model and whisper shared the GPU's memory and "a long call then ran the card flat out and could freeze the computer (#37)".

**Rewrites**: notes are written again on their own (quietly, swapped in when done) when the people change, unless the user edited them; "Write again" takes an optional instruction that may change format, length or language but "never invent facts".

## Names from the call app (`app/src/main/callNames/`, `app/native/yogurt-system/src/wincalls.rs`)

While a meeting records, a helper process reads the call app's **UI Automation** tree (Windows), the Accessibility tree (macOS, needs permission) or AT-SPI (Linux), every **400 ms**, and prints the windows as JSON. Windows needs no permission.

- **Which windows**: only the processes of the app that holds the microphone (two Zoom meetings are two processes, #98); Zoom only windows titled "Zoom Meeting…"/"Zoom Webinar…"/"Meeting", browsers only titles with the word "Meet", Teams any window but only if its tree contains a call control ("Leave", "Mute mic", "Hang up", or the language-independent id `hangup-button`), so the calendar and chat windows are never copied.
- **How** (`wincalls.rs`): `CUIAutomation8`, connection and transaction timeouts of 1.5 s, **one cache request per window** (control type, name, help text, automation id, class name; subtree scope; raw view), so the whole tree comes over in one cross-process call. At most 4 windows, 4,000 elements, depth 40, texts cut at 300 characters; edit fields' values are never asked for. The loop sleeps so a slow read happens less often (`max(interval, 2 × read time)`). The helper runs at low priority; a reader quiet for 5 s is restarted (at most 20 times a meeting).
- **What each app gives** (`parse.ts`):
  - **Zoom**: each video tile's name reads like "Ana Ruiz, Computer audio unmuted, Video off, Active speaker"; the participants panel has rows "Name (me)", "(Host)"; the floating window says "Talking: Name". Strings for 10 languages are in the code. Zoom marks an active speaker only with 3+ people.
  - **Teams** (new Teams is WebView2): **no word or class marks the active speaker**, only a visual ring (measured on Teams for Linux 2.28). Names come from the People panel when it is open ("Dana Smith, Organizer, Unmuted" rows in a list named Participants/Attendees/People), "(You)" marks the user, and some builds label "Name, speaking".
  - **Meet** (Chrome, Edge, Brave, Arc, Firefox): no word marks the speaker either; the talking tile carries an obfuscated CSS class (`v5h6Xc` as of October 2026) that Chromium exposes as the UIA class name. A tile is "the element seven levels above a name's text". Google changes the class between releases.
  - **Webex**: read on macOS only; on Windows its lines stay Them.
- **Attribution** (`attribute.ts`): samples are moved onto the meeting's time line (capture start, minus ~750 ms for the app's own lag). A line of the other side gets a name only if **one person was marked talking for more than half of it** (a reading counts for at most 2 s), or if the call has **exactly two people** (the user and one other). Anything else stays Them: "Nothing is guessed". After the meeting every Them line is attributed again with the whole call known, and everyone seen in the call is added to the meeting's people.
- **Logs**: counts only ("talking 1 of 4"), and a redacted picture of the first window's tree where every non-app word is replaced by its length, because "main.log is pasted into public issues, and names belong to the people in the call".

## Other things worth knowing

- **Microphone in use, two signals** (`yogurt-system/src/win.rs`): active capture sessions (as flit), plus the privacy **ConsentStore** keys under `HKCU\…\CapabilityAccessManager\ConsentStore\microphone` (`LastUsedTimeStart` set, `LastUsedTimeStop` zero), counted only while that app still runs.
- **Ask across meetings** (`library/`): meetings chunked (~300 words), embedded with EmbeddingGemma, retrieved by meaning and words, answered with citations; indexing runs last in the queue and never while recording.
- **Local MCP server** (`app/src/main/mcp/`, off by default): other apps on the PC can read meetings, notes and transcripts over a named pipe with a random name per launch.

## What we take

- **Measure the PC, then pick the model.** The idea of a catalog with sizes, active parameters and quality scores, a memory fit test, and a one-time speed check stored by CPU/GPU name. We'd measure the LLM too (prompt reading and writing speed), not estimate it from bandwidth.
- **Transcript first, instructions last**, a fixed head, follow-up questions (next steps, title) as continuations: the cheapest way to get more than one answer out of one read of the meeting.
- **Map-reduce with even parts** for meetings longer than the context, with compact part summaries that keep concrete details.
- **Clean-up and checks in code** rather than more prompt words: strip lead-ins, fences, closing remarks; check names.
- **One queue, recording always wins, unload when idle**, and the 28 GiB line below which nothing heavy runs during a meeting.
- **Step-wise GPU fallback and watchdogs** on Windows: a Vulkan driver that hangs or loses the device must never cost the notes.
- **Names from the call app's UI Automation tree**, with Yogurt's rule that every parser fails towards "Them" and nothing names a person on weak evidence; the window filters (call controls, title words); the redacted log.
- **Invitee names and email domains as vocabulary**, for the transcript and for the notes.

## What we leave

- Electron, node-llama-cpp, whisper.cpp (flit has transcribe.cpp), SQLite.
- Live whisper on short chunks; turbo needing a GPU.
- Reading ahead into the LLM during meetings on laptops (Yogurt itself limits it to 32 GB Macs).
- Ask across meetings and the embedding model, the MCP server: possible later, not part of a first summary feature.
- **Giving up diarization.** Names from the call app work per line and only when the app marks a speaker. flit already has Sortformer's stable speaker numbers; the call app's evidence can name those for the whole meeting ([research/call-app-names.md](../research/call-app-names.md)).

## From its issue tracker (read 2026-11-22)

How the call names hold up in use, from public issues and pull requests (details and quotes in [research/call-app-names.md](../research/call-app-names.md#the-accessibility-trees)):

- [#83](https://github.com/FrigadeHQ/yogurt/issues/83): Windows, Teams desktop: 0 of 4,014 remote lines named in 8 calls on 0.3.48; the reader hung and restarted every 6 s in one call; Teams in Chrome had an empty web tree.
- [#112](https://github.com/FrigadeHQ/yogurt/issues/112): no Meet names while its window is in the background.
- [#78](https://github.com/FrigadeHQ/yogurt/issues/78), PR [#86](https://github.com/FrigadeHQ/yogurt/pull/86): voice separation is coming back as a fallback (sherpa-onnx, pyannote + ERes2Net; "about 25% speaker error", "about 3 min … after a 20 min meeting, on one CPU core"); Zoom 7.2.5 on macOS no longer marks an active speaker.
- PR [#97](https://github.com/FrigadeHQ/yogurt/pull/97): Zoom's live captions name the speaker when no tile is marked; 0 → 6 of 29 lines in a replayed 7-person call.
