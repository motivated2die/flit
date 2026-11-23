package app

import (
	"encoding/json"
	"fmt"
	"github.com/motivated2die/flit/internal/calendar"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/motivated2die/flit/internal/capture"
	"github.com/motivated2die/flit/internal/config"
	"github.com/motivated2die/flit/internal/coreaudio"
	"github.com/motivated2die/flit/internal/detect"
	"github.com/motivated2die/flit/internal/models"
	"github.com/motivated2die/flit/internal/session"
	"github.com/motivated2die/flit/internal/transcript"
	"github.com/motivated2die/flit/internal/ui"
)

// testApp is an App with a few sessions on disk and no tray or windows.
func testApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	a := &App{dataDir: dir, cfg: config.Default(), dismissed: map[string]bool{}, previews: map[string]string{}, page: pageHome}
	a.cfg.SetupComplete = true
	at := time.Date(2026, 11, 21, 14, 2, 0, 0, time.Local)
	for i, m := range []session.Meta{
		{Provider: "teams", Title: "Weekly sync", Language: "de", Started: at, Ended: at.Add(43 * time.Minute), State: session.Ready},
		{Provider: "zoom", Language: "en", Started: at.Add(-26 * time.Hour), Ended: at.Add(-25 * time.Hour), State: session.Failed, Error: "the speech model couldn't be loaded"},
		{Language: "de", Started: at.Add(-50 * time.Hour), Ended: at.Add(-50*time.Hour + 5*time.Minute), State: session.Ready},
	} {
		s, err := session.Create(dir, m)
		if err != nil {
			t.Fatal(err)
		}
		if m.State == session.Ready {
			text := "Weekly sync · 2026-11-21 14:02–14:45 · German\n\n" +
				"[00:00] Me: Hallo zusammen, lasst uns mit dem Release-Plan anfangen.\n" +
				"[00:06] Them: Gerne. Ich habe gestern noch die Zahlen aktualisiert, und es sieht gut aus für Freitag.\n" +
				strings.Repeat("[01:12] Me: Dann gehen wir die offenen Punkte durch, einen nach dem anderen.\n", i+3)
			os.WriteFile(s.Path(session.TranscriptText), []byte(text), 0o644)
		}
		if i == 0 { // the weekly sync has speakers, one of them named
			sec := func(v float64) time.Duration { return time.Duration(v * float64(time.Second)) }
			segs := []transcript.Segment{
				{Track: transcript.Me, Start: 0, End: sec(5), Text: "Hallo zusammen, lasst uns mit dem Release-Plan anfangen."},
				{Track: transcript.Them, Speaker: 1, Start: sec(6), End: sec(14), Text: "Gerne. Ich habe gestern noch die Zahlen aktualisiert, und es sieht gut aus für Freitag."},
				{Track: transcript.Them, Speaker: 2, Start: sec(15), End: sec(80), Text: "Von meiner Seite gibt es noch zwei offene Punkte beim Installer, die wir vor dem Release klären sollten, sonst wird es knapp."},
			}
			b, _ := json.Marshal(session.Result{Segments: segs})
			os.WriteFile(s.Path(session.TranscriptJSON), b, 0o644)
			os.WriteFile(s.Path(session.ThemFile), nil, 0o644)
			s.Meta.Speakers = 2
			s.Save()
			s.SetNames(transcript.Names{Speakers: map[int]string{2: "Anna"}})
		}
	}
	a.reloadSessions()
	return a
}

func TestPagesBuild(t *testing.T) {
	a := testApp(t)
	for _, page := range []string{pageHome, pageRecordings, pageCalendar, pageSettings, pageDebug, pageWelcome, pageSession + a.sessions[0].Meta.ID, pageSession + "gone"} {
		a.page = page
		f := a.frame()
		img, err := ui.RenderFrame(f, 960, 640, 1, "", nil)
		if err != nil {
			t.Fatalf("%s: %v", page, err)
		}
		if dir := os.Getenv("FLIT_RENDER_DIR"); dir != "" {
			name := strings.NewReplacer(":", "-").Replace(page)
			fh, _ := os.Create(filepath.Join(dir, "window-"+name+".png"))
			png.Encode(fh, img)
			fh.Close()
		}
	}
}

func TestHomeShowsLatestTranscript(t *testing.T) {
	a := testApp(t)
	s := a.latestReady()
	if s == nil || sessionTitle(s) != "Weekly sync" {
		t.Fatalf("latest %v", s)
	}
	if p := a.preview(s); strings.Contains(p, "German") || !strings.HasPrefix(p, "[00:00] Me:") {
		t.Fatalf("preview keeps the header: %q", p)
	}
}

func TestSessionWhen(t *testing.T) {
	at := time.Date(2026, 11, 21, 14, 2, 0, 0, time.Local)
	s := &session.Session{Meta: session.Meta{Started: at, Ended: at.Add(43*time.Minute + 20*time.Second)}}
	if got := sessionWhen(s); got != "Sat 21 Nov 2026 · 14:02 · 43 min" {
		t.Fatalf("%q", got)
	}
}

func sampleDevices(a *App) {
	a.devs = devices{
		mics: []coreaudio.Endpoint{
			{ID: "{m1}", Name: "Mikrofon (BlackShark V3 Pro XBOX - Chat)"},
			{ID: "{m2}", Name: "SteelSeries Sonar - Microphone", Default: true},
		},
		outputs: []coreaudio.Endpoint{{ID: "{o1}", Name: "SteelSeries Sonar - Chat", Default: true}, {ID: "{o2}", Name: "SteelSeries Sonar - Gaming"}},
		defMic:  "{m2}", defOut: "{o2}",
		names: map[string]string{"{m1}": "Mikrofon (BlackShark V3 Pro XBOX - Chat)", "{m2}": "SteelSeries Sonar - Microphone", "{o1}": "SteelSeries Sonar - Chat", "{o2}": "SteelSeries Sonar - Gaming"},
	}
	a.cfg.MicID, a.cfg.MicName = "{m1}", "Mikrofon (BlackShark V3 Pro XBOX - Chat)"
}

func TestAudioPageAndPanel(t *testing.T) {
	a := testApp(t)
	sampleDevices(a)
	a.page = pageAudio
	img, err := ui.RenderFrame(a.frame(), 960, 860, 1, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := os.Getenv("FLIT_RENDER_DIR")
	if dir != "" {
		fh, _ := os.Create(filepath.Join(dir, "window-audio.png"))
		png.Encode(fh, img)
		fh.Close()
	}
	states := map[string]func(){
		"idle":     func() {},
		"detected": func() { a.detected = &detect.Candidate{Provider: "zoom", Target: 1} },
		"counting": func() { a.autoFrom = time.Now() },
		"working":  func() { a.detected = nil; a.autoFrom = time.Time{}; a.working = a.sessions[0]; a.progress = 0.42 },
	}
	for _, name := range []string{"idle", "detected", "counting", "working"} {
		states[name]()
		hover := ""
		if name == "idle" {
			hover = "panel:quit"
		}
		s, err := ui.RenderPanel(a.panelContent(), 1, hover, false)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if dir != "" {
			fh, _ := os.Create(filepath.Join(dir, "panel-"+name+".png"))
			png.Encode(fh, ui.SurfaceImage(s))
			fh.Close()
		}
		s.Free()
	}
}

func TestMicSource(t *testing.T) {
	a := testApp(t)
	c := &detect.Candidate{Provider: "zoom", Mic: "{headset}"}
	if src := a.micSource(c); src.DeviceID != "{headset}" {
		t.Fatalf("automatic: %+v, want the meeting app's microphone", src)
	}
	if src := a.micSource(nil); src.DeviceID != "" {
		t.Fatalf("by hand: %+v, want Windows' default", src)
	}
	a.cfg.MicID = "{chosen}"
	if src := a.micSource(c); src.DeviceID != "{chosen}" {
		t.Fatalf("chosen: %+v", src)
	}
}

func TestDebugLines(t *testing.T) {
	a := testApp(t)
	lines := a.debugLines()
	if len(lines) != 2 || lines[1] != "Not loaded" || !strings.Contains(lines[0], "Parakeet") {
		t.Fatalf("%q", lines)
	}
}

func TestSpeakersCardRenames(t *testing.T) {
	a := testApp(t)
	var s *session.Session
	for _, x := range a.sessions {
		if x.Meta.Title == "Weekly sync" {
			s = x
		}
	}
	sp := a.speakers(s)
	if len(sp) != 3 || sp[2].Speaker != 2 {
		t.Fatalf("%+v", sp)
	}
	a.rename(s, transcript.Them, 1, "Ben")
	a.rename(s, transcript.Me, 0, "Moritz")
	text, _ := s.Transcript(false)
	for _, want := range []string{"Moritz: Hallo", "Ben: Gerne", "Anna: Von meiner"} {
		if !strings.Contains(text, want) {
			t.Errorf("transcript lacks %q:\n%s", want, text)
		}
	}
	// Back to the default.
	a.rename(s, transcript.Them, 1, "  ")
	if text, _ = s.Transcript(false); !strings.Contains(text, "Speaker 1: Gerne") {
		t.Fatalf("default not restored:\n%s", text)
	}
}

func TestRecordingFacts(t *testing.T) {
	a := testApp(t)
	var weekly *session.Session
	for _, s := range a.sessions {
		if s.Meta.Title == "Weekly sync" {
			weekly = s
		}
	}
	if f := a.facts(weekly); !strings.Contains(f, "43 min") || !strings.Contains(f, "3 speakers") || !strings.Contains(f, "words") {
		t.Fatalf("facts %q", f)
	}
	if sn := a.snippet(weekly); !strings.HasPrefix(sn, "Me: Hallo zusammen") {
		t.Fatalf("snippet %q", sn)
	}
	if plural(1284, "word") != "1,284 words" || plural(1, "speaker") != "1 speaker" {
		t.Fatal("plural")
	}
}

func TestDeleteAudioAndRecording(t *testing.T) {
	a := testApp(t)
	s := a.sessions[0]
	os.WriteFile(s.Path(session.MeFile), []byte("x"), 0o644)
	a.deleteAudio(s)
	if s.Has(session.MeFile) || s.Has(session.ThemFile) {
		t.Fatal("audio still there")
	}
	if _, err := os.Stat(s.Path(session.TranscriptText)); err != nil {
		t.Fatal("transcript went with the audio")
	}
	n := len(a.sessions)
	a.deleteRecording(s)
	if _, err := os.Stat(s.Dir); !os.IsNotExist(err) || len(a.sessions) != n-1 || a.page != pageRecordings {
		t.Fatalf("not deleted: %v, %d sessions, page %s", err, len(a.sessions), a.page)
	}
	// Never a folder outside the sessions folder.
	outside := t.TempDir()
	a.deleteRecording(&session.Session{Dir: outside})
	if _, err := os.Stat(outside); err != nil {
		t.Fatal("deleted a folder outside the sessions folder")
	}
}

func TestCleanupOldAudioAndAll(t *testing.T) {
	a := testApp(t)
	// Weekly sync (today) and the 50-hour-old recording are transcribed;
	// make the second one old and give both audio.
	var recent, old *session.Session
	for _, s := range a.sessions {
		if s.Meta.State != session.Ready {
			continue
		}
		if recent == nil {
			recent = s
		} else {
			old = s
		}
	}
	old.Meta.Started = time.Now().Add(-40 * 24 * time.Hour)
	old.Save()
	for _, s := range []*session.Session{recent, old} {
		os.WriteFile(s.Path(session.MeFile), make([]byte, 1000), 0o644)
		os.WriteFile(s.Path(session.ThemFile), make([]byte, 1000), 0o644)
	}
	a.reloadSessions()
	a.countUsage()
	if a.usage.oldAudioN != 1 || a.usage.oldAudio != 2000 || a.usage.allAudioN != 2 {
		t.Fatalf("usage %+v", a.usage)
	}
	cutoff := time.Now().Add(-oldAudioAge)
	a.deleteAudioWhere(func(s *session.Session) bool { return s.Meta.Started.Before(cutoff) })
	if old.Has(session.MeFile) || !recent.Has(session.MeFile) {
		t.Fatal("wrong audio deleted")
	}
	// Everything, except the recording being transcribed.
	a.working = recent
	a.deleteAllRecordings()
	if len(a.sessions) != 1 || a.sessions[0].Dir != recent.Dir {
		t.Fatalf("%d sessions left", len(a.sessions))
	}
}

func TestRenameSession(t *testing.T) {
	a := testApp(t)
	s := a.sessions[0]
	a.renameSession(s, " Budget review ")
	if sessionTitle(s) != "Budget review" {
		t.Fatalf("%q", sessionTitle(s))
	}
	a.renameSession(s, "")
	if sessionTitle(s) != defaultTitle(s) {
		t.Fatalf("default: %q", sessionTitle(s))
	}
}

func TestAutoRecordCountdown(t *testing.T) {
	a := testApp(t)
	if !a.cfg.AutoRecord {
		t.Fatal("auto-record is off by default")
	}
	a.detected = &detect.Candidate{Provider: "teams", Target: 1}
	a.startCountdown()
	if !a.counting() || a.homeActions() == nil {
		t.Fatal("no countdown")
	}
	if !strings.Contains(a.homeStatus(), "starts recording") {
		t.Fatalf("status %q", a.homeStatus())
	}
	token := a.autoToken
	a.stopCountdown() // Cancel
	if a.counting() || a.autoToken == token {
		t.Fatal("countdown not cancelled")
	}
}

// calendarApp is testApp with a calendar: a meeting going on now (Meet),
// two parallel Zoom meetings tomorrow at 14:00, one in three days.
func calendarApp(t *testing.T) *App {
	a := testApp(t)
	now := time.Now()
	day := startOfDay(now)
	ms := []calendar.Meeting{
		{UID: "standup", Title: "Standup", Start: now.Add(-5 * time.Minute), End: now.Add(10 * time.Minute), Provider: "meet", Link: "https://meet.google.com/abc-defg-hij"},
		{UID: "ag", Title: "AG Projekt", Start: day.Add(38 * time.Hour), End: day.Add(39 * time.Hour), Provider: "zoom", Link: "https://example.zoom.us/j/1"},
		{UID: "cloud", Title: "Projekt Cloud an der Uni", Start: day.Add(38 * time.Hour), End: day.Add(39 * time.Hour), Provider: "zoom", Link: "https://example.zoom.us/j/2"},
		{UID: "review", Title: "Review", Start: day.Add(82 * time.Hour), End: day.Add(83 * time.Hour), Location: "Room 4"},
	}
	a.cal = &calendarState{svc: &calendar.Service{}, status: calendar.Status{Configured: true, Name: "Work", Meetings: ms, Updated: now}}
	return a
}

func TestCalendarMatchesRecordings(t *testing.T) {
	a := calendarApp(t)
	s, _ := session.Create(a.dataDir, session.Meta{Provider: "meet", Language: "de", Started: time.Now().Add(-3 * time.Minute)})
	a.matchMeeting(s, time.Time{}, "Meet - abc-defg-hij - Google Chrome")
	if s.Meta.Meeting == nil || s.Meta.Meeting.Title != "Standup" || sessionTitle(s) != "Standup" {
		t.Fatalf("%+v", s.Meta)
	}
	// A name the user gave stays.
	s.SetTitle("Daily")
	a.matchMeeting(s, time.Now(), "")
	if sessionTitle(s) != "Daily" {
		t.Fatalf("title %q", sessionTitle(s))
	}
	// Two Zoom meetings at the same time: a question, then the answer.
	day := startOfDay(time.Now())
	p, _ := session.Create(a.dataDir, session.Meta{Provider: "zoom", Language: "de", Started: day.Add(38*time.Hour + time.Minute)})
	a.matchMeeting(p, time.Time{}, "Zoom Meeting")
	if p.Meta.Meeting != nil || len(p.Meta.MeetingChoices) != 2 {
		t.Fatalf("choices %+v", p.Meta)
	}
	choice := p.Meta.MeetingChoices[1]
	a.chooseMeeting(p, &choice)
	if !p.Meta.MeetingChosen || sessionTitle(p) != choice.Title || len(p.Meta.MeetingChoices) != 0 {
		t.Fatalf("chosen %+v", p.Meta)
	}
	a.matchMeeting(p, day.Add(39*time.Hour), "") // the end: the choice stays
	if p.Meta.Meeting.UID != choice.UID {
		t.Fatal("choice overwritten")
	}
	if n, ok := a.detectedName("meet", ""); !ok || n != "Standup" {
		t.Fatalf("notch name %q %v", n, ok)
	}
}

func TestCalendarPages(t *testing.T) {
	a := calendarApp(t)
	dir := os.Getenv("FLIT_RENDER_DIR")
	for _, page := range []string{pageCalendar, pageHome, pageSettings} {
		a.page = page
		img, err := ui.RenderFrame(a.frame(), 960, 900, 1, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		if dir != "" {
			fh, _ := os.Create(filepath.Join(dir, "calendar-"+page+".png"))
			png.Encode(fh, img)
			fh.Close()
		}
	}
	if len(a.upcomingSection()) < 2 {
		t.Fatal("no upcoming meetings on Home")
	}
	if dayName(startOfDay(time.Now()).AddDate(0, 0, 1), startOfDay(time.Now())) != "Tomorrow" {
		t.Fatal("tomorrow")
	}
}

func TestPanelMeetings(t *testing.T) {
	a := calendarApp(t)
	nodes := a.panelMeetings()
	var rows []ui.MeetingRow
	for _, n := range nodes {
		if r, ok := n.(ui.MeetingRow); ok {
			rows = append(rows, r)
		}
	}
	if len(rows) != 2 || rows[0].Title != "Standup" || len(rows[0].Actions) != 2 || !strings.HasPrefix(rows[1].Place, "Tomorrow") {
		t.Fatalf("%+v", rows)
	}
	s, err := ui.RenderPanel(a.panelContent(), 1, "panel:cal:0:join", false)
	if err != nil {
		t.Fatal(err)
	}
	if dir := os.Getenv("FLIT_RENDER_DIR"); dir != "" {
		fh, _ := os.Create(filepath.Join(dir, "panel-meetings.png"))
		png.Encode(fh, ui.SurfaceImage(s))
		fh.Close()
	}
	s.Free()
	// No calendar: no section.
	b := testApp(t)
	if b.panelMeetings() != nil {
		t.Fatal("section without a calendar")
	}
}

func TestRecordingSearch(t *testing.T) {
	a := testApp(t)
	a.page = pageRecordings
	names := func(list []*session.Session) []string {
		var out []string
		for _, s := range list {
			out = append(out, sessionTitle(s))
		}
		return out
	}
	// By name (and date), every word must match.
	a.setSearch("weekly")
	if found, _ := a.searchSessions(); len(found) != 1 || sessionTitle(found[0]) != "Weekly sync" {
		t.Fatalf("by name: %v", names(found))
	}
	a.setSearch("zoom 20 nov")
	if found, _ := a.searchSessions(); len(found) != 1 || sessionTitle(found[0]) != "Zoom meeting" {
		t.Fatalf("name and date: %v", names(found))
	}
	// Words that were said: only when searching transcripts too.
	a.setSearch("offenen PUNKTE")
	if found, _ := a.searchSessions(); len(found) != 0 {
		t.Fatalf("names only found %v", names(found))
	}
	a.searchDeep = true
	found, snippets := a.searchSessions()
	if len(found) != 1 || sessionTitle(found[0]) != "Recording" {
		t.Fatalf("in transcripts: %v", names(found))
	}
	if sn := snippets[found[0].Meta.ID]; sn != "Me: Dann gehen wir die offenen Punkte durch, einen nach dem anderen." {
		t.Fatalf("snippet %q", sn)
	}
	a.setSearch("")
	if found, _ := a.searchSessions(); len(found) != len(a.sessions) {
		t.Fatal("an empty search hides recordings")
	}
	a.setSearch("gerne")
	img, err := ui.RenderFrame(a.frame(), 960, 640, 1, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if dir := os.Getenv("FLIT_RENDER_DIR"); dir != "" {
		fh, _ := os.Create(filepath.Join(dir, "window-recordings-search.png"))
		png.Encode(fh, img)
		fh.Close()
	}
}

func TestMatchLine(t *testing.T) {
	long := "[12:04] Anna: " + strings.Repeat("eins zwei drei ", 8) + "Installer und mehr"
	got := matchLine("[00:00] Me: Hallo\n"+long, "installer")
	if !strings.HasPrefix(got, "Anna: …") || !strings.Contains(got, "Installer und mehr") {
		t.Fatalf("%q", got)
	}
}

func TestSessionPlayerDocked(t *testing.T) {
	a := testApp(t)
	var s *session.Session
	for _, x := range a.sessions {
		if x.Meta.Title == "Weekly sync" { // the one with a Them track
			s = x
		}
	}
	a.page = pageSession + s.Meta.ID
	f := a.frame()
	if f.Dock == nil || f.DockID != "player:"+s.Meta.ID {
		t.Fatalf("no docked player: %q", f.DockID)
	}
	// Scrubbing shows the place under the pointer before playing from it.
	f.Dock.(ui.PlayerBar).OnScrub(0.5, false)
	if pos := a.playerBar(s).(ui.PlayerBar).Pos; !a.scrubbing || pos != 43*time.Minute/2 {
		t.Fatalf("scrubbing %v at %v", a.scrubbing, pos)
	}
	a.scrubbing = false
	img, err := ui.RenderFrame(a.frame(), 960, 640, 1, "para:1", map[string]float32{ui.FrameScrollID(a.page): 300})
	if err != nil {
		t.Fatal(err)
	}
	if dir := os.Getenv("FLIT_RENDER_DIR"); dir != "" {
		fh, _ := os.Create(filepath.Join(dir, "window-session-transcript.png"))
		png.Encode(fh, img)
		fh.Close()
	}
	// A page without audio has no player.
	for _, x := range a.sessions {
		if x != s {
			a.page = pageSession + x.Meta.ID
			if f := a.frame(); f.Dock != nil {
				t.Fatalf("%s: a player without audio", sessionTitle(x))
			}
		}
	}
}

func TestLiveGlowPictures(t *testing.T) {
	a := testApp(t)
	f := a.frame()
	f.Live, f.Status = true, "Recording 12:34"
	img, err := ui.RenderFrame(f, 960, 640, 1.25, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	s, err := ui.RenderPanel(a.panelContent(), 1.25, "panel:latest", true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Free()
	if dir := os.Getenv("FLIT_RENDER_DIR"); dir != "" {
		fh, _ := os.Create(filepath.Join(dir, "window-live-glow.png"))
		png.Encode(fh, img)
		fh.Close()
		fh, _ = os.Create(filepath.Join(dir, "panel-live-glow.png"))
		png.Encode(fh, ui.SurfaceImage(s))
		fh.Close()
	}
	// The light is there: the bottom left corner is redder than the
	// sidebar further up.
	corner, above := img.RGBAAt(4, img.Bounds().Dy()-4), img.RGBAAt(4, img.Bounds().Dy()/3)
	if int(corner.R)-int(corner.B) <= int(above.R)-int(above.B)+4 {
		t.Fatalf("no glow: corner %v, above %v", corner, above)
	}
}

func TestRecordingVoiceMeter(t *testing.T) {
	a := testApp(t)
	// A recording without capture streams: the meter shows flat bars.
	a.rec = &recording{sess: a.sessions[0], r: &capture.Recording{}, started: time.Now().Add(-90 * time.Second)}
	if b := a.voiceBands(); len(b[0]) != capture.NBands || len(b[1]) != capture.NBands {
		t.Fatalf("bands %v", b)
	}
	img, err := ui.RenderFrame(a.frame(), 960, 640, 1, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	narrow, err := ui.RenderFrame(a.frame(), 720, 640, 1, "", nil) // the window's smallest width
	if err != nil {
		t.Fatal(err)
	}
	s, err := ui.RenderPanel(a.panelContent(), 1, "", true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Free()
	if dir := os.Getenv("FLIT_RENDER_DIR"); dir != "" {
		fh, _ := os.Create(filepath.Join(dir, "window-home-recording.png"))
		png.Encode(fh, img)
		fh.Close()
		fh, _ = os.Create(filepath.Join(dir, "window-home-recording-narrow.png"))
		png.Encode(fh, narrow)
		fh.Close()
		fh, _ = os.Create(filepath.Join(dir, "panel-recording.png"))
		png.Encode(fh, ui.SurfaceImage(s))
		fh.Close()
	}
}

func TestFindFold(t *testing.T) {
	for _, c := range []struct {
		s, q string
		want []int
	}{
		{"Release-Plan vor dem release", "RELEASE", []int{0, 21}},
		{"aaaa", "aa", []int{0, 2}},           // not overlapping
		{"Grüße aus Köln", "KÖLN", []int{12}}, // bytes: ü and ß take two
		{"nichts", "xyz", nil},
	} {
		got := findFold(c.s, c.q)
		if fmt.Sprint(got) != fmt.Sprint(c.want) {
			t.Errorf("findFold(%q, %q) = %v, want %v", c.s, c.q, got, c.want)
		}
	}
}

func TestTranscriptSearch(t *testing.T) {
	a := testApp(t)
	var s *session.Session
	for _, x := range a.sessions {
		if x.Meta.Title == "Weekly sync" {
			s = x
		}
	}
	a.go_(pageSession + s.Meta.ID)
	marks := func() (n int, shown int, scroll int) {
		card := a.transcriptBlocks(s).(ui.CornerCard)
		for i, it := range card.Items {
			b := it.(ui.TranscriptBlock)
			n += len(b.Marks)
			for _, m := range b.Marks {
				if m.Underline {
					shown = i
				}
			}
			if b.ScrollTo {
				scroll++
			}
		}
		return
	}
	a.setTranscriptSearch("release")
	if n, shown, scroll := marks(); n != 2 || shown != 0 || scroll != 1 {
		t.Fatalf("2 results in paragraphs 0 and 2: %d marks, shown in %d, %d scrolls", n, shown, scroll)
	}
	// The scroll is asked for once, then the page is left alone.
	if _, _, scroll := marks(); scroll != 0 {
		t.Fatal("scrolls again without a new search or arrow")
	}
	a.stepResult(1)
	if _, shown, scroll := marks(); shown != 2 || scroll != 1 {
		t.Fatalf("next: shown in %d, %d scrolls", shown, scroll)
	}
	a.stepResult(1) // past the end: back to the first
	if _, shown, _ := marks(); shown != 0 {
		t.Fatalf("wrap: shown in %d", shown)
	}
	a.stepResult(-1)
	if _, shown, _ := marks(); shown != 2 {
		t.Fatalf("previous from the first: shown in %d", shown)
	}
	corner := a.transcriptSearch("release", 2)
	if l, ok := corner[0].(ui.Label); !ok || l.Text != "2 of 2" {
		t.Fatalf("count %+v", corner[0])
	}
	a.setTranscriptSearch("zzz")
	if l := a.transcriptSearch("zzz", 0)[0].(ui.Label); l.Text != "No results" {
		t.Fatalf("count %q", l.Text)
	}
	// Pictures: searching at the top, and scrolled with the search stuck
	// to the top of the view.
	a.setTranscriptSearch("noch")
	dir := os.Getenv("FLIT_RENDER_DIR")
	for name, off := range map[string]float32{"window-session-search.png": 300, "window-session-search-stuck.png": 420} {
		img, err := ui.RenderFrame(a.frame(), 960, 640, 1, "", map[string]float32{ui.FrameScrollID(a.page): off})
		if err != nil {
			t.Fatal(err)
		}
		if dir != "" {
			fh, _ := os.Create(filepath.Join(dir, name))
			png.Encode(fh, img)
			fh.Close()
		}
	}
	// Another page forgets the search.
	a.go_(pageRecordings)
	if a.tsQuery != "" {
		t.Fatal("search kept on another page")
	}
}

func TestDebugCores(t *testing.T) {
	a := testApp(t)
	a.dbg.loaded, a.dbg.cores = true, []float64{0.95, 0.6, 0.3}
	if a.debugCores() != nil {
		t.Fatal("cores shown with nothing being transcribed")
	}
	// Transcribing as it goes, with 6 threads, three of them busy now:
	// six squares, the busy ones first.
	a.live, a.dbg.threads = &liveJob{}, 6
	got := a.debugCores()
	if len(got) != 6 || got[0] != 0.95 || got[2] != 0.3 || got[5] != 0 {
		t.Fatalf("cores %v", got)
	}
	// Between its chunks: the squares stay, empty.
	a.dbg.cores = nil
	if got := a.debugCores(); len(got) != 6 || got[0] != 0 {
		t.Fatalf("idle live worker: %v", got)
	}
	// Never more than eight.
	a.live, a.dbg.threads = nil, 0
	a.working = a.sessions[0]
	a.dbg.cores = []float64{1, 1, 1, 1, 1, 1, 1, 1, 0.9, 0.8, 0.5}
	if got := a.debugCores(); len(got) != 8 {
		t.Fatalf("%d squares", len(got))
	}
	// Pictures: at rest (a 2×2 in full, two columns fading) and hovered.
	f := a.frame() // samples the (absent) engines: set what it would show
	f.Debug = []string{"Parakeet Ultra", "1.12 GB · 46% CPU"}
	f.DebugCores = []float32{0.95, 0.85, 0.7, 0.6, 0.45, 0.3, 0.2, 0.1}
	for _, hover := range []string{"", "debug"} {
		img, err := ui.RenderFrame(f, 960, 640, 1.5, hover, nil)
		if err != nil {
			t.Fatal(err)
		}
		if dir := os.Getenv("FLIT_RENDER_DIR"); dir != "" {
			fh, _ := os.Create(filepath.Join(dir, "window-debug-cores"+hover+".png"))
			png.Encode(fh, img)
			fh.Close()
		}
	}
}

func TestFinalLimit(t *testing.T) {
	a := testApp(t)
	// On by default, at Balanced; off, no limit and no level to choose.
	if a.finalLimit() != 50 || len(a.finalSettings()) != 2 {
		t.Fatalf("limit %d by default, want Balanced (50)", a.finalLimit())
	}
	a.cfg.LimitFinal = false
	if a.finalLimit() != 0 || len(a.finalSettings()) != 1 {
		t.Fatal("limited while off")
	}
	a.cfg.LimitFinal = true
	a.cfg.FinalLoad = "light"
	if a.finalLimit() != 25 {
		t.Fatal("light")
	}
	// The jobs' next worker keeps to the limit; changing it replaces the
	// worker, and off it runs free again.
	j := &jobs{a: a}
	j.limit.Store(int32(a.finalLimit()))
	asr, _ := models.Default(models.RoleASR, "de")
	vad, _ := models.Default(models.RoleVAD, "")
	sup, err := j.supervisor(asr, vad, models.Model{})
	if err != nil {
		t.Skip("no engine here:", err)
	}
	if sup.CPULimit != 25 || !sup.Background || sup.Spec.Threads != capThreads(25) {
		t.Fatalf("limited worker: cap %d, background %v, %d threads", sup.CPULimit, sup.Background, sup.Spec.Threads)
	}
	if again, _ := j.supervisor(asr, vad, models.Model{}); again != sup {
		t.Fatal("replaced without a change")
	}
	j.limit.Store(0)
	free, _ := j.supervisor(asr, vad, models.Model{})
	if free == sup || free.CPULimit != 0 || free.Background || free.Spec.Threads != 0 {
		t.Fatalf("unlimited worker: cap %d, background %v, %d threads", free.CPULimit, free.Background, free.Spec.Threads)
	}
	free.Close()
}

func TestModelDownloads(t *testing.T) {
	a := testApp(t)
	need := a.neededModels()
	if len(need) != 3 || need[0].Role != models.RoleASR {
		t.Fatalf("needed models %+v", need)
	}
	// Nothing on disk in the test's data folder: all waiting.
	for _, n := range a.modelCards(false) {
		if c := n.(ui.ProgressCard); c.Trailing != "Waiting" || c.Value != 0 {
			t.Fatalf("card %+v", c)
		}
	}
	// Two coming down at once: one status, one percentage, the share of
	// all the bytes; each card its own.
	a.downloadProgress(need[0], 300, 1000)
	a.downloadProgress(need[1], 1000, 1000)
	if a.status != "Downloading 2 models" || a.progress < 0.64 || a.progress > 0.66 {
		t.Fatalf("status %q, progress %v", a.status, a.progress)
	}
	if strings.Contains(a.status, "%") {
		t.Fatal("a percentage in the status: shown twice")
	}
	cards := a.modelCards(true)
	if len(cards) != 2 || cards[0].(ui.ProgressCard).Trailing != "30%" || cards[0].(ui.ProgressCard).Value != 0.3 {
		t.Fatalf("active cards %+v", cards)
	}
	if got := a.downloadLine(); got != "Downloading 2 models 65%" {
		t.Fatalf("status line %q", got)
	}
	// Pictures: the welcome page while they come down.
	a.page = pageWelcome
	img, err := ui.RenderFrame(a.frame(), 960, 1000, 1, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if dir := os.Getenv("FLIT_RENDER_DIR"); dir != "" {
		fh, _ := os.Create(filepath.Join(dir, "window-welcome-downloads.png"))
		png.Encode(fh, img)
		fh.Close()
	}
}

func TestTranscribingCard(t *testing.T) {
	a := testApp(t)
	a.working, a.progress = a.sessions[0], 0.42
	c, ok := a.liveCard().(ui.ProgressCard)
	if !ok || c.Value != 0.42 || c.Trailing != "42%" || !strings.Contains(c.Title, "Weekly sync") || !strings.Contains(c.Detail, "50% of the processor") {
		t.Fatalf("card %+v", a.liveCard())
	}
	// Models still coming down come first.
	need := a.neededModels()
	a.downloadProgress(need[0], 100, 1000)
	if _, ok := a.liveCard().(ui.VStack); !ok {
		t.Fatal("downloads not shown before the transcription")
	}
	a.downloadEnded(need[0], nil)
	a.page = pageHome
	img, err := ui.RenderFrame(a.frame(), 960, 640, 1, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if dir := os.Getenv("FLIT_RENDER_DIR"); dir != "" {
		fh, _ := os.Create(filepath.Join(dir, "window-home-transcribing.png"))
		png.Encode(fh, img)
		fh.Close()
	}
}

// rowIDs are the recording rows' IDs on a page (in its cards).
func rowIDs(n ui.Node) []string {
	var out []string
	var walk func(ui.Node)
	walk = func(n ui.Node) {
		switch v := n.(type) {
		case ui.VStack:
			for _, it := range v.Items {
				walk(it)
			}
		case ui.Card:
			for _, it := range v.Items {
				walk(it)
			}
		case ui.RecordingRow:
			out = append(out, v.ID)
		}
	}
	walk(n)
	return out
}

func TestRecordingNotListedWhileRecording(t *testing.T) {
	a := testApp(t)
	rec := a.sessions[0]
	a.rec = &recording{sess: rec, r: &capture.Recording{}, started: time.Now()}
	for name, page := range map[string]ui.Node{"home": a.homePage(), "recordings": a.recordingsPage()} {
		rows := strings.Join(rowIDs(page), ",")
		if rows == "" || strings.Contains(rows, rec.Meta.ID) {
			t.Fatalf("%s lists %q while %s records", name, rows, rec.Meta.ID)
		}
	}
	a.setSearch(sessionTitle(rec))
	if found, _ := a.searchSessions(); len(found) != 0 {
		t.Fatal("search finds the recording in progress")
	}
	// The recordings page counts only what it lists.
	a.setSearch("")
	p := a.recordingsPage().(ui.VStack)
	if para, ok := p.Items[1].(ui.Para); !ok || !strings.HasPrefix(para.Text, fmt.Sprintf("%d meetings", len(a.sessions)-1)) {
		t.Fatalf("count line %+v", p.Items[1])
	}
	// Stopped (being transcribed): listed again.
	a.rec, a.working = nil, rec
	if rows := strings.Join(rowIDs(a.recordingsPage()), ","); !strings.Contains(rows, rec.Meta.ID) {
		t.Fatal("not listed once the recording stopped")
	}
}

func TestUninstallPopover(t *testing.T) {
	a := testApp(t)
	a.go_(pageSettings)
	a.askUninstall()
	if a.dataSize <= 0 {
		t.Fatal("data folder not measured")
	}
	dir := os.Getenv("FLIT_RENDER_DIR")
	for _, wipe := range []bool{false, true} {
		a.uninstallWipe = wipe
		f := a.frame()
		if f.Modal == nil {
			t.Fatal("no popover")
		}
		img, err := ui.RenderFrame(f, 960, 640, 1, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		if dir != "" {
			name := "window-uninstall-keep.png"
			if wipe {
				name = "window-uninstall-delete.png"
			}
			fh, _ := os.Create(filepath.Join(dir, name))
			png.Encode(fh, img)
			fh.Close()
		}
	}
	// Esc (or Cancel) closes it, and so does leaving the page.
	a.closePopover()
	if a.uninstallAsk || a.frame().Modal != nil {
		t.Fatal("still open after Esc")
	}
	a.askUninstall()
	a.go_(pageHome)
	if a.uninstallAsk {
		t.Fatal("still open on another page")
	}
}

func TestUninstallArgs(t *testing.T) {
	if c, w := uninstallArgs(nil); c || w {
		t.Fatal("from Installed apps: ask, and keep the data")
	}
	if c, w := uninstallArgs([]string{"--confirmed", "--delete-data"}); !c || !w {
		t.Fatal("flags not read")
	}
}

func TestRemovableDataDir(t *testing.T) {
	flit := t.TempDir()
	os.WriteFile(filepath.Join(flit, "config.json"), []byte("{}"), 0o644)
	home, _ := os.UserHomeDir()
	for dir, want := range map[string]bool{
		flit:          true,
		t.TempDir():   false, // nothing of flit's in it
		`C:\`:         false,
		home:          false,
		"relative\\x": false,
		"":            false,
	} {
		if got := removableDataDir(dir); got != want {
			t.Errorf("removableDataDir(%q) = %v, want %v", dir, got, want)
		}
	}
}

func TestLiveSettings(t *testing.T) {
	a := testApp(t)
	if !a.cfg.LiveTranscribe || a.cfg.LiveLoad != "balanced" {
		t.Fatal("live transcription isn't on, at Balanced, by default")
	}
	if n := len(a.liveSettings()); n != 2 {
		t.Fatalf("%d nodes while on", n)
	}
	a.cfg.LiveTranscribe = false
	if n := len(a.liveSettings()); n != 1 {
		t.Fatalf("%d nodes while off", n)
	}
	a.cfg.LiveTranscribe = true
	if p, name := liveLoad(""); p != 25 || name != "Balanced" {
		t.Fatalf("default load %d %s", p, name)
	}
	if capThreads(1) != 1 || capThreads(100) != runtime.NumCPU() {
		t.Fatal("threads")
	}
	// Without the models (or jobs), a recording simply isn't transcribed live.
	a.startLive(a.sessions[0])
	if a.live != nil {
		t.Fatal("live started without models")
	}
	a.page = pageSettings
	img, err := ui.RenderFrame(a.frame(), 960, 700, 1, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if dir := os.Getenv("FLIT_RENDER_DIR"); dir != "" {
		fh, _ := os.Create(filepath.Join(dir, "settings-live.png"))
		png.Encode(fh, img)
		fh.Close()
	}
}
