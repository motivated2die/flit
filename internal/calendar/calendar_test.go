package calendar

import (
	"os"
	"strings"
	"testing"
	"time"
)

var berlin, _ = time.LoadLocation("Europe/Berlin")

func load(t *testing.T, name string) *Feed {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	feed, err := Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	return feed
}

func at(y int, m time.Month, d, h, mi int) time.Time { return time.Date(y, m, d, h, mi, 0, 0, berlin) }

// The Exchange feed (shaped like a real one): on Saturday 21 Nov 2026 the
// next seven days hold the weekly Monday meeting and the fortnightly
// Tuesday one; the single events of 2025 and the series that ended don't
// show.
func TestExchangeNextWeek(t *testing.T) {
	feed := load(t, "exchange.ics")
	if feed.Name != "Kalender" || feed.Events() != 5 {
		t.Fatalf("name %q, %d events", feed.Name, feed.Events())
	}
	now := at(2026, 11, 21, 12, 0)
	got := feed.Meetings(now, now.AddDate(0, 0, 7))
	if len(got) != 2 {
		t.Fatalf("%d meetings: %+v", len(got), got)
	}
	mon, tue := got[0], got[1]
	if mon.Title != "Wöchentliche Abstimmung" || !mon.Start.Equal(at(2026, 11, 23, 11, 30)) || !mon.End.Equal(at(2026, 11, 23, 12, 0)) {
		t.Errorf("monday: %+v", mon)
	}
	// "Zoom" is a place in words; the join link comes from the description
	// (folded across lines), not the privacy notice's link.
	if mon.Location != "Zoom" || mon.Link != "https://example.zoom.us/j/11122233344?pwd=AAAABBBBCCCC" || mon.Provider != "zoom" {
		t.Errorf("monday place: %q %q %q", mon.Location, mon.Link, mon.Provider)
	}
	if tue.Title != "AG Projekt" || !tue.Start.Equal(at(2026, 11, 24, 14, 0)) {
		t.Errorf("tuesday: %+v", tue)
	}
	if tue.Location != "" || tue.Link != "https://example.zoom-x.de/j/68400000228?pwd=a7Ke.1" || tue.Provider != "zoom" {
		t.Errorf("tuesday place: %q %q %q", tue.Location, tue.Link, tue.Provider)
	}
}

func TestExchangeExdatesAndDST(t *testing.T) {
	feed := load(t, "exchange.ics")
	// Weekly Mondays in Oct/Nov 2026: the 26th and the 2nd are excluded,
	// and the clock stays at 11:30 across the end of summer time (25 Oct).
	got := feed.Meetings(at(2026, 10, 18, 0, 0), at(2026, 11, 10, 0, 0))
	var mondays []string
	for _, m := range got {
		if m.Title == "Wöchentliche Abstimmung" {
			mondays = append(mondays, m.Start.In(berlin).Format("Jan 2 15:04"))
		}
	}
	if strings.Join(mondays, ", ") != "Oct 19 11:30, Nov 9 11:30" {
		t.Fatalf("mondays %v", mondays)
	}
	// The fortnightly Tuesday series skips its excluded dates (15 Sep).
	got = feed.Meetings(at(2026, 9, 1, 0, 0), at(2026, 10, 1, 0, 0))
	var tuesdays []string
	for _, m := range got {
		if m.Title == "AG Projekt" {
			tuesdays = append(tuesdays, m.Start.In(berlin).Format("Jan 2"))
		}
	}
	if strings.Join(tuesdays, ", ") != "Sep 29" {
		t.Fatalf("tuesdays %v", tuesdays)
	}
	// The single 2025 events: an escaped comma, a folded link as place.
	got = feed.Meetings(at(2025, 11, 24, 0, 0), at(2025, 11, 25, 0, 0))
	var titles []string
	for _, m := range got {
		titles = append(titles, m.Title)
	}
	if strings.Join(titles, "|") != "Wöchentliche Abstimmung|Anna und Ben zum Projekt|Vergleich A vs. B" {
		t.Fatalf("titles %q", titles)
	}
	if got[1].Link != "https://example.zoom-x.de/j/5550001111?pwd=RFZrSVBZV3RhbGcrM1hZL08wcDRNZz09" || got[2].Location != "Raum kurzfristige Absprachen" || got[2].Link != "" {
		t.Fatalf("places %+v / %+v", got[1], got[2])
	}
}

const googleFeed = `BEGIN:VCALENDAR
X-WR-CALNAME:Work
BEGIN:VEVENT
UID:standup@google
SUMMARY:Standup
DTSTART;TZID=Europe/Berlin:20261102T093000
DTEND;TZID=Europe/Berlin:20261102T094500
RRULE:FREQ=DAILY;BYDAY=MO,TU,WE,TH,FR
LOCATION:https://meet.google.com/abc-defg-hij
END:VEVENT
BEGIN:VEVENT
UID:standup@google
RECURRENCE-ID;TZID=Europe/Berlin:20261124T093000
SUMMARY:Standup (moved)
DTSTART;TZID=Europe/Berlin:20261124T110000
DTEND;TZID=Europe/Berlin:20261124T111500
END:VEVENT
BEGIN:VEVENT
UID:standup@google
RECURRENCE-ID;TZID=Europe/Berlin:20261125T093000
STATUS:CANCELLED
DTSTART;TZID=Europe/Berlin:20261125T093000
SUMMARY:Standup
END:VEVENT
BEGIN:VEVENT
UID:holiday@google
SUMMARY:Holiday
DTSTART;VALUE=DATE:20261126
DTEND;VALUE=DATE:20261127
TRANSP:TRANSPARENT
END:VEVENT
BEGIN:VEVENT
UID:review@google
SUMMARY:Review
DTSTART:20261127T130000Z
DURATION:PT1H30M
LOCATION:Teams: https://teams.microsoft.com/l/meetup-join/19%3ameeting_x
END:VEVENT
BEGIN:VEVENT
UID:monthly@google
SUMMARY:Retro
DTSTART;TZID=Europe/Berlin:20260901T150000
DTEND;TZID=Europe/Berlin:20260901T160000
RRULE:FREQ=MONTHLY;BYDAY=-1FR;COUNT=4
END:VEVENT
END:VCALENDAR
`

func TestGoogleStyle(t *testing.T) {
	feed, err := Parse(strings.NewReader(googleFeed))
	if err != nil {
		t.Fatal(err)
	}
	got := feed.Meetings(at(2026, 11, 23, 0, 0), at(2026, 11, 28, 0, 0))
	var lines []string
	for _, m := range got {
		// An all-day date floats: midnight wherever this PC is (CI runs in
		// UTC), so it is shown in local time; the rest in the feed's zone.
		start := m.Start.In(berlin)
		if m.AllDay {
			start = m.Start.Local()
		}
		lines = append(lines, start.Format("Mon 15:04")+" "+m.Title)
	}
	want := []string{
		"Mon 09:30 Standup", "Tue 11:00 Standup (moved)", "Thu 00:00 Holiday", "Thu 09:30 Standup",
		"Fri 09:30 Standup", "Fri 14:00 Review", "Fri 15:00 Retro",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	for _, m := range got {
		switch m.Title {
		case "Holiday":
			if !m.AllDay || !m.Free {
				t.Errorf("holiday %+v", m)
			}
		case "Review":
			if m.End.Sub(m.Start) != 90*time.Minute || m.Provider != "teams" || m.Location != "Teams" {
				t.Errorf("review %+v", m)
			}
		}
	}
	if got[0].Provider != "meet" || got[0].Location != "" {
		t.Errorf("standup %+v", got[0])
	}
	// COUNT=4 from September: Sep 25, Oct 30, Nov 27, Dec 25; none after.
	if m := feed.Meetings(at(2026, 12, 26, 0, 0), at(2027, 3, 1, 0, 0)); len(m) != 0 && m[0].Title == "Retro" {
		t.Fatalf("retro after COUNT: %+v", m)
	}
}

func TestNotACalendar(t *testing.T) {
	if _, err := Parse(strings.NewReader("<html><body>Sign in</body></html>")); err != ErrNotCalendar {
		t.Fatalf("err %v", err)
	}
	// Broken lines and events don't stop the rest.
	feed, err := Parse(strings.NewReader("BEGIN:VCALENDAR\nGARBAGE\nBEGIN:VEVENT\nSUMMARY:no start\nEND:VEVENT\nBEGIN:VEVENT\nUID:x\nSUMMARY:ok\nDTSTART:20261123T100000Z\nEND:VEVENT\nEND:VCALENDAR\n"))
	if err != nil || feed.Events() != 1 {
		t.Fatalf("%v, %d events", err, feed.Events())
	}
}

func TestRunawaySeriesIsBounded(t *testing.T) {
	// A secondly-ish series from long ago, far beyond the walk cap.
	feed, err := Parse(strings.NewReader("BEGIN:VCALENDAR\nBEGIN:VEVENT\nUID:x\nSUMMARY:daily\nDTSTART:19000101T100000Z\nDTEND:19000101T110000Z\nRRULE:FREQ=DAILY\nEND:VEVENT\nEND:VCALENDAR\n"))
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	feed.Meetings(time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 28, 0, 0, 0, 0, time.UTC))
	if time.Since(start) > time.Second {
		t.Fatal("expansion too slow")
	}
}

func TestVTimezoneRules(t *testing.T) {
	// A zone only the feed defines: Berlin's rules under another name.
	feed, err := Parse(strings.NewReader(`BEGIN:VCALENDAR
BEGIN:VTIMEZONE
TZID:Customized Time Zone
BEGIN:STANDARD
DTSTART:16010101T030000
TZOFFSETFROM:+0200
TZOFFSETTO:+0100
RRULE:FREQ=YEARLY;INTERVAL=1;BYDAY=-1SU;BYMONTH=10
END:STANDARD
BEGIN:DAYLIGHT
DTSTART:16010101T020000
TZOFFSETFROM:+0100
TZOFFSETTO:+0200
RRULE:FREQ=YEARLY;INTERVAL=1;BYDAY=-1SU;BYMONTH=3
END:DAYLIGHT
END:VTIMEZONE
BEGIN:VEVENT
UID:a
SUMMARY:summer
DTSTART;TZID=Customized Time Zone:20260715T100000
END:VEVENT
BEGIN:VEVENT
UID:b
SUMMARY:winter
DTSTART;TZID=Customized Time Zone:20261215T100000
END:VEVENT
END:VCALENDAR
`))
	if err != nil {
		t.Fatal(err)
	}
	got := feed.Meetings(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC))
	if len(got) != 2 || !got[0].Start.Equal(at(2026, 7, 15, 10, 0)) || !got[1].Start.Equal(at(2026, 12, 15, 10, 0)) {
		t.Fatalf("%+v", got)
	}
}

func TestNthWeekday(t *testing.T) {
	for _, c := range []struct {
		y, m, n int
		wd      time.Weekday
		want    int
	}{{2026, 10, -1, time.Sunday, 25}, {2026, 3, -1, time.Sunday, 29}, {2026, 11, 2, time.Tuesday, 10}, {2026, 11, 5, time.Tuesday, 0}} {
		d, ok := nthWeekday(c.y, time.Month(c.m), c.n, c.wd)
		if (c.want == 0) != !ok || (ok && d != c.want) {
			t.Errorf("%+v: %d %v", c, d, ok)
		}
	}
}
