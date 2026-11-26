package transcript

import (
	"strings"
	"testing"
	"time"
)

func seg(track Track, speaker int, start, end float64, text string) Segment {
	return Segment{Track: track, Speaker: speaker, Start: dur(start), End: dur(end), Text: text}
}

func dur(s float64) time.Duration { return time.Duration(s * float64(time.Second)) }

func TestMerge(t *testing.T) {
	me := []Segment{seg(Me, 0, 0, 2, "a"), seg(Me, 0, 10, 12, "c")}
	them := []Segment{seg(Them, 1, 5, 8, "b"), seg(Them, 1, 10, 11, "d")}
	got := Merge(me, them)
	var order string
	for _, s := range got {
		order += s.Text
	}
	if order != "abcd" {
		t.Fatalf("order %q", order)
	}
	if me[1].Text != "c" {
		t.Fatal("input modified")
	}
}

// words gives a segment word times, one word every 300 ms from start.
func words(s Segment) Segment {
	at := s.Start
	for _, w := range strings.Fields(s.Text) {
		s.Words = append(s.Words, Word{Start: at, End: at + 250*time.Millisecond, Text: w})
		at += 300 * time.Millisecond
	}
	s.End = at
	return s
}

func texts(segs []Segment) []string {
	var out []string
	for _, s := range segs {
		out = append(out, s.Text)
	}
	return out
}

func TestSuppressEcho(t *testing.T) {
	them := []Segment{
		words(seg(Them, 0, 10, 0, "We should ship the release on Friday, after the review.")),
		words(seg(Them, 0, 30, 0, "Das Budget für das nächste Quartal ist freigegeben.")),
	}
	me := []Segment{
		words(seg(Me, 0, 10.2, 0, "we should ship the release on friday after the review")), // echo
		words(seg(Me, 0, 15, 0, "Okay.")), // short: kept
		words(seg(Me, 0, 18, 0, "I think Friday is too early for that")),
		// The user talking, and the echo of Them inside the same segment.
		words(seg(Me, 0, 29.5, 0, "Ja genau das Budget für das nächste Quartal ist freigegeben super")),
		words(seg(Me, 0, 60, 0, "We should ship the release on Friday")), // same words, far away: kept
	}
	got := texts(SuppressEcho(me, them, DefaultEcho()))
	want := []string{"Okay.", "I think Friday is too early for that", "Ja genau", "super", "We should ship the release on Friday"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("kept %q, want %q", got, want)
	}
}

// Two stretches of the same language share common words; that alone must
// not make one an echo of the other.
func TestSuppressEchoKeepsUnrelatedSpeech(t *testing.T) {
	them := []Segment{words(seg(Them, 0, 0, 0, "und dann ist er in die Stadt gegangen und hat sich dort ein Haus gekauft"))}
	me := []Segment{words(seg(Me, 0, 1, 0, "ich bin in der Stadt und habe mir ein neues Auto angesehen und dann bin ich gegangen"))}
	if got := SuppressEcho(me, them, DefaultEcho()); len(got) != 1 || got[0].Text != me[0].Text {
		t.Fatalf("got %q", texts(got))
	}
}

func TestFormatPlain(t *testing.T) {
	segs := Merge(
		[]Segment{seg(Me, 0, 0, 3, "Hallo zusammen,"), seg(Me, 0, 3.5, 6, "lasst uns anfangen.")},
		[]Segment{seg(Them, 1, 6.5, 30, "Gerne."), seg(Them, 2, 31, 40, "Kurze Frage."), seg(Them, 0, 70, 71, " ")},
	)
	h := Header{Title: "Teams meeting", Start: time.Date(2026, 11, 20, 14, 2, 0, 0, time.Local), End: time.Date(2026, 11, 20, 14, 47, 0, 0, time.Local), Language: "German"}
	got := Format(h, segs, Names{Speakers: map[int]string{2: "Anna"}}, Plain)
	want := "Teams meeting · 2026-11-20 14:02–14:47 · German\n\n" +
		"[00:00] Me: Hallo zusammen, lasst uns anfangen.\n" +
		"[00:06] Speaker 1: Gerne.\n" +
		"[00:31] Anna: Kurze Frage.\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestFormatMarkdown(t *testing.T) {
	segs := []Segment{seg(Me, 0, 0, 1, "One."), seg(Them, 0, 2, 3, "Two.")}
	got := Format(Header{}, segs, Names{}, Markdown)
	want := "**Me** · 00:00  \nOne.\n\n**Them** · 00:02  \nTwo.\n"
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestParagraphGap(t *testing.T) {
	segs := []Segment{seg(Me, 0, 0, 1, "a"), seg(Me, 0, 10, 11, "b")}
	if n := len(Paragraphs(segs, Names{}, 3*time.Second)); n != 2 {
		t.Fatalf("%d paragraphs, want a break after a long gap", n)
	}
}

func TestClock(t *testing.T) {
	for d, want := range map[time.Duration]string{0: "00:00", 65 * time.Second: "01:05", 3725 * time.Second: "1:02:05"} {
		if got := Clock(d); got != want {
			t.Errorf("Clock(%v) = %s, want %s", d, got, want)
		}
	}
}

func TestTrim(t *testing.T) {
	segs := []Segment{
		words(seg(Me, 0, 0, 0, "eins zwei drei vier")),      // 0.0..1.2 s
		words(seg(Them, 1, 5, 0, "fünf sechs sieben acht")), // 5.0..6.2 s
		seg(Them, 2, 10, 12, "wordless segment at end"),     // 10..12 s
	}
	// Untrimmed returns input unchanged.
	if got := Trim(segs, 0, 0); len(got) != 3 {
		t.Fatalf("untrimmed len %d", len(got))
	}
	// Trim middle: cuts into first segment's last 2 words and drops the last segment.
	got := Trim(segs, dur(0.6), dur(8))
	if len(got) != 2 {
		t.Fatalf("trimmed len %d: %+v", len(got), got)
	}
	if got[0].Text != "drei vier" || got[1].Text != "fünf sechs sieben acht" {
		t.Fatalf("trimmed texts %q", texts(got))
	}
	// Original untouched.
	if segs[0].Text != "eins zwei drei vier" {
		t.Fatal("input mutated")
	}
}
