package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/motivated2die/flit/internal/calendar"
	"github.com/motivated2die/flit/internal/detect"
	"github.com/motivated2die/flit/internal/install"
	"github.com/motivated2die/flit/internal/logx"
	"github.com/motivated2die/flit/internal/session"
	"github.com/motivated2die/flit/internal/ui"
)

// The notch is how flit speaks up. Without Direct2D it falls back to
// Windows notifications from the tray icon.

func (a *App) initNotch() {
	n, err := ui.NewNotch()
	if err != nil {
		logx.Printf("notch unavailable, using notifications: %v", err)
		return
	}
	a.notch = n
	n.OnButton = a.onNotchButton
	n.OnUnstash = a.unstashNotes
	n.OnAutoHide = func() {
		if a.joinPrompt != nil {
			a.clearJoinPrompt()
			if a.stashedNotes != "" {
				a.syncNotch()
			}
		}
	}
	n.Levels = a.peaks
}

func (a *App) onNotchButton(id string) {
	switch id {
	case "record":
		if a.detected != nil {
			c := *a.detected
			a.startRecording(&c)
		}
	case "dismiss":
		a.stopCountdown()
		if a.detected != nil {
			a.dismissed[a.detected.Provider] = true
			a.detected = nil
		}
		a.syncNotch()
		a.refresh()
	case "join":
		if m := a.joinPrompt; m != nil {
			link := m.Link
			a.clearJoinPrompt()
			if link != "" {
				openURL(link)
			}
			a.syncNotch()
		}
	case "join-close":
		a.clearJoinPrompt()
		a.syncNotch()
	case "stop":
		a.stopRecording()
	case "notes":
		if a.rec != nil {
			if a.notesFloat != nil {
				a.pinNotes(a.rec.sess)
			} else {
				a.openNotes(a.rec.sess)
			}
		}
	case "copy":
		a.copyTranscript(a.lastReady, false) // "Transcript copied" replaces it
	case "open":
		// The title of a ready transcript: straight to its recording.
		if s := a.lastReady; s != nil {
			a.lastReady = nil // acknowledged, as by copying it
			a.syncNotch()
			a.openPage(pageSession + s.Meta.ID)
			a.refresh()
		}
	case "close":
		if a.notch != nil && a.notch.View().Mode == ui.NotchReady {
			a.lastReady = nil
		}
		a.syncNotch()
	}
}

func (a *App) showPrompt(c detect.Candidate) {
	name := providerName(c.Provider) + " meeting"
	if title, ok := a.detectedName(c.Provider, c.Title); ok {
		name = shorten(title, 36) // the calendar knows which meeting
	}
	if a.notch == nil {
		a.onNotify = func() { a.startRecording(&c) }
		if a.counting() {
			a.onNotify = func() { a.onNotchButton("dismiss") }
			a.tray.Notify(name+" detected", "Recording starts in a few seconds. Click here to cancel.")
			return
		}
		a.tray.Notify(name+" detected", "Click here or on the flit icon to transcribe it.")
		return
	}
	if a.counting() {
		// Recording starts on its own: the capsule fills up until then.
		a.notch.Show(ui.NotchView{Mode: ui.NotchPrompt, Title: name + " detected", Detail: "recording starts automatically",
			CountFrom: a.autoFrom, CountFor: autoRecordAfter,
			Buttons: []ui.NotchButton{{ID: "record", Label: "Record now", Primary: true}, {ID: "dismiss", Label: "Cancel"}}})
		return
	}
	detail := ""
	if t := meetingTitle(c); t != providerName(c.Provider)+" meeting" {
		detail = t
	}
	a.notch.Show(ui.NotchView{Mode: ui.NotchPrompt, Title: name, Detail: shorten(detail, 32), AutoHide: 30 * time.Second,
		Buttons: []ui.NotchButton{{ID: "record", Label: "Transcribe", Primary: true}, {ID: "dismiss", Label: "Not now"}}})
}

const (
	joinPromptLead     = time.Minute
	joinPromptDuration = 15 * time.Second
)

// showJoinPrompt opens the notch 1 minute before a calendar meeting with a
// Join link, a Close button, and a 15-second sweeping progress background.
// Joining opens the meeting link without starting a recording (recording
// starts naturally once the meeting app opens).
func (a *App) showJoinPrompt(m calendar.Meeting) {
	mc := m
	a.joinPrompt = &mc
	now := time.Now()
	if a.notch == nil {
		link := m.Link
		a.onNotify = func() {
			if link != "" {
				openURL(link)
			}
		}
		if a.tray != nil {
			a.tray.Notify(shorten(m.Title, 48), "Starts in 1 minute. Click to join.")
		}
		return
	}
	detail := "starts in 1 min"
	if m.Start.Sub(now) <= 15*time.Second {
		detail = "starting now"
	}
	a.notch.Show(ui.NotchView{
		Mode:      ui.NotchPrompt,
		Title:     shorten(m.Title, 36),
		Detail:    detail,
		CountFrom: now,
		CountFor:  joinPromptDuration,
		AutoHide:  joinPromptDuration,
		Buttons:   []ui.NotchButton{{ID: "join", Label: "Join", Primary: true}, {ID: "join-close", Label: "Close"}},
	})
}

func (a *App) clearJoinPrompt() {
	a.joinPrompt = nil
}

// syncNotch reconciles the notch with the app's current state, in priority
// order: stash drop target > detected meeting > upcoming join prompt >
// recording > transcribing > ready transcript > stashed notes > hidden.
func (a *App) syncNotch() {
	a.syncNotchState(false)
}

// syncNotchCompact reconciles the notch like syncNotch, but transitions a
// running recording directly into its compact capsule instead of expanding it
// for CollapseAfter first (used after cancelling a notes drag or unstashing).
func (a *App) syncNotchCompact() {
	a.syncNotchState(true)
}

func (a *App) syncNotchState(startCollapsed bool) {
	if a.notch == nil {
		return
	}
	switch {
	case a.stashDragging:
		a.notch.Show(ui.NotchView{Mode: ui.NotchStashDrop, Title: "Stash the notes", Highlight: a.stashReady})
	case a.detected != nil:
		a.showPrompt(*a.detected)
	case a.joinPrompt != nil:
		// Keep the active join prompt and its countdown undisturbed.
	case a.rec != nil:
		a.showRecordingView(startCollapsed)
	case a.working != nil || a.status != "":
		a.showWorking(true)
	case a.lastReady != nil:
		text, _ := a.lastReady.Transcript(false)
		a.showReady(a.lastReady, countWordsIn(text))
	case a.stashedNotes != "":
		a.notch.Show(ui.NotchView{Mode: ui.NotchStashed, StashedNotes: true})
	default:
		a.notch.Hide()
	}
}

func (a *App) showRecording() {
	a.showRecordingView(false)
}

func (a *App) showRecordingView(startCollapsed bool) {
	if a.notch == nil || a.rec == nil {
		return
	}
	a.notch.Show(ui.NotchView{
		Mode:           ui.NotchRecording,
		Title:          "Recording",
		Since:          a.rec.started,
		Collapsible:    true,
		HoverDelay:     ui.ExpandHover, // delay expansion only while recording
		StashedNotes:   a.stashedNotes != "",
		StartCollapsed: startCollapsed,
		Buttons:        []ui.NotchButton{{ID: "notes", Label: "Notes"}, {ID: "stop", Label: "Stop"}},
	})
}

// showWorking shows the transcription after a meeting (start) and keeps
// its progress up to date while it shows; it collapses out of the way and
// stays until the transcript is ready.
func (a *App) showWorking(start bool) {
	if a.notch == nil {
		return
	}
	if !start {
		if cur := a.notch.View(); cur.Mode != ui.NotchWorking || !a.notch.Visible() {
			return // only update a working notch that is showing
		}
	}
	v := ui.NotchView{Mode: ui.NotchWorking, Title: "Transcribing", Progress: a.progress, Collapsible: true}
	if a.status != "" {
		v.Title = a.status // the percentage follows, from Progress
	}
	a.notch.Show(v)
}

func (a *App) showReady(s *session.Session, words int) {
	if a.notch == nil {
		a.onNotify = func() { a.copyTranscript(s, false) }
		a.tray.Notify("Transcript ready", fmt.Sprintf("%d words. Click to copy them.", words))
		return
	}
	// It stays (collapsed when left alone) until the user copies or closes.
	a.notch.Show(ui.NotchView{Mode: ui.NotchReady, Title: "Transcript ready", Detail: fmt.Sprintf("%d words", words), Collapsible: true, TitleID: "open",
		Buttons: []ui.NotchButton{{ID: "close", Label: "Close"}, {ID: "copy", Label: "Copy", Primary: true}}})
}

func (a *App) showMessage(title, detail string, warning bool) {
	if a.notch == nil {
		a.tray.Notify(title, detail)
		return
	}
	a.notch.Show(ui.NotchView{Mode: ui.NotchMessage, Title: title, Detail: shorten(detail, 48), Warning: warning, AutoHide: 8 * time.Second})
}

// showStatus shows what flit is doing (a click on the tray icon).
func (a *App) showStatus() {
	switch {
	case a.rec != nil:
		a.showRecording()
	case a.detected != nil:
		a.showPrompt(*a.detected)
	case a.working != nil || a.status != "":
		a.showWorking(true)
	case a.lastReady != nil:
		text, _ := a.lastReady.Transcript(false)
		a.showReady(a.lastReady, countWordsIn(text))
	default:
		a.showMessage("flit", a.statusLine(), false)
	}
}

func shorten(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// welcome says hello after an install or update (the installer starts the
// installed copy with --welcome=install|update), since a new tray icon
// often lands among the hidden icons where nobody sees it.
func (a *App) welcome(args []string) {
	for _, arg := range args {
		kind, ok := strings.CutPrefix(arg, install.WelcomeFlag)
		if !ok {
			continue
		}
		title := "flit is installed"
		if kind == "update" {
			title = "flit was updated"
		}
		detail := "It waits in the tray for your next meeting."
		if a.status != "" || a.working != nil {
			detail = "Getting the speech model ready in the background."
		}
		a.showMessage(title, detail, false)
		if !a.cfg.SetupComplete {
			a.openWindow()
		}
		return
	}
}
