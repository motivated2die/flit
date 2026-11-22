package app

import (
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/motivated2die/flit/internal/ui"
	"github.com/motivated2die/flit/internal/update"
)

func hasUpdateNav(f ui.Frame) bool {
	for _, e := range f.Nav {
		if e.ID == pageUpdate {
			return e.Dot
		}
	}
	return false
}

func TestUpdateEntryOnlyWhenNewer(t *testing.T) {
	a := testApp(t)
	if hasUpdateNav(a.frame()) {
		t.Fatal("an Update entry before any check")
	}
	a.checked(update.Release{Version: Version}, nil) // same version: nothing
	if hasUpdateNav(a.frame()) || a.update.checked.IsZero() {
		t.Fatalf("same version: entry shown or not counted as checked (%+v)", a.update)
	}
	a.checked(update.Release{}, errors.New("offline"))
	if hasUpdateNav(a.frame()) || a.update.err == "" {
		t.Fatal("a failed check showed the entry or no error")
	}
	a.checked(update.Release{Version: "99.0.0", Page: "https://github.com/motivated2die/flit/releases/tag/v99.0.0"}, nil)
	if !hasUpdateNav(a.frame()) || a.update.err != "" {
		t.Fatal("no Update entry for a newer release")
	}
	a.checked(update.Release{}, update.ErrNoRelease) // the release was withdrawn
	if hasUpdateNav(a.frame()) {
		t.Fatal("the entry stayed after the release went")
	}
}

func TestUpdatePageStates(t *testing.T) {
	a := testApp(t)
	a.checked(update.Release{Version: "99.0.0", Page: "https://example.com/notes"}, nil)
	a.page = pageUpdate
	dir := os.Getenv("FLIT_RENDER_DIR")
	shot := func(name string) {
		t.Helper()
		h := int32(640)
		if a.page == pageSettings {
			h = 1900 // down to the App card
		}
		img, err := ui.RenderFrame(a.frame(), 960, h, 1, "", nil)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if dir != "" {
			fh, _ := os.Create(filepath.Join(dir, "window-update-"+name+".png"))
			png.Encode(fh, img)
			fh.Close()
		}
	}
	shot("available")
	a.update.state, a.update.done, a.update.total = updateDownloading, 12_000_000, 31_000_000
	shot("downloading")
	a.update.state = updateReady
	a.rec = &recording{}
	shot("waiting-for-recording")
	a.rec = nil
	a.update.state, a.update.err = updateIdle, "The update failed: flit.exe does not match its checksum"
	shot("failed")
	a.portable = true
	shot("portable")

	// Settings shows the state in the App card.
	a.page = pageSettings
	shot("settings")
	// Up to date while on the page: it says so instead of an empty page.
	a.checked(update.Release{Version: Version}, nil)
	a.page = pageUpdate
	shot("up-to-date")
}
