package tcpp

import (
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Backend selects where a model runs.
type Backend int32

const (
	BackendAuto   Backend = 0
	BackendCPU    Backend = 1
	BackendVulkan Backend = 3
)

// Timestamps is the granularity a run reports.
type Timestamps int32

const (
	TimestampsNone    Timestamps = 0
	TimestampsAuto    Timestamps = 1
	TimestampsSegment Timestamps = 2
	TimestampsWord    Timestamps = 3
)

// Model is a loaded GGUF model. It may be shared between goroutines, but
// only one run may be in flight per model (transcribe.h, threading notes);
// Model serialises runs of its sessions itself.
type Model struct {
	lib  *Lib
	ptr  uintptr
	runs sync.Mutex
	Caps Capabilities
}

// Capabilities is what the loaded model supports.
type Capabilities struct {
	SampleRate     int
	Languages      []string
	MaxTimestamps  Timestamps
	LanguageDetect bool
	Streaming      bool
	MaxAudioMS     int64 // 0: no practical limit
}

// LoadModel loads a model file (UTF-8 path).
func (l *Lib) LoadModel(path string, backend Backend) (*Model, error) {
	var p modelLoadParams
	l.p.modelLoadParamsInit.call(uintptr(unsafe.Pointer(&p)))
	p.Backend = int32(backend)
	cpath, err := windows.BytePtrFromString(path)
	if err != nil {
		return nil, err
	}
	var ptr uintptr
	if err := l.status(l.p.modelLoadFile.call(uintptr(unsafe.Pointer(cpath)), uintptr(unsafe.Pointer(&p)), uintptr(unsafe.Pointer(&ptr))), "load model"); err != nil {
		return nil, err
	}
	m := &Model{lib: l, ptr: ptr}
	m.Caps = m.capabilities()
	return m, nil
}

func (m *Model) capabilities() Capabilities {
	var c capabilities
	m.lib.p.capabilitiesInit.call(uintptr(unsafe.Pointer(&c)))
	if m.lib.status(m.lib.p.modelGetCaps.call(m.ptr, uintptr(unsafe.Pointer(&c))), "capabilities") != nil {
		return Capabilities{}
	}
	caps := Capabilities{
		SampleRate:     int(c.NativeSampleRate),
		MaxTimestamps:  Timestamps(c.MaxTimestampKind),
		LanguageDetect: c.SupportsLanguageDetect,
		Streaming:      c.SupportsStreaming,
		MaxAudioMS:     c.MaxAudioMS,
	}
	if c.NLanguages > 0 && c.Languages != nil {
		for _, p := range unsafe.Slice(c.Languages, c.NLanguages) {
			caps.Languages = append(caps.Languages, windows.BytePtrToString(p))
		}
	}
	return caps
}

// Feature is a transcribe_feature a model may support.
type Feature int

const (
	FeatureLongForm      Feature = 2
	FeatureCancellation  Feature = 3
	FeatureDiarization   Feature = 6
	FeatureVocabulary    Feature = 7
	FeatureContextPrompt Feature = 8
)

// Supports reports whether the model supports a feature (for vocabulary:
// whether its decoder takes phrase boosting at all).
func (m *Model) Supports(f Feature) bool {
	return cbool(m.lib.p.modelSupports.call(m.ptr, uintptr(f)))
}

// Close frees the model. All its sessions must be closed first.
func (m *Model) Close() {
	if m.ptr != 0 {
		m.lib.p.modelFree.call(m.ptr)
		m.ptr = 0
	}
}

// Session holds per-run state and results. Use it from one goroutine at a
// time.
type Session struct {
	m       *Model
	ptr     uintptr
	abortID uintptr
}

// NewSession creates a session; threads 0 uses the engine's default.
func (m *Model) NewSession(threads int) (*Session, error) {
	var p sessionParams
	m.lib.p.sessionParamsInit.call(uintptr(unsafe.Pointer(&p)))
	if threads > 0 {
		p.NThreads = int32(threads)
	}
	var ptr uintptr
	if err := m.lib.status(m.lib.p.sessionInit.call(m.ptr, uintptr(unsafe.Pointer(&p)), uintptr(unsafe.Pointer(&ptr))), "session init"); err != nil {
		return nil, err
	}
	s := &Session{m: m, ptr: ptr, abortID: newAbortFlag()}
	m.lib.p.setAbortCallback.call(ptr, abortCallback, s.abortID)
	return s, nil
}

// Close frees the session.
func (s *Session) Close() {
	if s.ptr != 0 {
		s.m.lib.p.sessionFree.call(s.ptr)
		s.ptr = 0
		freeAbortFlag(s.abortID)
	}
}

// Abort asks a run in progress to stop; it returns StatusAborted with the
// chunks completed so far readable. Safe from any goroutine.
func (s *Session) Abort() { setAbort(s.abortID, true) }

// RunOptions configure one offline run.
type RunOptions struct {
	Language   string // "" lets the model decide (detect, if it can)
	Timestamps Timestamps
	Vocabulary []string // custom terms in priority order (phrase boosting)
}

// Segment is one result segment, times in milliseconds of the input.
type Segment struct {
	T0, T1    int64
	Text      string
	Words     []Word
	SpeakerID int // 0: no speaker attribution
}

// Word is one word with its times.
type Word struct {
	T0, T1 int64
	Text   string
}

// Result is a run's output, copied out of the engine.
type Result struct {
	Text      string
	Language  string // detected or used, if the model reports it
	Segments  []Segment
	Truncated bool
}

// Run transcribes 16 kHz mono PCM. On StatusAborted, StatusOutputTruncated
// and StatusOutputRepetition the partial result is returned along with the
// error.
func (s *Session) Run(pcm []float32, opt RunOptions) (Result, error) {
	l := s.m.lib
	var p runParams
	l.p.runParamsInit.call(uintptr(unsafe.Pointer(&p)))
	p.Timestamps = int32(opt.Timestamps)
	var keep []*byte // C strings live until the call returns
	if opt.Language != "" {
		b, err := windows.BytePtrFromString(opt.Language)
		if err != nil {
			return Result{}, err
		}
		p.Language = b
	}
	if len(opt.Vocabulary) > 0 {
		for _, t := range opt.Vocabulary {
			b, err := windows.BytePtrFromString(t)
			if err != nil {
				return Result{}, err
			}
			keep = append(keep, b)
		}
		p.Vocabulary = &keep[0]
		p.NVocabulary = int32(len(keep))
	}
	var pcmPtr uintptr
	if len(pcm) > 0 {
		pcmPtr = uintptr(unsafe.Pointer(&pcm[0]))
	}

	s.m.runs.Lock()
	setAbort(s.abortID, false)
	r := l.p.run.call(s.ptr, pcmPtr, uintptr(len(pcm)), uintptr(unsafe.Pointer(&p)))
	err := l.status(r, "run")
	var res Result
	if err == nil || IsStatus(err, StatusAborted) || IsStatus(err, StatusOutputTruncated) || IsStatus(err, StatusOutputRepetition) {
		res = s.result()
	}
	s.m.runs.Unlock()
	runtime.KeepAlive(pcm)
	runtime.KeepAlive(keep)
	runtime.KeepAlive(&p)
	return res, err
}

// result copies the session's current result.
func (s *Session) result() Result {
	l := s.m.lib
	res := Result{
		Text:      cstr(l.p.fullText.call(s.ptr)),
		Language:  cstr(l.p.detectedLanguage.call(s.ptr)),
		Truncated: cbool(l.p.wasTruncated.call(s.ptr)),
	}
	var words []Word
	nw := int(int32(l.p.nWords.call(s.ptr)))
	for i := 0; i < nw; i++ {
		var w word
		l.p.wordInit.call(uintptr(unsafe.Pointer(&w)))
		if l.p.getWord.call(s.ptr, uintptr(i), uintptr(unsafe.Pointer(&w))) != StatusOK {
			continue
		}
		words = append(words, Word{T0: w.T0MS, T1: w.T1MS, Text: windows.BytePtrToString(w.Text)})
	}
	ns := int(int32(l.p.nSegments.call(s.ptr)))
	for i := 0; i < ns; i++ {
		var g segment
		l.p.segmentInit.call(uintptr(unsafe.Pointer(&g)))
		if l.p.getSegment.call(s.ptr, uintptr(i), uintptr(unsafe.Pointer(&g))) != StatusOK {
			continue
		}
		seg := Segment{T0: g.T0MS, T1: g.T1MS, Text: windows.BytePtrToString(g.Text), SpeakerID: int(g.SpeakerID)}
		if g.NWords > 0 && int(g.FirstWord)+int(g.NWords) <= len(words) {
			seg.Words = append([]Word(nil), words[g.FirstWord:g.FirstWord+g.NWords]...)
		}
		res.Segments = append(res.Segments, seg)
	}
	return res
}

// Abort flags, reached from the engine's abort callback by id.
var (
	abortMu    sync.Mutex
	abortFlags = map[uintptr]*atomic.Bool{}
	abortNext  uintptr
	// One callback for all sessions: windows.NewCallback entries are never
	// freed, so it is created once.
	abortCallback = windows.NewCallback(func(id uintptr) uintptr {
		abortMu.Lock()
		f := abortFlags[id]
		abortMu.Unlock()
		if f != nil && f.Load() {
			return 1
		}
		return 0
	})
)

func newAbortFlag() uintptr {
	abortMu.Lock()
	defer abortMu.Unlock()
	abortNext++
	abortFlags[abortNext] = &atomic.Bool{}
	return abortNext
}

func setAbort(id uintptr, v bool) {
	abortMu.Lock()
	f := abortFlags[id]
	abortMu.Unlock()
	if f != nil {
		f.Store(v)
	}
}

func freeAbortFlag(id uintptr) {
	abortMu.Lock()
	delete(abortFlags, id)
	abortMu.Unlock()
}
