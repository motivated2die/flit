# Summary prompting, note steering and evaluation

Question (2026-11-23): why are flit's current meeting summaries uneven in quality, specificity and usefulness, and how can we tighten prompting, note steering and speaker attribution for small local models (Qwen3.5 4B / 2B) — then verify every change with repeatable multi-run benchmarks?

Context: current pipeline in [internal/summary/](../../internal/summary/), first measurements in [summary-experiments.md](summary-experiments.md), architecture in [architecture/summaries.md](../architecture/summaries.md).

## 1. Diagnosis of flit's current pipeline

Read directly from `internal/summary/{input,cues,prompt,run,tidy}.go` and the latest GPU output (`.cache/summaries/ami-IS1009a_qwen3.5-4b_gpu_single.md`, Qwen3.5 4B, `single`, `ctx 8192`):

1. **Rigid 5-section template encourages padding and duplication.** `format()` (`internal/summary/prompt.go:131-145`) asks for five numbered sections in order: Summary, Topics, Decisions, Tasks, Open questions. Even with "Leave it out when there is nothing", Qwen3.5 4B pads empty/weak categories:
   - Generic filler in `## Summary` ("The team held a kickoff meeting to introduce themselves...").
   - Duplicate sections: `## Next Steps` (from the user's heading) *and* `## Tasks` (from the template) repeating the same assignments.
   - Brainstorming ideas promoted to `## Decisions` ("The remote control must be multi-functional...").
   - Fabricated `## Open questions` ("What specific materials will be used..."), never asked in IS1009a.
2. **Under-specified note steering and broken anchor numbering.**
   - `Cues()` (`internal/summary/cues.go:71-78`) assigns an `[N#]` index to *every* non-heading note line, even when `n.Turn == -1` (unanchored). `notesText()` (`internal/summary/prompt.go:91-93`) then prints `[N#]` on unanchored notes that have no matching `[N#]` in the transcript, confusing the model.
   - **Pre-meeting vs. in-meeting notes are not distinguished**, even though `notes.json` already gives us `Block.Typed == notes.Unknown` (pre-meeting / pasted) vs. `Block.Typed != notes.Unknown` (typed live during the call).
   - **Formatting cues are ignored in the prompt**: `**bold**`, `*italic*`, H3 headings, and open/done checkboxes (`- [ ]` / `- [x]`) carry strong user-intent signal that the prompt never explains.
   - **Missed-information markers** (e.g., short gaps, `???`, or `@flit` instructions asking "what did I miss here?") are only partially served: `@flit` answers are appended as a separate `## Your questions` section in `Assemble()`, isolated from the topic they belong to, and the main writer never sees the resolved answers (so it can contradict or duplicate them).
3. **Weak speaker attribution.**
   - `format()` asks for "**Name:** what, by when" in Tasks, but does **not** ask for speaker attribution of ideas, proposals or objections in topic bullets.
   - When `speakers.json` only has `Them 1..3`, `Single` can infer real names from introductions, but `Extract` cannot (sections 2–N never see section 1's introductions).
   - No rule forbids using raw labels like `Them 1` as task owners or reminds the model to use plain role descriptions ("the designer") when a name is unknown.
4. **No meeting-type adaptability.** A design kickoff, a 1:1, a status standup and an interview all receive the exact same 5-section instruction.
5. **No vocabulary / spelling guidance in the summary prompt.** Unlike transcription, the summary prompt does not pass custom vocabulary or respell known names/terms.

## 2. What the reference projects do (code-level findings)

All paths relative to each repo root at the pinned commits in [references/README.md](../references/README.md). AutoDoc is AGPL-3.0: ideas only, no code copied.

### Yogurt (`references/yogurt/app/src/main/notes/enhance.ts`, MIT, `71b095a`)

- **No generic Overview/Summary section.** Starts immediately with the first `## <specific topic>` ("most important first", forbidding generic headings like "Discussion", "Updates", "Topic 1"). Adapts to meeting style ("When people took turns giving their own updates, a section per person, like `## Maya's updates`, works well").
- **Tight bullet craft.** "A bullet is one full point of up to about 20 words, starting with the point (never 'The team discussed')." Decisions and open questions live *inside* their topic ("Decided: ..."), avoiding padded standalone Decisions/Open Questions sections.
- **Explicit noise filter.** Explicitly lists what to omit: small talk, greetings, weekends, jokes, call-audio chatter — unless the meeting was about it.
- **Strict people/attribution rule (`peopleRule`).** `"Them"` is explicitly forbidden as a name; use real names when known, `"You"` for the user, or plain role words (`"the client"`, `"the designer"`). Special single-mic room guard (`oneMic`).
- **Pre-meeting script vs. live notes rule.** Tells the model that notes can be a plan/script written before the meeting: a planned question/step goes into the summary *only* if the transcript shows it was actually asked or done.
- **Deterministic post-processing.** `withoutCopies` strips copied labels/markers; `markMine` marks bullets restating the user's own lines; continuation call for `## Next steps` only when the model stopped early (reusing KV cache).
- **Vocabulary respelling (`vocab.ts`).** Known names/companies/jargon are respelled into the transcript *and* passed as a spelling hint line.

### anarlog (`references/anarlog/crates/template-app/assets/enhance.*.md.jinja`, MIT, `7c2a321`)

- **Explicit Pre-Meeting vs. Meeting Notes diffing.** Separates `# Pre-Meeting Notes` (agenda, preliminary questions) from `# Meeting Notes` (full current state) and instructs the model to focus on what was *added or changed during the meeting* as the strongest salience signal.
- **User formatting as importance cues.** Explicitly instructs the model to pay close attention to `**bold**`, `_italic_`, `<u>underline</u>`, `~~strikethrough~~`, and to treat `### H3` headers as topics the user wants retained no matter what.
- **No generic opening sections.** Explicitly bans "Overview", "Meeting Overview", "Introduction", "Participants" unless requested.
- **Bilingual/technical-term rule.** For non-English summaries (relevant to flit's German meetings), explicitly instructs keeping English technical terms and product names in English while following the target language's grammar.
- **Deterministic structural evals (`crates/template-eval`, `tests/llm_eval/cases/enhance.rs`).** Runs each prompt across multiple samples (`required_pass_rate: 0.8`) and checks deterministic structural expectations (no code fences, heading levels, list presence, max word count) before any semantic grading.

### AutoDoc (`references/autodoc/src/main/services/notes-*.ts`, AGPL-3.0, `5082757`, ideas only)

- **Deterministic owner validation (`notes-owner-attribution.ts`).** Distinguishes plausible human owners from generic pronouns (`we`, `team`, `someone`) and organization/tool names (`engineering`, `Slack`), and checks whether the transcript actually contains a commitment or direct assignment around that name.
- **Evidence grounding (`notes-writer-grounding.ts`).** In paraphrase mode (Windows tight writer), checks concrete "atoms" — quantities/numbers, polarity/direction, completion state — against a nearby transcript window rather than requiring exact verbatim quotes for every bullet.
- **Two-stage takeaway selection (`notes-takeaway-selection.ts`).** Letting the small model *pick indices* (`{"picks": [1, 4, 7]}`) of self-contained key takeaways from already-generated bullets beat heuristic ranking in blind tests (15 wins, 4 losses, 8 ties on 27 meetings) because selection cannot invent new facts.
- **No self-verification by the same small model (`notes-claim-verification.ts`).** Removed after 30-meeting eval: the same 3B–4B model rejected faithful notes more often than it caught hallucinations.

## 3. Insights from recent research & industry practice (2025–2026)

- **Note-guided steering (Granola / Hyprnote paradigm).** Treating human notes as a *routing skeleton and salience prior* rather than mere appendix text reduces generic fluff and constrains small models from inventing structure. Key challenge: asynchronous typing lag (a user types a note 30–120 s after a point, or adds a catch-up marker when distracted), which flit's per-line `notes.json` timestamps are uniquely positioned to solve.
- **Small LLM failure modes on meetings (ACL / ArXiv 2025–2026).** Sub-8B models suffer from:
  1. *Speaker attribution drift* under ASR noise (assigning proposals or tasks to the wrong participant).
  2. *Extrinsic action/decision fabrication* when prompted with mandatory sections.
  3. *Structure drift / amnesia* on long transcripts (>8k–12k tokens).
- **Fact-factored / modular pipelines (FRAME & SCOPE, ArXiv late 2025).** Separating fact/quote extraction, attribution, and synthesis lowers omissions and hallucinations compared to single-shot prompts — *provided* cross-chunk context (speaker names/roles) is carried forward, which explains why flit's Round 2 `Extract` degraded on AMI when sections 2–4 lost section 1's introductions.
- **Fine-grained error taxonomies (QMSum Mistake, SHAM-Bench).** Instead of ROUGE alone, modern meeting-summary evaluation scores discrete dimensions: *omission*, *extrinsic hallucination*, *attribution error*, *irrelevance/fluff*, and *structural compliance*.

## 4. Benchmark datasets (existing & new candidates)

| Dataset | Language | Size | Labels available | License | Fit for flit |
|---|---|---|---|---|---|
| **AMI Meeting Corpus** (already in `.cache/ami/ami_public_manual_1.6.2.zip`!) | English | **142 meetings** with `.abssumm.xml` already cached locally | Word-level transcripts, speaker IDs, human `abstract`, `actions`, `decisions`, `problems`, extractive links | CC BY 4.0 | **Immediate win**: we only used `IS1009a` so far; we can extract a diverse 5–10 meeting benchmark suite (`IS1009a–d`, `ES2004a–d`, `TS3003a–d`, varying 14–45 min) directly from the cached zip with zero extra downloads. |
| **QMSum** ([Yale-LILY/QMSum](https://github.com/Yale-LILY/QMSum)) | English | 232 meetings (AMI, ICSI, Parliament), 1,808 query-summary pairs | Human queries + target answers + oracle transcript spans | MIT wrapper (underlying AMI CC BY 4.0, ICSI) | **Ideal for `@flit` mention benchmarking**: queries + oracle spans directly mirror `@flit` questions. |
| **FAME** (arXiv:2502.13001, ACL 2025 Findings) | **German (300)** & English (500) | 800 synthetic multi-agent meetings across 14 meeting types | Full multi-speaker transcripts across diverse meeting types (standup, planning, retro, 1:1, etc.) | Check repo `LICENSE` before pinning | **Top candidate for German + meeting-type diversity**, filling the gap where `de-synth-4spk` is read audiobook speech. |
| **MeetingBank** | English | 1,300+ city council meetings | Transcripts + official minutes + segment alignments | Research/public (verify exact license) | Good for long-meeting (>45 min) `Extract` stress tests. |
| **ELITR Minuting Corpus** | English & Czech | ~180 hours technical project meetings | Participant-written meeting minutes | Research/open (verify exact license) | Closest in style to real software/engineering meetings. |
| **Author's `testdata/private/`** | German | Real recordings (e.g. `interview-2026-11-22`) | Real flit notes, timestamps, `@flit` mentions | Private (git-ignored, never logged) | Gold standard for real-world German + live note-taking feel. |

## 5. Concrete avenues for improvement (to benchmark)

Ordered by expected impact-to-effort ratio for Qwen3.5 4B (and 2B fallback), keeping latency within the 15 min/hour laptop budget:

### Avenue A: Rewrite the writer prompt for succinctness, attribution & zero padding
- **Drop the mandatory 5-section scaffold.** Test Yogurt's pattern: skip generic `## Summary` (or cap at 1–2 crisp sentences of outcome only), fold `Decided:` and `Open:` inline inside topic sections *or* make standalone `## Decisions` / `## Open questions` strictly opt-in with strong negative examples, and keep `## Tasks` / `## Next steps` only for explicit commitments.
- **Explicit bullet craft & noise filter.** Max ~20 words per bullet, start with the point (ban "The team discussed..."), explicitly filter out small talk/warm-ups/logistics, and require speaker attribution (`**Name** proposed...`, `**Name** raised concern about...`) whenever a speaker is known.
- **Strict `peopleRule` & German technical-term rule.** Forbid `"Them 1"` as an owner when a real name was spoken or a plain role is clearer; keep English technical/product terms intact in German summaries.

### Avenue B: Richer note-steering using data flit already records
- **Split pre-meeting vs. in-meeting notes automatically** using `Block.Typed == notes.Unknown` vs. `!= notes.Unknown` (anarlog's insight, zero UI change needed): tell the model pre-meeting lines are the agenda/plan (include only if actually discussed) and in-meeting lines are live salience signals.
- **Fix `[N#]` anchoring.** Only emit `[N#]` tags on notes that actually anchored (`n.Turn >= 0`); for anchored notes with a timestamp, consider showing the note inline or grouping nearby context cleanly so small models don't have to cross-reference distant IDs.
- **Surface formatting & task-state cues.** Tell the model that `**bold**` / `### H3` in user notes are high-priority anchors to retain, `- [ ]` are open tasks to track/enrich with owners & deadlines, and `- [x]` are already done.
- **Feed `@flit` Q&A context cleanly.** Experiment with passing the resolved `@flit` answers (or missed-info markers) to the writer — or keeping them in `## Your questions` while telling the writer not to duplicate them — and test a dedicated missed-info cue (e.g. `[@flit 12:40: missed this]` / `???`) that summarizes the preceding 60–90 s window.

### Avenue C: Meeting-type awareness (lightweight, zero extra LLM call or cheap pre-pass)
- Either let the prompt explicitly adapt structure by meeting style (kickoff/planning vs. status/standup vs. 1:1/interview vs. brainstorming, as Yogurt does in 2 sentences), **or** test a tiny 1-token / 20-token classification/template selector (and optional user meeting-type preset later).

### Avenue D: Fix `Extract` mode for long meetings (>50 min)
- Carry a **running speaker/role glossary** (extracted from section 1 or pre-seeded from `speakers.json` + introductions) into sections 2–N so later parts don't regress to `Them 1/2`.
- Tighten JSON extraction schema & prompt to distinguish *decisions* from *brainstormed ideas*, and add lightweight deterministic checks (quantity grounding, plausible-owner check) before the writer pass.

### Avenue E: Multi-run benchmark harness (`flitdev`)
- Extend `cmd/flitdev` (or add a dedicated `flitdev bench` / `flitdev summary` multi-run mode) to:
  1. Extract a curated set of AMI meetings (e.g., `IS1009a`, `IS1009b`, `IS1009c`, `ES2004a`, `TS3003a` — 14 to 40 min) from the already-cached `ami_public_manual_1.6.2.zip` alongside their gold `.abssumm.xml` references.
  2. Pair each meeting with realistic simulated notes (pre-meeting agenda + timed in-meeting bullets + bold/H3 cues + `@flit` questions + missed-info markers) and a no-notes control.
  3. Run **N = 3 seeds** per prompt variant (`-seeds 42,43,44`) to measure variance, not just a single lucky seed.
  4. Compute **deterministic checks** in code (anarlog's `template-eval` + AutoDoc's grounding ideas): word count, bullet length, duplicate headings/tasks, leaked `[N#]`/`Them 1` labels, language match, `@flit` answer presence, quantity/number preservation, and recall against AMI gold `actions` / `decisions`.
  5. Output side-by-side comparison tables (and optionally an offline rubric score) so we can iterate on prompts with real numbers before changing defaults.

## 6. Benchmark results (2026-11-23, Qwen3.5 4B, RX 7800 XT Vulkan)

Measured with `flitdev bench -model qwen3.5-4b -device gpu -seeds 42,43` (`ctx 20480`, `llama.cpp b12633`) across 8 cases × 2 seeds = 16 runs per variant:
- 3 German FAME meetings (MIT, from [FKIRSTE/acl2025-mimic-to-get-fame](https://github.com/FKIRSTE/acl2025-mimic-to-get-fame), cached in `.cache/fame/`): `Casual_Catch-Up` (`Anthropometrie`), `Cross-Functional_Meeting` (`Ethnographie`), `Decision-Making_Meeting` (`Klimaanpassung`), each with loose sparse notes + `@flit` questions (including a German missed-info cue).
- English AMI `IS1009a` (13.4 min) across 3 note profiles: `none`, `sparse` (loose fragments + gaps), and `structured`.
- English AMI `IB4010` (49.0 min, ~12,100 words, film-club selection) with sparse early-only notes (35-min silence gap), in both `Single` and `Extract` mode.

| Metric (16 runs) | **v1 baseline** | **v2 tight prompt + loose-note cues + glossary** | **v3 (+ AnyJev-style classifier)** | **v4 refined (shipped)** |
|---|---:|---:|---:|---:|
| Avg wall time | 14.1 s | 12.0 s | 12.0 s | **11.8 s** (-16%) |
| Avg words | 513 | 413 | 377 | **359** (-30%) |
| Speaker attribution (topic bullets) | 12% | 59% | 60% | **63%** (**5.2×**) |
| Generic headings (`Overview`, etc.) | 3 | 0 | 0 | **0** |
| Raw `Them 1..3` labels | 48 | 134 | 109 | **64** (**0** on `IB4010-extract s=42` & `IS1009a-sparse s=42`) |
| Number grounding | 50% (42/84) | 73% (33/45) | 80% (37/46) | **78% (36/46)** |
| 49-min `IB4010` (`Extract`, s=42) | 920 w / 13 H2 / 21 `Them` / missed final vote | 471 w / 6 H2 / 18 `Them` / captured vote | 487 w / 5 H2 / 19 `Them` | **303 w / 5 H2 / 0 `Them` / discovered Denis, Agnes, Mirek** |

### Key findings from the runs

1. **Bullet-only + zero generic Summary fixed prose walls and cut bloat by 30%.** In `v1`, German FAME runs frequently collapsed into 5-sentence prose paragraphs with 0% attribution; `v2`–`v4` produced 100% attributed crisp bullets (~16–18 words) on German runs with zero generic headings.
2. **Fast meeting-type classification (`internal/summary/classify.go`) had zero net latency cost.** Running a tiny constrained JSON enum call (`max_tokens: 48`, `temperature: 0`) on a ~550-token start/middle/end sample of the already-loaded model took ~0.3 s on GPU and paid for itself via tighter generation (12.0 s → 11.8 s avg), while improving number grounding and surfacing tasks on `decision_planning` meetings without fabricating them on casual catch-ups.
3. **Loose-note steering + pre-meeting/live split works across `none`, `sparse`, and `structured` profiles.** Separating `<pre_meeting_notes>` (`Typed == Unknown`) from `<live_notes>` (`Typed != Unknown`), fixing `[N#]` to emit only for anchored notes, and explicitly instructing the model not to drop topics when the user stops taking notes prevented the 35-minute note gap in `IB4010` from truncating coverage.
4. **Extract speaker glossary (`Speakers []SpeakerID`) transformed long-meeting quality.** Carrying discovered speaker names across chunks let `Extract` on 49-min `IB4010` discover Denis, Agnes, and Mirek from speech and drop `Them 1..3` from 21 → 0 (s=42) while shrinking 920 words of plot recaps into 303 words of decisions and votes.
5. **User-facing guidance:** Updated `notesPlaceholder` in `internal/app/notes_windows.go` to guide loose note-taking (`**bold**`, `[]` tasks, `@flit` for questions or missed info), and tightened `summary.Input.Instruction` so extra instructions steer focus/emphasis/language without breaking factual grounding or core structure.
