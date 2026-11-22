package coreaudio

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/moutend/go-wca/pkg/wca"
	"golang.org/x/sys/windows"
)

// Process loopback (Windows 10 build 20348 / Windows 11 and later): an
// audio client that records what one process tree plays, whatever device it
// plays to. It is activated asynchronously on a virtual device, with a
// completion handler the caller implements as a COM object; here that is a
// small static object whose vtable holds Go callbacks.

var (
	mmdevapi                        = windows.NewLazySystemDLL("mmdevapi.dll")
	procActivateAudioInterfaceAsync = mmdevapi.NewProc("ActivateAudioInterfaceAsync")

	iidIUnknown          = ole.NewGUID("{00000000-0000-0000-C000-000000000046}")
	iidIAgileObject      = ole.NewGUID("{94EA2B94-E9CC-49E0-C0FF-EE64CA8F5B90}")
	iidCompletionHandler = ole.NewGUID("{41D949AB-9862-444A-80F6-C261334DA5EB}")
)

// ErrProcessLoopbackUnsupported means this Windows can't record a single
// process (older than build 20348).
var ErrProcessLoopbackUnsupported = errors.New("this Windows version can't record a single app's audio")

const (
	activationTypeProcessLoopback = 1
	loopbackIncludeTree           = 0
	vtBlob                        = 65
	sOK                           = 0
	eNoInterface                  = 0x80004002
)

// audioclientActivationParams mirrors AUDIOCLIENT_ACTIVATION_PARAMS.
type audioclientActivationParams struct {
	ActivationType  int32
	TargetProcessID uint32
	LoopbackMode    int32
}

// propVariantBlob mirrors a PROPVARIANT holding a BLOB (24 bytes on x64).
type propVariantBlob struct {
	VT       uint16
	_        [3]uint16
	Size     uint32
	_        uint32
	BlobData uintptr
}

// handler is the COM object: its first word points to the vtable.
type handler struct {
	vtbl *handlerVtbl
	refs atomic.Int32
	done chan struct{}
	once sync.Once
}

type handlerVtbl struct {
	QueryInterface, AddRef, Release, ActivateCompleted uintptr
}

var (
	handlerVtblOnce sync.Once
	theHandlerVtbl  *handlerVtbl
	// Pending handlers stay reachable here while Windows holds them.
	handlersMu sync.Mutex
	handlers   = map[uintptr]*handler{}
)

func lookupHandler(this uintptr) *handler {
	handlersMu.Lock()
	defer handlersMu.Unlock()
	return handlers[this]
}

func vtbl() *handlerVtbl {
	handlerVtblOnce.Do(func() {
		theHandlerVtbl = &handlerVtbl{
			QueryInterface: windows.NewCallback(func(this, riid, ppv uintptr) uintptr {
				g := (*ole.GUID)(cptr(riid))
				if ole.IsEqualGUID(g, iidIUnknown) || ole.IsEqualGUID(g, iidCompletionHandler) || ole.IsEqualGUID(g, iidIAgileObject) {
					*(*uintptr)(cptr(ppv)) = this
					if h := lookupHandler(this); h != nil {
						h.refs.Add(1)
					}
					return sOK
				}
				*(*uintptr)(cptr(ppv)) = 0
				return eNoInterface
			}),
			AddRef: windows.NewCallback(func(this uintptr) uintptr {
				if h := lookupHandler(this); h != nil {
					return uintptr(h.refs.Add(1))
				}
				return 1
			}),
			Release: windows.NewCallback(func(this uintptr) uintptr {
				if h := lookupHandler(this); h != nil {
					return uintptr(max(0, h.refs.Add(-1)))
				}
				return 0
			}),
			ActivateCompleted: windows.NewCallback(func(this, op uintptr) uintptr {
				if h := lookupHandler(this); h != nil {
					h.once.Do(func() { close(h.done) })
				}
				return sOK
			}),
		}
	})
	return theHandlerVtbl
}

// ActivateProcessLoopback returns an uninitialised IAudioClient recording
// the process tree of pid. The caller initialises it with a format of its
// choice and AUDCLNT_STREAMFLAGS_LOOPBACK (see InitProcessLoopback).
func ActivateProcessLoopback(pid uint32) (*wca.IAudioClient, error) {
	if procActivateAudioInterfaceAsync.Find() != nil {
		return nil, ErrProcessLoopbackUnsupported
	}
	// Windows reads the parameters through a plain address, so they live
	// on the heap (which doesn't move) and stay reachable until the end.
	params := &audioclientActivationParams{ActivationType: activationTypeProcessLoopback, TargetProcessID: pid, LoopbackMode: loopbackIncludeTree}
	pv := &propVariantBlob{VT: vtBlob, Size: uint32(unsafe.Sizeof(*params)), BlobData: uintptr(unsafe.Pointer(params))}
	defer runtime.KeepAlive(params)

	h := &handler{vtbl: vtbl(), done: make(chan struct{})}
	h.refs.Store(1)
	key := uintptr(unsafe.Pointer(h))
	handlersMu.Lock()
	handlers[key] = h
	handlersMu.Unlock()
	defer func() {
		handlersMu.Lock()
		delete(handlers, key)
		handlersMu.Unlock()
	}()

	path, _ := windows.UTF16PtrFromString(`VAD\Process_Loopback`)
	var op *ole.IUnknown
	hr, _, _ := procActivateAudioInterfaceAsync.Call(uintptr(unsafe.Pointer(path)), uintptr(unsafe.Pointer(wca.IID_IAudioClient)),
		uintptr(unsafe.Pointer(pv)), key, uintptr(unsafe.Pointer(&op)))
	if int32(hr) < 0 {
		return nil, fmt.Errorf("ActivateAudioInterfaceAsync: %w", ole.NewError(hr))
	}
	defer op.Release()
	select {
	case <-h.done:
	case <-time.After(5 * time.Second):
		return nil, errors.New("process loopback activation timed out")
	}
	// IActivateAudioInterfaceAsyncOperation::GetActivateResult (vtable 3).
	var result uintptr
	var client *wca.IAudioClient
	vt := *(*[4]uintptr)(unsafe.Pointer(op.RawVTable))
	r, _, _ := callN(vt[3], uintptr(unsafe.Pointer(op)), uintptr(unsafe.Pointer(&result)), uintptr(unsafe.Pointer(&client)))
	if int32(r) < 0 {
		return nil, fmt.Errorf("GetActivateResult: %w", ole.NewError(r))
	}
	if int32(result) < 0 {
		return nil, fmt.Errorf("activating process loopback: %w", ole.NewError(result))
	}
	return client, nil
}

// cptr turns an address handed over by Windows into a pointer (memory
// Windows owns or a Go object kept reachable in handlers).
func cptr(p uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&p)) }
