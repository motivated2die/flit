package app

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/motivated2die/flit/internal/capture"
	"github.com/motivated2die/flit/internal/coreaudio"
	"github.com/motivated2die/flit/internal/detect"
	"github.com/motivated2die/flit/internal/logx"
	"github.com/motivated2die/flit/internal/ui"
)

// The Audio page: which microphone and which output flit records, each
// with a live level meter, and which apps use a microphone right now, as
// detection sees them. It is where a silent track gets explained.

const pageAudio = "audio"

// devices is what the Audio page shows, read when it opens.
type devices struct {
	mics, outputs  []coreaudio.Endpoint
	defMic, defOut string // Windows' default devices (console role)
	names          map[string]string
}

func (a *App) loadDevices() {
	var d devices
	d.names = map[string]string{}
	err := coreaudio.Run(func() error {
		en, err := coreaudio.NewEnumerator()
		if err != nil {
			return err
		}
		defer en.Release()
		if d.mics, err = en.Endpoints(coreaudio.Capture); err != nil {
			return err
		}
		if d.outputs, err = en.Endpoints(coreaudio.Render); err != nil {
			return err
		}
		d.defMic = en.DefaultID(coreaudio.Capture, coreaudio.Console)
		d.defOut = en.DefaultID(coreaudio.Render, coreaudio.Console)
		return nil
	})
	if err != nil {
		logx.Printf("listing audio devices: %v", err)
	}
	for _, l := range [][]coreaudio.Endpoint{d.mics, d.outputs} {
		sort.SliceStable(l, func(i, j int) bool { return strings.ToLower(l[i].Name) < strings.ToLower(l[j].Name) })
		for _, e := range l {
			d.names[e.ID] = e.Name
		}
	}
	a.devs = d
}

// syncMeters runs the level meters while the Audio page is on screen and
// stops them otherwise (a microphone meter records, and Windows shows its
// microphone-in-use icon meanwhile).
func (a *App) syncMeters() {
	want := a.win != nil && a.win.Shown() && a.page == pageAudio
	if !want {
		for k, m := range a.audioMeters {
			m.Close()
			delete(a.audioMeters, k)
		}
		return
	}
	if a.audioMeters == nil {
		a.audioMeters = map[string]*capture.Meter{}
		a.audioLevels = map[string]float32{}
	}
	open := func(key string, src capture.Source) {
		if _, ok := a.audioMeters[key]; ok {
			return
		}
		m, err := capture.OpenMeter(src)
		if err != nil {
			logx.Printf("meter %s: %v", key, err)
			return
		}
		a.audioMeters[key] = m
	}
	for _, e := range a.devs.mics {
		open("in:"+e.ID, capture.Source{Kind: capture.Mic, DeviceID: e.ID})
	}
	for _, e := range a.devs.outputs {
		open("out:"+e.ID, capture.Source{Kind: capture.Loopback, DeviceID: e.ID})
	}
}

// level is a device's meter level, smoothed so it falls gently.
func (a *App) level(key string) float32 {
	m := a.audioMeters[key]
	if m == nil {
		return 0
	}
	v := max(ui.LevelMeter(m.TakePeak()), a.audioLevels[key]*0.85)
	a.audioLevels[key] = v
	return v
}

func (a *App) audioPage() ui.Node {
	d := a.devs
	defName := func(id string) string {
		if n := d.names[id]; n != "" {
			return n
		}
		return "none"
	}
	tags := func(e coreaudio.Endpoint, def string) string {
		switch {
		case e.ID == def && e.Default:
			return "Windows default, also for calls"
		case e.ID == def:
			return "Windows default"
		case e.Default:
			return "Windows default for calls"
		}
		return ""
	}

	mics := []ui.Node{ui.ChoiceRow{ID: "mic:auto", Title: "Automatic",
		Detail:   "The meeting app's microphone, else Windows' default: " + defName(d.defMic),
		Selected: a.cfg.MicID == "", Level: -1, OnClick: func() { a.setMic("", "") }}}
	for _, e := range d.mics {
		e := e
		mics = append(mics, ui.ChoiceRow{ID: "mic:" + e.ID, Title: e.Name, Detail: a.recordingNow(tags(e, d.defMic), "me", e.ID),
			Selected: a.cfg.MicID == e.ID, Level: a.level("in:" + e.ID), OnClick: func() { a.setMic(e.ID, e.Name) }})
	}
	outs := []ui.Node{ui.ChoiceRow{ID: "out:auto", Title: "Automatic",
		Detail:   "Windows' default: " + defName(d.defOut),
		Selected: a.cfg.OutputID == "", Level: -1, OnClick: func() { a.setOutput("", "") }}}
	for _, e := range d.outputs {
		e := e
		outs = append(outs, ui.ChoiceRow{ID: "out:" + e.ID, Title: e.Name, Detail: a.recordingNow(tags(e, d.defOut), "them", e.ID),
			Selected: a.cfg.OutputID == e.ID, Level: a.level("out:" + e.ID), OnClick: func() { a.setOutput(e.ID, e.Name) }})
	}

	return ui.VStack{Gap: 12, Items: []ui.Node{
		ui.Title("Audio"),
		ui.Para{Text: "Speak, and play something: the bars show which device hears it. Choosing another device during a recording switches the recording over. While this page is open, Windows shows that flit uses the microphone.", Muted: true},
		ui.HStack{Items: []ui.Node{ui.Link{ID: "audio:reload", Glyph: ui.GlyphRefresh, Label: "Look for devices again", OnClick: func() { a.reloadDevices() }}}},
		ui.Caption("Your microphone (Me)"),
		ui.Card{Gap: 0, Items: mics},
		ui.Caption("What this PC plays (Them)"),
		ui.Para{Text: "A detected meeting is recorded from the meeting app itself, whatever device it plays to. This choice is for recording by hand, and for when that isn't possible.", Muted: true, Small: true},
		ui.Card{Gap: 0, Items: outs},
		ui.Caption("Apps using a microphone now"),
		a.micUsersCard(),
	}}
}

func (a *App) micUsersCard() ui.Node {
	var users []detect.MicUser
	if a.watcher != nil {
		users = a.watcher.MicUsers()
	}
	if len(users) == 0 {
		return ui.Card{Items: []ui.Node{ui.Para{Text: "None. When a meeting starts, its app appears here; if it doesn't, flit can't notice the meeting.", Muted: true}}}
	}
	var rows []ui.Node
	for _, u := range users {
		title, state := u.Exe, "Not a meeting app"
		if u.Provider != "" {
			p, _ := detect.ProviderByID(u.Provider)
			title = p.Name
			state = "Meeting app"
			if !a.cfg.ProviderEnabled(p.ID, p.On) {
				state = "Meeting app, off in Settings"
			}
		}
		mic := a.devs.names[u.Mic]
		if mic == "" {
			mic = "a microphone"
		}
		rows = append(rows, ui.ListRow{ID: fmt.Sprintf("user:%d", u.PID), Glyph: ui.GlyphMic, Title: title,
			Detail: fmt.Sprintf("%s (process %d) uses %s", u.Exe, u.PID, mic), Trailing: state})
	}
	return ui.Card{Gap: 0, Items: rows}
}

func (a *App) setMic(id, name string) {
	a.cfg.MicID, a.cfg.MicName = id, name
	a.save()
	a.switchMic() // a running recording follows
	a.invalidate()
}

func (a *App) setOutput(id, name string) {
	a.cfg.OutputID, a.cfg.OutputName = id, name
	a.save()
	a.switchOutput()
	a.invalidate()
}

// recordingNow marks the device a running recording records from.
func (a *App) recordingNow(detail string, track string, id string) string {
	if a.rec == nil {
		return detail
	}
	cur := a.rec.r.MeSource.DeviceID
	def := a.devs.defMic
	if track == "them" {
		if a.rec.r.ThemSource.Kind != capture.Loopback {
			return detail
		}
		cur, def = a.rec.r.ThemSource.DeviceID, a.devs.defOut
	}
	if cur == "" {
		cur = def
	}
	if id != cur {
		return detail
	}
	if detail == "" {
		return "Recording now"
	}
	return "Recording now · " + detail
}

func (a *App) reloadDevices() {
	for k, m := range a.audioMeters {
		m.Close()
		delete(a.audioMeters, k)
	}
	a.loadDevices()
	a.syncMeters()
	a.invalidate()
}

// micSource is the microphone to record for a meeting (c nil: by hand).
func (a *App) micSource(c *detect.Candidate) capture.Source {
	src := capture.Source{Kind: capture.Mic, DeviceID: a.cfg.MicID, DeviceName: a.cfg.MicName}
	if a.cfg.MicID == "" && c != nil && c.Mic != "" {
		src.DeviceID = c.Mic // what the meeting hears
	}
	return src
}

// silentDB is the level below which a recorded track counts as silent.
const silentDB = -70 // a real microphone's noise floor peaks well above this

// checkLevels logs a finished recording's loudest moments and warns when
// the microphone recorded nothing: the sign of the wrong device.
func (a *App) checkLevels(rec *recording) {
	db := func(p float32) float64 {
		if p <= 0 {
			return -120
		}
		return 20 * math.Log10(float64(p))
	}
	me, them := db(rec.r.Me.MaxPeak()), db(rec.r.Them.MaxPeak())
	logx.Printf("levels of %s: mic peak %.0f dBFS, meeting peak %.0f dBFS", rec.sess.Meta.ID, me, them)
	if me < silentDB && time.Since(rec.started) > 20*time.Second {
		a.showMessage("Your microphone was silent", "Check the Audio page in flit", true)
	}
}
