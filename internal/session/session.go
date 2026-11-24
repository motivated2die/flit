// Package session is one recorded meeting on disk: a folder under
// <data>\sessions with meta.json, the two tracks and, once transcribed, the
// transcript. Its state is saved on every change, so a crash or reboot
// finds it again (Recover).
package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/motivated2die/flit/internal/audio/m4a"
)

// State is where a session is in its life.
type State string

const (
	Recording    State = "recording"
	Recorded     State = "recorded" // waiting for transcription
	Transcribing State = "transcribing"
	Ready        State = "ready"
	Failed       State = "failed"
)

// Files in a session folder.
const (
	MetaFile        = "meta.json"
	MeFile          = "me.wav"
	ThemFile        = "them.wav"
	MeM4A           = "me.m4a"
	ThemM4A         = "them.m4a"
	TranscriptJSON  = "transcript.json"
	TranscriptText  = "transcript.txt"
	TranscriptMD    = "transcript.md"
	SpeakersFile    = "speakers.json" // the names the user gave the speakers
	NotesFile       = "notes.md"      // the user's notes (Markdown)
	NotesTimesFile  = "notes.json"    // when each line of the notes was typed
	SummaryFile     = "summary.md"    // the summary written on request
	SummaryInfoFile = "summary.json"  // how it was written
)

// MaxRetries is how often a failed transcription is retried on start.
const MaxRetries = 3

// Meta is meta.json.
type Meta struct {
	ID       string    `json:"id"`
	Provider string    `json:"provider,omitempty"` // detect.Provider ID; "" for a manual recording
	Title    string    `json:"title,omitempty"`    // a meeting's name, shown to the user
	Language string    `json:"language"`
	Model    string    `json:"model,omitempty"`
	Started  time.Time `json:"started"`
	Ended    time.Time `json:"ended,omitempty"`
	State    State     `json:"state"`
	Error    string    `json:"error,omitempty"`
	Retries  int       `json:"retries,omitempty"`
	Progress float64   `json:"progress,omitempty"` // 0..1 while transcribing
	// ThemSource says how the meeting's audio was recorded: "process" (only
	// the meeting app) or "loopback" (everything the PC played).
	ThemSource string `json:"themSource,omitempty"`
	// Devices lists the devices recorded and when they were switched to.
	Devices []DeviceUse `json:"devices,omitempty"`
	// Speakers is how many people diarization told apart on the meeting
	// track (0: not run, or one voice); SpeakersError why it failed.
	Speakers      int    `json:"speakers,omitempty"`
	SpeakersError string `json:"speakersError,omitempty"`
	// TitleByUser: the user named the recording; the calendar leaves the
	// title alone.
	TitleByUser bool `json:"titleByUser,omitempty"`
	// Meeting is the calendar meeting this recording belongs to (a copy,
	// so it stays after the calendar moves on); MeetingChoices the ones it
	// could be when meetings overlapped; MeetingChosen: the user decided.
	Meeting        *MeetingInfo  `json:"meeting,omitempty"`
	MeetingChoices []MeetingInfo `json:"meetingChoices,omitempty"`
	MeetingChosen  bool          `json:"meetingChosen,omitempty"`
}

// MeetingInfo is a calendar meeting as a recording keeps it.
type MeetingInfo struct {
	UID       string    `json:"uid,omitempty"`
	Title     string    `json:"title"`
	Start     time.Time `json:"start"`
	End       time.Time `json:"end"`
	Recurring bool      `json:"recurring,omitempty"`
	Location  string    `json:"location,omitempty"`
	Link      string    `json:"link,omitempty"`
	Provider  string    `json:"provider,omitempty"`
}

// DeviceUse is a device a track was recorded from, from a moment on.
type DeviceUse struct {
	Track  string    `json:"track"` // "me" or "them"
	Device string    `json:"device"`
	From   time.Time `json:"from"`
}

// Session is a session folder.
type Session struct {
	Dir          string
	Meta         Meta
	notesState   uint8 // 0 = unknown, 1 = none, 2 = present
	summaryState uint8 // 0 = unknown, 1 = none, 2 = present
}

// Root is the sessions folder in the data folder.
func Root(dataDir string) string { return filepath.Join(dataDir, "sessions") }

// Create makes a new session folder named after its start time and
// provider, and saves m (whose ID and State it fills in).
func Create(dataDir string, m Meta) (*Session, error) {
	if m.Started.IsZero() {
		m.Started = time.Now()
	}
	name := m.Started.Format("2006-01-02_1504")
	if m.Provider != "" {
		name += "_" + m.Provider
	}
	dir := filepath.Join(Root(dataDir), name)
	for i := 2; ; i++ {
		if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
			break
		}
		dir = filepath.Join(Root(dataDir), fmt.Sprintf("%s_%d", name, i))
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	m.ID = filepath.Base(dir)
	if m.State == "" {
		m.State = Recording
	}
	s := &Session{Dir: dir, Meta: m}
	return s, s.Save()
}

// Load reads a session folder.
func Load(dir string) (*Session, error) {
	b, err := os.ReadFile(filepath.Join(dir, MetaFile))
	if err != nil {
		return nil, err
	}
	s := &Session{Dir: dir}
	if err := json.Unmarshal(b, &s.Meta); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(dir), err)
	}
	return s, nil
}

// Save writes meta.json atomically.
func (s *Session) Save() error {
	b, err := json.MarshalIndent(s.Meta, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(s.Dir, MetaFile)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Path returns a file in the session folder.
func (s *Session) Path(name string) string { return filepath.Join(s.Dir, name) }

// Has reports whether the session folder holds a file.
func (s *Session) Has(name string) bool {
	_, err := os.Stat(s.Path(name))
	return err == nil
}

// RecordFileNames returns the filenames for a new recording's tracks: 48 kHz
// M4A when Windows Media Foundation AAC is available, falling back to WAV
// otherwise (for example on Windows N editions without the Media Feature Pack).
func RecordFileNames() (me, them string) {
	if m4a.Available() {
		return MeM4A, ThemM4A
	}
	return MeFile, ThemFile
}

// TrackFile returns the filename on disk for side ("me" or "them"), checking
// finalized .m4a, in-progress/crashed .m4a.aac, and legacy .wav in order.
func (s *Session) TrackFile(side string) string {
	var names []string
	if side == "me" {
		names = []string{MeM4A, MeM4A + m4a.ScratchSuffix, MeFile}
	} else {
		names = []string{ThemM4A, ThemM4A + m4a.ScratchSuffix, ThemFile}
	}
	for _, f := range names {
		if s.Has(f) {
			return f
		}
	}
	return ""
}

// HasTrack reports whether side ("me" or "them") has an audio file on disk.
func (s *Session) HasTrack(side string) bool { return s.TrackFile(side) != "" }

// HasAudio reports whether the session has any audio track on disk.
func (s *Session) HasAudio() bool { return s.HasTrack("me") || s.HasTrack("them") }

// AudioFiles returns all audio filenames present in the session folder.
func (s *Session) AudioFiles() []string {
	var out []string
	for _, f := range []string{MeM4A, MeM4A + m4a.ScratchSuffix, MeFile, ThemM4A, ThemM4A + m4a.ScratchSuffix, ThemFile} {
		if s.Has(f) {
			out = append(out, f)
		}
	}
	return out
}

// List returns all sessions, newest first. Unreadable folders are skipped.
func List(dataDir string) []*Session {
	entries, err := os.ReadDir(Root(dataDir))
	if err != nil {
		return nil
	}
	var out []*Session
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if s, err := Load(filepath.Join(Root(dataDir), e.Name())); err == nil {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Meta.Started.After(out[j].Meta.Started) })
	return out
}

// Recover finds sessions an earlier run left unfinished and returns those
// to transcribe: recordings cut off by a crash (their WAV files are
// readable up to the last sync), interrupted transcriptions, and failures
// with retries left. Their state is updated on disk.
func Recover(dataDir string) []*Session {
	var todo []*Session
	for _, s := range List(dataDir) {
		switch s.Meta.State {
		case Recording:
			for _, pair := range [][2]string{{MeM4A + m4a.ScratchSuffix, MeM4A}, {ThemM4A + m4a.ScratchSuffix, ThemM4A}} {
				if s.Has(pair[0]) && !s.Has(pair[1]) {
					_ = m4a.Recover(s.Path(pair[0]), s.Path(pair[1]))
				}
			}
			if s.Meta.Ended.IsZero() {
				var paths []string
				for _, f := range s.AudioFiles() {
					paths = append(paths, s.Path(f))
				}
				s.Meta.Ended = modTime(paths...)
			}
			s.Meta.State = Recorded
		case Transcribing:
			s.Meta.State = Recorded
		case Failed:
			if s.Meta.Retries >= MaxRetries {
				continue
			}
			s.Meta.State = Recorded
		case Recorded:
		default:
			continue
		}
		s.Meta.Progress = 0
		_ = s.Save()
		todo = append(todo, s)
	}
	// Oldest first.
	sort.Slice(todo, func(i, j int) bool { return todo[i].Meta.Started.Before(todo[j].Meta.Started) })
	return todo
}

func modTime(paths ...string) time.Time {
	var t time.Time
	for _, p := range paths {
		if st, err := os.Stat(p); err == nil && st.ModTime().After(t) {
			t = st.ModTime()
		}
	}
	return t
}
