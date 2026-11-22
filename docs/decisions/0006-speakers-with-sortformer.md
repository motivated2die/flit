# 0006: Speakers with Sortformer, renamed by the user

- **Status:** accepted
- **Date:** 2026-11-21

## Context

The author asked for speaker labels in transcripts that hold for a whole meeting, and for names the user can give them, saved beside the transcript. "Me" and "Them" come from the two tracks already; the open part is telling the people on the meeting's track apart ([research/diarization.md](../research/diarization.md)). How other apps do it: [references/speaker-apps.md](../references/speaker-apps.md).

## Options

1. **Sortformer 4spk v2.1 through transcribe.cpp**: already in our engine DLL, one 139 MB model, ~30× real time on CPU, one pass over the whole track keeps numbers stable. At most 4 speakers on the meeting's side; trained on English.
2. **Embedding + clustering (pyannote-style)** via sherpa-onnx: no speaker cap, but a second runtime with cgo ([0001](0001-stay-with-go.md) rules that out) and three models to tune.
3. **Our own speaker embeddings in Go** (e.g. an ECAPA-TDNN port) to link or merge Sortformer's speakers: no runtime, but a neural network to implement and validate. Worth it only if Sortformer's splits prove common in real meetings.
4. **Wait for Nemotron 3 Diarization (8 speakers)** in transcribe.cpp: not available yet.

## Decision

Option 1 now, behind the same DIARIZE-role binding so 4 can replace the model later. The meeting track is diarized after transcription in one pass at Sortformer's offline operating point; each word takes the speaker it overlaps most; flickers and fragments are cleaned up ([`transcript.AssignSpeakers`](../../internal/transcript/speakers.go)). The user names speakers on the recording's page; names live in `speakers.json` and the `.txt`/`.md` transcripts are rewritten with them. Giving two speakers the same name merges them, which repairs one of the two errors seen on the test meetings (one person split in two; the other, two people merged into one label, it cannot repair). On by default ("Tell speakers apart" in Settings); a failure leaves the transcript as Me/Them and says why.

## Consequences

- Measured on the test meetings: 79–89% of words on the meeting's side get the right person ([research/diarization.md](../research/diarization.md#speakers-measured-2026-11-21)). Not yet measured on real meetings; the author's recordings are next.
- More than 4 people on the meeting's side get merged into 4 labels. The UI says "up to four".
- Diarization takes ~2 min per hour of meeting (measured: ~30× real time). Its memory in the worker is not measured yet (guess: the 139 MB model plus working buffers); the model loads only when a diarize request comes.
- Option 3 stays open if real meetings show frequent splits.
