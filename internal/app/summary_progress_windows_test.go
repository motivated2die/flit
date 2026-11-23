package app

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/motivated2die/flit/internal/llm"
	"github.com/motivated2die/flit/internal/session"
	"github.com/motivated2die/flit/internal/ui"
)

// While a summary's model runs: the page and Home show its progress card,
// the sidebar's debug lines name the model with its memory and CPU, the
// core squares follow its process, and a GPU square stands beside them
// when it runs on the graphics processor.
func TestSummaryProgressAndMeters(t *testing.T) {
	a := testApp(t)
	var s *session.Session
	for _, x := range a.sessions {
		if x.Meta.Title == "Weekly sync" {
			s = x
		}
	}
	id := s.Meta.ID
	m := summaryModel()
	// This test's own process stands in for llama-server.
	a.sumRun = &summaryRun{id: id, title: sessionTitle(s), model: m, step: "read", done: 1, total: 3, fraction: 0.42,
		cancel: func() {}, started: time.Now(), pid: os.Getpid(), device: llm.GPU, threads: 4}

	if !a.busy() || !a.summarizing() || !a.onGPU() {
		t.Fatal("a running summary isn't busy work")
	}
	lines := a.debugLines()
	if lines[0] != m.Name || !strings.Contains(lines[1], "CPU") {
		t.Errorf("debug lines %q", lines)
	}
	if cores := a.debugCores(); len(cores) != 4 {
		t.Errorf("%d core squares, want the server's 4 threads", len(cores))
	}
	if _, shown := a.debugGPU(); !shown {
		t.Error("no GPU square on the graphics processor")
	}
	a.dbg.cores, a.dbg.gpu, a.dbg.gpuOK = []float64{0.9, 0.8, 0.7, 0.3}, 0.65, true
	if lines := a.debugLines(); !strings.Contains(lines[1], "65% GPU") {
		t.Errorf("GPU summary debug lines %q, want 65%% GPU", lines)
	}

	card := a.summaryCard(a.sumRun, "x", "Writing the summary").(ui.ProgressCard)
	if card.Value != 0.42 || card.Trailing != "42%" || !strings.Contains(card.Detail, "part 2 of 3") || !strings.Contains(card.Detail, "graphics processor") {
		t.Errorf("card %+v", card)
	}
	a.page = pageSession + id
	c, err := ui.LayoutFrame(a.frame(), 960, 2400, 1, "")
	if err != nil {
		t.Skip("no Direct2D:", err)
	}
	if _, ok := c.HitByID("summary:cancel"); !ok {
		t.Error("no Cancel")
	}
	if _, ok := c.HitByID("summary:write"); ok {
		t.Error("Write summary while one is written")
	}
	if dir := os.Getenv("FLIT_RENDER_DIR"); dir != "" {
		save := func(name string) {
			img, err := ui.RenderFrame(a.frame(), 960, 640, 1, "", nil)
			if err != nil {
				t.Fatal(err)
			}
			f, _ := os.Create(filepath.Join(dir, name))
			png.Encode(f, img)
			f.Close()
		}
		save("window-summary-page.png")
		a.page = pageHome
		save("window-summary-home.png")
		a.sumRun.device = llm.CPU
		save("window-summary-home-cpu.png")
		a.page = pageDebug
		a.sumRun.device = llm.GPU
		save("window-summary-debug.png")
	}

	// On the processor: no GPU square.
	a.sumRun.device = llm.CPU
	if _, shown := a.debugGPU(); shown {
		t.Error("a GPU square on the processor")
	}
	// Downloading the model first: the card follows the download.
	a.sumRun.pid, a.sumRun.step, a.sumRun.done, a.sumRun.total = 0, "download", 500, 2000
	if a.summarizing() || a.debugCores() != nil {
		t.Error("meters before the server runs")
	}
	if card := a.summaryCard(a.sumRun, "x", "t").(ui.ProgressCard); card.Value != 0.25 || !strings.Contains(card.Detail, "Downloading") {
		t.Errorf("download card %+v", card)
	}
	a.cancelSummary()
	if a.busy() {
		t.Error("busy after cancel")
	}
	// A recording starting stops a summary, and its page says why.
	a.sumRun = &summaryRun{id: id, model: m, cancel: func() {}, pid: os.Getpid()}
	a.yieldSummary()
	if a.sumRun != nil || !strings.Contains(a.sumErr[id], "recording") {
		t.Errorf("not stopped for the recording: %v, %q", a.sumRun, a.sumErr[id])
	}
}
