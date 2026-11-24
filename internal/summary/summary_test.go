package summary

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/motivated2die/flit/internal/llm"
	"github.com/motivated2die/flit/internal/notes"
	"github.com/motivated2die/flit/internal/transcript"
)

func sec(s int) time.Duration { return time.Duration(s) * time.Second }

func seg(track transcript.Track, speaker, from, to int, text string) transcript.Segment {
	return transcript.Segment{Track: track, Speaker: speaker, Start: sec(from), End: sec(to), Text: text}
}

// A short German meeting: Anna proposes the budget, Me asks about it.
func meeting() Input {
	segs := []transcript.Segment{
		seg(transcript.Me, 0, 0, 5, "Hallo zusammen, wir fangen mit dem Budget an."),
		seg(transcript.Them, 1, 6, 20, "Ich schlage vor, dass wir das Budget auf 25.000 Euro festlegen, das ist für die Messe."),
		seg(transcript.Them, 2, 21, 30, "Das ist mir zu viel, wir haben nur 20.000 Euro."),
		seg(transcript.Me, 0, 40, 50, "Dann klären wir das bis Freitag. Moritz schickt die Zahlen."),
		seg(transcript.Them, 1, 200, 210, "Zum Schluss noch das Design, das übernimmt Francina."),
	}
	d := notes.Parse("## Budget\n\n- 25.000 Euro Messe?\n- [@flit 0:55: wurde das Budget beschlossen?]\n\n## Design\n\n- Francina\n")
	d.Blocks[1].Typed = sec(22)
	d.Blocks[2].Typed = sec(55)
	d.Blocks[4].Typed = sec(212)
	return Input{Title: "Planung", Segments: segs, Names: transcript.Names{Speakers: map[int]string{1: "Anna", 2: "Ben"}}, Notes: d, Me: "Moritz", MeNames: []string{"Moritz", "Haisch"}, Language: "en"}
}

func TestLanguage(t *testing.T) {
	in := meeting()
	if got := Language(in.Turns(), "en"); got != "de" {
		t.Errorf("German meeting read as %q", got)
	}
	en := []Turn{{Text: "So we have to decide this by Friday, and the budget is not what we want it to be."}}
	if got := Language(en, "de"); got != "en" {
		t.Errorf("English read as %q", got)
	}
	if got := Language([]Turn{{Text: "Okay."}}, "de"); got != "de" {
		t.Errorf("too few words should fall back: %q", got)
	}
}

func TestCues(t *testing.T) {
	in := meeting()
	ts := in.Turns()
	qs, ns := Cues(in, ts)
	if len(qs) != 1 || qs[0].At != sec(55) || qs[0].Text != "wurde das Budget beschlossen?" {
		t.Fatalf("questions %+v", qs)
	}
	marks := func(i int) string { return strings.Join(ts[i].Marks, ",") }
	// The question's window covers the first minutes, not the design at 3:20.
	for i := 0; i < 4; i++ {
		if !strings.Contains(marks(i), "Q1") {
			t.Errorf("turn %d not in the question's window: %q", i, marks(i))
		}
	}
	if strings.Contains(marks(4), "Q1") {
		t.Error("the design turn is in the question's window")
	}
	// "25.000 Euro Messe?" typed at 0:22 points at Anna's proposal, which
	// shares its words, not at Ben's reply, which is closer.
	var budget, francina Note
	for _, n := range ns {
		switch n.Text {
		case "25.000 Euro Messe?":
			budget = n
		case "Francina":
			francina = n
		}
	}
	if budget.Turn != 1 || francina.Turn != 4 {
		t.Errorf("anchors: budget %d, Francina %d", budget.Turn, francina.Turn)
	}
	// Moritz says his own name: not a mention of the user by others.
	for i, tr := range ts {
		if strings.Contains(marks(i), "@you") {
			t.Errorf("turn %d (%s) marked as naming the user", i, tr.Who)
		}
	}
}

func TestSplit(t *testing.T) {
	var ts []Turn
	at := 0
	for i := range 30 {
		gap := 1
		if i == 12 {
			gap = 20 // the longest pause, past two thirds of the first section
		}
		at += gap
		ts = append(ts, Turn{Start: sec(at), End: sec(at + 5), Text: "x"})
		at += 5
	}
	ss := Split(ts, func(Turn) int { return 100 }, 1500)
	if len(ss) < 2 || ss[0].To != 12 {
		t.Fatalf("sections %+v", ss)
	}
	n := 0
	for i, s := range ss {
		n += s.To - s.From
		if i > 0 && s.From != ss[i-1].To {
			t.Error("sections don't follow each other")
		}
		if (s.To-s.From)*100 > 1500+100 {
			t.Errorf("section %d too long: %d turns", i, s.To-s.From)
		}
	}
	if n != len(ts) {
		t.Errorf("sections cover %d of %d turns", n, len(ts))
	}
}

func TestFindQuote(t *testing.T) {
	ts := meeting().Turns()
	s := section(ts, 0, len(ts))
	if at := findQuote(ts, s, "das Budget auf 25.000 Euro festlegen"); at != sec(6) {
		t.Errorf("exact quote at %v", at)
	}
	if at := findQuote(ts, s, "Budget auf 25.000 Euro festsetzen [N1]"); at != sec(6) {
		t.Errorf("one word off, with a cue mark: %v", at)
	}
	if at := findQuote(ts, s, "wir verdoppeln das Budget nächstes Jahr"); at != -1 {
		t.Errorf("invented quote found at %v", at)
	}
}

func TestTidy(t *testing.T) {
	got := Tidy("Hier sind die Notizen:\n\n```markdown\n# Planung\n\n## Budget\n\n- 25.000 Euro [N1]\n#### Detail\n- mehr\n```\n\nIch hoffe, das hilft!")
	want := "## Budget\n\n- 25.000 Euro\n\n### Detail\n\n- mehr"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	if got := Tidy("- **** Sie ist da.\n  - **** **** Mut\n- **a** **b**"); got != "- Sie ist da.\n  - Mut\n- **a** **b**" {
		t.Errorf("empty bold: %q", got)
	}
	if got := Tidy("- - [ ] **Ben:** a\n- - b"); got != "- [ ] **Ben:** a\n- b" {
		t.Errorf("double markers: %q", got)
	}
	if got := Tidy("## <Sprache>\n\n- a"); got != "## Sprache\n\n- a" {
		t.Errorf("placeholder heading: %q", got)
	}
}

func TestAssemble(t *testing.T) {
	w := wordsFor("de")
	md := "## Zusammenfassung\n\nKurz.\n\n## Budget\n\n- 25.000 Euro\n\n## Entscheidungen\n\n- Es wurden keine Entscheidungen getroffen.\n\n## Aufgaben\n\n- [ ] **Moritz:** Zahlen schicken\n\n## Offene Fragen\n\n- Keine.\n"
	got := Assemble(md, []Answer{{Q: Question{N: 1, Text: "Beschlossen?"}, Text: "Nein, nur vorgeschlagen."}}, w)
	want := "## Zusammenfassung\n\nKurz.\n\n## Budget\n\n- 25.000 Euro\n\n## Deine Fragen\n\n- **Beschlossen?** Nein, nur vorgeschlagen.\n\n## Aufgaben\n\n- [ ] **Moritz:** Zahlen schicken"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	got = Assemble("## Aufgaben\n\n- **Ben:** Angebot holen\n  - bis Freitag\n", nil, w)
	if got != "## Aufgaben\n\n- [ ] **Ben:** Angebot holen\n  - bis Freitag" {
		t.Errorf("tasks as bullets: %q", got)
	}
}

// A question written after a long meeting (no time) reads the part that
// shares its words, not the whole transcript.
func TestAskWithoutTime(t *testing.T) {
	var ts []Turn
	for i := range 400 {
		text := "Wir reden über das Wetter und andere allgemeine Dinge, die nichts zur Sache tun."
		if i == 250 {
			text = "Die Messe in Hannover kostet uns zwölftausend Euro für den Stand."
		}
		ts = append(ts, Turn{Who: "Anna", Start: sec(i * 10), End: sec(i*10 + 9), Text: text})
	}
	f := &fake{}
	a, _, prompt, err := ask(context.Background(), f, Options{}, wordsFor("de"), "", ts, Question{N: 1, At: notes.Unknown, Text: "Was kostet die Messe?"}, nil)
	if err != nil || a.Text != "Nein, nur vorgeschlagen." {
		t.Fatalf("answer %+v, %v", a, err)
	}
	if !strings.Contains(prompt, "zwölftausend") || len(prompt) > 8000 {
		t.Errorf("excerpt of %d characters, with the stand: %v", len(prompt), strings.Contains(prompt, "zwölftausend"))
	}
	if strings.Contains(prompt, " at ") && strings.Contains(prompt, "During the meeting at") {
		t.Error("the question was given a time")
	}
	// Nothing in common: not answered, no call.
	n := len(f.reqs)
	a, _, _, _ = ask(context.Background(), f, Options{}, wordsFor("de"), "", ts, Question{N: 2, At: notes.Unknown, Text: "Urlaubsplanung?"}, nil)
	if a.Text != wordsFor("de").NotDiscussed || len(f.reqs) != n {
		t.Errorf("unrelated question: %+v", a)
	}
}

// fake answers like a model: JSON for a schema, Markdown otherwise.
type fake struct{ reqs []llm.Request }

func (f *fake) Tokens(_ context.Context, s string) (int, error) { return len(s) / 4, nil }

func (f *fake) Chat(_ context.Context, r llm.Request) (llm.Response, error) {
	f.reqs = append(f.reqs, r)
	prompt := r.Messages[len(r.Messages)-1].Content
	if r.OnProgress != nil { // as llama-server streams: reading, then writing
		total := len(prompt) / 4
		r.OnProgress(llm.CallProgress{Read: 0, ReadTotal: total})
		r.OnProgress(llm.CallProgress{Read: total / 2, ReadTotal: total})
		r.OnProgress(llm.CallProgress{Read: total, ReadTotal: total, Wrote: 20})
		r.OnProgress(llm.CallProgress{Read: total, ReadTotal: total, Wrote: 900}) // more than expected
	}
	switch {
	case r.Schema != nil && strings.Contains(prompt, "Classify this meeting"):
		return llm.Response{Text: `{"type":"decision_planning","density":"medium"}`}, nil
	case r.Schema != nil:
		return llm.Response{Text: `{"speakers":[{"label":"Them 1","name":"Anna"}],"topics":[{"title":"Budget","points":["Anna schlug 25.000 Euro vor"]}],"decisions":[{"text":"Budget beschlossen","quote":"das hat niemand gesagt"}],"tasks":[{"who":"Moritz","what":"die Zahlen schicken","due":"Freitag","quote":"Moritz schickt die Zahlen"}],"open":[]}`}, nil
	case strings.Contains(prompt, "wrote this question"):
		return llm.Response{Text: "Nein, nur vorgeschlagen."}, nil
	default:
		return llm.Response{Text: "## Budget\n\n- **Anna** schlug 25.000 Euro vor.\n\n## Aufgaben\n\n- [ ] **Moritz:** Zahlen schicken, bis Freitag\n"}, nil
	}
}

// The progress of a run never goes back, names each step in order, and
// ends at 1; every call's reading and writing moves it.
func TestProgress(t *testing.T) {
	for _, ap := range []Approach{Single, Extract} {
		var seen []Progress
		_, err := Run(context.Background(), &fake{}, meeting(), Options{Approach: ap, Progress: func(p Progress) { seen = append(seen, p) }})
		if err != nil {
			t.Fatal(err)
		}
		last, steps := -1.0, []string{}
		for _, p := range seen {
			if p.Fraction < last {
				t.Errorf("%s: went back from %.3f to %.3f at %s", ap, last, p.Fraction, p.Step)
			}
			last = p.Fraction
			if len(steps) == 0 || steps[len(steps)-1] != p.Step {
				steps = append(steps, p.Step)
			}
		}
		want := "classify,ask,write"
		if ap == Extract {
			want = "classify,ask,read,write"
		}
		if got := strings.Join(steps, ","); got != want {
			t.Errorf("%s: steps %s, want %s", ap, got, want)
		}
		if last != 1 {
			t.Errorf("%s: ended at %.3f", ap, last)
		}
		// The question is a small part next to the writer's reading the
		// whole meeting; the writer's writing is a large part of its share.
		var askEnd, writeStart float64
		for _, p := range seen {
			if p.Step == "ask" {
				askEnd = p.Fraction
			}
			if p.Step == "write" && writeStart == 0 {
				writeStart = p.Fraction
			}
		}
		if askEnd <= 0 || askEnd > writeStart || writeStart > 0.9 {
			t.Errorf("%s: ask ends at %.2f, write starts at %.2f", ap, askEnd, writeStart)
		}
	}
}

func TestRun(t *testing.T) {
	for _, ap := range []Approach{Single, Extract} {
		f := &fake{}
		in := meeting()
		in.Instruction = "Bitte sehr kurz."
		r, err := Run(context.Background(), f, in, Options{Approach: ap})
		if err != nil {
			t.Fatal(ap, err)
		}
		if r.Language != "de" || len(r.Answers) != 1 {
			t.Errorf("%s: language %q, answers %d", ap, r.Language, len(r.Answers))
		}
		if !strings.Contains(r.Markdown, "## Deine Fragen\n\n- **wurde das Budget beschlossen?** Nein, nur vorgeschlagen.\n\n## Aufgaben") {
			t.Errorf("%s:\n%s", ap, r.Markdown)
		}
		calls := 3 // classify, the question, the writer
		if ap == Extract {
			calls = 4
			// The decision quoted with words nobody said is dropped; the
			// task, quoted right, stays.
			if r.Dropped != 1 || len(r.Items) != 1 || len(r.Items[0].Tasks) != 1 {
				t.Errorf("checks: dropped %d, items %+v", r.Dropped, r.Items)
			}
		}
		if r.Class.Type != "decision_planning" {
			t.Errorf("%s: class %+v", ap, r.Class)
		}
		if len(f.reqs) != calls {
			t.Errorf("%s: %d calls", ap, len(f.reqs))
		}
		writer := f.reqs[len(f.reqs)-1].Messages[1].Content
		for _, want := range []string{"Bitte sehr kurz.", "<live_notes>", "## Budget", "[N1] 25.000 Euro Messe?", "Write the meeting notes in German", "Meeting style (planning / decision)"} {
			if !strings.Contains(writer, want) {
				t.Errorf("%s: the writer's prompt lacks %q", ap, want)
			}
		}
		if ap == Extract && !strings.Contains(writer, "Them 1 = Anna") {
			t.Errorf("extract writer lacks speaker glossary")
		}
		if strings.Contains(writer, "@flit") || strings.Contains(writer, "[Q1]") {
			t.Errorf("%s: the writer sees the question", ap)
		}
		ask := f.reqs[1].Messages[1].Content
		if !strings.Contains(ask, "25.000 Euro") || strings.Contains(ask, "Francina") {
			t.Errorf("the question's excerpt is wrong:\n%s", ask)
		}
	}
}

func TestMissedInfoCueAndUnanchoredNotes(t *testing.T) {
	if !isMissedInfoCue("ich habe kurz nicht aufgepasst, was war das?") {
		t.Error("expected German missed-info cue to match")
	}
	if !isMissedInfoCue("missed this part???") {
		t.Error("expected English missed-info cue to match")
	}
	if isMissedInfoCue("what is the budget?") {
		t.Error("normal question falsely matched missed-info cue")
	}
	// Pre-meeting (untimed) notes must not receive orphan [N#] marks.
	d := notes.Parse("- Agenda item\n- Live point\n")
	d.Blocks[0].Typed = notes.Unknown
	d.Blocks[1].Typed = sec(22)
	in := meeting()
	in.Notes = d
	ts := in.Turns()
	_, ns := Cues(in, ts)
	nb := notesBlock(d, ns)
	if !strings.Contains(nb, "<pre_meeting_notes>\n- Agenda item") {
		t.Errorf("pre-meeting note not separated or got orphan mark:\n%s", nb)
	}
	if !strings.Contains(nb, "<live_notes>\n- [N1] Live point") {
		t.Errorf("live note not anchored as [N1]:\n%s", nb)
	}
	// Spelling hint extracts acronyms and mixed-case/alphanumeric terms.
	hint := spellingHint("DIN33402 Review", notes.Parse("- IPCC report & RAMSIS model\n- regular words\n"))
	for _, want := range []string{"DIN33402", "IPCC", "RAMSIS"} {
		if !strings.Contains(hint, want) {
			t.Errorf("spellingHint missing %q: %q", want, hint)
		}
	}
	if strings.Contains(hint, "regular") {
		t.Errorf("spellingHint included common word: %q", hint)
	}
}
