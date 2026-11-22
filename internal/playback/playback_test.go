package playback

import (
	"slices"
	"testing"
)

func TestCursorRangesWithGaps(t *testing.T) {
	c := newCursor([]Range{{100, 250}, {1000, 1100}, {5, 5}})
	type piece struct {
		tl, n  int64
		silent bool
	}
	var got []piece
	for {
		tl, n, silent := c.next(100)
		if n == 0 {
			break
		}
		got = append(got, piece{tl, n, silent})
	}
	want := []piece{{100, 100, false}, {200, 50, false}}
	for left := int64(gap); left > 0; left -= min(100, left) {
		want = append(want, piece{1000, min(100, left), true})
	}
	want = append(want, piece{1000, 100, false})
	if !slices.Equal(got, want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}
}

func TestCursorRest(t *testing.T) {
	c := newCursor([]Range{{0, 1000}, {2000, 3000}})
	c.next(600)
	if got := c.rest(450); !slices.Equal(got, []Range{{450, 1000}, {2000, 3000}}) {
		t.Fatalf("%v", got)
	}
	c.next(400) // the first range is done; position still in it
	if got := c.rest(900); !slices.Equal(got, []Range{{900, 1000}, {2000, 3000}}) {
		t.Fatalf("%v", got)
	}
}

func TestTimeline(t *testing.T) {
	var tl timeline
	tl.add(16000, 1600, false) // 1.0 s → 1.1 s
	tl.add(48000, 800, true)   // silence before 3.0 s
	tl.add(48000, 1600, false)
	for _, c := range []struct{ played, want int64 }{
		{0, 16000}, {800, 16800}, {1600, 48000}, {2000, 48000}, {2400, 48000}, {3000, 48600}, {9999, 49600},
	} {
		if got, ok := tl.at(c.played); !ok || got != c.want {
			t.Errorf("played %d: %d, want %d", c.played, got, c.want)
		}
	}
	tl.reset()
	if _, ok := tl.at(0); ok {
		t.Fatal("empty timeline maps")
	}
}

func TestMix(t *testing.T) {
	dst := make([]float32, 3)
	mix(dst, [][]float32{{0.5, 0.8, 0.1}, {0.25, 0.8}})
	if !slices.Equal(dst, []float32{0.75, 1, 0.1}) {
		t.Fatalf("%v", dst)
	}
}
