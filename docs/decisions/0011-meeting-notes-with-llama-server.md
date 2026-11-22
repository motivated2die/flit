# 0011: Meeting notes with a small local LLM through llama-server, off by default

- **Status:** proposed
- **Date:** 2026-11-22

## Context

The vision so far ruled out "summaries, action items, notes generation, any LLM pass" ([vision.md](../vision.md)). The author now wants to explore them, with the worry that local summaries take long, need much processing power and large instruction-following models, especially on a laptop without a dedicated GPU (integrated AMD graphics). Research: [research/local-summaries.md](../research/local-summaries.md); references: [Yogurt](../references/yogurt.md) (node-llama-cpp, Gemma 4 / gpt-oss / Qwen3 picked by hardware), [AutoDoc](../references/autodoc.md#notes-studied-2026-11-22-same-commit) (managed Ollama, Qwen3 4B or Llama 3.2 3B, 8k context on Windows).

Constraints: Go without cgo ([0001](0001-stay-with-go.md)); nothing heavy in the tray process; everything local; flit's CPU limits ([0009](0009-quiet-transcription-by-default.md)); German and English.

## Options

### A. llama.cpp's `llama-server` as an on-demand child process

Pinned release (CPU + Vulkan), downloaded when the feature is turned on; started by a supervisor on loopback with a random port and key, in the same kind of job object as the engine worker; exits when idle. OpenAI-compatible API: chat templates, prompt caching, JSON-schema output, Vulkan for integrated GPUs, no ABI to mirror. Costs: a second ggml copy on disk, a local port, release flags to keep pinned.

### B. `llama.dll` through `syscall` in flit's own worker

One engine style, no port. But llama.cpp's C API passes and returns structs by value, changes often, and has no Jinja chat templates; we would also own KV reuse and sampling. That is ADR 0001's "revisit when" case for little gain over A.

### C. Ollama, managed (AutoDoc's way)

Mature, but a large runtime with its own model store, for one feature.

### D. Only an external endpoint or the clipboard

No download; the user's own assistant or local server does the work. Not "notes in flit" for most users.

## Decision (proposed)

**A**, behind an `internal/llm` client that also accepts another local OpenAI-compatible server (D comes almost free), plus "Copy transcript with notes prompt" (no model) as the first step. **Off by default.** When turned on: the runtime downloads, a one-minute speed check measures prompt reading and writing on this PC (CPU and Vulkan), and flit picks **Gemma 4 E4B** or **E2B** (QAT Q4_0, 5.15 / 3.35 GB) and says how long notes for an hour take here. Notes run after transcription and speakers, never during a recording, under the transcription limit, one model in memory at a time, answer as checked JSON with verbatim quotes. Design: [architecture/summaries.md](../architecture/summaries.md).

To accept after the experiment in the research note: speeds on the author's laptop, German quality of E4B vs E2B vs Qwen3 4B, memory with Teams running.

## Consequences

- flit stays small when the feature is off: no runtime, no model, no code path that loads anything.
- Turned on, it adds a 3–5 GB model and a runtime download, and several minutes of background work per hour of meeting on a CPU-only laptop (estimate; 1.5–3.5 min with the iGPU).
- A second inference engine to pin, test and update (llama.cpp releases daily; we update deliberately).
- The vision's "explicitly not" changes to "opt-in".

## Revisit when

- The experiment shows notes for an hour taking over ~20 min at Balanced on the author's laptop, or German notes from 4 B models are not useful: then only D and the clipboard.
- llama-server's flags or API churn costs more than mirroring `llama.h` would (then B).
- transcribe.cpp or Windows itself offers a light text-generation role.
