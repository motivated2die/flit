package app

import (
	"os"
	"strings"
	"time"

	"github.com/motivated2die/flit/internal/capture"
	"github.com/motivated2die/flit/internal/detect"
	"github.com/motivated2die/flit/internal/logx"
	"github.com/motivated2die/flit/internal/session"
	"github.com/motivated2die/flit/internal/ui"
)

// recording is the meeting being recorded.
type recording struct {
	sess     *session.Session
	r        *capture.Recording
	micID    string            // the microphone asked for ("" = Windows' default)
	meeting  *detect.Candidate // the detected meeting, nil for a recording by hand
	provider string            // "" for a manual recording
	started  time.Time
}

// onMeeting handles detection events on the UI thread.
func (a *App) onMeeting(ev detect.Event) {
	switch ev.Kind {
	case detect.Started:
		logx.Printf("meeting started: %s (pid %d)", ev.Provider, ev.Target)
		if a.rec != nil || a.dismissed[ev.Provider] {
			return
		}
		c := ev.Candidate
		a.detected = &c
		if a.cfg.AutoRecord {
			a.startCountdown()
		}
		a.showPrompt(c)
	case detect.Ended:
		logx.Printf("meeting ended: %s", ev.Provider)
		delete(a.dismissed, ev.Provider)
		if a.detected != nil && a.detected.Provider == ev.Provider {
			a.detected = nil
			a.stopCountdown()
			if a.notch != nil && a.notch.View().Mode == ui.NotchPrompt {
				a.notch.Hide()
			}
		}
		if a.rec != nil && a.rec.provider == ev.Provider {
			a.stopRecording()
		}
	}
	a.refresh()
}

// autoRecordAfter is the countdown before a detected meeting is recorded.
const autoRecordAfter = 5 * time.Second

// startCountdown records the detected meeting after autoRecordAfter unless
// it is cancelled (dismissed), ends, or is recorded by hand first.
func (a *App) startCountdown() {
	a.autoToken++
	token := a.autoToken
	a.autoFrom = time.Now()
	logx.Printf("recording automatically in %v", autoRecordAfter)
	if a.tray == nil {
		return // tests
	}
	time.AfterFunc(autoRecordAfter, func() {
		a.tray.Post(func() {
			if token != a.autoToken || a.autoFrom.IsZero() || a.detected == nil || a.rec != nil {
				return
			}
			a.autoFrom = time.Time{}
			c := *a.detected
			a.startRecording(&c)
		})
	})
}

// stopCountdown cancels an automatic start.
func (a *App) stopCountdown() {
	a.autoToken++
	a.autoFrom = time.Time{}
}

// counting reports whether a detected meeting is about to be recorded.
func (a *App) counting() bool { return !a.autoFrom.IsZero() && a.detected != nil && a.rec == nil }

// startRecording records the detected meeting c, or everything the PC
// plays when c is nil.
func (a *App) startRecording(c *detect.Candidate) {
	if a.rec != nil {
		return
	}
	a.stopCountdown()
	a.yieldSummary()
	meta := session.Meta{Language: a.cfg.Language, Model: a.cfg.Model}
	them := capture.Source{Kind: capture.Loopback, DeviceID: a.cfg.OutputID, DeviceName: a.cfg.OutputName}
	if c != nil {
		meta.Provider = c.Provider
		meta.Title = meetingTitle(*c)
		them = capture.Source{Kind: capture.Process, PID: c.Target}
	}
	s, err := session.Create(a.dataDir, meta)
	if err != nil {
		a.fail("Couldn't start recording", err)
		return
	}
	windowTitle := ""
	if c != nil {
		windowTitle = c.Title
	}
	a.matchMeeting(s, time.Time{}, windowTitle)
	mic := a.micSource(c)
	meFile, themFile := session.RecordFileNames()
	r, err := capture.Record(s.Dir, meFile, themFile, mic, them,
		func(ev capture.Event) {
			if ev.Err != nil {
				logx.Printf("capture %s: %v", ev.Source, ev.Err)
			} else {
				logx.Printf("capture %s: recording from %s", ev.Source, ev.Device)
			}
		})
	if err != nil {
		_ = os.RemoveAll(s.Dir)
		a.fail("Couldn't start recording", err)
		return
	}
	s.Meta.ThemSource = "process"
	if r.ThemSource.Kind == capture.Loopback {
		s.Meta.ThemSource = "loopback"
	}
	_ = s.Save()
	a.rec = &recording{sess: s, r: r, started: time.Now(), micID: mic.DeviceID, meeting: c}
	a.startLive(s)
	if a.devs.names == nil {
		a.loadDevices() // device names for the panel and the window
	}
	if c != nil {
		a.rec.provider = c.Provider
	}
	a.detected = nil
	a.failed = nil
	logx.Printf("recording %s (%s)", s.Meta.ID, s.Meta.ThemSource)
	a.voiceSmooth = ui.MeterSmoother{}
	a.reloadSessions()
	a.noteDevice("me", mic.DeviceID)
	if r.ThemSource.Kind == capture.Loopback {
		a.noteDevice("them", r.ThemSource.DeviceID)
	}
	a.showRecording()
	a.refresh()
}

// stopRecording ends the recording and queues it for transcription.
func (a *App) stopRecording() { a.finishRecording(true) }

func (a *App) finishRecording(transcribe bool) {
	rec := a.rec
	if rec == nil {
		return
	}
	a.rec = nil
	a.saveAllNotes() // what was typed during the meeting
	err := rec.r.Stop()
	a.checkLevels(rec)
	s := rec.sess
	s.Meta.Ended = time.Now()
	s.Meta.State = session.Recorded
	if err != nil {
		logx.Printf("finishing %s: %v", s.Meta.ID, err)
	}
	_ = s.Save()
	logx.Printf("recorded %s: %.0f s", s.Meta.ID, s.Meta.Ended.Sub(s.Meta.Started).Seconds())
	windowTitle := ""
	if rec.meeting != nil {
		windowTitle = rec.meeting.Title
	}
	a.matchMeeting(s, s.Meta.Ended, windowTitle) // with the real length now
	a.reloadSessions()
	if transcribe {
		a.showWorking(true)
		// The transcription goes on from what was transcribed live, once
		// that has stopped.
		// A downloaded update waited for the recording: it starts now, and
		// the new version transcribes the recording (session.Recover).
		a.afterLive(func() { a.jobs.add(s); a.startUpdate() })
	} else {
		a.afterLive(func() {})
		if a.notch != nil {
			a.notch.Hide()
		}
	}
	a.refresh()
}

// switchMic moves a running recording to the microphone now chosen.
func (a *App) switchMic() {
	if a.rec == nil {
		return
	}
	src := a.micSource(a.rec.meeting)
	if src.DeviceID == a.rec.r.MeSource.DeviceID {
		return
	}
	if err := a.rec.r.SwitchMic(src); err != nil {
		logx.Printf("switching the microphone: %v", err)
		a.showMessage("Couldn't switch the microphone", err.Error(), true)
		return
	}
	a.rec.micID = src.DeviceID
	a.peak, a.voiceSmooth = [2]float32{}, ui.MeterSmoother{}
	a.noteDevice("me", src.DeviceID)
	logx.Printf("recording %s: microphone switched", a.rec.sess.Meta.ID)
}

// switchOutput moves a recording of the whole output to the output now
// chosen (a meeting app's own audio doesn't depend on it).
func (a *App) switchOutput() {
	if a.rec == nil || a.rec.r.ThemSource.Kind != capture.Loopback {
		return
	}
	src := capture.Source{Kind: capture.Loopback, DeviceID: a.cfg.OutputID, DeviceName: a.cfg.OutputName}
	if src.DeviceID == a.rec.r.ThemSource.DeviceID {
		return
	}
	if err := a.rec.r.SwitchThem(src); err != nil {
		logx.Printf("switching the output: %v", err)
		a.showMessage("Couldn't switch the output", err.Error(), true)
		return
	}
	a.noteDevice("them", src.DeviceID)
}

// noteDevice records in the session which device a track records from now.
func (a *App) noteDevice(track, id string) {
	if a.rec == nil {
		return
	}
	name := a.devs.names[id]
	if name == "" {
		name = "Windows' default"
	}
	s := a.rec.sess
	s.Meta.Devices = append(s.Meta.Devices, session.DeviceUse{Track: track, Device: name, From: time.Now()})
	_ = s.Save()
}

func (a *App) fail(what string, err error) {
	logx.Printf("%s: %v", what, err)
	a.failed = err
	a.showMessage(what, err.Error(), true)
	a.refresh()
}

// meetingTitle is a short name for the session: the browser tab's title
// without the browser's name, else the app's name.
func meetingTitle(c detect.Candidate) string {
	name := providerName(c.Provider) + " meeting"
	t := c.Title
	for _, suffix := range []string{" - Google Chrome", " - Microsoft Edge", " — Mozilla Firefox", " - Brave", " - Opera", " - Vivaldi"} {
		t = strings.TrimSuffix(t, suffix)
	}
	t = strings.TrimSpace(t)
	if t == "" || len([]rune(t)) > 80 {
		return name
	}
	return t
}
