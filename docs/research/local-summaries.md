# Meeting notes with a local LLM: what it costs, and how to keep it light

Question (2026-11-22): can flit write a summary, decisions, action items and open questions for a meeting, on the PC, without breaking its principles (small and quiet, local only, accuracy first)? The hardest target is the author's work laptop: no dedicated GPU, an integrated AMD GPU. This note collects what the references do ([Yogurt](../references/yogurt.md), [AutoDoc](../references/autodoc.md#notes-studied-2026-11-22-same-commit), [Handy](../references/handy.md)), what was found elsewhere, a cost model, and the levers that matter. Nothing here is built or measured on an LLM yet; estimates are labelled. The proposed design: [architecture/summaries.md](../architecture/summaries.md), decision: [ADR 0011](../decisions/0011-meeting-notes-with-llama-server.md).

## How much text a meeting is

Counted on flit's test meetings (`testdata/meetings/*/meeting.json`, the reference segments' words, 2026-11-22):

| Meeting | Length | Words | Words per minute |
|---|---:|---:|---:|
| `ami-IS1009a` (English, 4 people) | 839 s | 2,076 | 149 |
| `de-synth-4spk` (German, synthetic) | 322 s | 664 | 124 |

So an hour of meeting is roughly **7,000–9,000 words**. In tokens (a guess until counted with the model's tokenizer: ~1.3 tokens per English word, more for German compounds), that is **~12,000–16,000 tokens** of transcript plus speaker labels. Yogurt's code comment says "an hour's transcript is ~22k tokens" (`app/src/main/notes/readAhead.ts`), so a busier meeting can be half again as long. Planning figure: **15k tokens per hour**, 30k for a dense one.

## Where the time goes

An LLM does two different things, and they hit different limits:

1. **Reading the prompt (prefill)**: all transcript tokens at once, in large matrix products. **Compute-bound**: more cores, wider SIMD or a GPU make it faster. This is the part that grows with the meeting.
2. **Writing the answer (decode)**: one token at a time, each reading all active weights from memory. **Memory-bandwidth-bound**: an integrated GPU shares the same RAM as the CPU, so it helps decode much less than prefill.

For notes, the prompt is 10–30× longer than the answer, so **prefill decides the time on a CPU** and decode decides it once a GPU takes the prefill.

Numbers found (other people's, none on the author's laptop):

| Hardware | Model | Prefill | Decode | Source |
|---|---|---:|---:|---|
| Radeon 780M iGPU, Vulkan | Llama 2 7B Q4_0 | ~850 tok/s | — | a llama.cpp issue ([#13565](https://github.com/ggml-org/llama.cpp/issues/13565), as summarised by search; not re-read) |
| Ryzen AI 7 350 iGPU, ROCm | Gemma 4 E4B Q4_K | 291 tok/s (pp512) | 6.7 tok/s (tg128) | community table ([knightli.com](https://knightli.com/en/2026/04/23/llama-cpp-gpu-benchmark-cuda-rocm-vulkan-scoreboard/)) |
| Raspberry Pi 5 (4 × A76) | Gemma 4 E2B | 133 tok/s | 7.6 tok/s | Google's figure as reported by a search summary ([Gemma 4 E2B QAT post](https://pt13762104.github.io/2026/06/06/taking-a-look-at-gemma-4-e2b-qat/)) |
| Mac (unified) | whatever Yogurt picked | ~360 tok/s | — | Yogurt's code comment |
| none found | CPU-only x86 laptop, 3–4 B | — | — | to measure |

Generation on a CPU can be estimated from memory bandwidth, as Yogurt's picker does: `tok/s ≈ bandwidth / bytes read per token × 0.75` ([references/yogurt.md](../references/yogurt.md#the-notes-model-picked-for-the-computer-packagespicker)). Dual-channel DDR5-5600 is ~90 GB/s on paper, DDR4-3200 ~51 GB/s; Gemma 4 E4B reads ~2.6 GB per token at Q4_0 (half its 5.15 GB file, by its active-parameter share), so **~15–25 tok/s at best**. The 6.7 tok/s measured on the Ryzen AI 7 350 says real laptops can land far below that.

**Estimate for one hour of meeting** (15k tokens in, ~1,000 tokens out; derived from the figures above, *guesses* until the experiment below):

| PC | Model | Prefill | Writing | Total |
|---|---|---|---|---|
| 8-core laptop, CPU only, unlimited | Gemma 4 E4B Q4_0 | 2–5 min (at 50–120 tok/s, guess) | 1–2.5 min | **3–8 min** |
| same, at flit's Balanced cap (50%) | same | ~1.5–2× | ~1.3× | **5–14 min** |
| same, CPU only | Gemma 4 E2B Q4_0 | ~half | ~0.6× | **2–5 min** |
| AMD iGPU through Vulkan | Gemma 4 E4B | 20–50 s | 1–2.5 min | **1.5–3.5 min** |

This is slow next to transcription (an hour of meeting is ~5 min at Balanced, [engine-cores.md](engine-cores.md)), but it runs once, after the transcript, in the background. For flit's "a transcript a minute after the meeting is fine" principle, a summary ten minutes later is acceptable; one that blocks the PC is not.

## The levers, most gain first

1. **Write less.** Decode is the expensive part per token. Notes of ~600–1,000 tokens (short bullets, no prose overview longer than three sentences), **thinking/reasoning off** (Yogurt: `thoughtBudget: 0`, Gemma's wrapper with `reasoning: false`; a reasoning model's hidden tokens cost the same as visible ones), and a hard `max_tokens`.
2. **Read the meeting once.** Put the transcript **first** behind a fixed head, the instructions **after** it (Yogurt's `transcriptHead`). Then the title, the next steps or a "write again with this instruction" are continuations that read only a few hundred new tokens, because llama.cpp keeps the evaluated prefix in its KV cache (`cache_prompt`, on by default in llama-server, [discussion #13606](https://github.com/ggml-org/llama.cpp/discussions/13606)). One prompt with all sections beats one prompt per section.
3. **Feed less text.** The transcript flit gives the model can be smaller than the one it shows: no timestamps per line, consecutive segments of one speaker joined, short labels ("Me", "Anna", "S2"), echo already removed. Guess: 10–20% fewer tokens than the `.txt`. A transcript respelled with the vocabulary and invitees' names (Yogurt's `vocab.ts`) also keeps the model from inventing spellings.
4. **Pick the model by measuring the PC, not by guessing.** A one-time check at download (read 1,024 tokens, write 64; ~1 min) gives real prefill and decode speeds; store them by CPU and GPU name (Yogurt's `speedKey`). From those, predict the minutes per hour of meeting, choose E2B or E4B, and tell the user before downloading 3–5 GB. See [Measure, don't estimate](#measure-dont-estimate).
5. **Let the integrated GPU read the prompt.** Vulkan works on AMD (and Intel) integrated graphics without a vendor runtime. It moves the compute-bound part off the CPU, which also keeps the CPU free and cooler. Needs the Vulkan build of the engine (llama.cpp's Windows Vulkan release; transcribe.cpp's `ggml-vulkan.dll` is 44 MB for comparison). Watch for drivers that hang: Yogurt's watchdogs and step-wise fallback to the CPU ([references/yogurt.md](../references/yogurt.md#the-worker-appsrcengine-appsrcmainenginets)).
6. **Map-reduce only when the meeting doesn't fit.** One read of the whole transcript keeps cross-references ("as Anna said earlier"); parts lose them. With a 16k context, meetings up to ~50–60 min fit whole (estimate above); longer ones go in even parts of ≤ 12k tokens with compact part notes, then one final pass (Yogurt's `enhance.ts`). Parts must keep concrete details (numbers, dates, names) or the final pass can't.
7. **Run it after the transcript, under the same limits.** The notes need the final transcript and the speakers' names, so they run last. Same job object (CPU cap, below-normal priority, threads matched to the cap) as the transcription after a meeting ([ADR 0009](../decisions/0009-quiet-transcription-by-default.md)); one model in memory at a time (the speech worker has exited or gone idle before the LLM loads); the LLM process exits after a short idle time and returns its memory. **Nothing during a recording** on PCs under ~28 GB (Yogurt's line, `queue.ts`).
8. **Check in code, not with the same model.** Ask for each decision and action item with a short verbatim quote, and drop or flag items whose quote isn't in the transcript (AutoDoc's ledger, `notes-evidence-validate.ts`). AutoDoc measured the alternative and removed it: an LLM check of each note against the transcript "judged faithful notes unsupported more often than it caught real inventions, and it added the most processing time" (30 meetings, `notes-claim-verification.ts`).

Not worth it for flit now:

- **Reading ahead during the meeting** (Yogurt's `readAhead.ts`): makes notes instant, but loads the LLM next to the speech model and runs it while the user is in a call. Yogurt allows it only on Macs with ≥ 32 GB. On a laptop it trades the meeting's smoothness for minutes saved afterwards.
- **Speculative decoding** (a small draft model proposing tokens): saves decode time on GPUs, but a second model in memory and little gain on a bandwidth-bound CPU (guess).
- **NPUs.** AMD's Lemonade server splits prefill (NPU) and decode (iGPU) on Ryzen AI chips, but NPU LLM support needs XDNA 2 (Ryzen AI 300/400); the NPUs of Ryzen 7040/8040 have no runtime for LLMs (third-party summaries, [AMD on Lemonade](https://www.amd.com/en/developer/resources/technical-articles/unlocking-a-wave-of-llm-apps-on-ryzen-ai-through-lemonade-server.html), [dev.to guide](https://dev.to/jovan_chan_9500711396d4e6/amd-lemonade-local-llm-server-gpu-npu-inference-on-consumer-hardware-2026-guide-4d0c)). Windows' built-in Phi Silica is limited to Copilot+ PCs (from memory, to check). Both are vendor-specific; flit can reach them through an OpenAI-compatible endpoint (below) without depending on them.
- **Ask across meetings** with an embedding model (Yogurt's `library/`): useful, but a second model and an index; flit's text search covers finding a meeting.

## Models

What the references ship for this job, and what fits a 16 GB laptop (sizes from the catalogs; quality for German **unverified** for all):

| Model | File | Context | License | Used by | Notes |
|---|---:|---:|---|---|---|
| Gemma 4 E2B, QAT Q4_0 | 3.35 GB | 128k | Apache-2.0 (Yogurt's catalog) | Yogurt (agent score 5) | 2.3 B active; the weak-PC choice |
| **Gemma 4 E4B, QAT Q4_0** | 5.15 GB | 128k | Apache-2.0 (Yogurt's catalog) | Yogurt's starter (score 7) | 4 B active; MMLU-Pro 69.4% per its card as reproduced by [Ollama](https://ollama.com/library/gemma4:e4b); multilingual, "English-centric" per one comparison ([mindstudio](https://www.mindstudio.ai/blog/gemma-4-vs-qwen-3-5-open-weight-comparison)) |
| Gemma 4 E4B Q3_K_S | 3.86 GB | 128k | same | Yogurt's "compact" (score 6) | for 16 GB CPU-only PCs in Yogurt's picker |
| Qwen3 4B Instruct | ~2.5 GB at Q4 (to check) | 32k+ | Apache-2.0 | AutoDoc's default | non-thinking variant |
| Llama 3.2 3B | ~2 GB at Q4 | 128k | Llama 3.2 Community | AutoDoc's low-spec | weak outside English (AutoDoc notes it returning the blank example for non-English, `llm.ts`) |
| gpt-oss-20b, MXFP4 | 12.1 GB | 128k | Apache-2.0 | Yogurt on 32 GB | MoE, 3.6 B active: fast decode for its quality, too big for 16 GB |

Qwen 3.5 small models exist (one comparison cites 262k context at all sizes); not checked further. The choice between Gemma 4 E4B and Qwen3/3.5 4B for German meetings is the first thing to measure. Both families come as GGUF, so the engine choice doesn't constrain it.

## Engines (Go, no cgo)

| Option | How | For | Against |
|---|---|---|---|
| **A. `llama-server.exe`** (llama.cpp's own server) as a child process | A pinned llama.cpp release (CPU + Vulkan), downloaded on demand with SHA-256 like models; started by a supervisor on `127.0.0.1`, a random port and `--api-key` per launch; OpenAI-compatible HTTP; exits when idle | Chat templates of every family (Jinja), prompt caching, JSON-schema/grammar constrained output, Vulkan, streaming, no ABI to mirror; the same process isolation as flit's engine worker; works with the Windows job object unchanged | A second ggml copy on disk (size of the CPU + Vulkan zip to check; tens of MB); a local port (mitigated: loopback, random key, `--no-webui`); flags change between releases (pin and test) |
| B. `llama.dll` via `syscall` in flit's worker | As `transcribe.dll` ([ADR 0001](../decisions/0001-stay-with-go.md)) | No port; one engine style | `llama.h` passes and returns structs **by value** (`llama_model_default_params()`, `llama_batch_get_one()`, `llama_decode(ctx, batch)`): possible on Windows x64 (large structs go by hidden pointer) but exactly ADR 0001's "revisit when"; a large, fast-changing API; chat templates only for built-in formats (`llama_chat_apply_template`), no Jinja; we'd own KV reuse and sampling chains |
| C. Ollama (AutoDoc) | Managed child | Mature | Hundreds of MB of runtime with CUDA/ROCm libraries, its own model store and naming; heavier than the feature |
| D. **Any OpenAI-compatible endpoint** the user already runs (Lemonade, Ollama, LM Studio, Foundry Local) | The same HTTP client as A, with a URL | Lets an NPU or a home server do the work; zero download | Not flit's default (local-only must stay true: only `localhost` URLs, or an explicit warning) |
| E. Nothing: copy the transcript with a ready prompt | Clipboard | Zero cost; the user's own assistant | Not "notes in flit" |

A and D are the same client: building A gives D for almost nothing (Handy's `llm_client.rs` does the same for its post-processing providers). E is worth having anyway.

## Measure, don't estimate

Yogurt estimates decode from a bandwidth table and doesn't estimate prefill at all. flit can do better with one short run:

- At the first download of the notes model (and when the CPU or GPU name changes), run the model once on a fixed 1,024-token text: prefill tokens/s, then 64 tokens of decode, under the same CPU cap the real job will have. llama-server's response includes `timings` (prompt and predicted tokens per second; field names to check against the pinned version).
- From the two speeds and the meeting's token count, show "Notes for a one-hour meeting take about N minutes on this PC" in Settings, and choose the smaller model or turn the feature off (with a reason) when N is too high (guess at a bar: 20 min).
- Try Vulkan first and fall back to CPU when it is slower, fails or hangs; remember the outcome per GPU name.

## Experiment plan

1. **Speeds on the author's laptop** (CPU-only and Vulkan iGPU): `llama-bench -p 1024 -n 128` for Gemma 4 E2B Q4_0, E4B Q4_0, E4B Q3_K_S and Qwen3 4B Q4_K_M, at 4 threads and all threads. Also on the 8-thread desktop. Gives the table above with real numbers.
2. **Token counts**: the test meetings' reference transcripts through each model's tokenizer (`llama-tokenize`), in the compact format of lever 3 and as `.txt`.
3. **Quality**: notes for `ami-IS1009a` (English) and `de-synth-4spk` (German), then the author's own meetings, from each model. Score by hand: decisions and action items found / missed / invented, owners right, German grammar. The verbatim-quote check counts what it would drop.
4. **Memory**: peak working set of the server while writing notes for a one-hour meeting, with Teams running, on 16 GB.
5. **Feel**: heat, fan and battery during a notes run at Balanced vs unlimited, and whether the PC stays responsive.

Results go here, then into [ADR 0011](../decisions/0011-meeting-notes-with-llama-server.md) (accept, change or reject).
