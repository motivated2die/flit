package transcript

import (
	"fmt"
	"strings"
	"time"
)

// Header describes the session at the top of a formatted transcript.
type Header struct {
	Title    string // e.g. "Teams meeting"
	Start    time.Time
	End      time.Time
	Language string // display name, e.g. "German"
}

// Names labels speakers. Empty fields fall back to "Me", "Them" and
// "Speaker 1", "Speaker 2", …. Two speakers given the same name read as
// one person (their consecutive turns join into one paragraph).
type Names struct {
	Me       string         `json:"me,omitempty"`
	Them     string         `json:"them,omitempty"`
	Speakers map[int]string `json:"speakers,omitempty"` // Them's diarized speakers by number
}

// Of is the label of a segment's speaker.
func (n Names) Of(s Segment) string { return n.of(s) }

// Label is the label of Me (track Me), an undiarized Them (speaker 0) or
// one of Them's speakers.
func (n Names) Label(track Track, speaker int) string {
	return n.of(Segment{Track: track, Speaker: speaker})
}

func (n Names) of(s Segment) string {
	if s.Track == Me {
		return or(n.Me, "Me")
	}
	if s.Speaker > 0 {
		if name := n.Speakers[s.Speaker]; name != "" {
			return name
		}
		return fmt.Sprintf("Speaker %d", s.Speaker)
	}
	return or(n.Them, "Them")
}

func or(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// Paragraph is consecutive speech of one speaker.
type Paragraph struct {
	Speaker string
	Track   Track
	Start   time.Duration
	End     time.Duration // the end of its last segment
	Text    string
}

// Paragraphs joins consecutive segments of the same speaker that are less
// than maxGap apart (segments must be in time order, as Merge returns them).
func Paragraphs(segs []Segment, names Names, maxGap time.Duration) []Paragraph {
	var out []Paragraph
	var lastEnd time.Duration
	for _, s := range segs {
		text := strings.TrimSpace(s.Text)
		if text == "" {
			continue
		}
		who := names.of(s)
		if n := len(out); n > 0 && out[n-1].Speaker == who && s.Start-lastEnd < maxGap {
			out[n-1].Text += " " + text
			out[n-1].End = max(out[n-1].End, s.End)
		} else {
			out = append(out, Paragraph{Speaker: who, Track: s.Track, Start: s.Start, End: s.End, Text: text})
		}
		lastEnd = max(lastEnd, s.End)
	}
	return out
}

// Style selects the output format.
type Style int

const (
	Plain    Style = iota // [00:06] Speaker 1: text
	Markdown              // **Them 1** · 00:06 \n text
)

// Format renders the transcript for the clipboard.
func Format(h Header, segs []Segment, names Names, style Style) string {
	var b strings.Builder
	if line := headerLine(h); line != "" {
		if style == Markdown {
			b.WriteString("### ")
		}
		b.WriteString(line)
		b.WriteString("\n\n")
	}
	paras := Paragraphs(segs, names, 3*time.Second)
	for i, p := range paras {
		if style == Markdown {
			fmt.Fprintf(&b, "**%s** · %s  \n%s\n", p.Speaker, Clock(p.Start), p.Text)
			if i < len(paras)-1 {
				b.WriteString("\n")
			}
		} else {
			fmt.Fprintf(&b, "[%s] %s: %s\n", Clock(p.Start), p.Speaker, p.Text)
		}
	}
	return b.String()
}

func headerLine(h Header) string {
	var parts []string
	if h.Title != "" {
		parts = append(parts, h.Title)
	}
	if !h.Start.IsZero() {
		when := h.Start.Format("2006-01-02 15:04")
		if !h.End.IsZero() {
			when += "–" + h.End.Format("15:04")
		}
		parts = append(parts, when)
	}
	if h.Language != "" {
		parts = append(parts, h.Language)
	}
	return strings.Join(parts, " · ")
}

// Clock formats an offset as m:ss under an hour, h:mm:ss beyond.
func Clock(d time.Duration) string {
	s := int(d / time.Second)
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%02d:%02d", s/60, s%60)
}
