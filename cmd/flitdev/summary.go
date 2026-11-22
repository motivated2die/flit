package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/motivated2die/flit/internal/eval"
	"github.com/motivated2die/flit/internal/llm"
	"github.com/motivated2die/flit/internal/models"
	"github.com/motivated2die/flit/internal/notes"
	"github.com/motivated2die/flit/internal/session"
	"github.com/motivated2die/flit/internal/summary"
	"github.com/motivated2die/flit/internal/sysinfo"
	"github.com/motivated2die/flit/internal/transcript"
)

// summaryCmd writes summaries of a recording or a test meeting with each
// of the given models, devices and approaches, and prints what each took:
//
//	go run ./cmd/flitdev summary -session testdata/private/interview \
//	    -model qwen3.5-2b,qwen3.5-4b -device gpu,cpu -approach single,extract -out .cache/summaries
//
// Summaries, prompts and figures go to -out; nothing is logged.
func summaryCmd(ctx context.Context, root string, args []string) error {
	fs := flag.NewFlagSet("summary", flag.ExitOnError)
	sessionDir := fs.String("session", "", "a flit session folder (transcript.json, speakers.json, notes.md)")
	meetingDir := fs.String("meeting", "", "a test meeting folder (meeting.json); its reference transcript is used")
	notesFile := fs.String("notes", "", "notes Markdown to use instead of the session's (a test meeting has none)")
	modelIDs := fs.String("model", "qwen3.5-2b", "language model IDs, comma-separated")
	devices := fs.String("device", "gpu", "cpu, gpu-prefill, gpu; comma-separated")
	approaches := fs.String("approach", "extract", "single, extract; comma-separated")
	threads := fs.Int("threads", 0, "CPU threads (0: llama.cpp's default)")
	cpuLimit := fs.Int("cpulimit", 0, "cap the server at this percent of all processors (0: none)")
	section := fs.Int("section", 0, "extract: tokens per section (0: default)")
	ctxSize := fs.Int("ctx", 0, "context size (0: 8192 for extract, the meeting's size for single)")
	me := fs.String("me", "", "the user's name in the summary (default Me)")
	meNames := fs.String("me-names", "", "words that name the user, comma-separated")
	instruction := fs.String("instruction", "", "the user's extra instruction")
	noCues := fs.Bool("nocues", false, "leave out cue marks")
	seed := fs.Int("seed", 42, "sampling seed")
	speed := fs.Bool("speed", false, "only measure reading and writing speed, no summary")
	limit := fs.Duration("limit", 0, "use only the first part of the transcript (e.g. 10m)")
	out := fs.String("out", filepath.Join(root, ".cache", "summaries"), "where summaries and figures go")
	fs.Parse(args)

	in, name, err := summaryInput(*sessionDir, *meetingDir, *notesFile)
	if err != nil {
		return err
	}
	if *limit > 0 {
		var segs []transcript.Segment
		for _, s := range in.Segments {
			if s.Start < *limit {
				segs = append(segs, s)
			}
		}
		in.Segments = segs
	}
	in.Me, in.Instruction = *me, *instruction
	if *meNames != "" {
		in.MeNames = strings.Split(*meNames, ",")
	}
	length := in.Segments[len(in.Segments)-1].End
	exe := filepath.Join(runtimeDir(root), llm.ServerExe)
	if _, err := os.Stat(exe); err != nil {
		return fmt.Errorf("no llama-server: run `go run ./cmd/flitdev llm` first")
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		return err
	}
	fmt.Printf("%s: %s, %d segments\n", name, transcript.Clock(length), len(in.Segments))
	for _, id := range strings.Split(*modelIDs, ",") {
		m, ok := models.ByID(id)
		if !ok || m.Role != models.RoleLLM {
			return fmt.Errorf("unknown language model %q", id)
		}
		path := m.Path(filepath.Join(root, "models"))
		for _, dv := range strings.Split(*devices, ",") {
			dev, err := llm.ParseDevice(dv)
			if err != nil {
				return err
			}
			for _, ap := range strings.Split(*approaches, ",") {
				n := *ctxSize
				if n == 0 {
					n = 8192
					if summary.Approach(ap) == summary.Single {
						// The meeting at ~1 token per 3 characters, plus the
						// prompt and the answer.
						n = max(8192, roundUp(transcriptChars(in)/3+3000, 1024))
					}
				}
				srv, err, first := llm.StartFallback(ctx, llm.Options{Exe: exe, Model: path, Context: n, Threads: *threads, Device: dev, CPULimit: *cpuLimit, Background: *cpuLimit > 0,
					Logf: func(f string, a ...any) {}})
				if err != nil {
					return fmt.Errorf("%s on %s: %w", id, dev, err)
				}
				if first != nil {
					fmt.Printf("   %s failed (%v): on the CPU instead\n", dev, first)
					dev = srv.Options.Device
				}
				if *speed {
					sp, err := llm.MeasureSpeed(ctx, &srv.Client)
					srv.Close()
					if err != nil {
						return err
					}
					fmt.Printf("== %s · %s · threads %d: reads %.0f tokens/s, writes %.1f tokens/s (loaded in %s)\n", id, dev, *threads, sp.Prompt, sp.Predict, srv.Loaded.Round(100*time.Millisecond))
					break
				}
				var peak atomic.Uint64
				stop := make(chan struct{})
				go watchMemory(uint32(srv.PID()), &peak, stop)
				opt := summary.Options{Approach: summary.Approach(ap), SectionTokens: *section, NoCues: *noCues, Seed: *seed,
					Progress: func(step string, done, total int) { fmt.Printf("    %s %d/%d\n", step, done+1, total) }}
				fmt.Printf("== %s · %s · %s · ctx %d (loaded in %s)\n", id, dev, ap, n, srv.Loaded.Round(100*time.Millisecond))
				start := time.Now()
				res, err := summary.Run(ctx, &srv.Client, in, opt)
				wall := time.Since(start)
				close(stop)
				srv.Close()
				tag := fmt.Sprintf("%s_%s_%s_%s", name, id, dev, ap)
				if *noCues {
					tag += "_nocues"
				}
				report(res, wall, length, peak.Load(), tag, *out)
				if err != nil {
					fmt.Printf("   failed: %v\n", err)
					continue
				}
			}
		}
	}
	return nil
}

func roundUp(n, to int) int { return (n + to - 1) / to * to }

func transcriptChars(in summary.Input) int {
	n := 0
	for _, s := range in.Segments {
		n += len([]rune(s.Text)) + 10
	}
	return n
}

func watchMemory(pid uint32, peak *atomic.Uint64, stop chan struct{}) {
	t := time.NewTicker(200 * time.Millisecond)
	defer t.Stop()
	for {
		if st, err := sysinfo.Process(pid); err == nil && st.WorkingSet > peak.Load() {
			peak.Store(st.WorkingSet)
		}
		select {
		case <-stop:
			return
		case <-t.C:
		}
	}
}

func report(res summary.Result, wall, length time.Duration, peak uint64, tag, out string) {
	t := res.Total()
	fmt.Printf("   %s, %d turns, %d tokens, %d section(s), %d dropped\n", res.Language, res.Turns, res.Tokens, len(res.Sections), res.Dropped)
	for _, c := range res.Calls {
		fmt.Printf("   %-10s read %5d (+%d cached) at %6.0f t/s · wrote %4d at %5.1f t/s · %5.1fs · %s\n", c.Step, c.Timings.PromptN, c.Timings.CacheN,
			c.Timings.PromptPerSecond(), c.Timings.PredictedN, c.Timings.PredictedPerSecond(), c.Wall.Seconds(), c.Finish)
	}
	perHour := time.Duration(float64(wall) * float64(time.Hour) / float64(length))
	fmt.Printf("   total %s: read %d in %.1fs, wrote %d in %.1fs; peak memory %s; %s per hour of meeting (linear)\n",
		wall.Round(100*time.Millisecond), t.PromptN, t.PromptMS/1000, t.PredictedN, t.PredictedMS/1000, sysinfo.Bytes(peak), perHour.Round(time.Second))
	os.WriteFile(filepath.Join(out, tag+".md"), []byte(res.Markdown+"\n"), 0o644)
	var p strings.Builder
	for i, s := range res.Prompts {
		fmt.Fprintf(&p, "===== prompt %d =====\n%s\n", i+1, s)
	}
	os.WriteFile(filepath.Join(out, tag+".prompts.txt"), []byte(p.String()), 0o644)
	figures := map[string]any{"wall_s": wall.Seconds(), "meeting_s": length.Seconds(), "tokens": res.Tokens, "sections": len(res.Sections),
		"dropped": res.Dropped, "calls": res.Calls, "peak_bytes": peak, "items": res.Items, "language": res.Language}
	b, _ := json.MarshalIndent(figures, "", " ")
	os.WriteFile(filepath.Join(out, tag+".json"), b, 0o644)
}

// summaryInput reads a flit session or a test meeting.
func summaryInput(sessionDir, meetingDir, notesFile string) (summary.Input, string, error) {
	var in summary.Input
	var name string
	switch {
	case sessionDir != "":
		s, err := session.Load(sessionDir)
		if err != nil {
			return in, "", err
		}
		segs, err := s.Segments()
		if err != nil {
			return in, "", err
		}
		d, err := s.Notes()
		if err != nil {
			return in, "", err
		}
		in = summary.Input{Title: s.Meta.Title, Segments: segs, Names: s.Names(), Notes: d, Language: s.Meta.Language}
		name = filepath.Base(sessionDir)
	case meetingDir != "":
		m, err := eval.Load(meetingDir)
		if err != nil {
			return in, "", err
		}
		names := transcript.Names{Speakers: map[int]string{}}
		num := map[string]int{}
		for _, sp := range m.Speakers {
			if sp.Track == "them" {
				num[sp.ID] = len(num) + 1
				names.Speakers[num[sp.ID]] = sp.Label
			}
		}
		var segs []transcript.Segment
		for _, r := range m.Segments {
			seg := transcript.Segment{Track: transcript.Them, Speaker: num[r.Speaker], Start: r.Start, End: r.End, Text: r.Text}
			if m.TrackOf(r.Speaker) == "me" {
				seg.Track, seg.Speaker = transcript.Me, 0
			}
			segs = append(segs, seg)
		}
		in = summary.Input{Title: m.Name, Segments: transcript.Merge(segs), Names: names, Language: m.Language}
		name = m.Name
	default:
		return in, "", fmt.Errorf("give -session or -meeting")
	}
	if notesFile != "" {
		b, err := os.ReadFile(notesFile)
		if err != nil {
			return in, "", err
		}
		in.Notes = notes.Parse(string(b))
		if tb, err := os.ReadFile(strings.TrimSuffix(notesFile, ".md") + ".json"); err == nil {
			var t notes.Times
			if json.Unmarshal(tb, &t) == nil {
				in.Notes.ApplyTimes(t)
			}
		}
	}
	if len(in.Segments) == 0 {
		return in, "", summary.ErrEmpty
	}
	return in, name, nil
}
