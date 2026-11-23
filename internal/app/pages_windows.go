package app

import (
	"fmt"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/motivated2die/flit/internal/logx"
	"github.com/motivated2die/flit/internal/session"
	"github.com/motivated2die/flit/internal/sysinfo"
	"github.com/motivated2die/flit/internal/transcript"
	"github.com/motivated2die/flit/internal/ui"
)

// Home, Recordings and a recording's page. Home is what's happening now
// plus the latest recordings; Recordings is all of them in the same rows;
// a row opens the recording's page with its player, speakers and
// transcript.

const homeRecent = 4 // recordings on Home

// --- home -----------------------------------------------------------------

func (a *App) homePage() ui.Node {
	status := []ui.Node{ui.Para{Text: a.homeStatus(), Muted: true}}
	head := ui.Node(ui.Title("Home"))
	if name := a.cfg.GreetingName(); name != "" {
		head = ui.VStack{Gap: 2, Items: []ui.Node{
			ui.Title("Home"),
			ui.Label{Text: "Welcome, " + name, Muted: true},
		}}
	}
	items := []ui.Node{
		head,
		ui.HStack{Gap: 12, Items: append(status, a.homeActions()...)},
	}
	if card := a.liveCard(); card != nil {
		items = append(items, ui.Space(4), card)
	}
	if a.failed != nil && a.rec == nil && a.working == nil {
		items = append(items, ui.Card{Gap: 6, Items: []ui.Node{
			ui.Label{Text: "Something went wrong", Strong: true},
			ui.Para{Text: a.failed.Error(), Muted: true},
		}})
	}
	items = append(items, a.upcomingSection()...)
	items = append(items, ui.Space(8), ui.CaptionRow{Text: "Latest recordings",
		Right: ui.Link{ID: "home:all", Glyph: ui.GlyphHistory, Label: "All recordings", OnClick: func() { a.go_(pageRecordings) }}})
	if listed := a.listed(); len(listed) == 0 {
		items = append(items, ui.Card{Items: []ui.Node{
			ui.Para{Text: "Your recordings and their transcripts appear here once a meeting has been recorded.", Muted: true},
		}})
	} else {
		items = append(items, a.recordingList(listed[:min(homeRecent, len(listed))], "home"))
	}
	return ui.VStack{Gap: 12, Items: items}
}

// homeActions is the page's main button: start, stop, or take up a
// detected meeting.
func (a *App) homeActions() []ui.Node {
	switch {
	case a.rec != nil:
		rec := a.rec.sess
		// Cancel and Notes as round icons; Cancel asks again in words.
		cancel := ui.Node(ui.Button{ID: "home:cancel", Glyph: ui.GlyphDelete, Large: true, OnClick: func() {
			a.confirm, a.confirmAt = "home:cancel", time.Now()
			a.invalidate()
			if a.tray != nil { // none in tests
				time.AfterFunc(5*time.Second, func() { a.tray.Post(a.invalidate) })
			}
		}})
		if a.confirm == "home:cancel" && time.Since(a.confirmAt) < 5*time.Second {
			cancel = ui.Button{ID: "home:cancel", Label: "Discard recording", Glyph: ui.GlyphDelete, Danger: true, Large: true, OnClick: func() {
				a.confirm = ""
				a.cancelRecording()
			}}
		}
		return []ui.Node{
			cancel,
			ui.Button{ID: "home:notes", Glyph: ui.GlyphNotes, Large: true, OnClick: func() { a.openNotes(rec) }},
			ui.Button{ID: "home:stop", Label: "Transcribe", Glyph: ui.GlyphStop, Primary: true, Large: true, OnClick: a.stopRecording},
		}
	case a.detected != nil:
		c := *a.detected
		cancel := "Not now"
		if a.counting() {
			cancel = "Cancel"
		}
		return []ui.Node{
			ui.Button{ID: "home:dismiss", Label: cancel, Large: true, OnClick: func() { a.onNotchButton("dismiss") }},
			ui.Button{ID: "home:record", Label: "Transcribe", Glyph: ui.GlyphRecord, Primary: true, Large: true, OnClick: func() { a.startRecording(&c) }},
		}
	}
	return []ui.Node{ui.Button{ID: "home:start", Label: "Start recording", Glyph: ui.GlyphRecord, Primary: true, Large: true, OnClick: func() { a.startRecording(nil) }}}
}

// homeStatus says in a sentence what flit is doing.
func (a *App) homeStatus() string {
	switch {
	case a.rec != nil:
		return "Recording your microphone and the meeting. The transcript follows when you stop or the meeting ends; Cancel throws the recording away."
	case a.detected != nil && a.counting():
		return providerName(a.detected.Provider) + " is using your microphone: flit starts recording the meeting in a few seconds. Cancel to leave this one out."
	case a.detected != nil:
		return providerName(a.detected.Provider) + " is using your microphone. flit can record the meeting and transcribe both sides when it ends."
	case a.working != nil:
		return "Transcribing the last meeting. You can close this window meanwhile."
	case a.sumRun != nil:
		return "Writing a summary on this PC. You can close this window meanwhile."
	}
	return "Waiting for a meeting. Flit notices when Teams, Zoom, Webex, Slack or Google Meet start using your microphone and offers to transcribe it; or start a recording yourself."
}

// liveCard shows a recording's levels or a transcription's progress; nil
// when idle.
func (a *App) liveCard() ui.Node {
	switch {
	case a.rec != nil:
		title := "Recording"
		if a.rec.sess.Meta.Title != "" {
			title = a.rec.sess.Meta.Title
		}
		source := "Your microphone and all audio this PC plays"
		if a.rec.sess.Meta.ThemSource == "process" {
			source = "Your microphone and " + providerName(a.rec.provider) + "'s audio, nothing else"
		}
		if a.live != nil {
			source += ". Transcribing as it goes"
			if a.live.upTo > 0 {
				source += ": up to " + ui.Clock(a.live.upTo)
			}
		}
		// The voice meter stacked on the right; in a narrow window, wide
		// under the text.
		// A click on the card opens the recording, its notes ready to type in.
		rec := a.rec.sess
		return ui.Card{ID: "home:live", OnClick: func() { a.openNotes(rec) }, Items: []ui.Node{ui.Aside{
			Main: ui.VStack{Gap: 10, Items: []ui.Node{
				ui.LiveTitle{Text: title, Detail: ui.Clock(time.Since(a.rec.started))},
				ui.Para{Text: source, Muted: true, Small: true},
			}},
			Side: a.voiceMeter(true), Below: a.voiceMeter(false), MinMain: 280, Gap: 16,
		}}}
	case len(a.downloads) > 0: // models first (a transcription waits for them)
		return ui.VStack{Gap: 8, Items: a.modelCards(true)}
	case a.working != nil:
		return a.transcribeCard(a.working, "home:working", "Transcribing "+sessionTitle(a.working))
	case a.sumRun != nil:
		return a.summaryCard(a.sumRun, "home:summary", "Summarising "+a.sumRun.title)
	}
	return nil
}

// transcribeCard is a running transcription on the card downloads and
// summaries use: its background fills up as it goes, showing where the
// speech model runs and any processor cap.
func (a *App) transcribeCard(s *session.Session, id, title string) ui.Node {
	d := a.sampleDebug()
	where := ""
	if m, ok := a.shownModel(d); ok {
		if a.transcribeOnGPU(d) {
			where = m.Name + " on the graphics processor"
		} else {
			where = m.Name + " on the processor"
		}
	}
	detail := sessionWhen(s)
	if where != "" {
		detail += " · " + where
	}
	if limit := a.finalLimit(); limit > 0 && !a.transcribeOnGPU(d) {
		detail += fmt.Sprintf(" · at most %d%% of the processor", limit)
	}
	return ui.ProgressCard{ID: id, Title: title, Detail: detail,
		Trailing: fmt.Sprintf("%d%%", int(a.progress*100)), Value: a.progress}
}

// --- recordings -----------------------------------------------------------

func (a *App) recordingsPage() ui.Node {
	items := []ui.Node{ui.Title("Recordings")}
	listed := a.listed()
	if len(listed) == 0 {
		return ui.VStack{Gap: 12, Items: append(items, ui.Para{Text: "No recordings yet.", Muted: true})}
	}
	n := fmt.Sprintf("%d recordings", len(listed))
	if len(listed) == 1 {
		n = "1 recording"
	}
	items = append(items,
		ui.Para{Text: n + " · " + sysinfo.Bytes(uint64(a.storage)) + " on disk. Settings can free up space; a recording's page can delete its audio or all of it.", Muted: true},
		ui.HStack{Gap: 8, Items: []ui.Node{
			ui.Button{ID: "rec:folder", Label: "Open folder", Glyph: ui.GlyphFolder, OnClick: func() { openFolder(session.Root(a.dataDir)) }},
			ui.Button{ID: "rec:clean", Label: "Free up space", OnClick: func() { a.go_(pageSettings) }},
		}},
		ui.Space(4),
		a.searchBar())
	found, snippets := a.searchSessions()
	if strings.TrimSpace(a.search) != "" {
		line := fmt.Sprintf("%d of %d recordings match", len(found), len(listed))
		switch {
		case len(found) == 0:
			line = "No recording matches “" + strings.TrimSpace(a.search) + "”"
			if !a.searchDeep {
				line += ". Also in transcripts searches what was said."
			}
		case len(found) == 1:
			line = fmt.Sprintf("1 of %d recordings matches", len(listed))
		}
		items = append(items, ui.Para{Text: line, Muted: true, Small: true})
	}
	if len(found) > 0 {
		items = append(items, a.recordingRows(found, "rec", snippets))
	}
	return ui.VStack{Gap: 12, Items: items}
}

// searchBar is the Recordings page's search field and what it searches.
func (a *App) searchBar() ui.Node {
	field := ui.TextField{ID: "rec:search", Glyph: ui.GlyphSearch, Value: a.search, Placeholder: "Search recordings", MaxLen: 100, H: 34, Round: true,
		OnCommit: a.setSearch, OnChange: a.setSearch, OnClear: func() { a.setSearch("") }}
	scope := 0
	if a.searchDeep {
		scope = 1
	}
	return ui.HStack{Gap: 8, Items: []ui.Node{
		ui.Group{field}, // takes the room the choice leaves
		ui.Segmented{ID: "rec:scope", Options: []string{"Name", "Fulltext"}, Selected: scope, OnSelect: func(i int) {
			a.searchDeep = i == 1
			a.invalidate()
		}},
	}}
}

func (a *App) setSearch(q string) {
	if q != a.search {
		a.search = q
		a.invalidate()
	}
}

// searchSessions returns the recordings matching the search: every word
// in the name or date, or, searching transcripts too, in what was said.
// For a match in the transcript, snippets holds the line it is on.
func (a *App) searchSessions() ([]*session.Session, map[string]string) {
	terms := strings.Fields(strings.ToLower(a.search))
	if len(terms) == 0 {
		return a.listed(), nil
	}
	var found []*session.Session
	snippets := map[string]string{}
	for _, s := range a.listed() {
		name := strings.ToLower(sessionTitle(s) + " " + sessionWhen(s))
		var text string
		if a.searchDeep && s.Meta.State == session.Ready {
			text = a.lowerTranscript(s)
		}
		ok, inText := true, ""
		for _, t := range terms {
			switch {
			case strings.Contains(name, t):
			case text != "" && strings.Contains(text, t):
				if inText == "" {
					inText = t
				}
			default:
				ok = false
			}
			if !ok {
				break
			}
		}
		if !ok {
			continue
		}
		found = append(found, s)
		if inText != "" {
			snippets[s.Meta.ID] = matchLine(a.preview(s), inText)
		}
	}
	return found, snippets
}

// lowerTranscript is a transcript's text in lower case, for searching
// (cached until the transcript changes).
func (a *App) lowerTranscript(s *session.Session) string {
	if a.searchText == nil {
		a.searchText = map[string]string{}
	}
	if t, ok := a.searchText[s.Meta.ID]; ok {
		return t
	}
	t := strings.ToLower(a.preview(s))
	a.searchText[s.Meta.ID] = t
	return t
}

// matchLine is the transcript line holding term (lower case), as
// "Speaker: …words around it…" for a recording row.
func matchLine(text, term string) string {
	for _, line := range strings.Split(text, "\n") {
		low := strings.ToLower(line)
		i := strings.Index(low, term)
		if i < 0 {
			continue
		}
		if len(low) != len(line) { // lower case changed the bytes: show it whole
			i = 0
		}
		// "[01:12] Me: text" → "Me: text"
		if strings.HasPrefix(line, "[") {
			if j := strings.Index(line, "] "); j >= 0 && j < 12 {
				line, i = line[j+2:], max(0, i-j-2)
			}
		}
		speaker, rest := "", line
		if j := strings.Index(line, ": "); j > 0 && j < 40 && j < i {
			speaker, rest, i = line[:j+2], line[j+2:], i-j-2
		}
		// Start a few words before the match.
		if i > 40 {
			cut := strings.LastIndex(rest[:i-30], " ")
			if cut > 0 {
				rest = "…" + rest[cut:]
			}
		}
		return speaker + rest
	}
	return ""
}

// listed are the recordings the lists show: all but the one being
// recorded, which has Home's recording card until it stops; then it joins
// the lists, being transcribed.
func (a *App) listed() []*session.Session {
	if a.rec == nil {
		return a.sessions
	}
	out := make([]*session.Session, 0, len(a.sessions))
	for _, s := range a.sessions {
		if s.Dir != a.rec.sess.Dir {
			out = append(out, s)
		}
	}
	return out
}

// recordingList is a card of recording rows.
func (a *App) recordingList(list []*session.Session, scope string) ui.Node {
	return a.recordingRows(list, scope, nil)
}

// recordingRows is a card of recording rows; snippets replace the
// transcript's first words (a search's match).
func (a *App) recordingRows(list []*session.Session, scope string, snippets map[string]string) ui.Node {
	var rows []ui.Node
	for i, s := range list {
		s := s
		if i > 0 {
			rows = append(rows, ui.Divider{})
		}
		snippet := a.snippet(s)
		if sn, ok := snippets[s.Meta.ID]; ok {
			snippet = sn
		}
		rows = append(rows, ui.RecordingRow{
			ID: scope + ":" + s.Meta.ID, Live: a.rec != nil && a.rec.sess.Dir == s.Dir,
			Title: sessionTitle(s), Facts: a.facts(s), Snippet: snippet, Trailing: a.stateText(s),
			OnClick: func() {
				if a.isRecording(s) {
					a.openNotes(s) // the running meeting: straight to its title or notes
					return
				}
				a.go_(pageSession + s.Meta.ID)
			},
		})
	}
	return ui.Card{Gap: 0, Items: rows}
}

// facts is a recording's when, length, words and speakers.
func (a *App) facts(s *session.Session) string {
	parts := []string{sessionWhen(s)}
	if s.Meta.State == session.Ready {
		if w := len(strings.Fields(a.preview(s))); w > 0 {
			parts = append(parts, plural(w, "word"))
		}
		if n := a.speakerCount(s); n > 1 {
			parts = append(parts, plural(n, "speaker"))
		}
	}
	return strings.Join(parts, " · ")
}

// speakerCount counts Me and Them's speakers (Them as one undiarized).
func (a *App) speakerCount(s *session.Session) int {
	return len(a.speakers(s))
}

// snippet is the beginning of a transcript, on one line.
func (a *App) snippet(s *session.Session) string {
	if s.Meta.State != session.Ready {
		return ""
	}
	paras := a.paragraphs(s)
	if len(paras) == 0 {
		return ""
	}
	p := paras[0]
	text := p.Speaker + ": " + p.Text
	if r := []rune(text); len(r) > 160 {
		text = string(r[:160]) + " …"
	}
	return text
}

func plural(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	s := fmt.Sprint(n)
	if n >= 1000 {
		s = fmt.Sprintf("%d,%03d", n/1000, n%1000)
	}
	return s + " " + what + "s"
}

// --- a recording ----------------------------------------------------------

func (a *App) sessionPage(id string) ui.Node {
	s := a.sessionByID(id)
	back := ui.Node(ui.HStack{Items: []ui.Node{ui.Link{ID: "session:back", Glyph: ui.GlyphBack, Label: "Recordings", OnClick: func() { a.go_(pageRecordings) }}}})
	if s == nil {
		return ui.VStack{Gap: 8, Items: []ui.Node{back, ui.Para{Text: "This recording is gone.", Muted: true}}}
	}
	live := a.isRecording(s)
	if live {
		// The running meeting is not in Recordings yet: back goes Home; a
		// small button on the right ends it.
		back = ui.HStack{Items: []ui.Node{
			ui.VStack{Items: []ui.Node{ui.HStack{Items: []ui.Node{ui.Link{ID: "session:back", Glyph: ui.GlyphBack, Label: "Home", OnClick: func() { a.go_(pageHome) }}}}}},
			ui.Button{ID: "session:stop", Label: "Transcribe", Glyph: ui.GlyphStop, Primary: true, Small: true, OnClick: a.stopRecording},
		}}
	}
	ready := s.Meta.State == session.Ready
	focusTitle := a.focusTitle == id
	if focusTitle {
		a.focusTitle = "" // asked for once
	}
	title := ui.TextField{ID: "session:title", Title: true, Value: s.Meta.Title, Placeholder: defaultTitle(s), MaxLen: 80,
		OnCommit: func(t string) { a.renameSession(s, t) }, Focus: focusTitle, Next: "notes:" + id}
	items := []ui.Node{back, title,
		ui.Label{Text: a.facts(s) + " · " + a.stateText(s), Muted: true}}
	if card := a.meetingCard(s); card != nil {
		items = append(items, card)
	}
	if a.working != nil && a.working.Dir == s.Dir {
		items = append(items, a.transcribeCard(s, "session:working", "Transcribing"))
	}
	if s.Meta.State == session.Failed && s.Meta.Error != "" {
		items = append(items, ui.Card{Items: []ui.Node{ui.Para{Text: "Transcription failed: " + s.Meta.Error, Muted: true}}})
	}
	if live {
		// While it records: the notes, with room to write.
		items = append(items, ui.Space(4))
		items = append(items, a.notesSection(s, false)...)
		return ui.VStack{Gap: 10, Items: items}
	}
	items = append(items, a.sessionActions(s)) // the player is docked below the page (frame)
	items = append(items, ui.Space(8))
	items = append(items, a.notesSection(s, true)...)
	if ready {
		items = append(items, ui.Space(8))
		items = append(items, a.summarySection(s)...)
		items = append(items, ui.Space(8), ui.CaptionRow{Text: "Speakers", Right: a.analyzeLink(s)}, a.speakersCard(s))
		items = append(items, ui.Space(8), ui.Caption("Transcript"), a.transcriptBlocks(s))
	}
	if files := a.filesCard(s); files != nil {
		items = append(items, ui.Space(8), ui.Caption("Files"), files)
	}
	return ui.VStack{Gap: 10, Items: items}
}

// filesCard manages what a recording keeps on disk: its audio and itself.
func (a *App) filesCard(s *session.Session) ui.Node {
	busy := (a.working != nil && a.working.Dir == s.Dir) || (a.rec != nil && a.rec.sess.Dir == s.Dir) || (a.finding != nil && a.finding.Dir == s.Dir)
	if busy {
		return nil
	}
	var rows []ui.Node
	if a.hasAudio(s) {
		size := audioSize(s)
		detail := "Both tracks, " + sysinfo.Bytes(uint64(size)) + "; needed to listen and to transcribe again"
		rows = append(rows, ui.SettingRow{Label: "Audio", Detail: detail, Control: a.confirmButton("session:delaudio", "Delete audio", false, func() { a.deleteAudio(s) })}, ui.Divider{})
	}
	rows = append(rows, ui.SettingRow{Label: "This recording", Detail: "Its folder with audio, transcript and speaker names", Control: a.confirmButton("session:delete", "Delete", false, func() { a.deleteRecording(s) })})
	return ui.Card{Gap: 2, Items: rows}
}

// sessionActions are a recording's smaller actions.
func (a *App) sessionActions(s *session.Session) ui.Node {
	var btns []ui.Node
	if s.Meta.State == session.Ready {
		btns = append(btns,
			ui.Button{ID: "session:copy", Label: "Copy", Glyph: ui.GlyphCopy, Primary: true, OnClick: func() { a.copyTranscript(s, false) }},
			ui.Button{ID: "session:md", Label: "Copy as Markdown", OnClick: func() { a.copyTranscript(s, true) }})
	}
	btns = append(btns, ui.Button{ID: "session:folder", Label: "Open folder", Glyph: ui.GlyphFolder, OnClick: func() { openFolder(s.Dir) }})
	busy := (a.working != nil && a.working.Dir == s.Dir) || (a.rec != nil && a.rec.sess.Dir == s.Dir) || (a.finding != nil && a.finding.Dir == s.Dir)
	if !busy {
		if (s.Meta.State == session.Failed || s.Meta.State == session.Ready || s.Meta.State == session.Recorded) && a.hasAudio(s) {
			btns = append(btns, ui.Button{ID: "session:again", Label: "Transcribe again", Glyph: ui.GlyphRefresh, OnClick: func() { a.retranscribe(s) }})
		}
	}
	return ui.HStack{Gap: 8, Items: btns}
}

// confirmButton is a destructive button that asks for a second click
// (within 5 s) before it acts.
func (a *App) confirmButton(id, label string, large bool, act func()) ui.Node {
	return a.confirmButtonAs(id, label, "Click again to "+strings.ToLower(label), large, act)
}

// confirmButtonAs is confirmButton with its own label for the second
// click (a short one where room is tight).
func (a *App) confirmButtonAs(id, label, again string, large bool, act func()) ui.Node {
	armed := a.confirm == id && time.Since(a.confirmAt) < 5*time.Second
	if armed {
		return ui.Button{ID: id, Label: again, Glyph: ui.GlyphDelete, Danger: true, Large: large, OnClick: func() {
			a.confirm = ""
			act()
		}}
	}
	return ui.Button{ID: id, Label: label, Glyph: ui.GlyphDelete, Large: large, OnClick: func() {
		a.confirm, a.confirmAt = id, time.Now()
		a.invalidate()
		time.AfterFunc(5*time.Second, func() { a.tray.Post(a.invalidate) })
	}}
}

func (a *App) hasAudio(s *session.Session) bool { return s.HasAudio() }

// deleteAudio removes a recording's tracks and keeps its transcript.
func (a *App) deleteAudio(s *session.Session) {
	a.stopPlayer(s)
	for _, f := range s.AudioFiles() {
		if err := os.Remove(s.Path(f)); err != nil && !os.IsNotExist(err) {
			logx.Printf("deleting %s of %s: %v", f, s.Meta.ID, err)
		}
	}
	logx.Printf("deleted the audio of %s", s.Meta.ID)
	a.reloadSessions()
	a.invalidate()
}

// deleteRecording removes a recording's folder and goes back to the list.
func (a *App) deleteRecording(s *session.Session) {
	a.removeSession(s)
	a.go_(pageRecordings)
}

// renameSession gives a recording a title ("" goes back to the default).
func (a *App) renameSession(s *session.Session, title string) {
	if err := s.SetTitle(title); err != nil {
		logx.Printf("renaming %s: %v", s.Meta.ID, err)
	}
	// The running recording keeps its own copy of the session, saved again
	// when it ends: it must carry the new name, or the name would be lost.
	if a.rec != nil && a.rec.sess != s && a.rec.sess.Dir == s.Dir {
		a.rec.sess.Meta.Title, a.rec.sess.Meta.TitleByUser = s.Meta.Title, s.Meta.TitleByUser
	}
	a.forget(s)
	a.invalidate()
}

// cancelRecording stops the recording and throws it away.
func (a *App) cancelRecording() {
	rec := a.rec
	if rec == nil {
		return
	}
	a.finishRecording(false)
	logx.Printf("recording cancelled")
	a.afterLive(func() { // its files are left alone first
		a.removeSession(rec.sess)
		a.reloadSessions()
		a.invalidate()
	})
}

// --- transcript blocks ------------------------------------------------------

// paragraphs are a transcript's speaker paragraphs with the given names.
func (a *App) paragraphs(s *session.Session) []transcript.Paragraph {
	if a.paraCache == nil {
		a.paraCache = map[string][]transcript.Paragraph{}
	}
	if p, ok := a.paraCache[s.Meta.ID]; ok {
		return p
	}
	segs, err := s.Segments()
	if err != nil {
		return nil
	}
	p := transcript.Paragraphs(segs, s.Names(), 3*time.Second)
	a.paraCache[s.Meta.ID] = p
	return p
}

// transcriptBlocks shows the transcript paragraph by paragraph; a click
// plays from there, and the paragraph playing is marked.
func (a *App) transcriptBlocks(s *session.Session) ui.Node {
	paras := a.paragraphs(s)
	if len(paras) == 0 {
		return ui.Card{Items: []ui.Node{ui.Para{Text: a.preview(s)}}}
	}
	current := -1
	if p := a.playerOf(s); p != nil && (p.Playing() || p.Position() > 0) {
		at := toDuration(p.Position())
		for i, para := range paras {
			if para.Start <= at {
				current = i
			}
		}
	}
	audio := a.hasAudio(s)
	// The search: every match in red, the one shown underlined too, and
	// the page scrolled to it after typing or an arrow.
	q := strings.TrimSpace(a.tsQuery)
	found := make([][]int, len(paras))
	total := 0
	if q != "" {
		for i, p := range paras {
			found[i] = findFold(p.Text, q)
			total += len(found[i])
		}
	}
	if total > 0 {
		a.tsIndex = (a.tsIndex%total + total) % total
	}
	n := 0 // matches so far, to find the shown one
	var blocks []ui.Node
	for i, p := range paras {
		p := p
		b := ui.TranscriptBlock{ID: fmt.Sprintf("para:%d", i), Speaker: p.Speaker, Time: transcript.Clock(p.Start), Text: p.Text,
			Me: p.Track == transcript.Me, Current: i == current}
		for _, at := range found[i] {
			shown := n == a.tsIndex
			b.Marks = append(b.Marks, ui.TextMark{From: at, To: at + len(q), Color: ui.RGBA(ui.ColLive, 1), Underline: shown})
			if shown && a.tsJump {
				b.ScrollTo = true
			}
			n++
		}
		key := fmt.Sprintf("para:%d", i)
		if audio {
			b.OnPlay = func() { a.toggleSection(s, p, key) }
			b.Playing = a.sectionPlaying(s, key)
		}
		b.OnCopy = func() { a.copySection(p.Text, key) }
		b.Copied = a.copied == key && time.Since(a.copiedAt) < copiedFor
		blocks = append(blocks, b)
	}
	a.tsJump = false
	return ui.CornerCard{Corner: a.transcriptSearch(q, total), Sticky: q != "", Gap: 2, Items: blocks}
}

// transcriptSearch is the search in the transcript's top right corner:
// while searching, how many results and arrows to go through them.
func (a *App) transcriptSearch(q string, total int) []ui.Node {
	field := ui.TextField{ID: "session:search", Glyph: ui.GlyphSearch, Value: a.tsQuery, Placeholder: "Search transcript", W: 220, MaxLen: 100, Round: true,
		OnChange: a.setTranscriptSearch, OnCommit: a.setTranscriptSearch, OnClear: func() { a.setTranscriptSearch("") }}
	if q == "" {
		return []ui.Node{field}
	}
	count := "No results"
	if total > 0 {
		count = fmt.Sprintf("%d of %d", a.tsIndex+1, total)
	}
	return []ui.Node{
		ui.Label{Text: count, Muted: true},
		ui.IconButton{ID: "session:search:prev", Glyph: ui.GlyphUp, Disabled: total == 0, OnClick: func() { a.stepResult(-1) }},
		ui.IconButton{ID: "session:search:next", Glyph: ui.GlyphDown, Disabled: total == 0, OnClick: func() { a.stepResult(1) }},
		field,
	}
}

// setTranscriptSearch changes the words searched for: back to the first
// result, shown.
func (a *App) setTranscriptSearch(q string) {
	if q == a.tsQuery {
		return
	}
	a.tsQuery, a.tsIndex, a.tsJump = q, 0, strings.TrimSpace(q) != ""
	a.invalidate()
}

// stepResult shows the next (1) or previous (-1) result, round the end.
func (a *App) stepResult(d int) {
	a.tsIndex += d // wrapped into range when the blocks are built
	a.tsJump = true
	a.invalidate()
}

// findFold returns the byte offsets in s where q starts, ignoring case
// (not overlapping).
func findFold(s, q string) []int {
	if q == "" {
		return nil
	}
	var out []int
	ls, lq := strings.ToLower(s), strings.ToLower(q)
	if len(ls) != len(s) || len(lq) != len(q) {
		// Lower case changed the bytes (rare letters): compare rune by
		// rune on the original.
		for i := 0; i+len(q) <= len(s); {
			if strings.EqualFold(s[i:i+len(q)], q) {
				out = append(out, i)
				i += len(q)
				continue
			}
			_, size := utf8.DecodeRuneInString(s[i:])
			i += size
		}
		return out
	}
	for i := 0; ; {
		j := strings.Index(ls[i:], lq)
		if j < 0 {
			return out
		}
		out = append(out, i+j)
		i += j + len(lq)
	}
}
