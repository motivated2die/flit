package tcpp

import (
	"errors"
	"runtime"
	"sort"
	"unsafe"
)

// Diarizer is a diarization session (who spoke when) on a loaded DIARIZE
// model: Sortformer, up to MaxSpeakers speakers numbered by first
// appearance.
type Diarizer struct {
	m           *Model
	ptr         uintptr
	SampleRate  int
	MaxSpeakers int
	sortformer  bool // the model takes the Sortformer operating point
	preset      int32
}

// SpeakerTurn is a stretch of one speaker's speech, in milliseconds.
// Turns of different speakers may overlap.
type SpeakerTurn struct {
	T0, T1  int64
	Speaker int // 1-based, in order of first appearance
}

// Sortformer operating points (include/transcribe/sortformer.h).
const (
	sortformerVeryHighLatency = 1 // ~30 s lookahead: the offline-file point
	extSlotDiarizeRun         = 2
	extKindSortformerDiarize  = 0x52444653 // 'SFDR'
)

// NewDiarizer opens a diarization session on m.
func (m *Model) NewDiarizer(threads int) (*Diarizer, error) {
	l := m.lib
	var info diarizeInfo
	l.p.diarInfoInit.call(uintptr(unsafe.Pointer(&info)))
	if err := l.status(l.p.diarGetInfo.call(m.ptr, uintptr(unsafe.Pointer(&info))), "diarize info"); err != nil {
		return nil, err
	}
	var p diarizeSessionParams
	l.p.diarSessionParamsInit.call(uintptr(unsafe.Pointer(&p)))
	if threads > 0 {
		p.NThreads = int32(threads)
	}
	var ptr uintptr
	if err := l.status(l.p.diarSessionInit.call(m.ptr, uintptr(unsafe.Pointer(&p)), uintptr(unsafe.Pointer(&ptr))), "diarize session"); err != nil {
		return nil, err
	}
	d := &Diarizer{m: m, ptr: ptr, SampleRate: int(info.SampleRate), MaxSpeakers: int(info.MaxSpeakers), preset: sortformerVeryHighLatency}
	d.sortformer = cbool(l.p.acceptsExtKind.call(m.ptr, extSlotDiarizeRun, extKindSortformerDiarize))
	return d, nil
}

// Close frees the session.
func (d *Diarizer) Close() {
	if d.ptr != 0 {
		d.m.lib.p.diarSessionFree.call(d.ptr)
		d.ptr = 0
	}
}

// Run diarizes one whole recording of 16 kHz mono PCM in a single pass, so
// speaker numbers hold across all of it. Sortformer runs at its offline
// operating point (~30 s lookahead), the one its published accuracy is
// measured at. Turns come back in time order.
func (d *Diarizer) Run(pcm []float32) ([]SpeakerTurn, error) {
	if len(pcm) == 0 {
		return nil, nil
	}
	l := d.m.lib
	var p diarizeParams
	l.p.diarParamsInit.call(uintptr(unsafe.Pointer(&p)))
	var sf sortformerExt
	if d.sortformer {
		l.p.sortformerExtInit.call(uintptr(unsafe.Pointer(&sf)))
		if sf.Ext.Size != uint64(unsafe.Sizeof(sf)) {
			return nil, errors.New("transcribe.dll: Sortformer extension size differs from the binding's")
		}
		sf.Preset = d.preset
		p.Family = uintptr(unsafe.Pointer(&sf))
	}
	d.m.runs.Lock()
	defer d.m.runs.Unlock()
	err := l.status(l.p.diarRun.call(d.ptr, uintptr(unsafe.Pointer(&pcm[0])), uintptr(len(pcm)), uintptr(unsafe.Pointer(&p))), "diarize run")
	runtime.KeepAlive(pcm)
	runtime.KeepAlive(&sf)
	if err != nil {
		return nil, err
	}
	n := int(int32(l.p.diarNSegments.call(d.ptr)))
	turns := make([]SpeakerTurn, 0, n)
	for i := 0; i < n; i++ {
		var s speakerSegment
		l.p.speakerSegmentInit.call(uintptr(unsafe.Pointer(&s)))
		if l.p.diarGetSegment.call(d.ptr, uintptr(i), uintptr(unsafe.Pointer(&s))) != StatusOK || s.SpeakerID <= 0 {
			continue
		}
		turns = append(turns, SpeakerTurn{T0: s.T0, T1: s.T1, Speaker: int(s.SpeakerID)})
	}
	// The engine groups turns by speaker; callers want them in time.
	sort.SliceStable(turns, func(i, j int) bool { return turns[i].T0 < turns[j].T0 })
	return turns, nil
}
