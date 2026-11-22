package wav

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteReadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.wav")
	w, err := Create(path, Format{SampleRate: 16000, Channels: 2})
	if err != nil {
		t.Fatal(err)
	}
	in := []float32{0, 0.5, -0.5, 1, -1, 0.25, 2, -2} // 2 and -2 clip
	if err := w.Write(in[:4]); err != nil {
		t.Fatal(err)
	}
	if err := w.Write(in[4:]); err != nil {
		t.Fatal(err)
	}
	if w.Frames() != 4 {
		t.Fatalf("Frames = %d", w.Frames())
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	a, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if a.SampleRate != 16000 || a.Channels != 2 || a.Frames() != 4 {
		t.Fatalf("got %+v, %d frames", a.Format, a.Frames())
	}
	want := []float32{0, 0.5, -0.5, 1, -1, 0.25, 1, -1}
	for i := range want {
		if math.Abs(float64(a.Samples[i]-want[i])) > 1.0/16384 {
			t.Errorf("sample %d = %v, want %v", i, a.Samples[i], want[i])
		}
	}
}

func TestSyncedFileIsReadableBeforeClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rec.wav")
	w, _ := Create(path, Format{SampleRate: 16000, Channels: 1})
	defer w.Close()
	w.Write(make([]float32, 1600))
	if err := w.Sync(); err != nil {
		t.Fatal(err)
	}
	w.Write(make([]float32, 800)) // buffered, not synced: like a crash after Sync
	a, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if a.Frames() != 1600 {
		t.Fatalf("frames = %d, want the synced 1600", a.Frames())
	}
}

// build makes a WAV with a given fmt chunk and raw data.
func build(tag, channels, bits uint16, rate uint32, extensible bool, data []byte) []byte {
	var fmtc bytes.Buffer
	ftag := tag
	if extensible {
		ftag = formatExtensible
	}
	binary.Write(&fmtc, binary.LittleEndian, ftag)
	binary.Write(&fmtc, binary.LittleEndian, channels)
	binary.Write(&fmtc, binary.LittleEndian, rate)
	binary.Write(&fmtc, binary.LittleEndian, rate*uint32(channels*bits/8))
	binary.Write(&fmtc, binary.LittleEndian, channels*bits/8)
	binary.Write(&fmtc, binary.LittleEndian, bits)
	if extensible {
		binary.Write(&fmtc, binary.LittleEndian, uint16(22))
		binary.Write(&fmtc, binary.LittleEndian, bits)
		binary.Write(&fmtc, binary.LittleEndian, uint32(3))
		binary.Write(&fmtc, binary.LittleEndian, tag)
		fmtc.Write(make([]byte, 14))
	}
	var b bytes.Buffer
	b.WriteString("RIFF")
	binary.Write(&b, binary.LittleEndian, uint32(0))
	b.WriteString("WAVE")
	b.WriteString("LIST") // a chunk to skip
	binary.Write(&b, binary.LittleEndian, uint32(3))
	b.Write([]byte{1, 2, 3, 0}) // odd size + pad byte
	b.WriteString("fmt ")
	binary.Write(&b, binary.LittleEndian, uint32(fmtc.Len()))
	b.Write(fmtc.Bytes())
	b.WriteString("data")
	binary.Write(&b, binary.LittleEndian, uint32(len(data)))
	b.Write(data)
	return b.Bytes()
}

func TestReadFormats(t *testing.T) {
	f32 := make([]byte, 8)
	binary.LittleEndian.PutUint32(f32[0:], math.Float32bits(0.5))
	binary.LittleEndian.PutUint32(f32[4:], math.Float32bits(-0.25))
	cases := []struct {
		name       string
		tag, bits  uint16
		extensible bool
		data       []byte
		want       []float32
	}{
		{"pcm16", formatPCM, 16, false, []byte{0x00, 0x40, 0x00, 0xC0}, []float32{0.5, -0.5}},
		{"pcm24", formatPCM, 24, false, []byte{0, 0, 0x40, 0, 0, 0xC0}, []float32{0.5, -0.5}},
		{"pcm32", formatPCM, 32, false, []byte{0, 0, 0, 0x40, 0, 0, 0, 0xC0}, []float32{0.5, -0.5}},
		{"pcm8", formatPCM, 8, false, []byte{192, 64}, []float32{0.5, -0.5}},
		{"float32", formatFloat, 32, false, f32, []float32{0.5, -0.25}},
		{"float32 extensible", formatFloat, 32, true, f32, []float32{0.5, -0.25}},
	}
	for _, c := range cases {
		a, err := Read(bytes.NewReader(build(c.tag, 1, c.bits, 48000, c.extensible, c.data)))
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if a.SampleRate != 48000 || a.Channels != 1 || len(a.Samples) != len(c.want) {
			t.Errorf("%s: %+v %v", c.name, a.Format, a.Samples)
			continue
		}
		for i, w := range c.want {
			if math.Abs(float64(a.Samples[i]-w)) > 1e-6 {
				t.Errorf("%s: sample %d = %v, want %v", c.name, i, a.Samples[i], w)
			}
		}
	}
}

func TestReadRejects(t *testing.T) {
	if _, err := Read(bytes.NewReader([]byte("RIFF\x00\x00\x00\x00AVI "))); !errors.Is(err, ErrFormat) {
		t.Errorf("AVI: %v", err)
	}
	if _, err := Read(bytes.NewReader(build(2, 1, 4, 8000, false, []byte{1, 2}))); !errors.Is(err, ErrFormat) {
		t.Errorf("ADPCM: %v", err)
	}
}

func TestReadFixture(t *testing.T) {
	path := filepath.Join("..", "..", "..", "testdata", "jfk.wav")
	if _, err := os.Stat(path); err != nil {
		t.Skip("testdata/jfk.wav missing")
	}
	a, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if a.SampleRate != 16000 || a.Channels != 1 || a.Seconds() < 10 || a.Seconds() > 12 {
		t.Fatalf("jfk.wav: %+v, %.2f s", a.Format, a.Seconds())
	}
}

func TestOpenReadFrames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.wav")
	w, err := Create(path, Format{SampleRate: 16000, Channels: 1})
	if err != nil {
		t.Fatal(err)
	}
	pcm := make([]float32, 1000)
	for i := range pcm {
		pcm[i] = float32(i%100) / 200
	}
	w.Write(pcm)
	w.Close()
	f, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if f.Frames() != 1000 || f.Format.SampleRate != 16000 {
		t.Fatalf("%d frames, %+v", f.Frames(), f.Format)
	}
	dst := make([]float32, 300)
	n, err := f.ReadFrames(dst, 850)
	if err != nil || n != 150 {
		t.Fatalf("n %d err %v", n, err)
	}
	if d := dst[0] - pcm[850]; d > 1e-3 || d < -1e-3 {
		t.Fatalf("sample %v want %v", dst[0], pcm[850])
	}
	if _, err := f.ReadFrames(dst, 1000); err == nil {
		t.Fatal("read past the end")
	}
}

// A file still being written (as during a recording, whose tracks sync
// every 2 s) can be read up to the last sync while the writer keeps it
// open.
func TestOpenWhileWriting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "growing.wav")
	w, err := Create(path, Format{SampleRate: 16000, Channels: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	w.Write(make([]float32, 16000))
	w.Sync()
	f, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	n1 := f.Frames()
	f.Close()
	w.Write(make([]float32, 16000))
	w.Sync()
	f, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if n1 != 16000 || f.Frames() != 32000 {
		t.Fatalf("frames %d then %d", n1, f.Frames())
	}
}
