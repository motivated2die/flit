# Speaker names (proposal)

Status: **proposed** (2026-11-22). The matching is built as a tested prototype (`internal/callnames`) and measured in simulation; the reader, storage and UI are not built. Research and numbers: [research/call-app-names.md](../research/call-app-names.md). Decision: [ADR 0012](../decisions/0012-speaker-names-from-the-call-app.md) (proposed). Builds on [ADR 0006](../decisions/0006-speakers-with-sortformer.md) (Sortformer voices, renamed by the user) and [research/speaker-identity.md](../research/speaker-identity.md) (voices across recordings).

The goal: after a Teams, Zoom or Meet call, the meeting track's voices carry the right names where the evidence is clear, the rest are "Speaker N" with the right names offered first, and nothing is named on a guess. Sortformer separates the voices; the call app only has to say who each voice is, once.

## Sources, strongest first

| Source | Gives | When | Used as |
|---|---|---|---|
| **The user's names** | names for this recording | after | final; never overwritten |
| **Live captions** in the call window (Teams, Meet, Zoom when allowed) | speaker name + words | during | evidence, matched by **text** against flit's words (`callnames.Captions`) |
| **Active-speaker marks** in the call window (Zoom tiles; some Teams builds) | who talks now | during | evidence, matched by time (`callnames.Marks`) |
| **The roster** (participants panel, tiles) | who is in the call, which one is the user | during | names a 1:1 call; suggestions otherwise |
| A platform transcript the user opens (Teams `.vtt`, Zoom `.vtt`, Meet document) | speaker name + words | after | the same caption matcher, offset to flit's time line |
| **Calendar attendees** (`ATTENDEE;CN=`) | invited names | before | suggestions; vocabulary |
| Earlier recordings of the series, names typed before | candidates | after | ranking |
| Voiceprints ([speaker-identity.md](../research/speaker-identity.md)) | a likely name per voice | after | suggestion, later |

No meeting app has a local interface for any of this; the cloud APIs need accounts, admin consent or a bot ([research](../research/call-app-names.md#official-interfaces-researched-2026-11-22)). The accessibility tree (UI Automation) is the only local source, and it is brittle, so everything here treats it as **opportunistic evidence**: the more there is, the more gets named; none means today's result.

## Parts

```
 recording starts (detect: provider, process tree)
   │
   ├─▶ flit.exe --call-watch <provider> <pid…>   child process, below-normal priority, small CPU cap
   │      call windows of those PIDs only (title rules + call controls)
   │      marks + captions: one cached UIA subtree read every 1–2 s, slower when reads are slow
   │      roster: at most every 30 s
   │      stdout: one JSON line per reading {t_qpc, speaking[], participants[], me, captions[]}
   │
   └─▶ tray: keeps the readings for the session (memory, then callnames.json)

 after transcription and diarization
   └─▶ callnames: Marks + Captions → evidence → Assign (voices) → Refine (lines)
         → speakers.json names with their source, flagged voices, suggestions
```

| Package | Responsibility | State |
|---|---|---|
| `internal/callnames` | **Matching**: `Marks` (readings → evidence, lag-corrected, the user excluded), `Captions` (caption lines → evidence by local alignment with the transcript's words), `Assign` (votes per voice; strict thresholds; splits merged by same name; merges flagged; the two-person rule; suggestions), `Refine` (lines whose evidence is clear move to the right voice). Pure Go. | **prototype, tested**; simulated with `flitdev callnames-sim` |
| `internal/callnames` (next) | **Parsers** per app turning a tree into `Reading`s and `Caption`s, every rule failing towards empty; **redaction** for the log; tests on stored redacted trees from real calls | not built; needs the probe's results |
| `internal/uia` | A minimal UI Automation client over `go-ole` (already a dependency): `CUIAutomation8`, connection/transaction timeouts (`IUIAutomation2`), one cache request (control type, name, help text, automation id, class name; subtree; raw view; no live elements), `ElementFromHandleBuildCache`, cached children. ~15 vtable methods. Never asks for edit fields' values. | not built |
| `cmd/flit` role `--call-watch` | The reader loop in its own process, so a hang in the call app's accessibility code can't touch the tray or the recording. Exits when stdin closes. | not built |
| `internal/calendar` | Keep attendees' display names (`CN`), not their addresses, on `Meeting`. | not built |
| `internal/transcript` | `Names` gains where each name came from (`user`, `marks`, `captions`, `roster`). | not built |

## Rules that keep it reliable

From the prototype (`DefaultOptions`, starting values to tune on real calls):

- Evidence counts only where **exactly one** person other than the user is marked; a reading holds at most 2 s; marks are moved back by the app's lag (0.75 s); the **first second of each run of a voice is ignored** (the app may still show the previous person).
- A voice takes a name with **≥ 5 s** of evidence, **≥ 60%** of both its evidenced speech and its speech while the app was marking anyone, and **2×** the runner-up. A runner-up with ≥ 5 s and ≥ 25% flags the voice as probably two people.
- A line moves (`Refine`) only when one name covers **more than half** of it.
- Captions count with **≥ 3** matched words and **≥ 40%** of the caption.
- A 1:1 call (only the user and one other person in every roster reading, at least 10 readings) names the whole meeting track.
- A user's name always wins; re-running diarization re-runs the matching on the stored readings.

In simulation on flit's diarized test meetings these rules gave 0–5% wrong words in every condition and named far more than per-line naming when evidence was sparse ([numbers](../research/call-app-names.md#matching-name-voices-correct-lines)).

## Not intrusive, not heavy

- Reads only the window of the call being recorded; never chat, calendar or other windows; never typed text.
- Never changes how the user's apps run: no forcing Chromium's accessibility on, no relaunch flags, nothing injected. An empty tree means no names.
- Reads every 1–2 s, not 400 ms (Yogurt), and the roster every 30 s; a slow read slows the reader down; a hung read is the child's problem. Measured: ~70 ms per read of a 2,400-element Chromium window on the author's PC.
- Runs only during recordings of a supported app, behind a setting.

## Files

| Path | Content |
|---|---|
| `sessions\…\callnames.json` | the readings (time, speaking, participants, me) and caption lines; kept as long as the audio, deleted with the recording |
| `sessions\…\speakers.json` | names as today, plus source per name |

Names and caption text are personal data like the transcript: never in `debug.log`. The log gets counts ("call names: zoom, 1,210 readings, 38 caption lines, 3 of 4 voices named, 1 flagged, 6 lines moved") and once per meeting a structure summary with texts replaced by their lengths (as `scripts/uia-probe.ps1` prints by default).

## UI

- The recording's speakers list: a name from the call says so ("from Zoom", "from captions"); one click renames as before. A flagged voice says "may be two people".
- Settings → Speakers: "Names from the meeting app" (default to decide, [open-questions.md](../open-questions.md)), with a line on what is read and that nothing leaves the PC. A hint that turning on live captions in the call gives the best names.
- Later: "Name speakers from a transcript file…" on the recording page (Teams/Zoom `.vtt`).

## Steps

1. **Probe** real calls with `scripts/uia-probe.ps1` (what Teams, Zoom, Meet expose on Windows today, and at what cost). Decides which parsers are worth writing.
2. **Roster and attendees first**: suggestions and the 1:1 rule need only the roster (read rarely) and the calendar; they can't name anyone wrongly in calls of three or more.
3. **Reader + parsers** for what the probe found (captions likely first for Teams, tiles for Zoom), readings stored with the session.
4. **Measure on real calls** against hand-labelled speakers; tune; then decide the default.
