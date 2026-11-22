package app

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/motivated2die/flit/internal/engine"
	"github.com/motivated2die/flit/internal/install"
	"github.com/motivated2die/flit/internal/logx"
	"github.com/motivated2die/flit/internal/models"
	"github.com/motivated2die/flit/internal/pipeline"
	"github.com/motivated2die/flit/internal/session"
	"github.com/motivated2die/flit/internal/ui"
)

// Transcribing during a meeting (Settings → Transcribe during meetings):
// while recording, a separate engine worker, capped by Windows at a share
// of the processor and below normal priority, transcribes the chunks that
// are certain every few seconds. The chunks come at the pace of the
// meeting (one per track about every 30 s of speech), so the load is
// steady and never above the cap. When the meeting ends, the usual
// transcription goes on from there (docs/research/live-transcription.md).

// Background loads: at most this share of all processors.
var liveLoads = []loadLevel{{"light", "Light", 15}, {"balanced", "Balanced", 25}, {"fast", "Faster", 40}}

// The limited transcription after a meeting: a one-off the user waits
// for, so it may take more than the steady live one.
var finalLoads = []loadLevel{{"light", "Light", 25}, {"balanced", "Balanced", 50}, {"fast", "Faster", 75}}

type loadLevel struct {
	id, name string
	percent  int
}

// levelOf returns the level id of levels (the middle one if unknown).
func levelOf(levels []loadLevel, id string) loadLevel {
	for _, l := range levels {
		if l.id == id {
			return l
		}
	}
	return levels[1]
}

// finalLimit is the share of the processor the transcription after a
// meeting may use (0: no limit).
func (a *App) finalLimit() int {
	if !a.cfg.LimitFinal {
		return 0
	}
	return levelOf(finalLoads, a.cfg.FinalLoad).percent
}

// applyFinalLimit hands the limit to the transcription jobs: the next
// engine worker they start keeps to it.
func (a *App) applyFinalLimit() {
	if a.jobs != nil {
		a.jobs.limit.Store(int32(a.finalLimit()))
	}
}

// finalSettings are Settings' switch and level for limiting the
// transcription after a meeting.
func (a *App) finalSettings() []ui.Node {
	nodes := []ui.Node{ui.Toggle{ID: "set:limitfinal", Label: "Limit the transcription after a meeting",
		Detail: "Keeps the PC quiet while it runs, at low priority; the transcript takes longer",
		On:     a.cfg.LimitFinal, OnChange: func(on bool) { a.cfg.LimitFinal = on; a.save(); a.applyFinalLimit(); a.invalidate() }}}
	if !a.cfg.LimitFinal {
		return nodes
	}
	cur := levelOf(finalLoads, a.cfg.FinalLoad)
	sel := 1
	var names []string
	for i, l := range finalLoads {
		names = append(names, l.name)
		if l.id == cur.id {
			sel = i
		}
	}
	return append(nodes, ui.SettingRow{Label: "Load",
		Detail: fmt.Sprintf("At most %d%% of the processor, %d of %d threads", cur.percent, capThreads(cur.percent), runtime.NumCPU()),
		Control: ui.Segmented{ID: "set:finalload", Options: names, Selected: sel, OnSelect: func(i int) {
			a.cfg.FinalLoad = finalLoads[i].id
			a.save()
			a.applyFinalLimit()
			a.invalidate()
		}}})
}

func liveLoad(id string) (int, string) {
	l := levelOf(liveLoads, id)
	return l.percent, l.name
}

// capThreads matches the engine's threads to a cap (live, or a limited
// transcription after the meeting): more threads than the cap allows only
// take turns (measured: slower).
func capThreads(percent int) int {
	return max(1, (runtime.NumCPU()*percent+50)/100)
}

// liveTick is how often the recording is looked at for certain chunks.
const liveTick = 15 * time.Second

// liveJob is the transcription running during a recording.
type liveJob struct {
	sess   *session.Session
	cancel context.CancelFunc
	done   chan struct{}
	upTo   time.Duration // transcribed up to here (both tracks)
	// Its own engine worker, the models it loads and its threads (for
	// the debug view).
	sup      *engine.Supervisor
	asr, vad models.Model
	threads  int
}

// startLive starts transcribing the recording as it goes, if enabled and
// the models are on disk (they download at start-up; a meeting never
// waits for them).
func (a *App) startLive(s *session.Session) {
	if !a.cfg.LiveTranscribe || a.live != nil || a.jobs == nil {
		return
	}
	asr, vad, ok := a.localModels(s.Meta.Language, s.Meta.Model)
	if !ok {
		logx.Printf("live transcription: the models aren't downloaded yet")
		return
	}
	engineDir, err := engineDir(a.dataDir)
	if err != nil {
		logx.Printf("live transcription: %v", err)
		return
	}
	exe, err := install.Current()
	if err != nil {
		logx.Printf("live transcription: %v", err)
		return
	}
	percent, _ := liveLoad(a.cfg.LiveLoad)
	dir := modelsDir(a.dataDir)
	sup := &engine.Supervisor{
		Command:     engine.WorkerCommand(exe),
		Spec:        engine.LoadSpec{EngineDir: engineDir, ASRModel: asr.Path(dir), VADModel: vad.Path(dir), CPUOnly: true, Threads: capThreads(percent)},
		IdleTimeout: 2 * time.Minute,
		CPULimit:    percent,
		Background:  true,
		Logf:        engineLog,
	}
	ctx, cancel := context.WithCancel(context.Background())
	job := &liveJob{sess: s, cancel: cancel, done: make(chan struct{}), sup: sup, asr: asr, vad: vad, threads: capThreads(percent)}
	a.live = job
	opt := pipeline.Options{Language: s.Meta.Language, Vocabulary: a.cfg.Vocabulary}
	logx.Printf("live transcription: at most %d%% of the processor, %d thread(s)", percent, capThreads(percent))
	go func() {
		defer close(job.done)
		defer sup.Close()
		t := time.NewTicker(liveTick)
		defer t.Stop()
		failing := false
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			n, err := s.TranscribeLive(ctx, sup, opt)
			if ctx.Err() != nil {
				return
			}
			if err != nil && !failing {
				logx.Printf("live transcription: %v", err) // keeps trying; the end does the rest anyway
			}
			failing = err != nil
			if n > 0 {
				upTo := s.LiveProgress()
				a.tray.Post(func() {
					if a.live == job {
						job.upTo = upTo
						a.invalidate()
					}
				})
			}
		}
	}()
}

// afterLive stops live transcription and runs then once it has (at once
// without one). The session's files are left alone only after that.
func (a *App) afterLive(then func()) {
	job := a.live
	if job == nil {
		then()
		return
	}
	a.live = nil
	job.cancel()
	go func() {
		<-job.done
		a.tray.Post(then)
	}()
}

// stopLiveNow stops live transcription and waits (quitting).
func (a *App) stopLiveNow() {
	if job := a.live; job != nil {
		a.live = nil
		job.cancel()
		<-job.done
	}
}

// localModels are the session's models if both are on disk.
func (a *App) localModels(lang, chosen string) (asr, vad models.Model, ok bool) {
	asr, found := models.ByID(chosen)
	if chosen == "" || !found {
		if asr, found = models.Default(models.RoleASR, lang); !found {
			return asr, vad, false
		}
	}
	vad, _ = models.Default(models.RoleVAD, "")
	dir := modelsDir(a.dataDir)
	for _, m := range []models.Model{asr, vad} {
		if st, err := os.Stat(m.Path(dir)); err != nil || st.Size() != m.Size {
			return asr, vad, false
		}
	}
	return asr, vad, true
}

// liveSettings is the switch and, when on, the load.
func (a *App) liveSettings() []ui.Node {
	nodes := []ui.Node{ui.Toggle{ID: "set:live", Label: "Transcribe during meetings",
		Detail: "Steadily in the background, so the transcript is ready soon after; the same result as afterwards",
		On:     a.cfg.LiveTranscribe, OnChange: func(on bool) { a.cfg.LiveTranscribe = on; a.save(); a.invalidate() }}}
	if !a.cfg.LiveTranscribe {
		return nodes
	}
	percent, _ := liveLoad(a.cfg.LiveLoad)
	sel := 1
	var names []string
	for i, l := range liveLoads {
		names = append(names, l.name)
		if l.percent == percent {
			sel = i
		}
	}
	return append(nodes, ui.SettingRow{Label: "Background load",
		Detail: fmt.Sprintf("At most %d%% of the processor, at low priority", percent),
		Control: ui.Segmented{ID: "set:liveload", Options: names, Selected: sel, OnSelect: func(i int) {
			a.cfg.LiveLoad = liveLoads[i].id
			a.save()
			a.invalidate()
		}}})
}
