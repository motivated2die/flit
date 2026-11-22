# Speakers across recordings: naming a person once

Question (2026-11-21): can flit recognise the same person in later meetings, so the user names a regular participant once instead of once per recording? Constraint: stay light, keep the transcription pipeline robust. This note records what the code does today, what the references offer, what exists elsewhere, and a recommendation. Nothing here is built or measured yet; guesses are labelled.

## What flit does today

- Sortformer numbers the meeting track's speakers **by arrival order within one recording** (`internal/engine/tcpp/diarize_windows.go`). "Speaker 2" today and "Speaker 2" tomorrow are unrelated; the numbers carry no identity.
- `transcript.AssignSpeakers` (`internal/transcript/speakers.go`) maps words to those numbers, removes flickers and fragments, and renumbers by first appearance.
- Names are per session: `speakers.json` holds `transcript.Names{Me, Them, Speakers map[int]string}` (`internal/transcript/format.go`). Running diarization again clears `Speakers` (`internal/session/transcribe.go`, `assignSpeakers`), because the numbers may change.
- The recording page (`internal/app/speakers_windows.go`) shows each speaker with a quote, talk time and a play button. `transcript.Clips` already picks **each speaker's longest stretches where nobody else talks** (±0.5 s margin, both tracks). Those are the clips a voice match would embed.
- Audio is kept by default (`config.KeepAudio: true`). "Free old audio" removes it after 30 days (`internal/app/cleanup_windows.go`). So the meeting track is usually there when a speaker is named.
- The calendar parser keeps title, time, location, link and provider. It keeps **no attendees and no descriptions** (`internal/calendar/events.go`, `Meeting`). Recurring events share a `UID`.

The "Me" track already has an identity: it is always the user. Cross-recording matching only concerns the people on the meeting track.

## What the references offer

| Source | Cross-recording speakers? | What's usable |
|---|---|---|
| transcribe.cpp `cfa237c` | No speaker-embedding role. Roles: ASR, VAD, LANGID, DIARIZE (`include/transcribe/`). No upstream issue asks for one (searched issues for "speaker" and "embedding", 2026-11-21). | **ECAPA-TDNN is already ported** (`src/arch/ecapa_tdnn/`, family doc `docs/porting/families/ecapa_tdnn.md`), but only as SpeechBrain's VoxLingua107 *language* ID. The graph computes the embedding (`enc.emb`, `graph.cpp`, before the classifier); the public `langid.h` returns only logits. That language embedding was trained to separate languages, not voices, so it is not a voiceprint. The *architecture* matches SpeechBrain's speaker model (`spkrec-ecapa-voxceleb`, Apache-2.0), though. |
| transcribe.cpp Sortformer | No | Its speaker cache is ordered by arrival and internal to one run. It isn't exposed and wasn't trained for comparison across recordings. |
| AutoDoc `5082757` | No | The rename dropdown offers the **calendar invite's attendees** as names (`docs/design/specs/2026-03-25-speaker-diarization-design.md`, "suggestions"). No voice matching. Idea only (AGPL). |
| Handy `4807c21` | No | Dictation app, nothing about speakers beyond catalog entries for Sortformer and MOSS. |
| Mac apps ([speaker-apps.md](../references/speaker-apps.md)) | Argmax announced "speaker identification" across recordings, not shipped (as of the 2026-11-21 survey) | All of them have rename and merge; none confirmed to recognise people across files. |

## Building blocks found elsewhere

Recognising a person across recordings is **speaker verification**. A small network turns a few seconds of speech into a fixed vector (an embedding, 192–512 numbers). Two clips of the same voice give vectors with high cosine similarity. The library of known people is then just stored vectors plus a threshold. Only the embedding network is hard; the search is a dozen lines of Go.

Embedding models (figures from their papers or cards; not measured by us):

| Model | Size | License | Notes |
|---|---|---|---|
| SpeechBrain `spkrec-ecapa-voxceleb` (ECAPA-TDNN) | ~20 M params (from memory, to check) | Apache-2.0 (to confirm on the card) | Same family transcribe.cpp already runs, so a port would reuse its graph, front end and parity tooling |
| CAM++ (3D-Speaker, Alibaba) | ~51% fewer params than ECAPA, 18% lower EER on VoxCeleb1-O ([arXiv 2303.00332](https://arxiv.org/abs/2303.00332)) | Apache-2.0 (repo; a mirror of the weights says the same, [HF](https://huggingface.co/OpenASR/campplus-voxceleb)) | Lighter. Would be a new family in any engine |
| NVIDIA TitaNet-L | 0.68% EER VoxCeleb1 (paper, [arXiv 2110.04410](https://arxiv.org/pdf/2110.04410v1.pdf)) | CC-BY-4.0 (to confirm) | NeMo checkpoint; no GGML port known |
| WeSpeaker ResNet34 | — | not confirmed | pyannote's embedding model |

Ways to run one from Go without cgo:

1. **A speaker-embedding role in transcribe.cpp**: SpeechBrain's speaker ECAPA converted with the existing family, and an API that returns `enc.emb` instead of logits. This fits best: the same DLL, binding style (`internal/engine/tcpp`) and worker. It needs an upstream contribution or a patch we carry. Effort guess: the converter changes for a different classifier head and label-free GGUF; a small C role (`transcribe_embed_*`); parity against SpeechBrain with the repo's own `validate.py`. Days, not weeks, if the front end matches (unverified: the speaker model's Fbank settings may differ from the language model's).
2. **sherpa-onnx's C API DLL, called by `syscall` like `transcribe.dll`**: no cgo. It has `SherpaOnnxSpeakerEmbeddingExtractor*` and `SherpaOnnxSpeakerEmbeddingManager{Add,Search,Verify,GetBestMatches}` (read from `sherpa-onnx/c-api/c-api.h`, master, 2026-11-21). It runs CAM++, ECAPA, WeSpeaker and NeMo ONNX exports. The catch is a second runtime: ONNX Runtime plus sherpa's DLL, tens of MB (size not checked). Good for a **prototype measurement**, a poor fit to ship ([0001](../decisions/0001-stay-with-go.md), [0006](../decisions/0006-speakers-with-sortformer.md) option 2).
3. **ONNX Runtime via purego** (`shota3506/onnxruntime-purego`, `amikos-tech/pure-onnx`): no cgo, but the same second runtime, and the bindings call themselves unstable. No advantage over 2.
4. **Our own ECAPA or CAM++ in pure Go**: no new runtime, but a network to implement, optimise and validate ourselves (ADR 0006 option 3). Speed is fine for a few clips per speaker. The ongoing cost is owning numerical parity. Only if 1 is blocked.

Not considered further: pyannote/Python (too heavy); Picovoice Eagle (proprietary SDK with a license key); cloud speaker-recognition APIs (audio leaves the machine; Microsoft's was retired, from memory); meeting-app participant lists (no public API for the desktop clients, [diarization.md](diarization.md#alternatives-recorded-not-planned); but their UI Automation trees carry them, see [call-app-names.md](call-app-names.md), 2026-11-22).

Signals that need no model:

- **Names the user has typed before**: offer them in the rename field, ranked. This makes naming faster but doesn't identify anyone.
- **Recurring series**: occurrences share a calendar `UID`, so people named in earlier occurrences are likely candidates. This is a strong prior that costs nothing.
- **Calendar attendees** (AutoDoc's idea): would need `ATTENDEE;CN=` parsed, which we drop today. Names only, no addresses, kept with the matched meeting. It narrows the candidates, but invites often list rooms, lists or no-shows.

## How it would work (sketch, if built)

1. **Implicit enrolment, no sample recordings.** When the user names a meeting-track speaker, embed that speaker's `transcript.Clips` stretches (say up to 6 × 8 s) from the kept audio and store them under the name. Clips from real meetings match later meetings' audio conditions (codec, the person's usual headset) better than a separate enrolment recording would. An explicit "this is how Anna sounds" sample adds a UI flow for little gain. Guess: skip it.
2. **Store per meeting, not one average.** A person keeps one vector per meeting they were named in (capped, e.g. the last 20) in the data directory, outside any session. This handles different mics and codecs. A vector also records which session it came from, so deleting a recording or a person removes it.
3. **Matching after diarization.** For each meeting-track speaker with enough clean speech (guess: ≥ 15 s; below that, no suggestion), compare with every known person (best few per-meeting vectors). Assign one-to-one across the meeting's speakers. Suggest a name only above a threshold *and* with a margin over the runner-up. Speakers in the same recurring series rank first.
4. **Suggest, don't overwrite.** The recording page shows "Anna?" with one click to accept. Only accepted names add vectors. A wrong match is then a visible suggestion, not a silent error that gets written into the transcript and fed back into the library. Auto-accept only after measurement says the threshold holds.
5. **Splits get repaired along the way.** If Sortformer splits one person into two labels and both match Anna, both get the suggestion, and accepting merges them (same name, existing behaviour). With the same vectors, ADR 0006's open option 3 (merging splits inside one meeting) costs little extra.
6. **Merges get flagged.** A label whose clips disagree with each other (low pairwise similarity) is probably two people. No suggestion for it, and don't enrol it.

What doesn't change: transcription, diarization and `AssignSpeakers` stay as they are. Matching is a separate step after them, and a failure there leaves today's result. Rerunning diarization renumbers speakers; matching would then suggest the names back.

## Risks

- **Channel and codec variation across meetings** (Teams/Zoom Opus, changing headsets, loudspeaker echo on the user's side doesn't affect the loopback track) is the main reason verification degrades outside VoxCeleb's conditions. The guess is that several vectors per person absorb most of it; this is what the measurement must show.
- **Short talkers.** People who say little in a meeting give few or no clean clips, so they get no suggestion. That's acceptable.
- **Sortformer's merges** contaminate vectors if enrolled. Hence the consistency check and enrolling only what the user confirmed.
- **German.** Speaker embeddings are largely language-independent but trained mostly on English (VoxCeleb). Test on the German synthetic meeting and the author's recordings.
- **Privacy.** A stored voiceprint identifies a person. Under GDPR, biometric data used to identify someone is a special category (Art. 9). Not legal advice, but the design should assume it: opt-in, local only, a list of known voices with per-person delete, deleted along with their recordings if wanted, and never in logs (as with transcript text).

## Experiment plan (before any UI)

1. **Data that already fits.** AMI scenario meetings come in series of four with the same people (IS1009a–d). `flitdev testdata` already builds IS1009a, and b–d can be built the same way. Enrol the three "Them" people from IS1009a, then identify them in b, c, d. That gives a true-match rate, a wrong-match rate and the abstain rate per threshold. Then do the same across the author's own recurring meetings.
2. **Prototype the embedding through sherpa-onnx's C API** (option 2, dev tool only in `cmd/flitdev`, not shipped) with CAM++ and ECAPA ONNX exports. That answers "is it accurate enough on our audio?" before investing in a transcribe.cpp role.
3. If the numbers are good (guess at a bar: ≥ 90% right among suggestions made, < 2% wrong suggestions), do option 1 (ECAPA speaker role in transcribe.cpp, ideally upstream), write an ADR, then build the library and the suggestion UI.

Until then, the zero-model improvements (previously typed names in the rename field, series history) are safe to build on their own.
