package install

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestDeleteLater: the folders it is given are gone a few seconds later,
// with what is in them (paths with spaces too).
func TestDeleteLater(t *testing.T) {
	root := t.TempDir()
	var dirs []string
	for _, name := range []string{"Programs flit", "flit"} {
		d := filepath.Join(root, name)
		os.MkdirAll(filepath.Join(d, "sessions", "2026-11-22_1252"), 0o755)
		os.WriteFile(filepath.Join(d, "sessions", "2026-11-22_1252", "me.wav"), []byte("x"), 0o644)
		os.WriteFile(filepath.Join(d, "config.json"), []byte("{}"), 0o644)
		dirs = append(dirs, d)
	}
	if err := DeleteLater(dirs...); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		left := 0
		for _, d := range dirs {
			if _, err := os.Stat(d); err == nil {
				left++
			}
		}
		if left == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d of %d folders still there after 15 s", left, len(dirs))
		}
		time.Sleep(250 * time.Millisecond)
	}
}
