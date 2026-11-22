package session

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/motivated2die/flit/internal/audio/wav"
	"github.com/motivated2die/flit/internal/pipeline"
	"github.com/motivated2die/flit/internal/transcript"
)

// LiveFile holds what was transcribed so far: during the meeting (live
// transcription) and during the final transcription, saved after every
// chunk, so neither has to start over after a stop or a crash.
const LiveFile = "live.json"

// liveState is live.json.
type liveState struct {
	Language string               `json:"language"`
	Model    string               `json:"model,omitempty"`
	Me       pipeline.Progressive `json:"me"`
	Them     pipeline.Progressive `json:"them"`
}

func (l *liveState) track(t transcript.Track) *pipeline.Progressive {
	if t == transcript.Me {
		return &l.Me
	}
	return &l.Them
}

// loadLive reads live.json if it was made for the same language and model
// (otherwise its text would be the wrong one); else an empty state.
func (s *Session) loadLive(language string) *liveState {
	fresh := &liveState{Language: language, Model: s.Meta.Model, Me: pipeline.Progressive{Track: transcript.Me}, Them: pipeline.Progressive{Track: transcript.Them}}
	b, err := os.ReadFile(s.Path(LiveFile))
	if err != nil {
		return fresh
	}
	var l liveState
	if json.Unmarshal(b, &l) != nil || l.Language != language || l.Model != s.Meta.Model {
		return fresh
	}
	l.Me.Track, l.Them.Track = transcript.Me, transcript.Them
	return &l
}

func (s *Session) saveLive(l *liveState) error {
	b, err := json.Marshal(l)
	if err != nil {
		return err
	}
	return writeAtomic(s.Path(LiveFile), b)
}

// DiscardLive forgets what was transcribed so far (before transcribing
// again from the start).
func (s *Session) DiscardLive() { _ = os.Remove(s.Path(LiveFile)) }

// LiveProgress is how far live transcription has come: the time up to
// which both tracks are transcribed (zero: nothing yet).
func (s *Session) LiveProgress() time.Duration {
	b, err := os.ReadFile(s.Path(LiveFile))
	if err != nil {
		return 0
	}
	var l liveState
	if json.Unmarshal(b, &l) != nil {
		return 0
	}
	done := int64(-1)
	for _, t := range []struct {
		file string
		p    pipeline.Progressive
	}{{MeFile, l.Me}, {ThemFile, l.Them}} {
		if s.Has(t.file) && (done < 0 || t.p.Done < done) {
			done = t.p.Done
		}
	}
	if done <= 0 {
		return 0
	}
	return time.Duration(done) * time.Second / pipeline.Rate
}

// TranscribeLive transcribes, while the session is still being recorded,
// the chunks of each track that are certain (one step). It is meant to be
// called every few seconds from one goroutine, with an engine that is
// limited so the meeting isn't slowed down; the final Transcribe goes on
// from where it stopped.
func (s *Session) TranscribeLive(ctx context.Context, e pipeline.Engine, opt pipeline.Options) (int, error) {
	if opt.Language == "" {
		opt.Language = s.Meta.Language
	}
	l := s.loadLive(opt.Language)
	total := 0
	for _, t := range []struct {
		file  string
		track transcript.Track
	}{{MeFile, transcript.Me}, {ThemFile, transcript.Them}} {
		if !s.Has(t.file) {
			continue
		}
		f, err := wav.Open(s.Path(t.file))
		if err != nil {
			continue // not written far enough yet
		}
		n, err := l.track(t.track).Step(ctx, e, f, false, opt, func() { _ = s.saveLive(l) }, nil)
		f.Close()
		total += n
		if err != nil {
			return total, fmt.Errorf("%s: %w", t.file, err)
		}
	}
	return total, nil
}
