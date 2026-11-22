# 0009: Transcribe during meetings and limit the run after, by default

- **Status:** accepted; supersedes the default (off) of [0007](0007-live-transcription.md)
- **Date:** 2026-11-22

## Context

[0007](0007-live-transcription.md) made transcribing during meetings a setting, off by default. Since then the transcription after a meeting got a limit of its own (Settings → Transcription), measured on a 5-minute meeting: 20 s unlimited on all 8 cores, 24 s at Balanced (50%), 44 s at Light (25%) ([research/engine-cores.md](../research/engine-cores.md)). Unlimited, the run after a meeting spins the PC up; at Balanced it costs a fifth more time. The author asked for both to be on by default, at Balanced, and offered on the welcome page.

## Options

1. **Keep both off.** The fastest transcript when the meeting ends, at the cost of the PC spinning up for it, and nothing done during the meeting.
2. **Both on, Balanced.** During the meeting at most 25% of the processor, at low priority; after it at most 50%. Most of the transcript is done when the meeting ends, so the capped run after it has only the tail and the speakers left.

## Decision

Option 2: **Transcribe during meetings** on, Background load Balanced (25%); **Limit the transcription after a meeting** on, Load Balanced (50%). Both are on the welcome page, so the choice is seen when flit is set up. Existing settings files keep what they say: an installation that saved live transcription off stays off.

## Consequences

- A new installation never takes more than half the processor for transcribing, and a quarter during a meeting.
- After a meeting without live transcription (a recording by hand while it was off, or a long one), the run takes about a fifth longer.
- The limit's switch is saved even when off (`limitFinal`), since off is no longer the default.

## Revisit when

- Users find the transcript too slow to arrive after long meetings, or the steady live load noticeable during calls on weaker PCs.

## Note (2026-11-22, later)

At the author's request the two switches left the welcome page again, to keep it short; they stay on and Balanced by default, in Settings → Transcription. The decision itself is unchanged.
