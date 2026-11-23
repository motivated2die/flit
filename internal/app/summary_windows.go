package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/motivated2die/flit/internal/download"
	"github.com/motivated2die/flit/internal/llm"
	"github.com/motivated2die/flit/internal/logx"
	"github.com/motivated2die/flit/internal/models"
	"github.com/motivated2die/flit/internal/notes"
	"github.com/motivated2die/flit/internal/session"
	"github.com/motivated2die/flit/internal/summary"
	"github.com/motivated2die/flit/internal/ui"
)

// Summaries (docs/architecture/summaries.md): written on request for a
// finished recording, by a small model in llama-server, never during a
// recording. One at a time; the runtime and the model download the first
// time. The summary opens in the notes editor and is saved as summary.md.

// summaryRun is the summary being written. Its fields change on the UI
// thread only (the worker posts its updates).
type summaryRun struct {
	id          string
	title       string // the recording's, for Home's card
	step        string // "download", "load", "ask", "read", "write"
	done, total int    // download: bytes; ask, read: which of how many
	fraction    float64
	cancel      context.CancelFunc
	started     time.Time
	model       models.Model
	// The server, once it runs: for the core and GPU squares and the
	// Debug page.
	pid     int
	device  llm.Device
	threads int
}

// text says what a step is doing.
func (r *summaryRun) text() string {
	switch r.step {
	case "download":
		if r.total > 0 {
			return fmt.Sprintf("Downloading %s, once: %s of %s", r.model.Name, mbText(int64(r.done)), mbText(int64(r.total)))
		}
		return "Downloading " + r.model.Name + ", once"
	case "load":
		return "Loading " + r.model.Name
	case "ask":
		if r.total > 1 {
			return fmt.Sprintf("Answering your questions: %d of %d", r.done+1, r.total)
		}
		return "Answering your question"
	case "read":
		return fmt.Sprintf("Reading the transcript: part %d of %d", r.done+1, r.total)
	case "write":
		return "Writing the summary"
	}
	return "Starting"
}

// value is the run's share done for its card: the download's while the
// model comes down, then the summary's.
func (r *summaryRun) value() float64 {
	if r.step == "download" {
		return float64(r.done) / float64(max(1, r.total))
	}
	return r.fraction
}

// where says where the model runs, for the card.
func (r *summaryRun) where() string {
	switch {
	case r.pid == 0:
		return r.model.Name
	case r.device == llm.CPU:
		return r.model.Name + " on the processor"
	}
	return r.model.Name + " on the graphics processor"
}

// summarizing reports whether a summary's server is running.
func (a *App) summarizing() bool { return a.sumRun != nil && a.sumRun.pid != 0 }

func mbText(n int64) string {
	if n >= 1e9 {
		return fmt.Sprintf("%.1f GB", float64(n)/1e9)
	}
	return fmt.Sprintf("%.0f MB", float64(n)/1e6)
}

// summaryModel is the model summaries use on this PC: the lighter one
// unless there is memory to spare (docs/research/summary-experiments.md).
func summaryModel() models.Model {
	id := "qwen3.5-2b"
	if llm.TotalMemory() >= 15<<30 {
		id = "qwen3.5-4b"
	}
	m, _ := models.ByID(id)
	return m
}

func llmDir(dataDir string) string { return filepath.Join(dataDir, "llm", llm.RuntimeVersion) }

var llmFetch sync.Mutex

// ensureLLM downloads the runtime and the model unless they are there.
func (a *App) ensureLLM(ctx context.Context, m models.Model, progress func(done, total int64)) (exe, model string, err error) {
	llmFetch.Lock()
	defer llmFetch.Unlock()
	dir := llmDir(a.dataDir)
	exe = filepath.Join(dir, llm.ServerExe)
	if _, err := os.Stat(exe); err != nil {
		zip := filepath.Join(a.dataDir, "llm", fmt.Sprintf("llama-%s.zip", llm.RuntimeVersion))
		logx.Printf("downloading the summary runtime (llama.cpp %s, %d MB)", llm.RuntimeVersion, llm.RuntimeSize/1000000)
		if err := (download.Client{UserAgent: "flit"}).Get(ctx, download.File{URL: llm.RuntimeURL, Dest: zip, SHA256: llm.RuntimeSHA256, Size: llm.RuntimeSize}, nil); err != nil {
			return "", "", fmt.Errorf("downloading the summary runtime: %w", err)
		}
		if _, err := llm.ExtractRuntime(zip, dir); err != nil {
			return "", "", err
		}
		os.Remove(zip)
	}
	f := download.File{URL: m.URL(), Dest: m.Path(modelsDir(a.dataDir)), SHA256: m.SHA256, Size: m.Size}
	if st, err := os.Stat(f.Dest); err != nil || st.Size() != m.Size {
		logx.Printf("downloading %s (%d MB)", m.ID, m.Size/1e6)
		if err := (download.Client{UserAgent: "flit"}).Get(ctx, f, progress); err != nil {
			return "", "", fmt.Errorf("downloading %s: %w", m.Name, err)
		}
	}
	return exe, f.Dest, nil
}

// writeSummary starts writing s's summary, with the user's instruction.
func (a *App) writeSummary(s *session.Session) {
	if a.sumRun != nil || s.Meta.State != session.Ready || a.summaryWait() != "" {
		return
	}
	a.saveAllNotes()
	// One model in memory at a time: the speech worker, idle (nothing is
	// being transcribed), goes first.
	if a.jobs != nil {
		a.jobs.releaseEngine()
	}
	id := s.Meta.ID
	ctx, cancel := context.WithCancel(context.Background())
	m := summaryModel()
	run := &summaryRun{id: id, title: sessionTitle(s), cancel: cancel, started: time.Now(), model: m}
	a.sumRun = run
	delete(a.sumErr, id)
	instruction := strings.TrimSpace(a.sumInstr[id])
	limit := a.finalLimit()
	// The worker's updates, applied on the UI thread while the run is
	// still the current one; the window repaints at most every 100 ms.
	var last time.Time
	post := func(update func(r *summaryRun), now bool) {
		if !now && time.Since(last) < 100*time.Millisecond {
			return
		}
		last = time.Now()
		a.tray.Post(func() {
			if a.sumRun == run {
				update(run)
				a.invalidate()
			}
		})
	}
	a.invalidate()
	go func() {
		md, info, err := a.runSummary(ctx, s, m, instruction, limit, post)
		a.tray.Post(func() { a.summaryEnded(s, run, md, info, err) })
	}()
}

// summarySingleMax is the longest meeting (tokens, about) read in one pass;
// longer ones go in sections. About 50 minutes of talk.
const summarySingleMax = 12000

// summaryWait is why a summary can't start now ("" when it can): never
// while a meeting records (the PC belongs to the call), nor while a
// transcription runs (one model at a time).
func (a *App) summaryWait() string {
	switch {
	case a.rec != nil:
		return "Summaries wait until the recording ends."
	case a.working != nil || a.finding != nil:
		return "Summaries wait until the transcription is done."
	}
	return ""
}

// runSummary does the work, off the UI thread.
func (a *App) runSummary(ctx context.Context, s *session.Session, m models.Model, instruction string, limit int, post func(func(*summaryRun), bool)) (string, session.SummaryInfo, error) {
	var info session.SummaryInfo
	exe, model, err := a.ensureLLM(ctx, m, func(done, total int64) {
		post(func(r *summaryRun) { r.step, r.done, r.total = "download", int(done), int(total) }, false)
	})
	if err != nil {
		return "", info, err
	}
	segs, err := s.Segments()
	if err != nil {
		return "", info, err
	}
	doc, err := s.Notes()
	if err != nil {
		return "", info, err
	}
	post(func(r *summaryRun) { r.step = "load" }, true)
	o := llm.Options{Exe: exe, Model: model, Context: 8192, Device: llm.GPU, CPULimit: limit, Background: limit > 0, Logf: logx.Printf}
	if limit > 0 {
		o.Threads = capThreads(limit)
	}
	if a.cfg.SummaryCPU {
		o.Device = llm.CPU
	}
	in := summary.Input{Title: s.Meta.Title, Segments: segs, Names: s.Names(), Notes: doc, Instruction: instruction, Language: s.Meta.Language}
	if parts := a.cfg.NameParts(); len(parts) > 0 {
		in.Me, in.MeNames = a.cfg.MeName(), parts
	}
	approach := summary.Single
	if n := summary.EstimateTokens(in); n > summarySingleMax {
		approach = summary.Extract // a long meeting: sections in an 8k context
	} else {
		// The whole meeting, the notes, the instructions and the answer.
		o.Context = max(8192, (n*12/10+3000+1023)/1024*1024)
	}
	srv, err, gpuErr := llm.StartFallback(ctx, o)
	if gpuErr != nil {
		logx.Printf("summary: the graphics processor failed (%v); on the CPU", gpuErr)
	}
	if err != nil {
		return "", info, err
	}
	defer srv.Close()
	pid, device, threads := srv.PID(), srv.Options.Device, o.Threads
	post(func(r *summaryRun) { r.pid, r.device, r.threads = pid, device, threads }, true)
	start := time.Now()
	step := ""
	r, err := summary.Run(ctx, &srv.Client, in, summary.Options{Approach: approach, Progress: func(p summary.Progress) {
		changed := p.Step != step
		step = p.Step
		post(func(r *summaryRun) { r.step, r.done, r.total, r.fraction = p.Step, p.Done, p.Total, p.Fraction }, changed || p.Fraction >= 1)
	}})
	t := r.Total()
	logx.Printf("summary of %s: %s, %s on %s, %d tokens read in %.0f s, %d written in %.0f s, %d sections, %d questions, %d dropped: %v",
		s.Meta.ID, m.ID, approach, srv.Options.Device, t.PromptN, t.PromptMS/1000, t.PredictedN, t.PredictedMS/1000, len(r.Sections), len(r.Answers), r.Dropped, err)
	if err != nil {
		return "", info, err
	}
	info = session.SummaryInfo{Written: time.Now(), Model: m.ID, Runtime: llm.RuntimeVersion, Device: srv.Options.Device.String(), Approach: string(approach),
		Language: r.Language, Instruction: instruction, Seconds: time.Since(start).Seconds(), Read: t.PromptN, Wrote: t.PredictedN}
	return r.Markdown, info, nil
}

// summaryEnded runs on the UI thread.
func (a *App) summaryEnded(s *session.Session, run *summaryRun, md string, info session.SummaryInfo, err error) {
	if a.sumRun == run {
		a.sumRun = nil
	}
	id := s.Meta.ID
	if err != nil {
		if errors.Is(err, context.Canceled) {
			a.invalidate()
			return
		}
		if a.sumErr == nil {
			a.sumErr = map[string]string{}
		}
		a.sumErr[id] = err.Error()
		logx.Printf("summary of %s failed: %v", id, err)
		a.invalidate()
		return
	}
	if err := s.SaveSummary(md+"\n", info); err != nil {
		logx.Printf("saving the summary of %s: %v", id, err)
	}
	delete(a.summaries, id) // read again from disk
	a.invalidate()
}

// cancelSummary stops the summary being written.
func (a *App) cancelSummary() {
	if a.sumRun != nil {
		a.sumRun.cancel()
		a.sumRun = nil
		a.invalidate()
	}
}

// yieldSummary stops a summary being written when a recording starts: the
// PC belongs to the call (nothing heavy during a recording), and the
// summary's page says to write it again afterwards.
func (a *App) yieldSummary() {
	run := a.sumRun
	if run == nil {
		return
	}
	a.cancelSummary()
	if a.sumErr == nil {
		a.sumErr = map[string]string{}
	}
	a.sumErr[run.id] = "stopped for the recording that started. Write it again when the meeting is over."
	logx.Printf("summary of %s stopped for a recording", run.id)
}

// summaryFor is the editor of s's summary (nil when there is none).
func (a *App) summaryFor(s *session.Session) *notesState {
	if a.summaries == nil {
		a.summaries = map[string]*notesState{}
	}
	if ns, ok := a.summaries[s.Meta.ID]; ok {
		return ns
	}
	md, _, err := s.Summary()
	if err != nil {
		logx.Printf("reading the summary of %s: %v", s.Meta.ID, err)
	}
	if md == "" {
		a.summaries[s.Meta.ID] = nil
		return nil
	}
	ed := notes.NewEditor(notes.Parse(md))
	ed.Clock = func() time.Duration { return notes.Unknown }
	ns := &notesState{ed: ed, dir: s.Dir, saved: ed.Rev}
	a.summaries[s.Meta.ID] = ns
	return ns
}

// summaryChanged saves an edited summary a moment after the last change.
func (a *App) summaryChanged(id string) {
	ns := a.summaries[id]
	if ns == nil {
		return
	}
	if ns.timer != nil {
		ns.timer.Stop()
	}
	ns.timer = time.AfterFunc(notesSaveAfter, func() {
		a.tray.Post(func() {
			if ns.ed.Rev == ns.saved {
				return
			}
			s := &session.Session{Dir: ns.dir}
			_, info, _ := s.Summary()
			info.Edited = true
			if err := s.SaveSummary(ns.ed.Doc.Markdown(), info); err != nil {
				logx.Printf("saving the summary of %s: %v", id, err)
				return
			}
			ns.saved = ns.ed.Rev
		})
	})
}

// summarySection is a finished recording's summary on its page: a button
// to write it (with a field for an extra instruction), its progress, or
// the summary in the editor with copy links and "Write again".
func (a *App) summarySection(s *session.Session) []ui.Node {
	id := s.Meta.ID
	run := a.sumRun
	busy := run != nil && run.id == id
	ns := a.summaryFor(s)
	var right ui.Node
	if ns != nil && !busy {
		label := "Copy"
		if a.copied == "summary:md:"+id && time.Since(a.copiedAt) < copiedFor {
			label = "Copied"
		}
		right = ui.HStack{Gap: 14, Right: true, Items: []ui.Node{
			ui.Link{ID: "summary:md:" + id, Glyph: ui.GlyphCopy, Label: label, OnClick: func() { a.copySummary(id) }},
		}}
	}
	if busy {
		right = ui.HStack{Right: true, Items: []ui.Node{ui.Link{ID: "summary:cancel", Label: "Cancel", OnClick: a.cancelSummary}}}
	}
	out := []ui.Node{ui.CaptionRow{Text: "Summary", Right: right}}
	if busy {
		return append(out, a.summaryCard(run, "summary:progress", "Writing the summary"))
	}
	var items []ui.Node
	if ns != nil {
		items = append(items, ui.NotesEditor{ID: "summary:" + id, Ed: ns.ed, MinH: 80, Toolbar: true, ToolbarOnFocus: true, OnChange: func() { a.summaryChanged(id) }})
	} else {
		m := summaryModel()
		text := "A small language model writes a summary from the transcript and your notes, on this PC, and answers your @flit questions."
		if _, err := os.Stat(m.Path(modelsDir(a.dataDir))); err != nil {
			text += fmt.Sprintf(" The first time, it downloads %s.", mbText(m.Size+llm.RuntimeSize))
		}
		items = append(items, ui.Para{Text: text, Muted: true})
	}
	if msg := a.sumErr[id]; msg != "" {
		items = append(items, ui.Para{Text: "The summary failed: " + msg, Muted: true})
	}
	label := "Write summary"
	if ns != nil {
		label = "Write again"
	}
	wait := a.summaryWait()
	if wait != "" {
		items = append(items, ui.Para{Text: wait, Muted: true})
	}
	other := run != nil || wait != "" // another recording's summary is being written, or it waits
	items = append(items, ui.HStack{Gap: 8, Items: []ui.Node{
		ui.VStack{Items: []ui.Node{ui.TextField{ID: "summary:instr:" + id, Value: a.sumInstr[id], MaxLen: 300,
			Placeholder: "Extra instruction (optional), e.g. “focus on the budget”",
			OnChange:    func(t string) { a.setSumInstr(id, t) }, OnCommit: func(t string) { a.setSumInstr(id, t) }}}},
		ui.Button{ID: "summary:write", Label: label, Glyph: ui.GlyphNotes, Primary: ns == nil, Disabled: other, OnClick: func() { a.writeSummary(s) }},
	}})
	return append(out, ui.Card{Gap: 10, Items: items})
}

// summaryCard is a running summary on the card transcription uses: its
// background fills up as it goes; the step and where the model runs under
// the title, the share done on the right.
func (a *App) summaryCard(run *summaryRun, id, title string) ui.Node {
	detail := run.text() + " · " + run.where()
	if limit := a.finalLimit(); limit > 0 && run.pid != 0 && run.device == llm.CPU {
		detail += fmt.Sprintf(" · at most %d%% of the processor", limit)
	}
	trailing := fmt.Sprintf("%d%%", int(run.value()*100))
	if run.step == "load" || run.step == "" {
		trailing = ""
	}
	return ui.ProgressCard{ID: id, Title: title, Detail: detail, Trailing: trailing, Value: run.value()}
}

func (a *App) setSumInstr(id, t string) {
	if a.sumInstr == nil {
		a.sumInstr = map[string]string{}
	}
	a.sumInstr[id] = t
}

// copySummary copies the summary as Markdown (with HTML for apps that
// paste formatting).
func (a *App) copySummary(id string) {
	ns := a.summaries[id]
	if ns == nil {
		return
	}
	if err := ui.SetClipboardRich(a.tray.Hwnd(), strings.TrimSuffix(ns.ed.Doc.Markdown(), "\n"), ns.ed.Doc.HTML()); err != nil {
		logx.Printf("copying the summary: %v", err)
		return
	}
	a.copied, a.copiedAt = "summary:md:"+id, time.Now()
	a.invalidate()
	time.AfterFunc(copiedFor, func() { a.tray.Post(a.invalidate) })
}

// vocabulary is the speech model's custom terms: the user's own, and
// their first and last name separately.
func (a *App) vocabulary() []string {
	v := append([]string(nil), a.cfg.Vocabulary...)
	for _, w := range a.cfg.NameParts() {
		if !slices.Contains(v, w) {
			v = append(v, w)
		}
	}
	return v
}

func (a *App) setName(first, last string) {
	a.cfg.FirstName = strings.Join(strings.Fields(first), " ")
	a.cfg.LastName = strings.Join(strings.Fields(last), " ")
	a.cfg.YourName = ""
	a.save()
	a.invalidate()
}

// nameFields renders separate First name and Last name inputs linked by Tab.
func (a *App) nameFields(prefix string) ui.Node {
	firstID, lastID := prefix+":first", prefix+":last"
	return ui.HStack{Gap: 8, Right: true, Items: []ui.Node{
		ui.TextField{ID: firstID, Value: a.cfg.First(), Placeholder: "First name", W: 118, MaxLen: 40, TabNext: lastID,
			OnCommit: func(t string) { a.setName(t, a.cfg.Last()) }},
		ui.TextField{ID: lastID, Value: a.cfg.Last(), Placeholder: "Last name", W: 118, MaxLen: 40, TabPrev: firstID,
			OnCommit: func(t string) { a.setName(a.cfg.First(), t) }},
	}}
}

// summarySettings are Settings → Summaries.
func (a *App) summarySettings() []ui.Node {
	m := summaryModel()
	detail := fmt.Sprintf("%s, %s, chosen for this PC's memory", m.Name, mbText(m.Size))
	if _, err := os.Stat(m.Path(modelsDir(a.dataDir))); err != nil {
		detail += "; downloads when you first ask for a summary"
	}
	return []ui.Node{
		ui.SettingRow{Label: "Your name", Detail: "Added separately to transcription vocabulary and summaries",
			Control: a.nameFields("set:name")},
		ui.Divider{},
		ui.SettingRow{Label: "Summary model", Detail: detail},
		ui.Divider{},
		ui.Toggle{ID: "set:sumgpu", Label: "Use the graphics processor", Detail: "Faster where it works; flit falls back to the processor when it doesn't", On: !a.cfg.SummaryCPU,
			OnChange: func(on bool) { a.cfg.SummaryCPU = !on; a.save(); a.invalidate() }},
	}
}
