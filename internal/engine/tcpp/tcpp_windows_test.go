package tcpp

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/motivated2die/flit/internal/audio/wav"
	"github.com/motivated2die/flit/internal/models"
)

// These tests run the real engine. They need the DLLs
// (go run ./cmd/flitdev engine, or FLIT_ENGINE_DIR) and the test models
// (go run ./cmd/flitdev models); without them they are skipped.

func repo() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..")
}

var (
	libOnce sync.Once
	lib     *Lib
	libErr  error
)

func testLib(t *testing.T) *Lib {
	t.Helper()
	dir := os.Getenv("FLIT_ENGINE_DIR")
	if dir == "" {
		dir = filepath.Join(repo(), "internal", "engine", "native", "files")
	}
	if _, err := os.Stat(filepath.Join(dir, "transcribe.dll")); err != nil {
		t.Skip("no engine DLLs (go run ./cmd/flitdev engine)")
	}
	libOnce.Do(func() {
		SetLogger(func(level LogLevel, msg string) {
			if level <= LogWarn && level != LogInfo {
				println("engine:", msg)
			}
		})
		lib, libErr = Load(dir, true)
	})
	if libErr != nil {
		t.Fatal(libErr)
	}
	return lib
}

func testModel(t *testing.T, id string) *Model {
	t.Helper()
	l := testLib(t)
	m, _ := models.ByID(id)
	path := m.Path(filepath.Join(repo(), "models"))
	if _, err := os.Stat(path); err != nil {
		t.Skipf("model %s missing (go run ./cmd/flitdev models)", id)
	}
	mod, err := l.LoadModel(path, BackendCPU)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mod.Close)
	return mod
}

func fixture(t *testing.T, name string) []float32 {
	t.Helper()
	a, err := wav.ReadFile(filepath.Join(repo(), "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	if a.SampleRate != 16000 || a.Channels != 1 {
		t.Fatalf("%s is %+v, want 16 kHz mono", name, a.Format)
	}
	return a.Samples
}

func TestLoadChecksABI(t *testing.T) {
	l := testLib(t)
	if v := l.Version(); !strings.HasPrefix(v, "0.4.2") {
		t.Errorf("version %q", v)
	}
}

func TestWhisperEnglish(t *testing.T) {
	m := testModel(t, "whisper-tiny")
	s, err := m.NewSession(0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	res, err := s.Run(fixture(t, "jfk.wav"), RunOptions{Language: "en", Timestamps: TimestampsSegment})
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ToLower(res.Text)
	if !strings.Contains(text, "ask not what your country can do for you") {
		t.Errorf("text %q", res.Text)
	}
	if len(res.Segments) == 0 || res.Segments[len(res.Segments)-1].T1 < 8000 {
		t.Errorf("segments %+v", res.Segments)
	}
}

func TestWhisperGerman(t *testing.T) {
	m := testModel(t, "whisper-tiny")
	s, _ := m.NewSession(0)
	defer s.Close()
	res, err := s.Run(fixture(t, "fleurs-de.wav"), RunOptions{Language: "de"})
	if err != nil {
		t.Fatal(err)
	}
	// Whisper tiny is weak; check it heard German, not the exact words.
	hits := 0
	for _, w := range []string{"wasser", "dinosaurier", "griff", "großer", "gewachsen"} {
		if strings.Contains(strings.ToLower(res.Text), w) {
			hits++
		}
	}
	if hits < 2 {
		t.Errorf("German text %q", res.Text)
	}
}

func TestParakeetWords(t *testing.T) {
	m := testModel(t, "parakeet-tdt_ctc-110m")
	if m.Caps.MaxTimestamps < TimestampsWord {
		t.Fatalf("caps %+v", m.Caps)
	}
	s, _ := m.NewSession(0)
	defer s.Close()
	res, err := s.Run(fixture(t, "jfk.wav"), RunOptions{Timestamps: TimestampsWord})
	if err != nil {
		t.Fatal(err)
	}
	var words []Word
	for _, seg := range res.Segments {
		words = append(words, seg.Words...)
	}
	if len(words) < 15 {
		t.Fatalf("%d words: %+v", len(words), res)
	}
	for i := 1; i < len(words); i++ {
		if words[i].T0 < words[i-1].T0 || words[i].T1 < words[i].T0 {
			t.Fatalf("word times not monotonic at %d: %+v %+v", i, words[i-1], words[i])
		}
	}
	if !strings.Contains(strings.ToLower(res.Text), "country") {
		t.Errorf("text %q", res.Text)
	}
}

func TestVocabularyIsAccepted(t *testing.T) {
	m := testModel(t, "parakeet-tdt_ctc-110m")
	s, _ := m.NewSession(0)
	defer s.Close()
	// The 110M model decodes with its CTC head, which takes no vocabulary
	// (the 0.6B TDT models do): the binding must report that, and a run
	// with terms must still work.
	if m.Supports(FeatureVocabulary) {
		t.Log("parakeet-tdt_ctc-110m reports vocabulary support")
	}
	if _, err := s.Run(fixture(t, "jfk.wav"), RunOptions{Vocabulary: []string{"Americans", "flit"}}); err != nil {
		t.Fatal(err)
	}
}

func TestVAD(t *testing.T) {
	m := testModel(t, "silero-vad-v6.2")
	v, err := m.NewVAD(0)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	pcm := fixture(t, "jfk.wav")
	// Two seconds of silence in front: the first segment must start after.
	pcm = append(make([]float32, 32000), pcm...)
	res, err := v.Run(pcm, VADParams{})
	if err != nil {
		t.Fatal(err)
	}
	if v.FrameSamples <= 0 || len(res.Probs) < len(pcm)/v.FrameSamples-2 {
		t.Fatalf("frame %d, %d probs for %d samples", v.FrameSamples, len(res.Probs), len(pcm))
	}
	if len(res.Segments) == 0 || res.Segments[0][0] < 28000 {
		t.Fatalf("segments %v", res.Segments)
	}
	for _, seg := range res.Segments {
		if seg[0] >= seg[1] || seg[1] > int64(len(pcm)) {
			t.Fatalf("bad segment %v", seg)
		}
	}
}
