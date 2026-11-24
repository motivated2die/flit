package app

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/motivated2die/flit/internal/audio/m4a"
	"github.com/motivated2die/flit/internal/capture"
	"github.com/motivated2die/flit/internal/detect"
	"github.com/motivated2die/flit/internal/install"
	"github.com/motivated2die/flit/internal/logx"
	"github.com/motivated2die/flit/internal/session"
	"github.com/motivated2die/flit/internal/ui"
)

// The main window: setup, home, recordings and settings. Pages are built
// from the app's state on every repaint (internal/ui/view.go).

const (
	pageWelcome    = "welcome"
	pageHome       = "home"
	pageRecordings = "recordings"
	pageSettings   = "settings"
	pageSession    = "session:" // + session ID
)

func (a *App) initWindow() {
	w, err := ui.NewWindow("flit")
	if err != nil {
		logx.Printf("main window unavailable: %v", err)
		return
	}
	a.win = w
	logx.Printf("main window ready")
	a.previews = map[string]string{}
	if !a.cfg.SetupComplete {
		a.page = pageWelcome
	} else {
		a.page = pageHome
	}
	w.Build = a.frame
	w.OnEscape = a.closePopover
	w.OnBack = a.navigateBack
	w.OnHide = func() {
		a.saveAllNotes()
		a.syncMeters()
		if !w.Visible() {
			a.closePlayer() // closed (not minimised): nothing plays on
		}
	}
	if fw, err := ui.NewFloatWindow("flit notes"); err != nil {
		logx.Printf("floating notes window unavailable: %v", err)
	} else {
		fw.Build = a.pinnedNotesNode
		fw.OnClose = a.unpinNotes
		fw.OnDragMove = a.onFloatDragMove
		fw.OnDragEnd = a.onFloatDragEnd
		a.notesFloat = fw
	}
}

// openWindow shows the main window. If notes are currently pinned in the
// floating panel, they auto-unpin back into the main window.
func (a *App) openWindow() {
	if a.pinnedNotes != "" {
		id := a.pinnedNotes
		a.saveNotes(id)
		a.pinnedNotes = ""
		if a.notesFloat != nil {
			a.notesFloat.FadeHide()
		}
		if a.page == "" || a.page == pageHome || a.page == pageSession+id {
			a.page = pageSession + id
			a.focusNotes = id
		}
	}
	if a.stashedNotes != "" && a.page == pageSession+a.stashedNotes {
		a.stashedNotes = ""
		a.syncNotch()
	}
	if a.win == nil {
		if a.tray != nil { // none in tests
			a.showStatus()
		}
		return
	}
	a.reloadSessions()
	if a.page == pageAudio {
		a.loadDevices()
	}
	a.win.Show()
	a.syncMeters()
	logx.Printf("main window opened (%s)", a.page)
}

// invalidate repaints the window, the floating notes panel and the tray panel
// if they are open.
func (a *App) invalidate() {
	if a.win != nil && a.win.Visible() {
		a.win.Invalidate()
	}
	if a.notesFloat != nil && a.notesFloat.Visible() {
		a.notesFloat.Invalidate()
	}
	if a.panel != nil && a.panel.Visible() {
		a.panel.Invalidate()
	}
}

func (a *App) go_(page string) {
	if a.player != nil && page != pageSession+a.player.sessID {
		a.closePlayer()
	}
	a.confirm = ""
	a.uninstallAsk = false
	a.saveAllNotes()
	if page != a.page {
		a.tsQuery, a.tsIndex, a.tsJump = "", 0, false // a search belongs to its page
	}
	a.page = page
	if page == pageRecordings || strings.HasPrefix(page, pageSession) {
		a.reloadSessions()
	}
	if page == pageAudio {
		a.loadDevices()
	}
	if page == pageSettings {
		a.countUsage()
	}
	if page == pageCalendar || page == pageHome {
		a.onCalendar()
	}
	a.syncMeters()
	a.invalidate()
}

// navigateBack handles the mouse browser Back button: closes a modal popover
// if open, returns from a meeting detail page to Meetings (or Home while
// actively recording), and returns from any other page to Home.
func (a *App) navigateBack() {
	if a.uninstallAsk {
		a.closePopover()
		return
	}
	if id, ok := strings.CutPrefix(a.page, pageSession); ok {
		if s := a.sessionByID(id); s != nil && a.isRecording(s) {
			a.go_(pageHome)
			return
		}
		a.go_(pageRecordings)
		return
	}
	if a.page != pageHome && a.page != pageWelcome {
		a.go_(pageHome)
	}
}

// reloadSessions re-reads the session list (on navigation and events, not
// on every repaint).
func (a *App) reloadSessions() {
	a.sessions = session.List(a.dataDir)
	a.storage = dirSize(session.Root(a.dataDir))
}

// dirSize adds up the sizes of the files under dir.
func dirSize(dir string) int64 {
	var n int64
	filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if info, err := d.Info(); err == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n
}

func (a *App) frame() ui.Frame {
	nav := []ui.NavEntry{
		{ID: pageHome, Label: "Home", Glyph: ui.GlyphHome},
		{ID: pageRecordings, Label: "Meetings", Glyph: ui.GlyphArticle},
		{ID: pageCalendar, Label: "Calendar", Glyph: ui.GlyphCalendar},
		{ID: pageAudio, Label: "Audio", Glyph: ui.GlyphMic},
		{ID: pageSettings, Label: "Settings", Glyph: ui.GlyphSettings},
	}
	if !a.cfg.SetupComplete {
		nav = append([]ui.NavEntry{{ID: pageWelcome, Label: "Welcome", Glyph: ui.GlyphPower}}, nav...)
	}
	selected := a.page
	if id, ok := strings.CutPrefix(selected, pageSession); ok {
		selected = pageRecordings
		if s := a.sessionByID(id); s != nil && a.isRecording(s) {
			selected = pageHome // the running meeting belongs to Home
		}
	}
	var page ui.Node
	switch {
	case a.page == pageWelcome:
		page = a.welcomePage()
	case a.page == pageRecordings:
		page = a.recordingsPage()
	case a.page == pageSettings:
		page = a.settingsPage()
	case a.page == pageAudio:
		page = a.audioPage()
	case a.page == pageDebug:
		page = a.debugPage()
	case a.page == pageCalendar:
		page = a.calendarPage()
	case a.page == pageUpdate:
		page = a.updatePage()
	case strings.HasPrefix(a.page, pageSession):
		page = a.sessionPage(strings.TrimPrefix(a.page, pageSession))
	default:
		page = a.homePage()
	}
	gpu, gpuShown := a.debugGPU()
	f := ui.Frame{Nav: nav, Selected: selected, OnNav: a.go_, Page: page, PageID: a.page,
		Status: a.sidebarStatus(), Live: a.rec != nil,
		LightTheme: a.cfg.LightTheme, OnToggleTheme: a.toggleTheme,
		Debug: a.debugLines(), DebugCores: a.debugCores(), DebugGPU: gpu, DebugGPUShown: gpuShown, OnDebug: func() { a.go_(pageDebug) },
		Update: a.updateBadge(), OnUpdate: func() { a.go_(pageUpdate) }}
	if a.uninstallAsk && a.page == pageSettings {
		f.Modal = a.uninstallPopover()
	}
	// A recording's player is docked at the bottom, always in reach.
	if id, ok := strings.CutPrefix(a.page, pageSession); ok {
		if s := a.sessionByID(id); s != nil && a.hasAudio(s) && !a.isRecording(s) { // nothing to play while it records
			f.Dock, f.DockID = a.playerBar(s), "player:"+id
		}
	}
	return f
}

// sessionByID finds a session in the list.
func (a *App) sessionByID(id string) *session.Session {
	for _, s := range a.sessions {
		if s.Meta.ID == id {
			return s
		}
	}
	return nil
}

func (a *App) sidebarStatus() string {
	if a.rec != nil {
		return "Recording " + ui.Clock(time.Since(a.rec.started))
	}
	if r := a.sumRun; r != nil && a.working == nil {
		return fmt.Sprintf("Summarising %d%%", int(r.value()*100))
	}
	return a.statusLine()
}

// voiceMeter is the recording's two sides as one box of moving bars (Home's
// recording card, stacked on its right; the tray panel, side by side).
func (a *App) voiceMeter(stacked bool) ui.Node {
	return ui.VoiceMeter{Labels: [2]string{"You", "Meeting"}, Bands: a.voiceBands, Smooth: &a.voiceSmooth, Stacked: stacked}
}

// voiceBands are both tracks' latest bands (flat when nothing records).
func (a *App) voiceBands() [2][]float32 {
	var me, them [capture.NBands]float32
	if a.rec != nil && a.rec.r != nil {
		if a.rec.r.Me != nil {
			me = a.rec.r.Me.Bands()
		}
		if a.rec.r.Them != nil {
			them = a.rec.r.Them.Bands()
		}
	}
	return [2][]float32{me[:], them[:]}
}

// peaks samples the recording's peaks at most every 40 ms and shares them
// between the notch and the window (taking a peak resets it).
func (a *App) peaks() (float32, float32) {
	if a.rec == nil {
		return 0, 0
	}
	if time.Since(a.peakAt) >= 40*time.Millisecond {
		a.peak = [2]float32{a.rec.r.Me.TakePeak(), a.rec.r.Them.TakePeak()}
		a.peakAt = time.Now()
	}
	return a.peak[0], a.peak[1]
}

func (a *App) latestReady() *session.Session {
	if a.lastReady != nil {
		return a.lastReady
	}
	for _, s := range a.sessions {
		if s.Meta.State == session.Ready {
			return s
		}
	}
	return nil
}

// preview is a session's transcript text without the header line (cached).
func (a *App) preview(s *session.Session) string {
	if p, ok := a.previews[s.Meta.ID]; ok {
		return p
	}
	text, err := s.Transcript(false)
	if err != nil {
		return ""
	}
	// Drop the header line and the blank line after it.
	if i := strings.Index(text, "\n\n"); i >= 0 && i < 200 {
		text = text[i+2:]
	}
	text = strings.TrimSpace(text)
	a.previews[s.Meta.ID] = text
	return text
}

func (a *App) retranscribe(s *session.Session) {
	s.DiscardLive() // from the beginning
	s.Meta.State, s.Meta.Error, s.Meta.Retries = session.Recorded, "", 0
	_ = s.Save()
	a.forget(s)
	a.jobs.add(s)
	a.invalidate()
}

func (a *App) stateText(s *session.Session) string {
	switch {
	case a.rec != nil && a.rec.sess.Dir == s.Dir:
		return "Recording"
	case a.working != nil && a.working.Dir == s.Dir:
		return fmt.Sprintf("Transcribing %d%%", int(a.progress*100))
	}
	switch s.Meta.State {
	case session.Ready:
		if a.hasSummary(s) {
			return "Summarized"
		}
		return "Transcribed"
	case session.Failed:
		return "Failed"
	case session.Recorded, session.Transcribing:
		return "Waiting"
	}
	return string(s.Meta.State)
}

func sessionTitle(s *session.Session) string {
	if s.Meta.Title != "" {
		return s.Meta.Title
	}
	return defaultTitle(s)
}

func keepAudioDetail() string {
	if m4a.Available() {
		return "Both tracks stay in the session's folder, about 70 MB per hour"
	}
	return "Both tracks stay in the session's folder, about 230 MB per hour"
}

// defaultTitle is a recording's name without one given.
func defaultTitle(s *session.Session) string {
	if s.Meta.Provider != "" {
		return providerName(s.Meta.Provider) + " meeting"
	}
	return "Recording"
}

func sessionWhen(s *session.Session) string {
	t := s.Meta.Started.Format("Mon 2 Jan 2006 · 15:04")
	if !s.Meta.Ended.IsZero() {
		d := s.Meta.Ended.Sub(s.Meta.Started).Round(time.Minute)
		if d < time.Minute {
			t += " · under a minute"
		} else {
			t += fmt.Sprintf(" · %d min", int(d.Minutes()))
		}
	}
	return t
}

// --- settings -----------------------------------------------------------

var languages = []struct{ code, name string }{{"de", "German"}, {"en", "English"}}

func (a *App) languageControl(id string) ui.Node {
	sel := 0
	var names []string
	for i, l := range languages {
		names = append(names, l.name)
		if l.code == a.cfg.Language {
			sel = i
		}
	}
	return ui.Segmented{ID: id, Options: names, Selected: sel, OnSelect: func(i int) { a.setLanguage(languages[i].code) }}
}

var uiScales = []struct {
	pct   int
	label string
}{
	{80, "80%"},
	{100, "100%"},
	{110, "110%"},
	{125, "125%"},
}

func (a *App) uiScaleIndex() int {
	pct := a.cfg.UIScale
	if pct <= 0 {
		pct = 100
	}
	best, bestD := 1, 1000
	for i, s := range uiScales {
		d := s.pct - pct
		if d < 0 {
			d = -d
		}
		if d < bestD {
			best, bestD = i, d
		}
	}
	return best
}

func (a *App) applyUIScale() {
	z := a.cfg.UIScaleFactor()
	if a.win != nil {
		a.win.SetZoom(z)
	}
	if a.notesFloat != nil {
		a.notesFloat.SetZoom(z)
	}
	if a.panel != nil {
		a.panel.SetZoom(z)
	}
	if a.notch != nil {
		a.notch.SetZoom(z)
	}
}

// toggleTheme switches between dark and light mode across every window and
// persists the choice to config.json.
func (a *App) toggleTheme() {
	a.cfg.LightTheme = !a.cfg.LightTheme
	ui.SetTheme(a.cfg.LightTheme)
	if a.win != nil {
		a.win.RefreshTheme()
	}
	if a.notesFloat != nil {
		a.notesFloat.RefreshTheme()
	}
	if a.panel != nil {
		a.panel.RefreshTheme()
	}
	if a.notch != nil {
		a.notch.RefreshTheme()
	}
	a.save()
}

func (a *App) setUIScale(pct int) {
	if pct == 100 {
		pct = 0 // default
	}
	if a.cfg.UIScale == pct {
		a.invalidate()
		return
	}
	a.cfg.UIScale = pct
	a.save()
	a.applyUIScale()
	a.invalidate()
}

func (a *App) uiScaleControl() ui.Node {
	labels := make([]string, len(uiScales))
	for i, s := range uiScales {
		labels[i] = s.label
	}
	sel := a.uiScaleIndex()
	if a.scaleDragOn {
		sel = a.scaleDragIdx
	}
	return ui.StepSlider{
		ID:       "set:uiscale",
		Labels:   labels,
		Selected: sel,
		OnChange: func(i int, done bool) {
			if !done {
				if !a.scaleDragOn || a.scaleDragIdx != i {
					a.scaleDragOn, a.scaleDragIdx = true, i
					a.invalidate()
				}
				return
			}
			a.scaleDragOn = false
			a.setUIScale(uiScales[i].pct)
		},
	}
}

func (a *App) settingsPage() ui.Node {
	var apps []ui.Node
	for _, p := range detect.Providers {
		p := p
		apps = append(apps, ui.Toggle{ID: "app:" + p.ID, Label: p.Name, On: a.cfg.ProviderEnabled(p.ID, p.On), OnChange: func(on bool) {
			if a.cfg.Providers == nil {
				a.cfg.Providers = map[string]bool{}
			}
			a.cfg.Providers[p.ID] = on
			a.save()
			a.invalidate()
		}})
	}
	return ui.VStack{Gap: 12, Items: append([]ui.Node{
		ui.Title("Settings"),
		ui.Space(4),
		ui.Caption("Transcription"),
		ui.Card{Gap: 2, Items: []ui.Node{
			ui.SettingRow{Label: "Meeting language", Detail: "Transcripts are best when this is set before the meeting", Control: a.languageControl("set:lang")},
			ui.Divider{},
			ui.Group(a.liveSettings()),
			ui.Divider{},
			ui.Group(a.finalSettings()),
			ui.Divider{},
			ui.Group(a.transcribeGPUSettings()),
			ui.Divider{},
			ui.Toggle{ID: "set:speakers", Label: "Tell speakers apart", Detail: "Labels up to four people on the meeting's side as Speaker 1, 2, … (adds a 139 MB model)", On: a.cfg.Speakers,
				OnChange: func(on bool) {
					a.cfg.Speakers = on
					a.save()
					if on && a.jobs != nil {
						a.jobs.prepare()
					}
					a.invalidate()
				}},
		}},
		ui.Caption("Meeting apps"),
		ui.Card{Gap: 0, Items: append([]ui.Node{
			ui.Toggle{ID: "set:auto", Label: "Record meetings automatically", Detail: "Starts 5 seconds after a meeting is detected, with Cancel in the notch; off: flit asks first", On: a.cfg.AutoRecord,
				OnChange: func(on bool) { a.cfg.AutoRecord = on; a.save(); a.invalidate() }},
			ui.Divider{},
			ui.Caption("Apps that are watched"),
		}, apps...)},
		ui.Group(a.calendarSettings()),
		ui.Caption("Recordings"),
		ui.Card{Gap: 4, Items: []ui.Node{
			ui.Toggle{ID: "set:keep", Label: "Keep recordings after transcription", Detail: keepAudioDetail(), On: a.cfg.KeepAudio,
				OnChange: func(on bool) { a.cfg.KeepAudio = on; a.save(); a.invalidate() }},
			ui.HStack{Items: []ui.Node{ui.Link{ID: "set:recfolder", Glyph: ui.GlyphFolder, Label: "Open recordings folder", OnClick: func() { openFolder(session.Root(a.dataDir)) }}}},
		}},
		ui.Caption("Summaries"),
		ui.Card{Gap: 2, Items: a.summarySettings()},
		ui.Caption("App"),
		ui.Card{Gap: 4, Items: []ui.Node{
			ui.SettingRow{Label: "Interface scale", Detail: "Scales the window, floating notes, tray popover and notch", Control: a.uiScaleControl()},
			ui.Divider{},
			ui.Toggle{ID: "set:autostart", Label: "Start with Windows", Detail: "flit needs to run to notice meetings", On: install.AutoStartEnabled(), OnChange: func(on bool) {
				if err := install.SetAutoStart(on); err != nil {
					logx.Printf("autostart: %v", err)
				}
				a.invalidate()
			}},
			ui.Divider{},
			ui.Group(a.updateSettings()),
			ui.Divider{},
			ui.SettingRow{Label: "flit " + Version, Detail: "Data in " + shortenPath(a.dataDir), Control: ui.HStack{Gap: 8, Right: true, Items: []ui.Node{
				ui.Button{ID: "set:debug", Label: "Debug", OnClick: func() { a.go_(pageDebug) }},
				ui.Button{ID: "set:log", Label: "Log", OnClick: func() { openFile(logx.Path(a.dataDir)) }},
				ui.Button{ID: "set:data", Label: "Data folder", Glyph: ui.GlyphFolder, OnClick: func() { openFolder(a.dataDir) }},
			}}},
		}},
		ui.Space(8),
	}, a.dangerSection()...)}
}

// --- welcome ------------------------------------------------------------

func (a *App) welcomePage() ui.Node {
	return ui.VStack{Gap: 12, Items: []ui.Node{
		ui.Title("Welcome to flit"),
		ui.Para{Text: "flit waits in the tray. When a meeting app starts using your microphone, it offers to record the meeting, then transcribes your voice and the others' separately, on this PC. Nothing leaves your computer.", Muted: true},
		ui.Space(4),
		ui.Card{Gap: 2, Items: []ui.Node{
			ui.SettingRow{Label: "What's your name?", Detail: "Helps flit transcribe your name and spot your tasks", Control: a.nameFields("welcome:name")},
			ui.Divider{},
			ui.SettingRow{Label: "Your meetings are mostly in", Control: a.languageControl("welcome:lang")},
			ui.Divider{},
			ui.Toggle{ID: "welcome:autostart", Label: "Start with Windows", Detail: "Recommended: flit can only notice meetings while it runs", On: install.AutoStartEnabled(), OnChange: func(on bool) {
				_ = install.SetAutoStart(on)
				a.invalidate()
			}},
			ui.Divider{},
			a.welcomeGPURow(),
		}},
		ui.Space(4),
		ui.Caption("Speech models"),
		ui.Para{Text: "Downloaded once, all at the same time, and kept on this PC. You can finish setting up meanwhile.", Muted: true, Small: true},
		ui.Group(a.modelCards(false)),
		ui.Space(4),
		ui.HStack{Right: true, Items: []ui.Node{ui.Button{ID: "welcome:done", Label: "Done", Glyph: ui.GlyphCheck, Primary: true, OnClick: func() {
			a.cfg.SetupComplete = true
			a.save()
			a.go_(pageHome)
		}}}},
	}}
}

func shortenPath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(strings.ToLower(p), strings.ToLower(home)) {
		return "~" + p[len(home):]
	}
	return p
}

func openFile(path string) {
	if _, err := os.Stat(path); err != nil {
		openFolder(filepath.Dir(path))
		return
	}
	_ = exec.Command("explorer.exe", path).Start() // opens with the default app
}
