// Package engine runs the speech engine in a worker process and talks to it
// (docs/decisions/0001-stay-with-go.md). The tray process never loads the
// engine: the Supervisor starts `flit.exe --engine-worker` when there is
// work, sends it requests over its stdin and reads answers from its stdout,
// restarts it after a crash, and lets it exit when idle, which returns all
// of the model's memory.
//
// Each message is a frame: a 4-byte little-endian length, a JSON header of
// that length, then the header's Payload bytes (raw float32 PCM).
package engine

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"unsafe"

	"github.com/motivated2die/flit/internal/pipeline"
)

// Ops.
const (
	OpLoad       = "load"
	OpVAD        = "vad"
	OpTranscribe = "transcribe"
	OpDiarize    = "diarize"
)

// Request is a message to the worker.
type Request struct {
	ID      uint64            `json:"id"`
	Op      string            `json:"op"`
	Load    *LoadSpec         `json:"load,omitempty"`
	Options *pipeline.Options `json:"options,omitempty"`
	Payload int               `json:"payload,omitempty"` // bytes of PCM after the header
}

// LoadSpec says which models the worker loads.
type LoadSpec struct {
	EngineDir string `json:"engineDir"` // folder with transcribe.dll
	ASRModel  string `json:"asrModel,omitempty"`
	VADModel  string `json:"vadModel,omitempty"`
	DiarModel string `json:"diarModel,omitempty"` // loaded on the first diarize request
	CPUOnly   bool   `json:"cpuOnly,omitempty"`
	Threads   int    `json:"threads,omitempty"`
}

// Response is the worker's answer to the request with the same ID.
type Response struct {
	ID       uint64                 `json:"id"`
	Error    string                 `json:"error,omitempty"`
	Status   int                    `json:"status,omitempty"` // engine status for an engine error
	Frame    int                    `json:"frame,omitempty"`
	Probs    []float32              `json:"probs,omitempty"`
	Segments []pipeline.Segment     `json:"segments,omitempty"`
	Turns    []pipeline.SpeakerTurn `json:"turns,omitempty"`
	Info     *Info                  `json:"info,omitempty"`
}

// Info describes the loaded engine.
type Info struct {
	Version        string   `json:"version"`
	Languages      []string `json:"languages,omitempty"`
	Vocabulary     bool     `json:"vocabulary"`
	MaxAudioMS     int64    `json:"maxAudioMs,omitempty"`
	Backend        string   `json:"backend,omitempty"`        // "vulkan", "cpu", ...
	DeviceName     string   `json:"deviceName,omitempty"`     // "Vulkan0", "CPU"
	DeviceDesc     string   `json:"deviceDesc,omitempty"`     // e.g. "AMD Radeon RX 7800 XT"
	DeviceType     string   `json:"deviceType,omitempty"`     // "gpu", "igpu", "cpu"
	DiarBackend    string   `json:"diarBackend,omitempty"`    // resolved backend for Sortformer
	DiarDeviceDesc string   `json:"diarDeviceDesc,omitempty"` // resolved device for Sortformer
	FallbackReason string   `json:"fallbackReason,omitempty"` // set when GPU failed and fell back to CPU
}

// OnGPU reports whether the loaded ASR or diarization model runs on a GPU.
func (i Info) OnGPU() bool {
	return (i.Backend != "" && i.Backend != "cpu") || (i.DiarBackend != "" && i.DiarBackend != "cpu")
}

const maxHeader = 64 << 20 // VAD probabilities of hours of audio stay well below

// writeFrame writes v as a header followed by payload.
func writeFrame(w io.Writer, v any, payload []byte) error {
	h, err := json.Marshal(v)
	if err != nil {
		return err
	}
	var n [4]byte
	binary.LittleEndian.PutUint32(n[:], uint32(len(h)))
	if _, err := w.Write(n[:]); err != nil {
		return err
	}
	if _, err := w.Write(h); err != nil {
		return err
	}
	if len(payload) > 0 {
		_, err = w.Write(payload)
	}
	return err
}

// readHeader reads a frame's header into v.
func readHeader(r io.Reader, v any) error {
	var n [4]byte
	if _, err := io.ReadFull(r, n[:]); err != nil {
		return err
	}
	size := binary.LittleEndian.Uint32(n[:])
	if size > maxHeader {
		return fmt.Errorf("engine protocol: header of %d bytes", size)
	}
	h := make([]byte, size)
	if _, err := io.ReadFull(r, h); err != nil {
		return err
	}
	return json.Unmarshal(h, v)
}

// readPCM reads a payload of n bytes as float32 samples.
func readPCM(r io.Reader, n int) ([]float32, error) {
	if n%4 != 0 || n < 0 || n > math.MaxInt32 {
		return nil, fmt.Errorf("engine protocol: payload of %d bytes", n)
	}
	pcm := make([]float32, n/4)
	if n == 0 {
		return pcm, nil
	}
	_, err := io.ReadFull(r, unsafe.Slice((*byte)(unsafe.Pointer(&pcm[0])), n))
	return pcm, err
}

// pcmBytes views samples as bytes (little-endian on every platform flit
// runs on).
func pcmBytes(pcm []float32) []byte {
	if len(pcm) == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(&pcm[0])), 4*len(pcm))
}

// ErrEngine is an error the engine itself reported (as opposed to the
// worker crashing or the protocol failing).
type ErrEngine struct {
	Msg    string
	Status int
}

func (e *ErrEngine) Error() string { return e.Msg }

// ErrWorkerGone means the worker process ended while a request was open.
var ErrWorkerGone = errors.New("engine worker exited")
