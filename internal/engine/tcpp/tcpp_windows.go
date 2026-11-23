// Package tcpp calls transcribe.cpp's C API in transcribe.dll without cgo.
//
// The DLL is loaded from an explicit folder (LoadLibraryEx with an altered
// search path, so ggml*.dll next to it resolve). Every struct the binding
// passes is mirrored in abi_windows.go and checked against the DLL's own
// sizes when it loads; a mismatch refuses to run instead of corrupting
// memory. Only what flit needs is bound: backends, model load, offline ASR
// runs with segment and word results, abort, logging, Silero VAD, and
// Sortformer diarization.
//
// Threading follows transcribe.h: models may be shared, a session is used by
// one goroutine at a time, and at most one run may be in flight per model.
package tcpp

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Lib is the loaded DLL.
type Lib struct {
	dir string
	h   windows.Handle
	p   procs
}

type proc uintptr

// call invokes the function. Arguments are often addresses of Go values;
// uintptrescapes keeps them alive and in place for the duration of the call.
//
//go:uintptrescapes
func (p proc) call(args ...uintptr) uintptr {
	r, _, _ := syscall.SyscallN(uintptr(p), args...)
	return r
}

type procs struct {
	version, statusString, abiStructSize, logSet                                  proc
	backendInitParamsInit, initBackendsEx                                         proc
	deviceInfoInit, deviceGetInfo, modelDevice                                    proc
	modelLoadParamsInit, modelLoadFile, modelFree, capabilitiesInit, modelGetCaps proc
	modelSupports                                                                 proc
	sessionParamsInit, sessionInit, sessionFree, setAbortCallback                 proc
	runParamsInit, run, wasAborted, wasTruncated                                  proc
	fullText, detectedLanguage, nSegments, nWords                                 proc
	segmentInit, getSegment, wordInit, getWord                                    proc
	vadInfoInit, vadSessionParamsInit, vadParamsInit, vadResultInit               proc
	vadSegmentInit, vadGetInfo, vadSessionInit, vadSessionFree                    proc
	vadRun, vadGetResult, vadProbs, vadGetSegment                                 proc
	diarInfoInit, diarSessionParamsInit, diarParamsInit, diarGetInfo              proc
	diarSessionInit, diarSessionFree, diarRun, diarNSegments, diarGetSegment      proc
	speakerSegmentInit, sortformerExtInit, acceptsExtKind                         proc
}

var (
	loadMu sync.Mutex
	loaded *Lib
)

// Load loads transcribe.dll from dir, checks its ABI, and initialises its
// backends from the same folder (cpuOnly restricts them to the CPU). Loading
// twice returns the first Lib; a process can only have one.
func Load(dir string, cpuOnly bool) (*Lib, error) {
	loadMu.Lock()
	defer loadMu.Unlock()
	if loaded != nil {
		return loaded, nil
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	h, err := windows.LoadLibraryEx(filepath.Join(dir, "transcribe.dll"), 0, windows.LOAD_WITH_ALTERED_SEARCH_PATH)
	if err != nil {
		return nil, fmt.Errorf("loading transcribe.dll from %s: %w", dir, err)
	}
	l := &Lib{dir: dir, h: h}
	if err := l.bind(); err != nil {
		windows.FreeLibrary(h)
		return nil, err
	}
	if err := l.checkABI(); err != nil {
		windows.FreeLibrary(h)
		return nil, err
	}
	installLogger(l)
	if err := l.initBackends(cpuOnly); err != nil {
		return nil, err
	}
	loaded = l
	return l, nil
}

func (l *Lib) bind() error {
	var missing []string
	get := func(name string) proc {
		a, err := windows.GetProcAddress(l.h, name)
		if err != nil {
			missing = append(missing, name)
		}
		return proc(a)
	}
	p := &l.p
	p.version = get("transcribe_version")
	p.statusString = get("transcribe_status_string")
	p.abiStructSize = get("transcribe_abi_struct_size")
	p.logSet = get("transcribe_log_set")
	p.backendInitParamsInit = get("transcribe_backend_init_params_init")
	p.initBackendsEx = get("transcribe_init_backends_ex")
	p.deviceInfoInit = get("transcribe_device_info_init")
	p.deviceGetInfo = get("transcribe_device_get_info")
	p.modelDevice = get("transcribe_model_device")
	p.modelLoadParamsInit = get("transcribe_model_load_params_init")
	p.modelLoadFile = get("transcribe_model_load_file")
	p.modelFree = get("transcribe_model_free")
	p.capabilitiesInit = get("transcribe_capabilities_init")
	p.modelGetCaps = get("transcribe_model_get_capabilities")
	p.modelSupports = get("transcribe_model_supports")
	p.sessionParamsInit = get("transcribe_session_params_init")
	p.sessionInit = get("transcribe_session_init")
	p.sessionFree = get("transcribe_session_free")
	p.setAbortCallback = get("transcribe_set_abort_callback")
	p.runParamsInit = get("transcribe_run_params_init")
	p.run = get("transcribe_run")
	p.wasAborted = get("transcribe_was_aborted")
	p.wasTruncated = get("transcribe_was_truncated")
	p.fullText = get("transcribe_full_text")
	p.detectedLanguage = get("transcribe_detected_language")
	p.nSegments = get("transcribe_n_segments")
	p.nWords = get("transcribe_n_words")
	p.segmentInit = get("transcribe_segment_init")
	p.getSegment = get("transcribe_get_segment")
	p.wordInit = get("transcribe_word_init")
	p.getWord = get("transcribe_get_word")
	p.vadInfoInit = get("transcribe_vad_info_init")
	p.vadSessionParamsInit = get("transcribe_vad_session_params_init")
	p.vadParamsInit = get("transcribe_vad_params_init")
	p.vadResultInit = get("transcribe_vad_result_init")
	p.vadSegmentInit = get("transcribe_vad_segment_init")
	p.vadGetInfo = get("transcribe_vad_get_info")
	p.vadSessionInit = get("transcribe_vad_session_init")
	p.vadSessionFree = get("transcribe_vad_session_free")
	p.vadRun = get("transcribe_vad_run")
	p.vadGetResult = get("transcribe_vad_get_result")
	p.vadProbs = get("transcribe_vad_probs")
	p.vadGetSegment = get("transcribe_vad_get_segment")
	p.diarInfoInit = get("transcribe_diarize_info_init")
	p.diarSessionParamsInit = get("transcribe_diarize_session_params_init")
	p.diarParamsInit = get("transcribe_diarize_params_init")
	p.diarGetInfo = get("transcribe_diarize_get_info")
	p.diarSessionInit = get("transcribe_diarize_session_init")
	p.diarSessionFree = get("transcribe_diarize_session_free")
	p.diarRun = get("transcribe_diarize_run")
	p.diarNSegments = get("transcribe_diarize_n_segments")
	p.diarGetSegment = get("transcribe_diarize_get_segment")
	p.speakerSegmentInit = get("transcribe_speaker_segment_init")
	p.sortformerExtInit = get("transcribe_sortformer_diarize_ext_init")
	p.acceptsExtKind = get("transcribe_model_accepts_ext_kind")
	if len(missing) > 0 {
		return fmt.Errorf("transcribe.dll lacks %v", missing)
	}
	return nil
}

// ErrABI means the DLL's struct layout differs from the binding's.
var ErrABI = errors.New("transcribe.dll ABI mismatch")

func (l *Lib) checkABI() error {
	for _, s := range abiStructs {
		got := l.p.abiStructSize.call(s.id)
		if got != s.size {
			return fmt.Errorf("%w: %s is %d bytes in the DLL, %d in the binding", ErrABI, s.name, got, s.size)
		}
	}
	return nil
}

func (l *Lib) initBackends(cpuOnly bool) error {
	var p backendInitParams
	l.p.backendInitParamsInit.call(uintptr(unsafe.Pointer(&p)))
	dir, err := windows.BytePtrFromString(l.dir)
	if err != nil {
		return err
	}
	p.ArtifactDir = dir
	if cpuOnly {
		p.AllowedBackends = 1 // TRANSCRIBE_BACKEND_MASK_CPU
	}
	return l.status(l.p.initBackendsEx.call(uintptr(unsafe.Pointer(&p))), "init backends")
}

// Version is the DLL's version string.
func (l *Lib) Version() string { return cstr(l.p.version.call()) }

// Dir is the folder the DLL was loaded from.
func (l *Lib) Dir() string { return l.dir }

// Status codes of transcribe_status that callers act on.
const (
	StatusOK                   = 0
	StatusFileNotFound         = 3
	StatusOOM                  = 7
	StatusBackend              = 8
	StatusUnsupportedLanguage  = 10
	StatusUnsupportedTimestamp = 12
	StatusAborted              = 13
	StatusInputTooLong         = 17
	StatusOutputTruncated      = 18
	StatusOutputRepetition     = 19
	StatusInputTooShort        = 21
)

// Error is a non-OK transcribe_status.
type Error struct {
	Op     string
	Status int
	Text   string
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s (status %d)", e.Op, e.Text, e.Status) }

// IsStatus reports whether err is an engine error with that status.
func IsStatus(err error, status int) bool {
	var e *Error
	return errors.As(err, &e) && e.Status == status
}

func (l *Lib) status(r uintptr, op string) error {
	s := int(int32(r))
	if s == StatusOK {
		return nil
	}
	return &Error{Op: op, Status: s, Text: cstr(l.p.statusString.call(uintptr(s)))}
}

// cstr copies a NUL-terminated C string; the engine's pointers are
// borrowed and die with the next run.
func cstr(p uintptr) string {
	if p == 0 {
		return ""
	}
	return windows.BytePtrToString((*byte)(cptr(p)))
}

func cbool(r uintptr) bool { return r&0xFF != 0 }

// cptr turns an address returned by the engine into a pointer. The memory
// is the engine's (never Go's heap), so the GC moving objects doesn't
// apply; the indirection keeps vet's uintptr check, meant for Go memory,
// quiet in this one place.
func cptr(p uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&p)) }
