package app

import (
	"fmt"

	"github.com/motivated2die/flit/internal/logx"
	"github.com/motivated2die/flit/internal/session"
	"github.com/motivated2die/flit/internal/transcript"
	"github.com/motivated2die/flit/internal/ui"
)

// The speakers of a transcript: who was found, renaming them, and finding
// them (again) for a recording whose meeting track was kept.

// speakers returns the session's speakers, read once per change.
func (a *App) speakers(s *session.Session) []transcript.SpeakerSummary {
	if a.speakerCache == nil {
		a.speakerCache = map[string][]transcript.SpeakerSummary{}
	}
	if sp, ok := a.speakerCache[s.Meta.ID]; ok {
		return sp
	}
	segs, err := s.TrimmedSegments()
	if err != nil {
		return nil
	}
	sp := transcript.Speakers(segs)
	a.speakerCache[s.Meta.ID] = sp
	return sp
}

// forget drops what is cached of a session's transcript.
func (a *App) forget(s *session.Session) {
	delete(a.previews, s.Meta.ID)
	delete(a.wordCounts, s.Meta.ID)
	delete(a.hasAudioMap, s.Meta.ID)
	delete(a.speakerCache, s.Meta.ID)
	delete(a.paraCache, s.Meta.ID)
	delete(a.searchText, s.Meta.ID)
}

// rename gives one speaker a name ("" goes back to the default) and
// rewrites the session's transcripts.
func (a *App) rename(s *session.Session, track transcript.Track, speaker int, name string) {
	n := s.Names()
	switch {
	case track == transcript.Me && speaker == 0:
		n.Me = name
	case track == transcript.Me:
		if n.MeSpeakers == nil {
			n.MeSpeakers = map[int]string{}
		}
		n.MeSpeakers[speaker] = name
	case speaker == 0:
		n.Them = name
	default:
		if n.Speakers == nil {
			n.Speakers = map[int]string{}
		}
		n.Speakers[speaker] = name
	}
	if err := s.SetNames(n); err != nil {
		logx.Printf("renaming a speaker of %s: %v", s.Meta.ID, err)
	}
	a.forget(s)
	a.invalidate()
}

func (a *App) findSpeakers(s *session.Session) {
	if a.jobs == nil || a.finding != nil {
		return
	}
	a.finding = s
	a.jobs.addFindSpeakers(s)
	a.invalidate()
}

// onSpeakersFound runs on the UI thread when a find-speakers job is done.
func (a *App) onSpeakersFound(s *session.Session, err error) {
	a.finding = nil
	if err != nil {
		logx.Printf("finding speakers in %s: %v", s.Meta.ID, err)
	}
	a.forget(s)
	a.reloadSessions()
	a.invalidate()
}

// speakersCard lists the transcript's speakers, each with a field to name
// them.
func (a *App) speakersCard(s *session.Session) ui.Node {
	names := s.Names()
	audio := a.hasAudio(s)
	sps := a.speakers(s)
	ids := make([]string, len(sps))
	for i, sp := range sps {
		ids[i] = fmt.Sprintf("speaker:%s:%s:%d", s.Meta.ID, sp.Track, sp.Speaker)
	}
	var rows []ui.Node
	for i, sp := range sps {
		sp := sp
		if i > 0 {
			rows = append(rows, ui.Divider{})
		}
		value, placeholder := "", names.Label(sp.Track, sp.Speaker)
		switch {
		case sp.Track == transcript.Me && sp.Speaker == 0:
			value = names.Me
		case sp.Track == transcript.Me:
			value = names.MeSpeakers[sp.Speaker]
		case sp.Speaker == 0:
			value = names.Them
		default:
			value = names.Speakers[sp.Speaker]
		}
		detail := fmt.Sprintf("%s · first at %s", roughDuration(sp.Talk), transcript.Clock(sp.First))
		if sp.Track == transcript.Me {
			detail = "Your microphone · " + detail
		}
		quote := "“" + sp.Quote + "”"
		if sp.Quote == "" {
			quote = "(nothing said)"
		}
		id := ids[i]
		prevID, nextID := "", ""
		if i > 0 {
			prevID = ids[i-1]
		}
		if i+1 < len(ids) {
			nextID = ids[i+1]
		}
		row := ui.Node(ui.SettingRow{Label: quote, Detail: detail, Control: ui.TextField{
			ID: id, Value: value, Placeholder: placeholder, W: 200, MaxLen: 40,
			TabPrev: prevID, TabNext: nextID,
			OnCommit: func(name string) { a.rename(s, sp.Track, sp.Speaker, name) },
		}})
		if audio {
			glyph := ui.GlyphPlay
			playing := a.speakerPlaying(s, sp.Track, sp.Speaker)
			if playing {
				glyph = ui.GlyphPause
			}
			label := names.Label(sp.Track, sp.Speaker)
			row = ui.HStack{Gap: 12, Items: []ui.Node{
				ui.RoundButton{ID: id + ":play", Glyph: glyph, Active: playing, H: 52, OnClick: func() { a.toggleSpeaker(s, sp.Track, sp.Speaker, label) }},
				row,
			}}
		}
		rows = append(rows, row)
	}
	items := []ui.Node{ui.Card{Gap: 2, Items: rows}}

	hint := "Click a name to change it. Two speakers with the same name read as one person."
	if audio {
		hint = "Play a speaker to hear them where nobody else talks, and click a name to change it. Two speakers with the same name read as one person."
	}
	switch {
	case a.finding != nil && a.finding.Dir == s.Dir:
		if a.transcribeOnGPU(a.sampleDebug()) {
			hint = "Analyzing the audio for speakers on the graphics processor…"
		} else {
			hint = "Analyzing the audio for speakers…"
		}
	case s.Meta.SpeakersError != "":
		hint = "Telling the speakers apart failed: " + s.Meta.SpeakersError
	case s.Meta.Speakers == 0 && s.Meta.MeSpeakers == 0 && (s.HasTrack("them") || s.HasTrack("me")):
		hint = "Each track is one speaker here. Analyzing the audio again tells up to four people apart per track (about 2 minutes per hour of audio on CPU)."
	}
	items = append(items, ui.Para{Text: hint, Muted: true, Small: true})
	return ui.VStack{Gap: 8, Items: items}
}

// analyzeLink runs speaker analysis on the kept audio again, beside the
// Speakers caption; nil when there is no audio or it is busy.
func (a *App) analyzeLink(s *session.Session) ui.Node {
	busy := a.finding != nil || (a.working != nil && a.working.Dir == s.Dir)
	if (!s.HasTrack("them") && !s.HasTrack("me")) || busy {
		return nil
	}
	label := "Analyze speakers"
	if s.Meta.Speakers > 0 || s.Meta.MeSpeakers > 0 {
		label = "Analyze speakers again"
	}
	return ui.Link{ID: "session:speakers", Glyph: ui.GlyphRefresh, Label: label, OnClick: func() { a.findSpeakers(s) }}
}
