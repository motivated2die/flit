package eval

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

const sample = `SPEAKER IS1009a 1 55.120 2.540 <NA> <NA> FIE088 <NA> <NA>
SPEAKER IS1009a 1 50.000 1.000 <NA> <NA> FIO087 <NA> <NA>
SPKR-INFO IS1009a 1 <NA> <NA> <NA> unknown FIE088 <NA>
`

func TestRTTMRoundTrip(t *testing.T) {
	turns, err := ReadRTTM(strings.NewReader(sample))
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 2 || turns[0].Speaker != "FIO087" || turns[1].Start != 55120*time.Millisecond || turns[1].Duration != 2540*time.Millisecond {
		t.Fatalf("turns %+v", turns)
	}
	var b bytes.Buffer
	WriteRTTM(&b, "IS1009a", turns)
	again, _ := ReadRTTM(&b)
	if len(again) != 2 || again[1] != turns[1] {
		t.Fatalf("round trip %+v", again)
	}
}

func TestClip(t *testing.T) {
	turns := []Turn{{"a", 10 * time.Second, 5 * time.Second}, {"b", 30 * time.Second, 5 * time.Second}}
	got := Clip(turns, 12*time.Second, 32*time.Second)
	want := []Turn{{"a", 0, 3 * time.Second}, {"b", 18 * time.Second, 2 * time.Second}}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("Clip = %+v", got)
	}
}
