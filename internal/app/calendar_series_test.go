package app

import (
	"testing"
	"time"

	"github.com/motivated2die/flit/internal/session"
)

func TestPreviousInSeries(t *testing.T) {
	base := time.Date(2026, 2, 1, 10, 0, 0, 0, time.UTC)
	mk := func(id, title, uid string, days int) *session.Session {
		var m *session.MeetingInfo
		if uid != "" {
			m = &session.MeetingInfo{UID: uid, Title: "Weekly Sync", Recurring: true}
		}
		return &session.Session{Meta: session.Meta{
			ID:      id,
			Title:   title,
			Started: base.AddDate(0, 0, days),
			Meeting: m,
		}}
	}

	cur := mk("s5", "Weekly Sync", "uid-series", 28)
	all := []*session.Session{
		mk("s6", "Future occurrence", "uid-series", 35),
		cur,
		mk("s4", "Renamed Q1 Planning", "uid-series", 21), // renamed by user
		mk("s3", "Weekly Sync", "uid-series", 14),
		mk("s2", "Weekly Sync (moved)", "uid-series", 7),
		mk("s1", "Oldest occurrence", "uid-series", 0),
		mk("other", "Weekly Sync", "uid-other", 20), // same title, different UID
		mk("nocal", "Manual Note", "", 19),          // no calendar link
	}

	got := previousInSeries(all, cur, 3)
	if len(got) != 3 {
		t.Fatalf("got %d previous meetings, want 3", len(got))
	}
	wantIDs := []string{"s4", "s3", "s2"}
	for i, w := range wantIDs {
		if got[i].Meta.ID != w {
			t.Errorf("got[%d] = %s, want %s", i, got[i].Meta.ID, w)
		}
	}

	// Oldest recording has 0 previous meetings, but still has siblings in the series.
	oldest := all[5]
	if prev := previousInSeries(all, oldest, 3); len(prev) != 0 {
		t.Errorf("oldest should have 0 previous, got %d", len(prev))
	}
	if !hasOtherInSeries(all, oldest) {
		t.Errorf("oldest should report hasOtherInSeries=true")
	}

	// Single event with unique UID has no siblings.
	single := all[6]
	if hasOtherInSeries(all, single) {
		t.Errorf("single event should report hasOtherInSeries=false")
	}
}
