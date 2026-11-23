package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/motivated2die/flit/internal/download"
	"github.com/motivated2die/flit/internal/engine/native"
	"github.com/motivated2die/flit/internal/models"
)

func fetchEngine(ctx context.Context, root string, withVulkan bool) error {
	fmt.Printf("engine: transcribe.cpp %s\n", native.Version)
	bundle := filepath.Join(root, ".cache", native.BundleName)
	if err := get(ctx, download.File{URL: native.BundleURL, Dest: bundle, SHA256: native.BundleSHA256, Size: native.BundleSize}); err != nil {
		return err
	}
	out := filepath.Join(root, "internal", "engine", "native", "files")
	n, err := extractBundle(bundle, out)
	if err != nil {
		return err
	}
	if withVulkan {
		if err := native.ExtractVulkan(bundle, out); err != nil {
			return err
		}
		n++
	} else {
		_ = os.Remove(filepath.Join(out, native.VulkanDLL))
	}
	c, err := readJSONContract(filepath.Join(out, "contract.json"))
	if err != nil {
		return err
	}
	if err := native.CheckContract(c); err != nil {
		return err
	}
	fmt.Printf("  extracted %d files to %s (ABI %s)\n", n, out, c.HeaderHash)
	return nil
}

// extractBundle writes the kept files of the bundle into out, without the
// bundle's top folder.
func extractBundle(bundle, out string) (int, error) {
	f, err := os.Open(bundle)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return 0, err
	}
	tr := tar.NewReader(gz)
	n := 0
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return n, nil
		}
		if err != nil {
			return n, err
		}
		if h.Typeflag != tar.TypeReg || !native.Kept(h.Name) {
			continue
		}
		// Drop the top folder; refuse anything that would escape out.
		rel := h.Name
		if i := strings.IndexByte(rel, '/'); i >= 0 {
			rel = rel[i+1:]
		}
		rel = path.Clean(rel)
		if rel == "." || strings.HasPrefix(rel, "..") || path.IsAbs(rel) {
			return n, fmt.Errorf("bad path in bundle: %q", h.Name)
		}
		dest := filepath.Join(out, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return n, err
		}
		w, err := os.Create(dest)
		if err != nil {
			return n, err
		}
		_, err = io.Copy(w, io.LimitReader(tr, 200<<20))
		if cerr := w.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return n, err
		}
		n++
	}
}

func fetchModels(ctx context.Context, root string, ids []string) error {
	var want []models.Model
	if len(ids) == 0 {
		for _, m := range models.Catalog {
			if m.TestOnly || m.Role == models.RoleVAD || m.Role == models.RoleDiarize {
				want = append(want, m)
			}
		}
	} else {
		for _, id := range ids {
			m, ok := models.ByID(id)
			if !ok {
				return fmt.Errorf("unknown model %q", id)
			}
			want = append(want, m)
		}
	}
	dir := filepath.Join(root, "models")
	for _, m := range want {
		fmt.Printf("model %s (%s, %s)\n", m.ID, mb(m.Size), m.License)
		if err := get(ctx, download.File{URL: m.URL(), Dest: m.Path(dir), SHA256: m.SHA256, Size: m.Size}); err != nil {
			return err
		}
	}
	return nil
}
