# 0007: Transcribe during meetings, capped by Windows, speakers at the end

- **Status:** accepted; its default (off) superseded by [0009](0009-quiet-transcription-by-default.md)
- **Date:** 2026-11-21

## Context

flit transcribed after the meeting. On a capable PC, much of that work can be done during the meeting, so the transcript is ready soon after it ends. The author's conditions: no loss of accuracy (keep the long, pause-aligned chunks), a strict limit on CPU use so the meeting isn't slowed down, and a slow, steady load rather than bursts. Measurements: [research/live-transcription.md](../research/live-transcription.md).

## Options

1. **Short slices, live captions style** (Handy, most meeting tools): low delay, but less context per slice and worse words; against the author's first condition.
2. **The batch chunks, as soon as each is certain**, in a separate engine worker limited by Windows (job object hard CPU cap, below-normal priority, threads matched to the cap). The work comes at the meeting's pace.
3. **The same, limited by pacing in flit** (sleeping between chunks to average a target load): no OS guarantee; bursts at full load in between, which is what the author wants to avoid.
4. **Lower priority only** (no cap): the PC stays responsive, but an idle CPU is used fully, which shows as heat, fan noise and battery drain.

## Decision

Option 2, as a setting (**Transcribe during meetings**, off by default) with a **Background load** of Light (15%), Balanced (25%, default) or Faster (40%) of the processor. Each cut is decided on a fresh window from the same place, so the live transcript equals the transcript made afterwards (byte-identical on the test meetings). The final transcription goes on from where live transcription stopped; both save after every chunk. Telling speakers apart stays at the end, in one pass over the whole meeting track.

## Consequences

- On an 8-thread desktop even Light keeps up (both tracks need ~56% of the meeting's length at 15%); when the meeting ends, about one chunk per track is left (a few seconds), plus diarization (~2 min per hour of meeting).
- A second engine worker runs during recordings with the option on (the model loaded twice when a previous meeting is still being transcribed at full speed).
- When the PC is too slow for the chosen load, live transcription falls behind and the end does the rest; it never takes more than the cap.
- Live diarization waits for transcribe.cpp's streaming Sortformer API.
