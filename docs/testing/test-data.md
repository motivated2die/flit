# Test data

flit needs meetings with **separate tracks** (the user's mic, the meeting's audio) and **references** (what was said, by whom, when) to measure transcription accuracy, echo handling and diarization. Recordings of the author's own meetings will come later (ADR [0004](../decisions/0004-product-defaults.md): recordings are kept). Until then: public data, kept small.

## What exists publicly (searched 2026-11-21)

- **English meetings with one headset per speaker**: the [AMI Meeting Corpus](https://groups.inf.ed.ac.uk/ami/corpus/) (CC BY 4.0): 100 h, 4 people per meeting, individual headsets, a headset mix, word-level transcripts with times, and standard diarization references ([pyannote/AMI-diarization-setup](https://github.com/pyannote/AMI-diarization-setup)). Exactly the shape we need.
- **German meetings**: no openly licensed multi-speaker German meeting corpus with speaker references was found. CALLHOME German is telephone speech under a restricted license; VerbMobil is restricted; commercial sets (e.g. Terac) aren't open. Other open sets with diarization (AISHELL-4, AliMeeting) are Mandarin.
- **German speech with speaker IDs** (to assemble meetings from): Multilingual LibriSpeech German (CC BY 4.0, read audiobooks), VoxPopuli German (CC0, parliament speech, but its parquet files have ~470 MB row groups), FLEURS (CC BY 4.0, read sentences, no speaker IDs).

## The test meetings

Built by `go run ./cmd/flitdev testdata` into `testdata/meetings/` (git-ignored, ~145 MB; downloads ~160 MB into `.cache/`, all pinned by SHA-256 except the MLS clips, which come through signed URLs). Needs ffmpeg for the German clips (Opus).

| Meeting | Length | Language | Speakers | Tracks |
|---|---:|---|---|---|
| `ami-IS1009a` | 14:00 | English | 4 (1 Me, 3 Them) | me, them, mix, me-bleed |
| `ami-IS1009a-5min` | 5:00 | English | 4 | same, minutes 0:50–5:50 |
| `de-synth-4spk` | 5:22 | German | 4 (1 Me, 3 Them) | me, them, mix, me-bleed |

Each folder: `meeting.json` (source, license, attribution, speakers with their track and label, reference segments with text and times), the WAVs (16 kHz mono 16-bit) and `reference.rttm`. Read with `internal/eval`.

### AMI IS1009a

A design team's kick-off meeting (AMI test split, the same split Sortformer's published DER uses). Me = the project manager's headset (channel 0, FIE088); Them = the other three headsets summed.

The four sit **in one room**, so every headset also hears the others. An online meeting's loopback track never contains the user, so each headset is **gated to its wearer's reference turns** (padded by 0.25 s, 10 ms fades). Without that, the summed "Them" track contained the project manager's own words, and the echo filter rightly removed most of the "Me" track (first measurement, 2026-11-21). `me-bleed.wav` adds Them at −18 dB, 30 ms late, as loudspeakers would. `mix.wav` is AMI's original headset mix.

Reference text: AMI's manual word annotations (`ami_public_manual_1.6.2.zip`), joined into segments at pauses ≥ 1 s. Speaker reference: pyannote's `only_words` RTTM. The reference contains disfluencies ("uh", "um"), which counts against models that leave them out.

Attribution: AMI Meeting Corpus (Carletta et al., 2005), CC BY 4.0.

### de-synth-4spk

Synthetic: 20 utterances (10–20 s each) of 4 German speakers from MLS German's 1-hour subset, in a seeded random order, never the same speaker twice in a row, pauses of 0.25–1.2 s, about one turn in eight starting 0.5 s before the previous one ends. Each utterance normalised to the same level. `me-bleed.wav` as above. References are exact by construction.

Limits: read audiobook speech (often 19th-century literature) is clearer and more formal than meeting speech; turns are long; no real room acoustics. Good for German WER, echo handling and diarization mechanics; not a substitute for real meetings.

Attribution: Multilingual LibriSpeech (Pratap et al., 2020), German, from LibriVox, CC BY 4.0.

## Measurements so far

Parakeet Ultra (Q8_0), CPU only, Ryzen 7 5800X3D, `flit transcribe --meeting …` (2026-11-21):

| Meeting | Mic track | WER Me | WER Them | Speed (per track) |
|---|---|---:|---:|---|
| ami-IS1009a-5min | clean | 15.8% | 18.3% | ~19× real time |
| ami-IS1009a-5min | with bleed | 17.5% | 18.3% | |
| de-synth-4spk | clean | 8.4% | 10.7% | 17–30× |
| de-synth-4spk | with bleed | 8.4% | 10.7% | |

With bleed, the word-level echo filter removed 165 of 517 (AMI) and 487 of 670 (German) mic words. The first, text-similarity filter had given 28.1% and 21.2% there: see [research/diarization.md](../research/diarization.md#level-2-speaker-bleed-echo).

### Speakers on the meeting track

`flit transcribe --speakers --meeting …` (Sortformer 4spk v2.1 Q8_0, same PC, 2026-11-21): details and what goes wrong in [research/diarization.md](../research/diarization.md#speakers-measured-2026-11-21).

| Meeting | DER (meeting track) | Words with the right speaker | Speed |
|---|---:|---:|---:|
| de-synth-4spk | 16.8% | 88.7% | 31× |
| ami-IS1009a-5min | 51.6% | 78.9% | 30× |
| ami-IS1009a | 55.0% | 85.0% | 29× |

## Adding data

- A new public set goes into `cmd/flitdev/testdata.go` with pinned hashes, and into this document with its license.
- Own recordings: `testdata/private/` (git-ignored), same folder layout and `meeting.json`.
