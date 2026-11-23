package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/motivated2die/flit/internal/audio/m4a"
	"github.com/motivated2die/flit/internal/audio/wav"
	"github.com/motivated2die/flit/internal/pipeline"
	"github.com/motivated2die/flit/internal/transcript"
)

func writeWAV(t *testing.T, path string, seconds float64, value float32) {
	t.Helper()
	w, err := wav.Create(path, wav.Format{SampleRate: 16000, Channels: 1})
	if err != nil {
		t.Fatal(err)
	}
	pcm := make([]float32, int(seconds*16000))
	for i := range pcm {
		pcm[i] = value
	}
	w.Write(pcm)
	w.Close()
}

func TestCreateListLoad(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 11, 21, 14, 2, 0, 0, time.Local)
	a, err := Create(dir, Meta{Provider: "teams", Language: "de", Started: at})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Create(dir, Meta{Provider: "teams", Language: "de", Started: at})
	if a.Meta.ID != "2026-11-21_1402_teams" || b.Meta.ID != "2026-11-21_1402_teams_2" || a.Meta.State != Recording {
		t.Fatalf("ids %q %q, state %s", a.Meta.ID, b.Meta.ID, a.Meta.State)
	}
	c, _ := Create(dir, Meta{Language: "en", Started: at.Add(time.Hour)})
	list := List(dir)
	if len(list) != 3 || list[0].Meta.ID != c.Meta.ID {
		t.Fatalf("list %v", list)
	}
	l, err := Load(a.Dir)
	if err != nil || l.Meta.Provider != "teams" {
		t.Fatalf("%+v %v", l, err)
	}
}

func TestRecover(t *testing.T) {
	dir := t.TempDir()
	mk := func(state State, retries int, minutes int) *Session {
		s, _ := Create(dir, Meta{Language: "de", Started: time.Now().Add(time.Duration(minutes) * time.Minute), State: state, Retries: retries})
		return s
	}
	crashed := mk(Recording, 0, -30)
	writeWAV(t, crashed.Path(MeFile), 1, 0)
	interrupted := mk(Transcribing, 0, -20)
	failed := mk(Failed, 1, -10)
	mk(Failed, MaxRetries, -5) // given up
	mk(Ready, 0, -1)
	todo := Recover(dir)
	if len(todo) != 3 || todo[0].Dir != crashed.Dir || todo[1].Dir != interrupted.Dir || todo[2].Dir != failed.Dir {
		var ids []string
		for _, s := range todo {
			ids = append(ids, s.Meta.ID)
		}
		t.Fatalf("recovered %v", ids)
	}
	again, _ := Load(crashed.Dir)
	if again.Meta.State != Recorded || again.Meta.Ended.IsZero() {
		t.Fatalf("crashed session now %+v", again.Meta)
	}
}

// engine hears speech where samples aren't zero and answers each chunk
// with a segment of words.
type engine struct{ fail bool }

func (engine) VAD(_ context.Context, pcm []float32) ([]float32, int, error) {
	probs := make([]float32, len(pcm)/512)
	for i := range probs {
		if pcm[i*512] != 0 {
			probs[i] = 0.9
		}
	}
	return probs, 512, nil
}

func (e engine) Transcribe(_ context.Context, pcm []float32, opt pipeline.Options) ([]pipeline.Segment, error) {
	if e.fail {
		return nil, errors.New("engine down")
	}
	word := "hallo"
	if pcm[0] < 0.15 {
		word = "servus"
	}
	return []pipeline.Segment{{T0: 0, T1: 600, Text: word + " " + opt.Language, Words: []pipeline.Word{{T0: 0, T1: 300, Text: word}, {T0: 300, T1: 600, Text: opt.Language}}}}, nil
}

func TestTranscribe(t *testing.T) {
	dir := t.TempDir()
	s, _ := Create(dir, Meta{Provider: "teams", Language: "de", Started: time.Date(2026, 11, 21, 9, 0, 0, 0, time.Local)})
	writeWAV(t, s.Path(MeFile), 3, 0.1)
	writeWAV(t, s.Path(ThemFile), 3, 0.2)
	var last float64
	if err := s.Transcribe(context.Background(), engine{}, pipeline.Options{}, "Weekly sync", func(p float64) { last = p }); err != nil {
		t.Fatal(err)
	}
	if last != 1 || s.Meta.State != Ready {
		t.Fatalf("progress %v, state %s", last, s.Meta.State)
	}
	text, err := s.Transcript(false)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Weekly sync · 2026-11-21 09:00", "German", "Me: servus de", "Them: hallo de"} {
		if !strings.Contains(text, want) {
			t.Errorf("transcript lacks %q:\n%s", want, text)
		}
	}
	if _, err := os.Stat(filepath.Join(s.Dir, TranscriptMD)); err != nil {
		t.Error(err)
	}
}

func TestTranscribeFailureCountsRetry(t *testing.T) {
	dir := t.TempDir()
	s, _ := Create(dir, Meta{Language: "en"})
	writeWAV(t, s.Path(ThemFile), 2, 0.2)
	if err := s.Transcribe(context.Background(), engine{fail: true}, pipeline.Options{}, "", nil); err == nil {
		t.Fatal("no error")
	}
	l, _ := Load(s.Dir)
	if l.Meta.State != Failed || l.Meta.Retries != 1 || !strings.Contains(l.Meta.Error, "engine down") {
		t.Fatalf("%+v", l.Meta)
	}
}

func TestTranscribeCancelledIsNotAFailure(t *testing.T) {
	dir := t.TempDir()
	s, _ := Create(dir, Meta{Language: "en"})
	writeWAV(t, s.Path(ThemFile), 2, 0.2)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Transcribe(ctx, engine{}, pipeline.Options{}, "", nil); err == nil {
		t.Fatal("no error")
	}
	if s.Meta.State != Recorded || s.Meta.Retries != 0 {
		t.Fatalf("%+v", s.Meta)
	}
}

// diarizing is an engine that also tells speakers apart: the first 300 ms
// are one voice, the rest another (or, with one, a single voice).
type diarizing struct {
	engine
	one  bool
	fail bool
}

func (d diarizing) Diarize(_ context.Context, pcm []float32) ([]pipeline.SpeakerTurn, error) {
	if d.fail {
		return nil, errors.New("no model")
	}
	ms := int64(len(pcm)) * 1000 / pipeline.Rate
	if d.one {
		return []pipeline.SpeakerTurn{{T0: 0, T1: ms, Speaker: 1}}, nil
	}
	return []pipeline.SpeakerTurn{{T0: 0, T1: 300, Speaker: 2}, {T0: 300, T1: ms, Speaker: 1}}, nil
}

func TestTranscribeWithSpeakersAndNames(t *testing.T) {
	s, _ := Create(t.TempDir(), Meta{Language: "de"})
	writeWAV(t, s.Path(MeFile), 3, 0.1)
	writeWAV(t, s.Path(ThemFile), 3, 0.2)
	if err := s.Transcribe(context.Background(), diarizing{}, pipeline.Options{Speakers: true}, "", nil); err != nil {
		t.Fatal(err)
	}
	text, _ := s.Transcript(false)
	if s.Meta.Speakers != 2 || !strings.Contains(text, "Speaker 1: hallo") || !strings.Contains(text, "Speaker 2: de") {
		t.Fatalf("speakers %d:\n%s", s.Meta.Speakers, text)
	}
	// Named: the formatted transcripts change, transcript.json doesn't.
	if err := s.SetNames(transcript.Names{Me: "Moritz", Speakers: map[int]string{2: " Anna "}}); err != nil {
		t.Fatal(err)
	}
	text, _ = s.Transcript(false)
	md, _ := s.Transcript(true)
	if !strings.Contains(text, "Moritz: servus") || !strings.Contains(text, "Anna: de") || !strings.Contains(md, "**Anna**") {
		t.Fatalf("names:\n%s\n%s", text, md)
	}
	if l, _ := Load(s.Dir); l.Names().Speakers[2] != "Anna" {
		t.Fatalf("names not kept: %+v", l.Names())
	}
	// Two speakers with one name read as one person.
	s.SetNames(transcript.Names{Speakers: map[int]string{1: "Anna", 2: "Anna"}})
	if text, _ = s.Transcript(false); !strings.Contains(text, "Anna: hallo de") {
		t.Fatalf("merged:\n%s", text)
	}
	// Found again (one voice now): numbers are reset, names with them.
	if err := s.FindSpeakers(context.Background(), diarizing{one: true}); err != nil {
		t.Fatal(err)
	}
	text, _ = s.Transcript(false)
	if s.Meta.Speakers != 0 || !strings.Contains(text, "Them: hallo de") || len(s.Names().Speakers) != 0 {
		t.Fatalf("speakers %d, names %+v:\n%s", s.Meta.Speakers, s.Names(), text)
	}
}

func TestSpeakersFailureKeepsTranscript(t *testing.T) {
	s, _ := Create(t.TempDir(), Meta{Language: "de"})
	writeWAV(t, s.Path(ThemFile), 3, 0.2)
	if err := s.Transcribe(context.Background(), diarizing{fail: true}, pipeline.Options{Speakers: true}, "", nil); err != nil {
		t.Fatal(err)
	}
	text, _ := s.Transcript(false)
	if s.Meta.State != Ready || !strings.Contains(s.Meta.SpeakersError, "no model") || !strings.Contains(text, "Them: hallo de") {
		t.Fatalf("%+v\n%s", s.Meta, text)
	}
}

func TestSetTitleRewritesHeader(t *testing.T) {
	s, _ := Create(t.TempDir(), Meta{Provider: "teams", Language: "de"})
	writeWAV(t, s.Path(ThemFile), 3, 0.2)
	if err := s.Transcribe(context.Background(), engine{}, pipeline.Options{}, "", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.SetTitle("  Budget review "); err != nil {
		t.Fatal(err)
	}
	text, _ := s.Transcript(false)
	if !strings.HasPrefix(text, "Budget review · ") {
		t.Fatalf("header: %q", strings.SplitN(text, "\n", 2)[0])
	}
	if l, _ := Load(s.Dir); l.Meta.Title != "Budget review" {
		t.Fatalf("title %q", l.Meta.Title)
	}
}

// countingEngine counts the chunks it transcribes.
type countingEngine struct {
	engine
	chunks *int
}

func (c countingEngine) Transcribe(ctx context.Context, pcm []float32, opt pipeline.Options) ([]pipeline.Segment, error) {
	*c.chunks++
	return c.engine.Transcribe(ctx, pcm, opt)
}

func TestLiveThenFinalEqualsFinalOnly(t *testing.T) {
	dir := t.TempDir()
	mk := func() *Session {
		s, _ := Create(dir, Meta{Language: "de"})
		writeWAV(t, s.Path(MeFile), 100, 0.1)
		writeWAV(t, s.Path(ThemFile), 100, 0.2)
		return s
	}
	// During the meeting: what is certain of 100 s of continuous speech
	// (cuts at 40 and 80 s; the last 20 s wait).
	live := mk()
	n := 0
	done, err := live.TranscribeLive(context.Background(), countingEngine{chunks: &n}, pipeline.Options{})
	if err != nil || done != 4 || n != 4 {
		t.Fatalf("live: %d chunks (%d), %v", done, n, err)
	}
	if p := live.LiveProgress(); p != 80*time.Second {
		t.Fatalf("live progress %v", p)
	}
	// Once more: nothing new is certain.
	if done, _ := live.TranscribeLive(context.Background(), countingEngine{chunks: &n}, pipeline.Options{}); done != 0 {
		t.Fatalf("second step did %d chunks", done)
	}
	// The end: only the rest.
	n = 0
	if err := live.Transcribe(context.Background(), countingEngine{chunks: &n}, pipeline.Options{}, "", nil); err != nil {
		t.Fatal(err)
	}
	if n != 2 || live.Has(LiveFile) {
		t.Fatalf("final did %d chunks; live.json left: %v", n, live.Has(LiveFile))
	}
	// The same as transcribing everything afterwards.
	whole := mk()
	if err := whole.Transcribe(context.Background(), engine{}, pipeline.Options{}, "", nil); err != nil {
		t.Fatal(err)
	}
	a, _ := live.Segments()
	b, _ := whole.Segments()
	if len(a) != len(b) || len(a) != 6 {
		t.Fatalf("%d segments live, %d whole", len(a), len(b))
	}
	for i := range a {
		if a[i].Start != b[i].Start || a[i].Text != b[i].Text || a[i].Track != b[i].Track {
			t.Fatalf("segment %d: %+v vs %+v", i, a[i], b[i])
		}
	}
}

func TestLiveStateForAnotherLanguageIsDropped(t *testing.T) {
	s, _ := Create(t.TempDir(), Meta{Language: "de"})
	writeWAV(t, s.Path(MeFile), 100, 0.1)
	n := 0
	s.TranscribeLive(context.Background(), countingEngine{chunks: &n}, pipeline.Options{})
	// The language was changed before the end: everything in English.
	n = 0
	s.Transcribe(context.Background(), countingEngine{chunks: &n}, pipeline.Options{Language: "en"}, "", nil)
	if n != 3 {
		t.Fatalf("%d chunks, want all 3 again", n)
	}
	text, _ := s.Transcript(false)
	if strings.Contains(text, " de") {
		t.Fatalf("German chunks kept:\n%s", text)
	}
}

func TestM4ALiveTranscribeAndCrashRecover(t *testing.T) {
	if !m4a.Available() {
		t.Skip("Media Foundation AAC unavailable")
	}
	dir := t.TempDir()
	s, err := Create(dir, Meta{Language: "de"})
	if err != nil {
		t.Fatal(err)
	}
	// Write a 3-second 48 kHz stereo them.m4a and 3-second mono me.m4a.
	writeM4A := func(path string, ch int) {
		w, err := m4a.Create(path, m4a.Format{SampleRate: 48000, Channels: ch})
		if err != nil {
			t.Fatal(err)
		}
		buf := make([]float32, 48000*3*ch)
		for i := range 48000 * 3 {
			v := float32(0.2)
			if ch == 1 {
				buf[i] = v
			} else {
				buf[2*i], buf[2*i+1] = v, v
			}
		}
		if err := w.Write(buf); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
	}
	writeM4A(s.Path(MeM4A), 1)
	writeM4A(s.Path(ThemM4A), 2)

	if !s.HasAudio() || s.TrackFile("me") != MeM4A || s.TrackFile("them") != ThemM4A {
		t.Fatalf("track lookup failed: me=%q them=%q", s.TrackFile("me"), s.TrackFile("them"))
	}
	if err := s.Transcribe(context.Background(), engine{}, pipeline.Options{Speakers: true}, "", nil); err != nil {
		t.Fatal(err)
	}
	if s.Meta.State != Ready {
		t.Fatalf("state %s, want Ready", s.Meta.State)
	}

	// Simulate a crash mid-recording with leftover .m4a.aac: Recover must remux to .m4a.
	crashed, err := Create(dir, Meta{Language: "de", State: Recording})
	if err != nil {
		t.Fatal(err)
	}
	w, err := m4a.Create(crashed.Path(MeM4A), m4a.Format{SampleRate: 48000, Channels: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Write(make([]float32, 48000)); err != nil {
		t.Fatal(err)
	}
	if err := w.Sync(); err != nil {
		t.Fatal(err)
	}
	// Copy the live .aac to simulate an abrupt process exit before Close().
	aacBytes, err := os.ReadFile(crashed.Path(MeM4A + m4a.ScratchSuffix))
	if err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	_ = os.Remove(crashed.Path(MeM4A))
	if err := os.WriteFile(crashed.Path(MeM4A+m4a.ScratchSuffix), aacBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	todo := Recover(dir)
	if len(todo) != 1 || todo[0].Dir != crashed.Dir {
		t.Fatalf("Recover returned %d sessions, want crashed", len(todo))
	}
	if !crashed.Has(MeM4A) || crashed.Has(MeM4A+m4a.ScratchSuffix) {
		t.Fatalf("Recover didn't remux .aac to .m4a: files=%v", crashed.AudioFiles())
	}
}
