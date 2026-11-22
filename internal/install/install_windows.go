// Package install puts flit where it belongs for the current user, without
// admin rights (docs/decisions/0003-distribution-without-admin.md): a
// downloaded flit.exe copies itself to %LOCALAPPDATA%\Programs\flit,
// registers an Installed-apps entry and a Start Menu shortcut (HKCU and the
// user's own folders only), and starts the installed copy. Development
// builds are portable and never install.
package install

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/motivated2die/flit/internal/instance"
)

const (
	appName        = "flit"
	exeName        = "flit.exe"
	runKey         = `Software\Microsoft\Windows\CurrentVersion\Run`
	uninstallKey   = `Software\Microsoft\Windows\CurrentVersion\Uninstall\flit`
	versionFile    = "installed_version.txt"
	AutostartFlag  = "--autostart"
	UninstallFlag  = "--uninstall"
	WelcomeFlag    = "--welcome="
	portableMarker = "portable"
)

// Dir is the install folder.
func Dir() string {
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", appName)
}

// Exe is the installed executable.
func Exe() string { return filepath.Join(Dir(), exeName) }

// Current returns this process's executable, symlinks resolved.
func Current() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return p, nil
}

// Portable reports whether this build must not install itself: an exe in
// a source checkout (a go.mod next to it or one folder up), a "portable"
// file next to it, or FLIT_PORTABLE=1. `go run` builds live in a temporary
// folder and count as portable too.
func Portable() bool {
	if os.Getenv("FLIT_PORTABLE") == "1" {
		return true
	}
	exe, err := Current()
	if err != nil {
		return true
	}
	return portableAt(exe)
}

func portableAt(exe string) bool {
	dir := filepath.Dir(exe)
	for _, p := range []string{
		filepath.Join(dir, "go.mod"),
		filepath.Join(dir, "..", "go.mod"),
		filepath.Join(dir, portableMarker),
	} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return strings.Contains(strings.ToLower(dir), `\go-build`)
}

// IsInstalledCopy reports whether this process is the installed exe.
func IsInstalledCopy() bool {
	exe, err := Current()
	return err == nil && sameFile(exe, Exe())
}

func sameFile(a, b string) bool {
	sa, err1 := os.Stat(a)
	sb, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(sa, sb)
}

// Ensure installs or updates the running exe when it isn't the installed
// copy, starts the installed copy and returns exit=true: this process has
// handed over. dataDir holds installed_version.txt.
func Ensure(version, dataDir string, args []string) (exit bool, err error) {
	if Portable() || IsInstalledCopy() {
		if IsInstalledCopy() {
			cleanOld()
		}
		return false, nil
	}
	src, err := Current()
	if err != nil {
		return false, err
	}
	installed := readVersion(dataDir)
	welcome := "install"
	if installed != "" {
		welcome = "update"
	}
	// The running copy holds the exe open; ask it to go first.
	if !instance.Quit(10 * time.Second) {
		return false, errors.New("the running flit didn't quit")
	}
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return false, err
	}
	if err := replaceExe(src, Exe()); err != nil {
		return false, err
	}
	if err := os.MkdirAll(dataDir, 0o755); err == nil {
		_ = os.WriteFile(filepath.Join(dataDir, versionFile), []byte(version), 0o644)
	}
	_ = registerApp(version)
	_ = createShortcut(Exe())
	if AutoStartEnabled() || installed == "" {
		// A new install starts with Windows by default (docs/decisions/0003);
		// an update keeps the user's choice and refreshes the path.
		_ = SetAutoStart(true)
	}
	cmd := exec.Command(Exe(), WelcomeFlag+welcome)
	if err := cmd.Start(); err != nil {
		return false, fmt.Errorf("starting the installed copy: %w", err)
	}
	return true, nil
}

// replaceExe copies src over dst; a running dst is renamed to .old first
// (Windows lets a running exe be renamed, not overwritten).
func replaceExe(src, dst string) error {
	if _, err := os.Stat(dst); err == nil {
		old := dst + ".old"
		_ = os.Remove(old)
		if err := os.Rename(dst, old); err != nil {
			return fmt.Errorf("moving the old copy aside: %w", err)
		}
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".new"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

func cleanOld() { _ = os.Remove(Exe() + ".old") }

func readVersion(dataDir string) string {
	b, err := os.ReadFile(filepath.Join(dataDir, versionFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// runValue is autostart's value name: a side-by-side development instance
// (FLIT_INSTANCE) gets its own, so it never rewrites the installed flit's.
func runValue() string { return appName + instance.Suffix() }

// AutoStartEnabled reports whether flit starts at sign-in.
func AutoStartEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue(runValue())
	return err == nil
}

// SetAutoStart turns starting at sign-in on or off, for the installed copy
// (or this exe when it runs portable).
func SetAutoStart(on bool) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if !on {
		if err := k.DeleteValue(runValue()); err != nil && !errors.Is(err, registry.ErrNotExist) {
			return err
		}
		return nil
	}
	target := Exe()
	if Portable() {
		if cur, err := Current(); err == nil {
			target = cur
		}
	}
	return k.SetStringValue(runValue(), fmt.Sprintf(`"%s" %s`, target, AutostartFlag))
}

func registerApp(version string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, uninstallKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	exe := Exe()
	for name, v := range map[string]string{
		"DisplayName":     "flit",
		"DisplayVersion":  version,
		"Publisher":       "flit",
		"DisplayIcon":     exe,
		"InstallLocation": Dir(),
		"UninstallString": fmt.Sprintf(`"%s" %s`, exe, UninstallFlag),
	} {
		if err := k.SetStringValue(name, v); err != nil {
			return err
		}
	}
	_ = k.SetDWordValue("NoModify", 1)
	_ = k.SetDWordValue("NoRepair", 1)
	if st, err := os.Stat(exe); err == nil {
		_ = k.SetDWordValue("EstimatedSize", uint32(st.Size()/1024))
	}
	return nil
}

// Unregister removes autostart, the Installed-apps entry and the shortcut.
func Unregister() {
	_ = SetAutoStart(false)
	_ = registry.DeleteKey(registry.CURRENT_USER, uninstallKey)
	if p, err := shortcutPath(); err == nil {
		_ = os.Remove(p)
	}
}

// DeleteSelfLater removes the install folder once this process has exited
// (a running exe can't delete itself).
func DeleteSelfLater() error { return DeleteLater(Dir()) }

// DeleteLater removes folders once this process has exited (a running
// exe can't delete itself), in a hidden cmd.exe: about two seconds later,
// then again each second for a few seconds while anything is left (a file
// still held open by a process that is ending).
//
// cmd.exe gets its exact command line: Go quotes arguments for the C
// runtime (an inner " becomes \"), which cmd doesn't understand, so the
// quoted paths reached it broken and nothing was ever deleted. With /S, cmd
// takes what is between the first and the last quote as it is. It runs in
// the temp folder: a folder a process stands in can't be removed.
func DeleteLater(dirs ...string) error {
	script := `ping -n 3 127.0.0.1 >nul`
	for _, d := range dirs {
		for range 5 {
			script += fmt.Sprintf(` & (if exist "%s" (rmdir /S /Q "%s" & ping -n 2 127.0.0.1 >nul))`, d, d)
		}
	}
	cmd := exec.Command("cmd.exe")
	hide(cmd)
	cmd.SysProcAttr.CmdLine = `cmd.exe /D /S /C "` + script + `"`
	cmd.Dir = os.TempDir()
	return cmd.Start()
}

// RemoveAll removes a folder, trying again for up to wait while files in
// it are still held open (a process that is ending).
func RemoveAll(dir string, wait time.Duration) error {
	end := time.Now().Add(wait)
	for {
		err := os.RemoveAll(dir)
		if err == nil || time.Now().After(end) {
			return err
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// StopOthers ends every other process running an exe from the install
// folder (an app that didn't quit, an engine worker left over), so its
// files can be deleted. It reports how many it ended.
func StopOthers() int {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0
	}
	defer windows.CloseHandle(snap)
	self := windows.GetCurrentProcessId()
	prefix := strings.ToLower(filepath.Clean(Dir()) + `\`)
	n := 0
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		if e.ProcessID == self || !strings.EqualFold(windows.UTF16ToString(e.ExeFile[:]), exeName) {
			continue
		}
		h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, e.ProcessID)
		if err != nil {
			continue
		}
		buf := make([]uint16, windows.MAX_PATH)
		size := uint32(len(buf))
		if windows.QueryFullProcessImageName(h, 0, &buf[0], &size) == nil && strings.HasPrefix(strings.ToLower(windows.UTF16ToString(buf[:size])), prefix) {
			if windows.TerminateProcess(h, 1) == nil {
				windows.WaitForSingleObject(h, 3000)
				n++
			}
		}
		windows.CloseHandle(h)
	}
	return n
}
