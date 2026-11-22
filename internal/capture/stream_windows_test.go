package capture

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/go-ole/go-ole"

	"github.com/motivated2die/flit/internal/coreaudio"
)

func TestDescribeNamesCoreAudioErrors(t *testing.T) {
	for code, want := range map[uint32]string{
		errUnsupportedFormat: "Initialize: the device refused its own audio format (AUDCLNT_E_UNSUPPORTED_FORMAT)",
		errDeviceInUse:       "Initialize: another app uses the device exclusively (AUDCLNT_E_DEVICE_IN_USE)",
		0x88890001:           "Initialize: HRESULT 0x88890001", // unnamed: the number
	} {
		err := describe("Initialize", ole.NewError(uintptr(code)))
		if err.Error() != want {
			t.Errorf("%#x: %q, want %q", code, err, want)
		}
		if hresult(err) != code || hresult(fmt.Errorf("meeting audio: %w", err)) != code {
			t.Errorf("%#x: the code is lost (%#x)", code, hresult(err))
		}
	}
	if err := describe("Activate", ole.NewError(0x80070005)); !errors.Is(err, coreaudio.ErrMicBlocked) {
		t.Errorf("access denied: %v", err)
	}
}

func TestTransient(t *testing.T) {
	for code, want := range map[uint32]bool{
		errUnsupportedFormat: true, // the failure seen after installs: passed within seconds
		errDeviceInUse:       true,
		errInvalided:         true,
		errEndpointCreate:    true,
		errServiceNotRunning: false, // stays until someone starts the service
		0x88890001:           false,
	} {
		if got := transient(describe("Initialize", ole.NewError(uintptr(code)))); got != want {
			t.Errorf("%#x: transient %v, want %v", code, got, want)
		}
	}
	if transient(coreaudio.ErrNotFound) || transient(errors.New("no device")) {
		t.Error("an error without an HRESULT counted as transient")
	}
}

type countSink struct{ rate, n int }

func (c *countSink) Write(rate int, pcm []float32) { c.rate, c.n = rate, c.n+len(pcm) }

// A waiting stream whose device hasn't opened yet still fills its track
// with silence, so both tracks of a recording stay as long as each other;
// a strict one (never started) writes nothing.
func TestWaitingStreamPadsSilence(t *testing.T) {
	sink := &countSink{}
	al := &Aligner{Origin: 0}
	s := &Stream{track: sink, al: al, patient: true}
	s.pad(al, 2*10_000_000) // two seconds in
	if sink.rate != Rate || sink.n != 2*Rate {
		t.Fatalf("wrote %d frames at %d Hz, want %d at %d", sink.n, sink.rate, 2*Rate, Rate)
	}
	// The device opens at 48 kHz: the time line goes on from two seconds.
	al.Rebase(48000)
	if al.Written() != 2*48000 {
		t.Fatalf("rebased to %d frames, want %d", al.Written(), 2*48000)
	}
	if silence, _ := al.Place(3*10_000_000, 480); silence != 48000 {
		t.Fatalf("a packet at 3 s after 2 s written: %d frames of silence, want 48000", silence)
	}

	strict := &countSink{}
	sal := &Aligner{}
	(&Stream{track: strict, al: sal}).pad(sal, 2*10_000_000)
	if strict.n != 0 {
		t.Fatalf("a strict stream wrote %d frames before opening", strict.n)
	}
	if !strings.Contains((&hrError{step: "Start", code: errInvalided}).Error(), "removed or changed") {
		t.Fatal("the invalidated device lost its message")
	}
}
