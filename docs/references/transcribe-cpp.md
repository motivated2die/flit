# transcribe.cpp: the planned engine

[transcribe.cpp](https://github.com/handy-computer/transcribe.cpp) (MIT) is a C/C++ speech library on [ggml](https://github.com/ggml-org/ggml) that runs 20+ model families as GGUF files: ASR (Parakeet, Canary, Whisper, Qwen3-ASR, Cohere, Granite, Moonshine, Voxtral, …), plus VAD (Silero), language ID (ECAPA-TDNN) and diarization (Sortformer). It is made by the Handy authors and is Handy's only engine since v0.10. Analysed at `cfa237c` (2026-11-21; latest release v0.4.2 from 2026-11-12). Paths are relative to its repo.

## Why it fits us

1. **A plain C ABI.** One public header, `include/transcribe.h` (2.8k lines, ~94 functions), plus family extensions in `include/transcribe/*.h` (VAD 17, language ID 16, diarization 11, Whisper 4, Parakeet 2, …). No C++ types cross the boundary; no exception escapes an entry point (`transcribe.h` header comment).
2. **Built for bindings** (`docs/bindings.md`). Every struct starts with `uint64_t struct_size` and has an `_init` function that fills defaults and the size. Text results are borrowed `const char *` to copy at the boundary. An ABI digest (`include/transcribe.abihash`) and a `contract.json` in each native bundle (`version`, `header_hash`, `backends`) let a binding refuse a mismatched DLL **before** loading it. Official bindings: Python, TypeScript, Rust, Swift; a third-party .NET one on NuGet.
3. **Prebuilt Windows DLLs.** Each release ships `transcribe-native-<ver>-windows-x86_64-cpu-vulkan.tar.gz` (and a CUDA variant). v0.4.2's bundle (18 MB compressed) contains:

   | File | Size |
   |---|---:|
   | `transcribe.dll` | 2.0 MB |
   | `ggml.dll`, `ggml-base.dll` | 0.7 MB |
   | `ggml-cpu-{x64,sse42,sandybridge,haswell,skylakex,icelake,cascadelake,cannonlake,alderlake}.dll` (one is picked at runtime for the CPU) | 9 × ~1 MB |
   | `ggml-vulkan.dll` | 43.7 MB |
   | `contract.json`, licenses (transcribe.cpp, ggml, miniz, silero_vad: MIT-style) | |

   CPU-only, the engine is **~12 MB of DLLs**; Vulkan GPU support adds 44 MB. No MSVC needed on our side; building from source needs Visual Studio Build Tools + CMake (`docs/build-windows.md`).
4. **Everything we need in one engine**: ASR with segment/word/token timestamps, Silero VAD with per-frame probabilities (`transcribe_vad_probs`), Sortformer diarization (`include/transcribe/diarize.h`), language ID, and vocabulary biasing.

## API essentials

```c
transcribe_init_backends(artifact_dir);               // once; DLL folder for dynamic backends
transcribe_model_load_params_init(&lp);  transcribe_model_load_file(path, &lp, &model);
transcribe_session_init(model, &sp, &session);
transcribe_run_params_init(&rp);  rp.language = "de";  rp.timestamps = …WORD;
rp.vocabulary = terms;  rp.n_vocabulary = n;          // phrase boosting
transcribe_run(session, pcm_f32_16k_mono, n_samples, &rp);
transcribe_n_segments(session);  transcribe_get_segment(session, i, &seg);  // t0_ms, t1_ms, text, speaker_id
transcribe_set_abort_callback(session, cb, user);     // cancellation
transcribe_log_set(cb, user);                         // once, at start-up
```

- Input is **16 kHz mono float32** PCM.
- `transcribe_run_batch` decodes several utterances in one call (a GPU throughput win).
- Streaming (`transcribe_stream_begin/feed/finalize`, `transcribe_stream_get_text` with committed and tentative text) exists for streaming-capable families. We don't plan to use it (see [research/long-form-chunking.md](../research/long-form-chunking.md)).
- **No progress callback** for an offline run. Progress comes from our own chunking (chunks done / chunks total, or audio seconds done).
- Errors: `TRANSCRIBE_ERR_BACKEND` is recoverable by reloading on CPU; `TRANSCRIBE_ERR_INPUT_TOO_LONG`, `…OUTPUT_TRUNCATED`, `…OUTPUT_REPETITION` (loop detection for greedy decoders) are explicit, partial text stays readable.

## Constraints to design around

- **Pre-1.0 ABI.** "The on-disk ABI MAY break between 0.x minor releases" (`transcribe.h`). Pin one version, ship its DLLs with the app, check `contract.json`'s `header_hash` against the hash our binding was written for, and update deliberately.
- **One compute at a time per model.** "At most one `transcribe_run` / `transcribe_run_batch` / active stream may be in flight across ALL sessions of a given model" (`transcribe.h`, threading notes). Two tracks are transcribed one after the other (or with two loaded models, doubling memory). This fits a single worker process with a job queue.
- **The log callback may come from any thread**, including ggml's worker threads; install it once before loading anything.
- **Input length**, per family (`docs/input-limits.md`): Whisper and Parakeet have no practical limit (Whisper slices into 30 s windows internally; Parakeet's conformer encoder is "effectively unbounded"), others reject over-long input up front. `transcribe_capabilities::max_audio_ms` reports the usable limit. Parakeet-ultra additionally cuts audio over 30 s at pauses found by its own VAD head (`docs/porting/families/parakeet.md`, "Long-form pause segmentation (>30 s)").

## Vocabulary (custom terms)

`docs/prompting.md`: Parakeet (TDT and RNN-T heads), Whisper, Qwen3-ASR, Granite, Fun-ASR-Nano and MOSS take a `vocabulary` list. For Parakeet it is **phrase boosting**: terms are tokenized as the model writes them and form an Aho-Corasick automaton; once the model has started a term on its own, each further matching piece gets a logit bonus of 3.0, a broken match gives the bonus back. "Biasing therefore changes which word is written, never whether one is, so it cannot invent speech on silence." With no terms, decoding is bit-exact. **Offline runs and batches take terms; streaming ignores them.** Whisper and Qwen3-ASR additionally take a free-text context prompt.

This is how names, product terms and jargon (the "specific language" problem) can be addressed with a per-user vocabulary list.

## Diarization (Sortformer)

`docs/models/diar_streaming_sortformer_4spk-v2.1.md`: NVIDIA's streaming Sortformer, "who spoke when" for **up to 4 speakers**, numbered in order of first appearance, no text. Q8_0 is 139 MB; DER 14.7% on AMI IHM test (NeMo reference 14.8% under the same protocol). Only near-reference quantizations ship, because k-quants could flip speaker labels mid-stream. License: NVIDIA Open Model License. Trained and evaluated on English; behaviour on German is **unverified**. API: `transcribe_diarize_session_*`, `transcribe_diarize_run`, `transcribe_diarize_n_segments`, `transcribe_diarize_get_segment`. Details and plan: [research/diarization.md](../research/diarization.md).

## Speaker embeddings (none yet)

No role returns speaker embeddings (as of `cfa237c`; no upstream issue asks for one). The ECAPA-TDNN family (`src/arch/ecapa_tdnn/`) is ported for SpeechBrain's VoxLingua107 **language** ID only. Its graph computes the pooled embedding (`enc.emb` in `graph.cpp`) before the classifier, but `langid.h` returns only logits. SpeechBrain's speaker model uses the same architecture, so a speaker-embedding role would build on this family. Why it matters: [research/speaker-identity.md](../research/speaker-identity.md).

## Binding from Go without cgo

The calling conventions are friendly to `golang.org/x/sys/windows` lazy DLL calls (as Resound calls Win32 and Direct2D):

- all structs are passed by pointer; scalars are ints, `bool`, pointers; no float arguments by value in the core calls checked so far (`transcribe_run`: session, `const float *`, `int`, params pointer);
- the two callbacks (`transcribe_log_callback`, `transcribe_abort_callback`) are plain C function pointers with a `void *` user argument, which `windows.NewCallback` provides (Go supports such callbacks from foreign threads);
- every `_init` function writes `struct_size`, so at start-up the binding can compare it to Go's `unsafe.Sizeof` of its mirror struct and refuse to run on a layout mismatch.

What it costs: Go mirror structs for the ~15 structs we use, written by hand (or generated from the header), re-checked whenever the pinned version changes. A test that loads the real DLL and checks every struct size belongs in CI.
