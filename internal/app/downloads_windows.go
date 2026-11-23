package app

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/motivated2die/flit/internal/engine/native"
	"github.com/motivated2die/flit/internal/models"
	"github.com/motivated2die/flit/internal/sysinfo"
	"github.com/motivated2die/flit/internal/ui"
)

// Model downloads, as the app shows them: each model the settings need on
// its own card (the welcome page; Home while they download), its
// background filling up as it comes. They download at the same time
// (jobs.models); progress arrives here on the UI thread.

// modelDownload is a model coming down.
type modelDownload struct {
	m           models.Model
	done, total int64
}

func (a *App) downloadProgress(m models.Model, done, total int64) {
	if a.downloads == nil {
		a.downloads = map[string]*modelDownload{}
	}
	d := a.downloads[m.ID]
	if d == nil {
		d = &modelDownload{m: m}
		a.downloads[m.ID] = d
	}
	d.done, d.total = done, max(total, 1)
	delete(a.downloadErr, m.ID)
	a.syncDownloads()
}

func (a *App) downloadEnded(m models.Model, err error) {
	delete(a.downloads, m.ID)
	if err != nil && !errors.Is(err, context.Canceled) {
		if a.downloadErr == nil {
			a.downloadErr = map[string]error{}
		}
		a.downloadErr[m.ID] = err
	}
	a.syncDownloads()
}

// syncDownloads sets the status the tray, the notch, the panel and the
// sidebar show: what is downloading, without a percentage (each shows the
// one number, from progress), and the share of all of it that is done.
func (a *App) syncDownloads() {
	var done, total int64
	name := ""
	for _, d := range a.downloads {
		done += d.done
		total += d.total
		name = d.m.Name
	}
	switch len(a.downloads) {
	case 0:
		a.status = ""
	case 1:
		a.status = "Downloading " + name
	default:
		a.status = fmt.Sprintf("Downloading %d models", len(a.downloads))
	}
	if total > 0 {
		a.progress = float64(done) / float64(total)
	}
	a.refresh()
}

// downloadLine is the status with its percentage, for one-line places
// (the sidebar's status, the tray's tooltip).
func (a *App) downloadLine() string {
	if len(a.downloads) == 0 {
		return a.status
	}
	return fmt.Sprintf("%s %d%%", a.status, int(a.progress*100))
}

// neededModels are the models the settings call for, in the order they
// are shown: the speech model, voice detection, and the speaker model.
func (a *App) neededModels() []models.Model {
	var out []models.Model
	m, ok := models.ByID(a.cfg.Model)
	if a.cfg.Model == "" || !ok {
		m, ok = models.Default(models.RoleASR, a.cfg.Language)
	}
	if ok {
		out = append(out, m)
	}
	if v, ok := models.Default(models.RoleVAD, ""); ok {
		out = append(out, v)
	}
	if a.cfg.Speakers {
		if d, ok := models.Default(models.RoleDiarize, ""); ok {
			out = append(out, d)
		}
	}
	if a.wantVulkan() {
		out = append(out, vulkanEngineItem)
	}
	return out
}

// modelCards are a card per needed model: downloading, ready or waiting;
// active only lists those coming down (Home).
func (a *App) modelCards(active bool) []ui.Node {
	var out []ui.Node
	for _, m := range a.neededModels() {
		role := map[models.Role]string{models.RoleASR: "Speech model", models.RoleVAD: "Voice detection", models.RoleDiarize: "Telling speakers apart"}[m.Role]
		if m.ID == vulkanEngineItem.ID {
			role = "Graphics support"
		}
		size := sysinfo.Bytes(uint64(m.Size))
		c := ui.ProgressCard{ID: "dl:" + m.ID, Title: m.Name}
		switch d, err := a.downloads[m.ID], a.downloadErr[m.ID]; {
		case d != nil:
			c.Value = float64(d.done) / float64(d.total)
			c.Trailing = fmt.Sprintf("%d%%", int(c.Value*100))
			c.Detail = fmt.Sprintf("%s · %s of %s", role, sysinfo.Bytes(uint64(d.done)), size)
		case active:
			continue
		case a.onDisk(m):
			c.Value, c.Trailing, c.Detail = 1, "Ready", role+" · "+size
		case err != nil:
			c.Trailing, c.Detail = "Failed", role+" · will try again; "+shorten(err.Error(), 60)
		default:
			c.Trailing, c.Detail = "Waiting", role+" · "+size
		}
		out = append(out, c)
	}
	return out
}

// onDisk reports whether a model (or optional runtime) is downloaded whole.
func (a *App) onDisk(m models.Model) bool {
	if m.ID == vulkanEngineItem.ID {
		return native.HasVulkan(a.dataDir)
	}
	st, err := os.Stat(m.Path(modelsDir(a.dataDir)))
	return err == nil && st.Size() == m.Size
}
