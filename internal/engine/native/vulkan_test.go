package native

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func makeBundle(t *testing.T, files map[string][]byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "bundle.tar.gz")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for name, data := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestExtractVulkan(t *testing.T) {
	c, _ := json.Marshal(Contract{Version: Version, HeaderHash: HeaderHash, Backends: []string{"vulkan", "cpu"}})
	bundle := makeBundle(t, map[string][]byte{
		"top/contract.json":   c,
		"top/ggml-vulkan.dll": []byte("fake-vulkan-dll"),
		"top/transcribe.dll":  []byte("ignored"),
	})
	dir := t.TempDir()
	if err := ExtractVulkan(bundle, dir); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, VulkanDLL))
	if err != nil || string(got) != "fake-vulkan-dll" {
		t.Fatalf("extracted %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "transcribe.dll")); !os.IsNotExist(err) {
		t.Error("non-vulkan file was extracted")
	}

	// Wrong contract is refused.
	badContract, _ := json.Marshal(Contract{Version: "9.9.9", HeaderHash: HeaderHash})
	bad := makeBundle(t, map[string][]byte{
		"top/contract.json":   badContract,
		"top/ggml-vulkan.dll": []byte("bad"),
	})
	if err := ExtractVulkan(bad, t.TempDir()); err == nil {
		t.Error("wrong version accepted")
	}
}
