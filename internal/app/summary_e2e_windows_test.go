package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/motivated2die/flit/internal/llm"
	"github.com/motivated2die/flit/internal/session"
)

// TestSummaryEndToEnd runs the app's own summary path against the real
// llama-server and model, when FLIT_LLM_E2E=1 and `flitdev llm` has put
// them in the checkout (.cache/llama-…, models/). Nothing downloads: the
// runtime and the model are linked into the test's data folder.
func TestSummaryEndToEnd(t *testing.T) {
	if os.Getenv("FLIT_LLM_E2E") != "1" {
		t.Skip("set FLIT_LLM_E2E=1 (needs `go run ./cmd/flitdev llm`)")
	}
	root, _ := filepath.Abs(filepath.Join("..", ".."))
	a := testApp(t)
	m := summaryModel()
	src := m.Path(filepath.Join(root, "models"))
	if _, err := os.Stat(src); err != nil {
		t.Skip("no model:", err)
	}
	dst := m.Path(modelsDir(a.dataDir))
	os.MkdirAll(filepath.Dir(dst), 0o755)
	if err := os.Link(src, dst); err != nil {
		t.Fatal(err)
	}
	rt := filepath.Join(root, ".cache", "llama-"+llm.RuntimeVersion)
	files, _ := os.ReadDir(rt)
	os.MkdirAll(llmDir(a.dataDir), 0o755)
	for _, f := range files {
		if err := os.Link(filepath.Join(rt, f.Name()), filepath.Join(llmDir(a.dataDir), f.Name())); err != nil {
			t.Fatal(err)
		}
	}
	var s *session.Session
	for _, x := range a.sessions {
		if x.Meta.Title == "Weekly sync" {
			s = x
		}
	}
	a.cfg.YourName = "Moritz Haisch"
	var steps []string
	run := &summaryRun{model: m}
	last := -1.0
	md, info, err := a.runSummary(context.Background(), s, m, "Bitte sehr kurz.", 0, func(update func(*summaryRun), _ bool) {
		update(run)
		if len(steps) == 0 || steps[len(steps)-1] != run.step {
			steps = append(steps, run.step)
		}
		if run.fraction < last {
			t.Errorf("progress went back: %.3f after %.3f", run.fraction, last)
		}
		last = run.fraction
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s on %s, %.0f s, read %d, wrote %d, steps %v\n%s", info.Model, info.Device, info.Seconds, info.Read, info.Wrote, steps, md)
	if run.pid == 0 || last != 1 {
		t.Errorf("the run's server %d, its progress ended at %.3f", run.pid, last)
	}
	if info.Language != "de" || info.Instruction != "Bitte sehr kurz." || !strings.Contains(md, "## ") {
		t.Errorf("info %+v", info)
	}
}
