package engine

import (
	"errors"
	"io"

	"github.com/motivated2die/flit/internal/pipeline"
)

// Backend is what the worker runs requests on: the real engine
// (tcpp_backend_windows.go) or a fake in tests.
type Backend interface {
	Load(spec LoadSpec) (Info, error)
	VAD(pcm []float32) (probs []float32, frame int, err error)
	Transcribe(pcm []float32, opt pipeline.Options) ([]pipeline.Segment, error)
	Diarize(pcm []float32) ([]pipeline.SpeakerTurn, error)
}

// StatusError lets a backend report the engine's status code.
type StatusError interface {
	error
	EngineStatus() int
}

// Serve answers requests from r on w until r ends (the supervisor closed
// the pipe). Requests are handled one at a time, as the engine allows.
func Serve(r io.Reader, w io.Writer, b Backend) error {
	for {
		var req Request
		if err := readHeader(r, &req); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		pcm, err := readPCM(r, req.Payload)
		if err != nil {
			return err
		}
		resp := handle(b, req, pcm)
		if err := writeFrame(w, resp, nil); err != nil {
			return err
		}
	}
}

func handle(b Backend, req Request, pcm []float32) (resp Response) {
	resp.ID = req.ID
	fail := func(err error) Response {
		resp.Error = err.Error()
		var se StatusError
		if errors.As(err, &se) {
			resp.Status = se.EngineStatus()
		}
		return resp
	}
	switch req.Op {
	case OpLoad:
		if req.Load == nil {
			return fail(errors.New("load without spec"))
		}
		info, err := b.Load(*req.Load)
		if err != nil {
			return fail(err)
		}
		resp.Info = &info
	case OpVAD:
		probs, frame, err := b.VAD(pcm)
		if err != nil {
			return fail(err)
		}
		resp.Probs, resp.Frame = probs, frame
	case OpTranscribe:
		var opt pipeline.Options
		if req.Options != nil {
			opt = *req.Options
		}
		segs, err := b.Transcribe(pcm, opt)
		if err != nil {
			return fail(err)
		}
		resp.Segments = segs
	case OpDiarize:
		turns, err := b.Diarize(pcm)
		if err != nil {
			return fail(err)
		}
		resp.Turns = turns
	default:
		return fail(errors.New("unknown op " + req.Op))
	}
	return resp
}
