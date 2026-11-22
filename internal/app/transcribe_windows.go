package app

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/motivated2die/flit/internal/audio/dsp"
	"github.com/motivated2die/flit/internal/audio/wav"
	"github.com/motivated2die/flit/internal/config"
	"github.com/motivated2die/flit/internal/download"
	"github.com/motivated2die/flit/internal/engine"
	"github.com/motivated2die/flit/internal/engine/native"
	"github.com/motivated2die/flit/internal/eval"
	"github.com/motivated2die/flit/internal/install"
	"github.com/motivated2die/flit/internal/models"
	"github.com/motivated2die/flit/internal/pipeline"
	"github.com/motivated2die/flit/internal/sysinfo"
	"github.com/motivated2die/flit/internal/transcript"
)

// runTranscribe is `flit transcribe`: the whole pipeline on WAV files, in a
// real engine worker, for development and benchmarks.
//
//	flit transcribe [flags] file.wav
//	flit transcribe [flags] --me mic.wav --them meeting.wav
func runTranscribe(args []string) int {
	fs := flag.NewFlagSet("transcribe", flag.ContinueOnError)
	lang := fs.String("lang", "", "meeting language (de, en); default from config")
	modelID := fs.String("model", "", "ASR model id (default: the language's default)")
	modelsDir := fs.String("models", "", "models folder (default: <data>\\models)")
	me := fs.String("me", "", "microphone track")
	them := fs.String("them", "", "meeting track")
	vocab := fs.String("vocab", "", "comma-separated custom terms")
	md := fs.Bool("markdown", false, "Markdown output")
	cpu := fs.Bool("cpu", true, "CPU only")
	threads := fs.Int("threads", 0, "engine threads (0: default)")
	meeting := fs.String("meeting", "", "a test meeting folder (testdata/meetings/…): transcribe its tracks and score them")
	bleed := fs.Bool("bleed", false, "with --meeting: use the mic track with loudspeaker bleed (me-bleed.wav)")
	speakers := fs.Bool("speakers", false, "tell the speakers on the meeting track apart (Sortformer)")
	realtime := fs.Bool("realtime", false, "with --live-step: feed both tracks at the meeting's own pace, logging how far behind and the engine's CPU")
	liveStep := fs.Int("live-step", 0, "simulate transcribing during the meeting: feed each track in slices of this many seconds")
	cpuLimit := fs.Int("cpu-limit", 0, "cap the engine worker at this percent of all processors, at low priority (as during a meeting)")
	save := fs.String("save", "", "write the meeting track's segments (with words and speakers) as JSON to this file, for experiments")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	var ref *eval.Manifest
	if *meeting != "" {
		m, err := eval.Load(*meeting)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		ref = &m
		*me, *them = m.Track(*meeting, "me"), m.Track(*meeting, "them")
		if *bleed {
			if p := m.Track(*meeting, "me-bleed"); p != "" {
				*me = p
			}
		}
		if *lang == "" {
			*lang = m.Language
		}
	}
	if *me == "" && *them == "" && fs.NArg() == 1 {
		*them = fs.Arg(0)
	}
	if *me == "" && *them == "" {
		fmt.Fprintln(os.Stderr, "usage: flit transcribe [flags] file.wav | --me mic.wav --them meeting.wav")
		fs.PrintDefaults()
		return 2
	}
	dataDir := config.Dir()
	cfg, _ := config.Load(dataDir)
	if *lang == "" {
		*lang = cfg.Language
	}
	if *modelsDir == "" {
		*modelsDir = filepath.Join(dataDir, "models")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	asr, ok := models.ByID(*modelID)
	if *modelID == "" {
		asr, ok = models.Default(models.RoleASR, *lang)
	}
	if !ok {
		fmt.Fprintf(os.Stderr, "no model %q for %q\n", *modelID, *lang)
		return 2
	}
	vad, _ := models.Default(models.RoleVAD, "")
	need := []models.Model{asr, vad}
	diar, _ := models.Default(models.RoleDiarize, "")
	if *speakers {
		need = append(need, diar)
	}
	for _, m := range need {
		if err := ensureModel(ctx, m, *modelsDir); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	engineDir, err := engineDir(dataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	exe, err := install.Current()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	sup := &engine.Supervisor{
		Command: engine.WorkerCommand(exe),
		Spec:    engine.LoadSpec{EngineDir: engineDir, ASRModel: asr.Path(*modelsDir), VADModel: vad.Path(*modelsDir), CPUOnly: *cpu, Threads: *threads},
		Logf: func(f string, a ...any) {
			if msg := fmt.Sprintf(f, a...); !strings.HasPrefix(msg, "engine: ") || os.Getenv("FLIT_ENGINE_LOG") != "" {
				fmt.Fprintln(os.Stderr, msg)
			}
		},
	}
	if *speakers {
		sup.Spec.DiarModel = diar.Path(*modelsDir)
	}
	if *cpuLimit > 0 {
		sup.CPULimit, sup.Background = *cpuLimit, true
	}
	defer sup.Close()

	opt := pipeline.Options{Language: *lang}
	if *vocab != "" {
		opt.Vocabulary = strings.Split(*vocab, ",")
	}
	var tracks [][]transcript.Segment
	var meSegs, themSegs []transcript.Segment
	var themPCM []float32
	if *realtime && *liveStep > 0 {
		var mePCM []float32
		var err error
		if *me != "" {
			if mePCM, _, err = load16k(*me); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
		}
		if *them != "" {
			if themPCM, _, err = load16k(*them); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
		}
		if meSegs, themSegs, err = simulateMeeting(ctx, sup, mePCM, themPCM, opt, *liveStep); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		*me, *them = "", "" // done: skip the per-track loop
	}
	for _, tr := range []struct {
		path  string
		track transcript.Track
		out   *[]transcript.Segment
	}{{*me, transcript.Me, &meSegs}, {*them, transcript.Them, &themSegs}} {
		if tr.path == "" {
			continue
		}
		pcm, secs, err := load16k(tr.path)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		start := time.Now()
		var segs []transcript.Segment
		if *liveStep > 0 {
			// As during a meeting: the track grows by liveStep seconds at a
			// time; whatever is certain is transcribed; the rest at the end.
			p := &pipeline.Progressive{Track: tr.track}
			for n := *liveStep * pipeline.Rate; n < len(pcm) && err == nil; n += *liveStep * pipeline.Rate {
				_, err = p.Step(ctx, sup, pipeline.Samples(pcm[:n]), false, opt, nil, nil)
			}
			if err == nil {
				endStart := time.Now()
				var left int
				left, err = p.Step(ctx, sup, pipeline.Samples(pcm), true, opt, nil, nil)
				fmt.Fprintf(os.Stderr, "%s: %d chunk(s) left at the end, %.1f s\n", tr.track, left, time.Since(endStart).Seconds())
			}
			segs = p.Segments
		} else {
			segs, err = pipeline.Track(ctx, sup, pcm, tr.track, opt, func(done, total int) {
				fmt.Fprintf(os.Stderr, "\r%s: %d%%", tr.track, done*100/max(1, total))
			})
		}
		el := time.Since(start)
		fmt.Fprintf(os.Stderr, "\r%s: %.0f s of audio in %.1f s (%.1f× real time), %d segments\n", tr.track, secs, el.Seconds(), secs/el.Seconds(), len(segs))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		*tr.out = segs
		if tr.track == transcript.Them {
			themPCM = pcm
		}
	}
	var turns []transcript.Turn
	if *speakers && themPCM != nil {
		start := time.Now()
		raw, err := sup.Diarize(ctx, themPCM)
		if err != nil {
			fmt.Fprintln(os.Stderr, "speakers:", err)
			return 1
		}
		secs := float64(len(themPCM)) / pipeline.Rate
		turns = pipeline.Turns(raw)
		themSegs = transcript.AssignSpeakers(themSegs, turns)
		n := map[int]bool{}
		for _, s := range themSegs {
			n[s.Speaker] = true
		}
		fmt.Fprintf(os.Stderr, "speakers: %d turns, %d speakers after cleanup, %.0f s in %.1f s (%.1f× real time)\n", len(raw), len(n), secs, time.Since(start).Seconds(), secs/time.Since(start).Seconds())
	}
	if *save != "" {
		b, err := json.MarshalIndent(themSegs, "", " ")
		if err == nil {
			err = os.WriteFile(*save, b, 0o644)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "save:", err)
			return 1
		}
	}
	if len(meSegs) > 0 && len(themSegs) > 0 {
		before := countWords(meSegs)
		meSegs = transcript.SuppressEcho(meSegs, themSegs, transcript.DefaultEcho())
		if d := before - countWords(meSegs); d > 0 {
			fmt.Fprintf(os.Stderr, "echo: removed %d of %d mic words\n", d, before)
		}
	}
	tracks = append(tracks, meSegs, themSegs)
	if ref != nil {
		printScores(*ref, meSegs, themSegs)
		if turns != nil {
			printSpeakerScores(*ref, *meeting, turns, themSegs)
		}
	}
	style := transcript.Plain
	if *md {
		style = transcript.Markdown
	}
	fmt.Print(transcript.Format(transcript.Header{}, transcript.Merge(tracks...), transcript.Names{}, style))
	return 0
}

// printScores compares each track's text with the reference: the Me
// speaker's words for the mic track, everyone else's for the meeting track.
func printScores(ref eval.Manifest, me, them []transcript.Segment) {
	refText := map[string]*strings.Builder{"me": {}, "them": {}}
	for _, s := range ref.Segments {
		b := refText[ref.TrackOf(s.Speaker)]
		if b != nil {
			b.WriteString(s.Text + " ")
		}
	}
	hyp := func(segs []transcript.Segment) string {
		var b strings.Builder
		for _, s := range segs {
			b.WriteString(s.Text + " ")
		}
		return b.String()
	}
	for _, tr := range []struct {
		name string
		segs []transcript.Segment
	}{{"me", me}, {"them", them}} {
		w := eval.ScoreText(refText[tr.name].String(), hyp(tr.segs))
		fmt.Fprintf(os.Stderr, "WER %-4s %5.1f%%  (%d words: %d sub, %d del, %d ins)\n", tr.name, 100*w.Rate(), w.Ref, w.Sub, w.Del, w.Ins)
	}
}

// printSpeakerScores compares the meeting track's speakers with the
// reference: DER of the diarizer's raw turns, and the share of words that
// ended up with the right person after AssignSpeakers.
func printSpeakerScores(ref eval.Manifest, dir string, turns []transcript.Turn, them []transcript.Segment) {
	f, err := os.Open(filepath.Join(dir, "reference.rttm"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	all, err := eval.ReadRTTM(f)
	f.Close()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	var refTurns []eval.Turn
	for _, t := range all {
		if ref.TrackOf(t.Speaker) == "them" {
			refTurns = append(refTurns, t)
		}
	}
	var hyp []eval.Turn
	for _, t := range turns {
		hyp = append(hyp, eval.Turn{Speaker: fmt.Sprint(t.Speaker), Start: t.Start, Duration: t.End - t.Start})
	}
	d := eval.ScoreDER(refTurns, hyp)
	fmt.Fprintf(os.Stderr, "DER them %5.1f%%  (missed %.0f s, false alarm %.0f s, confusion %.0f s of %.0f s)\n",
		100*d.Rate(), d.Missed.Seconds(), d.FalseAlarm.Seconds(), d.Confusion.Seconds(), d.Speech.Seconds())
	var pairs [][2]string
	for _, s := range them {
		for _, w := range s.Words {
			mid := (w.Start + w.End) / 2
			who := ""
			for _, t := range refTurns {
				if t.Start <= mid && mid < t.End() {
					if who != "" && who != t.Speaker {
						who = "" // overlapped speech: not scored
						break
					}
					who = t.Speaker
				}
			}
			if who != "" {
				pairs = append(pairs, [2]string{who, fmt.Sprint(s.Speaker)})
			}
		}
	}
	a := eval.ScoreAttribution(pairs)
	fmt.Fprintf(os.Stderr, "speaker of words: %.1f%% right (%d of %d words; mapping %v)\n", 100*a.Rate(), a.Correct, a.Words, a.Mapping)
}

func countWords(segs []transcript.Segment) int {
	n := 0
	for _, s := range segs {
		n += len(strings.Fields(s.Text))
	}
	return n
}

// load16k reads a WAV file as 16 kHz mono.
func load16k(path string) ([]float32, float64, error) {
	a, err := wav.ReadFile(path)
	if err != nil {
		return nil, 0, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	mono := dsp.Downmix(nil, a.Samples, a.Channels)
	if a.SampleRate != pipeline.Rate {
		r := dsp.NewResampler(a.SampleRate, pipeline.Rate)
		mono = append(r.Process(mono), r.Flush()...)
	}
	return mono, float64(len(mono)) / pipeline.Rate, nil
}

func ensureModel(ctx context.Context, m models.Model, dir string) error {
	f := download.File{URL: m.URL(), Dest: m.Path(dir), SHA256: m.SHA256, Size: m.Size}
	if _, err := os.Stat(f.Dest); err == nil {
		return nil // present; hash checked when downloaded
	}
	fmt.Fprintf(os.Stderr, "downloading %s (%.0f MB)\n", m.Name, float64(m.Size)/1e6)
	err := download.Client{UserAgent: "flit"}.Get(ctx, f, func(done, total int64) {
		fmt.Fprintf(os.Stderr, "\r  %.0f%%", 100*float64(done)/float64(max(total, 1)))
	})
	fmt.Fprintln(os.Stderr)
	return err
}

// engineDir is where the worker loads the DLLs from: FLIT_ENGINE_DIR, or
// the embedded engine unpacked into the data folder.
func engineDir(dataDir string) (string, error) {
	if d := os.Getenv("FLIT_ENGINE_DIR"); d != "" {
		return d, nil
	}
	d, err := native.Unpack(dataDir)
	if errors.Is(err, native.ErrMissing) {
		return "", fmt.Errorf("%w; or set FLIT_ENGINE_DIR", err)
	}
	return d, err
}

// simulateMeeting feeds both tracks as a meeting would, in real time,
// stepping live transcription every `step` seconds, and logs after each
// step how far behind it is and the engine's CPU use since the last one;
// then the final step. For measuring live transcription under a CPU cap.
func simulateMeeting(ctx context.Context, sup *engine.Supervisor, me, them []float32, opt pipeline.Options, step int) (meSegs, themSegs []transcript.Segment, err error) {
	ps := []*pipeline.Progressive{{Track: transcript.Me}, {Track: transcript.Them}}
	pcms := [][]float32{me, them}
	length := max(len(me), len(them))
	start := time.Now()
	var meter sysinfo.CPUMeter
	var peak float64
	for n := step * pipeline.Rate; n < length; n += step * pipeline.Rate {
		if wait := time.Duration(n)*time.Second/pipeline.Rate - time.Since(start); wait > 0 {
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			}
		}
		t0 := time.Now()
		for i, p := range ps {
			if pcms[i] == nil {
				continue
			}
			if _, err := p.Step(ctx, sup, pipeline.Samples(pcms[i][:min(n, len(pcms[i]))]), false, opt, nil, nil); err != nil {
				return nil, nil, err
			}
		}
		cpu := 0.0
		if pid := sup.PID(); pid > 0 {
			if st, err := sysinfo.Process(uint32(pid)); err == nil {
				cpu = meter.Percent(st.CPU, time.Now())
			}
		}
		peak = max(peak, cpu)
		behind := float64(n-int(min(ps[0].Done, ps[1].Done))) / pipeline.Rate
		if pcms[0] == nil {
			behind = float64(n-int(ps[1].Done)) / pipeline.Rate
		}
		fmt.Fprintf(os.Stderr, "%5.0f s fed: behind %4.0f s, step %4.1f s, engine CPU %4.1f%% of the machine\n", float64(n)/pipeline.Rate, behind, time.Since(t0).Seconds(), cpu)
	}
	t0 := time.Now()
	left := 0
	for i, p := range ps {
		if pcms[i] == nil {
			continue
		}
		k, err := p.Step(ctx, sup, pipeline.Samples(pcms[i]), true, opt, nil, nil)
		if err != nil {
			return nil, nil, err
		}
		left += k
	}
	fmt.Fprintf(os.Stderr, "meeting over: %d chunk(s) left, done %.1f s after the end; engine CPU peaked at %.1f%%\n", left, time.Since(t0).Seconds(), peak)
	return ps[0].Segments, ps[1].Segments, nil
}
