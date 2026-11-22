package app

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/motivated2die/flit/internal/download"
	"github.com/motivated2die/flit/internal/engine"
	"github.com/motivated2die/flit/internal/install"
	"github.com/motivated2die/flit/internal/logx"
	"github.com/motivated2die/flit/internal/models"
	"github.com/motivated2die/flit/internal/pipeline"
	"github.com/motivated2die/flit/internal/session"
)

// jobs transcribes sessions one after another on its own goroutine (the
// engine runs one job at a time anyway), owning the engine supervisor.
type jobs struct {
	a      *App
	queue  chan job
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	// fetchLocks let one download of a model finish before another of the
	// same model starts (the start-up download and the first job both
	// fetch; two at once collide on the file); different models download
	// at the same time.
	fetchMu    sync.Mutex
	fetchLocks map[string]*sync.Mutex

	// limit is the share of the processor a worker may use (0: no limit),
	// set from the UI thread; a worker started with another is replaced.
	limit     atomic.Int32
	limitUsed int32

	mu   sync.Mutex
	sup  *engine.Supervisor
	spec engine.LoadSpec
	asr  models.Model // what sup loads
	vad  models.Model
	diar models.Model // ID "": speakers off
}

// job is a session to transcribe, or (speakersOnly) to find the speakers
// of again.
type job struct {
	s            *session.Session
	speakersOnly bool
}

// engineState is what the debug view shows of the engine.
type engineState struct {
	ASR, VAD, Diar models.Model
	PID            int // 0: not loaded
	Info           engine.Info
}

func (j *jobs) engine() (st engineState, ok bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.sup == nil {
		return st, false
	}
	return engineState{ASR: j.asr, VAD: j.vad, Diar: j.diar, PID: j.sup.PID(), Info: j.sup.Info()}, true
}

func startJobs(a *App) *jobs {
	ctx, cancel := context.WithCancel(context.Background())
	j := &jobs{a: a, queue: make(chan job, 64), ctx: ctx, cancel: cancel, done: make(chan struct{})}
	go j.run()
	j.prepare()
	return j
}

func (j *jobs) add(s *session.Session) { j.enqueue(job{s: s}) }

// addFindSpeakers queues telling the speakers of a transcribed session
// apart (again).
func (j *jobs) addFindSpeakers(s *session.Session) { j.enqueue(job{s: s, speakersOnly: true}) }

func (j *jobs) enqueue(jb job) {
	select {
	case j.queue <- jb:
	default:
		logx.Printf("transcription queue full; %s waits for the next start", jb.s.Meta.ID)
	}
}

// prepare downloads the current language's model in the background, so the
// first meeting doesn't wait for it.
func (j *jobs) prepare() {
	lang, model, speakers := j.a.cfg.Language, j.a.cfg.Model, j.a.cfg.Speakers
	go func() {
		if _, _, _, err := j.models(lang, model, speakers); err != nil && j.ctx.Err() == nil {
			logx.Printf("model download: %v", err)
		}
	}()
}

func (j *jobs) close() {
	j.cancel()
	close(j.queue)
	select {
	case <-j.done:
	case <-time.After(5 * time.Second):
		logx.Printf("transcription didn't stop in time")
	}
	j.mu.Lock()
	if j.sup != nil {
		j.sup.Close()
	}
	j.mu.Unlock()
}

func (j *jobs) post(fn func()) { j.a.tray.Post(fn) }

func (j *jobs) run() {
	defer close(j.done)
	for jb := range j.queue {
		if j.ctx.Err() != nil {
			return
		}
		s := jb.s
		if jb.speakersOnly {
			j.post(func() { j.a.finding = s; j.a.invalidate() })
			err := j.findSpeakers(s)
			j.post(func() { j.a.onSpeakersFound(s, err) })
			continue
		}
		j.post(func() { j.a.working, j.a.progress, j.a.failed = s, 0, nil; j.a.refresh() })
		err := j.transcribe(s)
		j.post(func() { j.a.onTranscribed(s, err) })
	}
}

func (j *jobs) transcribe(s *session.Session) error {
	speakers := j.a.cfg.Speakers
	asr, vad, diar, err := j.models(s.Meta.Language, s.Meta.Model, speakers)
	if err != nil {
		return err
	}
	sup, err := j.supervisor(asr, vad, diar)
	if err != nil {
		return err
	}
	opt := pipeline.Options{Language: s.Meta.Language, Vocabulary: j.a.vocabulary(), Speakers: diar.ID != ""}
	start := time.Now()
	last := time.Time{}
	err = s.Transcribe(j.ctx, sup, opt, "", func(p float64) {
		if time.Since(last) < 200*time.Millisecond && p < 1 {
			return
		}
		last = time.Now()
		j.post(func() { j.a.progress = p; j.a.refresh(); j.a.showWorking(false) })
	})
	logx.Printf("transcribed %s in %.0f s: %v", s.Meta.ID, time.Since(start).Seconds(), err)
	return err
}

// findSpeakers tells the speakers of a transcribed session apart again.
func (j *jobs) findSpeakers(s *session.Session) error {
	asr, vad, diar, err := j.models(s.Meta.Language, s.Meta.Model, true)
	if err != nil {
		return err
	}
	if diar.ID == "" {
		return fmt.Errorf("the speaker model couldn't be downloaded")
	}
	sup, err := j.supervisor(asr, vad, diar)
	if err != nil {
		return err
	}
	start := time.Now()
	err = s.FindSpeakers(j.ctx, sup)
	logx.Printf("found %d speakers in %s in %.0f s: %v", s.Meta.Speakers, s.Meta.ID, time.Since(start).Seconds(), err)
	return err
}

// models makes sure the ASR model for lang (or the chosen one), the VAD
// model and, with speakers, the diarization model are on disk, downloading
// them with progress in the tray. A failed diarization download leaves
// diar empty (transcripts go on without speakers) rather than failing.
func (j *jobs) models(lang, chosen string, speakers bool) (asr, vad, diar models.Model, err error) {
	asr, ok := models.ByID(chosen)
	if chosen == "" || !ok {
		if asr, ok = models.Default(models.RoleASR, lang); !ok {
			return asr, vad, diar, fmt.Errorf("no speech model for %q", lang)
		}
	}
	vad, _ = models.Default(models.RoleVAD, "")
	need := []models.Model{vad, asr}
	if speakers {
		if d, ok := models.Default(models.RoleDiarize, ""); ok {
			need = append(need, d)
		}
	}
	// All at once: a slow server for one doesn't hold up the others.
	errs := make([]error, len(need))
	var wg sync.WaitGroup
	for i, m := range need {
		wg.Go(func() { errs[i] = j.fetch(m) })
	}
	wg.Wait()
	for i := range 2 {
		if errs[i] != nil {
			return asr, vad, diar, errs[i]
		}
	}
	if len(need) > 2 {
		if errs[2] != nil {
			logx.Printf("speakers off for now: %v", errs[2])
		} else {
			diar = need[2]
		}
	}
	return asr, vad, diar, nil
}

// fetch downloads a model unless it is on disk.
func (j *jobs) fetch(m models.Model) error {
	j.fetchMu.Lock()
	if j.fetchLocks == nil {
		j.fetchLocks = map[string]*sync.Mutex{}
	}
	lock := j.fetchLocks[m.ID]
	if lock == nil {
		lock = &sync.Mutex{}
		j.fetchLocks[m.ID] = lock
	}
	j.fetchMu.Unlock()
	lock.Lock()
	defer lock.Unlock()
	dir := modelsDir(j.a.dataDir)
	f := download.File{URL: m.URL(), Dest: m.Path(dir), SHA256: m.SHA256, Size: m.Size}
	if st, err := os.Stat(f.Dest); err == nil && st.Size() == m.Size {
		return nil
	}
	logx.Printf("downloading %s (%d MB)", m.ID, m.Size/1e6)
	j.post(func() { j.a.downloadProgress(m, 0, m.Size) })
	last := time.Time{}
	err := download.Client{UserAgent: "flit"}.Get(j.ctx, f, func(done, total int64) {
		if time.Since(last) < 250*time.Millisecond {
			return
		}
		last = time.Now()
		j.post(func() { j.a.downloadProgress(m, done, total) })
	})
	j.post(func() { j.a.downloadEnded(m, err) })
	if err != nil {
		return fmt.Errorf("downloading %s: %w", m.Name, err)
	}
	return nil
}

// supervisor returns the engine worker's supervisor for these models,
// replacing it when the models changed.
func (j *jobs) supervisor(asr, vad, diar models.Model) (*engine.Supervisor, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	dir := modelsDir(j.a.dataDir)
	spec := engine.LoadSpec{ASRModel: asr.Path(dir), VADModel: vad.Path(dir), CPUOnly: true}
	if diar.ID != "" {
		spec.DiarModel = diar.Path(dir)
	}
	limit := j.limit.Load()
	if limit > 0 {
		spec.Threads = capThreads(int(limit))
	}
	if j.sup != nil && j.spec.ASRModel == spec.ASRModel && j.spec.VADModel == spec.VADModel && j.spec.DiarModel == spec.DiarModel && j.limitUsed == limit {
		return j.sup, nil
	}
	if j.sup != nil {
		j.sup.Close()
		j.sup = nil
	}
	engineDir, err := engineDir(j.a.dataDir)
	if err != nil {
		return nil, err
	}
	exe, err := install.Current()
	if err != nil {
		return nil, err
	}
	spec.EngineDir = engineDir
	j.spec, j.asr, j.vad, j.diar = spec, asr, vad, diar
	j.limitUsed = limit
	j.sup = &engine.Supervisor{
		Command:     engine.WorkerCommand(exe),
		Spec:        spec,
		IdleTimeout: 2 * time.Minute,
		Logf:        engineLog,
	}
	if limit > 0 {
		// As live: a Windows job object caps it, below normal priority.
		j.sup.CPULimit, j.sup.Background = int(limit), true
		logx.Printf("transcription limited: at most %d%% of the processor, %d thread(s)", limit, spec.Threads)
	}
	return j.sup, nil
}

// releaseEngine stops the engine worker (it holds the models open); the
// next job starts a new one.
func (j *jobs) releaseEngine() {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.sup != nil {
		j.sup.Close()
		j.sup = nil
	}
}

// engineLog logs an engine worker's lines, leaving out its info chatter.
func engineLog(f string, args ...any) {
	msg := fmt.Sprintf(f, args...)
	if strings.HasPrefix(msg, "engine: ") && !strings.Contains(strings.ToLower(msg), "error") && !strings.Contains(strings.ToLower(msg), "warn") {
		return // the engine's info lines are noise in the app's log
	}
	logx.Printf("%s", msg)
}

func modelsDir(dataDir string) string { return dataDir + `\models` }

// onTranscribed runs on the UI thread when a session is done.
func (a *App) onTranscribed(s *session.Session, err error) {
	a.working, a.progress = nil, 0
	if err != nil {
		if s.Meta.State == session.Recorded {
			a.refresh() // stopped by quitting; picked up next time
			return
		}
		a.reloadSessions()
		a.fail("Transcription failed", err)
		return
	}
	a.lastReady = s
	a.forget(s)
	if !a.cfg.KeepAudio {
		for _, f := range []string{session.MeFile, session.ThemFile} {
			if err := os.Remove(s.Path(f)); err != nil && !os.IsNotExist(err) {
				logx.Printf("removing %s of %s: %v", f, s.Meta.ID, err)
			}
		}
	}
	a.reloadSessions()
	text, _ := s.Transcript(false)
	a.showReady(s, countWordsIn(text))
	a.refresh()
}

func countWordsIn(text string) int { return len(strings.Fields(text)) }
