# 0012: Speaker names from the call app's window, voted onto Sortformer's voices

- **Status:** proposed
- **Date:** 2026-11-22

## Context

Sortformer tells the meeting track's voices apart ([0006](0006-speakers-with-sortformer.md)), but the user names them on every recording. The author wants names filled in automatically, reliably, lightly and without intruding. Research: [research/call-app-names.md](../research/call-app-names.md).

- No meeting app offers a local, free interface for who is in a call or who talks. Teams' local "third-party app API" never had participants and was reportedly retired on 2026-06-30. The routes that know who talks (Graph transcripts, Zoom Apps SDK, RTMS, Meet APIs, Webex SDKs) need accounts, app registrations, admin consent, a bot in the call, or the platform's own recording.
- The apps' accessibility trees (UI Automation, no permission needed) show the roster, sometimes an active-speaker mark, and live captions with names. Yogurt reads them line by line without diarization; its users report brittle results on Windows (0 of 4,014 Teams lines named in 8 calls, reader hangs, empty Chrome trees), and Zoom on macOS stopped marking speakers in 7.2.5.
- A prototype of per-voice matching (`internal/callnames`) on flit's real diarized test meetings, with simulated app evidence, named far more than per-line naming when evidence was sparse (14-min meeting, marks for a quarter of it: 71% vs 21% of words right) at 0–5% wrong words.

## Options

1. **Per line, as Yogurt**: a line takes the name marked during most of it. Simple; but sparse evidence names few lines (6–21% of words with marks for a tenth to a quarter of the meeting, simulated).
2. **Per voice, lines corrected where clear** (prototype): evidence votes for each Sortformer voice over the whole meeting under strict thresholds; lines whose evidence is clear move to the right voice (repairs merges); captions matched by text; the roster names 1:1 calls and suggests the rest.
3. **Roster only**: suggestions and 1:1 calls; never names a voice in larger calls.
4. **Official APIs** (Graph transcripts, Zoom Apps/RTMS, Meet REST): accurate where available, but accounts, consent and cloud, against flit's principles; only as "open a transcript file" after the fact.
5. **Nothing from the app**: calendar attendees and typed names as suggestions.

## Decision (proposed)

**2**, built in steps: roster and calendar suggestions plus the 1:1 rule first (3 and 5, which can't name anyone wrongly in larger calls), then marks and captions for the apps where the probe (`scripts/uia-probe.ps1`) shows they exist on Windows. The reader runs in its own process during recordings of supported apps, reads every 1–2 s (roster every 30 s), never forces accessibility on, never reads other windows or typed text; names and captions never go into logs. A user's name always wins, and names say where they came from. Option 4 only as an optional transcript-file import later ([architecture/speaker-names.md](../architecture/speaker-names.md)).

To accept: probe results from real Teams, Zoom and Meet calls on Windows; the reader's cost during a call; the matching scored on the author's calls against hand-labelled speakers (bar: no wrong voice names, ≤ 3% wrong words).

## Consequences

- A UI Automation client in Go (`go-ole`, ~15 methods) and per-app parsers that will break as the apps change; they must fail to "no evidence", and redacted tree fixtures keep them tested.
- The matching itself is small, pure and tested; it also repairs diarization merges where the app gives line-level evidence, which flit can't do today.
- A small process runs during recordings of supported apps.
- flit reads another app's window; a setting and a clear line in Settings say so. Default on or off: for the author.

## Revisit when

- The probe shows the apps expose neither marks nor captions on Windows: then only steps 3/5 (roster, calendar) and voiceprints.
- A meeting app ships a local, documented interface for participants or the active speaker.
- A better diarizer (Nemotron, 8 speakers) or voiceprints make the app's evidence less needed.
