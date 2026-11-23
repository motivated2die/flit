package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/motivated2die/flit/internal/calendar"
	"github.com/motivated2die/flit/internal/models"
	"github.com/motivated2die/flit/internal/session"
	"github.com/motivated2die/flit/internal/ui"
)

// A finished recording's page offers a summary with a field for an extra
// instruction; a written one shows in the editor, with Copy and Write again.
func TestSummarySection(t *testing.T) {
	a := testApp(t)
	var s *session.Session
	for _, x := range a.sessions {
		if x.Meta.Title == "Weekly sync" {
			s = x
		}
	}
	id := s.Meta.ID
	a.page = pageSession + id
	c, err := ui.LayoutFrame(a.frame(), 960, 2400, 1, "")
	if err != nil {
		t.Skip("no Direct2D:", err)
	}
	if _, ok := c.HitByID("summary:write"); !ok {
		t.Fatal("no Write summary button")
	}
	a.setSumInstr(id, "kürzer")
	if a.sumInstr[id] != "kürzer" {
		t.Error("the instruction wasn't kept")
	}

	// While another recording's summary is written, this one waits.
	a.sumRun = &summaryRun{id: "other", step: "read", done: 1, total: 3, cancel: func() {}, started: time.Now()}
	if b := findButton(a.summarySection(s), "summary:write"); b == nil || !b.Disabled {
		t.Error("Write summary isn't disabled while another runs")
	}
	// Its own run shows progress and Cancel.
	a.sumRun.id = id
	c, _ = ui.LayoutFrame(a.frame(), 960, 2400, 1, "")
	if _, ok := c.HitByID("summary:cancel"); !ok {
		t.Error("no Cancel while writing")
	}
	if got := a.sumRun.text(); !strings.Contains(got, "part 2 of 3") {
		t.Errorf("progress %q", got)
	}
	a.cancelSummary()
	if a.sumRun != nil {
		t.Error("cancel kept the run")
	}

	// Top Copy button is disabled before a summary exists, and neither Copy as
	// Markdown nor Transcribe again live in the top action bar anymore.
	if b := findButton([]ui.Node{a.sessionActions(s)}, "session:copy"); b == nil || !b.Disabled {
		t.Errorf("top Copy button should exist and be disabled before summary: %+v", b)
	}
	if b := findButton([]ui.Node{a.sessionActions(s)}, "session:md"); b != nil {
		t.Error("top bar should not have Copy as Markdown")
	}
	if b := findButton([]ui.Node{a.sessionActions(s)}, "session:again"); b != nil {
		t.Error("top bar should not have Transcribe again")
	}

	// Written: the editor, Copy & Copy as text, Write again, top Copy enabled,
	// and Transcript caption has Transcribe again + Copy links.
	if err := s.SaveSummary("## Zusammenfassung\n\nRelease am Freitag.\n", session.SummaryInfo{Model: "qwen3.5-2b"}); err != nil {
		t.Fatal(err)
	}
	delete(a.summaries, id) // as summaryEnded does
	c, _ = ui.LayoutFrame(a.frame(), 960, 2400, 1, "")
	if b := findButton([]ui.Node{a.sessionActions(s)}, "session:copy"); b == nil || b.Disabled || !b.Primary {
		t.Errorf("top Copy button should be enabled and primary once summary exists: %+v", b)
	}
	if _, ok := c.EditorTarget("summary:" + id); !ok {
		t.Error("the summary isn't shown in an editor")
	}
	if _, ok := c.HitByID("summary:md:" + id); !ok {
		t.Error("no Summary Copy link")
	}
	if _, ok := c.HitByID("summary:text:" + id); !ok {
		t.Error("no Summary Copy as text link")
	}
	if _, ok := c.HitByID("session:again"); !ok {
		t.Error("no Transcribe again link on Transcript section")
	}
	if _, ok := c.HitByID("transcript:copy:" + id); !ok {
		t.Error("no Transcript Copy link")
	}
	if _, ok := c.HitByID("transcript:md:" + id); !ok {
		t.Error("no Transcript Copy as Markdown link")
	}
	if b := findButton(a.summarySection(s), "summary:write"); b == nil || b.Label != "Write again" {
		t.Error("no Write again")
	}
}

// The user's name joins the speech model's vocabulary, once.
func TestVocabularyHasYourName(t *testing.T) {
	a := testApp(t)
	a.cfg.Vocabulary = []string{"flit", "Haisch"}
	a.cfg.YourName = "Moritz  Haisch"
	got := strings.Join(a.vocabulary(), ",")
	if got != "flit,Haisch,Moritz" {
		t.Errorf("vocabulary %q", got)
	}
	if len(a.cfg.Vocabulary) != 2 {
		t.Error("the setting itself changed")
	}

	// Split FirstName and LastName inject separately as well.
	a.setName("Ada", "Lovelace")
	got = strings.Join(a.vocabulary(), ",")
	if got != "flit,Haisch,Ada,Lovelace" {
		t.Errorf("split name vocabulary %q", got)
	}
}

func TestWelcomeNameHomeGreetingAndSettings(t *testing.T) {
	a := testApp(t)
	a.cfg.SetupComplete = false
	a.page = pageWelcome
	welcome := a.welcomePage()
	if !nodeHasField(welcome, "welcome:name:first") {
		t.Error("Welcome page missing first name field")
	}
	if !nodeHasField(welcome, "welcome:name:last") {
		t.Error("Welcome page missing last name field")
	}

	// Home greeting uses first name, else last name.
	a.cfg.SetupComplete = true
	a.page = pageHome
	a.setName("Moritz", "Haisch")
	if !nodeHasText(a.homePage(), "Welcome, Moritz") {
		t.Error("Home missing 'Welcome, Moritz'")
	}
	a.setName("", "Haisch")
	if !nodeHasText(a.homePage(), "Welcome, Haisch") {
		t.Error("Home missing 'Welcome, Haisch' fallback")
	}
	a.setName("", "")
	if nodeHasText(a.homePage(), "Welcome,") {
		t.Error("Home showed Welcome greeting with empty name")
	}

	// Settings has split name fields, no Speech model row, and a calendar confirm button.
	ctx, cancel := context.WithCancel(context.Background())
	svc := &calendar.Service{}
	svc.Start(ctx, "")
	defer func() {
		cancel()
		svc.Wait()
	}()
	a.cal = &calendarState{svc: svc, cancel: cancel}
	a.page = pageSettings
	page := a.settingsPage()
	if nodeHasText(page, "Speech model") {
		t.Error("Settings still shows Speech model row")
	}
	if !nodeHasField(page, "set:name:first") {
		t.Error("Settings missing set:name:first")
	}
	if !nodeHasField(page, "set:name:last") {
		t.Error("Settings missing set:name:last")
	}
	c, err := ui.LayoutFrame(a.frame(), 960, 2400, 1, "")
	if err != nil {
		t.Skip("no Direct2D:", err)
	}
	apply, ok := c.HitByID("set:calapply")
	if !ok {
		t.Fatal("Settings missing calendar confirm button set:calapply")
	}
	a.cal.draft, a.cal.draftSet = "https://example.com/cal.ics", true
	apply.OnClick()
	if a.cfg.CalendarURL != "https://example.com/cal.ics" {
		t.Errorf("calendar confirm did not save URL: %q", a.cfg.CalendarURL)
	}
}

func TestDebugLinesActiveTaskModel(t *testing.T) {
	a := testApp(t)
	// Idle: ASR model.
	lines := a.debugLines()
	if !strings.Contains(lines[0], "Parakeet") {
		t.Fatalf("idle debugLines=%q", lines)
	}
	// Finding speakers: Sortformer diarization model.
	a.finding = a.sessions[0]
	a.dbg.at = time.Time{}
	lines = a.debugLines()
	if !strings.Contains(lines[0], "Sortformer") {
		t.Fatalf("finding speakers debugLines=%q", lines)
	}
	a.finding = nil
	// Transcribing in speaker stage (>= 80% with speakers on): Sortformer.
	a.working = a.sessions[0]
	a.progress = 0.80
	a.dbg.at = time.Time{}
	lines = a.debugLines()
	if !strings.Contains(lines[0], "Sortformer") {
		t.Fatalf("diarization stage debugLines=%q", lines)
	}
	a.working = nil
	// Summary starting (loading/downloading): summary LLM model.
	a.sumRun = &summaryRun{id: a.sessions[0].Meta.ID, model: models.Model{Name: "Qwen3.5 4B"}, step: "load"}
	a.dbg.at = time.Time{}
	lines = a.debugLines()
	if lines[0] != "Qwen3.5 4B" || lines[1] != "Loading…" {
		t.Fatalf("summary loading debugLines=%q", lines)
	}
}

func nodeHasField(n ui.Node, id string) bool {
	switch v := n.(type) {
	case ui.TextField:
		return v.ID == id
	case ui.SettingRow:
		return nodeHasField(v.Control, id)
	case ui.VStack:
		for _, it := range v.Items {
			if nodeHasField(it, id) {
				return true
			}
		}
	case ui.HStack:
		for _, it := range v.Items {
			if nodeHasField(it, id) {
				return true
			}
		}
	case ui.Card:
		for _, it := range v.Items {
			if nodeHasField(it, id) {
				return true
			}
		}
	case ui.Group:
		for _, it := range v {
			if nodeHasField(it, id) {
				return true
			}
		}
	}
	return false
}

func nodeHasText(n ui.Node, sub string) bool {
	switch v := n.(type) {
	case ui.Title:
		return strings.Contains(string(v), sub)
	case ui.Label:
		return strings.Contains(v.Text, sub)
	case ui.Para:
		return strings.Contains(v.Text, sub)
	case ui.SettingRow:
		return strings.Contains(v.Label, sub) || strings.Contains(v.Detail, sub)
	case ui.VStack:
		for _, it := range v.Items {
			if nodeHasText(it, sub) {
				return true
			}
		}
	case ui.HStack:
		for _, it := range v.Items {
			if nodeHasText(it, sub) {
				return true
			}
		}
	case ui.Card:
		for _, it := range v.Items {
			if nodeHasText(it, sub) {
				return true
			}
		}
	case ui.Group:
		for _, it := range v {
			if nodeHasText(it, sub) {
				return true
			}
		}
	}
	return false
}

// findButton finds a button by ID in a tree of nodes.
func findButton(nodes []ui.Node, id string) *ui.Button {
	for _, n := range nodes {
		switch v := n.(type) {
		case ui.Button:
			if v.ID == id {
				return &v
			}
		case ui.Card:
			if b := findButton(v.Items, id); b != nil {
				return b
			}
		case ui.HStack:
			if b := findButton(v.Items, id); b != nil {
				return b
			}
		case ui.VStack:
			if b := findButton(v.Items, id); b != nil {
				return b
			}
		}
	}
	return nil
}
