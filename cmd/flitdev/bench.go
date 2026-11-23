package main

import (
	"archive/zip"
	"context"
	"encoding/csv"
	"encoding/json"
	"encoding/xml"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/motivated2die/flit/internal/llm"
	"github.com/motivated2die/flit/internal/models"
	"github.com/motivated2die/flit/internal/notes"
	"github.com/motivated2die/flit/internal/summary"
	"github.com/motivated2die/flit/internal/transcript"
)

type benchCase struct {
	ID          string
	Language    string
	ExpectedTyp string // expected meeting type for classifier eval
	NoteProfile string // none, sparse, structured
	Approach    summary.Approach
	Input       summary.Input
	GoldSummary string
	GoldActions string
	GoldDecs    string
}

type benchMetrics struct {
	CaseID          string  `json:"case_id"`
	Seed            int     `json:"seed"`
	Approach        string  `json:"approach"`
	MeetingType     string  `json:"meeting_type,omitempty"`
	WallS           float64 `json:"wall_s"`
	ReadTokens      int     `json:"read_tokens"`
	WroteTokens     int     `json:"wrote_tokens"`
	Words           int     `json:"words"`
	H2Count         int     `json:"h2_count"`
	BulletCount     int     `json:"bullet_count"`
	AvgBulletWords  float64 `json:"avg_bullet_words"`
	AttributedPct   float64 `json:"attributed_pct"`
	TaskCount       int     `json:"task_count"`
	HasSummaryH2    bool    `json:"has_summary_h2"`
	DuplicateTasks  bool    `json:"duplicate_tasks"`
	GenericHeadings int     `json:"generic_headings"`
	FillerStarts    int     `json:"filler_starts"`
	LeakedMarks     int     `json:"leaked_marks"`
	RawThemLabels   int     `json:"raw_them_labels"`
	QuestionsTotal  int     `json:"questions_total"`
	QuestionsAns    int     `json:"questions_answered"`
	NumbersTotal    int     `json:"numbers_total"`
	NumbersGrounded int     `json:"numbers_grounded"`
}

func benchCmd(ctx context.Context, root string, args []string) error {
	fs := flag.NewFlagSet("bench", flag.ExitOnError)
	modelID := fs.String("model", "qwen3.5-4b", "language model ID")
	deviceStr := fs.String("device", "gpu", "cpu, gpu-prefill, gpu")
	seedsStr := fs.String("seeds", "42,43", "comma-separated sampling seeds")
	tag := fs.String("tag", "run", "output folder tag under .cache/bench/")
	only := fs.String("only", "", "optional substring filter on case ID")
	ctxSize := fs.Int("ctx", 20480, "context size for llama-server (covers 50-min IB4010)")
	fs.Parse(args)

	m, ok := models.ByID(*modelID)
	if !ok || m.Role != models.RoleLLM {
		return fmt.Errorf("unknown language model %q", *modelID)
	}
	dev, err := llm.ParseDevice(*deviceStr)
	if err != nil {
		return err
	}
	var seeds []int
	for _, s := range strings.Split(*seedsStr, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil {
			return fmt.Errorf("seed %q: %w", s, err)
		}
		seeds = append(seeds, n)
	}
	cases, err := loadBenchCases(root)
	if err != nil {
		return err
	}
	if *only != "" {
		var filt []benchCase
		for _, c := range cases {
			if strings.Contains(c.ID, *only) {
				filt = append(filt, c)
			}
		}
		cases = filt
	}
	if len(cases) == 0 {
		return fmt.Errorf("no benchmark cases matched")
	}

	exe := filepath.Join(runtimeDir(root), llm.ServerExe)
	if _, err := os.Stat(exe); err != nil {
		return fmt.Errorf("no llama-server: run `go run ./cmd/flitdev llm` first")
	}
	outDir := filepath.Join(root, ".cache", "bench", *tag)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	path := m.Path(filepath.Join(root, "models"))
	srv, err, first := llm.StartFallback(ctx, llm.Options{
		Exe: exe, Model: path, Context: *ctxSize, Device: dev,
		Logf: func(string, ...any) {},
	})
	if err != nil {
		return err
	}
	defer srv.Close()
	if first != nil {
		fmt.Printf("  %s failed (%v): fell back to %s\n", dev, first, srv.Options.Device)
		dev = srv.Options.Device
	}
	fmt.Printf("bench %q: model=%s device=%s ctx=%d loaded=%s cases=%d seeds=%v\n",
		*tag, *modelID, dev, *ctxSize, srv.Loaded.Round(100*time.Millisecond), len(cases), seeds)

	var all []benchMetrics
	totalStart := time.Now()
	for _, c := range cases {
		for _, seed := range seeds {
			t0 := time.Now()
			res, err := summary.Run(ctx, &srv.Client, c.Input, summary.Options{
				Approach: c.Approach,
				Seed:     seed,
			})
			wall := time.Since(t0)
			if err != nil {
				fmt.Printf("  FAIL %-28s seed=%d: %v\n", c.ID, seed, err)
				continue
			}
			met := evaluateSummary(c, res, seed, wall)
			all = append(all, met)
			base := fmt.Sprintf("%s_s%d", c.ID, seed)
			_ = os.WriteFile(filepath.Join(outDir, base+".md"), []byte(res.Markdown+"\n"), 0o644)
			var ps strings.Builder
			for i, p := range res.Prompts {
				fmt.Fprintf(&ps, "===== prompt %d =====\n%s\n", i+1, p)
			}
			_ = os.WriteFile(filepath.Join(outDir, base+".prompts.txt"), []byte(ps.String()), 0o644)
			fmt.Printf("  %-28s s=%d %-7s %-17s %5.1fs  %3dw  h2=%d  bul=%2d(%.0fw) attr=%3.0f%%  task=%d  dup=%v gen=%d fill=%d leak=%d them=%d num=%d/%d q=%d/%d\n",
				c.ID, seed, c.Approach, met.MeetingType, met.WallS, met.Words, met.H2Count, met.BulletCount, met.AvgBulletWords,
				met.AttributedPct, met.TaskCount, met.DuplicateTasks, met.GenericHeadings, met.FillerStarts,
				met.LeakedMarks, met.RawThemLabels, met.NumbersGrounded, met.NumbersTotal, met.QuestionsAns, met.QuestionsTotal)
		}
	}
	fmt.Printf("done in %s (%d runs) -> %s\n", time.Since(totalStart).Round(100*time.Millisecond), len(all), outDir)
	b, _ := json.MarshalIndent(all, "", "  ")
	_ = os.WriteFile(filepath.Join(outDir, "metrics.json"), b, 0o644)
	printAggregate(all)
	return nil
}

func printAggregate(all []benchMetrics) {
	if len(all) == 0 {
		return
	}
	var sumWall, sumWords, sumAvgBul, sumAttr float64
	var dupTasks, genHead, fillStarts, leaks, rawThem, qAns, qTot, nGnd, nTot int
	for _, m := range all {
		sumWall += m.WallS
		sumWords += float64(m.Words)
		sumAvgBul += m.AvgBulletWords
		sumAttr += m.AttributedPct
		if m.DuplicateTasks {
			dupTasks++
		}
		genHead += m.GenericHeadings
		fillStarts += m.FillerStarts
		leaks += m.LeakedMarks
		rawThem += m.RawThemLabels
		qAns += m.QuestionsAns
		qTot += m.QuestionsTotal
		nGnd += m.NumbersGrounded
		nTot += m.NumbersTotal
	}
	n := float64(len(all))
	fmt.Printf("\nAGGREGATE (%d runs): avg_wall=%.1fs avg_words=%.0f avg_bul_words=%.1f avg_attr=%.0f%% dup_tasks=%d gen_head=%d filler=%d leaks=%d raw_them=%d q=%d/%d num_grounded=%d/%d\n",
		len(all), sumWall/n, sumWords/n, sumAvgBul/n, sumAttr/n, dupTasks, genHead, fillStarts, leaks, rawThem, qAns, qTot, nGnd, nTot)
}

var (
	reBenchMark   = regexp.MustCompile(`\[(?:Q\d+|N\d+|@you)\]`)
	reBenchRawThm = regexp.MustCompile(`\bThem\s*\d+\b`)
	reBenchNumber = regexp.MustCompile(`\b\d+(?:[.,]\d+)?\b`)
	reBenchFiller = regexp.MustCompile(`(?i)^(?:\*\*[^*]+\*\*:?\s*)?(?:the team discussed|the group discussed|participants discussed|we discussed|das team besprach|die gruppe besprach|es wurde besprochen|es wurde diskutiert|im meeting wurde)`)
)

func evaluateSummary(c benchCase, res summary.Result, seed int, wall time.Duration) benchMetrics {
	tot := res.Total()
	d := notes.Parse(res.Markdown)
	m := benchMetrics{
		CaseID:         c.ID,
		Seed:           seed,
		Approach:       string(c.Approach),
		MeetingType:    res.Class.Type,
		WallS:          wall.Seconds(),
		ReadTokens:     tot.PromptN,
		WroteTokens:    tot.PredictedN,
		Words:          len(strings.Fields(res.Markdown)),
		LeakedMarks:    len(reBenchMark.FindAllString(res.Markdown, -1)),
		RawThemLabels:  len(reBenchRawThm.FindAllString(res.Markdown, -1)),
		QuestionsTotal: len(res.Answers),
	}
	for _, a := range res.Answers {
		if !a.Failed && !strings.Contains(strings.ToLower(a.Text), "nicht beantwortet") && !strings.Contains(strings.ToLower(a.Text), "not answered") {
			m.QuestionsAns++
		}
	}

	// Collect speaker names appearing in the transcript or input names.
	speakerWords := extractSpeakerTokens(c.Input)
	var hasNextSteps, hasTasks bool
	var bulWords, topicBullets, attributedBullets int
	inFixed := false
	for _, b := range d.Blocks {
		plain := strings.TrimSpace(notes.PlainInline(b.Text))
		switch b.Kind {
		case notes.H2:
			m.H2Count++
			low := strings.ToLower(strings.Trim(plain, " :"))
			if low == "summary" || low == "zusammenfassung" || low == "overview" || low == "überblick" {
				m.HasSummaryH2 = true
			}
			if low == "next steps" || low == "nächste schritte" {
				hasNextSteps = true
			}
			if low == "tasks" || low == "aufgaben" || low == "action items" {
				hasTasks = true
			}
			for _, g := range []string{"project overview", "meeting overview", "overview", "discussion", "updates", "general discussion", "überblick", "allgemeines", "diskussion", "einführung", "introduction"} {
				if low == g {
					m.GenericHeadings++
					break
				}
			}
			inFixed = low == "tasks" || low == "aufgaben" || low == "next steps" || low == "nächste schritte" ||
				low == "decisions" || low == "entscheidungen" || low == "open questions" || low == "offene fragen" ||
				low == "your questions" || low == "deine fragen"
		case notes.Task:
			m.TaskCount++
		case notes.Bullet, notes.Numbered:
			m.BulletCount++
			bulWords += len(strings.Fields(plain))
			if reBenchFiller.MatchString(b.Text) {
				m.FillerStarts++
			}
			if !inFixed {
				topicBullets++
				if mentionsSpeaker(plain, speakerWords) {
					attributedBullets++
				}
			}
		}
	}
	m.DuplicateTasks = hasNextSteps && hasTasks
	if m.BulletCount > 0 {
		m.AvgBulletWords = float64(bulWords) / float64(m.BulletCount)
	}
	if topicBullets > 0 {
		m.AttributedPct = 100 * float64(attributedBullets) / float64(topicBullets)
	}

	// Number grounding: check numbers in summary against transcript + notes.
	var src strings.Builder
	for _, s := range c.Input.Segments {
		src.WriteString(s.Text)
		src.WriteByte(' ')
	}
	src.WriteString(c.Input.Notes.Plain())
	srcText := strings.ToLower(src.String())
	seenNum := map[string]bool{}
	for _, num := range reBenchNumber.FindAllString(res.Markdown, -1) {
		if seenNum[num] {
			continue
		}
		seenNum[num] = true
		m.NumbersTotal++
		if strings.Contains(srcText, num) || numberWordInText(num, srcText) {
			m.NumbersGrounded++
		}
	}
	return m
}

func extractSpeakerTokens(in summary.Input) []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || strings.HasPrefix(strings.ToLower(s), "them") || strings.ToLower(s) == "me" {
			return
		}
		for _, w := range strings.Fields(s) {
			w = strings.Trim(w, ",.:;()")
			if len([]rune(w)) >= 3 {
				low := strings.ToLower(w)
				if !seen[low] {
					seen[low] = true
					out = append(out, low)
				}
			}
		}
	}
	add(in.Me)
	for _, n := range in.Names.Speakers {
		add(n)
	}
	// Also common AMI IS1009a / IB4010 participant first names when labels are Them 1..3.
	for _, n := range []string{"betty", "francina", "eileen", "jeanne", "agnes", "project manager", "designer", "marketing", "ergonomist", "anthropologist", "umweltpolitiker", "klimawissenschaftler", "sozialwissenschaftler", "museum", "curator"} {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

func mentionsSpeaker(text string, tokens []string) bool {
	low := strings.ToLower(text)
	for _, t := range tokens {
		if strings.Contains(low, t) {
			return true
		}
	}
	return false
}

var numWords = map[string][]string{
	"25": {"twenty-five", "twenty five", "fünfundzwanzig"},
	"50": {"fifty", "fünfzig"},
	"30": {"thirty", "dreißig"},
	"12": {"twelve", "zwölf"},
	"10": {"ten", "zehn"},
	"5":  {"five", "fünf"},
	"4":  {"four", "vier"},
	"3":  {"three", "drei"},
	"2":  {"two", "zwei"},
	"1":  {"one", "ein", "eine"},
}

func numberWordInText(num, text string) bool {
	for _, w := range numWords[num] {
		if strings.Contains(text, w) {
			return true
		}
	}
	return false
}

// loadBenchCases builds our compact, diverse English + German suite.
func loadBenchCases(root string) ([]benchCase, error) {
	var cases []benchCase

	// 1. German FAME meetings (MIT)
	fameDir := filepath.Join(root, ".cache", "fame")
	fameSpecs := []struct {
		id, file, typ, notesMD, instr string
		times                         map[string]int
	}{
		{
			id:   "fame-de-catchup-sparse",
			file: "Casual_Catch-Up_Anthropology_German_Anthropometrie.csv",
			typ:  "one_on_one",
			notesMD: "- Körpermaße / Ergonomie\n" +
				"- [@flit 6:00: welche konkreten Anwendungsbereiche wurden genannt?]\n" +
				"- **Standardisierung** wichtig\n",
			times: map[string]int{
				"Körpermaße / Ergonomie": 120,
				"[@flit 6:00: welche konkreten Anwendungsbereiche wurden genannt?]": 360,
				"**Standardisierung** wichtig":                                      480,
			},
		},
		{
			id:   "fame-de-crossfunc-sparse",
			file: "Cross-Functional_Meeting_Anthropology_German_Ethnographie.csv",
			typ:  "standup_sync",
			notesMD: "- Ausstellungskonzept + Ethnographie\n" +
				"- Feldarbeit / Geschichten hinter Artefakten\n" +
				"- [@flit 10:00: welche nächsten Schritte wurden vereinbart?]\n",
			times: map[string]int{
				"Ausstellungskonzept + Ethnographie":                         90,
				"Feldarbeit / Geschichten hinter Artefakten":                 240,
				"[@flit 10:00: welche nächsten Schritte wurden vereinbart?]": 600,
			},
		},
		{
			id:   "fame-de-decision-sparse",
			file: "Decision-Making_Meeting_Environment_and_Ecology_German_Klimaanpassung.csv",
			typ:  "decision_planning",
			notesMD: "- IPCC Daten als Basis\n" +
				"- [@flit 7:30: ich habe kurz nicht aufgepasst, was war das Hauptproblem bei der Finanzierung oder Umsetzung?]\n" +
				"- **soziale Auswirkungen** nicht vergessen\n",
			instr: "Fokus auf konkrete Maßnahmen und Verantwortlichkeiten",
			times: map[string]int{
				"IPCC Daten als Basis": 100,
				"[@flit 7:30: ich habe kurz nicht aufgepasst, was war das Hauptproblem bei der Finanzierung oder Umsetzung?]": 450,
				"**soziale Auswirkungen** nicht vergessen": 210,
			},
		},
	}
	for _, sp := range fameSpecs {
		p := filepath.Join(fameDir, sp.file)
		if _, err := os.Stat(p); err != nil {
			continue
		}
		in, gold, err := loadFAME(p, sp.notesMD, sp.times, sp.instr)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", sp.id, err)
		}
		cases = append(cases, benchCase{
			ID: sp.id, Language: "de", ExpectedTyp: sp.typ, NoteProfile: "sparse",
			Approach: summary.Single, Input: in, GoldSummary: gold,
		})
	}

	// 2. AMI English meetings (CC BY 4.0, already in .cache/ami/ami_public_manual_1.6.2.zip)
	amiZip := filepath.Join(root, ".cache", "ami", "ami_public_manual_1.6.2.zip")
	if _, err := os.Stat(amiZip); err == nil {
		// IS1009a (13.4 min) with 3 note profiles: none, sparse (loose + gaps), structured
		is1009aSegs, is1009aNames, abs, acts, decs, err := loadAMIFromZip(amiZip, "IS1009a")
		if err != nil {
			return nil, err
		}
		// Profile 1: none
		cases = append(cases, benchCase{
			ID: "ami-IS1009a-none", Language: "en", ExpectedTyp: "decision_planning", NoteProfile: "none",
			Approach: summary.Single,
			Input: summary.Input{
				Title: "Remote Control Kickoff", Segments: is1009aSegs, Names: is1009aNames, Me: "Betty", Language: "en",
			},
			GoldSummary: abs, GoldActions: acts, GoldDecs: decs,
		})
		// Profile 2: sparse loose jottings + long gap + @flit
		sparseDoc := notes.Parse("- 25 EUR / max 12.50 prod\n- [@flit 6:50: what is the profit aim?]\n- lost remote -> light sensor?\n")
		sparseDoc.Blocks[0].Typed = 395 * time.Second
		sparseDoc.Blocks[1].Typed = 410 * time.Second
		sparseDoc.Blocks[2].Typed = 640 * time.Second
		cases = append(cases, benchCase{
			ID: "ami-IS1009a-sparse", Language: "en", ExpectedTyp: "decision_planning", NoteProfile: "sparse",
			Approach: summary.Single,
			Input: summary.Input{
				Title: "Remote Control Kickoff", Segments: is1009aSegs, Names: is1009aNames, Notes: sparseDoc, Me: "Betty", Language: "en",
			},
			GoldSummary: abs, GoldActions: acts, GoldDecs: decs,
		})
		// Profile 3: structured (existing testdata/notes/ami-IS1009a.md)
		if b, err := os.ReadFile(filepath.Join(root, "testdata", "notes", "ami-IS1009a.md")); err == nil {
			doc := notes.Parse(string(b))
			if tb, err := os.ReadFile(filepath.Join(root, "testdata", "notes", "ami-IS1009a.json")); err == nil {
				var t notes.Times
				if json.Unmarshal(tb, &t) == nil {
					doc.ApplyTimes(t)
				}
			}
			cases = append(cases, benchCase{
				ID: "ami-IS1009a-struct", Language: "en", ExpectedTyp: "decision_planning", NoteProfile: "structured",
				Approach: summary.Single,
				Input: summary.Input{
					Title: "Remote Control Kickoff", Segments: is1009aSegs, Names: is1009aNames, Notes: doc, Me: "Betty", Language: "en",
				},
				GoldSummary: abs, GoldActions: acts, GoldDecs: decs,
			})
		}

		// Long ~49-min meeting IB4010 (film club selection), sparse notes early only (35-min gap!)
		// Tested in both Single (~15k tokens) and Extract mode!
		ibSegs, ibNames, ibAbs, ibActs, ibDecs, err := loadAMIFromZip(amiZip, "IB4010")
		if err != nil {
			return nil, err
		}
		ibNotes := notes.Parse("- film club movie pick\n- **Usual Suspects** vs **Sixth Sense**\n- [@flit 12:00: which movies were proposed early on?]\n")
		ibNotes.Blocks[0].Typed = 90 * time.Second
		ibNotes.Blocks[1].Typed = 300 * time.Second
		ibNotes.Blocks[2].Typed = 720 * time.Second
		cases = append(cases, benchCase{
			ID: "ami-IB4010-49m-single", Language: "en", ExpectedTyp: "decision_planning", NoteProfile: "sparse",
			Approach: summary.Single,
			Input: summary.Input{
				Title: "Film Club Selection", Segments: ibSegs, Names: ibNames, Notes: ibNotes, Language: "en",
			},
			GoldSummary: ibAbs, GoldActions: ibActs, GoldDecs: ibDecs,
		})
		cases = append(cases, benchCase{
			ID: "ami-IB4010-49m-extract", Language: "en", ExpectedTyp: "decision_planning", NoteProfile: "sparse",
			Approach: summary.Extract,
			Input: summary.Input{
				Title: "Film Club Selection", Segments: ibSegs, Names: ibNames, Notes: ibNotes, Language: "en",
			},
			GoldSummary: ibAbs, GoldActions: ibActs, GoldDecs: ibDecs,
		})
	}
	return cases, nil
}

func loadFAME(csvPath, notesMD string, times map[string]int, instr string) (summary.Input, string, error) {
	f, err := os.Open(csvPath)
	if err != nil {
		return summary.Input{}, "", err
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.LazyQuotes = true
	head, err := r.Read()
	if err != nil {
		return summary.Input{}, "", err
	}
	row, err := r.Read()
	if err != nil {
		return summary.Input{}, "", err
	}
	col := map[string]string{}
	for i, h := range head {
		if i < len(row) {
			col[h] = row[i]
		}
	}
	// Parse `>>Speaker: text` lines into timed segments (~150 wpm).
	names := transcript.Names{Speakers: map[int]string{}}
	spkID := map[string]int{}
	var segs []transcript.Segment
	var cursor time.Duration
	var meName string
	for _, raw := range strings.Split(col["Meeting"], "\n") {
		line := strings.TrimSpace(raw)
		if !strings.HasPrefix(line, ">>") {
			continue
		}
		line = strings.TrimPrefix(line, ">>")
		who, text, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		who, text = strings.TrimSpace(who), strings.TrimSpace(text)
		if who == "" || text == "" {
			continue
		}
		words := len(strings.Fields(text))
		dur := time.Duration(max(2, words*60/150)) * time.Second
		var seg transcript.Segment
		if meName == "" {
			meName = who
		}
		if who == meName {
			seg = transcript.Segment{Track: transcript.Me, Speaker: 0, Start: cursor, End: cursor + dur, Text: text}
		} else {
			id, ok := spkID[who]
			if !ok {
				id = len(spkID) + 1
				spkID[who] = id
				names.Speakers[id] = who
			}
			seg = transcript.Segment{Track: transcript.Them, Speaker: id, Start: cursor, End: cursor + dur, Text: text}
		}
		segs = append(segs, seg)
		cursor += dur + time.Second
	}
	doc := notes.Parse(notesMD)
	for i := range doc.Blocks {
		if sec, ok := times[doc.Blocks[i].Text]; ok {
			doc.Blocks[i].Typed = time.Duration(sec) * time.Second
		}
	}
	in := summary.Input{
		Title:       strings.TrimSpace(col["Title"]),
		Segments:    segs,
		Names:       names,
		Notes:       doc,
		Me:          meName,
		Instruction: instr,
		Language:    "de",
	}
	return in, col["Summary"], nil
}

func loadAMIFromZip(zipPath, meetingID string) ([]transcript.Segment, transcript.Names, string, string, string, error) {
	z, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, transcript.Names{}, "", "", "", err
	}
	defer z.Close()

	type word struct {
		Start float64 `xml:"starttime,attr"`
		End   float64 `xml:"endtime,attr"`
		Punc  string  `xml:"punc,attr"`
		Text  string  `xml:",chardata"`
	}
	type wordFile struct {
		Words []word `xml:"w"`
	}
	names := transcript.Names{Speakers: map[int]string{}}
	var segs []transcript.Segment
	agents := []string{"A", "B", "C", "D"}
	for idx, ag := range agents {
		prefix := fmt.Sprintf("words/%s.%s.words.xml", meetingID, ag)
		var f *zip.File
		for _, zf := range z.File {
			if zf.Name == prefix {
				f = zf
				break
			}
		}
		if f == nil {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, names, "", "", "", err
		}
		var wf wordFile
		dec := xml.NewDecoder(rc)
		dec.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
		err = dec.Decode(&wf)
		rc.Close()
		if err != nil {
			return nil, names, "", "", "", err
		}
		if idx > 0 {
			names.Speakers[idx] = fmt.Sprintf("Them %d", idx)
		}
		var curText strings.Builder
		var start, end float64
		flush := func() {
			t := strings.TrimSpace(curText.String())
			if t == "" {
				return
			}
			seg := transcript.Segment{
				Track:   transcript.Them,
				Speaker: idx,
				Start:   time.Duration(start * float64(time.Second)),
				End:     time.Duration(end * float64(time.Second)),
				Text:    t,
			}
			if idx == 0 {
				seg.Track, seg.Speaker = transcript.Me, 0
			}
			segs = append(segs, seg)
			curText.Reset()
		}
		for _, w := range wf.Words {
			if w.End <= w.Start && w.Punc == "" {
				continue
			}
			if curText.Len() > 0 && w.Start-end >= 1.0 {
				flush()
			}
			if curText.Len() == 0 {
				start = w.Start
			}
			if w.Punc == "true" {
				curText.WriteString(strings.TrimSpace(w.Text))
			} else {
				if curText.Len() > 0 {
					curText.WriteByte(' ')
				}
				curText.WriteString(strings.TrimSpace(w.Text))
			}
			if w.End > end {
				end = w.End
			}
		}
		flush()
	}
	sort.Slice(segs, func(i, j int) bool { return segs[i].Start < segs[j].Start })

	// Read gold abstractive summary if present.
	var abs, acts, decs string
	for _, zf := range z.File {
		if zf.Name == fmt.Sprintf("abstractive/%s.abssumm.xml", meetingID) {
			rc, err := zf.Open()
			if err == nil {
				var raw struct {
					Abstract struct {
						S []string `xml:"sentence"`
					} `xml:"abstract"`
					Actions struct {
						S []string `xml:"sentence"`
					} `xml:"actions"`
					Decisions struct {
						S []string `xml:"sentence"`
					} `xml:"decisions"`
				}
				dec := xml.NewDecoder(rc)
				dec.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
				_ = dec.Decode(&raw)
				rc.Close()
				abs = strings.Join(raw.Abstract.S, " ")
				acts = strings.Join(raw.Actions.S, " ")
				decs = strings.Join(raw.Decisions.S, " ")
			}
		}
	}
	return segs, names, abs, acts, decs, nil
}
