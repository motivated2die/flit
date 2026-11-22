package app

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/motivated2die/flit/internal/install"
)

// fakeInstall makes an install folder (LOCALAPPDATA pointed at a temp
// folder) with a "flit.exe" that runs (a copy of ping.exe) and a data
// folder with recordings and models, and starts that exe standing in the
// models folder: like an engine worker left over, it keeps both folders
// from being deleted until it is stopped.
func fakeInstall(t *testing.T) (programDir, dataDir string, blocker *exec.Cmd) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("LOCALAPPDATA", root)
	programDir = install.Dir()
	if filepath.Dir(filepath.Dir(programDir)) != root {
		t.Fatalf("install folder %s isn't under the temp folder", programDir)
	}
	os.MkdirAll(programDir, 0o755)
	src, err := os.Open(filepath.Join(os.Getenv("SystemRoot"), "System32", "PING.EXE"))
	if err != nil {
		t.Skip("no ping.exe to stand in for flit.exe:", err)
	}
	dst, _ := os.Create(install.Exe())
	io.Copy(dst, src)
	src.Close()
	dst.Close()
	dataDir = filepath.Join(root, "flit")
	for _, f := range []string{"config.json", "debug.log", "sessions/2026-11-22_1252/me.wav", "sessions/2026-11-22_1252/transcript.json", "models/parakeet-ultra/model.gguf", "engine/0.4.2/transcribe.dll"} {
		p := filepath.Join(dataDir, f)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("x"), 0o644)
	}
	blocker = exec.Command(install.Exe(), "-n", "60", "127.0.0.1")
	blocker.Dir = filepath.Join(dataDir, "models")
	if err := blocker.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { blocker.Process.Kill(); blocker.Wait() })
	return programDir, dataDir, blocker
}

func gone(dir string, within time.Duration) bool {
	for end := time.Now().Add(within); ; {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			return true
		}
		if time.Now().After(end) {
			return false
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// TestUninstallDeletesEverything: "Delete everything" stops what still
// runs from the install folder, deletes the data folder with the
// recordings and models at once, and the program folder moments later.
func TestUninstallDeletesEverything(t *testing.T) {
	programDir, dataDir, blocker := fakeInstall(t)
	if !removableDataDir(dataDir) {
		t.Fatal("the fake data folder isn't recognised as flit's")
	}
	if err := removeFiles(dataDir, true); err != nil {
		t.Fatal(err)
	}
	if blocker.ProcessState == nil {
		if err := blocker.Wait(); err == nil {
			t.Fatal("the process running from the install folder wasn't stopped")
		}
	}
	if !gone(dataDir, time.Second) {
		t.Fatal("recordings and models are still there")
	}
	if !gone(programDir, 15*time.Second) {
		t.Fatal("the program folder is still there")
	}
}

// TestUninstallKeepsData: "Keep my recordings" removes the program, not
// the data.
func TestUninstallKeepsData(t *testing.T) {
	programDir, dataDir, _ := fakeInstall(t)
	if err := removeFiles(dataDir, false); err != nil {
		t.Fatal(err)
	}
	if !gone(programDir, 15*time.Second) {
		t.Fatal("the program folder is still there")
	}
	for _, f := range []string{"sessions/2026-11-22_1252/me.wav", "models/parakeet-ultra/model.gguf", "config.json"} {
		if _, err := os.Stat(filepath.Join(dataDir, f)); err != nil {
			t.Fatalf("%s went too", f)
		}
	}
	if !gone(filepath.Join(dataDir, "engine"), 15*time.Second) {
		t.Fatal("the unpacked engine stayed")
	}
}
