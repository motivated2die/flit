package capture

import (
	"encoding/binary"
	"errors"
	"math"
	"path/filepath"
	"testing"

	"github.com/motivated2die/flit/internal/audio/wav"
)

func TestParseWaveFormat(t *testing.T) {
	f, err := ParseWaveFormat(FloatStereo48k())
	if err != nil || f != (Format{Rate: 48000, Channels: 2, Bits: 32, Float: true, BlockAlign: 8}) {
		t.Fatalf("%+v %v", f, err)
	}
	// WAVEFORMATEXTENSIBLE with a PCM subformat, 24 bits in 3 bytes.
	b := make([]byte, 40)
	binary.LittleEndian.PutUint16(b[0:], tagExtensible)
	binary.LittleEndian.PutUint16(b[2:], 2)
	binary.LittleEndian.PutUint32(b[4:], 44100)
	binary.LittleEndian.PutUint16(b[12:], 6)
	binary.LittleEndian.PutUint16(b[14:], 24)
	binary.LittleEndian.PutUint16(b[24:], tagPCM)
	if f, err := ParseWaveFormat(b); err != nil || f.Float || f.Bits != 24 || f.Rate != 44100 {
		t.Fatalf("extensible: %+v %v", f, err)
	}
	binary.LittleEndian.PutUint16(b[14:], 8)
	binary.LittleEndian.PutUint16(b[12:], 2)
	if _, err := ParseWaveFormat(b); !errors.Is(err, ErrFormat) {
		t.Fatalf("8-bit accepted: %v", err)
	}
}

func TestToMono(t *testing.T) {
	// Float stereo: (0.5, -0.5) and (1, 0.5).
	b := make([]byte, 16)
	for i, v := range []float32{0.5, -0.5, 1, 0.5} {
		binary.LittleEndian.PutUint32(b[4*i:], math.Float32bits(v))
	}
	got := ToMono(nil, b, Format{Rate: 48000, Channels: 2, Bits: 32, Float: true, BlockAlign: 8})
	if len(got) != 2 || got[0] != 0 || got[1] != 0.75 {
		t.Fatalf("float: %v", got)
	}
	// 16-bit mono.
	b16 := []byte{0x00, 0x40, 0x00, 0xC0}
	got = ToMono(nil, b16, Format{Rate: 16000, Channels: 1, Bits: 16, BlockAlign: 2})
	if got[0] != 0.5 || got[1] != -0.5 {
		t.Fatalf("int16: %v", got)
	}
	// 24-bit stereo, both channels 0.5.
	b24 := []byte{0, 0, 0x40, 0, 0, 0x40}
	got = ToMono(nil, b24, Format{Rate: 48000, Channels: 2, Bits: 24, BlockAlign: 6})
	if math.Abs(float64(got[0])-0.5) > 1e-6 {
		t.Fatalf("int24: %v", got)
	}
}

const tick = 10_000_000 // 100 ns units per second

func TestAligner(t *testing.T) {
	a := Aligner{Rate: 1000, Origin: 5 * tick}
	// First packet 1 s after the origin: 1000 frames of silence first.
	if s, k := a.Place(6*tick, 100); s != 1000 || k != 0 {
		t.Fatalf("first: %d %d", s, k)
	}
	// Next packet on time (within jitter): as is.
	if s, k := a.Place(6*tick+tick/10+tick/1000*5, 100); s != 0 || k != 0 {
		t.Fatalf("jitter: %d %d", s, k)
	}
	// Loopback was silent: 2 s gap.
	if s, _ := a.Place(8*tick+tick/5, 100); s != 2000 { // written 1.2 s, packet at 3.2 s
		t.Fatalf("gap: %d", s)
	}
	// A packet claiming to start 50 ms back (overlap): trimmed.
	at := 5*tick + a.Written()*tick/1000 - tick/20
	if s, k := a.Place(at, 100); s != 0 || k != 50 {
		t.Fatalf("overlap: %d %d", s, k)
	}
	// A packet entirely from before the origin is dropped.
	b := Aligner{Rate: 1000, Origin: 5 * tick}
	if s, k := b.Place(4*tick, 100); s != 0 || k != 100 || b.Written() != 0 {
		t.Fatalf("before origin: %d %d %d", s, k, b.Written())
	}
}

func TestAlignerPadAndRebase(t *testing.T) {
	a := Aligner{Rate: 48000}
	a.Place(0, 4800)
	if n := a.PadTo(tick); n != 43200 {
		t.Fatalf("pad %d", n)
	}
	if a.PadTo(tick/2) != 0 {
		t.Fatal("padded backwards")
	}
	a.Rebase(16000)
	if a.Written() != 16000 {
		t.Fatalf("rebased to %d", a.Written())
	}
}

func TestTrackResamplesAndWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "me.wav")
	tr, err := NewTrack(path)
	if err != nil {
		t.Fatal(err)
	}
	var tapped int
	tr.Tap = func(p []float32) { tapped += len(p) }
	tone := make([]float32, 4800)
	for i := range tone {
		tone[i] = float32(0.5 * math.Sin(2*math.Pi*440*float64(i)/48000))
	}
	for i := 0; i < 10; i++ { // 1 s at 48 kHz
		tr.Write(48000, tone)
	}
	if p := tr.TakePeak(); p < 0.49 || p > 0.51 {
		t.Fatalf("peak %v", p)
	}
	tr.Write(16000, make([]float32, 8000)) // device reopened at 16 kHz: 0.5 s more
	if err := tr.Close(); err != nil {
		t.Fatal(err)
	}
	a, err := wav.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if a.SampleRate != Rate || abs(a.Frames()-24000) > 2 || tapped != a.Frames() {
		t.Fatalf("%d frames (tapped %d), want 24000", a.Frames(), tapped)
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func TestLevelAndMaxPeak(t *testing.T) {
	var l Level
	l.Write(48000, []float32{0.1, -0.6, 0.2})
	l.Write(48000, []float32{0.3})
	if p := l.TakePeak(); p != 0.6 {
		t.Fatalf("peak %v", p)
	}
	if p := l.TakePeak(); p != 0 {
		t.Fatalf("peak after take %v", p)
	}
	tr, _ := NewTrack(filepath.Join(t.TempDir(), "x.wav"))
	tr.Write(16000, []float32{0.25, -0.5})
	tr.TakePeak()
	tr.Write(16000, []float32{0.1})
	if tr.MaxPeak() != 0.5 {
		t.Fatalf("max %v", tr.MaxPeak())
	}
	tr.Close()
}
