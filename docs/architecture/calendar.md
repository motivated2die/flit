# Calendar: meetings from a subscription link

Status: built (2026-11-21); this page was written first as the plan and updated where the build differs. Code: `internal/calendar` (parsing, recurrence, matching, the refresh service), `internal/app/calendar_windows.go` (pages, matching recordings).

## What it is for

flit knows *that* a meeting happens (an app uses the microphone) but not *which* one. A calendar knows the title, time, place and link. Joining the two names recordings correctly without typing, lets the user pick the right one when meetings overlap, and shows what is coming up.

It must need **no login**: the user pastes the calendar's **subscription link** (an ICS feed: Outlook/Exchange "Publish calendar", Google "Secret address in iCal format", Apple iCloud public calendars, `webcal://` links). Nothing else is sent anywhere; the link and everything parsed stay on this PC.

## What the user sees

- **Settings → Calendar**: a field for the link, **Test** (loads and parses now, says how many meetings it found in the next 7 days or what failed). The link is a secret (it opens the calendar); it is kept in `config.json` and never logged.
- **Calendar** in the sidebar: the next 7 days grouped by day ("Today", "Tomorrow", then the date), each meeting with its time, title and place; a place that is a link (Zoom, Teams, Meet, Webex, any https) becomes a **Join** button; a meeting that was recorded links to its recording. **Refresh** loads the feed now; a line says when it was last updated.
- **Home → Upcoming meetings**: the next two, if a calendar is set.
- **Recordings**: a recording that matches a meeting takes its title (unless the user named it) and shows the meeting's time and place. When meetings overlap and the match isn't clear, the recording's page asks **"Which meeting was this?"** with the candidates.
- **The notch** names the detected meeting by its calendar title when the match is clear.
- A warning only after **a full day** of failed refreshes ("The calendar couldn't be loaded since …"); single failures are silent.

## How it works

- **Refresh**: on start, on a new link, on Refresh, and two minutes before every full and half hour (xx:28, xx:58), so a meeting starting on the hour or half hour is known. One goroutine with a timer; requests time out after 30 s, read at most 10 MB, and send `If-None-Match`/`If-Modified-Since` so an unchanged feed costs one small request.
- **Parsing** (own code, RFC 5545 subset): unfolding, escapes, parameters; `VEVENT` with `DTSTART/DTEND/DURATION` (UTC, floating, `TZID`, all-day), `RRULE` (DAILY/WEEKLY/MONTHLY/YEARLY, INTERVAL, UNTIL, COUNT, BYDAY with ordinals, BYMONTHDAY, BYMONTH, WKST), `EXDATE`, `RECURRENCE-ID` overrides, `STATUS:CANCELLED`. Time zones: IANA names through Go's tz database (embedded), Windows names (Exchange's "W. Europe Standard Time") through a small table, else the feed's own `VTIMEZONE` rules, else local time.
- **Only what is needed is kept**: per event the UID, title, times, recurrence, location, and the meeting link found in location or description. Descriptions (often full e-mail threads, passwords, phone numbers) are read for the link and dropped. Events are expanded only for **yesterday to 7 days ahead**.
- **Cache**: the compact events, the validators and the failure state go to `calendar.json`, so the calendar shows at once after a restart and while offline. The cache is dropped when the link changes.
- **Bounded**: limits on feed size, event count and recurrence steps; a parse never panics into the app (recovered and reported as a failure); each refresh replaces the previous data, so nothing accumulates.

## Matching a recording to a meeting

At the start of a recording (and again at its end, with the real duration, unless the user chose), every meeting the recording started from 20 minutes before to 15 minutes after (meetings overrun) is scored:

- how close the recording started to the meeting's start, within an hour; a recording begun while the meeting runs counts as a good fit however late (0.6 of 1), one begun in the 15 minutes after its scheduled end as a fair one (0.45);
- how much the recording and the meeting overlap (at the end);
- the meeting app: a Zoom recording favours a meeting with a Zoom link and disfavours one with a Teams link;
- the window title the meeting app shows (Teams shows the meeting's name) against the meeting's title.

A clear winner is taken; candidates within a small margin of each other are offered as choices; all-day events and cancelled meetings never match. A meeting can also be recorded from the Calendar page or Home: **Record** appears beside Join while the pointer is on its row and starts recording at once, whenever the meeting is scheduled, with the meeting assigned as the user's choice (later matching leaves it alone). If the meeting's app is the one detected, its own audio is recorded; else everything the PC plays.

The matched meeting is stored in the session (`meta.json`: title, times, place, link), not a reference to the feed, so a recording keeps its meeting after the calendar moves on.

## Checked

- Tests: a synthetic feed shaped like an Exchange "Publish calendar" feed (`internal/calendar/testdata/exchange.ics`: Windows zone name with its `VTIMEZONE`, folded lines through URLs and umlauts, weekly and fortnightly series with `UNTIL` and multi-line `EXDATE`, escaped commas, Zoom links in location or description), a Google-style one (IANA zones, `RECURRENCE-ID` move and cancel, all-day, `DURATION`, `COUNT`, monthly last Friday), a feed-only zone, broken input, a series walked from 1900.
- The author's real feed (2026-11-21, not committed): parsed as expected; the next week held the Monday and the fortnightly Tuesday meeting, excluded dates skipped; two Zoom meetings at the same time in November 2025 are the case the choice question is for.
- Soak: 3000 full refreshes (fetch + parse + expand) of the test feed: heap 354 → 409 KB, goroutines unchanged, 0.4 ms each.
- Live: a portable instance against a local server: loaded 50 ms after start, the log shows only the host, `calendar.json` holds a hash of the link, not the link.

Not checked yet: Google and iCloud feeds from the real services, Outlook's "Customized Time Zone" feeds, a week of running.

## Not now

Several calendars (the config takes one link; the code is ready for a list), attendees, writing to calendars, logins (Microsoft Graph, Google API), reminders.
