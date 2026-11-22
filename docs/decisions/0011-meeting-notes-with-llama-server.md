# 0011: Summaries on request, by a small local LLM through llama-server, on the GPU where it helps

- **Status:** proposed
- **Date:** 2026-11-22 (revised the same day: on request only, accelerators, lighter models, the extract-then-write baseline)

## Context

The vision ruled out "summaries, action items, notes generation, any LLM pass" ([vision.md](../vision.md)). The author now wants them, with conditions: heavy processing only after the meeting and only when the user asks for it on a recording; as much use of local accelerators (integrated or dedicated GPUs) as possible; a model chosen for the PC, erring towards lighter ones; German and English; the user's own notes taken into account. Research: [research/local-summaries.md](../research/local-summaries.md) (runtime, models, accelerators, cost), [research/summary-pipeline.md](../research/summary-pipeline.md) (how). References: [Yogurt](../references/yogurt.md), [AutoDoc](../references/autodoc.md), [anarlog](../references/anarlog.md).

Constraints: Go without cgo ([0001](0001-stay-with-go.md)); nothing heavy in the tray process; local only; flit's CPU limits ([0009](0009-quiet-transcription-by-default.md)).

## Options

### Runtime

1. **llama.cpp's `llama-server`** as an on-demand child process: one pinned build with the CPU and **Vulkan** backends (37 MB zipped for x64 Vulkan, `b12633`), which covers AMD, Intel and NVIDIA GPUs, integrated or dedicated, without vendor runtimes; OpenAI-compatible API, chat templates, prompt cache, JSON schema, token probabilities.
2. `llama.dll` through `syscall`: one engine style, but structs by value, a large API, no Jinja templates.
3. Ollama (AutoDoc): a large runtime and model store for one feature.
4. **Windows ML / Foundry Local** (ONNX Runtime with vendor execution providers, including NPUs): broad hardware reach, but ONNX models, a second format and runtime, vendor-dependent paths.
5. Only another local server or the clipboard.

### When it runs

a. After every meeting, automatically (Yogurt, AutoDoc). b. **Only when the user asks, after the meeting.**

### How it works

A. One pass over the whole transcript. B. Map-reduce with prose part notes. **C. Extract per section (JSON with quotes), check in code, then one writer call.** D. Classify turns by token probabilities first, extract only from flagged ones.

## Decision (proposed)

Runtime **1**, with the same client reaching a user's other local server (5, which also covers Foundry Local and Lemonade); **b**; pipeline **C** as the baseline, A as its one-section case for comparison, D to try later. Hardware is read first (RAM, adapters through DXGI); the model must fit a memory cap (about a quarter of RAM without dedicated graphics); the **lighter** fitting candidate is the default, the next one offered as "better, slower"; a one-time speed check per model and adapter picks CPU, Vulkan with layers in system memory (`-ngl 0`) or full offload, and predicts the minutes per hour of meeting. First candidates to measure: Qwen3.5 2B/4B, Gemma 4 E2B/E4B, Granite 4.0 H Micro. Design: [architecture/summaries.md](../architecture/summaries.md).

**First version built and measured on the desktop's CPU (2026-11-22, [summary-experiments.md](../research/summary-experiments.md))**, which changed the plan in three points: (1) Qwen3.5 4B is the default writer where there are 15 GB of memory (the only candidate with usable German notes; ~7 min per hour of meeting at 4 desktop threads), Qwen3.5 2B below; Granite 4.0 H Micro is out; (2) the single pass (A) beat extract-then-write (C) on quality and time for a 14-minute meeting, so A is the default up to ~50 minutes; (3) each `@flit` question gets its own short call on its excerpt. The desktop's GPU failed in Vulkan (`ErrorDeviceLost`), so the CPU fallback is part of the first version, not a later addition.

To accept: the speed and memory numbers on the author's laptop (CPU and its integrated AMD GPU), and notes quality on AMI IS1009a–d (actions and decisions found, invented, owners) and German meetings, for two or three candidates.

## Consequences

- With summaries never used, nothing downloads and nothing runs.
- Asked for, a ~37 MB runtime and a ~1.5–3 GB model download once; a summary takes minutes in the background (estimate for one hour of meeting: a few minutes with the iGPU, longer on CPU only), at the transcription limit.
- A second inference engine to pin, test and update; llama.cpp releases daily, flit updates deliberately.
- GPU drivers can hang or lose the device; watchdogs and a CPU fallback are part of the supervisor.
- The vision's "explicitly not" becomes "on request".

## Revisit when

- Measurements show over ~15 minutes per hour of meeting on the author's laptop with the lightest acceptable model, or German output from small models isn't useful: then only the clipboard and another server.
- An NPU path (Windows ML) becomes broad enough on the laptops flit targets to be worth a second format.
- llama-server's interface churns more than a direct binding would cost.
