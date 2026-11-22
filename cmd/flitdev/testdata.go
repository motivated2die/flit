package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/motivated2die/flit/internal/audio/wav"
	"github.com/motivated2die/flit/internal/download"
	"github.com/motivated2die/flit/internal/eval"
)

// Test meetings (docs/testing/test-data.md):
//
//	ami-IS1009a        a real 14-minute meeting of 4 people, each on their own headset (English)
//	ami-IS1009a-5min   its minutes 0:50–5:50, for quick tests
//	de-synth-4spk      a 5-minute German "meeting" assembled from read speech of 4 people,
//	                   with exact references, overlaps and a bleed variant of the mic track
func fetchTestdata(ctx context.Context, root string, args []string) error {
	out := filepath.Join(root, "testdata", "meetings")
	if err := buildAMI(ctx, root, out); err != nil {
		return fmt.Errorf("AMI: %w", err)
	}
	if err := buildGermanSynth(ctx, root, out); err != nil {
		return fmt.Errorf("German synthetic meeting: %w", err)
	}
	fmt.Println("test meetings in", out)
	return nil
}

// --- AMI ---------------------------------------------------------------

const (
	amiMeeting = "IS1009a"
	amiAudio   = "https://groups.inf.ed.ac.uk/ami/AMICorpusMirror/amicorpus/IS1009a/audio/"
	amiSetup   = "https://raw.githubusercontent.com/pyannote/AMI-diarization-setup/main/"
)

// The files, pinned to what was downloaded on 2026-11-21.
var amiFiles = []download.File{
	{URL: amiAudio + "IS1009a.Headset-0.wav", SHA256: "634f42207e488b0eab3b34a81ff16d565705f8fc5e37be7cc3c3b56617ae3257", Size: 26843921},
	{URL: amiAudio + "IS1009a.Headset-1.wav", SHA256: "db98d175dda0d07613795c715667a72b99c5372aabd14ee4dcb94d1fa9c273ee", Size: 26843921},
	{URL: amiAudio + "IS1009a.Headset-2.wav", SHA256: "9e28bfd55727b07d6a6020b7bf2797d113b35c69a1a3ad25cc4150be0937bf28", Size: 26843921},
	{URL: amiAudio + "IS1009a.Headset-3.wav", SHA256: "7a0b041ad2f2cd9af1c8fcee08e598d6d88bd8f77a3174d7eb9e281a30ca0413", Size: 26843921},
	{URL: amiAudio + "IS1009a.Mix-Headset.wav", SHA256: "6eb5a0ede0d9e72794f976ce7bea5b78133eae969f99b4c5418b43c2468d25b1", Size: 26844093},
	{URL: amiSetup + "only_words/rttms/test/IS1009a.rttm", SHA256: "ba38d35ca567f3f1e061d90fdc33579ce60d2060fc8664eaeb77d2e0fcd88b01", Size: 11080},
	{URL: "https://groups.inf.ed.ac.uk/ami/AMICorpusAnnotations/ami_public_manual_1.6.2.zip", SHA256: "b56e5babb2496b8795deeeda7e71178d7fbc9963f94276cf2a3f4b56ebbc9f9d", Size: 22887865},
}

// The participants of IS1009a by headset channel (AMI corpusResources/
// meetings.xml): channel 0, the project manager, is "Me".
var amiSpeakers = []struct{ agent, id, role string }{
	{"A", "FIE088", "project manager"},
	{"B", "FIO087", "user interface designer"},
	{"C", "FIO084", "industrial designer"},
	{"D", "FIO089", "marketing expert"},
}

const amiAttribution = "AMI Meeting Corpus (Carletta et al., 2005), meeting IS1009a, https://groups.inf.ed.ac.uk/ami/corpus/ — CC BY 4.0. Speaker reference: pyannote/AMI-diarization-setup (only_words)."

func buildAMI(ctx context.Context, root, out string) error {
	cache := filepath.Join(root, ".cache", "ami")
	paths := map[string]string{}
	for _, f := range amiFiles {
		f.Dest = filepath.Join(cache, filepath.Base(f.URL))
		if err := get(ctx, f); err != nil {
			return err
		}
		paths[filepath.Base(f.URL)] = f.Dest
	}
	headsets := make([]wav.Audio, 4)
	for i := range headsets {
		a, err := wav.ReadFile(paths[fmt.Sprintf("IS1009a.Headset-%d.wav", i)])
		if err != nil {
			return err
		}
		if a.SampleRate != 16000 || a.Channels != 1 {
			return fmt.Errorf("headset %d: %+v", i, a.Format)
		}
		headsets[i] = a
	}
	mix, err := wav.ReadFile(paths["IS1009a.Mix-Headset.wav"])
	if err != nil {
		return err
	}
	f, err := os.Open(paths["IS1009a.rttm"])
	if err != nil {
		return err
	}
	turns, err := eval.ReadRTTM(f)
	f.Close()
	if err != nil {
		return err
	}
	// The four sit in one room, so every headset also hears the others. In
	// an online meeting the meeting's track never contains the user, so
	// each headset is kept only while its own wearer speaks (by the
	// reference, padded): clean tracks, as a meeting app delivers them.
	// me-bleed.wav then adds Them to Me deliberately, like loudspeakers.
	for i := range headsets {
		gate(headsets[i].Samples, turns, amiSpeakers[i].id, 16000)
	}
	me := headsets[0].Samples
	them := sum(headsets[1].Samples, headsets[2].Samples, headsets[3].Samples)
	meBleed := addBleed(me, them, 16000)
	segs, err := amiSegments(paths["ami_public_manual_1.6.2.zip"])
	if err != nil {
		return err
	}
	speakers := []eval.Speaker{{ID: amiSpeakers[0].id, Track: "me", Label: "Me"}}
	for i, s := range firstAppearance(segs, amiSpeakers[0].id) {
		speakers = append(speakers, eval.Speaker{ID: s, Track: "them", Label: fmt.Sprintf("Them %d", i+1)})
	}
	total := time.Duration(len(me)) * time.Second / 16000
	for _, cut := range []struct {
		name     string
		from, to time.Duration
	}{
		{"ami-IS1009a", 0, total},
		{"ami-IS1009a-5min", 50 * time.Second, 350 * time.Second},
	} {
		dir := filepath.Join(out, cut.name)
		a, b := int(cut.from.Seconds()*16000), min(len(me), int(cut.to.Seconds()*16000))
		desc := "Real meeting (design team, English, 4 people in one room). Me = the project manager's headset; Them = the other three headsets mixed. Each headset is gated to its wearer's reference turns (crosstalk removed, as in an online meeting's separate tracks); me-bleed.wav adds Them at -18 dB, 30 ms late. mix.wav is AMI's original headset mix."
		if cut.from > 0 {
			desc = fmt.Sprintf("Minutes %s–%s of ami-IS1009a. ", clock(cut.from), clock(cut.to)) + desc
		}
		m := eval.Manifest{
			Name: cut.name, Description: desc, Language: "en",
			Source: "AMI IS1009a", License: "CC BY 4.0", Attribution: amiAttribution,
			Seconds:  float64(b-a) / 16000,
			Tracks:   map[string]string{"me": "me.wav", "them": "them.wav", "mix": "mix.wav", "me-bleed": "me-bleed.wav"},
			Speakers: speakers,
			Segments: clipSegments(segs, cut.from, cut.to),
		}
		if err := writeMeeting(dir, m, map[string][]float32{"me": me[a:b], "them": them[a:b], "mix": mix.Samples[a:min(b, len(mix.Samples))], "me-bleed": meBleed[a:b]}, eval.Clip(turns, cut.from, cut.to)); err != nil {
			return err
		}
		fmt.Printf("  %s: %.0f s, %d reference segments\n", cut.name, m.Seconds, len(m.Segments))
	}
	return nil
}

// amiSegments reads the four participants' words from the AMI manual
// annotations and joins them into segments at pauses of 1 s or more.
func amiSegments(zipPath string) ([]eval.ReferenceSegment, error) {
	z, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, err
	}
	defer z.Close()
	var out []eval.ReferenceSegment
	for _, sp := range amiSpeakers {
		name := fmt.Sprintf("words/%s.%s.words.xml", amiMeeting, sp.agent)
		var words []amiWord
		for _, f := range z.File {
			if f.Name != name {
				continue
			}
			r, err := f.Open()
			if err != nil {
				return nil, err
			}
			words, err = parseAMIWords(r)
			r.Close()
			if err != nil {
				return nil, fmt.Errorf("%s: %w", name, err)
			}
		}
		if len(words) == 0 {
			return nil, fmt.Errorf("%s missing from the annotations", name)
		}
		var cur *eval.ReferenceSegment
		for _, w := range words {
			start, end := secs(w.Start), secs(w.End)
			if w.Punc {
				if cur != nil {
					cur.Text += w.Text
				}
				continue
			}
			if cur == nil || start-cur.End >= time.Second {
				if cur != nil {
					out = append(out, *cur)
				}
				cur = &eval.ReferenceSegment{Speaker: sp.id, Start: start, End: end, Text: w.Text}
				continue
			}
			cur.Text += " " + w.Text
			cur.End = max(cur.End, end)
		}
		if cur != nil {
			out = append(out, *cur)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	return out, nil
}

type amiWord struct {
	Start, End float64
	Punc       bool
	Text       string
}

func parseAMIWords(r io.Reader) ([]amiWord, error) {
	d := xml.NewDecoder(r)
	d.CharsetReader = func(label string, in io.Reader) (io.Reader, error) {
		if !strings.EqualFold(label, "ISO-8859-1") {
			return nil, fmt.Errorf("charset %s", label)
		}
		b, err := io.ReadAll(in)
		if err != nil {
			return nil, err
		}
		runes := make([]rune, len(b))
		for i, c := range b {
			runes[i] = rune(c) // Latin-1 maps 1:1 onto Unicode
		}
		return strings.NewReader(string(runes)), nil
	}
	var out []amiWord
	for {
		tok, err := d.Token()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "w" {
			continue
		}
		var w struct {
			Start string `xml:"starttime,attr"`
			End   string `xml:"endtime,attr"`
			Punc  string `xml:"punc,attr"`
			Text  string `xml:",chardata"`
		}
		if err := d.DecodeElement(&w, &se); err != nil {
			return nil, err
		}
		var aw amiWord
		if _, err := fmt.Sscan(w.Start, &aw.Start); err != nil {
			continue // a few words carry no times
		}
		fmt.Sscan(w.End, &aw.End)
		aw.Punc, aw.Text = w.Punc == "true", strings.TrimSpace(w.Text)
		if aw.Text != "" {
			out = append(out, aw)
		}
	}
}

// --- German synthetic meeting ----------------------------------------------

// Read German speech of four speakers from Multilingual LibriSpeech (its
// 1-hour German subset, through Hugging Face's dataset viewer API, which
// serves single rows), arranged into turns.
var deSpeakers = []string{"13726", "12275", "2314", "8337"} // the first is "Me"

const (
	mlsRows          = "https://datasets-server.huggingface.co/rows?dataset=facebook/multilingual_librispeech&config=german&split=1_hours&offset=%d&length=100"
	mlsAttribution   = "Multilingual LibriSpeech (Pratap et al., 2020), German, from LibriVox audiobooks, https://huggingface.co/datasets/facebook/multilingual_librispeech — CC BY 4.0. Arranged into a synthetic meeting by flit."
	deTurnsPerPerson = 5
)

type mlsRow struct {
	ID         string  `json:"id"`
	Speaker    string  `json:"speaker_id"`
	Transcript string  `json:"transcript"`
	Duration   float64 `json:"audio_duration"`
	Audio      []struct {
		Src string `json:"src"`
	} `json:"audio"`
}

func buildGermanSynth(ctx context.Context, root, out string) error {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		return fmt.Errorf("ffmpeg is needed to decode the Opus clips (winget install ffmpeg): %w", err)
	}
	cache := filepath.Join(root, ".cache", "mls-de")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		return err
	}
	rows, err := mlsGermanRows(ctx)
	if err != nil {
		return err
	}
	// Each speaker's first utterances in id order.
	bySpeaker := map[string][]mlsRow{}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	for _, r := range rows {
		if len(bySpeaker[r.Speaker]) < deTurnsPerPerson && len(r.Audio) > 0 {
			bySpeaker[r.Speaker] = append(bySpeaker[r.Speaker], r)
		}
	}
	clips := map[string][]float32{}
	for _, spk := range deSpeakers {
		if len(bySpeaker[spk]) < deTurnsPerPerson {
			return fmt.Errorf("speaker %s has %d utterances", spk, len(bySpeaker[spk]))
		}
		for _, r := range bySpeaker[spk] {
			pcm, err := mlsClip(ctx, ffmpeg, cache, r)
			if err != nil {
				return err
			}
			clips[r.ID] = normalize(pcm, 0.07)
		}
	}

	// Turns in a seeded random order, never the same speaker twice in a
	// row; pauses of 0.25–1.2 s, and every eighth turn or so starting 0.5 s
	// before the previous one ends (people talking over each other).
	rng := rand.New(rand.NewSource(42))
	var queue []mlsRow
	for _, spk := range deSpeakers {
		queue = append(queue, bySpeaker[spk]...)
	}
	rng.Shuffle(len(queue), func(i, j int) { queue[i], queue[j] = queue[j], queue[i] })
	order := noRepeats(queue, rng)

	const rate = 16000
	at := int(0.5 * rate)
	type placed struct {
		row        mlsRow
		start, end int
	}
	var turns []placed
	prevEnd := at
	for i, r := range order {
		start := prevEnd + int((0.25+rng.Float64()*0.95)*rate)
		if i > 0 && rng.Float64() < 0.125 {
			start = prevEnd - rate/2
		}
		end := start + len(clips[r.ID])
		turns = append(turns, placed{r, start, end})
		prevEnd = max(prevEnd, end)
	}
	n := prevEnd + rate/2
	me, them := make([]float32, n), make([]float32, n)
	for _, t := range turns {
		dst := them
		if t.row.Speaker == deSpeakers[0] {
			dst = me
		}
		for i, v := range clips[t.row.ID] {
			dst[t.start+i] += v
		}
	}
	mix := sum(me, them)
	bleed := addBleed(me, them, rate)

	speakers := []eval.Speaker{{ID: deSpeakers[0], Track: "me", Label: "Me"}}
	var segs []eval.ReferenceSegment
	var rttm []eval.Turn
	label := map[string]string{}
	for _, t := range turns {
		spk := t.row.Speaker
		if spk != deSpeakers[0] && label[spk] == "" {
			label[spk] = fmt.Sprintf("Them %d", len(label)+1)
			speakers = append(speakers, eval.Speaker{ID: spk, Track: "them", Label: label[spk]})
		}
		start, end := time.Duration(t.start)*time.Second/rate, time.Duration(t.end)*time.Second/rate
		segs = append(segs, eval.ReferenceSegment{Speaker: spk, Start: start, End: end, Text: t.row.Transcript})
		rttm = append(rttm, eval.Turn{Speaker: spk, Start: start, Duration: end - start})
	}
	m := eval.Manifest{
		Name:        "de-synth-4spk",
		Description: "Synthetic German meeting: read speech (audiobooks) of 4 people arranged into 20 turns with short pauses and some overlaps. Me = one speaker; Them = the other three. me-bleed.wav adds Them at -18 dB, 30 ms late, as heard from loudspeakers without a headset. References are exact.",
		Language:    "de", Source: "Multilingual LibriSpeech, German, 1-hour subset", License: "CC BY 4.0", Attribution: mlsAttribution,
		Seconds:  float64(n) / rate,
		Tracks:   map[string]string{"me": "me.wav", "them": "them.wav", "mix": "mix.wav", "me-bleed": "me-bleed.wav"},
		Speakers: speakers, Segments: segs,
	}
	dir := filepath.Join(out, m.Name)
	if err := writeMeeting(dir, m, map[string][]float32{"me": me, "them": them, "mix": mix, "me-bleed": bleed}, rttm); err != nil {
		return err
	}
	fmt.Printf("  %s: %.0f s, %d turns\n", m.Name, m.Seconds, len(turns))
	return nil
}

func mlsGermanRows(ctx context.Context) ([]mlsRow, error) {
	var rows []mlsRow
	for off := 0; off < 300; off += 100 {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf(mlsRows, off), nil)
		req.Header.Set("User-Agent", "flitdev")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		var page struct {
			Rows []struct {
				Row mlsRow `json:"row"`
			} `json:"rows"`
		}
		err = json.NewDecoder(resp.Body).Decode(&page)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		for _, r := range page.Rows {
			rows = append(rows, r.Row)
		}
		if len(page.Rows) < 100 {
			break
		}
	}
	return rows, nil
}

// mlsClip returns an utterance as 16 kHz mono, downloading and decoding it
// once (cached by id).
func mlsClip(ctx context.Context, ffmpeg, cache string, r mlsRow) ([]float32, error) {
	wavPath := filepath.Join(cache, r.ID+".wav")
	if a, err := wav.ReadFile(wavPath); err == nil {
		return a.Samples, nil
	}
	opus := filepath.Join(cache, r.ID+".opus")
	if err := client.Get(ctx, download.File{URL: r.Audio[0].Src, Dest: opus}, nil); err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, ffmpeg, "-loglevel", "error", "-y", "-i", opus, "-ar", "16000", "-ac", "1", "-c:a", "pcm_s16le", wavPath)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg %s: %v %s", r.ID, err, stderr.String())
	}
	a, err := wav.ReadFile(wavPath)
	if err != nil {
		return nil, err
	}
	return a.Samples, nil
}

// noRepeats reorders rows so no speaker speaks twice in a row, keeping the
// shuffled order where it can.
func noRepeats(rows []mlsRow, rng *rand.Rand) []mlsRow {
	for attempt := 0; attempt < 1000; attempt++ {
		ok := true
		for i := 1; i < len(rows); i++ {
			if rows[i].Speaker == rows[i-1].Speaker {
				j := rng.Intn(len(rows))
				rows[i], rows[j] = rows[j], rows[i]
				ok = false
			}
		}
		if ok {
			break
		}
	}
	return rows
}

// --- shared -----------------------------------------------------------------

// addBleed returns me with them leaking in, as a mic hears loudspeakers
// without a headset: -18 dB, 30 ms late.
func addBleed(me, them []float32, rate int) []float32 {
	out := append([]float32(nil), me...)
	delay, gain := int(0.03*float64(rate)), float32(math.Pow(10, -18.0/20))
	for i := delay; i < len(out) && i-delay < len(them); i++ {
		out[i] += gain * them[i-delay]
	}
	return out
}

// gate silences pcm outside the speaker's turns (padded by 0.25 s), with
// 10 ms fades so the cuts don't click.
func gate(pcm []float32, turns []eval.Turn, speaker string, rate int) {
	keep := make([]float32, len(pcm))
	pad, fade := rate/4, rate/100
	for _, t := range turns {
		if t.Speaker != speaker {
			continue
		}
		a := max(0, int(t.Start.Seconds()*float64(rate))-pad)
		b := min(len(pcm), int(t.End().Seconds()*float64(rate))+pad)
		for i := a; i < b; i++ {
			g := float32(1)
			if d := min(i-a, b-1-i); d < fade {
				g = float32(d) / float32(fade)
			}
			keep[i] = max(keep[i], g)
		}
	}
	for i := range pcm {
		pcm[i] *= keep[i]
	}
}

func writeMeeting(dir string, m eval.Manifest, tracks map[string][]float32, turns []eval.Turn) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for role, file := range m.Tracks {
		w, err := wav.Create(filepath.Join(dir, file), wav.Format{SampleRate: 16000, Channels: 1})
		if err != nil {
			return err
		}
		if err := w.Write(tracks[role]); err != nil {
			w.Close()
			return err
		}
		if err := w.Close(); err != nil {
			return err
		}
	}
	var b bytes.Buffer
	if err := eval.WriteRTTM(&b, m.Name, turns); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "reference.rttm"), b.Bytes(), 0o644); err != nil {
		return err
	}
	return m.Save(dir)
}

func clipSegments(segs []eval.ReferenceSegment, from, to time.Duration) []eval.ReferenceSegment {
	var out []eval.ReferenceSegment
	for _, s := range segs {
		// Keep segments that lie mostly inside the excerpt.
		if s.Start >= from && s.End <= to+time.Second {
			s.Start -= from
			s.End = min(s.End, to) - from
			out = append(out, s)
		}
	}
	return out
}

func firstAppearance(segs []eval.ReferenceSegment, except string) []string {
	var order []string
	seen := map[string]bool{except: true}
	for _, s := range segs {
		if !seen[s.Speaker] {
			seen[s.Speaker] = true
			order = append(order, s.Speaker)
		}
	}
	return order
}

// sum adds tracks of equal length, scaled down only if the sum would clip.
func sum(tracks ...[]float32) []float32 {
	n := 0
	for _, t := range tracks {
		n = max(n, len(t))
	}
	out := make([]float32, n)
	for _, t := range tracks {
		for i, v := range t {
			out[i] += v
		}
	}
	var peak float32
	for _, v := range out {
		peak = max(peak, float32(math.Abs(float64(v))))
	}
	if peak > 0.98 {
		g := 0.98 / peak
		for i := range out {
			out[i] *= g
		}
	}
	return out
}

// normalize scales pcm to an RMS level.
func normalize(pcm []float32, rms float64) []float32 {
	var e float64
	for _, v := range pcm {
		e += float64(v) * float64(v)
	}
	if e == 0 {
		return pcm
	}
	g := float32(rms / math.Sqrt(e/float64(len(pcm))))
	out := make([]float32, len(pcm))
	for i, v := range pcm {
		out[i] = max(-1, min(1, v*g))
	}
	return out
}

func secs(s float64) time.Duration { return time.Duration(s * float64(time.Second)) }

func clock(d time.Duration) string {
	s := int(d.Seconds())
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}
