package llm

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// ExtractRuntime unpacks the server and its libraries from the release
// archive into dir, flattened; it returns how many files it wrote.
func ExtractRuntime(zipPath, dir string) (int, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return 0, err
	}
	defer zr.Close()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}
	// The server last: its presence means the rest is there.
	files := append([]*zip.File(nil), zr.File...)
	sort.SliceStable(files, func(i, j int) bool {
		return !strings.EqualFold(path.Base(files[i].Name), ServerExe) && strings.EqualFold(path.Base(files[j].Name), ServerExe)
	})
	n := 0
	for _, f := range files {
		if f.FileInfo().IsDir() || !RuntimeKept(f.Name) {
			continue
		}
		base := path.Base(strings.ReplaceAll(f.Name, `\`, "/"))
		if base == "." || base == ".." || base == "" || base == "/" {
			return n, fmt.Errorf("bad path in archive: %q", f.Name)
		}
		r, err := f.Open()
		if err != nil {
			return n, err
		}
		tmp := filepath.Join(dir, base+".part")
		w, err := os.Create(tmp)
		if err == nil {
			_, err = io.Copy(w, io.LimitReader(r, 500<<20))
			if cerr := w.Close(); err == nil {
				err = cerr
			}
		}
		r.Close()
		if err == nil {
			err = os.Rename(tmp, filepath.Join(dir, base))
		}
		if err != nil {
			os.Remove(tmp)
			return n, err
		}
		n++
	}
	return n, nil
}
