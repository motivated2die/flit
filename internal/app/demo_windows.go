package app

import (
	"time"

	"github.com/motivated2die/flit/internal/ui"
)

// runNotchDemo cycles the notch through its states, for looking at it and
// its animations on a real screen (`flit notch-demo`, development).
func runNotchDemo() int {
	ui.InitDPIAwareness()
	tray, err := ui.NewTray("flit notch demo")
	if err != nil {
		return 1
	}
	n, err := ui.NewNotch()
	if err != nil {
		ui.ShowError("flit", err.Error())
		return 1
	}
	var level float32
	n.Levels = func() (float32, float32) {
		level = 0.02 + 0.5*float32((time.Now().UnixMilli()/90)%7)/7
		return level, level * 0.6
	}
	start := time.Now()
	steps := []struct {
		at time.Duration
		v  ui.NotchView
	}{
		{0, ui.NotchView{Mode: ui.NotchPrompt, Title: "Teams meeting", Detail: "Weekly sync",
			Buttons: []ui.NotchButton{{ID: "record", Label: "Transcribe", Primary: true}, {ID: "dismiss", Label: "Not now"}}}},
		{3 * time.Second, ui.NotchView{Mode: ui.NotchRecording, Title: "Recording", Since: start.Add(3 * time.Second),
			Buttons: []ui.NotchButton{{ID: "stop", Label: "Stop"}}}},
		{7 * time.Second, ui.NotchView{Mode: ui.NotchWorking, Title: "Transcribing", Progress: 0.1}},
		{8 * time.Second, ui.NotchView{Mode: ui.NotchWorking, Title: "Transcribing", Progress: 0.55}},
		{9 * time.Second, ui.NotchView{Mode: ui.NotchWorking, Title: "Transcribing", Progress: 1}},
		{10 * time.Second, ui.NotchView{Mode: ui.NotchReady, Title: "Transcript ready", Detail: "1,284 words",
			Buttons: []ui.NotchButton{{ID: "copy", Label: "Copy", Primary: true}}}},
		{13 * time.Second, ui.NotchView{Mode: ui.NotchHidden}},
	}
	for _, s := range steps {
		s := s
		time.AfterFunc(s.at, func() {
			tray.Post(func() {
				if s.v.Mode == ui.NotchHidden {
					n.Hide()
					time.AfterFunc(500*time.Millisecond, func() { tray.Post(tray.Quit) })
					return
				}
				n.Show(s.v)
			})
		})
	}
	n.OnButton = func(id string) { tray.Notify("notch", "clicked "+id) }
	tray.Run()
	return 0
}
