package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/motivated2die/flit/internal/install"
	"github.com/motivated2die/flit/internal/logx"
	"github.com/motivated2die/flit/internal/session"
	"github.com/motivated2die/flit/internal/sysinfo"
	"github.com/motivated2die/flit/internal/ui"
)

// Cleaning up after flit: the Settings page's danger section frees space
// (old audio, all audio, all recordings, the downloaded models) and
// uninstalls flit. Each action asks for a second click first.

// oldAudioAge is when a transcribed recording's audio counts as old.
const oldAudioAge = 30 * 24 * time.Hour

// usage is what the cleanup actions would remove, counted when the
// Settings page opens and after each cleanup (not on every repaint).
type usage struct {
	oldAudio, allAudio, recordings, models int64
	oldAudioN, allAudioN, recordingsN      int
}

func (a *App) countUsage() {
	var u usage
	now := time.Now()
	for _, s := range a.sessions {
		if a.busyWith(s) {
			continue
		}
		size := dirSize(s.Dir)
		u.recordings += size
		u.recordingsN++
		audio := audioSize(s)
		if audio == 0 || s.Meta.State != session.Ready {
			continue
		}
		u.allAudio += audio
		u.allAudioN++
		if now.Sub(s.Meta.Started) > oldAudioAge {
			u.oldAudio += audio
			u.oldAudioN++
		}
	}
	u.models = dirSize(modelsDir(a.dataDir))
	a.usage = u
}

func audioSize(s *session.Session) int64 {
	var n int64
	for _, f := range []string{session.MeFile, session.ThemFile} {
		if st, err := os.Stat(s.Path(f)); err == nil {
			n += st.Size()
		}
	}
	return n
}

// busyWith reports whether s is being recorded, transcribed or analysed.
func (a *App) busyWith(s *session.Session) bool {
	return (a.rec != nil && a.rec.sess.Dir == s.Dir) || (a.working != nil && a.working.Dir == s.Dir) || (a.finding != nil && a.finding.Dir == s.Dir)
}

// deleteAudioWhere removes the audio of transcribed recordings that match.
func (a *App) deleteAudioWhere(match func(*session.Session) bool) {
	n := 0
	for _, s := range a.sessions {
		if a.busyWith(s) || s.Meta.State != session.Ready || !match(s) || !a.hasAudio(s) {
			continue
		}
		a.stopPlayer(s)
		for _, f := range []string{session.MeFile, session.ThemFile} {
			if err := os.Remove(s.Path(f)); err != nil && !os.IsNotExist(err) {
				logx.Printf("deleting %s of %s: %v", f, s.Meta.ID, err)
			}
		}
		n++
	}
	logx.Printf("deleted the audio of %d recordings", n)
	a.afterCleanup()
}

// deleteAllRecordings removes every recording not in use.
func (a *App) deleteAllRecordings() {
	n := 0
	for _, s := range a.sessions {
		if a.busyWith(s) {
			continue
		}
		if a.removeSession(s) {
			n++
		}
	}
	a.lastReady = nil
	logx.Printf("deleted %d recordings", n)
	a.afterCleanup()
}

// deleteModels removes the downloaded models (they download again when
// needed), after stopping an idle engine that holds them open.
func (a *App) deleteModels() {
	if a.working != nil || a.finding != nil {
		return
	}
	if a.jobs != nil {
		a.jobs.releaseEngine()
	}
	if err := os.RemoveAll(modelsDir(a.dataDir)); err != nil {
		logx.Printf("deleting the models: %v", err)
	} else {
		logx.Printf("deleted the downloaded models")
	}
	a.afterCleanup()
}

func (a *App) afterCleanup() {
	a.reloadSessions()
	a.countUsage()
	a.invalidate()
}

// uninstall starts the installed flit's uninstaller with the choice made
// in the popover; it quits this flit, then removes the program (and, with
// wipe, the data folder).
func (a *App) uninstall(wipe bool) {
	args := []string{install.UninstallFlag, uninstallConfirmed}
	if wipe {
		args = append(args, uninstallWipe)
	}
	cmd := exec.Command(install.Exe(), args...)
	cmd.Dir = os.TempDir() // not in a folder it is about to delete
	if err := cmd.Start(); err != nil {
		logx.Printf("starting the uninstaller: %v", err)
		return
	}
	a.uninstallAsk = false
	a.invalidate()
}

// askUninstall opens the uninstall popover, keeping the data by default.
func (a *App) askUninstall() {
	a.uninstallAsk, a.uninstallWipe = true, false
	a.dataSize = dirSize(a.dataDir)
	a.invalidate()
}

// closePopover closes the uninstall popover (Cancel, Esc).
func (a *App) closePopover() {
	if a.uninstallAsk {
		a.uninstallAsk = false
		a.invalidate()
	}
}

// uninstallPopover asks what happens to flit's data when it goes: kept
// (the default, as from Windows' Installed apps) or deleted with it.
func (a *App) uninstallPopover() ui.Node {
	size := sysinfo.Bytes(uint64(a.dataSize))
	wipe := a.uninstallWipe
	choose := func(w bool) func() { return func() { a.uninstallWipe = w; a.invalidate() } }
	note := "They stay in " + shortenPath(a.dataDir) + ", with the downloaded speech models; installing flit again picks them up."
	action := ui.Button{ID: "uninstall:go", Label: "Uninstall", Primary: true, OnClick: func() { a.uninstall(false) }}
	if wipe {
		note = "Everything flit stored goes, and can't be brought back."
		action = ui.Button{ID: "uninstall:go", Label: "Uninstall and delete", Glyph: ui.GlyphDelete, Danger: true, OnClick: func() { a.uninstall(true) }}
	}
	return ui.VStack{Gap: 10, Items: []ui.Node{
		ui.Label{Text: "Uninstall flit", Large: true},
		ui.Para{Text: "This removes the app, its Start Menu entry, its entry in Installed apps and autostart. What should happen to what flit stored?", Muted: true},
		ui.Space(2),
		ui.ChoiceRow{ID: "uninstall:keep", Title: "Keep my recordings", Detail: "Recordings, transcripts and settings stay (" + size + ")", Selected: !wipe, Level: -1, OnClick: choose(false)},
		ui.ChoiceRow{ID: "uninstall:wipe", Title: "Delete everything", Detail: "Recordings, transcripts, speech models and settings (" + size + ")", Selected: wipe, Level: -1, OnClick: choose(true)},
		ui.Para{Text: note, Muted: true, Small: true},
		ui.Space(4),
		ui.HStack{Gap: 8, Right: true, Items: []ui.Node{
			ui.Button{ID: "uninstall:cancel", Label: "Cancel", OnClick: a.closePopover},
			action,
		}},
	}}
}

// dangerSection is the end of the Settings page.
func (a *App) dangerSection() []ui.Node {
	u := a.usage
	none := func(n int) bool { return n == 0 }
	row := func(id, label, detail, button string, empty bool, act func()) ui.Node {
		var control ui.Node = ui.Label{Text: "Nothing to delete", Muted: true, Right: true}
		if !empty {
			control = a.confirmButton(id, button, false, act)
		}
		return ui.SettingRow{Label: label, Detail: detail, Control: control}
	}
	rows := []ui.Node{
		row("clean:old", "Audio older than 30 days",
			fmt.Sprintf("%s of %s; the transcripts stay", sysinfo.Bytes(uint64(u.oldAudio)), plural(u.oldAudioN, "transcribed recording")),
			"Delete", none(u.oldAudioN), func() {
				cutoff := time.Now().Add(-oldAudioAge)
				a.deleteAudioWhere(func(s *session.Session) bool { return s.Meta.Started.Before(cutoff) })
			}),
		ui.Divider{},
		row("clean:audio", "All audio of transcribed recordings",
			fmt.Sprintf("%s of %s; the transcripts stay, but can't be listened to or redone", sysinfo.Bytes(uint64(u.allAudio)), plural(u.allAudioN, "recording")),
			"Delete", none(u.allAudioN), func() { a.deleteAudioWhere(func(*session.Session) bool { return true }) }),
		ui.Divider{},
		row("clean:all", "All recordings and transcripts",
			fmt.Sprintf("%s in %s", sysinfo.Bytes(uint64(u.recordings)), plural(u.recordingsN, "recording")),
			"Delete all", none(u.recordingsN), a.deleteAllRecordings),
		ui.Divider{},
		row("clean:models", "Downloaded speech models",
			sysinfo.Bytes(uint64(u.models))+"; downloaded again for the next transcription",
			"Delete", u.models == 0 || a.working != nil || a.finding != nil, a.deleteModels),
	}
	if install.IsInstalledCopy() {
		rows = append(rows, ui.Divider{}, ui.SettingRow{
			Label:   "Uninstall flit",
			Detail:  "Removes the app, its shortcut and autostart; asks whether your recordings stay",
			Control: ui.Button{ID: "clean:uninstall", Label: "Uninstall…", OnClick: a.askUninstall},
		})
	}
	return []ui.Node{ui.Caption("Free up space and uninstall"), ui.Card{Gap: 2, Items: rows}}
}

// removeSession deletes a recording's folder (only ever one inside the
// sessions folder); true if it's gone.
func (a *App) removeSession(s *session.Session) bool {
	root := session.Root(a.dataDir)
	rel, err := filepath.Rel(root, s.Dir)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") || strings.ContainsAny(rel, `\/`) {
		logx.Printf("not deleting %s: not a session folder", s.Dir)
		return false
	}
	a.stopPlayer(s)
	if err := os.RemoveAll(s.Dir); err != nil {
		logx.Printf("deleting %s: %v", s.Meta.ID, err)
		return false
	}
	logx.Printf("deleted %s", s.Meta.ID)
	if a.lastReady != nil && a.lastReady.Dir == s.Dir {
		a.lastReady = nil
	}
	a.forget(s)
	return true
}
