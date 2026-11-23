package native

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/motivated2die/flit/internal/download"
)

// VulkanDLL is transcribe.cpp's loadable Vulkan backend module. It lives
// beside transcribe.dll in Dir(dataDir) once downloaded.
const VulkanDLL = "ggml-vulkan.dll"

// VulkanPath is where ggml-vulkan.dll lives under dataDir.
func VulkanPath(dataDir string) string {
	return filepath.Join(Dir(dataDir), VulkanDLL)
}

// HasVulkan reports whether the Vulkan backend DLL is present for this
// engine version.
func HasVulkan(dataDir string) bool {
	st, err := os.Stat(VulkanPath(dataDir))
	return err == nil && st.Size() > 0
}

var vulkanFetch sync.Mutex

// EnsureVulkan downloads the pinned transcribe.cpp bundle (18 MB) when
// ggml-vulkan.dll is missing, checks its SHA-256 and contract, and extracts
// ggml-vulkan.dll into Dir(dataDir). Safe for concurrent callers.
func EnsureVulkan(ctx context.Context, dataDir string, progress func(done, total int64)) error {
	vulkanFetch.Lock()
	defer vulkanFetch.Unlock()
	if HasVulkan(dataDir) {
		return nil
	}
	dir := Dir(dataDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	bundle := filepath.Join(dataDir, "engine", BundleName)
	f := download.File{URL: BundleURL, Dest: bundle, SHA256: BundleSHA256, Size: BundleSize}
	if err := (download.Client{UserAgent: "flit"}).Get(ctx, f, progress); err != nil {
		return fmt.Errorf("downloading the Vulkan speech backend: %w", err)
	}
	err := ExtractVulkan(bundle, dir)
	_ = os.Remove(bundle)
	return err
}

// ExtractVulkan verifies contract.json in a transcribe.cpp release bundle
// and writes ggml-vulkan.dll atomically into dir.
func ExtractVulkan(bundlePath, dir string) error {
	f, err := os.Open(bundlePath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	dest := filepath.Join(dir, VulkanDLL)
	tmp := dest + ".tmp"
	var sawContract, wroteDLL bool
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			_ = os.Remove(tmp)
			return err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		base := strings.ToLower(path.Base(h.Name))
		switch base {
		case "contract.json":
			b, err := io.ReadAll(io.LimitReader(tr, 1<<20))
			if err != nil {
				_ = os.Remove(tmp)
				return err
			}
			var c Contract
			if err := json.Unmarshal(b, &c); err != nil {
				_ = os.Remove(tmp)
				return fmt.Errorf("contract.json: %w", err)
			}
			if err := CheckContract(c); err != nil {
				_ = os.Remove(tmp)
				return err
			}
			sawContract = true
		case VulkanDLL:
			w, err := os.Create(tmp)
			if err != nil {
				return err
			}
			_, err = io.Copy(w, io.LimitReader(tr, 200<<20))
			if cerr := w.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				_ = os.Remove(tmp)
				return err
			}
			wroteDLL = true
		}
	}
	if !sawContract {
		_ = os.Remove(tmp)
		return errors.New("engine bundle has no contract.json")
	}
	if !wroteDLL {
		_ = os.Remove(tmp)
		return errors.New("engine bundle has no " + VulkanDLL)
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
