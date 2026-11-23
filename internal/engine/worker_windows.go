package engine

import (
	"bufio"
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"

	"github.com/motivated2die/flit/internal/engine/tcpp"
	"github.com/motivated2die/flit/internal/pipeline"
)

// RunWorker is the worker process's main: it serves requests from stdin
// until the supervisor closes it, and returns the exit code.
func RunWorker() int {
	// The protocol owns the real stdout. ggml prints to stdout in places,
	// and the engine's C runtime takes its stdout from the process's
	// standard handle when transcribe.dll loads, so that handle is pointed
	// at stderr first: stray prints end up in the log, not in the protocol.
	//
	// The protocol keeps the original os.Stdout *File: wrapping the handle
	// in a second File would leave the first unreachable, and its finalizer
	// would close the handle under the protocol.
	protocol := os.Stdout
	if err := windows.SetStdHandle(windows.STD_OUTPUT_HANDLE, must(windows.GetStdHandle(windows.STD_ERROR_HANDLE))); err != nil {
		fmt.Fprintln(os.Stderr, "redirecting stdout:", err)
		return 2
	}
	os.Stdout = os.Stderr
	tcpp.SetLogger(func(level tcpp.LogLevel, msg string) {
		if level != tcpp.LogDebug {
			fmt.Fprintln(os.Stderr, msg)
		}
	})
	out := bufio.NewWriterSize(protocol, 1<<20)
	w := &flushWriter{out}
	if err := Serve(bufio.NewReaderSize(os.Stdin, 1<<20), w, &tcppBackend{}); err != nil {
		fmt.Fprintln(os.Stderr, "engine worker:", err)
		return 1
	}
	return 0
}

func must(h windows.Handle, err error) windows.Handle {
	if err != nil {
		return 0
	}
	return h
}

// flushWriter flushes after every frame write sequence; Serve writes one
// response per request.
type flushWriter struct{ *bufio.Writer }

func (f *flushWriter) Write(p []byte) (int, error) {
	n, err := f.Writer.Write(p)
	if err == nil {
		err = f.Flush()
	}
	return n, err
}

// tcppBackend runs requests on transcribe.cpp.
type tcppBackend struct {
	lib      *tcpp.Lib
	asr, vad *tcpp.Model
	session  *tcpp.Session
	vadSess  *tcpp.VAD
	vocab    bool
	spec     LoadSpec
	diar     *tcpp.Model
	diarSess *tcpp.Diarizer
	info     Info
}

func (b *tcppBackend) CurrentInfo() Info { return b.info }

func (b *tcppBackend) Load(spec LoadSpec) (Info, error) {
	if b.lib == nil {
		lib, err := tcpp.Load(spec.EngineDir, spec.CPUOnly)
		if err != nil {
			return Info{}, err
		}
		b.lib = lib
	}
	backend := tcpp.BackendAuto
	if spec.CPUOnly {
		backend = tcpp.BackendCPU
	}
	b.spec = spec
	info := Info{Version: b.lib.Version()}
	if spec.VADModel != "" && b.vad == nil {
		m, err := b.lib.LoadModel(spec.VADModel, tcpp.BackendCPU)
		if err != nil {
			return info, fmt.Errorf("VAD model: %w", err)
		}
		v, err := m.NewVAD(spec.Threads)
		if err != nil {
			return info, err
		}
		b.vad, b.vadSess = m, v
	}
	if spec.ASRModel != "" && b.asr == nil {
		m, err := b.lib.LoadModel(spec.ASRModel, backend)
		if err != nil {
			return info, fmt.Errorf("ASR model: %w", err)
		}
		s, err := m.NewSession(spec.Threads)
		if err != nil {
			return info, err
		}
		b.asr, b.session = m, s
		b.vocab = m.Supports(tcpp.FeatureVocabulary)
	}
	if b.asr != nil {
		info.Languages = b.asr.Caps.Languages
		info.MaxAudioMS = b.asr.Caps.MaxAudioMS
		info.Vocabulary = b.vocab
		info.Backend = b.asr.Device.Kind
		info.DeviceName = b.asr.Device.Name
		info.DeviceDesc = b.asr.Device.Description
		info.DeviceType = b.asr.Device.Type.String()
	}
	b.info = info
	return info, nil
}

func (b *tcppBackend) VAD(pcm []float32) ([]float32, int, error) {
	if b.vadSess == nil {
		return nil, 0, errors.New("no VAD model loaded")
	}
	res, err := b.vadSess.Run(pcm, tcpp.VADParams{})
	if err != nil {
		return nil, 0, wrapStatus(err)
	}
	return res.Probs, b.vadSess.FrameSamples, nil
}

func (b *tcppBackend) Transcribe(pcm []float32, opt pipeline.Options) ([]pipeline.Segment, error) {
	if b.session == nil {
		return nil, errors.New("no ASR model loaded")
	}
	ro := tcpp.RunOptions{Language: opt.Language, Timestamps: tcpp.TimestampsWord}
	if b.asr.Caps.MaxTimestamps < tcpp.TimestampsWord {
		ro.Timestamps = tcpp.TimestampsAuto
	}
	if b.vocab {
		ro.Vocabulary = opt.Vocabulary
	}
	res, err := b.session.Run(pcm, ro)
	if err != nil && len(res.Segments) == 0 {
		return nil, wrapStatus(err)
	}
	out := make([]pipeline.Segment, 0, len(res.Segments))
	for _, s := range res.Segments {
		seg := pipeline.Segment{T0: s.T0, T1: s.T1, Text: s.Text}
		for _, w := range s.Words {
			seg.Words = append(seg.Words, pipeline.Word{T0: w.T0, T1: w.T1, Text: w.Text})
		}
		out = append(out, seg)
	}
	if len(out) == 0 && res.Text != "" {
		out = append(out, pipeline.Segment{T0: 0, T1: int64(len(pcm)) * 1000 / pipeline.Rate, Text: res.Text})
	}
	return out, nil
}

// Diarize loads the diarization model on first use (most requests never
// need it) and runs it over the whole recording.
func (b *tcppBackend) Diarize(pcm []float32) ([]pipeline.SpeakerTurn, error) {
	if b.diarSess == nil {
		if b.spec.DiarModel == "" {
			return nil, errors.New("no diarization model")
		}
		backend := tcpp.BackendAuto
		if b.spec.CPUOnly {
			backend = tcpp.BackendCPU
		}
		m, err := b.lib.LoadModel(b.spec.DiarModel, backend)
		if err != nil {
			return nil, fmt.Errorf("diarization model: %w", err)
		}
		d, err := m.NewDiarizer(b.spec.Threads)
		if err != nil {
			m.Close()
			return nil, err
		}
		b.diar, b.diarSess = m, d
		b.info.DiarBackend = m.Device.Kind
		b.info.DiarDeviceDesc = m.Device.Description
	}
	turns, err := b.diarSess.Run(pcm)
	if err != nil {
		return nil, wrapStatus(err)
	}
	out := make([]pipeline.SpeakerTurn, len(turns))
	for i, t := range turns {
		out[i] = pipeline.SpeakerTurn{T0: t.T0, T1: t.T1, Speaker: t.Speaker}
	}
	return out, nil
}

type statusErr struct {
	error
	status int
}

func (e statusErr) EngineStatus() int { return e.status }

func wrapStatus(err error) error {
	var te *tcpp.Error
	if errors.As(err, &te) {
		return statusErr{err, te.Status}
	}
	return err
}
