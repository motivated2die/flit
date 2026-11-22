package coreaudio

import (
	"os"
	"testing"
)

// With FLIT_LIVE_DETECT=1 this prints the audio devices, which ones are
// Windows' defaults, and which apps hold sessions on which device: what
// decides which microphone a recording should use.
func TestListDevicesLive(t *testing.T) {
	if os.Getenv("FLIT_LIVE_DETECT") != "1" {
		t.Skip("set FLIT_LIVE_DETECT=1")
	}
	err := Run(func() error {
		en, err := NewEnumerator()
		if err != nil {
			return err
		}
		defer en.Release()
		for _, flow := range []Flow{Capture, Render} {
			eps, err := en.Endpoints(flow)
			if err != nil {
				return err
			}
			cons, comm := en.DefaultID(flow, Console), en.DefaultID(flow, Communications)
			for _, e := range eps {
				t.Logf("flow %d: %-60s console=%v communications=%v", flow, e.Name, e.ID == cons, e.ID == comm)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
