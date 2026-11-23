// Package app wires flit together: roles of the executable, the tray app's
// state on the UI thread, and the work that runs beside it.
package app

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/motivated2die/flit/internal/calendar"
	"github.com/motivated2die/flit/internal/capture"
	"github.com/motivated2die/flit/internal/config"
	"github.com/motivated2die/flit/internal/detect"
	"github.com/motivated2die/flit/internal/engine"
	"github.com/motivated2die/flit/internal/install"
	"github.com/motivated2die/flit/internal/instance"
	"github.com/motivated2die/flit/internal/logx"
	"github.com/motivated2die/flit/internal/session"
	"github.com/motivated2die/flit/internal/sysinfo"
	"github.com/motivated2die/flit/internal/transcript"
	"github.com/motivated2die/flit/internal/ui"
)

// Version is set by release builds (-ldflags -X).
var Version = "0.1.1-dev"

// Run dispatches on the first argument:
//
//	(none), --autostart, --welcome=…   the tray app (installing itself first unless portable)
//	--engine-worker                    the speech engine worker (started by the tray app)
//	--uninstall [--confirmed] [--delete-data]   remove flit for this user
//	transcribe …                       transcribe WAV files on the console (development, benchmarks)
//	record …                           record and transcribe a session on the console
func Run(args []string) int {
	if len(args) > 0 {
		switch args[0] {
		case engine.WorkerFlag:
			return engine.RunWorker()
		case "transcribe":
			attachConsole()
			return runTranscribe(args[1:])
		case "record":
			attachConsole()
			return runRecord(args[1:])
		case "notch-demo":
			return runNotchDemo()
		case install.UninstallFlag:
			return runUninstall(args[1:])
		}
	}
	return runTray(args)
}

// App is the tray app. Its fields belong to the UI thread unless noted.
type App struct {
	dataDir    string
	cfg        config.Config
	portable   bool // a development build: never installs or updates itself
	tray       *ui.Tray
	notch      *ui.Notch // nil: notifications instead
	win        *ui.Window
	panel      *ui.Panel       // nil: the native menu instead
	notesFloat *ui.FloatWindow // always-on-top popped-out notes panel

	// The Audio page's devices and level meters.
	devs        devices
	audioMeters map[string]*capture.Meter
	audioLevels map[string]float32

	// The main window's state.
	page     string
	sessions []*session.Session
	previews map[string]string // transcript text by session ID
	peak     [2]float32        // the recording's last sampled peaks (me, them)
	peakAt   time.Time
	// voiceSmooth keeps the voice meter's bars between frames.
	voiceSmooth ui.MeterSmoother
	dbg         debugState
	gpu         sysinfo.GPUInfo // detected once at start-up (DXGI + vulkan-1.dll)

	watcher   *detect.Watcher
	detected  *detect.Candidate // a meeting going on that isn't recorded
	dismissed map[string]bool   // providers the user said "not now" to, until their meeting ends
	rec       *recording

	jobs    *jobs
	working *session.Session // being transcribed
	finding *session.Session // its speakers being found again
	player  *appPlayer       // the open recording's player, if any
	// scrubbing is set while the player's knob is dragged, to scrubAt (a
	// share of the recording).
	scrubbing bool
	scrubAt   float32
	// The Recordings page's search: the words, whether transcripts are
	// searched too, and each transcript's text in lower case.
	search     string
	searchDeep bool
	searchText map[string]string
	storage    int64  // bytes in the sessions folder
	confirm    string // a destructive button waiting for its second click
	usage      usage  // what Settings' cleanup would free
	// The uninstall popover: open, deleting the data too, and how much
	// the data folder holds (counted when it opens).
	uninstallAsk  bool
	uninstallWipe bool
	dataSize      int64
	// The transcript search on a recording's page: the words, which
	// result is shown, and whether to scroll to it on the next build.
	tsQuery string
	tsIndex int
	tsJump  bool
	// Models coming down (by ID), and the last error of those that failed.
	downloads   map[string]*modelDownload
	downloadErr map[string]error
	// The transcript paragraph whose copy button was just used, and when.
	copied   string
	copiedAt time.Time
	// autoFrom is when the countdown to record the detected meeting
	// started (zero: none); autoToken tells a stale timer from the current.
	cal       *calendarState // nil until started (and in tests)
	live      *liveJob       // transcription during the recording, if on
	autoFrom  time.Time
	autoToken int
	confirmAt time.Time
	paraCache map[string][]transcript.Paragraph
	// notes are the recordings' notes being edited (notes_windows.go);
	// focusNotes asks the next layout to give that recording's editor the
	// keyboard.
	notes       map[string]*notesState
	summaries   map[string]*notesState // nil entry: none written
	sumRun      *summaryRun            // the summary being written
	sumErr      map[string]string      // why a recording's last summary failed
	sumInstr    map[string]string      // the extra instruction typed, per recording
	focusNotes  string
	focusTitle  string // the same for a recording's title field
	pinnedNotes   string            // session ID shown in notesFloat ("" when not pinned)
	stashedNotes  string            // session ID stashed in the notch ("" when not stashed)
	stashDragging bool              // notesFloat is currently being dragged
	stashReady    bool              // dragged notesFloat is close enough to the notch to stash
	focusFloat    bool              // ask notesFloat's editor for keyboard focus once
	joinPrompt   *calendar.Meeting // upcoming calendar meeting currently shown in the notch
	joinPrompted map[string]bool   // meeting keys already shown a join prompt
	// speakerCache holds each session's speakers (read from its transcript)
	// until it changes.
	speakerCache map[string][]transcript.SpeakerSummary
	progress     float64
	status       string // what the engine side is doing, for the tooltip
	lastReady    *session.Session
	failed       error

	// update is the check for newer releases and their download.
	update updater

	// onNotify is what a click on the current notification does.
	onNotify func()

	// providers is the detection goroutine's copy of the provider switches.
	providersMu sync.Mutex
	providers   map[string]bool
}

func runTray(args []string) int {
	dataDir := config.Dir()
	closeLog, err := logx.Init(dataDir)
	defer closeLog()
	if err != nil {
		fmt.Fprintln(os.Stderr, "logging:", err)
	}
	if sz, mod, ok := logx.CrashInfo(dataDir); ok {
		logx.Printf("previous crash log present: %s (%d bytes, %s)", filepath.Base(logx.CrashPath(dataDir)), sz, mod.Format("2006-01-02 15:04:05"))
	}
	closeCrash, err := logx.InitCrash(dataDir)
	defer closeCrash()
	if err != nil {
		logx.Printf("crash logging: %v", err)
	}
	ui.InitDPIAwareness()
	logx.Printf("flit %s starting %q", Version, args)

	if exit, err := install.Ensure(Version, dataDir, args); err != nil {
		logx.Printf("install failed, running from here: %v", err)
		ui.ShowError("flit", "flit could not be installed and runs from its current location:\n\n"+err.Error())
	} else if exit {
		logx.Printf("installed; the installed copy takes over")
		return 0
	}

	lock, err := instance.Acquire()
	if errors.Is(err, instance.ErrRunning) {
		logx.Printf("already running; asking it to show itself")
		instance.Show()
		return 0
	}
	if err != nil {
		logx.Printf("single-instance lock: %v", err)
	}
	defer lock.Release()

	a := &App{dataDir: dataDir, dismissed: map[string]bool{}, portable: install.Portable()}
	if a.cfg, err = config.Load(dataDir); err != nil {
		logx.Printf("config: %v", err)
	}
	a.initGPU()
	a.syncProviders()
	a.tray, err = ui.NewTray("flit")
	if err != nil {
		logx.Printf("fatal: %v", err)
		ui.ShowError("flit", "Could not create the tray icon:\n"+err.Error())
		return 1
	}
	a.tray.Menu = a.menu
	a.tray.OnClick = a.onTrayClick
	a.tray.OnQuitRequest = a.quit
	a.tray.OnShow = a.openWindow
	a.initNotch()
	a.initWindow()
	a.initPanel()
	a.tray.OnNotificationClick = func() {
		if f := a.onNotify; f != nil {
			a.onNotify = nil
			f()
		}
	}
	a.tray.OnEndSession = func() {
		logx.Printf("Windows is ending the session")
		a.shutdown()
	}

	a.jobs = startJobs(a)
	a.applyFinalLimit()
	a.applyGPU()
	a.startCalendar()
	for _, s := range session.Recover(dataDir) {
		logx.Printf("session %s: picked up again (%s)", s.Meta.ID, s.Meta.State)
		a.jobs.add(s)
	}
	a.watcher = detect.Watch(2500*time.Millisecond, a.providerEnabled,
		func(ev detect.Event) { a.tray.Post(func() { a.onMeeting(ev) }) },
		func(err error) { logx.Printf("detection: %v", err) })
	a.startUpdateChecks()
	a.refresh()
	logx.Printf("ready")
	a.welcome(args)
	a.tray.Run()
	a.shutdown()
	logx.Printf("exited")
	return 0
}

// providerEnabled runs on the watcher's goroutine, on its own copy of the
// switches (save updates it).
func (a *App) providerEnabled(p detect.Provider) bool {
	a.providersMu.Lock()
	defer a.providersMu.Unlock()
	if v, ok := a.providers[p.ID]; ok {
		return v
	}
	return p.On
}

func (a *App) syncProviders() {
	m := make(map[string]bool, len(a.cfg.Providers))
	for k, v := range a.cfg.Providers {
		m[k] = v
	}
	a.providersMu.Lock()
	a.providers = m
	a.providersMu.Unlock()
}

func (a *App) menu() []ui.MenuItem {
	items := []ui.MenuItem{
		{Label: "flit " + Version + " · " + a.statusLine(), Disabled: true},
		{Label: "Open flit", OnClick: a.openWindow},
		{},
	}
	switch {
	case a.rec != nil:
		items = append(items, ui.MenuItem{Label: "Stop and transcribe", OnClick: a.stopRecording})
	case a.detected != nil:
		c := *a.detected
		items = append(items,
			ui.MenuItem{Label: "Transcribe this " + providerName(c.Provider) + " meeting", OnClick: func() { a.startRecording(&c) }},
			ui.MenuItem{Label: "Record all audio instead", OnClick: func() { a.startRecording(nil) }})
	default:
		items = append(items, ui.MenuItem{Label: "Start recording", OnClick: func() { a.startRecording(nil) }})
	}
	last := a.lastReady
	items = append(items,
		ui.MenuItem{Label: "Copy last transcript", Disabled: last == nil, OnClick: func() { a.copyTranscript(last, false) }},
		ui.MenuItem{Label: "Copy last transcript as Markdown", Disabled: last == nil, OnClick: func() { a.copyTranscript(last, true) }},
		ui.MenuItem{Label: "Open recordings folder", OnClick: func() { openFolder(session.Root(a.dataDir)) }},
		ui.MenuItem{},
		ui.MenuItem{Label: "Meeting language: German", Checked: a.cfg.Language == "de", OnClick: func() { a.setLanguage("de") }},
		ui.MenuItem{Label: "Meeting language: English", Checked: a.cfg.Language == "en", OnClick: func() { a.setLanguage("en") }},
		ui.MenuItem{Label: "Start with Windows", Checked: install.AutoStartEnabled(), OnClick: func() {
			if err := install.SetAutoStart(!install.AutoStartEnabled()); err != nil {
				logx.Printf("autostart: %v", err)
			}
		}},
		ui.MenuItem{},
		ui.MenuItem{Label: "Quit", OnClick: a.quit},
	)
	return items
}

// onTrayClick opens the main window.
func (a *App) onTrayClick() { a.openWindow() }

func (a *App) setLanguage(lang string) {
	a.cfg.Language = lang
	a.save()
	a.jobs.prepare() // fetch that language's model ahead
	a.refresh()
}

// statusLine says what flit is doing, in a few words.
func (a *App) statusLine() string {
	switch {
	case a.rec != nil:
		return "Recording since " + a.rec.started.Format("15:04")
	case a.working != nil:
		if a.status != "" {
			return a.downloadLine()
		}
		return fmt.Sprintf("Transcribing %d%%", int(a.progress*100))
	case a.status != "":
		return a.downloadLine()
	case a.detected != nil:
		return providerName(a.detected.Provider) + " meeting detected"
	case a.failed != nil:
		return "Last transcription failed"
	case a.lastReady != nil:
		return "Transcript ready"
	}
	return "Idle"
}

// refresh brings the tray icon and tooltip up to date.
func (a *App) refresh() {
	if a.tray == nil { // no tray yet (or in tests): just the window
		a.invalidate()
		return
	}
	state, p := ui.TrayIdle, 0.0
	switch {
	case a.rec != nil:
		state = ui.TrayRecording
	case a.working != nil || a.status != "":
		state, p = ui.TrayTranscribing, a.progress
	case a.detected != nil:
		state = ui.TrayDetected
	case a.failed != nil:
		state = ui.TrayError
	case a.lastReady != nil:
		state = ui.TrayReady
	}
	a.tray.SetState(state, p)
	a.invalidate()
	if a.panel != nil {
		a.panel.Refresh()
	}
	lang := map[string]string{"de": "German", "en": "English"}[a.cfg.Language]
	a.tray.SetTooltip("flit · " + a.statusLine() + " · " + lang)
}

func (a *App) copyTranscript(s *session.Session, markdown bool) {
	if s == nil {
		return
	}
	text, err := s.Transcript(markdown)
	if err == nil {
		err = ui.SetClipboard(a.tray.Hwnd(), text)
	}
	if err != nil {
		logx.Printf("copy: %v", err)
		a.showMessage("Couldn't copy the transcript", err.Error(), true)
		return
	}
	a.showMessage("Transcript copied", fmt.Sprintf("%d words", countWordsIn(text)), false)
	if a.lastReady == s {
		a.lastReady = nil // acknowledged: back to idle
	}
	a.refresh()
}

// copyTranscriptAs copies the transcript from a section link and briefly shows
// "Copied" on key.
func (a *App) copyTranscriptAs(s *session.Session, markdown bool, key string) {
	if s == nil || a.tray == nil {
		return
	}
	text, err := s.Transcript(markdown)
	if err == nil {
		err = ui.SetClipboard(a.tray.Hwnd(), text)
	}
	if err != nil {
		logx.Printf("copy: %v", err)
		return
	}
	if a.lastReady == s {
		a.lastReady = nil
	}
	a.copied, a.copiedAt = key, time.Now()
	a.refresh()
	time.AfterFunc(copiedFor, func() { a.tray.Post(a.invalidate) })
}

func (a *App) save() {
	a.syncProviders()
	if err := config.Save(a.dataDir, a.cfg); err != nil {
		logx.Printf("saving config: %v", err)
	}
}

func (a *App) quit() {
	logx.Printf("quit")
	if a.notesFloat != nil {
		a.notesFloat.Hide()
	}
	a.saveAllNotes()
	a.cancelSummary() // the server is in a job that also ends with flit
	a.shutdown()
	a.tray.Quit()
}

var shutdownOnce = make(chan struct{})

// shutdown stops what runs; recordings are finished so the next start
// transcribes them.
func (a *App) shutdown() {
	select {
	case <-shutdownOnce:
		return
	default:
		close(shutdownOnce)
	}
	if a.watcher != nil {
		a.watcher.Stop()
	}
	for k, m := range a.audioMeters {
		m.Close()
		delete(a.audioMeters, k)
	}
	if a.rec != nil {
		a.finishRecording(false)
	}
	a.stopLiveNow()
	a.jobs.close()
	a.stopCalendar()
	a.save()
}

func openFolder(dir string) {
	_ = os.MkdirAll(dir, 0o755)
	_ = exec.Command("explorer.exe", dir).Start()
}

func providerName(id string) string {
	if p, ok := detect.ProviderByID(id); ok {
		return p.Name
	}
	return "meeting"
}

func runUninstall(args []string) int {
	confirmed, wipe := uninstallArgs(args)
	// Not in a folder about to go (who started it chose the directory).
	_ = os.Chdir(os.TempDir())
	dataDir := config.Dir()
	closeLog, _ := logx.Init(dataDir)
	logOpen := true
	defer func() {
		if logOpen {
			closeLog()
		}
	}()
	// From Settings, the choice was made in flit's own popover; from
	// Windows' Installed apps, ask here, and keep the data.
	if !confirmed && !ui.MessageBox("Uninstall flit", "Remove flit from this PC?\n\nYour recordings and transcripts stay in "+dataDir+" until you delete them.", true, false) {
		return 0
	}
	if !instance.Quit(10 * time.Second) {
		logx.Printf("uninstall: flit didn't quit in time")
	}
	install.Unregister()
	if wipe && !removableDataDir(dataDir) {
		logx.Printf("uninstall: not deleting %s: not flit's data folder", dataDir)
		wipe = false
	}
	if wipe {
		logx.Printf("uninstalled; deleting the data folder")
		closeLog() // the log is in it
		logOpen = false
	} else {
		logx.Printf("uninstalled")
	}
	if err := removeFiles(dataDir, wipe); err != nil && logOpen {
		logx.Printf("removing the program folder: %v", err)
	}
	return 0
}

// removeFiles removes flit's files: first whatever still runs from the
// install folder (flit stuck quitting, an engine worker left over holding
// the models open) is stopped; with wipe, the data folder is deleted now,
// trying again while a file is still held; the install folder, with the
// running uninstaller in it, goes once this process has exited, and with
// it what is left of the data folder.
func removeFiles(dataDir string, wipe bool) error {
	install.StopOthers()
	dirs := []string{install.Dir()}
	switch {
	case wipe:
		if err := install.RemoveAll(dataDir, 10*time.Second); err != nil {
			dirs = append(dirs, dataDir)
		}
	case removableDataDir(dataDir):
		// Kept: recordings, transcripts, settings and the downloaded
		// models. The engine's DLLs, unpacked from the exe, are the
		// program's, not data: they go with it.
		engine := filepath.Join(dataDir, "engine")
		if err := install.RemoveAll(engine, 5*time.Second); err != nil {
			dirs = append(dirs, engine)
		}
	}
	return install.DeleteLater(dirs...)
}

// The uninstaller's flags after --uninstall.
const (
	uninstallConfirmed = "--confirmed"   // asked in flit's window already
	uninstallWipe      = "--delete-data" // remove the data folder too
)

func uninstallArgs(args []string) (confirmed, wipe bool) {
	for _, a := range args {
		switch a {
		case uninstallConfirmed:
			confirmed = true
		case uninstallWipe:
			wipe = true
		}
	}
	return confirmed, wipe
}

// removableDataDir reports whether dir looks like flit's data folder and
// nothing more: an absolute folder below a drive's root, not the user's
// home, holding flit's config.json or sessions folder.
func removableDataDir(dir string) bool {
	if dir == "" || !filepath.IsAbs(dir) {
		return false
	}
	dir = filepath.Clean(dir)
	if filepath.Dir(dir) == dir || strings.EqualFold(dir, filepath.VolumeName(dir)+`\`) {
		return false // a drive's root
	}
	if home, err := os.UserHomeDir(); err == nil && strings.EqualFold(filepath.Clean(home), dir) {
		return false
	}
	for _, f := range []string{"config.json", "sessions"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			return true
		}
	}
	return false
}
