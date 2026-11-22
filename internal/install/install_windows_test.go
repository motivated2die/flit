package install

import (
	"os"
	"path/filepath"
	"testing"
)

// Registry and shortcut writes aren't tested automatically (they would
// change the developer's own installation); the file logic is.

func TestPortableAt(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "bin", "flit.exe")
	os.MkdirAll(filepath.Dir(exe), 0o755)
	if portableAt(exe) {
		t.Fatal("plain folder counted as portable")
	}
	os.WriteFile(filepath.Join(dir, "go.mod"), nil, 0o644)
	if !portableAt(exe) {
		t.Fatal("go.mod one folder up not portable")
	}
	other := filepath.Join(t.TempDir(), "flit.exe")
	os.WriteFile(filepath.Join(filepath.Dir(other), portableMarker), nil, 0o644)
	if !portableAt(other) {
		t.Fatal("portable marker ignored")
	}
}

func TestReplaceExe(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "download.exe")
	dst := filepath.Join(dir, "inst", "flit.exe")
	os.MkdirAll(filepath.Dir(dst), 0o755)
	os.WriteFile(src, []byte("v2"), 0o644)
	os.WriteFile(dst, []byte("v1"), 0o644)
	if err := replaceExe(src, dst); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "v2" {
		t.Fatalf("dst = %q", b)
	}
	if b, _ := os.ReadFile(dst + ".old"); string(b) != "v1" {
		t.Fatalf("old = %q", b)
	}
}

func TestReadVersion(t *testing.T) {
	dir := t.TempDir()
	if readVersion(dir) != "" {
		t.Fatal("version without file")
	}
	os.WriteFile(filepath.Join(dir, versionFile), []byte("0.1.0\r\n"), 0o644)
	if v := readVersion(dir); v != "0.1.0" {
		t.Fatalf("version %q", v)
	}
}
