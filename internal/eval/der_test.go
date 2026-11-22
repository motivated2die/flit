package eval

import (
	"testing"
	"time"
)

func turn(spk string, a, b float64) Turn {
	return Turn{Speaker: spk, Start: time.Duration(a * float64(time.Second)), Duration: time.Duration((b - a) * float64(time.Second))}
}

func TestDER(t *testing.T) {
	ref := []Turn{turn("anna", 0, 10), turn("ben", 10, 20)}
	// Perfect, with other labels.
	if d := ScoreDER(ref, []Turn{turn("2", 0, 10), turn("1", 10, 20)}); d.Rate() != 0 || d.Mapping["2"] != "anna" {
		t.Fatalf("perfect: %+v", d)
	}
	// Ben's second half given to a third speaker: 5 s confusion of 20.
	d := ScoreDER(ref, []Turn{turn("1", 0, 10), turn("2", 10, 15), turn("3", 15, 20)})
	if r := d.Rate(); r < 0.249 || r > 0.251 {
		t.Fatalf("split: %v %+v", r, d)
	}
	// Both merged into one: 10 s confusion.
	d = ScoreDER(ref, []Turn{turn("1", 0, 20)})
	if r := d.Rate(); r < 0.499 || r > 0.501 {
		t.Fatalf("merged: %v", r)
	}
	// Missed and false alarm.
	d = ScoreDER(ref, []Turn{turn("1", 2, 10), turn("2", 10, 22)})
	if d.Missed != 2*time.Second || d.FalseAlarm != 2*time.Second || d.Confusion != 0 {
		t.Fatalf("miss/fa: %+v", d)
	}
}

func TestAttribution(t *testing.T) {
	// Hypothesis 1 = anna (3 words, 1 wrong), 2 = ben, 3 = a split of ben.
	a := ScoreAttribution([][2]string{{"anna", "1"}, {"anna", "1"}, {"ben", "1"}, {"ben", "2"}, {"ben", "2"}, {"ben", "3"}})
	if a.Correct != 4 || a.Mapping["1"] != "anna" || a.Mapping["2"] != "ben" {
		t.Fatalf("%+v", a)
	}
}
