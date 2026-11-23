package logx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScrub(t *testing.T) {
	s := newScrubber([]string{"anna.berg"}, "WORKPC-17")
	in := `opened C:\Users\anna.berg\AppData\Local\flit\models, host WORKPC-17, mail anna.berg@example.com, ANNA.BERG again`
	got := s.scrub(in)
	for _, leak := range []string{"anna.berg", "ANNA.BERG", "WORKPC-17", "example.com"} {
		if strings.Contains(got, leak) {
			t.Errorf("%q leaks %q", got, leak)
		}
	}
	if !strings.Contains(got, `C:\Users\<user>\AppData\Local\flit\models`) {
		t.Errorf("path not kept readable: %q", got)
	}
}

func TestInitRotates(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, fileName), make([]byte, maxSize+1), 0o644)
	closeLog, err := Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	Printf("hello %d", 42)
	closeLog()
	if st, err := os.Stat(filepath.Join(dir, oldName)); err != nil || st.Size() != maxSize+1 {
		t.Fatalf("old log: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, fileName))
	if !strings.HasSuffix(string(b), "hello 42\n") {
		t.Fatalf("log %q", b)
	}
}

func TestRecent(t *testing.T) {
	for i := 0; i < recentLines+5; i++ {
		Printf("line %d", i)
	}
	got := Recent(3)
	if len(got) != 3 || !strings.HasSuffix(got[0], "line 202") || !strings.HasSuffix(got[2], "line 204") {
		t.Fatalf("%q", got)
	}
	if n := len(Recent(1000)); n != recentLines {
		t.Fatalf("%d lines", n)
	}
}

func TestInitCrashAndInfo(t *testing.T) {
	dir := t.TempDir()
	if _, _, ok := CrashInfo(dir); ok {
		t.Fatal("CrashInfo reported ok before crash.log existed")
	}

	// Empty crash.log created by InitCrash is not reported as a crash.
	closeCrash, err := InitCrash(dir)
	if err != nil {
		t.Fatal(err)
	}
	closeCrash()
	if _, _, ok := CrashInfo(dir); ok {
		t.Fatal("CrashInfo reported ok for empty crash.log")
	}

	// Non-empty crash.log is reported with its size.
	if err := os.WriteFile(CrashPath(dir), []byte("panic: boom\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if sz, mod, ok := CrashInfo(dir); !ok || sz != int64(len("panic: boom\n")) || mod.IsZero() {
		t.Fatalf("CrashInfo = (%d, %v, %v), want non-empty", sz, mod, ok)
	}

	// Oversized crash.log rotates to crash.old.log.
	if err := os.WriteFile(CrashPath(dir), make([]byte, maxSize+1), 0o644); err != nil {
		t.Fatal(err)
	}
	closeCrash, err = InitCrash(dir)
	if err != nil {
		t.Fatal(err)
	}
	closeCrash()
	if st, err := os.Stat(filepath.Join(dir, crashOldName)); err != nil || st.Size() != maxSize+1 {
		t.Fatalf("old crash log: %v", err)
	}
}
