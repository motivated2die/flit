package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/motivated2die/flit/internal/logx"
	"github.com/motivated2die/flit/internal/sysinfo"
	"github.com/motivated2die/flit/internal/ui"
	"github.com/motivated2die/flit/internal/update"
)

// Updates (docs/decisions/0010-updates-from-github-releases.md): shortly
// after start and then twice a day, flit asks GitHub for the latest full
// release. A newer one adds "Update" to the sidebar, with a dot; its page
// downloads the exe, checks it against the release's SHA256SUMS.txt and
// starts it. That copy installs itself like any downloaded flit.exe
// (install.Ensure): it asks this instance to quit, which finishes a
// recording and stops the engine, replaces the installed exe and starts
// it with the "flit was updated" notice. Transcriptions that were cut off
// are picked up again by the new version (session.Recover).

const (
	pageUpdate = "update"

	updateFirstCheck = 30 * time.Second
	updateInterval   = 12 * time.Hour

	// updateAPIEnv points the check at another API base for testing
	// (go run ./cmd/flitdev release-server): https, or http to this PC.
	// It also turns the automatic check on in a portable build.
	updateAPIEnv = "FLIT_UPDATE_API"
)

type updateState int

const (
	updateIdle        updateState = iota
	updateChecking                // asking GitHub
	updateDownloading             // fetching and checking the exe
	updateReady                   // downloaded; waits for the recording to end
	updateStarting                // the new exe runs and replaces this one
)

type updater struct {
	client      update.Client
	auto        bool // checks run by themselves (not in portable builds)
	state       updateState
	latest      *update.Release // a newer release than this one; nil if none
	checked     time.Time       // the last successful check
	err         string          // the last failed check or update
	done, total int64           // download progress
	exe         string          // the downloaded, checked exe (updateReady)
	cancel      context.CancelFunc
}

func (a *App) updateDir() string { return filepath.Join(a.dataDir, "update") }

// startUpdateChecks runs the periodic check for the app's lifetime.
func (a *App) startUpdateChecks() {
	u := &a.update
	u.client = update.Client{UserAgent: "flit/" + Version}
	u.auto = !a.portable
	if api := os.Getenv(updateAPIEnv); api != "" {
		if err := update.CheckAPI(api); err != nil {
			logx.Printf("update: ignoring %s: %v", updateAPIEnv, err)
		} else {
			u.client.API, u.auto = api, true
			logx.Printf("update: using the test API %s", api)
		}
	}
	if !u.auto {
		logx.Printf("update: no automatic checks in a portable build")
	}
	go func() {
		time.Sleep(updateFirstCheck)
		// What an earlier update downloaded; its installer is long gone.
		a.tray.Post(func() {
			if a.update.state == updateIdle {
				_ = os.RemoveAll(a.updateDir())
			}
		})
		for {
			a.tray.Post(func() {
				if a.update.auto && !a.cfg.NoUpdateCheck {
					a.checkForUpdate()
				}
			})
			time.Sleep(updateInterval)
		}
	}()
}

// checkForUpdate asks GitHub for the latest release on its own goroutine.
func (a *App) checkForUpdate() {
	u := &a.update
	if u.state != updateIdle {
		return
	}
	u.state = updateChecking
	a.invalidate()
	client := u.client
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		rel, err := client.Latest(ctx)
		a.tray.Post(func() { a.checked(rel, err) })
	}()
}

// checked takes a check's answer on the UI thread.
func (a *App) checked(rel update.Release, err error) {
	u := &a.update
	if u.state == updateChecking {
		u.state = updateIdle
	}
	switch {
	case errors.Is(err, update.ErrNoRelease):
		logx.Printf("update: no published release (%v)", err)
		u.latest, u.err, u.checked = nil, "", time.Now()
	case err != nil:
		logx.Printf("update: check failed: %v", err)
		u.err = "Could not check for updates: " + err.Error()
	case update.Newer(rel.Version, Version):
		if u.latest == nil || u.latest.Version != rel.Version {
			logx.Printf("update: v%s is available (running v%s)", rel.Version, Version)
		}
		u.latest, u.err, u.checked = &rel, "", time.Now()
	default:
		logx.Printf("update: up to date (latest release v%s, running v%s)", rel.Version, Version)
		u.latest, u.err, u.checked = nil, "", time.Now()
	}
	a.invalidate()
}

// installUpdate downloads the newer release; startUpdate runs it.
func (a *App) installUpdate() {
	u := &a.update
	if u.state != updateIdle || u.latest == nil || a.portable {
		return
	}
	rel := *u.latest
	ctx, cancel := context.WithCancel(context.Background())
	u.state, u.done, u.total, u.err, u.cancel = updateDownloading, 0, 0, "", cancel
	a.invalidate()
	logx.Printf("update: downloading v%s", rel.Version)
	client, dir := u.client, a.updateDir()
	go func() {
		defer cancel()
		var last time.Time
		exe, err := client.Download(ctx, rel, dir, func(done, total int64) {
			if done < total && time.Since(last) < 150*time.Millisecond {
				return
			}
			last = time.Now()
			a.tray.Post(func() {
				if a.update.state == updateDownloading {
					a.update.done, a.update.total = done, total
					a.invalidate()
				}
			})
		})
		cancelled := err != nil && ctx.Err() != nil // read now: the deferred cancel runs first
		a.tray.Post(func() {
			u.cancel = nil
			switch {
			case cancelled:
				logx.Printf("update: cancelled")
				u.state = updateIdle
			case err != nil:
				logx.Printf("update: %v", err)
				u.state, u.err = updateIdle, "The update failed: "+err.Error()
			default:
				logx.Printf("update: v%s downloaded and checked", rel.Version)
				u.state, u.exe = updateReady, exe
				a.startUpdate()
			}
			a.invalidate()
		})
	}()
}

// startUpdate runs the downloaded exe unless a recording is on; then it
// waits on the Update page for the recording to end.
func (a *App) startUpdate() {
	u := &a.update
	if u.state != updateReady || a.rec != nil {
		return
	}
	logx.Printf("update: starting %s", u.exe)
	// The new copy asks this instance to quit (OnQuitRequest), which
	// saves everything, then installs itself and starts.
	if err := exec.Command(u.exe).Start(); err != nil {
		logx.Printf("update: %v", err)
		u.state, u.err = updateIdle, "The new version could not be started: "+err.Error()
		a.invalidate()
		return
	}
	u.state = updateStarting
	a.invalidate()
	go func() {
		time.Sleep(time.Minute)
		a.tray.Post(func() {
			if a.update.state == updateStarting {
				logx.Printf("update: the new version did not take over within a minute")
				a.update.state, a.update.err = updateIdle, "The new version did not start. The log (Settings → Log) says more."
				a.invalidate()
			}
		})
	}()
}

func (a *App) cancelUpdate() {
	if a.update.cancel != nil {
		a.update.cancel()
	}
}

// updateNav is the sidebar entry, there only while an update waits.
func (a *App) updateNav() (ui.NavEntry, bool) {
	if a.update.latest == nil {
		return ui.NavEntry{}, false
	}
	return ui.NavEntry{ID: pageUpdate, Label: "Update", Glyph: ui.GlyphUpdate, Dot: true}, true
}

// updatePage offers the newer version.
func (a *App) updatePage() ui.Node {
	u := &a.update
	items := []ui.Node{ui.Title("Update")}
	if u.latest == nil {
		// Reached while the entry was there, and since then up to date.
		items = append(items, ui.Card{Items: []ui.Node{ui.Para{Text: "flit " + Version + " is up to date.", Muted: true}}})
		return ui.VStack{Gap: 12, Items: items}
	}
	rel := *u.latest
	items = append(items, ui.Para{Text: "flit " + rel.Version + " is available; this is " + Version + ". Updating restarts flit; your recordings, transcripts, models and settings stay as they are.", Muted: true})

	card := ui.ProgressCard{ID: "update:card", Title: "flit " + rel.Version}
	var actions []ui.Node
	switch u.state {
	case updateDownloading:
		if u.total > 0 {
			card.Value = float64(u.done) / float64(u.total)
			card.Trailing = fmt.Sprintf("%d%%", int(card.Value*100))
			card.Detail = fmt.Sprintf("Downloading · %s of %s", sysinfo.Bytes(uint64(u.done)), sysinfo.Bytes(uint64(u.total)))
		} else {
			card.Trailing, card.Detail = "…", "Downloading"
		}
		actions = append(actions, ui.Button{ID: "update:cancel", Label: "Cancel", Large: true, OnClick: a.cancelUpdate})
	case updateReady:
		card.Value, card.Trailing, card.Detail = 1, "Ready", "Downloaded and checked · installs when the recording ends"
		actions = append(actions, ui.Button{ID: "update:start", Label: "Restart and update", Glyph: ui.GlyphRefresh, Primary: true, Large: true, Disabled: a.rec != nil, OnClick: a.startUpdate})
	case updateStarting:
		card.Value, card.Trailing, card.Detail = 1, "Installing", "flit restarts in a moment"
	default:
		card.Trailing, card.Detail = "New", "Downloaded from GitHub and checked against its published checksum"
		if !a.portable {
			actions = append(actions, ui.Button{ID: "update:now", Label: "Update now", Glyph: ui.GlyphUpdate, Primary: true, Large: true, OnClick: a.installUpdate})
		}
	}
	actions = append(actions, ui.Button{ID: "update:notes", Label: "What's new", Glyph: ui.GlyphOpenLink, Large: true, OnClick: func() { openURL(rel.Page) }})
	items = append(items, card, ui.HStack{Gap: 8, Items: actions})

	switch {
	case a.portable:
		items = append(items, ui.Para{Text: "This is a development build: it doesn't update itself. Install a release build to update.", Muted: true, Small: true})
	case a.rec != nil && u.state != updateStarting:
		items = append(items, ui.Para{Text: "A meeting is being recorded. flit updates after the recording, so nothing is cut off.", Muted: true, Small: true})
	case a.working != nil && u.state != updateStarting:
		items = append(items, ui.Para{Text: "A transcription is running. It goes on from where it was after the update.", Muted: true, Small: true})
	}
	if u.err != "" {
		items = append(items, ui.Card{Gap: 6, Items: []ui.Node{
			ui.Label{Text: "Something went wrong", Strong: true},
			ui.Para{Text: u.err, Muted: true},
		}})
	}
	return ui.VStack{Gap: 12, Items: items}
}

// updateSettings are the Updates rows of the Settings page's App card.
func (a *App) updateSettings() []ui.Node {
	u := &a.update
	line := "Asks GitHub twice a day whether a newer flit is published"
	switch {
	case u.state == updateChecking:
		line = "Checking…"
	case u.latest != nil:
		line = "flit " + u.latest.Version + " is available (Update, in the sidebar)"
	case u.err != "":
		line = u.err
	case !u.checked.IsZero():
		line = "Up to date · checked " + u.checked.Format("15:04")
	}
	return []ui.Node{
		ui.Toggle{ID: "set:updates", Label: "Check for updates", Detail: line, On: !a.cfg.NoUpdateCheck, OnChange: func(on bool) {
			a.cfg.NoUpdateCheck = !on
			a.save()
			if on {
				a.checkForUpdate()
			}
			a.invalidate()
		}},
		ui.HStack{Items: []ui.Node{ui.Link{ID: "set:checknow", Glyph: ui.GlyphRefresh, Label: "Check now", OnClick: a.checkForUpdate}}},
	}
}
