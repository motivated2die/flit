package eval

import (
	"bufio"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Turn is one RTTM SPEAKER line: who spoke from Start for Duration.
type Turn struct {
	Speaker  string
	Start    time.Duration
	Duration time.Duration
}

// End is when the turn ends.
func (t Turn) End() time.Duration { return t.Start + t.Duration }

// ReadRTTM parses SPEAKER lines of an RTTM file (other line types are
// skipped). Turns come back sorted by start.
func ReadRTTM(r io.Reader) ([]Turn, error) {
	var out []Turn
	sc := bufio.NewScanner(r)
	for line := 1; sc.Scan(); line++ {
		f := strings.Fields(sc.Text())
		if len(f) == 0 || f[0] != "SPEAKER" {
			continue
		}
		if len(f) < 8 {
			return nil, fmt.Errorf("rttm line %d: %d fields", line, len(f))
		}
		start, err1 := strconv.ParseFloat(f[3], 64)
		dur, err2 := strconv.ParseFloat(f[4], 64)
		if err1 != nil || err2 != nil {
			return nil, fmt.Errorf("rttm line %d: bad times", line)
		}
		out = append(out, Turn{Speaker: f[7], Start: seconds(start), Duration: seconds(dur)})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	return out, sc.Err()
}

// WriteRTTM writes turns as SPEAKER lines for file id.
func WriteRTTM(w io.Writer, id string, turns []Turn) error {
	for _, t := range turns {
		if _, err := fmt.Fprintf(w, "SPEAKER %s 1 %.3f %.3f <NA> <NA> %s <NA> <NA>\n", id, t.Start.Seconds(), t.Duration.Seconds(), t.Speaker); err != nil {
			return err
		}
	}
	return nil
}

// Clip keeps the turns' parts inside [from, to) and shifts them to start
// at from.
func Clip(turns []Turn, from, to time.Duration) []Turn {
	var out []Turn
	for _, t := range turns {
		s, e := max(t.Start, from), min(t.End(), to)
		if e > s {
			out = append(out, Turn{Speaker: t.Speaker, Start: s - from, Duration: e - s})
		}
	}
	return out
}

func seconds(s float64) time.Duration { return time.Duration(s * float64(time.Second)) }
