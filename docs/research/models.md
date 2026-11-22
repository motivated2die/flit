# Speech models for German and English

## Source of the numbers

All figures below come from Handy's bundled catalog (`src-tauri/src/catalog/catalog.json` at Handy `4807c21`, built from transcribe.cpp 0.4.0). WER is the catalog's word error rate per language on its own test sets; **cpu×** and **gpu×** are real-time factors on the catalog's benchmark machine (how many seconds of audio per second of compute). They are good for **ranking**, not for predicting our numbers: the test sets are read or clean speech, not German meetings over Teams. Our own measurements go into [long-form-chunking.md](long-form-chunking.md#experiment-plan).

Size is the default quantization's file (Q8_0 unless noted).

## Candidates that cover German and English

| Model | Params | Size | WER en | WER de | cpu× | gpu× | Timestamps | Lang. detect | Vocabulary | License |
|---|---:|---:|---:|---:|---:|---:|:-:|:-:|:-:|---|
| **parakeet-ultra** | 627M | 740 MB | 4.26 | 4.58 | 13.3 | 23.0 | yes | yes | yes | CC-BY-4.0 |
| parakeet-tdt-0.6b-v3 | 627M | 740 MB | 4.83 | 5.24 | 13.7 | 25.1 | yes | yes | yes | CC-BY-4.0 |
| parakeet-primeline | 627M | 740 MB | 4.82 | 5.98 | 13.6 | 25.0 | yes | yes | yes | CC-BY-4.0 |
| orukeet | 627M | 740 MB | 4.43 | 5.64 | 13.6 | 25.1 | yes | yes | yes | CC-BY-SA-4.0 |
| canary-1b-v2 | 980M | 1.1 GB | 4.47 | 4.46 | 8.3 | 14.1 | no¹ | no | no | CC-BY-4.0 |
| canary-180m-flash | 189M | 218 MB | 5.98 | 7.33 | 21.7 | 33.1 | no¹ | no | no | CC-BY-4.0 |
| whisper-large-v3-turbo | 809M | 886 MB | 4.38 | 4.54 | 1.4 | 3.1 | yes | yes | yes + prompt | Apache-2.0 |
| whisper-large-v3 | 1.5B | 1.7 GB | 4.03 | 4.13 | 1.0 | 2.0 | yes | yes | yes + prompt | Apache-2.0 |
| cohere-transcribe-03-2026 | 2.0B | 2.4 GB | 5.08 | 5.06 | 4.3 | 8.1 | no | no | no | Apache-2.0 |
| Qwen3-ASR-1.7B | 2.0B | 2.2 GB | 3.23 | 4.25 | 2.5 | 4.1 | no | yes | yes + prompt | Apache-2.0 |
| Qwen3-ASR-0.6B | 782M | 850 MB | 4.23 | 6.80 | 5.3 | 9.7 | no | yes | yes + prompt | Apache-2.0 |
| Voxtral-Mini-3B-2507 | 4.7B | 5.0 GB | 3.89 | 4.71 | 0.6 | 1.1 | no | | | Apache-2.0 |

¹ The catalog's `timestamps` capability is false for Canary; verify in transcribe.cpp before ruling it out.

English-only models that would beat these for English (parakeet-unified-en-0.6b 3.99, canary-qwen-2.5b 3.58) don't help: our meetings switch between German and English.

## Assessment

- **parakeet-ultra is the default candidate.** Best German WER among the fast models (4.58), the same speed and size as the Parakeet v3 the author already knows from Handy, word timestamps (needed to merge tracks and map diarization), language detection, and vocabulary boosting. At ~13× real time on CPU, one hour of one track takes ~4.5 minutes of CPU time; both tracks ~9 minutes. Transcribing progressively during the meeting hides almost all of that.
- **parakeet-tdt-0.6b-v3** is the fallback and comparison baseline (the model Handy and AutoDoc both use on Windows).
- **canary-1b-v2** has slightly better German (4.46) but no timestamps in the catalog and is ~40% slower. Worth one test.
- **canary-180m-flash** (218 MB, ~22× real time) is the low-spec option: small download, still usable WER.
- **Whisper large-v3(-turbo)** has comparable WER and supports a context prompt, but at 1–1.4× real time on CPU it is only practical with a GPU. Whisper is also known to hallucinate on silence, which matters for a mostly silent "Me" track (unverified here; the Parakeet family has no autoregressive decoder and so doesn't loop the same way).
- **Qwen3-ASR-1.7B** has the best English and good German but is 5× slower than Parakeet and has no timestamps. Not for CPU.

## Mixed German/English

German workplace meetings switch languages mid-sentence and use English terms in German sentences. Unknown so far:

- whether Parakeet v3/ultra with `language = "de"` writes English terms correctly, and whether `language` auto-detect per chunk does better;
- whether vocabulary boosting with English terms works under `language = "de"`.

These are part of the experiment plan in [long-form-chunking.md](long-form-chunking.md#experiment-plan).

## Other model roles we need

| Role | Model | Size | Notes |
|---|---|---:|---|
| VAD | silero-vad-v6.2 (F32) | ~1 MB | MIT. For pause-aligned chunking and for skipping silence |
| Diarization | diar_streaming_sortformer_4spk-v2.1 (Q8_0) | 139 MB | NVIDIA Open Model License; ≤ 4 speakers. See [diarization.md](diarization.md) |
| Language ID | lang-id-voxlingua107-ecapa | 45 MB | Only if we auto-detect per chunk with a model that can't |

## Distribution

Models are downloaded on first use from Hugging Face (`handy-computer/<id>-gguf`, pinned revision, SHA-256 per file, as Handy's catalog lists them), not bundled. Licenses: Parakeet and Canary CC-BY-4.0 (attribution in the app's about/licenses page), orukeet CC-BY-SA-4.0, Sortformer NVIDIA Open Model License (read before shipping).
