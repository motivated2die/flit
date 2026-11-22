package coreaudio

import (
	"unsafe"

	"github.com/moutend/go-wca/pkg/wca"
)

// AppSession is an app's active audio session on a device.
type AppSession struct {
	PID      uint32
	DeviceID string
}

// ActiveSessions returns the active audio sessions on every active endpoint
// of flow: for Capture, which app uses which microphone right now; for
// Render, which app plays to which output. Run it on a COM thread.
func ActiveSessions(flow Flow) ([]AppSession, error) {
	en, err := NewEnumerator()
	if err != nil {
		return nil, err
	}
	defer en.Release()
	var out []AppSession
	err = en.each(flow, func(dev *wca.IMMDevice, devID string) bool {
		var mgr *wca.IAudioSessionManager2
		if err := Activate(dev, wca.IID_IAudioSessionManager2, unsafe.Pointer(&mgr)); err != nil {
			return true
		}
		defer mgr.Release()
		var sessions *wca.IAudioSessionEnumerator
		if err := mgr.GetSessionEnumerator(&sessions); err != nil {
			return true
		}
		defer sessions.Release()
		var n int
		if err := sessions.GetCount(&n); err != nil {
			return true
		}
		for i := 0; i < n; i++ {
			var ctl *wca.IAudioSessionControl
			if err := sessions.GetSession(i, &ctl); err != nil {
				continue
			}
			var state uint32
			if err := ctl.GetState(&state); err == nil && state == wca.AudioSessionStateActive {
				if d, err := ctl.QueryInterface(wca.IID_IAudioSessionControl2); err == nil {
					ctl2 := (*wca.IAudioSessionControl2)(unsafe.Pointer(d))
					var pid uint32
					if ctl2.GetProcessId(&pid) == nil && pid != 0 {
						out = append(out, AppSession{PID: pid, DeviceID: devID})
					}
					ctl2.Release()
				}
			}
			ctl.Release()
		}
		return true
	})
	return out, err
}

// ActiveSessionPIDs returns the processes with an active session on flow.
func ActiveSessionPIDs(flow Flow) ([]uint32, error) {
	sessions, err := ActiveSessions(flow)
	seen := map[uint32]bool{}
	var out []uint32
	for _, s := range sessions {
		if !seen[s.PID] {
			seen[s.PID] = true
			out = append(out, s.PID)
		}
	}
	return out, err
}

// DeviceByName finds an active endpoint of flow by its friendly name (a
// device whose ID changed, e.g. after a driver update, is found again).
func (e *Enumerator) DeviceByName(flow Flow, name string) string {
	id := ""
	_ = e.each(flow, func(dev *wca.IMMDevice, devID string) bool {
		if FriendlyName(dev) == name {
			id = devID
			return false
		}
		return true
	})
	return id
}
