// Package coreaudio is flit's thin layer over Windows Core Audio: COM
// threads, endpoints, activating audio clients, and process loopback.
//
// It uses go-wca for the interfaces it implements correctly and calls the
// COM vtables directly where go-wca is wrong or missing (Resound's notes
// list them: IMMDevice.Activate passes a pointer where the class context
// belongs, IMMDeviceEnumerator.GetDevice is a stub; ActivateAudioInterface-
// Async isn't there at all). Every call must run on a thread with COM
// initialised (Run, or a locked thread after Init).
package coreaudio

import (
	"errors"
	"fmt"
	"runtime"
	"syscall"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/moutend/go-wca/pkg/wca"
)

const clsctxAll = 0x17

// Flow is a data-flow direction.
type Flow uint32

const (
	Render  Flow = wca.ERender  // outputs
	Capture Flow = wca.ECapture // microphones
)

// Role is a default-device role.
type Role uint32

const (
	Console        Role = wca.EConsole
	Communications Role = wca.ECommunications
)

// Init initialises COM (multithreaded) on the current, locked thread.
// S_FALSE (already initialised) counts as success.
func Init() error {
	err := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED)
	var oe *ole.OleError
	if err != nil && errors.As(err, &oe) && oe.Code() == 1 {
		return nil
	}
	return err
}

// Run executes fn on a fresh locked OS thread with COM initialised and
// waits for it.
func Run(fn func() error) error {
	done := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		if err := Init(); err != nil {
			done <- fmt.Errorf("CoInitializeEx: %w", err)
			return
		}
		defer ole.CoUninitialize()
		done <- fn()
	}()
	return <-done
}

// Endpoint is an active audio device.
type Endpoint struct {
	ID      string
	Name    string
	Default bool // the default device for the communications role
}

// Enumerator wraps IMMDeviceEnumerator.
type Enumerator struct{ e *wca.IMMDeviceEnumerator }

// NewEnumerator creates one; Release it.
func NewEnumerator() (*Enumerator, error) {
	var e *wca.IMMDeviceEnumerator
	if err := wca.CoCreateInstance(wca.CLSID_MMDeviceEnumerator, 0, clsctxAll, wca.IID_IMMDeviceEnumerator, &e); err != nil {
		return nil, fmt.Errorf("device enumerator: %w", err)
	}
	return &Enumerator{e}, nil
}

// Release frees it.
func (e *Enumerator) Release() { e.e.Release() }

// Endpoints lists the active endpoints of a flow.
func (e *Enumerator) Endpoints(flow Flow) ([]Endpoint, error) {
	def := e.DefaultID(flow, Communications)
	var out []Endpoint
	err := e.each(flow, func(dev *wca.IMMDevice, id string) bool {
		out = append(out, Endpoint{ID: id, Name: FriendlyName(dev), Default: id == def})
		return true
	})
	return out, err
}

// Device returns the endpoint with id ("" = the default for role).
// Release it.
func (e *Enumerator) Device(flow Flow, role Role, id string) (*wca.IMMDevice, error) {
	if id == "" {
		var dev *wca.IMMDevice
		if err := e.e.GetDefaultAudioEndpoint(uint32(flow), uint32(role), &dev); err != nil {
			return nil, fmt.Errorf("default device: %w", err)
		}
		return dev, nil
	}
	var found *wca.IMMDevice
	err := e.each(flow, func(dev *wca.IMMDevice, devID string) bool {
		if devID == id {
			dev.AddRef()
			found = dev
			return false
		}
		return true
	})
	if err != nil {
		return nil, err
	}
	if found == nil {
		return nil, ErrNotFound
	}
	return found, nil
}

// ErrNotFound means no active endpoint has the ID.
var ErrNotFound = errors.New("audio device not found")

// DefaultID returns the default endpoint's ID for a role ("" if none).
func (e *Enumerator) DefaultID(flow Flow, role Role) string {
	var dev *wca.IMMDevice
	if err := e.e.GetDefaultAudioEndpoint(uint32(flow), uint32(role), &dev); err != nil {
		return ""
	}
	defer dev.Release()
	var id string
	_ = dev.GetId(&id)
	return id
}

// each calls fn for every active endpoint until it returns false. The
// device is released after fn returns.
func (e *Enumerator) each(flow Flow, fn func(dev *wca.IMMDevice, id string) bool) error {
	var col *wca.IMMDeviceCollection
	if err := e.e.EnumAudioEndpoints(uint32(flow), wca.DEVICE_STATE_ACTIVE, &col); err != nil {
		return fmt.Errorf("enumerating devices: %w", err)
	}
	defer col.Release()
	var n uint32
	if err := col.GetCount(&n); err != nil {
		return err
	}
	for i := uint32(0); i < n; i++ {
		var dev *wca.IMMDevice
		if err := col.Item(i, &dev); err != nil {
			continue
		}
		var id string
		_ = dev.GetId(&id)
		more := fn(dev, id)
		dev.Release()
		if !more {
			break
		}
	}
	return nil
}

// FriendlyName returns a device's display name.
func FriendlyName(dev *wca.IMMDevice) string {
	var ps *wca.IPropertyStore
	if err := dev.OpenPropertyStore(wca.STGM_READ, &ps); err != nil {
		return ""
	}
	defer ps.Release()
	var pv wca.PROPVARIANT
	if err := ps.GetValue(&wca.PKEY_Device_FriendlyName, &pv); err != nil || pv.VT != ole.VT_LPWSTR {
		return ""
	}
	return pv.String() // also frees the string
}

// Activate activates an interface on a device. go-wca's IMMDevice.Activate
// passes the class context by pointer; this passes it by value.
func Activate(dev *wca.IMMDevice, iid *ole.GUID, out unsafe.Pointer) error {
	hr, _, _ := syscall.SyscallN(dev.VTable().Activate,
		uintptr(unsafe.Pointer(dev)), uintptr(unsafe.Pointer(iid)), clsctxAll, 0, uintptr(out))
	if hr != 0 {
		return ole.NewError(hr)
	}
	return nil
}

// IsAccessDenied reports E_ACCESSDENIED: Windows' privacy settings keep
// desktop apps from the microphone.
func IsAccessDenied(err error) bool {
	var oe *ole.OleError
	return errors.As(err, &oe) && uint32(oe.Code()) == 0x80070005
}

// ErrMicBlocked explains IsAccessDenied to the user.
var ErrMicBlocked = errors.New("Windows' privacy settings keep desktop apps from the microphone")
