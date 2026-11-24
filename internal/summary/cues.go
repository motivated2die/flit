package summary

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/motivated2die/flit/internal/notes"
)

// Question is one of the user's @flit instructions: what to find out, and
// when they wrote it.
type Question struct {
	N    int
	At   time.Duration // notes.Unknown when not written during the recording
	Text string
}

// Note is one line of the user's notes, numbered for the prompt, with the
// turn it was most likely written about.
type Note struct {
	N     int
	Block int // index in the notes
	Text  string
	Typed time.Duration
	Turn  int // -1: not anchored
}

// The windows of transcript a cue points at (guesses, to tune): a question
// is about what was said in the minutes before it was typed; a note line
// about the last minute and a half.
const (
	questionBefore = 3 * time.Minute
	questionAfter  = 30 * time.Second
	noteBefore     = 90 * time.Second
	noteAfter      = 10 * time.Second
)

// Cues reads the questions and notes and marks the turns they point at:
// "Q1" on every turn in a question's window, "N3" on the one turn a note
// line most likely refers to, "@you" where someone else names the user.
func Cues(in Input, ts []Turn) ([]Question, []Note) {
	var qs []Question
	var ns []Note
	anchoredCount := 0
	for bi, b := range in.Notes.Blocks {
		for _, m := range notes.Mentions(b.Text) {
			at := m.At
			if at == notes.Unknown {
				at = b.Typed
			}
			text := strings.TrimSpace(m.Instruction)
			if text == "" {
				continue
			}
			q := Question{N: len(qs) + 1, At: at, Text: text}
			qs = append(qs, q)
			if at == notes.Unknown {
				continue
			}
			for i := range ts {
				if ts[i].End >= at-questionBefore && ts[i].Start <= at+questionAfter {
					ts[i].Marks = append(ts[i].Marks, fmt.Sprintf("Q%d", q.N))
				}
			}
		}
		plain := strings.TrimSpace(notes.PlainInline(withoutMentions(b.Text)))
		if utf8.RuneCountInString(plain) < 3 || isRuleLine(plain) || b.Kind == notes.H2 || b.Kind == notes.H3 {
			continue
		}
		n := Note{Block: bi, Text: plain, Typed: b.Typed, Turn: -1}
		if b.Typed != notes.Unknown {
			n.Turn = anchor(ts, plain, b.Typed)
			if n.Turn >= 0 {
				n.N = anchoredCount + 1
				anchoredCount++
				ts[n.Turn].Marks = append(ts[n.Turn].Marks, fmt.Sprintf("N%d", n.N))
			}
		}
		ns = append(ns, n)
	}
	if len(in.MeNames) > 0 {
		for i := range ts {
			if !ts[i].Me && mentionsAny(ts[i].Text, in.MeNames) {
				ts[i].Marks = append(ts[i].Marks, "@you")
			}
		}
	}
	return qs, ns
}

func withoutMentions(s string) string {
	ms := notes.Mentions(s)
	for i := len(ms) - 1; i >= 0; i-- {
		s = s[:ms[i].From] + s[ms[i].To:]
	}
	return s
}

func isRuleLine(s string) bool { return strings.Trim(s, "-*_ ") == "" }

// anchor is the turn a note typed at t most likely refers to: of the
// turns in its window, the one sharing the most words with it; with none
// shared, the last turn that began before it.
func anchor(ts []Turn, note string, t time.Duration) int {
	nw := keyWords(note)
	best, bestScore, last := -1, 0, -1
	for i, turn := range ts {
		if turn.Start <= t {
			last = i
		}
		if turn.End < t-noteBefore || turn.Start > t+noteAfter {
			continue
		}
		if s := overlap(nw, keyWords(turn.Text)); s > bestScore {
			best, bestScore = i, s
		}
	}
	if best >= 0 {
		return best
	}
	if last >= 0 && ts[last].End >= t-noteBefore {
		return last
	}
	return -1
}

// keyWords are a text's words worth matching: lower case, three letters
// or more, not among the most common ones.
func keyWords(s string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(s), notLetterOrDigit) {
		if utf8.RuneCountInString(w) < 3 {
			continue
		}
		if _, common := commonWords[w]; common {
			continue
		}
		out = append(out, w)
	}
	return out
}

func notLetterOrDigit(r rune) bool { return notLetter(r) && (r < '0' || r > '9') }

// overlap counts the note's words found in the turn; two words match when
// equal, or when both are long and share their first five letters (a
// cheap stand-in for German inflection: Polizist, Polizisten).
func overlap(note, turn []string) int {
	n := 0
	for _, a := range note {
		for _, b := range turn {
			if sameWord(a, b) {
				n++
				break
			}
		}
	}
	return n
}

func sameWord(a, b string) bool {
	if a == b {
		return true
	}
	ra, rb := []rune(a), []rune(b)
	if len(ra) < 5 || len(rb) < 5 {
		return false
	}
	return string(ra[:5]) == string(rb[:5])
}

func mentionsAny(text string, names []string) bool {
	ws := strings.FieldsFunc(strings.ToLower(text), notLetter)
	for _, name := range names {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			continue
		}
		for _, w := range ws {
			if w == name {
				return true
			}
		}
	}
	return false
}
