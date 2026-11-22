package calendar

import (
	"testing"
	"time"
)

func mtg(title string, h, mi, dur int, provider string) Meeting {
	s := at(2026, 11, 24, h, mi)
	return Meeting{UID: title, Title: title, Start: s, End: s.Add(time.Duration(dur) * time.Minute), Provider: provider}
}

func TestMatchClearMeeting(t *testing.T) {
	ms := []Meeting{mtg("Standup", 9, 30, 15, "meet"), mtg("AG Projekt", 14, 0, 60, "zoom"), mtg("Review", 16, 0, 60, "teams")}
	// Zoom recording started at 13:57: the 14:00 meeting.
	m := MatchRecording(ms, at(2026, 11, 24, 13, 57), time.Time{}, "zoom", "Zoom Meeting")
	if m.Best == nil || m.Best.Title != "AG Projekt" {
		t.Fatalf("%+v", m)
	}
	// Nothing near 11:00.
	if m := MatchRecording(ms, at(2026, 11, 24, 11, 0), time.Time{}, "zoom", ""); m.Best != nil || len(m.Choices) > 0 {
		t.Fatalf("matched at 11:00: %+v", m)
	}
}

func TestMatchParallelMeetingsOfferChoices(t *testing.T) {
	// Two Zoom meetings at 14:00 (as in the real feed): nothing tells them
	// apart, so the user chooses.
	ms := []Meeting{mtg("AG Projekt", 14, 0, 60, "zoom"), mtg("Projekt Cloud an der Uni", 14, 0, 60, "zoom")}
	m := MatchRecording(ms, at(2026, 11, 24, 14, 1), at(2026, 11, 24, 14, 58), "zoom", "Zoom Meeting")
	if m.Best != nil || len(m.Choices) != 2 {
		t.Fatalf("%+v", m)
	}
	// Teams shows the meeting's name in its window: that decides.
	ms = []Meeting{mtg("Budget review", 14, 0, 60, "teams"), mtg("Hiring sync", 14, 0, 30, "teams")}
	m = MatchRecording(ms, at(2026, 11, 24, 14, 2), time.Time{}, "teams", "Budget review | Microsoft Teams")
	if m.Best == nil || m.Best.Title != "Budget review" {
		t.Fatalf("by title: %+v", m)
	}
	// The app decides between a Zoom and a Teams meeting at the same time.
	ms = []Meeting{mtg("A", 14, 0, 60, "zoom"), mtg("B", 14, 0, 60, "teams")}
	if m := MatchRecording(ms, at(2026, 11, 24, 14, 0), time.Time{}, "teams-web", ""); m.Best == nil || m.Best.Title != "B" {
		t.Fatalf("by app: %+v", m)
	}
}

func TestMatchByOverlapAtTheEnd(t *testing.T) {
	// Back-to-back meetings; a recording from 14:55 to 15:50 is the second.
	ms := []Meeting{mtg("First", 14, 0, 60, ""), mtg("Second", 15, 0, 60, "")}
	m := MatchRecording(ms, at(2026, 11, 24, 14, 55), at(2026, 11, 24, 15, 50), "", "")
	if m.Best == nil || m.Best.Title != "Second" {
		t.Fatalf("%+v", m)
	}
	// All-day events never match.
	allDay := Meeting{Title: "Holiday", Start: at(2026, 11, 24, 0, 0), End: at(2026, 11, 25, 0, 0), AllDay: true}
	if m := MatchRecording([]Meeting{allDay}, at(2026, 11, 24, 10, 0), time.Time{}, "", ""); m.Best != nil {
		t.Fatal("all-day matched")
	}
}

func TestMatchLenientWhileRunning(t *testing.T) {
	ms := []Meeting{mtg("Workshop", 14, 0, 60, ""), mtg("Later", 16, 0, 30, "")}
	// Recorded by hand 40 minutes into the meeting: still that meeting.
	if m := MatchRecording(ms, at(2026, 11, 24, 14, 40), time.Time{}, "", ""); m.Best == nil || m.Best.Title != "Workshop" {
		t.Fatalf("40 min in: %+v", m)
	}
	// Started 10 minutes after its scheduled end: it overran.
	if m := MatchRecording(ms, at(2026, 11, 24, 15, 10), time.Time{}, "", ""); m.Best == nil || m.Best.Title != "Workshop" {
		t.Fatalf("overrun: %+v", m)
	}
	// Back-to-back: a recording begun just before the second one is the
	// second, not the end of the first.
	ms = []Meeting{mtg("First", 14, 0, 60, ""), mtg("Second", 15, 0, 60, "")}
	if m := MatchRecording(ms, at(2026, 11, 24, 14, 56), time.Time{}, "", ""); m.Best == nil || m.Best.Title != "Second" {
		t.Fatalf("back-to-back: %+v", m)
	}
}
