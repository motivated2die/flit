package app

import (
	"fmt"
	"time"

	"github.com/motivated2die/flit/internal/logx"
	"github.com/motivated2die/flit/internal/ui"
)

// The tray panel: a right-click on the tray icon opens it by the taskbar.
// It shows what flit is doing in more detail than the icon (the meeting,
// the clock and both levels while recording, what is being watched while
// idle) and the actions that fit. Without Direct2D the native menu stays.

func (a *App) initPanel() {
	p, err := ui.NewPanel()
	if err != nil {
		logx.Printf("tray panel unavailable, using the menu: %v", err)
		return
	}
	a.panel = p
	p.Build = a.panelContent
	p.Lit = func() bool { return a.rec != nil } // the recording dot's corner glows
	a.tray.OnRightClick = func(x, y int32) {
		if p.Visible() {
			p.Hide()
			return
		}
		if p.RecentlyHidden() { // this click closed it
			return
		}
		p.ShowAt(x, y)
	}
}

// openPage opens the main window on a page.
func (a *App) openPage(page string) {
	a.go_(page)
	a.openWindow()
}

// act runs an action and closes the panel, as a menu would.
func (a *App) act(fn func()) func() {
	return func() {
		if a.panel != nil {
			a.panel.Hide()
		}
		fn()
	}
}

// panelAct runs a status-section action while keeping the panel open so it
// transitions straight into the new state and refits its height.
func (a *App) panelAct(fn func()) func() {
	return func() {
		fn()
		if a.panel != nil {
			a.panel.Refresh()
		}
	}
}

func (a *App) panelContent() ui.Node {
	var body []ui.Node
	if meetings := a.panelMeetings(); len(meetings) > 1 {
		// Skip the leading divider: the rounded card edge separates it.
		body = append(body, meetings[1:]...)
	}
	if s := a.latestReady(); s != nil && a.rec == nil {
		if len(body) > 0 {
			body = append(body, ui.Divider{})
		}
		facts := s.Meta.Started.Format("Mon 2 Jan · 15:04")
		if w := a.wordCount(s); w > 0 {
			facts += " · " + plural(w, "word")
		}
		body = append(body, ui.Caption("Latest transcript"),
			ui.RecordingRow{ID: "panel:latest", Title: sessionTitle(s), Facts: facts, NoIcon: true,
				Actions: []ui.Node{ui.RoundButton{ID: "panel:latest:copy", Glyph: ui.GlyphCopy, OnClick: a.act(func() { a.copyTranscript(s, false) })}},
				OnClick: a.act(func() { a.openPage(pageSession + s.Meta.ID) })})
	}
	if len(body) > 0 {
		body = append(body, ui.Divider{})
	}
	body = append(body, ui.HStack{Items: []ui.Node{
		ui.BrandLink{ID: "panel:open", Detail: "Click to open the app", OnClick: a.act(a.openWindow)},
		ui.Space(0),
		ui.Inset{Top: 4, Child: ui.Button{ID: "panel:quit", Label: "Quit", Glyph: ui.GlyphPower, HoverDanger: true, OnClick: a.act(a.quit)}},
	}})
	return ui.PanelSheet{
		Top:  a.panelStatus(),
		Body: ui.VStack{Gap: 8, Items: body},
	}
}

// panelStatus is the panel's top: what flit is doing and the one action
// that fits, without explanations (the window has those).
func (a *App) panelStatus() ui.Node {
	switch {
	case a.rec != nil:
		title := "Recording"
		if a.rec.sess.Meta.Title != "" {
			title = a.rec.sess.Meta.Title
		}
		return ui.VStack{Gap: 10, Items: []ui.Node{
			ui.LiveTitle{Text: shorten(title, 24), Detail: ui.Clock(time.Since(a.rec.started))},
			ui.HStack{Gap: 8, Items: []ui.Node{
				ui.Button{ID: "panel:stop", Label: "Transcribe", Glyph: ui.GlyphStop, Primary: true, OnClick: a.panelAct(a.stopRecording)},
				a.confirmButtonAs("panel:cancel", "Cancel", "Click again", false, a.panelAct(a.cancelRecording)),
			}},
			ui.VoiceMeter{Labels: [2]string{"You", "Meeting"}, Bands: a.voiceBands, Smooth: &a.voiceSmooth, Flat: true},
		}}
	case a.detected != nil:
		c := *a.detected
		record, cancel := "Transcribe", "Not now"
		if a.counting() {
			record, cancel = "Record now", "Cancel"
		}
		return ui.VStack{Gap: 10, Items: []ui.Node{
			ui.Label{Text: providerName(c.Provider) + " meeting detected", Large: true},
			ui.HStack{Gap: 8, Items: []ui.Node{
				ui.Button{ID: "panel:record", Label: record, Glyph: ui.GlyphRecord, Primary: true, OnClick: a.panelAct(func() { a.startRecording(&c) })},
				ui.Button{ID: "panel:dismiss", Label: cancel, OnClick: a.panelAct(func() { a.onNotchButton("dismiss") })},
			}},
		}}
	case a.working != nil || a.status != "":
		title := "Transcribing"
		if a.status != "" {
			title = a.status
		}
		return ui.VStack{Gap: 10, Items: []ui.Node{
			ui.HStack{Items: []ui.Node{ui.Label{Text: shorten(title, 30), Large: true}, ui.Label{Text: fmt.Sprintf("%d%%", int(a.progress*100)), Muted: true, Right: true}}},
			ui.Progress{Value: a.progress},
		}}
	}
	return ui.VStack{Gap: 10, Items: []ui.Node{
		ui.Label{Text: "Ready and idle", Large: true},
		ui.HStack{Items: []ui.Node{ui.Button{ID: "panel:start", Label: "Start recording", Glyph: ui.GlyphRecord, Primary: true, OnClick: a.panelAct(func() { a.startRecording(nil) })}}},
	}}
}
