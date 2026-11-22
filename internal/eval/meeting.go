// Package eval holds test meetings with their references and, later, the
// scoring of transcripts against them (WER, speaker attribution, DER).
//
// A test meeting is a folder (built by `go run ./cmd/flitdev testdata`):
//
//	meeting.json     this Manifest: source, license, tracks, reference segments
//	me.wav           the local speaker's track ("Me")
//	them.wav         everyone else, mixed ("Them")
//	mix.wav          both, as one recording would hear them
//	me-bleed.wav     optional: Me with Them leaking in, as from loudspeakers
//	reference.rttm   who spoke when (NIST RTTM)
package eval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Manifest describes one test meeting.
type Manifest struct {
	Name        string             `json:"name"`
	Description string             `json:"description"`
	Language    string             `json:"language"`
	Source      string             `json:"source"`
	License     string             `json:"license"`
	Attribution string             `json:"attribution"`
	Seconds     float64            `json:"seconds"`
	Tracks      map[string]string  `json:"tracks"` // role → file: me, them, mix, me-bleed
	Speakers    []Speaker          `json:"speakers"`
	Segments    []ReferenceSegment `json:"segments"`
}

// Speaker is one person in the meeting.
type Speaker struct {
	ID    string `json:"id"`    // as in the RTTM
	Track string `json:"track"` // "me" or "them"
	Label string `json:"label"` // e.g. "Them 2", in order of first appearance on Them
}

// ReferenceSegment is what a speaker said, and when.
type ReferenceSegment struct {
	Speaker string        `json:"speaker"`
	Start   time.Duration `json:"start"`
	End     time.Duration `json:"end"`
	Text    string        `json:"text"`
}

// Load reads dir/meeting.json.
func Load(dir string) (Manifest, error) {
	var m Manifest
	b, err := os.ReadFile(filepath.Join(dir, "meeting.json"))
	if err != nil {
		return m, err
	}
	return m, json.Unmarshal(b, &m)
}

// Save writes dir/meeting.json.
func (m Manifest) Save(dir string) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "meeting.json"), append(b, '\n'), 0o644)
}

// Track returns a track's file path in dir, or "" if the meeting lacks it.
func (m Manifest) Track(dir, role string) string {
	if f := m.Tracks[role]; f != "" {
		return filepath.Join(dir, f)
	}
	return ""
}

// TrackOf returns the track ("me", "them") a speaker belongs to.
func (m Manifest) TrackOf(speaker string) string {
	for _, s := range m.Speakers {
		if s.ID == speaker {
			return s.Track
		}
	}
	return ""
}
