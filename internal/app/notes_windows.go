package app

import (
	"strings"
	"time"

	"github.com/motivated2die/flit/internal/logx"
	"github.com/motivated2die/flit/internal/notes"
	"github.com/motivated2die/flit/internal/session"
	"github.com/motivated2die/flit/internal/ui"
)

// A recording's notes: typed on its page during and after the meeting,
// kept as notes.md (and notes.json, when each line was typed) in its
// folder (docs/research/notes-editor.md). The editor of each recording
// opened stays in memory while flit runs, so undo and the caret survive
// going to another page; saving follows a second after the last change,
// and at once on leaving the page, hiding the window, the end of the
// recording and quitting.

const notesSaveAfter = time.Second

// notesPlaceholder is what an empty editor says.
const notesPlaceholder = "Notes for this meeting. ## heading · - list · 1. numbered · [] task · **bold** · @flit to point flit at something"

// notesState is one recording's notes being edited.
type notesState struct {
	ed    *notes.Editor
	dir   string // the session folder
	saved int    // the editor's Rev when last saved
	timer *time.Timer
}

// notesFor is the editor of s's notes, read from its folder the first time.
func (a *App) notesFor(s *session.Session) *notesState {
	if a.notes == nil {
		a.notes = map[string]*notesState{}
	}
	if ns, ok := a.notes[s.Meta.ID]; ok {
		return ns
	}
	doc, err := s.Notes()
	if err != nil {
		logx.Printf("reading the notes of %s: %v", s.Meta.ID, err)
	}
	dir := s.Dir
	ed := notes.NewEditor(doc)
	// Lines typed while this recording runs remember when.
	ed.Clock = func() time.Duration {
		if a.rec != nil && a.rec.sess.Dir == dir {
			return time.Since(a.rec.started)
		}
		return notes.Unknown
	}
	ns := &notesState{ed: ed, dir: dir, saved: ed.Rev}
	a.notes[s.Meta.ID] = ns
	return ns
}

// notesChanged saves the notes of recording id a moment after the last change.
func (a *App) notesChanged(id string) {
	ns := a.notes[id]
	if ns == nil {
		return
	}
	if ns.timer != nil {
		ns.timer.Stop()
	}
	ns.timer = time.AfterFunc(notesSaveAfter, func() { a.tray.Post(func() { a.saveNotes(id) }) })
}

// saveNotes writes recording id's notes if they changed since last time.
// Only counts go to the log, never the text.
func (a *App) saveNotes(id string) {
	ns := a.notes[id]
	if ns == nil || ns.ed.Rev == ns.saved {
		return
	}
	if ns.timer != nil {
		ns.timer.Stop()
	}
	if err := (&session.Session{Dir: ns.dir}).SaveNotes(ns.ed.Doc); err != nil {
		logx.Printf("saving the notes of %s: %v", id, err)
		return
	}
	ns.saved = ns.ed.Rev
	logx.Printf("notes of %s saved: %d lines", id, len(ns.ed.Doc.Blocks))
}

// saveAllNotes saves every changed notes (leaving a page, hiding, quitting).
func (a *App) saveAllNotes() {
	for id := range a.notes {
		a.saveNotes(id)
	}
}

// dropNotes forgets recording id's notes (it was deleted).
func (a *App) dropNotes(id string) {
	if a.pinnedNotes == id {
		a.pinnedNotes = ""
		if a.notesFloat != nil {
			a.notesFloat.Hide()
		}
	}
	if a.stashedNotes == id {
		a.stashedNotes = ""
		a.stashDragging = false
		a.stashReady = false
		a.syncNotch()
	}
	if ns := a.notes[id]; ns != nil && ns.timer != nil {
		ns.timer.Stop()
	}
	delete(a.notes, id)
}

// pinNotes pops recording s's notes out into the always-on-top floating panel
// and hides the main window.
func (a *App) pinNotes(s *session.Session) {
	if s == nil {
		return
	}
	a.notesFor(s) // ensure editor exists
	wasStashed := a.stashedNotes != ""
	a.stashedNotes = ""
	a.stashDragging = false
	a.stashReady = false
	a.pinnedNotes = s.Meta.ID
	a.focusFloat = true
	var ax, ay int32
	var hasAnchor bool
	if a.win != nil {
		if a.win.Visible() {
			if _, t, r, _, ok := a.win.Rect(); ok {
				ax, ay, hasAnchor = r, t, true
			}
			a.win.Hide()
		}
	}
	if a.notesFloat != nil {
		if wasStashed {
			a.notesFloat.RestorePreDrag()
		}
		a.notesFloat.ShowNear(ax, ay, hasAnchor)
	}
	if wasStashed {
		a.syncNotch()
	}
	logx.Printf("notes of %s pinned", s.Meta.ID)
}

// onFloatDragMove expands the notch into "Stash the notes" as soon as the
// floating notes window is dragged, and highlights its background when close
// enough to the notch to stash.
func (a *App) onFloatDragMove(cx, cy, winTop int32) {
	if a.notch == nil || a.pinnedNotes == "" {
		return
	}
	inZone := a.notch.InStashZone(cx, cy, winTop)
	if !a.stashDragging || inZone != a.stashReady {
		a.stashDragging = true
		a.stashReady = inZone
		a.syncNotch()
	}
}

// onFloatDragEnd stashes the floating notes window into the notch when dropped
// in the top drop zone, or restores the notch otherwise.
func (a *App) onFloatDragEnd(cx, cy, winTop int32) bool {
	if a.notch == nil || a.pinnedNotes == "" {
		a.stashDragging = false
		a.stashReady = false
		return false
	}
	inZone := a.notch.InStashZone(cx, cy, winTop)
	wasDragging := a.stashDragging
	a.stashDragging = false
	a.stashReady = false
	if inZone {
		a.stashNotes(a.pinnedNotes)
		return true
	}
	if wasDragging {
		a.syncNotch()
	}
	return false
}

// stashNotes hides the floating notes window and keeps a grab handle in the
// notch so the user can pull the notes back out at any time.
func (a *App) stashNotes(id string) {
	if id == "" {
		return
	}
	a.saveNotes(id)
	a.pinnedNotes = ""
	a.stashedNotes = id
	if a.notesFloat != nil {
		a.notesFloat.Hide()
	}
	if a.rec != nil {
		a.showRecordingView(true)
	} else {
		a.syncNotch()
	}
	logx.Printf("notes of %s stashed", id)
}

// unstashNotes restores the floating notes window from the notch, either at
// its pre-stash position (click) or directly under the cursor for a continuous
// pull-out drag.
func (a *App) unstashNotes(cx, cy int32, drag bool) {
	id := a.stashedNotes
	if id == "" {
		return
	}
	s := a.sessionByID(id)
	if s == nil && a.rec != nil && a.rec.sess.Meta.ID == id {
		s = a.rec.sess
	}
	a.stashedNotes = ""
	a.stashDragging = false
	a.stashReady = false
	if s == nil {
		a.syncNotch()
		return
	}
	a.notesFor(s)
	a.pinnedNotes = id
	a.focusFloat = true
	if a.win != nil && a.win.Visible() {
		a.win.Hide()
	}
	a.syncNotch()
	if a.notesFloat != nil {
		if drag {
			a.notesFloat.ShowAtDrag(cx, cy)
		} else {
			a.notesFloat.RestorePreDrag()
			a.notesFloat.ShowNear(0, 0, false)
		}
	}
	logx.Printf("notes of %s unstashed", id)
}

// unpinNotes returns the pinned notes back into the main window on that
// recording's page.
func (a *App) unpinNotes() {
	id := a.pinnedNotes
	if id == "" {
		return
	}
	a.saveNotes(id)
	a.pinnedNotes = ""
	if a.notesFloat != nil {
		a.notesFloat.Hide()
	}
	a.page = pageSession + id
	a.focusNotes = id
	a.openWindow()
	logx.Printf("notes of %s unpinned", id)
}

// pinnedNotesNode builds the content of the floating notes window.
func (a *App) pinnedNotesNode() ui.Node {
	if a.pinnedNotes == "" {
		return nil
	}
	s := a.sessionByID(a.pinnedNotes)
	if s == nil {
		if a.rec != nil && a.rec.sess.Meta.ID == a.pinnedNotes {
			s = a.rec.sess
		} else {
			return nil
		}
	}
	ns := a.notesFor(s)
	focus := a.focusFloat
	if focus {
		a.focusFloat = false
	}
	id := s.Meta.ID
	return ui.NotesEditor{
		ID:          "float:" + id,
		Ed:          ns.ed,
		Placeholder: notesPlaceholder,
		Focus:       focus,
		Toolbar:     true,
		Pinned:      true,
		OnPin:       a.unpinNotes,
		MinH:        140,
		OnChange:    func() { a.notesChanged(id) },
	}
}

// openNotes shows s's page ready to type in (the notch's and Home's Notes
// buttons, Home's recording card and row): a running recording without a
// name starts in its title (Enter or Tab then goes to the notes), otherwise
// the notes, the caret at their end.
func (a *App) openNotes(s *session.Session) {
	if s == nil {
		return
	}
	ns := a.notesFor(s)
	ns.ed.End(true, false)
	title := s.Meta.Title
	if cur := a.sessionByID(s.Meta.ID); cur != nil {
		title = cur.Meta.Title // the list's copy is the one renamed
	}
	if a.isRecording(s) && title == "" {
		a.focusTitle, a.focusNotes = s.Meta.ID, ""
	} else {
		a.focusNotes, a.focusTitle = s.Meta.ID, ""
	}
	a.openPage(pageSession + s.Meta.ID)
}

// isRecording reports whether s is the recording running now.
func (a *App) isRecording(s *session.Session) bool {
	return a.rec != nil && s != nil && a.rec.sess.Dir == s.Dir
}

// notesSection is a recording's notes on its page: the editor in a card,
// with a caption and copy links above it (caption), or alone and taller
// (while the meeting records).
func (a *App) notesSection(s *session.Session, caption bool) []ui.Node {
	id := s.Meta.ID
	ns := a.notesFor(s)
	focus := a.focusNotes == id
	if focus {
		a.focusNotes = "" // asked for once
	}
	var right ui.Node
	if !ns.ed.Doc.Empty() {
		label, plainLabel := "Copy", "Copy as text"
		if a.copied == "notes:md:"+id && time.Since(a.copiedAt) < copiedFor {
			label = "Copied"
		}
		if a.copied == "notes:text:"+id && time.Since(a.copiedAt) < copiedFor {
			plainLabel = "Copied"
		}
		right = ui.HStack{Gap: 14, Right: true, Items: []ui.Node{
			ui.Link{ID: "notes:text:" + id, Label: plainLabel, OnClick: func() { a.copyNotes(id, false) }},
			ui.Link{ID: "notes:md:" + id, Glyph: ui.GlyphCopy, Label: label, OnClick: func() { a.copyNotes(id, true) }},
		}}
	}
	minH := float32(120)
	if !caption {
		minH = 360
	}
	card := ui.Card{Items: []ui.Node{ui.NotesEditor{ID: "notes:" + id, Ed: ns.ed, Placeholder: notesPlaceholder,
		Focus: focus, Toolbar: true, OnPin: func() { a.pinNotes(s) }, MinH: minH, OnChange: func() { a.notesChanged(id) }}}}
	if !caption {
		return []ui.Node{card}
	}
	return []ui.Node{ui.CaptionRow{Text: "Notes", Right: right}, card}
}

// copyNotes copies all of recording id's notes: Markdown (with HTML for
// apps that paste formatting) or plain text.
func (a *App) copyNotes(id string, markdown bool) {
	ns := a.notes[id]
	if ns == nil {
		return
	}
	var err error
	if markdown {
		err = ui.SetClipboardRich(a.tray.Hwnd(), strings.TrimSuffix(ns.ed.Doc.Markdown(), "\n"), ns.ed.Doc.HTML())
	} else {
		err = ui.SetClipboard(a.tray.Hwnd(), strings.TrimSuffix(ns.ed.Doc.Plain(), "\n"))
	}
	if err != nil {
		logx.Printf("copying notes: %v", err)
		return
	}
	key := "notes:text:" + id
	if markdown {
		key = "notes:md:" + id
	}
	a.copied, a.copiedAt = key, time.Now()
	a.invalidate()
	time.AfterFunc(copiedFor, func() { a.tray.Post(a.invalidate) })
}
