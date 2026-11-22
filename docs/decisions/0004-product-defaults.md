# 0004: Product defaults for the first versions

- **Status:** accepted
- **Date:** 2026-11-21

## Decisions (from the author's answers to the first open questions)

1. **Recordings are kept** (both tracks), so real meetings become benchmark material. A later setting offers "keep only the transcript".
2. **Everything the local mic hears is transcribed.** No detection of mute in the meeting app.
3. **One meeting language setting** (German or English), chosen ahead. Automatic language switching (a new feature in Handy) is explored later.
4. **Which apps trigger the meeting prompt is a setting**: Teams, Zoom, Webex, Slack, Google Meet (browsers), and Discord, each on or off.
5. Autostart on by default (see [0003](0003-distribution-without-admin.md)).

## Consequences

- Session folders grow by ~115 MB per hour per track (16 kHz, 16-bit WAV). A size display in the session list and FLAC compression become worthwhile soon.
- The provider table in `internal/detect` is data with per-provider switches in `config.json`.
