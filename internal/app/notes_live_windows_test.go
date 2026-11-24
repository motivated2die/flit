package app

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/motivated2die/flit/internal/capture"
	"github.com/motivated2die/flit/internal/session"
	"github.com/motivated2die/flit/internal/ui"
)

// The running recording's page: back goes Home, a Transcribe button ends
// it, no player, no folder button and no Notes caption; an unnamed
// recording opens in its title, then the notes.
func TestRunningRecordingPage(t *testing.T) {
	a := testApp(t)
	var s *session.Session
	for _, x := range a.sessions {
		if x.Meta.Title == "" {
			s = x
		}
	}
	os.WriteFile(s.Path(session.ThemFile), nil, 0o644) // audio on disk, as while recording
	a.rec = &recording{sess: s, r: &capture.Recording{}, started: time.Now().Add(-5 * time.Minute)}

	a.openNotes(s)
	if a.page != pageSession+s.Meta.ID || a.focusTitle != s.Meta.ID || a.focusNotes != "" {
		t.Fatalf("page %q, title %q, notes %q", a.page, a.focusTitle, a.focusNotes)
	}
	f := a.frame()
	if f.Dock != nil {
		t.Error("a player while the meeting records")
	}
	c, err := ui.LayoutFrame(f, 960, 640, 1, "")
	if err != nil {
		t.Skip("no Direct2D:", err)
	}
	if _, ok := c.HitByID("session:folder"); ok {
		t.Error("the folder button is still there")
	}
	stop, ok := c.HitByID("session:stop")
	if !ok {
		t.Fatal("no Transcribe button")
	}
	if back, _ := c.HitByID("session:back"); back.R.R > stop.R.L || stop.R.R < 900 {
		t.Errorf("Transcribe isn't on the right: back %v, stop %v", back.R, stop.R)
	}
	if ed, ok := c.EditorTarget("notes:" + s.Meta.ID); !ok || ed.R.H() < 300 {
		t.Errorf("the notes aren't taller while recording: %v", ed.R)
	}
	if dir := os.Getenv("FLIT_RENDER_DIR"); dir != "" {
		img, err := ui.RenderFrame(a.frame(), 960, 640, 1, "", nil)
		if err == nil {
			fh, _ := os.Create(filepath.Join(dir, "window-session-recording.png"))
			png.Encode(fh, img)
			fh.Close()
		}
		a.page = pageHome
		img, err = ui.RenderFrame(a.frame(), 960, 640, 1, "", nil)
		if err == nil {
			fh, _ := os.Create(filepath.Join(dir, "window-home-recording-icons.png"))
			png.Encode(fh, img)
			fh.Close()
		}
		a.page = pageSession + s.Meta.ID
	}
	back, _ := c.HitByID("session:back")
	back.OnClick()
	if a.page != pageHome {
		t.Errorf("back went to %q", a.page)
	}
	// Named on its page (the list's copy of the session, not the
	// recording's own), it opens in the notes, and the name survives the
	// recording saving its own copy when it ends.
	a.reloadSessions()
	page := a.sessionByID(s.Meta.ID)
	if page == s {
		t.Fatal("the test needs the page's copy to differ from the recording's")
	}
	a.renameSession(page, "Design review")
	a.openNotes(s) // with the recording's own copy, as the notch does
	if a.focusNotes != s.Meta.ID || a.focusTitle != "" {
		t.Errorf("named: title %q, notes %q", a.focusTitle, a.focusNotes)
	}
	if err := a.rec.sess.Save(); err != nil {
		t.Fatal(err)
	}
	a.reloadSessions()
	if got := a.sessionByID(s.Meta.ID).Meta.Title; got != "Design review" {
		t.Errorf("the name was lost when the recording saved: %q", got)
	}
	if stop, _ := c.HitByID("session:stop"); stop.R.H() > 24 {
		t.Errorf("Transcribe is %v px high, want a chip", stop.R.H())
	}
	// Home: split pill with compact trash icon on the left and Transcribe on the right.
	a.page = pageHome
	c, _ = ui.LayoutFrame(a.frame(), 960, 640, 1, "")
	cancel, ok := c.HitByID("home:cancel")
	if !ok {
		t.Fatal("home:cancel missing on Home")
	}
	stopBtn, ok := c.HitByID("home:stop")
	if !ok {
		t.Fatal("home:stop missing on Home")
	}
	if cancel.R.W() >= stopBtn.R.W() {
		t.Errorf("cancel segment (%v) should be narrower than Transcribe (%v)", cancel.R.W(), stopBtn.R.W())
	}
	cancel.OnClick() // expands across the split pill to confirm discard
	c, _ = ui.LayoutFrame(a.frame(), 960, 640, 1, "")
	if h, _ := c.HitByID("home:cancel"); h.R.W() <= cancel.R.W()+20 {
		t.Error("Cancel doesn't expand to ask again")
	}
	if a.rec == nil {
		t.Error("one click cancelled the recording")
	}
}
