package detect

import "testing"

func snap() Snapshot {
	return Snapshot{
		Self: 1,
		Procs: map[uint32]Proc{
			1:  {1, 0, "flit.exe"},
			10: {10, 2, "explorer.exe"},
			20: {20, 10, "ms-teams.exe"},
			21: {21, 20, "msedgewebview2.exe"}, // Teams' media in a WebView2 child
			30: {30, 10, "chrome.exe"},
			31: {31, 30, "chrome.exe"}, // the audio service
			40: {40, 10, "discord.exe"},
			50: {50, 10, "obs64.exe"},
		},
		Titles: map[uint32][]string{
			30: {"Inbox - Gmail - Google Chrome", "Meet – abc-defg-hij - Google Chrome"},
		},
	}
}

func all(Provider) bool { return true }

func TestFindTeamsThroughWebView(t *testing.T) {
	s := snap()
	s.Capturing = []uint32{21, 1, 50}
	s.MicOf = map[uint32]string{21: "{headset}", 1: "{default}", 50: "{default}"}
	got := Find(s, all)
	if len(got) != 1 || got[0].Provider != "teams" || got[0].Target != 20 || got[0].Mic != "{headset}" {
		t.Fatalf("%+v", got)
	}
	users := MicUsers(s)
	if len(users) != 2 || users[0].Provider != "teams" || users[1].Exe != "obs64.exe" || users[1].Provider != "" {
		t.Fatalf("users %+v", users)
	}
}

func TestFindBrowserMeetingByTitle(t *testing.T) {
	s := snap()
	s.Capturing = []uint32{31}
	got := Find(s, all)
	if len(got) != 1 || got[0].Provider != "meet" || got[0].Target != 30 || got[0].Title == "" {
		t.Fatalf("%+v", got)
	}
	// The same browser using the mic for something else: no meeting.
	s.Titles[30] = []string{"Voice memo - Google Chrome"}
	if got := Find(s, all); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestFindRespectsSwitches(t *testing.T) {
	s := snap()
	s.Capturing = []uint32{40}
	if got := Find(s, func(p Provider) bool { return p.On }); len(got) != 0 {
		t.Fatalf("Discord is off by default: %+v", got)
	}
	if got := Find(s, all); len(got) != 1 || got[0].Provider != "discord" {
		t.Fatalf("%+v", got)
	}
}

func TestTitlePatterns(t *testing.T) {
	meet, _ := ProviderByID("meet")
	teams, _ := ProviderByID("teams-web")
	for title, want := range map[string]string{
		"Meet – abc-defg-hij - Google Chrome":                   "meet",
		"meet.google.com is sharing your screen":                "meet",
		"Weekly sync | Microsoft Teams - Microsoft Edge":        "teams-web",
		"Meeting notes about Google Meet alternatives - Chrome": "",
		"Teams pricing - Google Search":                         "",
	} {
		got := ""
		for _, p := range []Provider{meet, teams} {
			for _, re := range p.Titles {
				if re.MatchString(title) {
					got = p.ID
				}
			}
		}
		if got != want {
			t.Errorf("%q: %q, want %q", title, got, want)
		}
	}
}

func TestTrackerHysteresis(t *testing.T) {
	tr := NewTracker()
	teams := []Candidate{{Provider: "teams", Target: 20}}
	if ev := tr.Update(teams); len(ev) != 0 {
		t.Fatalf("started after one poll: %v", ev)
	}
	ev := tr.Update(teams)
	if len(ev) != 1 || ev[0].Kind != Started || ev[0].Target != 20 {
		t.Fatalf("%v", ev)
	}
	// Two polls without it (the app re-opening its mic): still on.
	tr.Update(nil)
	tr.Update(nil)
	if ev := tr.Update(teams); len(ev) != 0 || len(tr.Active()) != 1 {
		t.Fatalf("flapped: %v", ev)
	}
	for i := 0; i < 3; i++ {
		if ev := tr.Update(nil); len(ev) != 0 {
			t.Fatalf("ended early at %d: %v", i, ev)
		}
	}
	ev = tr.Update(nil)
	if len(ev) != 1 || ev[0].Kind != Ended {
		t.Fatalf("%v", ev)
	}
	if len(tr.Active()) != 0 {
		t.Fatal("still active")
	}
}

func TestTrackerBlipDoesNotStart(t *testing.T) {
	tr := NewTracker()
	tr.Update([]Candidate{{Provider: "zoom"}})
	tr.Update(nil)
	if ev := tr.Update([]Candidate{{Provider: "zoom"}}); len(ev) != 0 {
		t.Fatalf("two separate blips started a meeting: %v", ev)
	}
}
