package llm

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/motivated2die/flit/internal/engine"
)

// Device is where the model runs.
type Device int

const (
	// CPU only: no graphics device is touched.
	CPU Device = iota
	// GPUPrefill keeps the weights in system memory but lets the graphics
	// processor take the large matrix products of reading the prompt
	// (-ngl 0); "often a good setting for integrated GPUs" (llama.cpp
	// discussion #10879).
	GPUPrefill
	// GPU puts every layer on the graphics processor.
	GPU
)

func (d Device) String() string {
	switch d {
	case GPUPrefill:
		return "gpu-prefill"
	case GPU:
		return "gpu"
	}
	return "cpu"
}

// ParseDevice reads a Device's String.
func ParseDevice(s string) (Device, error) {
	for _, d := range []Device{CPU, GPUPrefill, GPU} {
		if d.String() == s {
			return d, nil
		}
	}
	return CPU, fmt.Errorf("unknown device %q (cpu, gpu-prefill, gpu)", s)
}

// Options start a server.
type Options struct {
	Exe     string // llama-server.exe
	Model   string // .gguf
	Context int    // tokens; 0: 8192
	Threads int    // 0: llama.cpp's default
	Device  Device
	// CPULimit and Background: as the speech worker (ADR 0009).
	CPULimit   int
	Background bool
	// Logf gets the server's start-up failures (never prompts: the server
	// runs without verbose logging, which is what would print them).
	Logf func(format string, args ...any)
	// LoadTimeout bounds loading the model (0: two minutes).
	LoadTimeout time.Duration
}

// Server is a running llama-server.
type Server struct {
	Client
	Options Options
	Loaded  time.Duration // how long loading the model took

	cmd     *exec.Cmd
	release func()
	exited  chan struct{}
	once    sync.Once
	tail    *lineTail
}

// Start launches llama-server on the loopback interface with a fresh port
// and key, and waits until the model is loaded.
func Start(ctx context.Context, o Options) (*Server, error) {
	if o.Context <= 0 {
		o.Context = 8192
	}
	if o.LoadTimeout <= 0 {
		o.LoadTimeout = 2 * time.Minute
	}
	port, err := freePort()
	if err != nil {
		return nil, err
	}
	key := randomKey()
	args := []string{
		"-m", o.Model,
		"--host", "127.0.0.1", "--port", strconv.Itoa(port),
		"--api-key", key,
		"--no-webui",
		"-c", strconv.Itoa(o.Context),
		"-np", "1", // one request at a time: one summary, one slot, its cache kept
		"--reasoning", "off",
		"--log-verbosity", "2", // warnings and errors, never prompts
		"--log-colors", "off",
	}
	if o.Threads > 0 {
		args = append(args, "-t", strconv.Itoa(o.Threads))
	}
	switch o.Device {
	case CPU:
		args = append(args, "--device", "none", "-ngl", "0")
	case GPUPrefill:
		args = append(args, "-ngl", "0")
	case GPU:
		args = append(args, "-ngl", "999")
	}
	cmd := exec.Command(o.Exe, args...)
	engine.HideWindow(cmd)
	tail := &lineTail{max: 30}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	s := &Server{
		Client:  Client{BaseURL: fmt.Sprintf("http://127.0.0.1:%d", port), Key: key, HTTP: &http.Client{}},
		Options: o, cmd: cmd, exited: make(chan struct{}), tail: tail, release: func() {},
	}
	go func() { tail.read(out); cmd.Wait(); close(s.exited) }()
	if rel, err := jobProcess(cmd.Process.Pid, o.CPULimit, o.Background); err != nil {
		s.logf("llm: the server's job object: %v", err)
	} else {
		s.release = rel
	}
	start := time.Now()
	deadline := time.NewTimer(o.LoadTimeout)
	defer deadline.Stop()
	tick := time.NewTicker(150 * time.Millisecond)
	defer tick.Stop()
	for {
		hctx, cancel := context.WithTimeout(ctx, time.Second)
		ok := s.Healthy(hctx)
		cancel()
		if ok {
			s.Loaded = time.Since(start)
			return s, nil
		}
		select {
		case <-ctx.Done():
			s.Close()
			return nil, ctx.Err()
		case <-s.exited:
			s.logTail("llm: the server ended while loading")
			s.Close()
			if strings.Contains(s.tail.String(), "ErrorDeviceLost") || strings.Contains(s.tail.String(), "vk::") {
				return nil, ErrGPU
			}
			return nil, errors.New("the language model server ended while loading")
		case <-deadline.C:
			s.logTail("llm: the server didn't finish loading")
			s.Close()
			return nil, fmt.Errorf("the language model didn't load within %s", o.LoadTimeout)
		case <-tick.C:
		}
	}
}

// ErrGPU means the graphics processor couldn't be used (Vulkan failed to
// create the device, or lost it while loading).
var ErrGPU = errors.New("the graphics processor couldn't be used")

// StartFallback starts on o.Device and, when a graphics device fails to
// load or the server ends while loading on it, once more on the CPU. The
// returned server's Options.Device says where it runs; the error of the
// first try is returned alongside when the fallback was taken.
func StartFallback(ctx context.Context, o Options) (*Server, error, error) {
	s, err := Start(ctx, o)
	if err == nil || o.Device == CPU || ctx.Err() != nil {
		return s, err, nil
	}
	first := err
	o.Device = CPU
	s, err = Start(ctx, o)
	return s, err, first
}

// PID is the server's process ID.
func (s *Server) PID() int { return s.cmd.Process.Pid }

// Exited is closed when the server process has ended.
func (s *Server) Exited() <-chan struct{} { return s.exited }

// Close stops the server and waits for it to end.
func (s *Server) Close() {
	s.once.Do(func() {
		select {
		case <-s.exited:
		default:
			s.cmd.Process.Kill()
			<-s.exited
		}
		s.release()
	})
}

func (s *Server) logf(format string, args ...any) {
	if s.Options.Logf != nil {
		s.Options.Logf(format, args...)
	}
}

func (s *Server) logTail(what string) {
	s.logf("%s; its last lines:\n%s", what, s.tail.String())
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func randomKey() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// lineTail keeps the last lines of the server's output, for errors.
type lineTail struct {
	mu    sync.Mutex
	lines []string
	max   int
}

func (t *lineTail) read(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		t.mu.Lock()
		t.lines = append(t.lines, sc.Text())
		if len(t.lines) > t.max {
			t.lines = t.lines[1:]
		}
		t.mu.Unlock()
	}
	io.Copy(io.Discard, r) // after an overlong line: keep the pipe from filling
}

func (t *lineTail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.Join(t.lines, "\n")
}
