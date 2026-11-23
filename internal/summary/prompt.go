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
			b.WriteString(" [")
			b.WriteString(m)
			b.WriteByte(']')
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// notesBlock formats the user's notes for the writer, separating untimed
// (pre-meeting / pasted) notes from live in-meeting jottings when both exist,
// and framing them as loose steering cues + supplemental facts.
func notesBlock(d notes.Doc, ns []Note) string {
	byBlock := map[int]Note{}
	for _, n := range ns {
		byBlock[n.Block] = n
	}
	var pre, live notes.Doc
	for i, b := range d.Blocks {
		// The questions are answered on their own; the writer gets the
		// notes without them.
		text := strings.TrimSpace(withoutMentions(b.Text))
		if text == "" || isRuleLine(text) {
			continue
		}
		if n, ok := byBlock[i]; ok && n.N > 0 {
			text = fmt.Sprintf("[N%d] %s", n.N, text)
		}
		b.Text = text
		if b.Typed == notes.Unknown {
			pre.Blocks = append(pre.Blocks, b)
		} else {
			live.Blocks = append(live.Blocks, b)
		}
	}
	preMD := strings.TrimSpace(pre.Markdown())
	liveMD := strings.TrimSpace(live.Markdown())
	if preMD == "" && liveMD == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("\nThe user's own notes (loose, incomplete steering cues and supplemental facts — the user often stops taking notes for long stretches while focusing on the meeting):\n")
	if preMD != "" {
		fmt.Fprintf(&b, "<pre_meeting_notes>\n%s\n</pre_meeting_notes>\n", preMD)
	}
	if liveMD != "" {
		fmt.Fprintf(&b, "<live_notes>\n%s\n</live_notes>\n", liveMD)
	}
	return b.String()
}

func hasAnchoredNotes(ns []Note) bool {
	for _, n := range ns {
		if n.N > 0 {
			return true
		}
	}
	return false
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
	return "You are a precise executive note-taker. Distill meeting transcripts into clear, scannable, strictly factual Markdown bullet notes in " + w.Name + ". Never invent facts, numbers, dates, decisions, tasks, or open questions. The transcript comes from speech recognition and may misspell names or terms."
}

// format is the output the writer is asked for, shared by both approaches.
func format(w words, dur time.Duration, hasNotes bool, mc MeetingClass, instruction string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Write the meeting notes in %s as Markdown using ONLY \"## \" headings, \"- \" bullets (nested by two spaces when needed), \"- [ ] \" tasks, and **bold**.\n", w.Name)
	b.WriteString("Rules:\n")
	b.WriteString("- Start immediately with the first \"## <Specific Topic>\" heading (no \"## Summary\", \"## Overview\", \"## Zusammenfassung\", intro, or closing remarks).\n")
	b.WriteString("- Every section MUST use short \"- \" bullet points (~10–22 words each), NEVER prose paragraphs. Start each bullet directly with the point or speaker — never write \"The team discussed…\", \"Participants talked about…\", \"Das Team besprach…\", or \"Es wurde besprochen…\".\n")
	b.WriteString("- Topic sections: one \"## \" heading per main topic, most important first, named specifically (never generic labels like \"Overview\", \"Discussion\", \"Updates\", \"Überblick\"). Cover the ENTIRE meeting from start to finish.\n")
	b.WriteString("- Speaker attribution: whenever a speaker has a real name or clear role (e.g. Betty, Francina, Ergonomist), explicitly attribute ideas, proposals, concerns, and objections to them (e.g. \"- **Anna** proposed…\", \"- **Ben** flagged concern about…\"). STRICTLY FORBIDDEN: never write raw labels like \"Them 1\", \"Them 2\", or \"Them 3\" anywhere in the notes. If a speaker introduced themselves by name in the transcript, use their real name; if their name and role are both unknown, write the bullet cleanly WITHOUT any speaker label.\n")
	b.WriteString("- Filter noise: omit small talk, greetings, personal chat (e.g. weather, weekends, plants, pets, hobbies, jokes), mic/audio checks, and warm-up exercises unless the meeting was specifically about them.\n")
	if hasNotes {
		b.WriteString("- Using the user's notes: treat them as loose steering hints and supplemental facts, NOT an exhaustive outline. Never skip important transcript topics just because the user took no notes on them. Retain concrete facts/numbers the user typed, pay extra attention to **bold** text and ### headings, keep open \"- [ ]\" tasks if still unresolved, and ignore pre-meeting agenda items that were never actually discussed. Rephrase in your own words — never copy their shorthand verbatim or output [N1] marks.\n")
	}
	fmt.Fprintf(&b, "- \"## %s\" (OPTIONAL): include ONLY explicit, finalized agreements. Brainstormed ideas, proposals, or general goals are NOT decisions. Omit this section if nothing was explicitly decided.\n", w.Decisions)
	fmt.Fprintf(&b, "- \"## %s\" (OPTIONAL): whenever someone agreed to or was assigned a concrete follow-up or deliverable (even an individual design/prep task for the next meeting), ALWAYS list it here as \"- [ ] **Name:** what, by when\" (omit deadline if not stated) — never bury concrete assignments as plain bullets in a topic section, and never duplicate them in a separate \"Next Steps\" section. Omit only if no one committed to any work.\n", w.Tasks)
	fmt.Fprintf(&b, "- \"## %s\" (OPTIONAL): include ONLY unresolved questions explicitly raised in the meeting that still need an answer later. Never invent follow-up questions. Omit if none.\n", w.Open)
	if w.Name != "English" {
		fmt.Fprintf(&b, "- Keep established English technical terms and product names in English within natural %s grammar.\n", w.Name)
	}
	b.WriteString(styleGuidance(mc, dur))
	if s := strings.TrimSpace(instruction); s != "" {
		fmt.Fprintf(&b, "User steering hint (adjust focus, emphasis, or language accordingly while keeping factual grounding, bullet format, and speaker attribution intact — only change the structure if they explicitly ask for a different format): %s\n", s)
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
