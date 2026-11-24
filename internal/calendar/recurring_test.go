package calendar

import (
	"strings"
	"testing"
)

const exchangeRecurringICS = "BEGIN:VCALENDAR\r\n" +
	"X-WR-CALNAME:Kalender\r\n" +
	"BEGIN:VEVENT\r\n" +
	"UID:040000008200E00074C5B7101A82E00800000000B053F924CD9CDB0100000000000000001\r\n" +
	" 0000000886A1A82B5BFE84F8695BCDD8E69B449\r\n" +
	"SUMMARY:Support JF\r\n" +
	"DTSTART:20260114T130000Z\r\n" +
	"DTEND:20260114T133000Z\r\n" +
	"RRULE:FREQ=WEEKLY;INTERVAL=1;BYDAY=WE;WKST=MO\r\n" +
	"END:VEVENT\r\n" +
	"BEGIN:VEVENT\r\n" +
	"UID:040000008200E00074C5B7101A82E00800000000B053F924CD9CDB0100000000000000001\r\n" +
	" 0000000886A1A82B5BFE84F8695BCDD8E69B449\r\n" +
	"RECURRENCE-ID:20260121T130000Z\r\n" +
	"SUMMARY:Support JF (verschoben)\r\n" +
	"DTSTART:20260120T093000Z\r\n" +
	"DTEND:20260120T100000Z\r\n" +
	"END:VEVENT\r\n" +
	"BEGIN:VEVENT\r\n" +
	"UID:single-event-uid-12345\r\n" +
	"SUMMARY:Ad-hoc Sync\r\n" +
	"DTSTART:20260115T100000Z\r\n" +
	"DTEND:20260115T103000Z\r\n" +
	"END:VEVENT\r\n" +
	"END:VCALENDAR\r\n"

func TestRecurringDetectionAndSeriesUID(t *testing.T) {
	feed, err := Parse(strings.NewReader(exchangeRecurringICS))
	if err != nil {
		t.Fatal(err)
	}
	got := feed.Meetings(at(2026, 1, 13, 0, 0), at(2026, 1, 29, 0, 0))
	if len(got) != 4 {
		t.Fatalf("expected 4 meetings (2 master + 1 rescheduled + 1 single), got %d: %+v", len(got), got)
	}
	const wantSeriesUID = "040000008200E00074C5B7101A82E00800000000B053F924CD9CDB01000000000000000010000000886A1A82B5BFE84F8695BCDD8E69B449"

	byTitle := map[string][]Meeting{}
	for _, m := range got {
		byTitle[m.Title] = append(byTitle[m.Title], m)
	}
	if len(byTitle["Support JF"]) != 2 {
		t.Fatalf("expected 2 master occurrences, got %d", len(byTitle["Support JF"]))
	}
	for _, m := range byTitle["Support JF"] {
		if !m.Recurring || m.UID != wantSeriesUID {
			t.Errorf("master occurrence: recurring=%v uid=%q", m.Recurring, m.UID)
		}
	}
	if len(byTitle["Support JF (verschoben)"]) != 1 {
		t.Fatalf("expected 1 rescheduled override, got %d", len(byTitle["Support JF (verschoben)"]))
	}
	moved := byTitle["Support JF (verschoben)"][0]
	if !moved.Recurring || moved.UID != wantSeriesUID {
		t.Errorf("rescheduled override should stay recurring with same UID: recurring=%v uid=%q", moved.Recurring, moved.UID)
	}
	if len(byTitle["Ad-hoc Sync"]) != 1 {
		t.Fatalf("expected 1 single event, got %d", len(byTitle["Ad-hoc Sync"]))
	}
	single := byTitle["Ad-hoc Sync"][0]
	if single.Recurring {
		t.Errorf("single event should not be marked recurring")
	}
}
