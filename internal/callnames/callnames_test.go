package callnames

import (
	"strings"
	"testing"
	"time"

	"github.com/motivated2die/flit/internal/transcript"
)

func sec(f float64) time.Duration { return time.Duration(f * float64(time.Second)) }

// talk is a meeting-track segment of speaker spk from a to b seconds, one
// word every half second (the words' text from text, repeated).
func talk(spk int, a, b float64, text string) transcript.Segment {
	s := transcript.Segment{Track: transcript.Them, Speaker: spk, Start: sec(a), End: sec(b)}
	ws := strings.Fields(text)
	for i, t := 0, a; t+0.4 <= b; i, t = i+1, t+0.5 {
		w := "w"
		if len(ws) > 0 {
			w = ws[i%len(ws)]
		}
		s.Words = append(s.Words, transcript.Word{Start: sec(t), End: sec(t + 0.4), Text: w})
	}
	return s
}

// marked: readings every 0.4 s from a to b naming who as talking.
func marked(who string, a, b float64) []Reading {
	var rs []Reading
	for t := a; t < b; t += 0.4 {
		rs = append(rs, Reading{At: sec(t), Speaking: []string{who}, Participants: []string{"Me Myself", "Anna", "Ben", "Cleo"}, Me: "Me Myself"})
	}
	return rs
}

func TestMarksNeedExactlyOneOther(t *testing.T) {
	o := DefaultOptions()
	rs := []Reading{
		{At: sec(10), Speaking: []string{"Anna"}},
		{At: sec(10.4), Speaking: []string{"Anna", "Me Myself"}, Me: "Me Myself"}, // the user doesn't count
		{At: sec(10.8), Speaking: []string{"Anna", "Ben"}},                        // two at once: nothing
		{At: sec(11.2), Speaking: nil},
		{At: sec(20), Speaking: []string{"Ben"}}, // the last one holds 2 s
	}
	ev := Marks(rs, o)
	if len(ev) != 2 {
		t.Fatalf("got %d stretches, want 2: %+v", len(ev), ev)
	}
	if ev[0].Name != "Anna" || ev[0].Start != sec(10)-o.Lag || ev[0].End != sec(10.8)-o.Lag {
		t.Errorf("Anna: %+v", ev[0])
	}
	if ev[1].Name != "Ben" || ev[1].End-ev[1].Start != o.Hold {
		t.Errorf("Ben: %+v", ev[1])
	}
	o.MyNames = []string{"anna"}
	if ev := Marks(rs[:1], o); len(ev) != 0 {
		t.Errorf("the user's own name named someone: %+v", ev)
	}
}

func TestAssignNamesVoicesAndMergesSplits(t *testing.T) {
	segs := []transcript.Segment{
		talk(1, 0, 20, ""), talk(2, 21, 40, ""), talk(3, 41, 60, ""), // speaker 3 is Anna again (a split)
		talk(1, 61, 70, ""),
	}
	var rs []Reading
	rs = append(rs, marked("Anna", 0.75, 20.75)...)
	rs = append(rs, marked("Ben", 21.75, 40.75)...)
	rs = append(rs, marked("Anna", 41.75, 60.75)...)
	res := Assign(segs, Marks(rs, DefaultOptions()), rs, DefaultOptions())
	for spk, want := range map[int]string{1: "Anna", 2: "Ben", 3: "Anna"} {
		if got := res.Speakers[spk]; got.Name != want || got.Source != FromMarks {
			t.Errorf("speaker %d = %+v, want %s", spk, got, want)
		}
	}
	if len(res.Suggest) != 1 || res.Suggest[0] != "Cleo" {
		t.Errorf("suggestions %v, want [Cleo]", res.Suggest)
	}
}

func TestAssignFlagsAMergedVoice(t *testing.T) {
	// One label holds Anna and Ben about equally: no name, flagged.
	segs := []transcript.Segment{talk(1, 0, 20, ""), talk(1, 21, 38, ""), talk(2, 40, 60, "")}
	var rs []Reading
	rs = append(rs, marked("Anna", 0.75, 20.75)...)
	rs = append(rs, marked("Ben", 21.75, 38.75)...)
	rs = append(rs, marked("Cleo", 40.75, 60.75)...)
	res := Assign(segs, Marks(rs, DefaultOptions()), rs, DefaultOptions())
	if p, ok := res.Speakers[1]; ok {
		t.Errorf("merged voice named %+v", p)
	}
	if len(res.Flagged) != 1 || res.Flagged[0] != 1 {
		t.Errorf("flagged %v, want [1]", res.Flagged)
	}
	if res.Speakers[2].Name != "Cleo" {
		t.Errorf("speaker 2 = %+v", res.Speakers[2])
	}
}

func TestAssignNeedsEnoughEvidence(t *testing.T) {
	segs := []transcript.Segment{talk(1, 0, 30, ""), talk(2, 31, 60, "")}
	rs := marked("Anna", 0.75, 4.75) // 4 s, of which the first second is the onset
	res := Assign(segs, Marks(rs, DefaultOptions()), rs, DefaultOptions())
	if len(res.Speakers) != 0 {
		t.Errorf("named on too little evidence: %+v", res.Speakers)
	}
}

func TestAssignTwoPersonCall(t *testing.T) {
	segs := []transcript.Segment{{Track: transcript.Them, Start: 0, End: sec(30), Words: talk(0, 0, 30, "").Words}}
	var rs []Reading
	for t := 0.0; t < 30; t += 0.4 {
		rs = append(rs, Reading{At: sec(t), Participants: []string{"Me Myself (You)", "Dana Smith"}, Me: "Me Myself (You)"})
	}
	res := Assign(segs, nil, rs, DefaultOptions())
	if res.Them.Name != "Dana Smith" || res.Them.Source != FromRoster {
		t.Errorf("Them = %+v", res.Them)
	}
	// A third person at any point: no longer a two-person call.
	rs = append(rs, Reading{At: sec(31), Participants: []string{"Me Myself (You)", "Dana Smith", "Eve"}, Me: "Me Myself (You)"})
	if res := Assign(segs, nil, rs, DefaultOptions()); res.Them.Name != "" {
		t.Errorf("three people, Them = %+v", res.Them)
	}
}

func TestCaptionsAlignWithTheTranscript(t *testing.T) {
	segs := []transcript.Segment{
		talk(1, 0, 6, "we should ship the release on friday after the review"),
		talk(2, 6, 12, "i can prepare the changelog and the migration notes"),
	}
	caps := []Caption{
		// seen late, one word misheard by the app ("shipped"), one missing
		{At: sec(9), Name: "Anna", Text: "We should shipped the release on Friday after review."},
		{At: sec(14), Name: "Ben", Text: "I can prepare the changelog and the migration notes."},
		{At: sec(14), Name: "Cleo", Text: "Completely unrelated words here today."},
	}
	ev := Captions(caps, segs, DefaultOptions())
	got := map[string]time.Duration{}
	for _, e := range ev {
		got[e.Name] += e.End - e.Start
		if e.Name == "Anna" && e.End > sec(6) || e.Name == "Ben" && e.Start < sec(6) {
			t.Errorf("caption placed on the wrong words: %+v", e)
		}
	}
	if got["Anna"] < sec(3) || got["Ben"] < sec(3) || got["Cleo"] != 0 {
		t.Errorf("evidence per name %v", got)
	}
}

func TestSame(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{{"release", "release", true}, {"release", "releases", true}, {"meeting", "meetings", true}, {"ship", "shop", false}, {"friday", "monday", false}, {"", "", false}} {
		if got := same(c.a, c.b); got != c.want {
			t.Errorf("same(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

func TestRefineMovesClearLines(t *testing.T) {
	// Voice 1 is Anna but holds one line of Ben's; voice 2 is Ben.
	segs := []transcript.Segment{talk(1, 0, 20, ""), talk(1, 21, 27, ""), talk(2, 30, 50, "")}
	var rs []Reading
	rs = append(rs, marked("Anna", 0.75, 20.75)...)
	rs = append(rs, marked("Ben", 21.75, 27.75)...)
	rs = append(rs, marked("Ben", 30.75, 50.75)...)
	o := DefaultOptions()
	o.MinMargin = 1.5 // 19 s of Anna against 5 s of Ben on voice 1
	ev := Marks(rs, o)
	res := Assign(segs, ev, rs, o)
	if res.Speakers[1].Name != "Anna" || res.Speakers[2].Name != "Ben" {
		t.Fatalf("voices %+v", res.Speakers)
	}
	out := Refine(segs, ev, &res, o)
	if out[0].Speaker != 1 || out[1].Speaker != 2 || out[2].Speaker != 2 {
		t.Errorf("speakers after Refine: %d %d %d, want 1 2 2", out[0].Speaker, out[1].Speaker, out[2].Speaker)
	}
	if segs[1].Speaker != 1 {
		t.Error("Refine changed its input")
	}
}
