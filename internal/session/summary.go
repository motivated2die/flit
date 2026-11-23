package session

import (
	"encoding/json"
	"errors"
	"os"
	"time"
)

// SummaryInfo is how a recording's summary was written (summary.json):
// enough to show it, judge it and write it again, never its text.
type SummaryInfo struct {
	Written     time.Time `json:"written"`
	Model       string    `json:"model"`   // models.Model ID
	Runtime     string    `json:"runtime"` // llama.cpp build
	Device      string    `json:"device"`  // cpu, gpu-prefill, gpu
	Approach    string    `json:"approach"`
	Language    string    `json:"language"`
	Instruction string    `json:"instruction,omitempty"` // the user's own addition
	Seconds     float64   `json:"seconds"`               // how long writing it took
	Read        int       `json:"read"`                  // tokens read
	Wrote       int       `json:"wrote"`                 // tokens written
	Edited      bool      `json:"edited,omitempty"`      // the user changed it since
}

// HasSummary reports whether the recording has a non-empty summary on disk.
func (s *Session) HasSummary() bool {
	st, err := os.Stat(s.Path(SummaryFile))
	return err == nil && st.Size() > 0
}

// Summary returns the recording's summary Markdown and how it was written;
// "" when there is none.
func (s *Session) Summary() (string, SummaryInfo, error) {
	var info SummaryInfo
	b, err := os.ReadFile(s.Path(SummaryFile))
	if errors.Is(err, os.ErrNotExist) {
		return "", info, nil
	}
	if err != nil {
		return "", info, err
	}
	if ib, err := os.ReadFile(s.Path(SummaryInfoFile)); err == nil {
		json.Unmarshal(ib, &info)
	}
	return string(b), info, nil
}

// SaveSummary writes the summary and how it was written.
func (s *Session) SaveSummary(md string, info SummaryInfo) error {
	b, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	if err := writeAtomic(s.Path(SummaryInfoFile), append(b, '\n')); err != nil {
		return err
	}
	return writeAtomic(s.Path(SummaryFile), []byte(md))
}

// RemoveSummary deletes the summary.
func (s *Session) RemoveSummary() error {
	for _, f := range []string{SummaryFile, SummaryInfoFile} {
		if err := os.Remove(s.Path(f)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}
