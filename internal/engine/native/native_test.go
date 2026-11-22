package native

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestKept(t *testing.T) {
	for name, want := range map[string]bool{
		"transcribe-native-windows-x86_64-cpu-vulkan/transcribe.dll":         true,
		"transcribe-native-windows-x86_64-cpu-vulkan/ggml-cpu-haswell.dll":   true,
		"transcribe-native-windows-x86_64-cpu-vulkan/ggml-vulkan.dll":        false,
		"transcribe-native-windows-x86_64-cpu-vulkan/contract.json":          true,
		"transcribe-native-windows-x86_64-cpu-vulkan/licenses/ggml/LICENSE":  true,
		"transcribe-native-windows-x86_64-cpu-vulkan/include/transcribe.h":   false,
		"transcribe-native-windows-x86_64-cpu-vulkan/bin/transcribe-cli.exe": false,
	} {
		if Kept(name) != want {
			t.Errorf("Kept(%s) = %v", name, !want)
		}
	}
}

func TestCheckContract(t *testing.T) {
	ok := Contract{Version: Version, HeaderHash: HeaderHash}
	if err := CheckContract(ok); err != nil {
		t.Fatal(err)
	}
	if CheckContract(Contract{Version: "0.3.0", HeaderHash: HeaderHash}) == nil {
		t.Error("accepted another version")
	}
	if CheckContract(Contract{Version: Version, HeaderHash: "0000"}) == nil {
		t.Error("accepted another ABI")
	}
}

func TestUnpackFSWritesAndSkipsIdentical(t *testing.T) {
	fsys := fstest.MapFS{
		"README.md":        {Data: []byte("not copied")},
		"transcribe.dll":   {Data: []byte("dll v1")},
		"licenses/LICENSE": {Data: []byte("MIT")},
	}
	dir := filepath.Join(t.TempDir(), "engine", Version)
	if err := unpackFS(fsys, dir); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "transcribe.dll")); string(b) != "dll v1" {
		t.Fatalf("dll = %q", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "README.md")); !os.IsNotExist(err) {
		t.Error("README was unpacked")
	}
	// A changed file on disk is replaced; an identical one is left alone.
	os.WriteFile(filepath.Join(dir, "transcribe.dll"), []byte("tampered"), 0o644)
	lic := filepath.Join(dir, "licenses", "LICENSE")
	before, _ := os.Stat(lic)
	if err := unpackFS(fsys, dir); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "transcribe.dll")); string(b) != "dll v1" {
		t.Fatalf("tampered dll not replaced: %q", b)
	}
	after, _ := os.Stat(lic)
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("identical file rewritten")
	}
}

func TestRemoveOtherVersions(t *testing.T) {
	parent := t.TempDir()
	for _, v := range []string{"0.3.0", Version, "0.4.1"} {
		os.MkdirAll(filepath.Join(parent, v), 0o755)
	}
	removeOtherVersions(parent, Version)
	entries, _ := os.ReadDir(parent)
	if len(entries) != 1 || entries[0].Name() != Version {
		t.Fatalf("left %v", entries)
	}
}
