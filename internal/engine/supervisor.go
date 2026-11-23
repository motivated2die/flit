package engine

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/motivated2die/flit/internal/pipeline"
)

// WorkerFlag is the first argument that makes flit.exe an engine worker.
const WorkerFlag = "--engine-worker"

// Supervisor owns the engine worker process and implements pipeline.Engine.
// Requests are serialised (the engine runs one at a time anyway). The
// worker starts on the first request, loads Spec, and exits after
// IdleTimeout without requests. A worker that dies mid-request is
// restarted once and the request retried.
type Supervisor struct {
	Command     func() *exec.Cmd // how to start a worker
	Spec        LoadSpec
	IdleTimeout time.Duration
	Logf        func(format string, args ...any) // worker stderr and lifecycle
	// CPULimit caps each worker at this share of all processors (percent,
	// enforced by Windows; 0 = none); Background runs it below normal
	// priority. For transcribing during a meeting.
	CPULimit   int
	Background bool

	mu             sync.Mutex
	w              *worker
	nextID         uint64
	idle           *time.Timer
	gpuFailed      bool
	fallbackReason string

	// Readable while a request holds mu (the debug view polls them).
	pid  atomic.Int64
	info atomic.Pointer[Info]
}

type worker struct {
	cmd     *exec.Cmd
	in      io.WriteCloser
	out     *bufio.Reader
	done    chan struct{}
	release func() // the CPU limit's job object
}

// Info returns what the last loaded worker reported.
func (s *Supervisor) Info() Info {
	if p := s.info.Load(); p != nil {
		return *p
	}
	return Info{}
}

// VAD implements pipeline.Engine.
func (s *Supervisor) VAD(ctx context.Context, pcm []float32) ([]float32, int, error) {
	resp, err := s.do(ctx, Request{Op: OpVAD}, pcm)
	if err != nil {
		return nil, 0, err
	}
	return resp.Probs, resp.Frame, nil
}

// Transcribe implements pipeline.Engine.
func (s *Supervisor) Transcribe(ctx context.Context, pcm []float32, opt pipeline.Options) ([]pipeline.Segment, error) {
	resp, err := s.do(ctx, Request{Op: OpTranscribe, Options: &opt}, pcm)
	if err != nil {
		return nil, err
	}
	return resp.Segments, nil
}

// Diarize implements pipeline.Diarizer.
func (s *Supervisor) Diarize(ctx context.Context, pcm []float32) ([]pipeline.SpeakerTurn, error) {
	resp, err := s.do(ctx, Request{Op: OpDiarize}, pcm)
	if err != nil {
		return nil, err
	}
	return resp.Turns, nil
}

// Engine status codes that indicate a GPU backend or memory failure
// recoverable by restarting on the CPU (matching tcpp.StatusOOM/StatusBackend).
const (
	statusOOM     = 7
	statusBackend = 8
)

func isGPUFallbackError(err error) bool {
	if errors.Is(err, ErrWorkerGone) {
		return true
	}
	var ee *ErrEngine
	return errors.As(err, &ee) && (ee.Status == statusBackend || ee.Status == statusOOM)
}

func (s *Supervisor) effectiveSpec() LoadSpec {
	spec := s.Spec
	if s.gpuFailed {
		spec.CPUOnly = true
	}
	return spec
}

func (s *Supervisor) storeInfo(info Info) {
	if s.gpuFailed && info.FallbackReason == "" {
		info.FallbackReason = s.fallbackReason
	}
	s.info.Store(&info)
}

// do sends one request, starting the worker if needed; a crash is retried
// once in a fresh worker. When a GPU worker fails to load, crashes, or hits
// a backend/OOM error, it falls back to a CPU-only worker for this supervisor.
func (s *Supervisor) do(ctx context.Context, req Request, pcm []float32) (Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.idle != nil {
		s.idle.Stop()
	}
	defer s.armIdle()
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return Response{}, err
		}
		if err := s.ensure(); err != nil {
			return Response{}, err
		}
		resp, err := s.roundTrip(ctx, req, pcm)
		if err == nil {
			if resp.Info != nil {
				s.storeInfo(*resp.Info)
			}
			return resp, nil
		}
		if !s.effectiveSpec().CPUOnly && isGPUFallbackError(err) {
			s.logf("engine: the graphics processor failed during %s (%v); falling back to the processor", req.Op, err)
			s.stopLocked()
			s.gpuFailed, s.fallbackReason = true, err.Error()
			continue
		}
		if errors.Is(err, ErrWorkerGone) && attempt == 0 {
			s.logf("engine worker died during %s; restarting", req.Op)
			continue
		}
		return resp, err
	}
}

func (s *Supervisor) roundTrip(ctx context.Context, req Request, pcm []float32) (Response, error) {
	w := s.w
	s.nextID++
	req.ID = s.nextID
	req.Payload = 4 * len(pcm)
	type result struct {
		resp Response
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		if err := writeFrame(w.in, req, pcmBytes(pcm)); err != nil {
			ch <- result{err: ErrWorkerGone}
			return
		}
		var resp Response
		if err := readHeader(w.out, &resp); err != nil {
			ch <- result{err: ErrWorkerGone}
			return
		}
		ch <- result{resp: resp}
	}()
	var r result
	select {
	case r = <-ch:
	case <-ctx.Done():
		// The engine can't be interrupted from here mid-run (yet); stop
		// the worker, which frees everything, and start fresh next time.
		s.stopLocked()
		<-ch
		return Response{}, ctx.Err()
	}
	if r.err != nil {
		s.stopLocked()
		return Response{}, r.err
	}
	if r.resp.ID != req.ID {
		s.stopLocked()
		return Response{}, fmt.Errorf("engine protocol: answer %d to request %d", r.resp.ID, req.ID)
	}
	if r.resp.Error != "" {
		return r.resp, &ErrEngine{Msg: r.resp.Error, Status: r.resp.Status}
	}
	return r.resp, nil
}

// ensure starts a worker and loads the models, unless one is running.
func (s *Supervisor) ensure() error {
	if s.w != nil {
		select {
		case <-s.w.done:
			s.w = nil
		default:
			return nil
		}
	}
	cmd := s.Command()
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting engine worker: %w", err)
	}
	w := &worker{cmd: cmd, in: in, out: bufio.NewReaderSize(out, 1<<20), done: make(chan struct{}), release: func() {}}
	if s.CPULimit > 0 || s.Background {
		rel, err := LimitProcess(cmd.Process.Pid, s.CPULimit, s.Background)
		if err != nil {
			s.logf("engine worker: limiting its CPU: %v", err)
		}
		w.release = rel
	}
	go func() {
		sc := bufio.NewScanner(stderr)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			s.logf("engine: %s", sc.Text())
		}
	}()
	go func() {
		err := cmd.Wait()
		if err != nil {
			s.logf("engine worker exited: %v", err)
		}
		s.pid.CompareAndSwap(int64(cmd.Process.Pid), 0)
		w.release()
		close(w.done)
	}()
	s.w = w
	s.pid.Store(int64(cmd.Process.Pid))
	s.logf("engine worker started (pid %d)", cmd.Process.Pid)

	spec := s.effectiveSpec()
	resp, err := s.roundTrip(context.Background(), Request{Op: OpLoad, Load: &spec}, nil)
	if err != nil {
		s.stopLocked()
		if !spec.CPUOnly {
			s.logf("engine: the graphics processor failed while loading (%v); falling back to the processor", err)
			s.gpuFailed, s.fallbackReason = true, err.Error()
			return s.ensure()
		}
		return fmt.Errorf("loading the engine: %w", err)
	}
	if resp.Info != nil {
		s.storeInfo(*resp.Info)
	}
	return nil
}

func (s *Supervisor) armIdle() {
	if s.IdleTimeout <= 0 || s.w == nil {
		return
	}
	if s.idle == nil {
		s.idle = time.AfterFunc(s.IdleTimeout, func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.w != nil {
				s.logf("engine idle: stopping the worker")
				s.stopLocked()
			}
		})
		return
	}
	s.idle.Reset(s.IdleTimeout)
}

// Close stops the worker.
func (s *Supervisor) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.idle != nil {
		s.idle.Stop()
	}
	s.stopLocked()
}

// PID is the worker's process ID, 0 when none runs. It doesn't wait for a
// request in progress.
func (s *Supervisor) PID() int { return int(s.pid.Load()) }

// Running reports whether a worker process is alive.
func (s *Supervisor) Running() bool { return s.PID() != 0 }

// stopLocked closes the worker's stdin (it exits when its input ends) and
// kills it if it hasn't after a few seconds.
func (s *Supervisor) stopLocked() {
	w := s.w
	if w == nil {
		return
	}
	s.w = nil
	s.pid.Store(0)
	w.in.Close()
	select {
	case <-w.done:
	case <-time.After(3 * time.Second):
		_ = w.cmd.Process.Kill()
		<-w.done
	}
}

func (s *Supervisor) logf(format string, args ...any) {
	if s.Logf != nil {
		s.Logf(format, args...)
	}
}

// WorkerCommand returns how to start this executable as a worker.
func WorkerCommand(exe string) func() *exec.Cmd {
	return func() *exec.Cmd {
		cmd := exec.Command(exe, WorkerFlag)
		cmd.Env = append(os.Environ(), "VK_LOADER_LAYERS_DISABLE=~implicit~")
		HideWindow(cmd)
		return cmd
	}
}
