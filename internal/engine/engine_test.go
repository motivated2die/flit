package engine

import (
	"bytes"
	"context"
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/motivated2die/flit/internal/pipeline"
)

// The test binary doubles as a worker with a fake backend, so the
// supervisor is tested with real processes and pipes but without the DLL.
func TestMain(m *testing.M) {
	if os.Getenv("FLIT_FAKE_WORKER") == "1" {
		if err := Serve(os.Stdin, os.Stdout, fakeBackend{}); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type fakeBackend struct{}

func (fakeBackend) Load(spec LoadSpec) (Info, error) {
	if spec.ASRModel == "missing" {
		return Info{}, errors.New("no such model")
	}
	if spec.ASRModel == "gpu-load-fail" && !spec.CPUOnly {
		return Info{}, statusErr{errors.New("vulkan device lost"), statusBackend}
	}
	if spec.ASRModel == "gpu-load-crash" && !spec.CPUOnly {
		os.Exit(4)
	}
	backend := "vulkan"
	if spec.CPUOnly {
		backend = "cpu"
	}
	return Info{Version: "fake", Languages: []string{"de", "en"}, Backend: backend, DeviceDesc: "Fake " + backend}, nil
}

func (fakeBackend) VAD(pcm []float32) ([]float32, int, error) {
	return make([]float32, len(pcm)/512), 512, nil
}

// Diarize says the first half is speaker 1 and the second speaker 2.
func (fakeBackend) Diarize(pcm []float32) ([]pipeline.SpeakerTurn, error) {
	ms := int64(len(pcm)) * 1000 / pipeline.Rate
	return []pipeline.SpeakerTurn{{T0: 0, T1: ms / 2, Speaker: 1}, {T0: ms / 2, T1: ms, Speaker: 2}}, nil
}

func (fakeBackend) Transcribe(pcm []float32, opt pipeline.Options) ([]pipeline.Segment, error) {
	switch opt.Language {
	case "crash":
		os.Exit(3)
	case "hang":
		time.Sleep(time.Minute)
	case "bad":
		return nil, statusErr{errors.New("unsupported language"), 10}
	case "gpu-err":
		return nil, statusErr{errors.New("vulkan queue submit failed"), statusBackend}
	}
	var sum float32
	for _, v := range pcm {
		sum += v
	}
	return []pipeline.Segment{{T0: 0, T1: int64(len(pcm)) / 16, Text: strings.Repeat("x", int(sum)) + " " + strings.Join(opt.Vocabulary, ",")}}, nil
}

func fakeCommand() *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "FLIT_FAKE_WORKER=1")
	return cmd
}

func newSupervisor(t *testing.T) *Supervisor {
	t.Helper()
	var mu sync.Mutex
	s := &Supervisor{Command: fakeCommand, Spec: LoadSpec{ASRModel: "m"}, Logf: func(f string, a ...any) {
		mu.Lock()
		defer mu.Unlock()
		t.Logf(f, a...)
	}}
	t.Cleanup(s.Close)
	return s
}

func TestServeRoundTrip(t *testing.T) {
	var in, out bytes.Buffer
	writeFrame(&in, Request{ID: 7, Op: OpTranscribe, Options: &pipeline.Options{Vocabulary: []string{"flit"}}, Payload: 12}, pcmBytes([]float32{1, 1, 1}))
	writeFrame(&in, Request{ID: 8, Op: "nope"}, nil)
	if err := Serve(&in, &out, fakeBackend{}); err != nil {
		t.Fatal(err)
	}
	var r1, r2 Response
	if err := readHeader(&out, &r1); err != nil {
		t.Fatal(err)
	}
	readHeader(&out, &r2)
	if r1.ID != 7 || len(r1.Segments) != 1 || r1.Segments[0].Text != "xxx flit" {
		t.Fatalf("response %+v", r1)
	}
	if r2.ID != 8 || r2.Error == "" {
		t.Fatalf("unknown op answered %+v", r2)
	}
}

func TestSupervisorStartsLoadsAndTranscribes(t *testing.T) {
	s := newSupervisor(t)
	segs, err := s.Transcribe(context.Background(), []float32{2, 1}, pipeline.Options{Language: "de"})
	if err != nil {
		t.Fatal(err)
	}
	if segs[0].Text != "xxx " {
		t.Fatalf("segments %+v", segs)
	}
	if s.Info().Version != "fake" || !s.Running() {
		t.Fatalf("info %+v, running %v", s.Info(), s.Running())
	}
	probs, frame, err := s.VAD(context.Background(), make([]float32, 2048))
	if err != nil || frame != 512 || len(probs) != 4 {
		t.Fatalf("VAD %v %d %v", probs, frame, err)
	}
}

func TestSupervisorEngineErrorKeepsWorker(t *testing.T) {
	s := newSupervisor(t)
	_, err := s.Transcribe(context.Background(), []float32{1}, pipeline.Options{Language: "bad"})
	var ee *ErrEngine
	if !errors.As(err, &ee) || ee.Status != 10 {
		t.Fatalf("err %v", err)
	}
	if !s.Running() {
		t.Fatal("an engine error stopped the worker")
	}
}

func TestSupervisorRestartsAfterCrash(t *testing.T) {
	s := newSupervisor(t)
	if _, err := s.Transcribe(context.Background(), []float32{1}, pipeline.Options{}); err != nil {
		t.Fatal(err)
	}
	// The crash is retried once in a new worker, which crashes too.
	if _, err := s.Transcribe(context.Background(), []float32{1}, pipeline.Options{Language: "crash"}); !errors.Is(err, ErrWorkerGone) {
		t.Fatalf("err %v", err)
	}
	// The next request starts a fresh worker.
	if _, err := s.Transcribe(context.Background(), []float32{1}, pipeline.Options{}); err != nil {
		t.Fatal(err)
	}
}

func TestSupervisorLoadFailure(t *testing.T) {
	s := newSupervisor(t)
	s.Spec.ASRModel = "missing"
	if _, err := s.Transcribe(context.Background(), nil, pipeline.Options{}); err == nil || !strings.Contains(err.Error(), "no such model") {
		t.Fatalf("err %v", err)
	}
	if s.Running() {
		t.Fatal("worker left running after a failed load")
	}
}

func TestSupervisorIdleStop(t *testing.T) {
	s := newSupervisor(t)
	s.IdleTimeout = 100 * time.Millisecond
	if _, err := s.Transcribe(context.Background(), []float32{1}, pipeline.Options{}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for s.Running() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if s.Running() {
		t.Fatal("idle worker still running")
	}
}

func TestSupervisorCancel(t *testing.T) {
	s := newSupervisor(t)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := s.Transcribe(ctx, []float32{1}, pipeline.Options{Language: "hang"})
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 10*time.Second {
		t.Fatalf("err %v after %v", err, time.Since(start))
	}
}

func TestSupervisorDiarizes(t *testing.T) {
	s := newSupervisor(t)
	turns, err := s.Diarize(context.Background(), make([]float32, 4*pipeline.Rate))
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 2 || turns[1] != (pipeline.SpeakerTurn{T0: 2000, T1: 4000, Speaker: 2}) {
		t.Fatalf("%+v", turns)
	}
}

// A limited worker runs inside a job with a hard CPU cap and below-normal
// priority.
func TestSupervisorCPULimit(t *testing.T) {
	s := newSupervisor(t)
	s.CPULimit, s.Background = 20, true
	if _, err := s.Transcribe(context.Background(), []float32{2, 1}, pipeline.Options{Language: "de"}); err != nil {
		t.Fatal(err)
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(s.PID()))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	var inJob int32
	isInJob := windows.NewLazySystemDLL("kernel32.dll").NewProc("IsProcessInJob")
	if r, _, err := isInJob.Call(uintptr(h), 0, uintptr(unsafe.Pointer(&inJob))); r == 0 || inJob == 0 {
		t.Fatalf("not in a job: %v", err)
	}
	if pc, err := windows.GetPriorityClass(h); err != nil || pc != windows.BELOW_NORMAL_PRIORITY_CLASS {
		t.Fatalf("priority %#x %v", pc, err)
	}
}

func TestSupervisorGPUFallbackOnLoad(t *testing.T) {
	for _, model := range []string{"gpu-load-fail", "gpu-load-crash"} {
		t.Run(model, func(t *testing.T) {
			s := newSupervisor(t)
			s.Spec.ASRModel = model
			s.Spec.CPUOnly = false
			segs, err := s.Transcribe(context.Background(), []float32{2}, pipeline.Options{Language: "de"})
			if err != nil {
				t.Fatalf("expected CPU fallback, got %v", err)
			}
			if len(segs) != 1 || segs[0].Text != "xx " {
				t.Fatalf("segments %+v", segs)
			}
			info := s.Info()
			if info.Backend != "cpu" || info.OnGPU() || info.FallbackReason == "" {
				t.Fatalf("expected cpu fallback info, got %+v", info)
			}
		})
	}
}

func TestSupervisorGPUFallbackOnBackendError(t *testing.T) {
	s := newSupervisor(t)
	s.Spec.CPUOnly = false
	if _, err := s.Transcribe(context.Background(), []float32{1}, pipeline.Options{Language: "de"}); err != nil {
		t.Fatal(err)
	}
	if !s.Info().OnGPU() {
		t.Fatalf("expected GPU first, got %+v", s.Info())
	}
	// A backend error triggers fallback to a CPU-only worker and retries.
	// In fakeBackend, "gpu-err" always errors, so it retries once on CPU and returns the error.
	if _, err := s.Transcribe(context.Background(), []float32{1}, pipeline.Options{Language: "gpu-err"}); err == nil {
		t.Fatal("expected error from always-failing fake op")
	}
	if s.Info().Backend != "cpu" || s.Info().OnGPU() || s.Info().FallbackReason == "" {
		t.Fatalf("expected fallback to CPU after backend error, got %+v", s.Info())
	}
}
