package app

import (
	"testing"
	"time"

	"github.com/motivated2die/flit/internal/calendar"
	"github.com/motivated2die/flit/internal/notes"
	"github.com/motivated2die/flit/internal/session"
	"github.com/motivated2die/flit/internal/ui"
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

func TestMeetingRowIconsAndStatus(t *testing.T) {
	a := &App{dataDir: t.TempDir()}
	s, err := session.Create(a.dataDir, session.Meta{Title: "Sync", Started: time.Now(), State: session.Transcribing})
	if err != nil {
		t.Fatal(err)
	}

	// Transcribing shows a loading spinner.
	if glyph, spin := a.meetingRowIcon(s); !spin || glyph != "" {
		t.Fatalf("transcribing: (%q, %v), want (\"\", true)", glyph, spin)
	}

	// Transcribed only shows file-signal + "Transcribed".
	s.Meta.State = session.Ready
	if glyph, spin := a.meetingRowIcon(s); spin || glyph != ui.GlyphFileSignal {
		t.Fatalf("transcribed: (%q, %v), want (%q, false)", glyph, spin, ui.GlyphFileSignal)
	}
	if got := a.stateText(s); got != "Transcribed" {
		t.Fatalf("transcribed status = %q, want Transcribed", got)
	}

	// With notes shows file-text + "Transcribed".
	if err := s.SaveNotes(notes.Parse("Action item\n")); err != nil {
		t.Fatal(err)
	}
	if glyph, spin := a.meetingRowIcon(s); spin || glyph != ui.GlyphArticle {
		t.Fatalf("with notes: (%q, %v), want (%q, false)", glyph, spin, ui.GlyphArticle)
	}
	if got := a.stateText(s); got != "Transcribed" {
		t.Fatalf("with notes status = %q, want Transcribed", got)
	}

	// With summary shows file-text-ai + "Summarized".
	if err := s.SaveSummary("## Summary\n\nAll good.\n", session.SummaryInfo{}); err != nil {
		t.Fatal(err)
	}
	if glyph, spin := a.meetingRowIcon(s); spin || glyph != ui.GlyphFileTextAI {
		t.Fatalf("summarized: (%q, %v), want (%q, false)", glyph, spin, ui.GlyphFileTextAI)
	}
	if got := a.stateText(s); got != "Summarized" {
		t.Fatalf("summarized status = %q, want Summarized", got)
	}
}

func TestNavigateBack(t *testing.T) {
	a := testApp(t)
	s := a.sessions[0]

	// Meeting detail page -> Meetings -> Home.
	a.page = pageSession + s.Meta.ID
	a.navigateBack()
	if a.page != pageRecordings {
		t.Fatalf("from meeting detail: got %q, want %q", a.page, pageRecordings)
	}
	a.navigateBack()
	if a.page != pageHome {
		t.Fatalf("from Meetings: got %q, want %q", a.page, pageHome)
	}

	// Active recording page -> Home directly.
	a.rec = &recording{sess: s, started: time.Now()}
	a.page = pageSession + s.Meta.ID
	a.navigateBack()
	if a.page != pageHome {
		t.Fatalf("from active recording: got %q, want %q", a.page, pageHome)
	}
	a.rec = nil

	// Other top-level pages -> Home.
	for _, p := range []string{pageCalendar, pageAudio, pageSettings, pageDebug} {
		a.page = p
		a.navigateBack()
		if a.page != pageHome {
			t.Fatalf("from %q: got %q, want %q", p, a.page, pageHome)
		}
	}
}
