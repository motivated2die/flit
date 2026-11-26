package m4a

import (
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func amplitude(x []float32, rate int, freq float64) float64 {
	start, end := len(x)/4, 3*len(x)/4
	var re, im float64
	for i := start; i < end; i++ {
		ph := 2 * math.Pi * freq * float64(i) / float64(rate)
		re += float64(x[i]) * math.Cos(ph)
		im += float64(x[i]) * math.Sin(ph)
	}
	return 2 * math.Hypot(re, im) / float64(end-start)
}

func TestRoundTripMonoAndStereoAndSeek(t *testing.T) {
	if !Available() {
		t.Skip("Media Foundation AAC unavailable on this system")
	}
	for _, tc := range []struct {
		name string
		ch   int
		br   int
	}{
		{"48k-mono-64k", 1, 64000},
		{"48k-stereo-96k", 2, 96000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), tc.name+".m4a")
			w, err := Create(path, Format{SampleRate: 48000, Channels: tc.ch, Bitrate: tc.br})
			if err != nil {
				t.Fatal(err)
			}
			const nFrames = 48000 * 2 // 2 seconds
			in := make([]float32, nFrames*tc.ch)
			for i := range nFrames {
				l := float32(0.5 * math.Sin(2*math.Pi*440*float64(i)/48000))
				if tc.ch == 1 {
					in[i] = l
				} else {
					r := float32(0.4 * math.Sin(2*math.Pi*880*float64(i)/48000))
					in[2*i], in[2*i+1] = l, r
				}
			}
			// Write in realistic 10 ms blocks (480 frames).
			for off := 0; off < len(in); off += 480 * tc.ch {
				end := min(len(in), off+480*tc.ch)
				if err := w.Write(in[off:end]); err != nil {
					t.Fatal(err)
				}
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			// Scratch .aac must be gone after clean Close.
			if _, err := os.Stat(path + ScratchSuffix); !os.IsNotExist(err) {
				t.Errorf("scratch %s still exists", path+ScratchSuffix)
			}

			f, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if f.Format.SampleRate != 48000 || f.Format.Channels != tc.ch {
				t.Fatalf("format %+v", f.Format)
			}
			if f.Frames() != nFrames {
				t.Fatalf("frames %d, want %d", f.Frames(), nFrames)
			}

			// Sequential interleaved read.
			all := make([]float32, nFrames*tc.ch)
			got, err := f.ReadInterleaved(all, 0)
			if err != nil || got != nFrames {
				t.Fatalf("ReadInterleaved got %d, err %v", got, err)
			}
			left := make([]float32, nFrames)
			for i := range nFrames {
				left[i] = all[i*tc.ch]
			}
			if a := amplitude(left, 48000, 440); math.Abs(a-0.5) > 0.02 {
				t.Errorf("left 440 Hz amp %.4f, want 0.5", a)
			}
			if tc.ch == 2 {
				right := make([]float32, nFrames)
				for i := range nFrames {
					right[i] = all[i*2+1]
				}
				if a := amplitude(right, 48000, 880); math.Abs(a-0.4) > 0.02 {
					t.Errorf("right 880 Hz amp %.4f, want 0.4", a)
				}
				if bleed := amplitude(left, 48000, 880); bleed > 0.02 {
					t.Errorf("right bled into left: %.4f", bleed)
				}
			}

			// Random-access seek to middle of file must match sequential decode closely.
			const seekAt = 50000
			const seekLen = 8000
			seeked := make([]float32, seekLen*tc.ch)
			got, err = f.ReadInterleaved(seeked, seekAt)
			if err != nil || got != seekLen {
				t.Fatalf("seeked ReadInterleaved got %d, err %v", got, err)
			}
			var maxDiff float64
			for i := range seekLen * tc.ch {
				d := math.Abs(float64(seeked[i] - all[seekAt*tc.ch+i]))
				if d > maxDiff {
					maxDiff = d
				}
			}
			if maxDiff > 0.01 {
				t.Errorf("seek mismatch maxDiff=%.5f", maxDiff)
			}
		})
	}
}

func TestPrimingAlignment(t *testing.T) {
	if !Available() {
		t.Skip("Media Foundation AAC unavailable on this system")
	}
	path := filepath.Join(t.TempDir(), "pulse.m4a")
	w, err := Create(path, Format{SampleRate: 48000, Channels: 1, Bitrate: 64000})
	if err != nil {
		t.Fatal(err)
	}
	in := make([]float32, 48000)
	for i := range 4800 {
		in[10000+i] = float32(0.5 * math.Sin(2*math.Pi*1000*float64(i)/48000) * math.Sin(math.Pi*float64(i)/4800))
	}
	if err := w.Write(in); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	_, out, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Cross-correlate around 0 lag to check exact priming alignment.
	bestLag, bestCorr := 0, -1e9
	for lag := -2048; lag <= 2048; lag++ {
		var c float64
		for i := 10000; i < 14800; i++ {
			j := i + lag
			if j >= 0 && j < len(out) {
				c += float64(in[i]) * float64(out[j])
			}
		}
		if c > bestCorr {
			bestCorr, bestLag = c, lag
		}
	}
	t.Logf("measured residual lag after defaultPriming=%d: %d samples (%.2f ms)", defaultPriming, bestLag, float64(bestLag)*1000/48000)
	if bestLag < -128 || bestLag > 128 {
		t.Errorf("priming alignment off by %d samples (adjust defaultPriming by %+d)", bestLag, bestLag)
	}
}

func TestGrowingADTSAndCrashRecovery(t *testing.T) {
	if !Available() {
		t.Skip("Media Foundation AAC unavailable on this system")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "rec.m4a")
	w, err := Create(path, Format{SampleRate: 48000, Channels: 1, Bitrate: 64000})
	if err != nil {
		t.Fatal(err)
	}
	tone := make([]float32, 48000)
	for i := range tone {
		tone[i] = float32(0.4 * math.Sin(2*math.Pi*440*float64(i)/48000))
	}
	// Write 1 second and Sync (simulating live recording).
	if err := w.Write(tone); err != nil {
		t.Fatal(err)
	}
	if err := w.Sync(); err != nil {
		t.Fatal(err)
	}

	// Open the growing .aac file while Writer is still open!
	aacPath := path + ScratchSuffix
	f, err := Open(aacPath)
	if err != nil {
		t.Fatal(err)
	}
	n1 := f.Frames()
	if n1 < 40000 {
		t.Fatalf("growing ADTS has only %d frames after 1s", n1)
	}
	// Write another second and Sync; growing reader must see the new frames.
	if err := w.Write(tone); err != nil {
		t.Fatal(err)
	}
	if err := w.Sync(); err != nil {
		t.Fatal(err)
	}
	n2 := f.Frames()
	if n2 <= n1+40000 {
		t.Fatalf("growing ADTS didn't advance: %d -> %d", n1, n2)
	}
	f.Close()

	// Simulate crash: copy the .aac aside and recover it via Recover().
	crashedAAC := filepath.Join(dir, "crashed.m4a.aac")
	crashedM4A := filepath.Join(dir, "crashed.m4a")
	b, err := os.ReadFile(aacPath)
	if err != nil {
		t.Fatal(err)
	}
	// Append 3 garbage bytes (partial frame on crash) — Recover must ignore them cleanly.
	if err := os.WriteFile(crashedAAC, append(b, 0xFF, 0xF1, 0x50), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := Recover(crashedAAC, crashedM4A); err != nil {
		t.Fatal(err)
	}
	_, samples, err := ReadFile(crashedM4A)
	if err != nil {
		t.Fatal(err)
	}
	if a := amplitude(samples, 48000, 440); math.Abs(a-0.4) > 0.02 {
		t.Errorf("recovered audio 440 Hz amp %.4f, want 0.4", a)
	}
}

func TestFFmpegValidatesM4A(t *testing.T) {
	if !Available() {
		t.Skip("Media Foundation AAC unavailable")
	}
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	path := filepath.Join(t.TempDir(), "check.m4a")
	w, err := Create(path, Format{SampleRate: 48000, Channels: 2, Bitrate: 96000})
	if err != nil {
		t.Fatal(err)
	}
	pcm := make([]float32, 48000*2)
	for i := range 48000 {
		pcm[2*i] = float32(0.4 * math.Sin(2*math.Pi*440*float64(i)/48000))
		pcm[2*i+1] = float32(0.3 * math.Sin(2*math.Pi*880*float64(i)/48000))
	}
	if err := w.Write(pcm); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(ffmpeg, "-v", "error", "-i", path, "-f", "null", "-").CombinedOutput()
	if err != nil || len(out) > 0 {
		t.Fatalf("ffmpeg validation failed (err=%v): %s", err, string(out))
	}
}

// TestMakeSampleStackGrowth exercises makeSample + readSampleBytes across
// fresh goroutines at varying recursion depths to ensure stack growth cannot
// invalidate stack-allocated COM out-parameters (regression test for missing
// //go:uintptrescapes on call).
func TestMakeSampleStackGrowth(t *testing.T) {
	if !Available() {
		t.Skip("Media Foundation AAC unavailable")
	}
	payload := make([]byte, 235)
	for i := range payload {
		payload[i] = byte(i)
	}
	var recurse func(depth int) error
	recurse = func(depth int) error {
		var pad [64]byte
		pad[0] = byte(depth)
		if depth > 0 {
			return recurse(depth - 1)
		}
		s, err := makeSample(payload, 1000, 213333)
		if err != nil {
			return err
		}
		got := readSampleBytes(s)
		release(s)
		if len(got) != len(payload) || got[len(got)-1] != payload[len(payload)-1] || pad[0] != 0 {
			return errBadSample
		}
		return nil
	}
	errs := make(chan error, 64)
	for depth := range 64 {
		go func(d int) {
			errs <- recurse(d)
		}(depth)
	}
	for range 64 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
}

var errBadSample = os.ErrInvalid
