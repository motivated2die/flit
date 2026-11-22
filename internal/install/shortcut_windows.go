package install

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"

	"github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
)

func shortcutPath() (string, error) {
	return filepath.Join(os.Getenv("APPDATA"), `Microsoft\Windows\Start Menu\Programs`, "flit.lnk"), nil
}

// createShortcut writes the Start Menu shortcut through the shell's
// WScript.Shell object, on a thread of its own with COM initialised.
func createShortcut(target string) error {
	path, err := shortcutPath()
	if err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		if err := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); err != nil {
			if oe, ok := err.(*ole.OleError); !ok || oe.Code() != 1 { // S_FALSE: already initialised
				done <- err
				return
			}
		}
		defer ole.CoUninitialize()
		unk, err := oleutil.CreateObject("WScript.Shell")
		if err != nil {
			done <- err
			return
		}
		defer unk.Release()
		shell, err := unk.QueryInterface(ole.IID_IDispatch)
		if err != nil {
			done <- err
			return
		}
		defer shell.Release()
		v, err := oleutil.CallMethod(shell, "CreateShortcut", path)
		if err != nil {
			done <- err
			return
		}
		sc := v.ToIDispatch()
		defer sc.Release()
		for name, val := range map[string]string{"TargetPath": target, "WorkingDirectory": filepath.Dir(target), "Description": "flit: meeting transcripts"} {
			if _, err := oleutil.PutProperty(sc, name, val); err != nil {
				done <- err
				return
			}
		}
		_, err = oleutil.CallMethod(sc, "Save")
		done <- err
	}()
	return <-done
}

func hide(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}
