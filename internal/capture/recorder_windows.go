package capture

import (
	"fmt"
	"path/filepath"
	"sync"
)

// Recording is a session's two tracks being recorded on one time line.
type Recording struct {
	Me, Them *Track
	// ThemSource is what the meeting track records in the end: Process, or
	// Loopback when process loopback couldn't start.
	ThemSource Source
	MeSource   Source

	mu       sync.Mutex
	me, them *Stream
	meAl     *Aligner
	themAl   *Aligner
	onEvent  func(Event)
}

// Record starts both tracks in dir (meFile, themFile). The mic failing to
// open is an error (nothing is recorded); the meeting audio falls back from
// process loopback to the whole output.
func Record(dir, meFile, themFile string, mic, them Source, onEvent func(Event)) (*Recording, error) {
	origin := QPC()
	meT, err := NewTrack(filepath.Join(dir, meFile))
	if err != nil {
		return nil, err
	}
	themT, err := NewTrack(filepath.Join(dir, themFile))
	if err != nil {
		meT.Close()
		return nil, err
	}
	r := &Recording{Me: meT, Them: themT, MeSource: mic, onEvent: onEvent,
		meAl: &Aligner{Origin: origin}, themAl: &Aligner{Origin: origin}}
	if r.me, err = StartOn(mic, meT, r.meAl, onEvent); err != nil {
		meT.Close()
		themT.Close()
		return nil, fmt.Errorf("microphone: %w", err)
	}
	r.ThemSource = them
	r.them, err = StartOn(them, themT, r.themAl, onEvent)
	if err != nil && them.Kind == Process {
		if onEvent != nil {
			onEvent(Event{Source: them, Err: fmt.Errorf("recording the app alone failed, recording all audio instead: %w", err)})
		}
		r.ThemSource = Source{Kind: Loopback}
		r.them, err = StartOn(r.ThemSource, themT, r.themAl, onEvent)
	}
	if err != nil {
		r.me.Stop()
		meT.Close()
		themT.Close()
		return nil, fmt.Errorf("meeting audio: %w", err)
	}
	return r, nil
}

// SwitchMic moves the Me track to another microphone while recording: the
// old device's stream is stopped (and padded up to now), the new one
// continues the same file on the same time line. If the new device can't
// be opened, the old one is opened again and the error returned.
func (r *Recording) SwitchMic(src Source) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.switchStream(&r.me, r.Me, r.meAl, &r.MeSource, src)
}

// SwitchThem moves the Them track to another source (an output's loopback
// for a recording by hand).
func (r *Recording) SwitchThem(src Source) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.switchStream(&r.them, r.Them, r.themAl, &r.ThemSource, src)
}

func (r *Recording) switchStream(s **Stream, t *Track, al *Aligner, cur *Source, src Source) error {
	old := *cur
	(*s).Stop()
	n, err := StartOn(src, t, al, r.onEvent)
	if err != nil {
		if back, err2 := StartOn(old, t, al, r.onEvent); err2 == nil {
			*s = back
		}
		return err
	}
	*s, *cur = n, src
	return nil
}

// Stop ends both streams (padding both to the same end) and closes the
// files.
func (r *Recording) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	end := QPC()
	r.me.StopAt(end)
	r.them.StopAt(end)
	err := r.Me.Close()
	if err2 := r.Them.Close(); err == nil {
		err = err2
	}
	return err
}
