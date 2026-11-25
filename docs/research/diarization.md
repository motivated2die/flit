# Speakers: Me, Them, and who among Them

Three levels, each building on the one before. Level 1 is the requirement; level 3 is the differentiator to explore, and it must stay light.

## Level 1: Me / Them by track

The mic track is "Me", the meeting track is "Them". Both are transcribed separately and merged by time, with word or segment timestamps. AutoDoc does exactly this and nothing more ([references/autodoc.md](../references/autodoc.md#speakers)). It needs no model at all, only time-aligned tracks ([audio-capture.md](audio-capture.md#aligning-the-tracks)).

## Level 2: speaker bleed (echo)

Without a headset, the mic records the remote speakers from the loudspeakers, so their words appear twice: in "Them" (clean) and in "Me" (echoed). The "Me" track can also pick up other people in the user's room, who are real "Me"-side speakers.

Options, cheapest first:

1. **Text-level suppression** after transcription, as AutoDoc does: drop a "Me" segment that closely matches a "Them" segment from 4 s before to 2 s after it (word Jaccard ≥ 0.5, containment ≥ 0.8 or trigram similarity ≥ 0.55; at least 3 words / 15 characters). Cheap, no audio processing, tunable on our own recordings. Fails on short echoes ("yes", "okay") and when the echoed copy is transcribed badly.
2. **Energy gating** before transcription: where the "Them" track is loud and the mic's level follows it with a short delay but is clearly quieter, the mic is mostly echo. Mute those stretches of the mic track before decoding. Cheap; risk of cutting the user off when both talk at once.
3. **Acoustic echo cancellation** using the "Them" track as the far-end reference (e.g. WebRTC's AEC3, or Windows' Voice Capture DSP in communications mode). The proper solution, but heavy: a native dependency and delicate timing.
4. Do nothing and recommend a headset.

Plan: 1 first (it also catches duplicates the others miss), measure on own recordings, add 2 if needed. 3 only if both fail.

### Measured (2026-11-21)

Text-level suppression is implemented in `internal/transcript/echo.go` and measured on the test meetings' bleed variants (Them mixed into the mic at −18 dB, 30 ms late; [testing/test-data.md](../testing/test-data.md)), Parakeet Ultra:

| Filter | AMI 5 min, bleed: WER Me | German synth, bleed: WER Me | AMI clean: words removed wrongly |
|---|---:|---:|---:|
| none (clean mic, for reference) | 15.8% | 8.4% | |
| AutoDoc-style: whole segments by Jaccard / containment / character trigrams | 28.1% | 21.2% | 2 segments |
| **word runs**: ≥ 3 consecutive words that Them said in the same order within ±2 s, gaps of ≤ 2 words filled | **17.5%** | **8.4%** | 4 words |

The segment-level filter failed two ways: character trigrams of two long German passages overlap heavily just because they're the same language, so real "Me" segments were dropped; and a segment holding both the user's words and the echo is all-or-nothing. Matching *runs of words in order and in time* removes the echo and keeps the user's words around it. On the German meeting the result equals the clean mic; on AMI 1.7 points remain, mostly echo words transcribed differently on the two tracks. This is the current implementation; energy gating (option 2) isn't needed for now.

## Level 3: speakers within "Them"

Remote participants arrive mixed into one audio stream by the meeting app; capturing per process doesn't separate them. Telling them apart needs diarization of the "Them" track.

### Sortformer through transcribe.cpp (built)

`diar_streaming_sortformer_4spk-v2.1` (Q8_0, 139 MB) from the same engine DLL ([references/transcribe-cpp.md](../references/transcribe-cpp.md#diarization-sortformer)); decision: [ADR 0006](../decisions/0006-speakers-with-sortformer.md); how other apps do it: [references/speaker-apps.md](../references/speaker-apps.md).

How it runs:

- **Binding** (`internal/engine/tcpp/diarize_windows.go`): the DIARIZE role (`transcribe_diarize_session_init/run/get_segment`), structs size-checked against the DLL like the rest; the Sortformer extension sets the **VERY_HIGH_LATENCY** preset (~30 s lookahead), the offline-file point NVIDIA's DER is published at. Turns come back grouped by speaker; the binding sorts them by time.
- **Worker**: `OpDiarize` sends the whole meeting track (an hour is 230 MB of float32 over the pipe); the worker loads the model on the first request only.
- **One pass over the whole track**, so speaker numbers hold from the first minute to the last (the model's arrival-order speaker cache carries identities across its 34 s chunks).
- **Words to speakers** (`transcript.AssignSpeakers`): each word takes the speaker it overlaps most, else the nearest turn within 1 s, else its neighbours' speaker in the segment; runs of ≤ 2 words of another speaker inside a segment, with the same speaker on both sides, are smoothed away; a speaker with < 3 s and < 2% of the track's speech is a fragment and its words go to the speakers around them; numbers are reassigned 1, 2, … by first appearance; segments split where the speaker changes. One speaker left: plain "Them".
- **Names**: `speakers.json` beside the transcript; the user renames on the recording's page and `transcript.txt`/`.md` are rewritten. Two speakers with the same name read as one person (their consecutive turns join), which is how a split is repaired.

### Speakers measured (2026-11-21)

CPU (Ryzen 7 5800X3D), `flit transcribe --speakers --meeting testdata/meetings/<m>`; DER on the meeting track against the Them speakers' reference turns (10 ms frames, no collar, overlap scored, `eval.ScoreDER`); "words right" = the share of transcribed words on the meeting track whose speaker (after `AssignSpeakers`, best one-to-one mapping) is the reference speaker at the word's midpoint (`eval.ScoreAttribution`; words in overlapped reference speech not scored).

| Meeting (meeting track) | Length | People | DER | Words right | Speed |
|---|---:|---:|---:|---:|---:|
| de-synth-4spk | 5:22 | 3 | 16.8% (conf. 22 s, missed 16 s) | **88.7%** | 31× |
| ami-IS1009a-5min | 5:00 | 3 | 51.6% | **78.9%** | 30× |
| ami-IS1009a | 14:00 | 3 | 55.0% | **85.0%** | 29× |

For comparison, the AMI headset **mix** (all four people, the condition NVIDIA publishes for): 24.3% DER on the 14-minute meeting, of which confusion 3.7%, missed speech 17% (short words and backchannels; the published 14.7% uses forced-alignment references and a tuned post-processing, ours neither). So the binding behaves like the reference; the high DER on AMI's meeting track comes from how that track is built (three gated headsets summed: abrupt level changes, padding; a real meeting's audio is one continuous mix) and is mostly false alarm and missed speech, which matter little for labelling words.

The three operating points (DEFAULT, VERY_HIGH, HIGH latency) scored within 1 point of each other on all three tracks.

What goes wrong:

- **One person split into two labels.** de-synth: speaker 12275's first and last turns are "Speaker 1", the three in the middle "Speaker 4" (all from the same audiobook recording, so not a channel change). This costs the 11% there. The user repairs it by naming both the same.
- **Two people merged into one label.** ami-5min: two of the three remote people share one label for most of the excerpt. Not repairable by naming; the words would need re-splitting. (2026-11-22: with line-level evidence from the call app, `callnames.Refine` re-splits such a voice line by line; simulated on this excerpt, 93% of words named right instead of 82% at best by naming whole voices, [call-app-names.md](call-app-names.md#matching-name-voices-correct-lines).)
- Tried and rejected: asking Sortformer itself whether two labels are one voice (a short clip alternating 3 pieces of each, diarized again). On de-synth it kept 12275's two labels apart, the same mistake as in the long run; on AMI the pieces themselves broke into several labels. Not a usable verifier.

Not measured yet: German real meetings (no open data), more than 4 people, hour-long meetings. The author's own recordings are the next data.

Limits to handle:

- More than 4 remote speakers: Sortformer can't represent them; they share labels. The setting says "up to four".
- Overlapping speech: each word gets one speaker.
- In-person and hybrid meetings (multiple people on the microphone): handled by diarizing the `me` track after echo suppression when a secondary-speaker threshold is crossed (see [Diarizing the microphone track](#diarizing-the-microphone-track-2026-11-25) below).

### Modern diarization models (2026-11-25)

Survey of open-weight models beyond Sortformer 4spk v2.1 and their fit with flit's architecture (Go without cgo, single `transcribe.cpp` GGML/Vulkan worker, Windows):

| Model / stack | Max speakers | Architecture & benchmarks | License | Fit with flit |
|---|---:|---|---|---|
| **NVIDIA Nemotron 3 Diarization** *(Sep 2026)* | **8** | ~100M params, pure 31-layer Transformer + RoPE + Arrival-Order Speaker Cache (AOSC), 10 ms output, streaming & offline. #1 on VoiceArena Diarization-Bench (14.72% DER); SotA on DIHARD III, CALLHOME, AMI (arXiv:2611.12246). Acoustic-based; expected to generalize to German like Sortformer (unverified). | OpenMDW 1.1 | **Best fit.** In progress in `handy-computer/transcribe.cpp` (Issue `#170`, PR `#175` *"nemotron3 diarization port"*, open as of 2026-11-25; not in v0.4.2). Because `internal/engine/tcpp/diarize_windows.go` already binds the generic `DIARIZE` C role and reads `MaxSpeakers` dynamically, adoption once released only needs bumping the pinned engine bundle and adding the GGUF to `internal/models/catalog.go`. |
| **`NVIDIA/NeMo-Speech.cpp`** *(runtime for Nemotron 3)* | **8** | Official GGML-based C++17 runtime with CPU + Vulkan Windows releases and C API (`nemo_speech_diar_*`) / CLI (`nemo-speech diarize`). | Apache-2.0 | Viable fallback or early benchmark tool, but GGML has no cross-version plugin ABI ([gpu-transcription.md](gpu-transcription.md)), so shipping it beside `transcribe.cpp` would duplicate ~44 MB of `ggml-vulkan.dll`. |
| **`sherpa-onnx`** (`pyannote-segmentation-3.0` + `CAM++` / `WeSpeaker` / `TitaNet`) | **Unlimited** | Modular segmentation + speaker embeddings + clustering. | Apache-2.0 / MIT | Callable without cgo via `sherpa-onnx`'s C API DLL (`syscall`), but adds a second runtime (ONNX Runtime DLLs, tens of MB), uses CPU/DirectML instead of Vulkan, and needs clustering tuning. Better kept as a dev prototype path for embeddings ([speaker-identity.md](speaker-identity.md)). |
| **Pyannote `community-1` (v4.0) / DiariZen / Senko** | **Unlimited** | Top accuracy (DiariZen: 80%-pruned WavLM-Large + Conformer + VBx; Pyannote 4.0: exclusive single-speaker mode) or speed (Senko: optimized 3D-Speaker). | MIT / Apache-2.0 (Pyannote gated) | Poor fit to ship: orchestration relies on Python/PyTorch + SciPy/VBx/HDBSCAN; no GGML/`transcribe.cpp` ports. |
| **Ultra-Sortformer** *(community 5–8 spk fine-tunes)* | 5–8 | SVD-widened Sortformer v2.1 heads. | NVIDIA Open Model | Superseded by official Nemotron 3 Diarization. |

### Diarizing the microphone track (2026-11-25)

In-person meetings recorded through the PC's microphone have no `them` speech, and hybrid calls can have several people sharing one room mic. Treating `me` unconditionally as one `"Me"` speaker collapses everyone in the room into one label.

Design (implemented in `internal/transcript`, `internal/session`, `internal/app`):

1. **Suppress echo before evaluating the mic track.** Without a headset on a remote call, loudspeaker bleed from `them` lands in `me.m4a` and looks like extra speakers to the diarizer. Running `transcript.SuppressEcho(me, them, ...)` first strips echoed words before mic diarization is thresholded.
2. **Diarize `me` when it has enough speech.** When speakers are enabled and post-echo `me` has at least **10 s** of transcribed speech, run the same `Diarize` worker call on `me` and map turns onto the post-echo `me` segments via `transcript.AssignSpeakers` (which applies flicker smoothing and `< 3 s` / `< 2%` fragment removal).
3. **20% secondary-speaker threshold (`transcript.MicDiarizeMinShare`).** Compute the non-dominant speakers' share of post-echo `me` speech: `1 - maxSpeakerTalk / totalTalk`.
   - Below **20%** (or `< 2` speakers): discard mic speaker IDs (`Speaker = 0`), keeping the track as plain `"Me"`. This protects solo-mic calls from occasional diarizer splits or brief background chatter.
   - At or above **20%**: keep the diarized speaker IDs on `me` and **do not assume Speaker 1 is `"Me"`**.
4. **Neutral labeling and naming.** Unnamed diarized `me` speakers default to `"Room 1"`, `"Room 2"`, … (never `"Me"`, and distinct from `them`'s `"Speaker 1"`, `"Speaker 2"`, … in hybrid calls). User names live in `speakers.json` under `meSpeakers` (backwards-compatible); giving any two speakers the same name merges them into one person as before.
5. **Re-analysis on kept audio.** `Session.FindSpeakers` and the recording page's *"Analyze speakers"* action work whenever either `me` or `them` audio was kept, supporting pure in-person recordings.

### Next steps

1. Measure on real meetings (DER needs a reference: label who spoke when by hand for 10–15 minutes of two or three meetings), including in-person and hybrid mic-track recordings to tune the 20% threshold.
2. Adopt **Nemotron 3 Diarization** (8 speakers) once `handy-computer/transcribe.cpp` PR `#175` lands in a release.
3. If splits are common: speaker embeddings to merge labels that are one voice (an ECAPA-TDNN speaker-embedding role in `transcribe.cpp`), comparing each label's best stretches. The same embeddings would also recognise people across recordings: [speaker-identity.md](speaker-identity.md).

### Alternatives (recorded, not planned)

| Option | Why not (now) |
|---|---|
| sherpa-onnx: pyannote segmentation + speaker embeddings + clustering | No speaker limit, but a second runtime (ONNX Runtime DLLs, cgo Go bindings) |
| pyannote.audio (AutoDoc's disabled experiment) | Python + torch; far too heavy |
| MOSS-Transcribe-Diarize (transcribe.cpp) | ASR with speaker markers, but English and Chinese only |
| multitalker-parakeet-streaming-0.6b-v1 | Speaker-attributed ASR for overlapping speech, English only |
| Meeting-app speaker info (active-speaker events) | No public API for Teams/Zoom desktop clients. **Update 2026-11-22**: their windows' UI Automation trees carry the roster and, for Zoom (and sometimes Teams and Meet), who is talking; Yogurt uses this. Now proposed as a name source on top of Sortformer: [call-app-names.md](call-app-names.md) |

## Experiment plan

On own recordings of meetings with 2–5 remote speakers, German and English (tooling exists: `flit transcribe --speakers --meeting <folder>` with a `meeting.json` and `reference.rttm`, see [testing/test-data.md](../testing/test-data.md#adding-data)):

- DER of Sortformer on the "Them" track, against a hand-made reference of who spoke when;
- the share of words with the right speaker after mapping;
- how it fails with 5+ speakers;
- run time per hour of audio on CPU (~30× real time on the test meetings);
- text-level echo suppression: share of echoed segments removed, share of real "Me" segments wrongly removed, with and without a headset.
