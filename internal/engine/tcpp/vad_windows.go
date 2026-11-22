package tcpp

import (
	"runtime"
	"unsafe"
)

// VAD is a Silero VAD session on a loaded VAD model.
type VAD struct {
	m            *Model
	ptr          uintptr
	FrameSamples int // samples per probability frame (512 = 32 ms at 16 kHz)
	SampleRate   int
}

// VADParams tune segment detection; zero values take the engine's defaults.
type VADParams struct {
	Threshold, NegThreshold   float64
	MinSpeechMS, MinSilenceMS int
	SpeechPadMS, MaxSpeechMS  int
}

// VADResult holds per-frame speech probabilities and speech segments
// (sample ranges).
type VADResult struct {
	Probs      []float32
	FirstFrame int64
	Segments   [][2]int64
}

// NewVAD opens a VAD session on m (a silero_vad model).
func (m *Model) NewVAD(threads int) (*VAD, error) {
	l := m.lib
	var info vadInfo
	l.p.vadInfoInit.call(uintptr(unsafe.Pointer(&info)))
	if err := l.status(l.p.vadGetInfo.call(m.ptr, uintptr(unsafe.Pointer(&info))), "vad info"); err != nil {
		return nil, err
	}
	var p vadSessionParams
	l.p.vadSessionParamsInit.call(uintptr(unsafe.Pointer(&p)))
	if threads > 0 {
		p.NThreads = int32(threads)
	}
	var ptr uintptr
	if err := l.status(l.p.vadSessionInit.call(m.ptr, uintptr(unsafe.Pointer(&p)), uintptr(unsafe.Pointer(&ptr))), "vad session"); err != nil {
		return nil, err
	}
	return &VAD{m: m, ptr: ptr, FrameSamples: int(info.FrameSamples), SampleRate: int(info.SampleRate)}, nil
}

// Close frees the session.
func (v *VAD) Close() {
	if v.ptr != 0 {
		v.m.lib.p.vadSessionFree.call(v.ptr)
		v.ptr = 0
	}
}

// Run scores a whole clip of 16 kHz mono PCM.
func (v *VAD) Run(pcm []float32, opt VADParams) (VADResult, error) {
	l := v.m.lib
	var p vadParams
	l.p.vadParamsInit.call(uintptr(unsafe.Pointer(&p)))
	if opt.Threshold > 0 {
		p.Threshold = opt.Threshold
	}
	if opt.NegThreshold > 0 {
		p.NegThreshold = opt.NegThreshold
	}
	if opt.MinSpeechMS > 0 {
		p.MinSpeechMS = int32(opt.MinSpeechMS)
	}
	if opt.MinSilenceMS > 0 {
		p.MinSilenceMS = int32(opt.MinSilenceMS)
	}
	if opt.SpeechPadMS > 0 {
		p.SpeechPadMS = int32(opt.SpeechPadMS)
	}
	if opt.MaxSpeechMS > 0 {
		p.MaxSpeechMS = int32(opt.MaxSpeechMS)
	}
	var pcmPtr uintptr
	if len(pcm) > 0 {
		pcmPtr = uintptr(unsafe.Pointer(&pcm[0]))
	}
	v.m.runs.Lock()
	defer v.m.runs.Unlock()
	err := l.status(l.p.vadRun.call(v.ptr, pcmPtr, uintptr(len(pcm)), uintptr(unsafe.Pointer(&p))), "vad run")
	runtime.KeepAlive(pcm)
	if err != nil {
		return VADResult{}, err
	}
	var r vadResult
	l.p.vadResultInit.call(uintptr(unsafe.Pointer(&r)))
	if err := l.status(l.p.vadGetResult.call(v.ptr, uintptr(unsafe.Pointer(&r))), "vad result"); err != nil {
		return VADResult{}, err
	}
	res := VADResult{FirstFrame: r.FirstFrame}
	if probs := l.p.vadProbs.call(v.ptr); probs != 0 && r.NProbs > 0 {
		res.Probs = append([]float32(nil), unsafe.Slice((*float32)(cptr(probs)), r.NProbs)...)
	}
	for i := 0; i < int(r.NSegments); i++ {
		var s vadSegment
		l.p.vadSegmentInit.call(uintptr(unsafe.Pointer(&s)))
		if l.p.vadGetSegment.call(v.ptr, uintptr(i), uintptr(unsafe.Pointer(&s))) == StatusOK {
			res.Segments = append(res.Segments, [2]int64{s.StartSample, s.EndSample})
		}
	}
	return res, nil
}
