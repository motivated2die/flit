package app

import (
	"fmt"
	"time"

	"github.com/motivated2die/flit/internal/logx"
	"github.com/motivated2die/flit/internal/playback"
	"github.com/motivated2die/flit/internal/session"
	"github.com/motivated2die/flit/internal/transcript"
	"github.com/motivated2die/flit/internal/ui"
)

// Listening to a recording: one player at a time, for the recording whose
// page is open. It opens on the first play and closes when the page or the
// window does.

// appPlayer is the open player and what it plays.
type appPlayer struct {
	p      *playback.Player
	sessID string
	tracks []transcript.Track // the player's track order
	clip   string             // the speaker whose clips play ("" = the meeting)
	label  string             // that speaker's name, for the bar
}

// playerOf returns the open player if it belongs to s.
func (a *App) playerOf(s *session.Session) *playback.Player {
	if a.player != nil && a.player.sessID == s.Meta.ID {
		return a.player.p
	}
	return nil
}

// openPlayer opens s's tracks (closing another session's player).
func (a *App) openPlayer(s *session.Session) (*appPlayer, error) {
	if a.player != nil && a.player.sessID == s.Meta.ID {
		return a.player, nil
	}
	a.closePlayer()
	var paths []string
	var tracks []transcript.Track
	for _, t := range []struct {
		side  string
		track transcript.Track
	}{{"me", transcript.Me}, {"them", transcript.Them}} {
		if f := s.TrackFile(t.side); f != "" {
			paths = append(paths, s.Path(f))
			tracks = append(tracks, t.track)
		}
	}
	p, err := playback.Open(paths...)
	if err != nil {
		return nil, err
	}
	p.OnChange = func() { a.tray.Post(a.invalidate) }
	a.player = &appPlayer{p: p, sessID: s.Meta.ID, tracks: tracks}
	return a.player, nil
}

func (a *App) closePlayer() {
	if a.player != nil {
		a.player.p.Close()
		a.player = nil
	}
}

// stopPlayer closes the player if it plays s (before s's files go away).
func (a *App) stopPlayer(s *session.Session) {
	if a.player != nil && a.player.sessID == s.Meta.ID {
		a.closePlayer()
	}
}

// playAt plays the whole meeting from t.
func (a *App) playAt(s *session.Session, t time.Duration) {
	ap, err := a.openPlayer(s)
	if err != nil {
		logx.Printf("playing %s: %v", s.Meta.ID, err)
		return
	}
	ap.clip, ap.label = "", ""
	if err := ap.p.PlayFrom(playback.ToSamples(max(0, t))); err != nil {
		logx.Printf("playing %s: %v", s.Meta.ID, err)
	}
	a.invalidate()
}

// togglePlay plays or pauses the meeting.
func (a *App) togglePlay(s *session.Session) {
	ap, err := a.openPlayer(s)
	if err != nil {
		logx.Printf("playing %s: %v", s.Meta.ID, err)
		return
	}
	switch {
	case ap.p.Playing():
		ap.p.Pause()
	case ap.clip != "":
		// After a speaker's clips, the play button plays the meeting from
		// where the clip was.
		ap.clip, ap.label = "", ""
		_ = ap.p.PlayFrom(ap.p.Position())
	case ap.p.Position() >= ap.p.Length()-playback.Rate/10:
		_ = ap.p.PlayFrom(0) // at the end: from the start
	default:
		_ = ap.p.Resume()
	}
	a.invalidate()
}

// seek jumps to a share of the recording.
func (a *App) seek(s *session.Session, frac float32) {
	ap, err := a.openPlayer(s)
	if err != nil {
		return
	}
	at := int64(float64(ap.p.Length()) * float64(frac))
	if ap.clip != "" {
		ap.clip, ap.label = "", ""
		_ = ap.p.PlayFrom(at)
	} else if err := ap.p.MoveTo(at); err != nil {
		logx.Printf("seeking: %v", err)
	}
	a.invalidate()
}

// speakerKey names a speaker for the player.
func speakerKey(track transcript.Track, speaker int) string {
	return fmt.Sprintf("%s:%d", track, speaker)
}

// toggleSpeaker plays (or pauses) a speaker's clearest moments, from their
// own track only.
func (a *App) toggleSpeaker(s *session.Session, track transcript.Track, speaker int, label string) {
	ap, err := a.openPlayer(s)
	if err != nil {
		logx.Printf("playing %s: %v", s.Meta.ID, err)
		return
	}
	key := speakerKey(track, speaker)
	if ap.clip == key && ap.p.Playing() {
		ap.p.Pause()
		a.invalidate()
		return
	}
	segs, err := s.TrimmedSegments()
	if err != nil {
		return
	}
	clips := transcript.Clips(segs, track, speaker, 3, 8*time.Second)
	if len(clips) == 0 {
		return
	}
	var ranges []playback.Range
	for _, c := range clips {
		ranges = append(ranges, playback.Range{From: playback.ToSamples(c.Start), To: playback.ToSamples(c.End)})
	}
	which := make([]bool, len(ap.tracks))
	for i, t := range ap.tracks {
		which[i] = t == track
	}
	ap.clip, ap.label = key, label
	if err := ap.p.Play(ranges, which); err != nil {
		logx.Printf("playing %s: %v", s.Meta.ID, err)
	}
	a.invalidate()
}

// toggleSection plays (or pauses) one paragraph of the transcript, both
// tracks, from just before it starts to just after it ends.
func (a *App) toggleSection(s *session.Session, p transcript.Paragraph, key string) {
	ap, err := a.openPlayer(s)
	if err != nil {
		logx.Printf("playing %s: %v", s.Meta.ID, err)
		return
	}
	if ap.clip == key && ap.p.Playing() {
		ap.p.Pause()
		a.invalidate()
		return
	}
	end := p.End
	if end <= p.Start {
		end = p.Start + 5*time.Second // no end known: a few seconds
	}
	ap.clip, ap.label = key, ""
	r := playback.Range{From: playback.ToSamples(max(0, p.Start-300*time.Millisecond)), To: playback.ToSamples(end + 300*time.Millisecond)}
	if err := ap.p.Play([]playback.Range{r}, nil); err != nil {
		logx.Printf("playing %s: %v", s.Meta.ID, err)
	}
	a.invalidate()
}

// sectionPlaying reports whether a paragraph is playing on its own.
func (a *App) sectionPlaying(s *session.Session, key string) bool {
	return a.player != nil && a.player.sessID == s.Meta.ID && a.player.clip == key && a.player.p.Playing()
}

// copiedFor is how long a copy button shows its check mark.
const copiedFor = 1500 * time.Millisecond

// copySection copies one paragraph's text; its button shows a check for a
// moment.
func (a *App) copySection(text, key string) {
	if err := ui.SetClipboard(a.tray.Hwnd(), text); err != nil {
		logx.Printf("copy: %v", err)
		return
	}
	a.copied, a.copiedAt = key, time.Now()
	a.invalidate()
	time.AfterFunc(copiedFor, func() { a.tray.Post(a.invalidate) })
}

// speakerPlaying reports whether a speaker's clips are playing.
func (a *App) speakerPlaying(s *session.Session, track transcript.Track, speaker int) bool {
	return a.player != nil && a.player.sessID == s.Meta.ID && a.player.clip == speakerKey(track, speaker) && a.player.p.Playing()
}

// playerBar is the recording's player, docked at the bottom of its page.
// Dragging along the bar shows where it would play from; letting go jumps
// there. Dragging the end handles previews a non-destructive transcript trim
// and applies it on release.
func (a *App) playerBar(s *session.Session) ui.Node {
	trimStart, trimEnd := s.Meta.TrimStart, s.Meta.TrimEnd
	if a.trimming && a.trimSessID == s.Meta.ID {
		trimStart, trimEnd = a.trimStart, a.trimEnd
	}
	bar := ui.PlayerBar{
		ID:        "player",
		TrimStart: trimStart,
		TrimEnd:   trimEnd,
		OnToggle:  func() { a.togglePlay(s) },
		OnScrub: func(f float32, done bool) {
			if done {
				a.scrubbing = false
				a.seek(s, f)
				return
			}
			a.scrubbing, a.scrubAt = true, f
			a.invalidate()
		},
		OnTrim: func(start, end time.Duration, done bool) {
			if !done {
				a.trimming, a.trimSessID, a.trimStart, a.trimEnd = true, s.Meta.ID, start, end
				a.invalidate()
				return
			}
			a.trimming = false
			if err := s.SetTrim(start, end); err != nil {
				logx.Printf("trimming %s: %v", s.Meta.ID, err)
			}
			a.forget(s)
			a.invalidate()
		},
	}
	if p := a.playerOf(s); p != nil {
		bar.Playing, bar.Pos, bar.Len = p.Playing(), toDuration(p.Position()), toDuration(p.Length())
		if a.player.clip != "" && a.player.label != "" && bar.Playing {
			bar.Note = a.player.label + ", where they can be heard clearly"
		}
	} else if !s.Meta.Ended.IsZero() {
		bar.Len = s.Meta.Ended.Sub(s.Meta.Started)
	}
	if a.scrubbing {
		bar.Pos = time.Duration(float64(bar.Len) * float64(a.scrubAt))
	}
	return bar
}

func toDuration(samples int64) time.Duration { return playback.ToTime(samples) }
