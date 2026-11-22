package app

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/motivated2die/flit/internal/capture"
	"github.com/motivated2die/flit/internal/config"
	"github.com/motivated2die/flit/internal/engine"
	"github.com/motivated2die/flit/internal/install"
	"github.com/motivated2die/flit/internal/models"
	"github.com/motivated2die/flit/internal/pipeline"
	"github.com/motivated2die/flit/internal/session"
)

// runRecord is `flit record`: a session recorded and transcribed exactly as
// the tray app does it, on the console. For trying flit by hand and testing
// a real setup (devices, meeting apps) without the UI.
//
//	flit record --seconds 30              # mic + everything the PC plays
//	flit record --pid 1234 --seconds 600  # mic + one app's process tree
func runRecord(args []string) int {
	fs := flag.NewFlagSet("record", flag.ContinueOnError)
	seconds := fs.Int("seconds", 30, "how long to record (Ctrl+C stops earlier)")
	pid := fs.Uint("pid", 0, "record this process tree instead of all audio")
	lang := fs.String("lang", "", "meeting language (default from config)")
	modelID := fs.String("model", "", "ASR model id")
	modelsFlag := fs.String("models", "", "models folder (default: the data folder's models)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	dataDir := config.Dir()
	cfg, _ := config.Load(dataDir)
	if *lang == "" {
		*lang = cfg.Language
	}
	mdir := *modelsFlag
	if mdir == "" {
		mdir = modelsDir(dataDir)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	them := capture.Source{Kind: capture.Loopback}
	if *pid != 0 {
		them = capture.Source{Kind: capture.Process, PID: uint32(*pid)}
	}
	s, err := session.Create(dataDir, session.Meta{Language: *lang, Model: *modelID, Title: "Test recording"})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	r, err := capture.Record(s.Dir, session.MeFile, session.ThemFile, capture.Source{Kind: capture.Mic, DeviceID: cfg.MicID}, them,
		func(ev capture.Event) { fmt.Fprintf(os.Stderr, "capture %s: %s %v\n", ev.Source, ev.Device, ev.Err) })
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "recording %s for %d s…\n", s.Dir, *seconds)
	select {
	case <-ctx.Done():
	case <-time.After(time.Duration(*seconds) * time.Second):
	}
	_ = r.Stop()
	s.Meta.Ended, s.Meta.State, s.Meta.ThemSource = time.Now(), session.Recorded, "process"
	if r.ThemSource.Kind == capture.Loopback {
		s.Meta.ThemSource = "loopback"
	}
	_ = s.Save()
	stop()

	asr, ok := models.ByID(*modelID)
	if !ok {
		asr, _ = models.Default(models.RoleASR, *lang)
	}
	vad, _ := models.Default(models.RoleVAD, "")
	for _, m := range []models.Model{asr, vad} {
		if err := ensureModel(context.Background(), m, mdir); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	engineDir, err := engineDir(dataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	exe, _ := install.Current()
	sup := &engine.Supervisor{Command: engine.WorkerCommand(exe), Spec: engine.LoadSpec{EngineDir: engineDir, ASRModel: asr.Path(mdir), VADModel: vad.Path(mdir), CPUOnly: true}}
	defer sup.Close()
	start := time.Now()
	err = s.Transcribe(context.Background(), sup, pipeline.Options{Language: *lang, Vocabulary: cfg.Vocabulary}, "", func(p float64) {
		fmt.Fprintf(os.Stderr, "\rtranscribing %3.0f%%", p*100)
	})
	fmt.Fprintf(os.Stderr, "\ntranscribed in %.1f s\n", time.Since(start).Seconds())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	text, _ := s.Transcript(false)
	fmt.Print(text)
	return 0
}
