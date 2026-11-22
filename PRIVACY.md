# Privacy

flit records meetings, so what it does with them matters. In short: **everything stays on your PC.** There is no account, no cloud transcription, no analytics and no telemetry.

## What stays on your PC

Recordings (your microphone and the meeting's audio, as separate tracks), transcripts, speaker names you give, your settings and your calendar link are kept in `%LOCALAPPDATA%\flit`. Transcription runs on your processor with models stored in the same folder. Nothing of it is uploaded anywhere.

The log (`debug.log` in that folder) never contains transcript text, meeting titles or the calendar link, and leaves out your Windows user name, your PC's name and e-mail addresses.

## What flit connects to

flit makes outgoing HTTPS requests only, and opens no port others could connect to:

| Where | When | What is sent |
|---|---|---|
| Hugging Face (`huggingface.co` and its download hosts) | Downloading a speech, voice-detection or speaker model: at setup, or when you change the language or model | An ordinary file download of a pinned model file. Nothing about you or your meetings |
| GitHub (`api.github.com`, `github.com` and its download hosts) | Checking for updates, shortly after start and twice a day (Settings → App → *Check for updates*, can be turned off), and downloading an update you chose | The request for the latest release, with the header `User-Agent: flit/<version>` |
| The address of your calendar link | Only if you add one (Settings → Calendar), to read your upcoming meetings: every half hour, and when you press Refresh or Test | An ordinary request for the calendar feed |

Like any website, these services see your IP address when flit connects to them.

## Recording other people

Whether you may record a conversation, and whether you need everyone's consent, depends on where you and the others are. flit doesn't decide that for you; please let the people in your meetings know when you record them.

## Removing everything

Settings → **Uninstall…** removes flit and, if you choose, all recordings, transcripts, models and settings. Windows' Settings → Apps → Installed apps → flit removes the program and keeps the data in `%LOCALAPPDATA%\flit`, which you can delete yourself.
