package summary

import (
	"regexp"
	"strings"

	"github.com/motivated2die/flit/internal/notes"
)

var (
	reMarks        = regexp.MustCompile(`\s*\[(?:Q\d+|N\d+|@you)\]`)
	reH1           = regexp.MustCompile(`^#\s+`)
	reDeep         = regexp.MustCompile(`^#{4,}\s+`)
	reDoubleMarker = regexp.MustCompile(`^(\s*)[-*]\s+([-*]\s)`)
	reBracketHead  = regexp.MustCompile(`^(#{2,3}\s+)<\s*(.*?)\s*>$`)
	// reEmptyBold is emphasis around nothing, with what follows it.
	reEmptyBold = regexp.MustCompile(`^(\s*(?:[-*]|\d+\.)\s+(?:\[[ xX]\]\s+)?)(?:\*\*\s*\*\*\s*)+`)
)

// Tidy turns the model's answer into notes Markdown: code fences, chatty
// lead-ins and closers, cue marks and a top-level title go; headings
// deeper than ### become ###; the rest is read and written back as the
// notes editor's elements, so the summary opens in the same editor.
func Tidy(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "```") {
			continue
		}
		l = reMarks.ReplaceAllString(l, "")
		l = reEmptyBold.ReplaceAllString(l, "$1")      // "- **** text" (seen from Gemma 4 E2B)
		l = reDoubleMarker.ReplaceAllString(l, "$1$2") // "- - [ ] text" (the same)
		if reH1.MatchString(t) {
			continue // the recording has its title already
		}
		if reDeep.MatchString(t) {
			l = "### " + reDeep.ReplaceAllString(t, "")
		}
		if m := reBracketHead.FindStringSubmatch(strings.TrimSpace(l)); m != nil {
			l = m[1] + m[2] // a placeholder copied: "## <Sprache>"
		}
		lines = append(lines, l)
	}
	// Drop a lead-in before the first heading or list ("Here are the
	// notes:") and a closer after the last one.
	first, last := -1, -1
	for i, l := range lines {
		if structural(l) {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first > 0 && looksLikeChat(strings.Join(lines[:first], " ")) {
		lines = lines[first:]
		last -= first
	}
	if last >= 0 && last < len(lines)-1 && looksLikeChat(strings.Join(lines[last+1:], " ")) {
		lines = lines[:last+1]
	}
	d := notes.Parse(strings.Join(lines, "\n"))
	return strings.TrimSpace(d.Markdown())
}

// reNone is a line saying a section has nothing in it.
var reNone = regexp.MustCompile(`(?i)^[(\-–—\s]*(es wurden |es gab |es wurde |there were |there was |there are )?(keine|kein|nichts|no|none|nothing|n/?a)\b`)

// Assemble finishes the writer's notes: sections the model filled with
// "none" go, and the answers to the user's questions go in as their own
// section, before decisions, tasks and open questions.
func Assemble(md string, answers []Answer, w words) string {
	d := notes.Parse(md)
	fixed := map[string]bool{}
	for _, h := range []string{w.Decisions, w.Tasks, w.Open, w.Questions} {
		fixed[strings.ToLower(h)] = true
	}
	var out []notes.Block
	insertAt := -1
	for i := 0; i < len(d.Blocks); {
		b := d.Blocks[i]
		if b.Kind != notes.H2 {
			out = append(out, b)
			i++
			continue
		}
		j := i + 1
		for j < len(d.Blocks) && d.Blocks[j].Kind != notes.H2 {
			j++
		}
		head := strings.ToLower(strings.Trim(notes.PlainInline(b.Text), " :"))
		body := d.Blocks[i+1 : j]
		empty := true
		for _, c := range body {
			if c.Kind == notes.Task || !reNone.MatchString(notes.PlainInline(c.Text)) {
				empty = false
			}
		}
		keep := !empty && head != strings.ToLower(w.Questions)
		if fixed[head] && insertAt < 0 {
			insertAt = len(out)
		}
		if keep {
			sec := append([]notes.Block(nil), d.Blocks[i:j]...)
			if head == strings.ToLower(w.Tasks) {
				// Tasks as plain bullets (seen from Qwen3.5 2B): make them tasks.
				for k := range sec {
					if sec[k].Kind == notes.Bullet && sec[k].Indent == 0 {
						sec[k].Kind = notes.Task
					}
				}
			}
			out = append(out, sec...)
		}
		i = j
	}
	if len(answers) > 0 {
		q := []notes.Block{{Kind: notes.H2, Text: w.Questions, Typed: notes.Unknown}}
		for _, a := range answers {
			q = append(q, notes.Block{Kind: notes.Bullet, Text: "**" + strings.TrimSpace(a.Q.Text) + "** " + a.Text, Typed: notes.Unknown})
		}
		if insertAt < 0 {
			insertAt = len(out)
		}
		out = append(out[:insertAt], append(q, out[insertAt:]...)...)
	}
	return strings.TrimSpace(notes.Doc{Blocks: out}.Markdown())
}

func structural(l string) bool {
	t := strings.TrimSpace(l)
	return strings.HasPrefix(t, "#") || strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* ")
}

// looksLikeChat is a short remark to the reader rather than notes.
func looksLikeChat(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return true
	}
	if len(s) > 200 {
		return false
	}
	for _, p := range []string{"here", "hier ", "sure", "gerne", "certainly", "natürlich", "i hope", "ich hoffe", "let me know", "lass mich wissen", "these notes", "diese notizen", "if you", "wenn du", "falls du"} {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}
