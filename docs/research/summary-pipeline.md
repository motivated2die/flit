# From transcript and the user's notes to meeting notes: the pipeline

Question (2026-11-22): when the user asks for it after a meeting, how should flit turn the transcript and the user's own notes into good notes (topics, decisions, tasks with owners, open questions) with a **small local model**, on a laptop without a dedicated GPU, without the PC grinding for long? Which approach is the best baseline to build first and compare against? Covers: what the references do, what small models get wrong, ways to split the work, how the user's notes and name can steer attention, cheap classification to save generation, and how to evaluate. Runtime, models and hardware: [local-summaries.md](local-summaries.md). Editor: [notes-editor.md](notes-editor.md). Design: [architecture/summaries.md](../architecture/summaries.md).

Product rules from the author: heavy processing only **after** the meeting, and only **when the user asks** for a recording (no automatic summaries); err towards lighter models; German and English.

## Decisions by the author (2026-11-22)

- **Target**: notes for a one-hour meeting in about **15 minutes on a weaker laptop** (CPU only or an integrated GPU) at most.
- **Language**: the summary is always written in the language **most of the transcript is in**. flit transcribes in a language set before the meeting; for a meeting with both languages, the majority is counted from the transcript's words with a cheap language guess (common short words per language, as Yogurt's `guessLanguage`), no model.
- **Attention cues, kept simple**: the user's first and last name go into the speech model's vocabulary, and lines that mention them are marked for the model as a hint. No further intent matching ("For you" sections, commitment cue lists, phonetic matching) unless a measurement shows it raises quality. The commitment and decision cues below are therefore experiments, not defaults.
- **@flit mentions** in the notes are the user's explicit pointers ([notes-editor.md](notes-editor.md#model-and-storage-internalnotesdocgo)): an instruction with the time it was written, `[@flit 12:40: was the budget decided or only proposed?]`. They become the strongest cue: the part of the transcript around that time goes into the prompt with the instruction, and the writer is asked to answer it explicitly.
- **Typing times** of every note line are recorded during the recording (`notes.json`) and used to anchor notes to the transcript, as described under cues below.

## What the references do

| | Yogurt ([ref](../references/yogurt.md#notes-appsrcmainnotes)) | AutoDoc ([ref](../references/autodoc.md#notes-studied-2026-11-22-same-commit)) | anarlog ([ref](../references/anarlog.md#enhancing-notes-cratestemplate-appassetsenhancemdjinja)) |
|---|---|---|---|
| Runs | after every meeting, automatically; Windows/Linux too | after every meeting | after the meeting, user picks a template |
| Models | Gemma 4 E2B/E4B, gpt-oss, Qwen3 1.7B, by hardware | Qwen3 4B Instruct, Llama 3.2 3B on low spec | own 1.1 GB fine-tune (Mac only); otherwise Ollama/LM Studio |
| Context | 32k (16k on small machines); one pass if it fits | **8k on Windows**, 4k low memory | whatever the server gives |
| Transcript in the prompt | **first**, behind a fixed head (cache reuse) | in chunks of ≤ 6,000 characters | after the notes |
| Long meetings | parts of ≤ 12k tokens → part notes (≤ 700 tokens) → final pass | extraction per chunk → topic pass | not handled specially |
| Extraction | no; writes Markdown directly | **JSON per chunk: decisions, commitments, quantities, each with a verbatim quote**; unquoted items dropped | no |
| User's notes | "hints, not text to copy"; flagged items get a section or bullet; facts they typed go in; a plan's questions only if asked; bullets restating their lines marked as theirs | (not studied) | **pre-meeting snapshot vs. what was added during**; H3 = "retain no matter what"; emphasis = importance; "integrate, don't convert into headers" |
| Names, vocabulary | invitees' names and companies respelled into the transcript, a spelling line for the model | owners kept only "when independently verifiable" | custom vocabulary feeds the summary too |
| Follow-ups | "Next steps" and title asked as continuations (cache) | — | — |
| Checks in code | strip lead-ins, fences, closers; remove copied labels; clean titles | quote must be in the chunk; regex recovery of explicit commitments (English) | structural expectations on output (headings, lists) |

## What small models get wrong (evidence so far)

- **Long inputs**: a vendor benchmark has Qwen3.5 4B falling from 76 to 55 quality between 30-second and 60-minute inputs, while large models stay high ([AssemblyAI](https://www.assemblyai.com/blog/summarize-meetings-llms-python); vendor-run, method not checked). AutoDoc caps Windows at 8k tokens.
- **Judging themselves**: AutoDoc's per-note check "judged faithful notes unsupported more often than it caught real inventions" (30 meetings).
- **Skipping parts of the task**: Yogurt asks for "Next steps" again when the model stopped before them; copying the user's labels and numbering (`withoutCopies`); chatty lead-ins and closers (`tidyNotes`).
- **Non-English**: Llama 3.2 3B "returns the blank example" for non-English output (AutoDoc `llm.ts`).
- **Loops with greedy decoding** (Yogurt `sampling.ts`): keep a little temperature and a presence penalty.

So: give the model short, focused inputs, small structured outputs, one job per call, and do checking and assembly in code.

## Approaches to compare

Token counts for **one hour** (~15k tokens of transcript, [local-summaries.md](local-summaries.md#how-much-text-a-meeting-is)); time on the laptop from the planning rates there, guesses until measured (CPU: prefill ~50–120 tok/s, decode ~6–15 tok/s; iGPU via Vulkan: prefill ~300–800 tok/s).

| | How | Read (prefill) | Written (decode) | Strengths | Weaknesses |
|---|---|---:|---:|---|---|
| **A. One pass** (Yogurt for meetings that fit) | whole transcript + notes → Markdown | ~16k | ~1,000 | cross-references kept; one call; simplest | long input degrades small models; needs ≥ 16k context (memory); a 2-hour meeting doesn't fit |
| **B. Map-reduce** (Yogurt for long meetings) | sections → prose part notes → final pass | ~18k | ~2,500 (4 × 350 + 1,000) | any length | more writing (decode is the slow part on CPU); detail lost in part notes |
| **C. Extract, then write** (AutoDoc's shape) | sections → **small JSON** (items with quotes) → code checks quotes, dedupes, times → one writer pass over items + user notes | ~19k | ~2,000 (4 × 250 + 1,000) | short inputs per call; each call one job; checkable output; the writer's input is small (fast, focused); partial results usable | more calls; cross-section links only through the writer; JSON adds some tokens |
| **D. Classify, then extract** (AnyJev-style) | code + a one-token label per turn (decision / task / question / other) read from the model's probabilities → extract only from flagged turns → writer | ~20k (every turn read, shared prefix cached) | ~1,200 | almost no generation for the bulk of the transcript; good for task lists | topics and context need another pass; many short calls; untested |

On a CPU, decode costs ~10× more per token than prefill, so the written tokens decide much of the time: at 10 tok/s, 1,000 tokens are ~100 s, 2,500 ~250 s. With the iGPU reading the prompt, the difference between A and C shrinks to their writing.

**Baseline to build first: C**, with the attention cues below; **A as the comparison** for meetings that fit the context (it's the same code with one section). D is an optimisation to test once C works.

## Compiling the context

### 1. The compact transcript

- One line per turn: `Name: text`, turns of one speaker joined, no timestamps (quotes are located in code instead). "Me" for the user's lines, with the user's name given once in the header ("Me is Moritz Haisch"), other speakers by the names in `speakers.json` or `S1`, `S2`.
- **Respelled** with the vocabulary, the user's name and the meeting's names (Yogurt's idea), so the model never sees two spellings of one person.
- Echo-removed, as the saved transcript already is.

### 2. Sections

- Cut at pauses ≥ 2 s between turns, aiming at **~3,000 tokens** (~12 minutes; guess, to tune), never inside a turn (a very long turn at sentences). Smaller than Yogurt's 12k because of how small models degrade with length; it also fits an 8k context with the prompt and output, which keeps memory low (AutoDoc's Windows size).
- Each section gets a header: its time range and the people speaking in it.
- Optional later: topic boundaries from lexical cohesion between neighbouring windows (TextTiling-style, no model), to avoid cutting a discussion in half.

### 3. Attention cues, computed in code before any model runs

Cheap signals that tell the model where to look, as short inline markers on transcript lines and as a list at the top of the section prompt:

- **@flit mentions** (built into the notes, [notes-editor.md](notes-editor.md#model-and-storage-internalnotesdocgo)): each is an instruction with the session time it was written. The turns in [*t* − 3 min, *t* + 30 s] (a guess, to tune) are put into the section prompt (or the section containing *t* gets it) as "At 12:40 Me asked: …", and the writer gets every instruction to answer in the notes. The strongest and cheapest signal, since the user said exactly what matters.
- **Note anchors.** The editor records when each note line was first typed (built, `notes.json`). A line typed at *t* most likely refers to what was said shortly before: the turns in [*t* − 90 s, *t* + 10 s] (window a guess, to tune), ranked by word overlap with the note (names, numbers, rare words count most). Those turns get `[N3]` (note 3). Notes written before the meeting or pasted afterwards have no time: they are anchored by word overlap alone, or not at all. **Neither Yogurt nor AutoDoc nor anarlog records note times** (checked in their code); anarlog's "pre-meeting snapshot vs. added during" is the coarse version of the same idea.
- **The user's name.** The first and last name from Settings, which are also in the speech model's vocabulary so they are spelled as typed; lines that contain either get `[@you]`, a hint for the model. Plain word matching, no inflection or phonetic tricks (the author's decision: only if measurements show a gain).
- **Commitment and decision cues** (an experiment, not a default): German and English word lists ("I'll / can you / by Friday", "ich kümmere mich / kannst du / bis Freitag / beschlossen"), lines marked `[!]`. Kept only if the evaluation shows they raise the recall of AMI's actions and decisions.
- **Dates and numbers** stay as they are; the writer is told to keep them.

### 4. The section prompt (extraction)

Instructions first, then the section, then the question repeated briefly (small models follow instructions placed close to where they answer; the instruction prefix is the same for every section, so llama-server's prompt cache keeps it). Output is JSON with short keys, constrained by a schema (`response_format`), capped at ~300 tokens:

```
You extract facts from part of a meeting transcript for "Me" (Moritz Haisch).
Lines marked [N#] are near a note Me wrote; [@you] mention Me; [!] may contain
a request, commitment or decision. Pay special attention to them, but use only
what the transcript says.
Notes Me wrote around this part: N3 "Budget 25 €?", N4 "Design → Francina"
Me asked flit at 14:10: "was the budget decided or only proposed?"
<part from="12:40" to="24:10">
Me: …
S2: … [!]
…
</part>
Answer with JSON: topics (title, up to 4 points), decisions, actions (who, what,
due, quote), questions. A quote is up to 12 words copied exactly. Empty lists
rather than guesses. Write the text in German.
```

### 5. Checks and assembly in code

- Parse the JSON (one retry; a failed section leaves a gap, not a failed summary).
- Find each quote in the section (normalised, one word off allowed): found → the item gets the turn's time; not found → dropped or shown as unverified (AutoDoc drops).
- Owners must be a label of the section (`Me`, a name, `S2`); others become unassigned.
- Merge sections: same task text or same quote → one item; topics with the same title merged.
- **Tasks from the user's notes**: open `- [ ]` lines from the notes are matched to extracted actions by word overlap; a match carries the user's wording and its done state.

### 6. The writer prompt (one call, small input)

Input: the user's notes in full (their headings, bullets, tasks as written, mentions turned into questions to answer), the merged items (with section times), meeting metadata, the language. Output: the elements of the editor (H2, H3, nested and numbered lists, tasks, paragraphs, bold and italics), ~1,000 tokens max, in the transcript's main language:

- The user's own headings stay as sections in their order; topics the transcript adds come after them. H3s the user wrote are kept no matter what (anarlog's rule).
- Every `@flit` instruction answered where it belongs ("Decided: …" or "Only proposed: …").
- A separate "For you" section only if the evaluation shows it helps (author's decision); by default the user's own tasks are simply tasks with "Me" (their name) as owner.
- Tasks as `- [ ] **Owner:** what, by when`; decisions as bullets starting with "Decided:" / "Beschlossen:"; open questions last.
- "Use only these notes and items. Integrate the user's notes, don't copy their lines or labels." (Yogurt's and anarlog's rules.)
- Then code: strip lead-ins and code fences, check the Markdown has only the five elements (anarlog's structural checks), mark bullets that restate the user's own lines (Yogurt's `markMine`), so the UI can show what came from the user and what from the model.

## The user's name and vocabulary in transcription

- **Settings → "Your name"** (first, last, optional nickname): added to the speech model's vocabulary for every meeting, used as "Me"'s label, and as the mention list above. transcribe.cpp's Parakeet phrase boosting "changes which word is written, never whether one is, so it cannot invent speech on silence" ([references/transcribe-cpp.md](../references/transcribe-cpp.md#vocabulary-custom-terms)), so adding names is safe.
- **Per meeting**: calendar attendees, the names given to speakers, and proper nouns the user types into the notes during the meeting are good vocabulary candidates. flit transcribes during the meeting, so words typed late only help the chunks not yet transcribed, or a re-run.
- Whether boosting a name actually fixes its recognition in German meetings is to measure (a test: the German test meeting with names spoken, with and without the terms).

## Cheap classification to save generation

Generation is the slow part on a CPU. Models that **decide without writing** fit the "classify, then extract" approach (D):

- **Jev** (TypeSafe AI, September 2026): a hosted model that answers yes/no, multiple choice and scales with probabilities instead of text, claimed ~190× faster than LLMs at similar accuracy on its makers' tasks ([DeepLearning.AI](https://www.deeplearning.ai/the-batch/models-built-to-do-one-thing-well), [inovex review](https://www.inovex.de/en/blog/typesafe-ai-jev-review-how-good-is-the-new-model-for-ai-classification/)); an independent study found lower F1 than the strongest LLMs on most tasks, at much lower cost ([arXiv 2610.06625](https://arxiv.org/pdf/2610.06625)). Not local.
- **OpenJev**: open weights reproducing the interface, 9B (Q4_K_M 5.6 GB) and 27B, **CC BY-NC 4.0** ([Hugging Face](https://huggingface.co/openjev/OpenJev-Flash-9B-GGUF)). Too big for the laptop and non-commercial.
- **AnyJev**: turns any open-weight LLM into such a decision model by reading next-token probabilities, with training-free bias correction, including on llama.cpp ([guide](https://betterstack.com/community/guides/ai/anyjev-llm/)). This is the transferable part: flit can do it with the notes model it already loads, through llama-server's token probabilities (`n_probs`) and a grammar that allows only the label tokens. One forward pass per turn, nothing written.
- **Small encoders** (GLiNER for names and dates, mmBERT-class classifiers) are fast on a CPU but need ONNX Runtime or a port, and labelled data for classification. Not now ([0001](../decisions/0001-stay-with-go.md)).

Order of use: code cues first (free), then label readout from the notes model where a section has many candidate lines, then extraction. Whether D's savings are worth its extra calls is an experiment, not a default.

## Evaluating

- **AMI's human summaries**: `ami_public_manual_1.6.2.zip`, already cached by `flitdev testdata` (CC BY 4.0), has abstractive summaries of IS1009a–d (abstract, actions, decisions, problems; IS1009a's actions: "The industrial designer will work on the working design and technical function. The interface specialist will do the working design and functional design. The marketing manager will look for user requirement specifications…") and **extractive links** from summary sentences to the dialogue acts that support them. That gives references for recall (actions and decisions found), precision (items not in the reference and not supported by a quote), owners, and which transcript parts a method attended to.
- **Notes for the test meetings**: a short agenda and five to ten lines "typed during" IS1009a (with times from the reference turns), written once by hand, to test anchoring and notes integration; the same meeting without notes as the control.
- **German**: `de-synth-4spk` is short and read speech; FAME's 300 synthetic German meetings ([arXiv 2502.13001](https://arxiv.org/pdf/2502.13001); license and summary annotations to check) or the author's own meetings.
- **Form checks in code** (anarlog's idea): only the five elements, no lead-in, tasks well formed, language right.
- **Time and memory** per approach and model on the laptop (CPU and Vulkan), the numbers that decide the defaults.
- A dev command (`flitdev notes-eval -meeting … -approach A|C|D -model …`) printing items found, missed, invented, owner accuracy, time and peak memory; scoring against the references by hand at first, later with a larger model as an offline judge on the public test data only.

## Experiment plan

1. Build C (and A as its one-section case) behind `internal/llm` against llama-server; run both on IS1009a–d with and without simulated notes, with two or three small models ([local-summaries.md](local-summaries.md#models)).
2. Measure the cues: recall of AMI's actions with and without `[N#]`, `[@you]`, `[!]` markers.
3. Section size 2k / 3k / 6k tokens: quality vs. time.
4. D on the sections with many `[!]` lines: label readout accuracy against AMI's extractive links, and time saved.
5. German: the same on FAME (if usable) and the author's meetings.
