package capture

import (
	"math"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/moutend/go-wca/pkg/wca"
	"golang.org/x/sys/windows"

	"github.com/motivated2die/flit/internal/audio/wav"
	"github.com/motivated2die/flit/internal/coreaudio"
)

// Live tests use the real audio devices: FLIT_LIVE_AUDIO=1 go test
// ./internal/capture -run Live -v. They play a quiet tone (-30 dB, 1.5 s)
// on the default output and, for the mic test, open the default microphone
// (Windows shows its mic-in-use icon meanwhile).

func live(t *testing.T) {
	if os.Getenv("FLIT_LIVE_AUDIO") != "1" {
		t.Skip("set FLIT_LIVE_AUDIO=1 to use the real audio devices")
	}
}

// playTone renders a 660 Hz tone at amp on the default communications
// output for d, from this process.
func playTone(t *testing.T, amp float32, d time.Duration) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		if err := coreaudio.Init(); err != nil {
			done <- err
			return
		}
		defer ole.CoUninitialize()
		en, err := coreaudio.NewEnumerator()
		if err != nil {
			done <- err
			return
		}
		defer en.Release()
		dev, err := en.Device(coreaudio.Render, coreaudio.Communications, "")
		if err != nil {
			done <- err
			return
		}
		defer dev.Release()
		var c *wca.IAudioClient
		if err := coreaudio.Activate(dev, wca.IID_IAudioClient, unsafe.Pointer(&c)); err != nil {
			done <- err
			return
		}
		defer c.Release()
		var wfx *wca.WAVEFORMATEX
		if err := c.GetMixFormat(&wfx); err != nil {
			done <- err
			return
		}
		defer ole.CoTaskMemFree(uintptr(unsafe.Pointer(wfx)))
		f, err := ParseWaveFormat(unsafe.Slice((*byte)(unsafe.Pointer(wfx)), 18+int(wfx.CbSize)))
		if err != nil || !f.Float {
			done <- err
			return
		}
		if err := c.Initialize(wca.AUDCLNT_SHAREMODE_SHARED, 0, wca.REFERENCE_TIME(100*time.Millisecond/100), 0, wfx, nil); err != nil {
			done <- err
			return
		}
		var size uint32
		c.GetBufferSize(&size)
		var rc *wca.IAudioRenderClient
		if err := c.GetService(wca.IID_IAudioRenderClient, &rc); err != nil {
			done <- err
			return
		}
		defer rc.Release()
		c.Start()
		defer c.Stop()
		n := 0
		for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
			var pad uint32
			c.GetCurrentPadding(&pad)
			free := size - pad
			if free == 0 {
				continue
			}
			var data *byte
			if err := rc.GetBuffer(free, &data); err != nil {
				done <- err
				return
			}
			out := unsafe.Slice((*float32)(unsafe.Pointer(data)), int(free)*f.Channels)
			for i := 0; i < int(free); i++ {
				v := amp * float32(math.Sin(2*math.Pi*660*float64(n)/float64(f.Rate)))
				for ch := 0; ch < f.Channels; ch++ {
					out[i*f.Channels+ch] = v
				}
				n++
			}
			rc.ReleaseBuffer(free, 0)
		}
		done <- nil
	}()
	return done
}

func record(t *testing.T, src Source, during func()) wav.Audio {
	t.Helper()
	path := filepath.Join(t.TempDir(), "t.wav")
	tr, err := NewTrack(path)
	if err != nil {
		t.Fatal(err)
	}
	var events []Event
	s, err := Start(src, tr, QPC(), func(e Event) { events = append(events, e) })
	if err != nil {
		tr.Close()
		t.Fatalf("%s: %v", src, err)
	}
	during()
	s.Stop()
	if err := tr.Close(); err != nil {
		t.Fatal(err)
	}
	a, err := wav.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s: %v, %.2f s recorded", src, events, a.Seconds())
	return a
}

func rms(x []float32) float64 {
	var e float64
	for _, v := range x {
		e += float64(v) * float64(v)
	}
	return math.Sqrt(e / float64(max(1, len(x))))
}

func TestLiveLoopbackHearsTone(t *testing.T) {
	live(t)
	a := record(t, Source{Kind: Loopback}, func() {
		time.Sleep(300 * time.Millisecond)
		if err := <-playTone(t, 0.03, 1500*time.Millisecond); err != nil {
			t.Fatal(err)
		}
		time.Sleep(300 * time.Millisecond)
	})
	if a.Seconds() < 1.9 || a.Seconds() > 2.6 {
		t.Errorf("track is %.2f s for ~2.1 s of recording", a.Seconds())
	}
	if r := rms(a.Samples); r < 0.005 {
		t.Errorf("loopback rms %.4f: the tone wasn't heard", r)
	}
}

func TestLiveProcessLoopbackHearsOwnTone(t *testing.T) {
	live(t)
	a := record(t, Source{Kind: Process, PID: uint32(windows.GetCurrentProcessId())}, func() {
		time.Sleep(300 * time.Millisecond)
		if err := <-playTone(t, 0.03, 1500*time.Millisecond); err != nil {
			t.Fatal(err)
		}
		time.Sleep(300 * time.Millisecond)
	})
	if r := rms(a.Samples); r < 0.005 {
		t.Errorf("process loopback rms %.4f: our own tone wasn't heard", r)
	}
	// Silence before the tone is in the track (the time line is kept).
	if a.Seconds() < 1.9 {
		t.Errorf("track is %.2f s", a.Seconds())
	}
}

func TestLiveProcessLoopbackIgnoresOthers(t *testing.T) {
	live(t)
	// Another process's tree (explorer.exe, which plays nothing now) must
	// not hear our tone.
	var pid uint32
	if h, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0); err == nil {
		e := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
		for err := windows.Process32First(h, &e); err == nil; err = windows.Process32Next(h, &e) {
			if windows.UTF16ToString(e.ExeFile[:]) == "explorer.exe" {
				pid = e.ProcessID
				break
			}
		}
		windows.CloseHandle(h)
	}
	if pid == 0 {
		t.Skip("no explorer.exe")
	}
	a := record(t, Source{Kind: Process, PID: pid}, func() {
		if err := <-playTone(t, 0.03, time.Second); err != nil {
			t.Fatal(err)
		}
	})
	if r := rms(a.Samples); r > 0.002 {
		t.Errorf("explorer's tree heard our tone (rms %.4f)", r)
	}
}

func TestLiveMicOpens(t *testing.T) {
	live(t)
	a := record(t, Source{Kind: Mic}, func() { time.Sleep(time.Second) })
	if a.Seconds() < 0.8 {
		t.Errorf("mic track %.2f s", a.Seconds())
	}
}

func TestLiveRecordBothTracksAligned(t *testing.T) {
	live(t)
	dir := t.TempDir()
	r, err := Record(dir, "me.wav", "them.wav", Source{Kind: Mic}, Source{Kind: Process, PID: uint32(windows.GetCurrentProcessId())}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-playTone(t, 0.03, time.Second); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	if err := r.Stop(); err != nil {
		t.Fatal(err)
	}
	me, _ := wav.ReadFile(filepath.Join(dir, "me.wav"))
	them, _ := wav.ReadFile(filepath.Join(dir, "them.wav"))
	t.Logf("me %.3f s, them %.3f s (%v)", me.Seconds(), them.Seconds(), r.ThemSource)
	if d := math.Abs(me.Seconds() - them.Seconds()); d > 0.05 {
		t.Errorf("tracks differ by %.3f s", d)
	}
	if rms(them.Samples) < 0.003 {
		t.Error("the meeting track didn't hear the tone")
	}
}

// TestLiveMicLevels measures every microphone for 1.5 s and prints its
// loudest sample: a device reading digital silence isn't delivering audio
// at all. Nothing is recorded. FLIT_LIVE_AUDIO=1.
func TestLiveMicLevels(t *testing.T) {
	live(t)
	var eps []coreaudio.Endpoint
	coreaudio.Run(func() error {
		en, err := coreaudio.NewEnumerator()
		if err != nil {
			return err
		}
		defer en.Release()
		eps, err = en.Endpoints(coreaudio.Capture)
		return err
	})
	meters := map[string]*Meter{}
	for _, e := range eps {
		m, err := OpenMeter(Source{Kind: Mic, DeviceID: e.ID})
		if err != nil {
			t.Logf("%-55s cannot open: %v", e.Name, err)
			continue
		}
		meters[e.Name] = m
	}
	time.Sleep(1500 * time.Millisecond)
	for name, m := range meters {
		p := m.TakePeak()
		m.Close()
		db := -120.0
		if p > 0 {
			db = 20 * math.Log10(float64(p))
		}
		t.Logf("%-55s peak %6.1f dBFS", name, db)
	}
}

// Switching the microphone mid-recording keeps one file on one time line:
// both tracks still end together.
func TestLiveSwitchMicKeepsTimeline(t *testing.T) {
	live(t)
	var eps []coreaudio.Endpoint
	coreaudio.Run(func() error {
		en, err := coreaudio.NewEnumerator()
		if err != nil {
			return err
		}
		defer en.Release()
		eps, err = en.Endpoints(coreaudio.Capture)
		return err
	})
	if len(eps) < 2 {
		t.Skip("needs two microphones")
	}
	dir := t.TempDir()
	r, err := Record(dir, "me.wav", "them.wav", Source{Kind: Mic, DeviceID: eps[0].ID}, Source{Kind: Loopback}, nil)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(800 * time.Millisecond)
	if err := r.SwitchMic(Source{Kind: Mic, DeviceID: eps[1].ID}); err != nil {
		t.Fatal(err)
	}
	if r.MeSource.DeviceID != eps[1].ID {
		t.Fatalf("source %+v", r.MeSource)
	}
	time.Sleep(800 * time.Millisecond)
	if err := r.Stop(); err != nil {
		t.Fatal(err)
	}
	me, _ := wav.ReadFile(filepath.Join(dir, "me.wav"))
	them, _ := wav.ReadFile(filepath.Join(dir, "them.wav"))
	t.Logf("me %.3f s, them %.3f s", me.Seconds(), them.Seconds())
	if d := math.Abs(me.Seconds() - them.Seconds()); d > 0.05 || me.Seconds() < 1.5 {
		t.Errorf("after a switch: me %.3f s, them %.3f s", me.Seconds(), them.Seconds())
	}
}
