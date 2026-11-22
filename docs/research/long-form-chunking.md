# Does transcribing longer chunks improve accuracy?

## The hypothesis

Meeting tools prioritise live captions and transcribe small slices of audio. A slice of a few seconds gives the model little context, so rare words, names and jargon are matched worse. Transcribing longer pieces of each track after the fact, at the cost of delay, should be more accurate.

## What the references and the engine say

**Partly confirmed: tiny slices and streaming cost accuracy.**

- Streaming ASR decodes with limited right context (lookahead). In transcribe.cpp's Parakeet streaming variants the default chunk is 1.12 s with 13 frames of right context (`docs/porting/families/parakeet.md`); lowering the lookahead changes word and sentence boundaries and punctuation compared with the offline run of the same model.
- In the catalog ([models.md](models.md)), streaming-capable models have the worst English WER among the 0.6B Parakeets: nemotron-3.5-asr-streaming 7.9, multitalker-parakeet-streaming 6.52, against 4.26–4.83 for the offline TDT models. Different checkpoints, so this is suggestive, not proof.
- **Vocabulary boosting only works offline.** transcribe.cpp ignores `vocabulary` in streaming (`docs/prompting.md`). For the "specific language" problem this alone is a strong reason for offline decoding.
- Fixed-length slicing cuts words in half; every reference cuts at pauses instead.

**Not confirmed: "longer is always better".** Every reference caps chunk length:

| Where | Chunking | Stated reason |
|---|---|---|
| Handy `audio_toolkit/chunking.rs` | Pause-aligned, target 30 s, max 40 s, min 5 s; ≤ 40 s is one piece | "most models' accuracy degrades past 30-40 s of audio in one run" |
| transcribe.cpp, Parakeet-ultra | Audio > 30 s is segmented at pauses by the model's own VAD head before decoding | Matches the reference implementation; validated on 11 TED-LIUM talks |
| transcribe.cpp, Whisper | 30 s windows with previous-text context | Whisper's architecture is a fixed 30 s window |
| AutoDoc | 90 s windows, 5 s overlap, above 20 min; worker windows of 600 s with 15 s overlap | Memory and robustness on long recordings |

Why longer stops helping (general background, not measured here): models are trained mostly on utterances of up to ~20–30 s, so far longer inputs are out of distribution; full-attention encoders grow quadratically in memory with length; and for autoregressive decoders a long output has more room to drift or loop (transcribe.cpp has explicit repetition detection for that).

## Conclusion so far

The accuracy gain the hypothesis aims at is real, but it comes from **how** chunks are made, not from making them as long as possible:

1. **Offline decoding** of each chunk with full left and right context, never streaming.
2. **Pause-aligned chunks of roughly 20–40 s**, never mid-word cuts.
3. **Language fixed ahead** (or detected once per meeting) instead of guessed per tiny slice.
4. **A personal vocabulary** for names and jargon, which only offline decoding takes.
5. **Per track**: each track's chunks contain one side of the conversation, cleaner than a mix.

Delay doesn't have to be large: as Handy does live, a chunk can be decoded as soon as its cut is certain (5 s of lookahead past the window). During a meeting, the transcript then trails the audio by roughly 35–45 s, and when the meeting ends only the last chunk of each track is left. This "near-live, offline-quality" mode is a better trade than waiting for the whole meeting, and the same code path serves a batch re-transcription.

Whether chunks **longer than 40 s** help Parakeet specifically (its encoder is unbounded in transcribe.cpp, but Parakeet-ultra pre-segments at 30 s itself) is an open question to measure, not to assume.

## Implemented so far (2026-11-21)

- `internal/chunk`: pause-aligned planning, target 30 s, 5–40 s, Silero-style hysteresis on VAD probabilities, live planning that only commits cuts more audio can't change (tested to equal planning the whole track). A pause's candidate sits at its middle but at most 1 s in, so a long silence (the user listening) fixes its cut after 2 s instead of moving with the audio.
- `internal/pipeline`: VAD over the track, plan, skip chunks without speech, one offline run per chunk, results shifted into track time and split into sentences at pauses (Parakeet returns one segment per chunk).
- First numbers with 30 s chunks are in [testing/test-data.md](../testing/test-data.md#measurements-so-far). The chunk-length experiment itself is still to do: `flit transcribe --meeting` is the harness, a chunk-length flag is the next step.

## Open points

- Does context across chunk boundaries matter? Parakeet has no prompt slot, so each chunk starts fresh. Whisper could take the previous chunk's text as a prompt. A small overlap (1–2 s) with de-duplication of words at the seam is another option.
- Is the right chunk length the same for German and English, and for the sparse "Me" track (mostly silence) and the dense "Them" track?
- Does running VAD to drop long silences before decoding change WER (positively by removing noise, or negatively by losing context)?

## Experiment plan

A small benchmark tool (`cmd/bench`, using the same engine binding as the app) that transcribes recorded sessions under different settings and scores them.

**Data**

- Own meetings recorded with the app's recorder (two tracks, 16 kHz), in German, English and mixed. Kept local and git-ignored (`testdata/private/`). Reference transcripts made by correcting a model draft by hand; to avoid favouring one model, draft with a different model than those compared.
- A public long-form set for repeatability: TED-LIUM 3 (English, used by transcribe.cpp's own long-form tests); a German long-form set still to be found.
- A list of 30–50 domain terms per meeting (names, products, acronyms) for term recall.

**Variables**

| Variable | Values |
|---|---|
| Cutting | fixed N s; pause-aligned with target T and max 1.33·T |
| Length | 5, 10, 20, 30, 45, 60, 120 s, whole track |
| Model | parakeet-ultra, parakeet-tdt-0.6b-v3, canary-1b-v2, whisper-large-v3-turbo (GPU only) |
| Language | fixed `de`, fixed `en`, auto |
| Vocabulary | off, on |
| Overlap | 0, 1 s, 2 s with seam de-duplication |

**Metrics**

- WER after normalisation (case, punctuation, number formatting) per track and overall;
- term recall: share of vocabulary-term occurrences written correctly;
- real-time factor and peak memory of the worker;
- for streaming as a baseline: the same audio through a streaming model (nemotron-3.5) at its default chunk.

**What would change the design**: if pause-aligned 30 s chunks aren't better than 10 s ones by a clear margin, near-live becomes cheap to offer; if whole-track or ≥ 60 s runs beat 30 s on Parakeet, we raise the target and accept later results.
