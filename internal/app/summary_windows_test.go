package app

import (
	"strings"
	"testing"
	"time"

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

	// Written: the editor, Copy, Write again.
	if err := s.SaveSummary("## Zusammenfassung\n\nRelease am Freitag.\n", session.SummaryInfo{Model: "qwen3.5-2b"}); err != nil {
		t.Fatal(err)
	}
	delete(a.summaries, id) // as summaryEnded does
	c, _ = ui.LayoutFrame(a.frame(), 960, 2400, 1, "")
	if _, ok := c.EditorTarget("summary:" + id); !ok {
		t.Error("the summary isn't shown in an editor")
	}
	if _, ok := c.HitByID("summary:md:" + id); !ok {
		t.Error("no Copy link")
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
