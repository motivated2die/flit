package session

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/motivated2die/flit/internal/audio/track"
	"github.com/motivated2die/flit/internal/pipeline"
	"github.com/motivated2die/flit/internal/transcript"
)

// Languages names the meeting languages for transcript headers.
var Languages = map[string]string{"de": "German", "en": "English"}

// Result is transcript.json.
type Result struct {
	Segments []transcript.Segment `json:"segments"`
}

// Transcribe turns the session's tracks into a transcript: both tracks
// through the pipeline, echo removed from the mic track, merged, and saved
// as transcript.json, .txt and .md. progress gets 0..1 over both tracks,
// weighted by their chunk counts. The session's state is saved along the
// way; on failure it is Failed with the error and one more retry counted.
func (s *Session) Transcribe(ctx context.Context, e pipeline.Engine, opt pipeline.Options, title string, progress func(float64)) (err error) {
	s.Meta.State, s.Meta.Progress, s.Meta.Error = Transcribing, 0, ""
	if opt.Language == "" {
		opt.Language = s.Meta.Language
	}
	_ = s.Save()
	defer func() {
		switch {
		case err != nil && ctx.Err() != nil:
			// Stopped (flit quitting), not failed: picked up again next time.
			s.Meta.State, s.Meta.Progress = Recorded, 0
		case err != nil:
			s.Meta.State, s.Meta.Error = Failed, err.Error()
			s.Meta.Retries++
		default:
			s.Meta.State, s.Meta.Progress, s.Meta.Error = Ready, 1, ""
		}
		_ = s.Save()
	}()

	tracks := []struct {
		side  string
		track transcript.Track
	}{{"me", transcript.Me}, {"them", transcript.Them}}
	var results [2][]transcript.Segment
	var themPCM []float32
	diar, canDiarize := e.(pipeline.Diarizer)
	speakers := opt.Speakers && canDiarize
	// Each track gets half of the bar; with speakers, 40% each and the
	// last 20% for telling them apart (it runs about as fast as ASR).
	share := 0.5
	if speakers {
		share = 0.4
	}
	setProgress := func(p float64) {
		s.Meta.Progress = p
		if progress != nil {
			progress(p)
		}
	}
	report := func(trackIdx int, done, total int64) {
		setProgress(share * (float64(trackIdx) + float64(done)/float64(max(1, total))))
	}
	// Whatever was transcribed during the meeting (or before a stop) is
	// kept; the rest is transcribed now, chunk by chunk, read from the
	// files, saving the state after each chunk.
	live := s.loadLive(opt.Language)
	for i, t := range tracks {
		file := s.TrackFile(t.side)
		if file == "" {
			continue
		}
		f, err := track.OpenMono(s.Path(file), pipeline.Rate)
		if err != nil {
			return fmt.Errorf("%s: %w", file, err)
		}
		p := live.track(t.track)
		_, err = p.Step(ctx, e, f, true, opt, func() { _ = s.saveLive(live) }, func(done, total int64) { report(i, done, total) })
		f.Close()
		if err != nil {
			return err
		}
		results[i] = p.Segments
	}
	if themFile := s.TrackFile("them"); speakers && themFile != "" {
		// Telling speakers apart needs the meeting's track in one piece.
		pcm, err := track.ReadAllMono(s.Path(themFile), pipeline.Rate)
		if err != nil {
			return fmt.Errorf("%s: %w", themFile, err)
		}
		themPCM = pcm
	}
	s.Meta.Speakers, s.Meta.SpeakersError = 0, ""
	if speakers && len(results[1]) > 0 {
		setProgress(2 * share)
		results[1], err = s.assignSpeakers(ctx, diar, themPCM, results[1])
		if err != nil {
			if ctx.Err() != nil {
				return err
			}
			// The transcript stands without speakers; say why.
			s.Meta.SpeakersError = err.Error()
			err = nil
		}
	}
	me := transcript.SuppressEcho(results[0], results[1], transcript.DefaultEcho())
	merged := transcript.Merge(me, results[1])
	if err := s.writeTranscript(merged, title); err != nil {
		return err
	}
	s.DiscardLive() // done: a new transcription starts from the beginning
	return nil
}

// assignSpeakers diarizes the meeting track and labels its segments.
func (s *Session) assignSpeakers(ctx context.Context, d pipeline.Diarizer, pcm []float32, segs []transcript.Segment) ([]transcript.Segment, error) {
	turns, err := d.Diarize(ctx, pcm)
	if err != nil {
		return segs, fmt.Errorf("telling speakers apart: %w", err)
	}
	segs = transcript.AssignSpeakers(segs, pipeline.Turns(turns))
	n := map[int]bool{}
	for _, seg := range segs {
		if seg.Speaker > 0 {
			n[seg.Speaker] = true
		}
	}
	s.Meta.Speakers = len(n)
	// Numbers from an earlier run may now be other people.
	names := s.Names()
	if names.Speakers != nil {
		names.Speakers = nil
		if err := s.saveNames(names); err != nil {
			return segs, err
		}
	}
	return segs, nil
}

// FindSpeakers tells the speakers of an already transcribed session apart
// (its meeting track must still be there) and rewrites the transcript. The
// words stay as they are.
func (s *Session) FindSpeakers(ctx context.Context, d pipeline.Diarizer) error {
	themFile := s.TrackFile("them")
	if themFile == "" {
		return fmt.Errorf("the meeting's recording was not kept")
	}
	segs, err := s.Segments()
	if err != nil {
		return err
	}
	pcm, err := track.ReadAllMono(s.Path(themFile), pipeline.Rate)
	if err != nil {
		return fmt.Errorf("%s: %w", themFile, err)
	}
	var me, them []transcript.Segment
	for _, seg := range segs {
		if seg.Track == transcript.Me {
			me = append(me, seg)
		} else {
			them = append(them, seg)
		}
	}
	them, err = s.assignSpeakers(ctx, d, pcm, them)
	if err != nil {
		s.Meta.SpeakersError = err.Error()
		_ = s.Save()
		return err
	}
	s.Meta.SpeakersError = ""
	if err := s.Save(); err != nil {
		return err
	}
	return s.writeTranscript(transcript.Merge(me, them), "")
}

// Segments reads transcript.json.
func (s *Session) Segments() ([]transcript.Segment, error) {
	b, err := os.ReadFile(s.Path(TranscriptJSON))
	if err != nil {
		return nil, err
	}
	var r Result
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("%s: %w", TranscriptJSON, err)
	}
	return r.Segments, nil
}

// SetTitle renames the session and rewrites its transcripts' header line.
// An empty title goes back to the default (the meeting app's name).
func (s *Session) SetTitle(title string) error {
	s.Meta.Title = strings.TrimSpace(title)
	s.Meta.TitleByUser = s.Meta.Title != ""
	return s.saveAndReformat()
}

// SetMeeting records which calendar meeting this was (nil: none), or the
// choices when it isn't clear. A meeting names the recording unless the
// user named it. byUser marks the user's choice, which later matching
// leaves alone.
func (s *Session) SetMeeting(m *MeetingInfo, choices []MeetingInfo, byUser bool) error {
	s.Meta.Meeting, s.Meta.MeetingChoices = m, choices
	if byUser {
		s.Meta.MeetingChosen, s.Meta.MeetingChoices = true, nil
	}
	if m != nil && !s.Meta.TitleByUser {
		s.Meta.Title = m.Title
	}
	return s.saveAndReformat()
}

// saveAndReformat saves meta.json and, when there is a transcript,
// rewrites its header line.
func (s *Session) saveAndReformat() error {
	if err := s.Save(); err != nil {
		return err
	}
	if !s.Has(TranscriptJSON) {
		return nil
	}
	segs, err := s.Segments()
	if err != nil {
		return err
	}
	return s.writeFormatted(segs, "")
}

// Names reads the names the user gave the speakers (none: the defaults).
func (s *Session) Names() transcript.Names {
	var n transcript.Names
	if b, err := os.ReadFile(s.Path(SpeakersFile)); err == nil {
		_ = json.Unmarshal(b, &n)
	}
	return n
}

// SetNames saves the speakers' names and rewrites the formatted
// transcripts with them. Giving two speakers the same name merges them in
// the text.
func (s *Session) SetNames(n transcript.Names) error {
	if err := s.saveNames(n); err != nil {
		return err
	}
	segs, err := s.Segments()
	if err != nil {
		return err
	}
	return s.writeFormatted(segs, "")
}

func (s *Session) saveNames(n transcript.Names) error {
	n.Me, n.Them = strings.TrimSpace(n.Me), strings.TrimSpace(n.Them)
	for k, v := range n.Speakers {
		if v = strings.TrimSpace(v); v == "" {
			delete(n.Speakers, k)
		} else {
			n.Speakers[k] = v
		}
	}
	b, err := json.MarshalIndent(n, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(s.Path(SpeakersFile), b)
}

func (s *Session) writeTranscript(segs []transcript.Segment, title string) error {
	b, err := json.MarshalIndent(Result{Segments: segs}, "", "  ")
	if err != nil {
		return err
	}
	if err := writeAtomic(s.Path(TranscriptJSON), b); err != nil {
		return err
	}
	return s.writeFormatted(segs, title)
}

// writeFormatted writes transcript.txt and .md with the speakers' names.
func (s *Session) writeFormatted(segs []transcript.Segment, title string) error {
	h, names := s.Header(title), s.Names()
	if err := writeAtomic(s.Path(TranscriptText), []byte(transcript.Format(h, segs, names, transcript.Plain))); err != nil {
		return err
	}
	return writeAtomic(s.Path(TranscriptMD), []byte(transcript.Format(h, segs, names, transcript.Markdown)))
}

// writeAtomic replaces a file through a temporary one, so a reader never
// sees half of it.
func writeAtomic(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Header is the transcript's first line.
func (s *Session) Header(title string) transcript.Header {
	if title == "" {
		title = s.Meta.Title
	}
	return transcript.Header{Title: strings.TrimSpace(title), Start: s.Meta.Started, End: s.Meta.Ended, Language: Languages[s.Meta.Language]}
}

// Transcript reads the formatted transcript (markdown or plain).
func (s *Session) Transcript(markdown bool) (string, error) {
	name := TranscriptText
	if markdown {
		name = TranscriptMD
	}
	b, err := os.ReadFile(s.Path(name))
	return string(b), err
}
