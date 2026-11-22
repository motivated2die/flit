package summary

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/motivated2die/flit/internal/llm"
	"github.com/motivated2die/flit/internal/notes"
)

// Model is what the pipeline needs of a language model server.
type Model interface {
	Chat(ctx context.Context, r llm.Request) (llm.Response, error)
	Tokens(ctx context.Context, text string) (int, error)
}

// Approach is how the transcript is read.
type Approach string

const (
	// Single reads the whole transcript in one call and writes the notes
	// (approach A). Needs a context the size of the meeting.
	Single Approach = "single"
	// Extract reads sections into small JSON items with quotes, checks
	// them in code, then writes the notes from the items in one call
	// (approach C). Any length, in an 8k context.
	Extract Approach = "extract"
)

// Options tune a run. Zero values take the defaults.
type Options struct {
	Approach Approach
	// SectionTokens is the size of a section for Extract (default 3000).
	SectionTokens int
	// MaxTokens caps the notes the writer writes (default 1200).
	MaxTokens   int
	Temperature float64 // default 0.3
	Seed        int
	// NoCues leaves out the cue marks (for measuring what they bring).
	NoCues bool
	// Progress hears how far the summary has come, as it goes.
	Progress func(Progress)
}

// Progress is how far a summary has come: the step running ("ask", "read",
// "write"), which of how many of its kind, and the whole run's share done
// (0..1), estimated from the tokens each call reads and writes.
type Progress struct {
	Step        string
	Done, Total int
	Fraction    float64
}

// Call is one model call's figures.
type Call struct {
	Step    string
	Timings llm.Timings
	Wall    time.Duration
	Finish  string
}

// Result is a written summary and how it came about.
type Result struct {
	Markdown string
	Language string
	Turns    int
	Tokens   int // the compact transcript's tokens
	Sections []Section
	Items    []Items  // Extract: what each section gave, after checks
	Answers  []Answer // the user's questions, answered
	Dropped  int      // Extract: decisions and tasks whose quote wasn't found
	Calls    []Call
	Prompts  []string // every prompt sent, for the dev command (never logged)
}

// Total sums the calls.
func (r Result) Total() llm.Timings {
	var t llm.Timings
	for _, c := range r.Calls {
		t = t.Add(c.Timings)
	}
	return t
}

// ErrEmpty means there is nothing to summarise.
var ErrEmpty = errors.New("the transcript is empty")

// Run writes the summary.
func Run(ctx context.Context, m Model, in Input, o Options) (Result, error) {
	if o.Approach == "" {
		o.Approach = Extract
	}
	if o.SectionTokens <= 0 {
		o.SectionTokens = 3000
	}
	if o.MaxTokens <= 0 {
		o.MaxTokens = 1200
	}
	if o.Temperature == 0 {
		o.Temperature = 0.3
	}
	ts := in.Turns()
	if len(ts) == 0 {
		return Result{}, ErrEmpty
	}
	r := Result{Turns: len(ts), Language: Language(ts, in.Language)}
	w := wordsFor(r.Language)
	qs, ns := Cues(in, ts)
	if o.NoCues {
		for i := range ts {
			ts[i].Marks = nil
		}
		ns = nil
	}
	whole := transcriptText(ts, 0, len(ts))
	n, err := m.Tokens(ctx, whole)
	if err != nil {
		return r, fmt.Errorf("counting tokens: %w", err)
	}
	r.Tokens = n
	perRune := float64(n) / float64(max(1, utf8.RuneCountInString(whole)))
	tokens := func(t Turn) int {
		return int(float64(utf8.RuneCountInString(t.Who)+utf8.RuneCountInString(t.Text)+4) * perRune)
	}
	head := fmt.Sprintf("Meeting: %q, %s long. Speakers: %s.\n", or(in.Title, "untitled"), clock(ts[len(ts)-1].End), speakersLine(ts, in.me()))
	if o.Approach == Extract {
		r.Sections = Split(ts, tokens, o.SectionTokens)
	}
	tr := plan(o, ts, qs, r.Sections, n, tokens)

	// The user's questions first, one focused call each, on the part of
	// the transcript that led up to them: small models answer one
	// question about a short excerpt far better than several questions
	// mixed into the whole task.
	for i, q := range qs {
		a, call, prompt, err := ask(ctx, m, o, w, head, ts, q, tr.call("ask", i, len(qs)))
		r.Calls = append(r.Calls, call)
		r.Prompts = append(r.Prompts, prompt)
		if err != nil {
			if ctx.Err() != nil {
				return r, ctx.Err()
			}
			a = Answer{Q: q, Text: w.NotDiscussed, Failed: true}
		}
		r.Answers = append(r.Answers, a)
	}
	// The writer reads note and name cues; the questions are answered.
	for i := range ts {
		ts[i].Marks = slices.DeleteFunc(ts[i].Marks, func(m string) bool { return strings.HasPrefix(m, "Q") })
	}
	whole = transcriptText(ts, 0, len(ts))
	legend := cueLegend(nil, ns, hasYou(ts))
	notesMD := notesText(in.Notes, ns)
	var tail strings.Builder
	if notesMD != "" {
		fmt.Fprintf(&tail, "\nThe user's own notes, written during the meeting:\n<notes>\n%s\n</notes>\n", notesMD)
	}

	var body string
	switch o.Approach {
	case Single:
		r.Sections = []Section{section(ts, 0, len(ts))}
		body = head + legend + "<transcript>\n" + whole + "</transcript>\n" + tail.String() + "\n" + format(w, in.Instruction)
	case Extract:
		var items strings.Builder
		for i, s := range r.Sections {
			it, call, prompt, err := extract(ctx, m, o, w, head, legend, ts, s, i, len(r.Sections), ns, tr.call("read", i, len(r.Sections)))
			r.Calls = append(r.Calls, call)
			r.Prompts = append(r.Prompts, prompt)
			if err != nil {
				if ctx.Err() != nil {
					return r, ctx.Err()
				}
				it = Items{} // a failed section leaves a gap, not a failed summary
			}
			r.Dropped += it.check(ts, s)
			r.Items = append(r.Items, it)
			items.WriteString(it.text(i, s))
		}
		body = head + "\nWhat was extracted from each part of the transcript, in order:\n<extracted>\n" + items.String() + "</extracted>\n" + tail.String() +
			"\n" + format(w, in.Instruction) + "Merge topics that recur across parts. Use only the extracted notes and the user's notes.\n"
	default:
		return r, fmt.Errorf("unknown approach %q", o.Approach)
	}
	resp, err := m.Chat(ctx, llm.Request{
		Messages:    []llm.Message{{Role: "system", Content: system(w)}, {Role: "user", Content: body}},
		MaxTokens:   o.MaxTokens,
		Temperature: o.Temperature,
		Seed:        o.Seed,
		OnProgress:  tr.call("write", 0, 1),
	})
	r.Prompts = append(r.Prompts, body)
	r.Calls = append(r.Calls, Call{Step: "write", Timings: resp.Timings, Wall: resp.Wall, Finish: resp.Finish})
	if err != nil {
		return r, err
	}
	md := Tidy(resp.Text)
	if md == "" {
		return r, errors.New("the model wrote nothing usable")
	}
	r.Markdown = Assemble(md, r.Answers, w)
	tr.finish()
	return r, nil
}

// Answer is the answer to one of the user's questions.
type Answer struct {
	Q      Question
	Text   string
	Failed bool // the call failed; Text says it wasn't answered
}

// ask answers one question from the excerpt around it (the whole
// transcript for a question not typed during the recording).
func ask(ctx context.Context, m Model, o Options, w words, head string, ts []Turn, q Question, progress func(llm.CallProgress)) (Answer, Call, string, error) {
	from, to, ok := excerpt(ts, q)
	if !ok {
		return Answer{Q: q, Text: w.NotDiscussed}, Call{Step: fmt.Sprintf("ask %d", q.N)}, "", nil
	}
	excerpt := plainText(ts, from, to)
	var b strings.Builder
	b.WriteString(head)
	// The question before the excerpt too: it is read with the question
	// in mind (the 4B said "not answered" to an answered question when it
	// came only after).
	fmt.Fprintf(&b, "The user's question: %q\n", q.Text)
	if from < to {
		fmt.Fprintf(&b, "Transcript excerpt, %s to %s:\n<transcript>\n%s</transcript>\n", clock(ts[from].Start), clock(ts[to-1].End), excerpt)
	}
	when := ""
	if q.At != notes.Unknown {
		when = " at " + clock(q.At)
	}
	fmt.Fprintf(&b, "\nDuring the meeting%s, the user wrote this question: %q\n", when, q.Text)
	fmt.Fprintf(&b, "Answer it in one or two sentences in %s from what the excerpt says, with the concrete facts (names, numbers, places). Look for the lines that relate to the question even when they use other words. Only if nothing in the excerpt relates to it, write: %s\n", w.Name, w.NotDiscussed)
	prompt := b.String()
	resp, err := m.Chat(ctx, llm.Request{
		Messages:    []llm.Message{{Role: "system", Content: system(w)}, {Role: "user", Content: prompt}},
		MaxTokens:   askMax,
		Temperature: o.Temperature,
		Seed:        o.Seed,
		OnProgress:  progress,
	})
	call := Call{Step: fmt.Sprintf("ask %d", q.N), Timings: resp.Timings, Wall: resp.Wall, Finish: resp.Finish}
	if err != nil {
		return Answer{}, call, prompt, err
	}
	text := strings.TrimSpace(notes.PlainInline(strings.Join(strings.Fields(resp.Text), " ")))
	if text == "" {
		text = w.NotDiscussed
	}
	return Answer{Q: q, Text: text}, call, prompt, nil
}

// excerpt is the turns [from, to) a question is answered from: those from
// questionBefore before it was typed to questionAfter after it (the last
// few before it when nothing was said near it); for a question without a
// time, the whole transcript when it is short, else the part sharing the
// most words with it. Not ok: nothing in the transcript relates to it.
func excerpt(ts []Turn, q Question) (from, to int, ok bool) {
	at := q.At
	if at == notes.Unknown {
		if estimate(ts) <= askWhole {
			return 0, len(ts), true
		}
		qw, best, score := keyWords(q.Text), -1, 0
		for i, t := range ts {
			if s := overlap(qw, keyWords(t.Text)); s > score {
				best, score = i, s
			}
		}
		if best < 0 {
			return 0, 0, false
		}
		at = ts[best].End + questionBefore/2
	}
	from, to = -1, 0
	for i, t := range ts {
		if t.End >= at-questionBefore && t.Start <= at+questionAfter {
			if from < 0 {
				from = i
			}
			to = i + 1
		}
	}
	if from < 0 {
		from, to = 0, 0
		for i, t := range ts {
			if t.Start <= at {
				to = i + 1
			}
		}
		from = max(0, to-6)
	}
	return from, to, true
}

// askWhole is how long a transcript may be to answer a question without
// a time from all of it (tokens, about).
const askWhole = 5000

// estimate is about how many tokens turns are (as EstimateTokens).
func estimate(ts []Turn) int {
	n := 0
	for _, t := range ts {
		n += len([]rune(t.Who)) + len([]rune(t.Text)) + 4
	}
	return n * 10 / 33
}

// plainText is turns [from, to) without cue marks.
func plainText(ts []Turn, from, to int) string {
	var b strings.Builder
	for _, t := range ts[from:to] {
		b.WriteString(t.Who + ": " + t.Text + "\n")
	}
	return b.String()
}

func or(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

// Items are what one section gave.
type Items struct {
	Topics    []Topic    `json:"topics"`
	Decisions []Decision `json:"decisions"`
	Tasks     []Task     `json:"tasks"`
	Open      []string   `json:"open"`
}

type Topic struct {
	Title  string   `json:"title"`
	Points []string `json:"points"`
}

type Decision struct {
	Text  string        `json:"text"`
	Quote string        `json:"quote"`
	At    time.Duration `json:"-"`
}

type Task struct {
	Who   string        `json:"who"`
	What  string        `json:"what"`
	Due   string        `json:"due"`
	Quote string        `json:"quote"`
	At    time.Duration `json:"-"`
}

// schema is the extraction's JSON schema. Caps keep the writing short.
func schema() json.RawMessage {
	str := map[string]any{"type": "string"}
	obj := func(props map[string]any, order ...string) map[string]any {
		return map[string]any{"type": "object", "properties": props, "required": order, "additionalProperties": false}
	}
	arr := func(items any, n int) map[string]any {
		return map[string]any{"type": "array", "items": items, "maxItems": n}
	}
	props := map[string]any{
		"topics":    arr(obj(map[string]any{"title": str, "points": arr(str, 5)}, "title", "points"), 4),
		"decisions": arr(obj(map[string]any{"text": str, "quote": str}, "text", "quote"), 5),
		"tasks":     arr(obj(map[string]any{"who": str, "what": str, "due": str, "quote": str}, "who", "what", "due", "quote"), 6),
		"open":      arr(str, 4),
	}
	b, _ := json.Marshal(obj(props, "topics", "decisions", "tasks", "open"))
	return b
}

func extract(ctx context.Context, m Model, o Options, w words, head, legend string, ts []Turn, s Section, i, n int, ns []Note, progress func(llm.CallProgress)) (Items, Call, string, error) {
	var b strings.Builder
	b.WriteString(head)
	b.WriteString(legend)
	fmt.Fprintf(&b, "Part %d of %d of the transcript, %s to %s:\n<transcript>\n%s</transcript>\n", i+1, n, clock(s.Start), clock(s.End), transcriptText(ts, s.From, s.To))
	var mineNotes []string
	for _, nt := range ns {
		if nt.Turn >= s.From && nt.Turn < s.To {
			mineNotes = append(mineNotes, fmt.Sprintf("N%d %q", nt.N, nt.Text))
		}
	}
	if len(mineNotes) > 0 {
		fmt.Fprintf(&b, "\nNotes the user wrote during this part: %s\n", strings.Join(mineNotes, ", "))
	}
	b.WriteString("\nExtract from this part, as JSON:\n")
	b.WriteString("- topics: up to 4 topics discussed, each a short title and up to 5 short points with the concrete facts (names, numbers, dates, reasons)\n")
	b.WriteString("- decisions: what was decided, each with a quote\n")
	b.WriteString("- tasks: what someone agreed or was asked to do: who (a speaker as named above), what, due (\"\" if not said), quote\n")
	b.WriteString("- open: questions left open\n")
	fmt.Fprintf(&b, "A quote is up to 12 words copied exactly from the transcript. Use empty lists rather than guesses. Write the text in %s.\n", w.Name)
	prompt := b.String()
	resp, err := m.Chat(ctx, llm.Request{
		Messages:    []llm.Message{{Role: "system", Content: system(w)}, {Role: "user", Content: prompt}},
		MaxTokens:   extractMax,
		Temperature: o.Temperature,
		Seed:        o.Seed,
		Schema:      schema(),
		OnProgress:  progress,
	})
	call := Call{Step: fmt.Sprintf("read %d/%d", i+1, n), Timings: resp.Timings, Wall: resp.Wall, Finish: resp.Finish}
	if err != nil {
		return Items{}, call, prompt, err
	}
	var it Items
	if err := json.Unmarshal([]byte(resp.Text), &it); err != nil {
		return Items{}, call, prompt, fmt.Errorf("section %d: %w", i+1, err)
	}
	return it, call, prompt, nil
}

// check finds each quote in the section: decisions and tasks whose quote
// isn't there are dropped (AutoDoc's rule); answers are kept either way,
// since the user asked for them. Returns how many were dropped.
func (it *Items) check(ts []Turn, s Section) int {
	dropped := 0
	var ds []Decision
	for _, d := range it.Decisions {
		if d.At = findQuote(ts, s, d.Quote); d.At >= 0 && meaningful(d.Text) {
			ds = append(ds, d)
		} else {
			dropped++
		}
	}
	var tk []Task
	for _, t := range it.Tasks {
		if t.At = findQuote(ts, s, t.Quote); t.At >= 0 && meaningful(t.What) {
			tk = append(tk, t)
		} else {
			dropped++
		}
	}
	it.Decisions, it.Tasks = ds, tk
	return dropped
}

// findQuote returns the start of the turn holding most of the quote's
// words, if at least four in five of them are there in order; -1
// otherwise.
func findQuote(ts []Turn, s Section, quote string) time.Duration {
	q := normWords(reMarks.ReplaceAllString(quote, ""))
	if len(q) == 0 {
		return -1
	}
	for _, t := range ts[s.From:s.To] {
		tw := normWords(t.Text)
		j := 0
		for _, w := range tw {
			if j < len(q) && w == q[j] {
				j++
			}
		}
		// Words in order, allowing gaps (one wrong word is still found).
		if j*5 >= len(q)*4 {
			return t.Start
		}
	}
	return -1
}

func normWords(s string) []string { return strings.FieldsFunc(strings.ToLower(s), notLetterOrDigit) }

// text is a section's items as the writer reads them: grouped under
// plain headings (per-line labels like "Decided:" were copied into the
// notes by the 4B).
func (it Items) text(i int, s Section) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Part %d (%s–%s)\n", i+1, clock(s.Start), clock(s.End))
	for _, t := range it.Topics {
		fmt.Fprintf(&b, "Topic %q:\n", t.Title)
		for _, p := range t.Points {
			fmt.Fprintf(&b, "  - %s\n", p)
		}
	}
	group := func(title string, lines []string) {
		if len(lines) == 0 {
			return
		}
		fmt.Fprintf(&b, "%s:\n", title)
		for _, l := range lines {
			fmt.Fprintf(&b, "  - %s\n", l)
		}
	}
	var ds, ts, os []string
	for _, d := range it.Decisions {
		ds = append(ds, d.Text)
	}
	for _, t := range it.Tasks {
		due := ""
		if strings.TrimSpace(t.Due) != "" {
			due = " (by " + t.Due + ")"
		}
		ts = append(ts, t.Who+": "+t.What+due)
	}
	os = append(os, it.Open...)
	group("Decisions", ds)
	group("Tasks", ts)
	group("Open questions", os)
	return b.String()
}

// meaningful is an item's text worth keeping: three words or more ("Them
// 1." came back as a decision).
func meaningful(s string) bool { return len(strings.Fields(s)) >= 3 }
