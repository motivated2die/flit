# Speaker names from the call app

Question (2026-11-22): can flit fill in the names of the people on the meeting's side by itself, instead of the user naming "Speaker 1, 2, 3" on every recording? Constraints from the author: reliable (a wrong name is worse than none), light on resources, not intrusive, local only; use flit's own diarization as the strength it is. This note records what flit has, what the meeting apps officially offer, what their windows expose, what Yogurt learned in the field, a matching method built as a prototype and measured in simulation, and what is left to verify on real calls. Design: [architecture/speaker-names.md](../architecture/speaker-names.md); decision: [ADR 0012](../decisions/0012-speaker-names-from-the-call-app.md) (proposed).

## Summary

- **No meeting app offers a local, free, intended interface for who is in a call and who is talking.** The official routes are cloud APIs with an app registration and sign-in, often admin consent, and for speaker data usually a recording or transcript the platform made itself. The one local interface (Teams' "third-party app API" on `localhost:8124`) gave only mute/meeting state and was reportedly retired on 2026-06-30.
- **The apps' accessibility trees are the only local source**, and they are the operating system's intended way for assistive tools to read an app. But in the field they are unreliable on Windows. Yogurt's users report **0 of 4,014 remote lines named in 8 Teams desktop calls**, reader hangs, Chrome pages with empty trees, and Zoom on macOS dropping its active-speaker label in 7.2.5.
- **flit's diarization changes the arithmetic.** Sortformer already separates the voices, so the app only has to identify each voice, not each line. In simulation on flit's real diarized test meetings, naming voices from all their evidence and correcting lines where the evidence is clear ("combined") beats Yogurt's per-line rule in every condition, most when evidence is sparse: **71% vs 21% of words named correctly with marks for a quarter of the meeting, 45% vs 6% with a tenth** (14-minute AMI meeting), at 0–3% wrong names.
- **Live captions** with speaker names, which all three big apps can show, are a second kind of evidence. flit matches them by **text** against its own transcript, so their delay doesn't matter. Captions for 30% of the meeting named 69–88% of words correctly (Yogurt's per-line rule: 26–39%).
- **The roster** (who is in the call) names nothing by itself in calls of three or more, but it names 1:1 calls outright and gives the right names to suggest everywhere else.
- **Next**: run the probe script (`scripts/uia-probe.ps1`) on real Teams, Zoom and Meet calls on Windows to learn which of these signals exist there today.

## What flit has

- **Sortformer** numbers the meeting track's voices (up to 4) for the whole recording ([ADR 0006](../decisions/0006-speakers-with-sortformer.md)); `transcript.AssignSpeakers` (`internal/transcript/speakers.go`) gives each word a voice, smooths flickers and fragments, and renumbers by first appearance. Measured 78–89% of words on the right person ([diarization.md](diarization.md#speakers-measured-2026-11-21); reproduced on 2026-11-22 with the same numbers). Its errors: one person split into two voices; two people merged into one voice, wholly (ami-5min) or partly (ami-14min: a voice that is three quarters one person and a quarter another).
- **Names** per recording in `speakers.json` (`transcript.Names`), typed by the user.
- **Detection** knows the provider and the process tree recording (`internal/detect`).
- **The calendar** has no attendees yet (`internal/calendar/events.go`).

## Official interfaces (researched 2026-11-22)

| App | Interface | Who's in the call | Who's talking | Local? Free? | What it takes |
|---|---|---|---|---|---|
| Teams desktop | "Third-party app API", WebSocket on `localhost:8124` (Stream Deck) | no | no (only in-meeting, muted, camera, hand…) | local, free | **Retired 2026-06-30** (message center MC1266901, per [a Stream Deck plugin](https://github.com/kosmonautica/StreamDeckMSTeams_Udo) and [MuteDeck](https://mutedeck.com/microsoft-teams-create-api-token); not confirmed on a Microsoft page). Those tools now read Teams' UI instead. |
| Teams | Microsoft Graph: meeting transcripts | after the meeting | **yes, in the transcript** (VTT with speaker names) | cloud | An Entra app, `OnlineMeetingTranscript.Read.All`, tenant admin switches for Graph transcript access and for speaker attribution (both **off by default**), work accounts only, and the meeting transcribed by Teams ([Microsoft Learn](https://learn.microsoft.com/en-us/microsoftteams/meeting-transcript-api-access), [Q&A on SpeakerAttributionNotAllowed](https://learn.microsoft.com/en-ie/answers/questions/6005298/graph-transcript-content-returns-speakerattributio)) |
| Teams | TeamsJS meeting apps (side panel) | yes (`getParticipant`) | **no** active-speaker API found ([meeting apps APIs](https://learn.microsoft.com/en-sg/microsoftteams/platform/apps-in-teams-meetings/meeting-apps-apis), [Q&A](https://learn.microsoft.com/en-us/answers/questions/438372/to-get-active-speaker-in-teams-online-meeting)) | inside Teams | An app installed into the meeting by the tenant |
| Zoom | Zoom Apps SDK (app inside the Zoom client) | `getMeetingParticipants`: **host only** ([dev forum](https://devforum.zoom.us/t/getmeetingparticipants-gives-reason-require-meeting-owner-role/76906)) | `onActiveSpeakerChange` ([SDK reference](https://appssdk.zoom.us/classes/ZoomSdk.ZoomSdk.html)) | runs in Zoom; would need a bridge to flit | A Marketplace app, OAuth, per-user consent, Zoom Apps allowed by the account |
| Zoom | Meeting SDK (a bot that joins) | yes | active-speaker callback (unreliable per [forum reports](https://devforum.zoom.us/t/zoom-windows-meeting-sdk-active-speaker-user-change-event-not-triggering/132134)) | joins as a visible participant | App review; since 2026-03-02 authorization (ZAK/OBF/RTMS) for meetings outside the account ([Zoom docs](https://developers.zoom.us/docs/meeting-sdk/windows/)) |
| Zoom | RTMS (realtime media streams) | yes | **yes**: active-speaker events, per-participant audio ([RTMS docs](https://developers.zoom.us/docs/rtms/meetings/work-with-streams/)) | cloud, to a server | A Zoom app and account enablement; media goes to the app's server |
| Google Meet | Meet REST API | yes (participants, sessions with times) ([guide](https://developers.google.com/workspace/meet/api/guides/participants)) | no | cloud | Google OAuth; Workspace |
| Google Meet | Meet Media API | yes | via audio levels per stream ([third-party explainer](https://www.recall.ai/blog/what-is-the-google-meet-media-api)) | joins as a client | Developer preview enrolment |
| Webex | Meetings SDKs | yes | active-speaker events need a paid host account and captions on ([Recall.ai's notes](https://docs.recall.ai/docs/webex)) | joins as a client | Webex integration |
| Slack huddles | none found | — | — | — | not searched in depth |

**Conclusion.** Every route that knows who talks needs an account-level integration, a bot in the call, or the platform's own recording or transcript. All of them break flit's "local only, no accounts" principle, and most aren't available to an ordinary employee without IT. None is worth building as a default. One is worth keeping in mind as an option: **importing a transcript the platform already made** (Teams, Zoom cloud recording and Meet can each give the user a file with speaker names). flit's caption matcher works on such a file unchanged (below).

## The accessibility trees

UI Automation (Windows) is how screen readers read apps; it needs no permission, and every app that wants to be accessible exposes its window through it. Yogurt reads it every 400 ms ([references/yogurt.md](../references/yogurt.md#names-from-the-call-app-appsrcmaincallnames-appnativeyogurt-systemsrcwincallsrs)). What each app shows, per Yogurt's parsers:

| App | Roster | Who is talking | Me | Captions |
|---|---|---|---|---|
| Zoom | tiles and participants panel rows | tile ends ", Active speaker" (3+ people); "Talking: Name" | "(me)" | caption rows with names; Yogurt PR #97 reads them when no tile is marked |
| Teams (WebView2) | People panel rows "Name, Role, Muted" while the panel is open | only a visual ring; some builds label "Name, speaking" | "(You)" | not read by Yogurt; Teams' captions show the speaker's name (whether in the tree on Windows: to verify) |
| Meet (Chromium) | tile names | an obfuscated CSS class on the talking tile (`v5h6Xc`, Oct 2026) | "You" | not read by Yogurt; show names |

**Field reports from Yogurt's issue tracker** (read 2026-11-22):

- [#83](https://github.com/FrigadeHQ/yogurt/issues/83), Windows 11, Teams desktop 26288.2004: after two fixes, still **0 of 4,014 remote lines named across 8 calls** (0.3.48, 2026-11-19). The log shows `no-call-window` at the start, and in one call the reader "went quiet for 6 s; starting it again" every 6 seconds for minutes: UI Automation reads of Teams hung. Teams in Chrome: the page's `RootWebArea` had no children (Chrome builds its web tree only when it thinks an assistive tool is on); the maintainer suggested `--force-renderer-accessibility`.
- [#112](https://github.com/FrigadeHQ/yogurt/issues/112): no names when the Meet window is in the background (open).
- [#78](https://github.com/FrigadeHQ/yogurt/issues/78) / PR [#86](https://github.com/FrigadeHQ/yogurt/pull/86): users ask for voice separation when the app shows no names; the maintainer's spike (sherpa-onnx, pyannote segmentation + ERes2Net) reports "about 25% speaker error … about 3 min of processing after a 20 min meeting, on one CPU core … once we have ~20 s of someone's voice, longer turns get the right name 94–100% of the time". A contributor notes **Zoom 7.2.5 on macOS no longer marks an active speaker at all** (0 in 637 readings of a 7-person call), and that "a meeting room joins as one participant, so everyone in it speaks under one name".
- PR [#97](https://github.com/FrigadeHQ/yogurt/pull/97): Zoom's live captions as the fallback: "0 of 29 remote lines named before, 6 of 29 after", low "by design" because a line needs one name for over half its length.

So, in late 2026, the apps' trees are useful but brittle: they change with app versions, may be empty or slow, and give sparse evidence. Yogurt, built on per-line naming without diarization, is moving towards what flit has.

**Measured on this PC** (2026-11-22, `scripts/uia-probe.ps1`, the managed UI Automation client in Windows PowerShell 5.1, one cached subtree request): an Edge window with an ordinary web page came back with 2,375 elements, depth 37, in **68–83 ms per read**, its web documents populated. No meeting app was running, so this is only the cost of a Chromium tree of that size, and shows that this client gets Chromium's web tree on this PC (unlike Yogurt's Chrome report; why it differs is unknown).

## Matching: name voices, correct lines

Built as a prototype in `internal/callnames` (pure Go, tests in `callnames_test.go`; not wired into the app):

1. **Evidence** (`Marks`): readings where exactly one person other than the user is marked talking, each until the next reading (at most 2 s), moved back by the app's lag (0.75 s). The user's own name never counts.
2. **Captions** (`Captions`): each caption line's words are found in the meeting track's words near the time it was seen (30 s before, 5 s after) by a local alignment (Smith-Waterman; a word matches exactly or one letter off from five letters up), at least 3 words and 40% of the caption. Timing comes from flit's own words, so the captions' delay and their revisions don't matter.
3. **Votes per voice** (`Assign`): every word of a voice (the first second of each run of that voice left out, where the app may still show the previous person) counts towards the names whose evidence covers it. A voice takes a name with ≥ 5 s of evidence, ≥ 60% of the voice's evidenced speech **and of its speech during the readings while the app was marking people** (so speech the app attributes to nobody counts against a name), and twice the runner-up. Two strong names: flagged, not named. Two voices may take the same name (a split). Only the user and one other person in the roster throughout: the whole meeting track takes that name.
4. **Lines where clear** (`Refine`): a line (segment) whose time one name covers for more than half moves to the voice carrying that name, or to a new voice for it. This repairs merges, which diarization can't, and names clear lines of voices with too little evidence overall.

**Simulation** (`go run ./cmd/flitdev callnames-sim -meeting <dir> -segments <file>`, 20 random runs per sparse condition): the app's evidence is made from each test meeting's reference turns (the app marks whoever talked 1 s ago, keeps the last speaker during silence, readings every 0.4 s), and applied to **flit's real diarized transcript** of that meeting (`flit transcribe --models models --model parakeet-ultra --speakers --meeting <dir> --save <file>`; CPU, 2026-11-22). Words are scored where exactly one meeting-track person speaks in the reference: right name, wrong name, or none. "Per line" is Yogurt's rule (`nameFor`: a line takes the name marked over more than half of it); "Combined" is `Assign` + `Refine`.

ami-IS1009a (14 min, English, 3 people on the meeting track, 4 voices; 576 scored words; best possible by naming whole voices: 89% right, 11% wrong because of Sortformer's partial merge):

| Condition | Per line (Yogurt): right / wrong / none | Per voice only | **Combined** |
|---|---|---|---|
| Marks all along, 1 s lag | 81% / 2% / 17% | 85% / 6% / 8% | **90% / 3% / 7%** |
| Marks, 2 s lag, 10% of readings wrong | 75% / 2% / 23% | 76% / 6% / 19% | **87% / 4% / 9%** |
| Marks for 25% of the meeting | 21% / 1% / 79% | 68% / 3% / 29% | **71% / 3% / 26%** |
| Marks for 10% of the meeting | 6% / 0% / 94% | 44% / 1% / 55% | **45% / 2% / 53%** |
| Marks, one person never marked | 60% / 2% / 37% | 64% / 1% / 35% | **69% / 3% / 29%** |
| No marks, captions for 30% | 26% / 0% / 74% | 64% / 5% / 31% | **69% / 4% / 27%** |
| No marks, captions all along | 90% / 0% / 10% | 87% / 13% / 0% | **95% / 5% / 0%** |
| Roster only (Teams, panel open) | 0% / 0% / 100% | 0% / 0% / 100% | 0% / 0% / 100% |

ami-IS1009a-5min (the first 5 minutes; Sortformer merged two people into one voice; 179 scored words; best possible per voice: 82% / 18% / 0%):

| Condition | Per line (Yogurt) | Per voice only | **Combined** |
|---|---|---|---|
| Marks all along, 1 s lag | 90% / 4% / 6% | 59% / 1% / 41% | **93% / 4% / 3%** |
| Marks, 2 s lag, 10% wrong | 71% / 3% / 25% | 23% / 0% / 76% | **80% / 3% / 17%** |
| Marks for 25% (and 10%: one minute either way) | 24% / 1% / 75% | 47% / 0% / 53% | **58% / 1% / 41%** |
| Marks, one person never marked | 73% / 4% / 23% | 59% / 1% / 41% | **76% / 4% / 20%** |
| No marks, captions for 30% | 39% / 0% / 61% | 56% / 1% / 44% | **71% / 1% / 29%** |
| No marks, captions all along | 91% / 1% / 8% | 59% / 1% / 41% | **94% / 1% / 5%** |

de-synth-4spk (5 min, German, synthetic, long clean turns; Sortformer split one person into two voices; 488 scored words): every method 100% right with full marks or captions. Sparse: marks for 25% → per line 34%, **combined 88%**; 10% → 18% vs **65%**; captions for 30% → 35% vs **88%**; one person never marked: 62% both. **No wrong names in any condition.** The split was repaired by both voices taking the same name.

What the simulation shows, and what it doesn't:

- Diarization turns sparse evidence into coverage: a few dozen seconds of a voice's speech marked with a name name all of it. This is the main gain over per-line naming, and it is largest exactly where the field reports put Windows today (marks only now and then, captions only when someone turns them on).
- Per-voice naming alone inherits diarization's merges (6% wrong on ami-14min); correcting lines where the evidence is clear brings wrong names back to per-line levels (2–4%) while keeping the coverage.
- Two earlier versions of the rules failed in instructive ways and were changed: comparing a name only with the voice's *evidenced* speech let a merged voice take one person's name when the other was never marked (20% wrong words on ami-5min; fixed by counting all speech during the readings); and per-voice naming alone flagged the merged voice but left it unnamed (fixed by `Refine`).
- **Not shown:** real apps' timing, mistakes and gaps (the simulated app is too regular); captions differing from speech more than AMI's reference text differs from flit's transcript; more than 4 people; German calls. One case is unexplained: with captions all along, one ami-14min voice took 13 s of FIO089's caption text although the reference says FIO084 spoke then (guess: FIO089's voice leaking into another headset on AMI's summed meeting track). The thresholds are starting values, not tuned on real calls.

## Captions and transcripts as evidence

- **Live captions** are a personal setting in Teams and Meet (any participant can turn them on, only they see them); in Zoom the host must allow them (Yogurt #97). They name who spoke and carry the words, which flit matches by text. That makes them the strongest evidence where they exist, and Teams, which never labels its active speaker, does label its captions. Whether Teams' and Meet's caption areas are in their UI Automation trees on Windows is the first thing to probe.
- **A platform transcript after the meeting** (Teams `.vtt`/`.docx` download, Zoom cloud recording `.vtt`, Meet's transcript document): the same matcher, with the cue times shifted to flit's time line (offset found from the first matched lines). The user opens a file; no API, no account in flit. Only useful where the organisation already records or transcribes meetings in the platform.

## Cost and intrusiveness

- **Reading less often than Yogurt.** Speaker turns last seconds and votes need only some of them, so a read every 1–2 s is enough for marks (simulation: readings every 0.4 s; 1–2 s untested). The roster changes rarely: read the panel's list at most every 30 s. Captions: as often as marks, only while a caption area exists. Measured cost on this PC: ~70 ms of the reader's time per read of a 2,400-element Chromium window (≈ 3–7% of one core at 1–2 s; the app's own cost unmeasured).
- **Back off when the app is slow**: a read that takes long means reading less often (Yogurt: `max(interval, 2 × read time)`); a read that hangs means a separate process, so the tray and the recording never wait on it.
- **Never force accessibility on.** No relaunching Chrome with `--force-renderer-accessibility`, nothing that changes how the user's apps run; if a tree is empty, flit gets no names from it.
- **Reading only the call's window**: the process tree being recorded, windows with call controls; never chat, calendar or other windows; never edit fields' contents.
- **Privacy**: names and caption text are personal data like the transcript. Never in logs (counts only; a summary with texts replaced by their lengths, as the probe does by default). Readings are kept with the session as long as its audio, deleted with it.

## Other name sources

- **Calendar attendees** (`ATTENDEE;CN=`): suggestions, and vocabulary before the meeting.
- **Names typed before**, people named in the same recurring series ([speaker-identity.md](speaker-identity.md#building-blocks-found-elsewhere)).
- **Voiceprints** ([speaker-identity.md](speaker-identity.md)): call evidence could label them; enrol only names the user accepted.
- **The LLM reading the transcript** ("Thanks, Anna"): weak; Yogurt removed its LLM voice naming.

## Experiment plan

1. **Probe real calls on Windows** with `scripts/uia-probe.ps1` (redacted by default): Teams desktop with the People panel open and closed and captions on and off; Zoom with 2 and 4 people, captions on; Meet in Edge and Chrome. Per app: are call controls found, any "speaking" labels, a roster list, a captions area; elements and milliseconds per read; any empty web documents. `-Tree` shows the structure for writing parsers.
2. **Reader cost** during a call: the probe at `-Every 1` with Task Manager on the call app (CPU before and during).
3. **Record evidence on real calls** (once a reader exists): readings and captions stored with the session, then `Assign` + `Refine` scored against speakers labelled by hand for 10–15 minutes of two or three meetings (the same labels the diarization measurement needs, [diarization.md](diarization.md#next-steps)).
4. **Tune** the thresholds on those, and the reading interval (0.4 s vs 1 s vs 2 s) by replaying recorded readings at lower rates.
5. **Bar for "on by default"** (guess): no wrong voice names on the author's calls, wrong words ≤ 3%, and a clear gain over the roster-only suggestions.
