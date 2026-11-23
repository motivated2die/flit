package app

import (
	"testing"
	"time"

	"github.com/motivated2die/flit/internal/calendar"
)

func TestUpcomingJoinMeeting(t *testing.T) {
	now := time.Date(2026, 11, 23, 14, 59, 15, 0, time.UTC)
	meetings := []calendar.Meeting{
		{UID: "allday", Title: "Holiday", Start: now.Add(30 * time.Second), End: now.Add(24 * time.Hour), AllDay: true, Link: "https://meet.example/allday"},
		{UID: "nolink", Title: "Coffee", Start: now.Add(30 * time.Second), End: now.Add(30 * time.Minute)},
		{UID: "tooearly", Title: "Later", Start: now.Add(3 * time.Minute), End: now.Add(33 * time.Minute), Link: "https://meet.example/later"},
		{UID: "old", Title: "Started ago", Start: now.Add(-2 * time.Minute), End: now.Add(28 * time.Minute), Link: "https://meet.example/old"},
		{UID: "soon", Title: "Weekly sync", Start: now.Add(45 * time.Second), End: now.Add(30 * time.Minute), Link: "https://meet.example/sync"},
	}

	prompted := map[string]bool{}
	m, ok := upcomingJoinMeeting(meetings, now, prompted)
	if !ok || m.UID != "soon" {
		t.Fatalf("upcomingJoinMeeting = (%q, %v), want (soon, true)", m.UID, ok)
	}

	// Once prompted, the same meeting occurrence is not prompted again.
	prompted[meetingReminderKey(m)] = true
	if m2, ok := upcomingJoinMeeting(meetings, now, prompted); ok {
		t.Fatalf("re-prompted already shown meeting %q", m2.UID)
	}
}

func TestNotchJoinPromptState(t *testing.T) {
	a := &App{dataDir: t.TempDir()}
	m := calendar.Meeting{
		UID:   "m1",
		Title: "Design review",
		Start: time.Now().Add(45 * time.Second),
		End:   time.Now().Add(30 * time.Minute),
		Link:  "https://meet.example/design",
	}
	a.showJoinPrompt(m)
	if a.joinPrompt == nil || a.joinPrompt.UID != "m1" {
		t.Fatalf("joinPrompt = %+v, want m1", a.joinPrompt)
	}
	if a.rec != nil {
		t.Fatal("showJoinPrompt must not start recording")
	}
	a.onNotchButton("join-close")
	if a.joinPrompt != nil {
		t.Fatal("join-close did not clear joinPrompt")
	}
}
