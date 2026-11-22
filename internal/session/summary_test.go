package session

import (
	"testing"
	"time"
)

func TestSummary(t *testing.T) {
	s := &Session{Dir: t.TempDir()}
	if md, _, err := s.Summary(); md != "" || err != nil {
		t.Fatalf("no summary yet: %q, %v", md, err)
	}
	info := SummaryInfo{Written: time.Date(2026, 11, 22, 20, 0, 0, 0, time.UTC), Model: "qwen3.5-2b", Device: "cpu", Language: "de", Read: 2000, Wrote: 500}
	if err := s.SaveSummary("## Zusammenfassung\n\nKurz.\n", info); err != nil {
		t.Fatal(err)
	}
	md, got, err := s.Summary()
	if err != nil || md != "## Zusammenfassung\n\nKurz.\n" || got != info {
		t.Errorf("read back %q, %+v, %v", md, got, err)
	}
	if err := s.RemoveSummary(); err != nil || s.Has(SummaryFile) || s.Has(SummaryInfoFile) {
		t.Errorf("remove: %v", err)
	}
}
