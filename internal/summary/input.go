// Package summary writes meeting notes from a transcript and the user's own
// notes with a small local language model, when the user asks for them
// after a meeting. Everything around the model is code: the compact
// transcript, the language, the cues that tell the model where to look
// (the user's @flit questions, when each note line was typed, mentions of
// the user's name), sections, checks of what the model returns, and
// tidying its Markdown. Research and the approaches compared:
// docs/research/summary-pipeline.md.
package summary

import (
	"strings"
	"time"
	"unicode"

	"github.com/motivated2die/flit/internal/notes"
	"github.com/motivated2die/flit/internal/transcript"
)

// Input is what a summary is written from.
type Input struct {
	Title    string
	Segments []transcript.Segment // merged, in time order
	Names    transcript.Names     // the speakers' names the user gave
	Notes    notes.Doc            // the user's notes, with typing times
	// Me is how the user appears in the transcript and the summary (a
	// first name, say); "" reads "Me". MeNames are words that mention the
	// user when others say them (first and last name).
	Me      string
	MeNames []string
	// Instruction is the user's own addition to the prompt for this
	// summary ("shorter", "focus on the budget"); "" for none.
	Instruction string
	// Language is the transcription's language ("de", "en"): the
	// fallback when the transcript's own words don't tell.
	Language string
}

// Turn is one speaker's consecutive speech, as the model reads it.
type Turn struct {
	Who        string
	Me         bool
	Start, End time.Duration
	Text       string
	Marks      []string // cues: "Q1", "N3", "@you"
}

// turnGap joins one speaker's segments into a turn when they are less
// than this apart: the model needs no line per breath.
const turnGap = 8 * time.Second

func (in Input) me() string {
	if s := strings.TrimSpace(in.Me); s != "" {
		return s
	}
	return "Me"
}

// Turns is the compact transcript: one line per turn, the user's speech
// under their name.
func (in Input) Turns() []Turn {
	names := in.Names
	names.Me = in.me()
	var out []Turn
	for _, p := range transcript.Paragraphs(in.Segments, names, turnGap) {
		out = append(out, Turn{Who: p.Speaker, Me: p.Track == transcript.Me, Start: p.Start, End: p.End, Text: p.Text})
	}
	return out
}

// EstimateTokens is about how many tokens the compact transcript is, before
// a tokenizer is at hand: a character count at the rate measured with the
// candidate models (~3.3 characters a token for German and English).
func EstimateTokens(in Input) int { return estimate(in.Turns()) }

// speakers lists the turns' speakers in order of first appearance.
func speakers(ts []Turn) []string {
	var out []string
	seen := map[string]bool{}
	for _, t := range ts {
		if !seen[t.Who] {
			seen[t.Who] = true
			out = append(out, t.Who)
		}
	}
	return out
}

// Language is the language most of the transcript is in ("de" or "en"),
// counted from common short words; the fallback when neither shows.
func Language(ts []Turn, fallback string) string {
	counts := map[string]int{}
	for _, t := range ts {
		for _, w := range strings.FieldsFunc(strings.ToLower(t.Text), notLetter) {
			if l, ok := commonWords[w]; ok {
				counts[l]++
			}
		}
	}
	best, n := "", 0
	for l, c := range counts {
		if c > n || (c == n && l == fallback) {
			best, n = l, c
		}
	}
	if n < 5 {
		if _, ok := languages[fallback]; ok {
			return fallback
		}
		return "en"
	}
	return best
}

func notLetter(r rune) bool { return !unicode.IsLetter(r) && r != '\'' }

// commonWords are short words frequent in one language and rare in the
// other (Yogurt's guessLanguage does the same).
var commonWords = func() map[string]string {
	m := map[string]string{}
	for _, w := range strings.Fields("der die das und ist nicht ich du wir sie es ein eine einen zu mit auf für den dem von sich auch noch nur aber wie was wenn dass oder hat habe haben bin sind war mal schon jetzt ja nein ganz gut") {
		m[w] = "de"
	}
	for _, w := range strings.Fields("the and is are not you we it a to of in for on with that this have has be was were they there what if or but so just yes no well can will would") {
		m[w] = "en"
	}
	return m
}()

// words are the fixed words of a summary in one language.
type words struct {
	Name         string // the language, in English (for the prompt)
	Summary      string
	Questions    string
	Decisions    string
	Tasks        string
	Open         string
	NotDiscussed string
}

var languages = map[string]words{
	"de": {"German", "Zusammenfassung", "Deine Fragen", "Entscheidungen", "Aufgaben", "Offene Fragen", "Im Gespräch nicht beantwortet."},
	"en": {"English", "Summary", "Your questions", "Decisions", "Tasks", "Open questions", "Not answered in the meeting."},
}

func wordsFor(lang string) words {
	if w, ok := languages[lang]; ok {
		return w
	}
	return languages["en"]
}
