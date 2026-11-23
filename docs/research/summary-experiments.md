# Summaries with small local models: first measurements

Question (2026-11-22): which of the candidate models ([local-summaries.md](local-summaries.md#models)) and which pipeline ([summary-pipeline.md](summary-pipeline.md#approaches-to-compare)) give useful notes for a meeting, how fast, on which hardware? This note holds what was run and seen. The code: `internal/llm`, `internal/summary`, `go run ./cmd/flitdev summary` ([BUILDING.md](../BUILDING.md#summaries)). Design: [architecture/summaries.md](../architecture/summaries.md).

## Setup

- **Runtime**: llama.cpp `b12633`, the Windows x64 Vulkan release (CPU backends included), started by `internal/llm` as `llama-server` with one slot, an 8k context, thinking off (`--reasoning off`), `temperature 0.3, top_p 0.9, min_p 0.05, presence_penalty 0.3`, seed 42.
- **Models** (all Apache-2.0, pinned in `internal/models`): Qwen3.5 2B Q4_K_M (1.28 GB), Qwen3.5 4B Q4_K_M (2.74 GB), Gemma 4 E2B Q4_0 (2.84 GB), Granite 4.0 H Micro Q4_K_M (1.94 GB).
- **PC**: the author's desktop, Ryzen 7 5800X3D (8 cores, no SMT, AVX2), 32 GB DDR4, Radeon RX 7800 XT; Windows 11 build 26300. **CPU only** in every run below: see [the GPU](#the-gpu-on-the-desktop).
- **Meetings**:
  - `interview-2026-11-22`: the author's own test recording (6:35, German, two people from a video plus the author; 1,600–1,700 tokens), with notes typed during it: three H3 sections, a few bullets and **three `@flit` questions**. Private: copied to `testdata/private/` (git-ignored), not quoted here beyond what's needed.
  - `ami-IS1009a` (14 min, English, four people, AMI reference transcript, CC BY 4.0) with notes written for the test (`testdata/notes/ami-IS1009a.md`, times in `.json`): an agenda, two facts, two `@flit` questions, one task line. AMI's human summary of the meeting is the reference (from `ami_public_manual_1.6.2.zip`, `abstractive/IS1009a.abssumm.xml`).

## The GPU on the desktop

On first try (2026-11-22), Vulkan didn't start on this PC: `llama-server` listed the GPU (`Vulkan0: AMD Radeon RX 7800 XT (16368 MiB, 15408 MiB free)`) but loading a model failed at `vk::PhysicalDevice::createDevice: ErrorDeviceLost`, before any layer was offloaded, even with every optional `GGML_VK_DISABLE_*` flag switched off and in `b11000` (2026-09-16) alike.

**Root cause (found 2026-11-23):** third-party **Vulkan implicit layers** registered in `HKLM\SOFTWARE\Khronos\Vulkan\ImplicitLayers` — specifically Twitch Studio's `VK_LAYER_Twitch_Overlay` (API version 1.1.0, `TwitchNativeOverlay64.dll`), alongside OBS (`VK_LAYER_OBS_HOOK`, 1.2.131), EOS and Steam layers — were injected by `vulkan-1.dll` (1.4.357.0) into `llama-server`'s Vulkan 1.4 `vkCreateDevice` chain (`[Vulkan Loader] ERROR: vkCreateDevice: Failed to create device chain.`). Disabling `VK_LAYER_Twitch_Overlay` alone (`DISABLE_TWITCH_VULKAN_OVERLAY=1`) or disabling all implicit layers (`VK_LOADER_LAYERS_DISABLE=~implicit~`) fixed it immediately. The speech worker already set `VK_LOADER_LAYERS_DISABLE=~implicit~` (`internal/engine/supervisor.go`, from Handy's lesson in [handy.md](../references/handy.md#7-smaller-details)), and `internal/llm/server_windows.go` now sets the same for GPU runs (respecting any explicit `VK_LOADER_LAYERS_DISABLE` already in the environment).

`llm.StartFallback` stays as the safety net: a GPU that fails for any other reason falls back to the CPU, and the summary records the device it actually ran on. Yogurt does the same on `ErrorDeviceLost` ([yogurt.md](../references/yogurt.md#the-worker-appsrcengine-appsrcmainenginets)).

### Measured on the desktop's RX 7800 XT (Vulkan, 2026-11-23)

With `VK_LOADER_LAYERS_DISABLE=~implicit~` and full offload (`-ngl 999`), `llama.cpp b12633`:

- `flitdev summary -speed` (first run includes Vulkan shader compile for that architecture): Qwen3.5 2B **329 read / 91.3 write t/s** (loaded 5.4 s); Qwen3.5 4B **2,068 read / 116.5 write t/s** (loaded 2.1 s); Gemma 4 E2B **349 read / 150.3 write t/s** (loaded 5.7 s). In `gpu-prefill` (`-ngl 0`), Qwen3.5 4B measured **528 read / 11.8 write t/s** (loaded 2.0 s).
- Full summaries (Qwen3.5 4B, `single`, `ctx 8192`):

| Meeting | Setup | Wall | Read | Read/s | Written | Write/s | Peak RAM |
|---|---|---:|---:|---:|---:|---:|---:|
| AMI IS1009a (14:00, en, 2 questions) | GPU, unlimited | **9.1 s** | 6,168 | 1,268–2,558 | 692 | 113–117 | 3.6 GB |
| AMI IS1009a (14:00, en, 2 questions) | GPU, `-cpulimit 50 -threads 4` | **8.9 s** | 6,168 | 1,787–2,584 | 692 | 107–115 | 3.6 GB |
| interview (6:35, de, 3 questions) | GPU, unlimited | **8.0 s** | 4,659 | 1,511–2,331 | 655 | 115–118 | 3.8 GB |

~16.5× faster than the 8-thread CPU run (150 s → 9 s on AMI, 130 s → 8 s on the interview; ~20 s estimated for a one-hour meeting), with lower system RAM working set and no slowdown from flit's default 50% CPU cap. Progress bar pacing stayed linear on the interview (`ask 1` 10% at 0.8 s, `ask 2` 22% at 1.8 s, `ask 3` 35% at 2.6 s of 8.0 s).

## Round 1: the interview, everything in one prompt

The first pipeline put the `@flit` questions into the main prompt next to the transcript and notes (single pass, A), or into the section's extraction JSON (extract-then-write, C). CPU, 8 threads.

| Model | Approach | Wall | Read | Read/s | Written | Write/s | Peak memory |
|---|---|---:|---:|---:|---:|---:|---:|
| Qwen3.5 2B | single | 42 s | 2,328 | 189 | 542 | 18.0 | 2.0 GB |
| Qwen3.5 2B | extract | 59 s | 3,105 | 209 | 845 | 19.0 | 2.1 GB |
| Gemma 4 E2B | single | 48 s | 2,383 | 274 | 502 | 12.6 | 2.7 GB |
| Gemma 4 E2B | extract | 79 s | 2,924 | 276 | 999 | 14.6 | 2.7 GB |
| Granite 4.0 H Micro | single | 57 s | 2,404 | 88 | 285 | 9.4 | 3.7 GB |
| Granite 4.0 H Micro | extract | 148 s | 3,084 | 79 | 1,008 | 9.3 | 4.0 GB |
| Qwen3.5 4B | single | 100 s | 2,328 | 91 | 579 | 7.8 | 4.5 GB |
| Qwen3.5 4B | extract | 188 s | 3,328 | 78 | 1,132 | 7.8 | 4.8 GB |

(Peak memory is the server's working set: model, KV cache and compute buffers; mapped model pages count.)

What came out, judged by hand against the transcript:

- **Qwen3.5 4B, single**: the best. All three questions answered correctly (the fine: on a friend's moped without a helmet, 30 €; the language game: abbreviating sentences; the party city: Mannheim, with Ibiza "even better"). The user's headings reused as sections; facts mostly right, one wrong attribution (who wrote the sender's address). It wrote "no decisions / no tasks / no open questions" sections instead of leaving them out.
- **Qwen3.5 2B, single**: usable outline, the questions half right, details mixed up (the 30 € fine merged with "paid off the books"; "a photo with a motorbike on her head").
- **Qwen3.5 2B, extract**: worse than its single pass. The extraction **mixed the questions up with the note lines**: question 2 answered with a note's content, question 3 with a line about the police; an "attack" invented. The writer then reported all three as unanswered.
- **Gemma 4 E2B**: the questions right, but its Markdown broken (`- **** text` on every bullet) and lines repeated.
- **Granite 4.0 H Micro**: copied the user's note lines word for word, ignored the questions, invented a task with a deadline ("within the next two weeks"). Dropped from the candidates.

Lessons: (1) a small model handles **one job at a time** far better than several in one prompt; (2) extract-then-write adds a second chance to go wrong for short meetings and costs more writing; (3) sections the model fills with "none" must go in code; (4) the size step from 2B to 4B matters more than any prompt change seen so far.

## Changes after round 1

- **One call per `@flit` question**, on the excerpt from 3 minutes before to 30 seconds after the time it was typed (a question typed after the meeting: the part sharing most words with it, or the whole transcript when that is short). One or two sentences back, max 160 tokens; `Assemble` puts them under "Deine Fragen" / "Your questions". The writer no longer sees the questions.
- **`Assemble`** drops sections whose lines all say there's nothing ("Keine Entscheidungen getroffen.", "None."); **`Tidy`** strips empty bold, copied placeholder headings, fences, lead-ins.
- Note anchors only for list and paragraph lines, not headings.

## Round 2: questions on their own, current prompts

CPU, 8 threads. "Read" includes the question calls (one per `@flit` question, 760–1,560 tokens each) and the writer's prompt (transcript, notes, instructions).

| Meeting | Model | Approach | Wall | Read | Read/s | Written | Write/s | Peak memory |
|---|---|---|---:|---:|---:|---:|---:|---:|
| interview (6:35, de, 3 questions) | Qwen3.5 2B | single | 54 s | 4,659 | 205 | 645 | 20.5 | 2.3 GB |
| | Qwen3.5 4B | single | 130 s | 4,659 | 84 | 685 | 9.1 | 5.2 GB |
| | Gemma 4 E2B | single (round-2 prompts) | 36 s | 4,658 | 291 | 314 | 15.5 | 2.8 GB |
| AMI IS1009a (14:00, en, 2 questions) | Qwen3.5 2B | single | 60 s | 6,168 | 212 | 562 | 18.3 | 2.2 GB |
| | Qwen3.5 4B | single | 150 s | 6,168 | 85 | 644 | 8.4 | 4.9 GB |
| | Gemma 4 E2B | single | 58 s | 6,383 | 282 | 501 | 14.1 | 2.8 GB |
| | Qwen3.5 4B | extract, 4 sections of 1,200 | 318 s | 7,924 | 84 | 2,002 | 9.0 | 5.9 GB |

The writer's prompt for AMI's 14 minutes was 3,695 tokens: **~15,800 tokens per hour** of this meeting's density, in line with the planning figure.

**Qwen3.5 4B**, by hand against the transcripts and AMI's human summary:

- *Interview*: all three questions answered right (one earlier run said "not answered" to the second; naming the question before the excerpt as well fixed it). Topic sections cover everything said; three detail errors (a "fall of 100 metres" for a 100-metre ride, Mannheim and Ibiza swapped in one answer, who wrote the sender's address). No empty or misread sections.
- *AMI*: about 13 of the 18 sentences of AMI's summary covered: the four people with the right names and roles (learnt from the introductions; the transcript only has "Them 1–3"), "original, trendy, user friendly", price 25 €, profit 50 M€, cost ≤ 12.50 €, compact and multi-purpose, the lost-remote signal by light sensor (beep or light), the industrial designer's and the marketing manager's tasks. Missing: the whiteboard warm-up, colours/materials/shapes, "meet again in thirty minutes", the doubt about the budget. One error: clapping and voice "deemed unreliable" (left open in the meeting). Both questions right.

**Qwen3.5 2B**: English usable with more errors (profit "50 Euro" where its own answer says 50 million, the marketing task given to the project manager, an idea stated as a feature, tasks as plain bullets); German weak: invented decisions and facts in every run.

**Gemma 4 E2B**: the best at the questions (all five right), the fastest at reading; but its notes repeat the user's own lines in bold without adding much (interview), or take the whiteboard warm-up for tasks ("Draw a snake on the whiteboard", AMI), and its Markdown needs repair (`- **** `, `- - [ ]`).

**Extract-then-write** (4B, AMI in four sections): twice the time of the single pass (2,002 tokens written against 644) and worse notes: decisions like "Them 1.", the speakers as "Them 1/2" (the sections can't see the introductions), whiteboard trivia as tasks and ten "open questions", the items' labels copied ("Decided: …"). For meetings that fit the context, the single pass is better in every respect measured. Changed since: items are grouped under plain headings for the writer (no per-line labels to copy), and decisions and tasks under three words are dropped. Extract stays for meetings over ~50 minutes and needs a better design before it is good (open question).

## Speed: what an hour costs

Speeds from `flitdev summary -speed` (1,000-token read, 48 written) with **4 threads** on the desktop, a stand-in for a laptop's CPU (a guess: the work laptop's own numbers are still to measure): Qwen3.5 4B reads 63 tokens/s and writes 9.6; Qwen3.5 2B 167 and 24.3; Gemma 4 E2B 238 and 13.1. With 8 threads the 4B read 85 and wrote 8.4–9.1: reading scales with cores, writing is memory-bound and gains nothing.

Writing hardly grows with the meeting (the notes are 550–700 tokens for 6 or 14 minutes, capped at 1,200), reading does. So the per-hour figure the dev command prints ("linear") overstates long meetings; the model is:

    time ≈ (15,800 + 1,300 × questions) / read speed + (~1,000 + 50 × questions) / write speed

| One hour, three questions | Read | Write | Total |
|---|---:|---:|---:|
| Qwen3.5 4B, desktop 4 threads | 19,700 / 63 = 313 s | 1,150 / 9.6 = 120 s | **~7 min** |
| Qwen3.5 4B, desktop 8 threads | 19,700 / 85 = 232 s | 1,150 / 8.4 = 137 s | ~6 min |
| Qwen3.5 2B, desktop 4 threads | 118 s | 47 s | ~3 min |
| Qwen3.5 4B, a laptop half as fast (guess) | | | ~14 min |

Within the author's 15 minutes per hour for the 4B if the laptop's CPU is at least half the desktop's at 4 threads; the iGPU reading the prompt would shorten the larger part. Two things not yet measured: reading speed at 16k tokens of context (measured up to 3.7k: no slowdown; Qwen3.5's linear-attention layers should keep it flat, unverified), and the laptop itself.

## Measured on the work laptop (Ryzen 5 5500U + Radeon Vega 7 iGPU, 2026-11-23)

Hardware: Lenovo ThinkPad `20YHS02900` (Windows 11 Enterprise 26200, non-admin), AMD Ryzen 5 5500U (6C/12T, 15 W TDP), **16 GB RAM** (14.82 GiB Windows-visible physical RAM after 1 GiB UMA carve-out), AMD Radeon(TM) Graphics (Vega 7, Vulkan 1.3.260). `llama-server b12633` with `VK_LOADER_LAYERS_DISABLE=~implicit~` enumerates `Vulkan0: AMD Radeon(TM) Graphics (8357 MiB, 7939 MiB free)` and runs cleanly on `gpu` (`-ngl 999`) and `gpu-prefill` (`-ngl 0`) without device loss or CPU fallback.

### Short-burst speed (`flitdev summary -speed`, ~1,000 read / 48 write tokens)

| Model | Device | Unlimited (read / write / load) | Balanced `-cpulimit 50 -threads 6` (read / write / load) |
|---|---|---|---|
| Qwen3.5 2B | **`gpu` (`-ngl 999`)** | 109* / 16.7 t/s (6.3 s) | **172 / 18.5 t/s (3.5 s)** |
| Qwen3.5 2B | `gpu-prefill` (`-ngl 0`) | 133 / 19.1 t/s (2.1 s) | 153 / 18.3 t/s (2.0 s) |
| Qwen3.5 2B | `cpu` | 126 / 19.6 t/s (2.0 s) | 106 / 17.6 t/s (2.0 s) |
| Qwen3.5 4B | **`gpu` (`-ngl 999`)** | **67 / 8.1 t/s (5.9 s)** | **66 / 8.0 t/s (6.5 s)** |
| Qwen3.5 4B | `gpu-prefill` (`-ngl 0`) | 62 / 7.7 t/s (4.4 s) | 63 / 7.7 t/s (4.2 s) |
| Qwen3.5 4B | `cpu` | 48 / 8.2 t/s (3.8 s) | 51 / 9.1 t/s (3.8 s) |

*\*First `qwen3.5-2b · gpu` run included initial Vulkan shader cache compilation.*

### Sustained full 14-minute meeting summary (`single`, `ctx 8192`, Balanced `-cpulimit 50 -threads 6`)

Measured on a synthetic 13:52 English meeting fixture (84 turns, 3,607 prompt tokens, 2 `@flit` notes; AMI server was returning 503 during this run):

| Model | Device | Wall | Read (3,607 tok) | Write | Peak RAM | Est. 1-hour meeting (sustained) |
|---|---|---:|---:|---:|---:|---:|
| Qwen3.5 2B | **`gpu` (`-ngl 999`)** | **1m04.5s** | 95 t/s (38.2 s) | 16.1 t/s (423 tok) | **1.83 GB** | **~3.8 min** (linear: 4m39s) |
| **Qwen3.5 4B** | **`gpu` (`-ngl 999`)** | **2m46.0s** | **36 t/s (100.4 s)** | 5.5 t/s (360 tok) | **3.51 GB** | **~10.3 min** (linear: 11m58s) |
| Qwen3.5 4B | `cpu` (Balanced 50%/6t) | 3m15.6s | 28 t/s (128.5 s) | 6.1 t/s (412 tok) | 4.47 GB | ~12.1 min (linear: 14m06s) |

Lessons from the laptop:
- **Full Vulkan offload (`gpu`, `-ngl 999`) beats both `gpu-prefill` (`-ngl 0`) and `cpu` Balanced** on this AMD iGPU: faster prefill (+62% burst on 2B; +29% burst and sustained on 4B), ~1 GB lower process RAM working set on 4B (3.51 GB vs. 4.47 GB), and offloads compute off the CPU cores.
- **Sustained thermal throttling on 15 W laptops matters**: 4B settles from 66 → 36 read t/s and 8.0 → 5.5 write t/s over a 3-minute run, yet still lands at **~10.3 min per hour of meeting** — comfortably inside the 15-minute ceiling.
- **16 GB iGPU RAM threshold bug fixed**: `GlobalMemoryStatusEx.TotalPhys` reported **14.82 GiB** on this 16 GB laptop because of the 1 GiB UMA carve-out, which caused the original `>= 15<<30` check in `internal/app/summary_windows.go` to downgrade it to `qwen3.5-2b` (`TestSummaryEndToEnd` ran on `qwen3.5-2b`). Lowered `summaryModelMinRAM4B` to `13<<30` (13 GiB), keeping 8–12 GB PCs on `qwen3.5-2b` while letting 16 GB iGPU laptops (even with 1–2 GiB UMA carve-out) use `qwen3.5-4b`.

## Choice

- **Model**: **Qwen3.5 4B** where the PC has 13 GiB of Windows-visible physical memory or more (covering 16 GB laptops with 1–2 GiB iGPU carve-outs; peak 3.5 GB on Vulkan iGPU, 4.5–5.2 GB on CPU), **Qwen3.5 2B** below. The 4B is the only candidate whose German notes are worth giving to a user; the 2B is the fallback for small PCs, with the errors described. Gemma 4 E2B is a candidate for a later split (it answers questions best and fastest) but not as the writer. Granite 4.0 H Micro is out.
- **Approach**: the **single pass** up to ~12,000 tokens (about 50 minutes), extract-then-write beyond, each `@flit` question in its own call.
- **Device**: full Vulkan offload (`gpu`, `-ngl 999`) when it starts, else the CPU (`StartFallback`). Measured faster than both `-ngl 0` (`gpu-prefill`) and CPU on both the desktop RX 7800 XT and the laptop's Vega 7 iGPU.
- **Next measurements**: a meeting of 45–60 minutes (confirming flat prefill scaling at 16k context), real-call iGPU feel when opting into `both`, and the vocabulary's effect on the user's name.
