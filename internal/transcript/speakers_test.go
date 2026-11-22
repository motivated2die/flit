package transcript

import (
	"slices"
	"testing"
	"time"
)

func turn(spk int, a, b float64) Turn { return Turn{Start: dur(a), End: dur(b), Speaker: spk} }

func speakersOf(segs []Segment) []int {
	var out []int
	for _, s := range segs {
		out = append(out, s.Speaker)
	}
	return out
}

func TestAssignSpeakersSplitsAtTurns(t *testing.T) {
	// One ASR segment spanning two speakers' turns without a pause: 10
	// words of 0.3 s from 0 s, the change at 1.5 s (after word 5).
	segs := []Segment{words(seg(Them, 0, 0, 0, "eins zwei drei vier fünf sechs sieben acht neun zehn"))}
	got := AssignSpeakers(segs, []Turn{turn(3, 0, 1.5), turn(1, 1.5, 3.2)})
	if !slices.Equal(texts(got), []string{"eins zwei drei vier fünf", "sechs sieben acht neun zehn"}) {
		t.Fatalf("%q", texts(got))
	}
	// Renumbered by first appearance: the diarizer's 3 is the first voice.
	if !slices.Equal(speakersOf(got), []int{1, 2}) {
		t.Fatalf("speakers %v", speakersOf(got))
	}
	if got[1].Start != dur(1.5) || got[0].End != got[0].Words[4].End {
		t.Fatalf("times %v %v", got[1].Start, got[0].End)
	}
}

func TestAssignSpeakersKeepsWholeSegments(t *testing.T) {
	segs := []Segment{
		words(seg(Them, 0, 0, 0, "Guten Morgen allerseits.")),
		words(seg(Them, 0, 5, 0, "Morgen, schön dass ihr da seid.")),
	}
	got := AssignSpeakers(segs, []Turn{turn(1, 0, 1), turn(2, 5, 7)})
	// Untouched segments keep their own text.
	if !slices.Equal(texts(got), texts(segs)) || !slices.Equal(speakersOf(got), []int{1, 2}) {
		t.Fatalf("%q %v", texts(got), speakersOf(got))
	}
}

func TestAssignSpeakersSmoothsFlickers(t *testing.T) {
	// Speaker 2 for one word in the middle of speaker 1's sentence.
	segs := []Segment{
		words(seg(Them, 0, 0, 0, "das ist ein ganz normaler Satz ohne Wechsel")),
		words(seg(Them, 0, 10, 0, "und hier spricht wirklich jemand anderes für eine Weile")),
	}
	got := AssignSpeakers(segs, []Turn{turn(1, 0, 0.85), turn(2, 0.85, 1.15), turn(1, 1.15, 3), turn(2, 10, 13)})
	if len(got) != 2 || !slices.Equal(speakersOf(got), []int{1, 2}) {
		t.Fatalf("%q %v", texts(got), speakersOf(got))
	}
}

func TestAssignSpeakersDropsFragments(t *testing.T) {
	// Speaker 3 has half a second in a ten-minute meeting: not a person.
	var segs []Segment
	var turns []Turn
	for i := 0; i < 20; i++ {
		start := float64(i) * 30
		segs = append(segs, words(seg(Them, 0, start, 0, "ein Satz mit genau acht Wörtern darin hier")))
		turns = append(turns, turn(1+i%2, start, start+2.4))
	}
	segs = append(segs, words(seg(Them, 0, 700, 0, "ja")))
	turns = append(turns, turn(3, 700, 700.3))
	got := AssignSpeakers(segs, turns)
	for _, s := range got {
		if s.Speaker < 1 || s.Speaker > 2 {
			t.Fatalf("speaker %d survived: %q", s.Speaker, s.Text)
		}
	}
}

func TestAssignSpeakersOneVoiceIsThem(t *testing.T) {
	segs := []Segment{words(seg(Them, 0, 0, 0, "nur eine Person spricht hier"))}
	got := AssignSpeakers(segs, []Turn{turn(2, 0, 2)})
	if got[0].Speaker != 0 {
		t.Fatalf("speaker %d, want 0 (plain Them)", got[0].Speaker)
	}
	if got := AssignSpeakers(segs, nil); got[0].Speaker != 0 {
		t.Fatal("no turns")
	}
}

func TestAssignSpeakersGapsTakeNeighbours(t *testing.T) {
	// The diarizer misses the end of a sentence (last 3 words): they stay
	// with their speaker rather than going to the next one, 9 s later.
	segs := []Segment{
		words(seg(Them, 0, 0, 0, "eins zwei drei vier fünf sechs")),
		words(seg(Them, 0, 10, 0, "sieben acht neun zehn")),
	}
	got := AssignSpeakers(segs, []Turn{turn(1, 0, 0.8), turn(2, 10, 11.5)})
	if len(got) != 2 || got[0].Speaker != 1 || got[0].Text != segs[0].Text {
		t.Fatalf("%q %v", texts(got), speakersOf(got))
	}
}

func TestSpeakersSummary(t *testing.T) {
	segs := Merge(
		[]Segment{seg(Me, 0, 0, 4, "Hallo, ich fange an.")},
		[]Segment{seg(Them, 2, 5, 7, "Kurz."), seg(Them, 1, 8, 20, "Das ist mein längster Beitrag in diesem Meeting.")},
	)
	got := Speakers(segs)
	if len(got) != 3 || got[0].Track != Me || got[1].Speaker != 1 || got[2].Speaker != 2 {
		t.Fatalf("%+v", got)
	}
	if got[1].Quote != "Das ist mein längster Beitrag in diesem Meeting." || got[1].Talk != dur(12) || got[1].First != dur(8) {
		t.Fatalf("%+v", got[1])
	}
	if (Names{Speakers: map[int]string{2: "Anna"}}).Label(Them, 2) != "Anna" || (Names{}).Label(Them, 1) != "Speaker 1" {
		t.Fatal("labels")
	}
}

func TestClips(t *testing.T) {
	segs := Merge(
		[]Segment{seg(Me, 0, 0, 3, "a"), seg(Me, 0, 40, 41, "b")},
		[]Segment{
			seg(Them, 1, 2, 12, "talked into by Me at 2-3"),
			seg(Them, 1, 20, 26, "clear, 6 s"),
			seg(Them, 2, 30, 32, "someone else"),
			seg(Them, 1, 50, 70, "clear, 20 s"),
			seg(Them, 1, 80, 81, "clear, 1 s"),
		},
	)
	got := Clips(segs, Them, 1, 2, 8*time.Second)
	want := []Clip{{dur(20), dur(26)}, {dur(50), dur(58)}}
	if !slices.Equal(got, want) {
		t.Fatalf("%v, want %v", got, want)
	}
	// Me: the 0-3 s segment is talked into; the 40 s one is clear.
	if got := Clips(segs, Me, 0, 3, 8*time.Second); !slices.Equal(got, []Clip{{dur(40), dur(41)}}) {
		t.Fatalf("me: %v", got)
	}
	// Nobody clear: the longest anyway.
	if got := Clips(segs[:2], Them, 1, 1, time.Minute); len(got) != 1 || got[0].Start != dur(2) {
		t.Fatalf("fallback: %v", got)
	}
}
