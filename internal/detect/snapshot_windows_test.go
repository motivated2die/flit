package detect

import (
	"os"
	"testing"
)

// Takes a real snapshot (no audio device is opened; sessions are only
// listed). With FLIT_LIVE_DETECT=1 it also prints what it found, which is
// how provider names and sessions are checked during a real meeting.
func TestTakeSnapshot(t *testing.T) {
	s, err := Take()
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Procs) < 10 || s.Procs[s.Self].Exe == "" {
		t.Fatalf("%d processes, self %+v", len(s.Procs), s.Procs[s.Self])
	}
	if len(s.Titles) == 0 {
		t.Log("no visible windows (headless session?)")
	}
	if os.Getenv("FLIT_LIVE_DETECT") == "1" {
		for _, pid := range s.Capturing {
			t.Logf("capturing: %d %s (parent %d %s)", pid, s.Procs[pid].Exe, s.Procs[pid].Parent, s.Procs[s.Procs[pid].Parent].Exe)
		}
		t.Logf("meetings: %+v", Find(s, func(Provider) bool { return true }))
	}
}
