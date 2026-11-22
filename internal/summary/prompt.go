package summary

import (
	"fmt"
	"strings"
	"time"

	"github.com/motivated2die/flit/internal/notes"
)

// Section is a stretch of turns read in one call.
type Section struct {
	From, To   int // turns [From, To)
	Start, End time.Duration
}

// Split cuts the turns into sections of about target tokens, never inside
// a turn, preferring the longest pause in the last third before the
// target is reached.
func Split(ts []Turn, tokens func(Turn) int, target int) []Section {
	var out []Section
	from, sum := 0, 0
	for i := 0; i < len(ts); i++ {
		sum += tokens(ts[i])
		if sum < target || i == from {
			continue
		}
		// Over the target: cut before the turn after the longest pause
		// among the turns that brought the sum past two thirds of it.
		cut, gap, run := i, time.Duration(-1), 0
		for j := from; j <= i; j++ {
			run += tokens(ts[j])
			if j > from && run >= target*2/3 {
				if g := ts[j].Start - ts[j-1].End; g > gap {
					cut, gap = j, g
				}
			}
		}
		if cut <= from {
			cut = i
		}
		out = append(out, section(ts, from, cut))
		from, sum = cut, 0
		for j := cut; j <= i; j++ {
			sum += tokens(ts[j])
		}
	}
	if from < len(ts) {
		out = append(out, section(ts, from, len(ts)))
	}
	return out
}

func section(ts []Turn, from, to int) Section {
	return Section{From: from, To: to, Start: ts[from].Start, End: ts[to-1].End}
}

// transcriptText is the compact transcript of turns [from, to): "Who:
// text", with cue marks at the end of the line.
func transcriptText(ts []Turn, from, to int) string {
	var b strings.Builder
	for _, t := range ts[from:to] {
		b.WriteString(t.Who)
		b.WriteString(": ")
		b.WriteString(t.Text)
		for _, m := range t.Marks {
			b.WriteString(" [" + m + "]")
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// notesText is the user's notes as the model reads them: their Markdown,
// mentions replaced by the question's number, anchored lines numbered.
func notesText(d notes.Doc, ns []Note) string {
	byBlock := map[int]Note{}
	for _, n := range ns {
		byBlock[n.Block] = n
	}
	var out notes.Doc
	for i, b := range d.Blocks {
		// The questions are answered on their own; the writer gets the
		// notes without them.
		text := strings.TrimSpace(withoutMentions(b.Text))
		if text == "" || isRuleLine(text) {
			continue
		}
		if n, ok := byBlock[i]; ok {
			text = fmt.Sprintf("[N%d] %s", n.N, text)
		}
		b.Text = text
		out.Blocks = append(out.Blocks, b)
	}
	return strings.TrimSpace(out.Markdown())
}

func questionsText(qs []Question) string {
	var b strings.Builder
	for _, q := range qs {
		if q.At == notes.Unknown {
			fmt.Fprintf(&b, "Q%d: %s\n", q.N, q.Text)
		} else {
			fmt.Fprintf(&b, "Q%d (asked at %s; the lines marked [Q%d] lead up to it): %s\n", q.N, clock(q.At), q.N, q.Text)
		}
	}
	return b.String()
}

func clock(d time.Duration) string { return notes.Clock(d) }

func speakersLine(ts []Turn, me string) string {
	var parts []string
	for _, s := range speakers(ts) {
		if s == me {
			parts = append(parts, s+" (the user, who wants these notes)")
		} else {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, ", ")
}

func system(w words) string {
	return "You write concise, factual meeting notes from a transcript. Use only what the transcript and the user's notes say: never invent names, numbers, dates, decisions or tasks. The transcript comes from speech recognition and can misspell words. Write in " + w.Name + "."
}

// format is the output the writer is asked for, shared by both approaches.
func format(w words, instruction string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Write the meeting notes in %s as Markdown, using only \"## \" headings, \"### \" subheadings, \"- \" bullets (nested by two spaces), \"- [ ] \" tasks and **bold**.\n", w.Name)
	b.WriteString("Sections, in this order:\n")
	fmt.Fprintf(&b, "1. \"## %s\": two to four sentences on what it was about and what came of it.\n", w.Summary)
	b.WriteString("2. One \"## \" section per main topic, in the order discussed, named after the topic. Where the user's notes have a heading for a topic, use that heading. Under each, short bullets with the concrete facts: names, numbers, dates, reasons.\n")
	fmt.Fprintf(&b, "3. \"## %s\": only what the participants explicitly agreed on. Leave it out when nothing was agreed (an interview or a chat usually has nothing).\n", w.Decisions)
	fmt.Fprintf(&b, "4. \"## %s\": only what someone agreed or was asked to do, each as \"- [ ] **Name:** what, by when\". Leave it out when there is nothing.\n", w.Tasks)
	fmt.Fprintf(&b, "5. \"## %s\": only questions about the meeting's subject that nobody answered and that need an answer later. A question that was answered in the conversation is not open. Leave it out when there are none.\n", w.Open)
	b.WriteString("Integrate the user's notes; don't copy their lines or labels. No introduction, no closing remarks, no [N1] marks.\n")
	if s := strings.TrimSpace(instruction); s != "" {
		fmt.Fprintf(&b, "The user adds this instruction, which takes precedence over the structure above: %s\n", s)
	}
	return b.String()
}

func cueLegend(qs []Question, ns []Note, you bool) string {
	var parts []string
	if len(qs) > 0 {
		parts = append(parts, "[Q1] marks lines said shortly before the user asked question Q1")
	}
	if len(ns) > 0 {
		parts = append(parts, "[N1] marks the line the user was most likely writing note N1 about")
	}
	if you {
		parts = append(parts, "[@you] marks lines that name the user")
	}
	if len(parts) == 0 {
		return ""
	}
	return "In the transcript, " + strings.Join(parts, "; ") + ". Pay special attention to these lines.\n"
}

func hasYou(ts []Turn) bool {
	for _, t := range ts {
		for _, m := range t.Marks {
			if m == "@you" {
				return true
			}
		}
	}
	return false
}
