package app

import (
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/motivated2die/flit/internal/llm"
	"github.com/motivated2die/flit/internal/logx"
	"github.com/motivated2die/flit/internal/models"
	"github.com/motivated2die/flit/internal/sysinfo"
	"github.com/motivated2die/flit/internal/ui"
)

// The debug view: the loaded model and what it and flit use, in two lines
// at the bottom of the sidebar, and in full (with the log's latest lines)
// on the Debug page they open.

const pageDebug = "debug"

var appStarted = time.Now()

// debugState caches one sample of the figures (taken at most every second,
// on the UI thread) and keeps the window repainting while it shows them.
type debugState struct {
	at        time.Time
	engine    engineState   // the first running engine (or the batch one, not loaded)
	engines   []engineState // the running engine workers: batch, live
	loaded    bool          // a worker is running with engine's models
	engineSt  sysinfo.Stats // of all running workers together
	engineCPU float64
	engineErr error
	self      sysinfo.Stats
	selfCPU   float64
	heap      uint64
	routines  int

	engineMeters map[int]*sysinfo.CPUMeter // by worker pid
	selfMeter    sysinfo.CPUMeter
	tick         bool // a repaint is scheduled

	// The cores the workers keep busy while transcribing: how busy each
	// is (0..1, busiest first), at least threads of them (a live worker's
	// cores between its chunks); read off the UI thread.
	cores     []float64
	threads   int
	coreMeter sysinfo.CoreMeter
	reading   bool

	// A summary's language model server, while one runs: its memory and
	// CPU, and how busy it keeps the graphics processor (the meter is
	// read on the cores' goroutine, one reading at a time).
	sumSt    sysinfo.Stats
	sumCPU   float64
	sumErr   error
	sumMeter sysinfo.CPUMeter
	gpu      float64
	gpuOK    bool // a reading has come
	gpuMeter sysinfo.GPUMeter
}

// transcribing reports whether transcription puts load on the machine now:
// after a meeting, finding speakers, or as it goes during one.
func (a *App) transcribing() bool { return a.working != nil || a.finding != nil || a.live != nil }

// busy reports whether flit's heavy work runs now: transcription, or a
// summary's model (the core squares show either).
func (a *App) busy() bool { return a.transcribing() || a.summarizing() }

// transcribeOnGPU reports whether the active speech worker runs on a GPU
// (or is configured to, before its first Load info arrives).
func (a *App) transcribeOnGPU(d *debugState) bool {
	if d.loaded {
		for _, e := range d.engines {
			if e.Info.OnGPU() {
				return true
			}
		}
		return false
	}
	return a.gpuForFinal()
}

// gpuPIDs returns the process IDs of active workers running on a GPU
// (llama-server for summaries and/or speech engine workers on Vulkan while
// transcribing or finding speakers).
func (a *App) gpuPIDs(d *debugState) []uint32 {
	var pids []uint32
	if a.summarizing() && a.sumRun.device != llm.CPU {
		pids = append(pids, uint32(a.sumRun.pid))
	}
	if a.transcribing() {
		for _, e := range d.engines {
			if e.PID != 0 && e.Info.OnGPU() {
				pids = append(pids, uint32(e.PID))
			}
		}
	}
	return pids
}

// onGPU reports whether any active heavy work (transcription, diarization
// or summary) runs on the graphics processor.
func (a *App) onGPU() bool { return len(a.gpuPIDs(&a.dbg)) > 0 }

func (a *App) sampleDebug() *debugState {
	d := &a.dbg
	now := time.Now()
	if now.Sub(d.at) < time.Second {
		return d
	}
	d.at = now
	d.engine, d.engines, d.loaded = engineState{}, nil, false
	if a.jobs != nil {
		if st, ok := a.jobs.engine(); ok {
			d.engine = st
			if st.PID != 0 {
				d.engines = append(d.engines, st)
			}
		}
	}
	d.threads = 0
	if l := a.live; l != nil && l.sup != nil {
		d.threads = l.threads
		if pid := l.sup.PID(); pid != 0 {
			d.engines = append(d.engines, engineState{ASR: l.asr, VAD: l.vad, PID: pid, Info: l.sup.Info()})
		}
	}
	if limit := a.finalLimit(); limit > 0 && (a.working != nil || a.finding != nil) {
		d.threads = max(d.threads, capThreads(limit)) // a limited run's threads
	}
	if len(d.engines) > 0 {
		d.engine, d.loaded = d.engines[0], true
	}
	d.engineSt, d.engineCPU, d.engineErr = sysinfo.Stats{}, 0, nil
	meters := map[int]*sysinfo.CPUMeter{}
	var pids []uint32
	for _, e := range d.engines {
		m := d.engineMeters[e.PID]
		if m == nil {
			m = &sysinfo.CPUMeter{}
		}
		meters[e.PID] = m
		pids = append(pids, uint32(e.PID))
		st, err := sysinfo.Process(uint32(e.PID))
		if err != nil {
			d.engineErr = err
			continue
		}
		d.engineSt.Private += st.Private
		d.engineSt.WorkingSet += st.WorkingSet
		d.engineCPU += m.Percent(st.CPU, now)
	}
	d.engineMeters = meters
	d.sumSt, d.sumCPU, d.sumErr = sysinfo.Stats{}, 0, nil
	if a.summarizing() {
		run := a.sumRun
		pids = append(pids, uint32(run.pid))
		d.threads = max(d.threads, run.threads)
		if st, err := sysinfo.Process(uint32(run.pid)); err != nil {
			d.sumErr = err
		} else {
			d.sumSt, d.sumCPU = st, d.sumMeter.Percent(st.CPU, now)
		}
	} else {
		d.sumMeter = sysinfo.CPUMeter{}
	}
	a.sampleCores(pids)
	if st, err := sysinfo.Process(0); err == nil {
		d.self, d.selfCPU = st, d.selfMeter.Percent(st.CPU, now)
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	d.heap, d.routines = ms.HeapAlloc, runtime.NumGoroutine()
	return d
}

// sampleCores reads how busy the workers' threads are, on a goroutine (a
// reading lists the system's threads: a few milliseconds), only while
// transcribing; the result arrives on the UI thread.
func (a *App) sampleCores(pids []uint32) {
	d := &a.dbg
	if !a.onGPU() && !d.reading {
		d.gpu, d.gpuOK = 0, false
		d.gpuMeter.Close()
	}
	if !a.busy() || len(pids) == 0 {
		d.cores, d.coreMeter = nil, sysinfo.CoreMeter{}
		return
	}
	if d.reading || a.tray == nil {
		return
	}
	d.reading = true
	gpuPIDs := a.gpuPIDs(d)
	go func() {
		th, err := sysinfo.ReadThreads(pids...)
		at := time.Now()
		gpu, gpuErr := 0.0, error(nil)
		if len(gpuPIDs) > 0 {
			gpu, gpuErr = d.gpuMeter.Read(gpuPIDs...) // only this goroutine touches the meter while reading
		}
		a.tray.Post(func() {
			d.reading = false
			if !a.busy() {
				return
			}
			if err == nil {
				d.cores = d.coreMeter.Cores(th, at)
			}
			if len(gpuPIDs) > 0 && gpuErr == nil {
				d.gpu, d.gpuOK = gpu, true
			}
			a.invalidate()
		})
	}()
}

// debugCores are the sidebar's core squares: each engaged core's use,
// busiest first, at least the live worker's threads (empty between its
// chunks), at most 8; nil when nothing is being transcribed.
func (a *App) debugCores() []float32 {
	d := &a.dbg
	if !(a.transcribing() && d.loaded) && !a.summarizing() {
		return nil
	}
	n := min(8, max(len(d.cores), d.threads))
	if n == 0 {
		return nil
	}
	out := make([]float32, n)
	for i := range min(n, len(d.cores)) {
		out[i] = float32(d.cores[i])
	}
	return out
}

// debugGPU is the sidebar's GPU square: how busy the summary's model keeps
// the graphics processor, shown while it runs there (empty until the first
// reading).
func (a *App) debugGPU() (float32, bool) {
	if !a.onGPU() {
		return 0, false
	}
	return float32(a.dbg.gpu), true
}

// repaintSoon repaints the window after every, as long as it stays open.
func (a *App) repaintSoon(every time.Duration) {
	if a.dbg.tick || a.tray == nil {
		return
	}
	a.dbg.tick = true
	time.AfterFunc(every, func() {
		a.tray.Post(func() {
			a.dbg.tick = false
			a.invalidate()
		})
	})
}

// shownModel is the loaded model, or the one the next meeting will load.
func (a *App) shownModel(d *debugState) (models.Model, bool) {
	if d.engine.ASR.ID != "" {
		return d.engine.ASR, true
	}
	m, ok := models.ByID(a.cfg.Model)
	if a.cfg.Model == "" || !ok {
		m, ok = models.Default(models.RoleASR, a.cfg.Language)
	}
	return m, ok
}

// dominantLoad formats whichever processing unit is busier right now: the
// GPU when active work runs on one and its utilisation exceeds the CPU's,
// otherwise the CPU.
func dominantLoad(cpu float64, onGPU bool, gpu float64, gpuOK bool) string {
	if pct := gpu * 100; onGPU && gpuOK && pct >= 0.5 && pct > cpu {
		return fmt.Sprintf("%.0f%% GPU", pct)
	}
	return fmt.Sprintf("%.0f%% CPU", cpu)
}

// debugLines are the sidebar's two lines.
func (a *App) debugLines() []string {
	d := a.sampleDebug()
	every := 2 * time.Second
	if a.busy() {
		every = time.Second // the core squares follow the load
	}
	a.repaintSoon(every)
	if a.summarizing() {
		// The summary's model, while it runs: the speech worker has made
		// way for it.
		usage := "Loaded"
		if d.sumErr == nil {
			usage = fmt.Sprintf("%s · %s", sysinfo.Bytes(d.sumSt.WorkingSet), dominantLoad(d.sumCPU, a.onGPU(), d.gpu, d.gpuOK))
		}
		return []string{a.sumRun.model.Name, usage}
	}
	name := "No speech model"
	if m, ok := a.shownModel(d); ok {
		name = m.Name
	}
	usage := "Not loaded"
	switch {
	case d.loaded && d.engineErr == nil:
		usage = fmt.Sprintf("%s · %s", sysinfo.Bytes(d.engineSt.Private), dominantLoad(d.engineCPU, a.onGPU(), d.gpu, d.gpuOK))
	case d.loaded:
		usage = "Loaded"
	}
	return []string{name, usage}
}

func (a *App) debugPage() ui.Node {
	d := a.sampleDebug()
	a.repaintSoon(time.Second)
	value := func(s string) ui.Node { return ui.Label{Text: s, Muted: true, Right: true} }

	engine := []ui.Node{}
	if m, ok := a.shownModel(d); ok {
		state := "not loaded; loads for the next transcription"
		if d.loaded {
			state = fmt.Sprintf("loaded in the engine worker (pid %d)", d.engine.PID)
			if len(d.engines) > 1 {
				state = fmt.Sprintf("loaded in %d engine workers (pid %d, %d): after a meeting and during one", len(d.engines), d.engines[0].PID, d.engines[1].PID)
			} else if a.live != nil && a.live.sup != nil && a.live.sup.PID() == d.engine.PID {
				state = fmt.Sprintf("loaded in the live transcription's worker (pid %d)", d.engine.PID)
			}
			if d.engine.Info.OnGPU() {
				if desc := d.engine.Info.DeviceDesc; desc != "" {
					state += ", on " + desc
				} else {
					state += ", on the graphics processor"
				}
			} else if d.engine.Info.Backend != "" {
				state += ", on the processor"
			}
			if d.engine.Info.FallbackReason != "" {
				state += " (GPU fell back to processor)"
			}
		}
		engine = append(engine, ui.SettingRow{Label: m.Name, Detail: state, Control: value(fmt.Sprintf("%d MB on disk", m.Size/1_000_000))})
	} else {
		engine = append(engine, ui.SettingRow{Label: "No speech model for this language"})
	}
	if d.loaded {
		mem := "unavailable"
		if d.engineErr == nil {
			mem = sysinfo.Bytes(d.engineSt.Private) + " private · " + sysinfo.Bytes(d.engineSt.WorkingSet) + " in RAM"
		}
		engine = append(engine,
			ui.Divider{},
			ui.SettingRow{Label: "Memory", Control: value(mem)},
			ui.Divider{},
			ui.SettingRow{Label: "CPU", Detail: "Share of all cores", Control: value(fmt.Sprintf("%.1f%%", d.engineCPU))},
		)
		if engineOnGPU(d) {
			gpu := "0%"
			if a.transcribing() {
				gpu = "measuring"
				if d.gpuOK {
					gpu = fmt.Sprintf("%.0f%%", d.gpu*100)
				}
			}
			engine = append(engine, ui.Divider{}, ui.SettingRow{Label: "Graphics processor", Detail: "The busiest of its engines, for this worker", Control: value(gpu)})
		}
		if cores := a.debugCores(); cores != nil {
			busy := 0
			for _, c := range d.cores {
				if c >= 0.05 {
					busy++
				}
			}
			engine = append(engine, ui.Divider{}, ui.SettingRow{Label: "Cores engaged", Detail: "Threads of the worker using a core", Control: value(fmt.Sprint(busy))})
		}
		if d.engine.Diar.ID != "" {
			diarDetail := "Loaded on first use"
			if d.engine.Info.DiarBackend == "vulkan" {
				diarDetail = "On the graphics processor"
			} else if d.engine.Info.DiarBackend != "" {
				diarDetail = "On the processor"
			}
			engine = append(engine, ui.Divider{}, ui.SettingRow{Label: "Speakers", Detail: diarDetail, Control: value(d.engine.Diar.Name)})
		}
		if d.engine.VAD.ID != "" {
			engine = append(engine, ui.Divider{}, ui.SettingRow{Label: "Voice detection", Detail: "Always on the processor", Control: value(d.engine.VAD.Name)})
		}
		if v := d.engine.Info.Version; v != "" {
			engine = append(engine, ui.Divider{}, ui.SettingRow{Label: "transcribe.cpp", Control: value(v)})
		}
	}
	if a.gpu.Best.Name != "" || a.gpu.Reason != "" {
		gpuDetail := "Vulkan ready · transcription mode: " + gpuModeLabel(a.transcribeGPU())
		if !a.gpu.Ready {
			gpuDetail = "Not ready for Vulkan: " + a.gpu.Reason
		}
		engine = append(engine, ui.Divider{}, ui.SettingRow{Label: "Graphics hardware", Detail: gpuDetail, Control: value(a.gpu.Summary())})
	}
	job := "Idle"
	switch {
	case a.working != nil:
		job = fmt.Sprintf("Transcribing %s, %d%%", a.working.Meta.ID, int(a.progress*100))
	case a.rec != nil && a.live != nil:
		job = "Recording, transcribing as it goes"
	case a.rec != nil:
		job = "Recording"
	case a.status != "":
		job = a.status
	}
	engine = append(engine, ui.Divider{}, ui.SettingRow{Label: "Job", Control: value(job)})

	self := []ui.Node{
		ui.SettingRow{Label: "Memory", Detail: "The tray app, without the engine", Control: value(sysinfo.Bytes(d.self.Private) + " private · " + sysinfo.Bytes(d.self.WorkingSet) + " in RAM")},
		ui.Divider{},
		ui.SettingRow{Label: "CPU", Detail: "Share of all cores", Control: value(fmt.Sprintf("%.1f%%", d.selfCPU))},
		ui.Divider{},
		ui.SettingRow{Label: "Go runtime", Control: value(fmt.Sprintf("%s heap · %d goroutines", sysinfo.Bytes(d.heap), d.routines))},
		ui.Divider{},
		ui.SettingRow{Label: "Running for", Control: value(roughDuration(time.Since(appStarted)))},
	}

	lines := logx.Recent(80)
	logText := strings.Join(lines, "\n")
	if logText == "" {
		logText = "Nothing logged yet."
	}
	head := []ui.Node{ui.Title("Debug"), ui.Space(4)}
	if a.summarizing() {
		head = append(head, ui.Caption("Summary model"), ui.Card{Gap: 2, Items: a.summaryDebug(d, value)})
	}
	return ui.VStack{Gap: 12, Items: append(head,
		ui.Caption("Speech engine"),
		ui.Card{Gap: 2, Items: engine},
		ui.Caption("flit "+Version),
		ui.Card{Gap: 2, Items: self},
		ui.Caption("Log, latest lines"),
		ui.Card{Gap: 8, Items: []ui.Node{
			ui.Para{Text: logText, Muted: true, Mono: true},
			ui.HStack{Gap: 16, Items: []ui.Node{
				ui.Link{ID: "dbg:log", Glyph: ui.GlyphDocument, Label: "Open log file", OnClick: func() { openFile(logx.Path(a.dataDir)) }},
				ui.Link{ID: "dbg:data", Glyph: ui.GlyphFolder, Label: "Open data folder", OnClick: func() { openFolder(a.dataDir) }},
			}},
		}},
	)}
}

// summaryDebug is the Debug page's card for a running summary's model.
func (a *App) summaryDebug(d *debugState, value func(string) ui.Node) []ui.Node {
	run := a.sumRun
	where := "on the processor"
	if run.device == llm.GPUPrefill {
		where = "weights in memory, the graphics processor reads the prompt"
	} else if run.device == llm.GPU {
		where = "on the graphics processor"
	}
	mem := "unavailable"
	if d.sumErr == nil {
		mem = sysinfo.Bytes(d.sumSt.Private) + " private · " + sysinfo.Bytes(d.sumSt.WorkingSet) + " in RAM"
	}
	rows := []ui.Node{
		ui.SettingRow{Label: run.model.Name, Detail: fmt.Sprintf("llama-server (pid %d), %s", run.pid, where), Control: value("llama.cpp " + llm.RuntimeVersion)},
		ui.Divider{},
		ui.SettingRow{Label: "Memory", Control: value(mem)},
		ui.Divider{},
		ui.SettingRow{Label: "CPU", Detail: "Share of all cores", Control: value(fmt.Sprintf("%.1f%%", d.sumCPU))},
	}
	if a.onGPU() {
		gpu := "measuring"
		if d.gpuOK {
			gpu = fmt.Sprintf("%.0f%%", d.gpu*100)
		}
		rows = append(rows, ui.Divider{}, ui.SettingRow{Label: "Graphics processor", Detail: "The busiest of its engines, for this process", Control: value(gpu)})
	}
	return append(rows, ui.Divider{}, ui.SettingRow{Label: "Job", Control: value(fmt.Sprintf("%s, %d%%", run.text(), int(run.value()*100)))})
}

// engineOnGPU reports whether any running speech worker uses the GPU.
func engineOnGPU(d *debugState) bool {
	for _, e := range d.engines {
		if e.PID != 0 && e.Info.OnGPU() {
			return true
		}
	}
	return false
}

// roughDuration is 2 h 05 min, 4 min, 12 s.
func roughDuration(d time.Duration) string {
	switch {
	case d >= time.Hour:
		return fmt.Sprintf("%d h %02d min", int(d.Hours()), int(d.Minutes())%60)
	case d >= time.Minute:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	}
	return fmt.Sprintf("%d s", int(d.Seconds()))
}
