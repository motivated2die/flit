package eval

import (
	"math"
	"testing"
)

func TestScore(t *testing.T) {
	for _, c := range []struct {
		ref, hyp     string
		sub, del, in int
	}{
		{"the cat sat", "the cat sat", 0, 0, 0},
		{"the cat sat", "the bat sat", 1, 0, 0},
		{"the cat sat on the mat", "the cat on the mat", 0, 1, 0},
		{"the cat", "the big cat", 0, 0, 1},
		{"", "anything", 0, 0, 1},
		{"Er griff alles an, was ins Wasser kam.", "er griff alles an was ins wasser kam", 0, 0, 0},
	} {
		w := ScoreText(c.ref, c.hyp)
		if w.Sub != c.sub || w.Del != c.del || w.Ins != c.in {
			t.Errorf("%q vs %q: %+v", c.ref, c.hyp, w)
		}
	}
	w := ScoreText("a b c d", "a x c")
	if math.Abs(w.Rate()-0.5) > 1e-9 {
		t.Errorf("rate %v", w.Rate())
	}
}

func TestNormalize(t *testing.T) {
	got := Normalize("Größe: T. Rex's Ex-Freund, 2026!")
	want := []string{"größe", "t", "rex's", "ex", "freund", "2026"}
	if len(got) != len(want) {
		t.Fatalf("%q", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%q", got)
		}
	}
}
