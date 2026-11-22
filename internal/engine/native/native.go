// Package native carries transcribe.cpp's prebuilt Windows DLLs inside
// flit.exe and unpacks them to a versioned folder before the engine worker
// loads them (docs/decisions/0003-distribution-without-admin.md).
//
// The DLLs are not in git. `go run ./cmd/flitdev engine` downloads the
// pinned release bundle, checks it and extracts the CPU DLLs into files/.
// A build without them still compiles; Available then reports false.
package native

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// The pinned transcribe.cpp release.
const (
	Version      = "0.4.2"
	BundleName   = "transcribe-native-0.4.2-windows-x86_64-cpu-vulkan.tar.gz"
	BundleURL    = "https://github.com/handy-computer/transcribe.cpp/releases/download/v0.4.2/" + BundleName
	BundleSHA256 = "6094102cf57b404b050707ceb258b12ce647b92e801b5d46077214e5b030c8aa"
	BundleSize   = 18052817
	// HeaderHash is transcribe.h's ABI digest the Go binding was written
	// against (internal/engine/tcpp). A bundle whose contract.json names
	// another hash is refused.
	HeaderHash = "f99ad4169374b8f3"
)

// Kept lists which files of the bundle flit ships: the core and the CPU
// backends. ggml-vulkan.dll (44 MB) is left out; GPU support comes later
// as an optional download.
func Kept(name string) bool {
	base := path.Base(name)
	switch {
	case base == "contract.json":
		return true
	case strings.HasPrefix(name, "licenses/") || strings.Contains(name, "/licenses/"):
		return true
	case strings.EqualFold(path.Ext(base), ".dll"):
		return !strings.Contains(strings.ToLower(base), "vulkan")
	}
	return false
}

//go:embed all:files
var files embed.FS

// Contract is the bundle's contract.json.
type Contract struct {
	Version    string   `json:"version"`
	HeaderHash string   `json:"header_hash"`
	Backends   []string `json:"backends"`
	Lane       string   `json:"lane"`
}

// ErrMissing means this build carries no engine DLLs.
var ErrMissing = errors.New("this build has no engine DLLs (run: go run ./cmd/flitdev engine)")

// Available reports whether the DLLs are embedded.
func Available() bool {
	_, err := fs.Stat(files, "files/transcribe.dll")
	return err == nil
}

// ReadContract returns the embedded contract.json.
func ReadContract() (Contract, error) {
	return readContract(files, "files/contract.json")
}

func readContract(fsys fs.FS, name string) (Contract, error) {
	var c Contract
	b, err := fs.ReadFile(fsys, name)
	if err != nil {
		return c, ErrMissing
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("contract.json: %w", err)
	}
	return c, nil
}

// CheckContract refuses a bundle the binding wasn't written for.
func CheckContract(c Contract) error {
	if c.Version != Version {
		return fmt.Errorf("engine bundle %s, flit is built for %s", c.Version, Version)
	}
	if c.HeaderHash != HeaderHash {
		return fmt.Errorf("engine ABI %s, binding written for %s", c.HeaderHash, HeaderHash)
	}
	return nil
}

// Dir returns where the engine of this version lives under dataDir.
func Dir(dataDir string) string {
	return filepath.Join(dataDir, "engine", Version)
}

// Unpack writes the embedded files into Dir(dataDir), replacing any that
// differ, and returns that folder. Folders of other engine versions are
// removed. Unpacking is skipped for files already there and identical, so
// it costs only hashing on later starts.
func Unpack(dataDir string) (string, error) {
	if !Available() {
		return "", ErrMissing
	}
	c, err := ReadContract()
	if err != nil {
		return "", err
	}
	if err := CheckContract(c); err != nil {
		return "", err
	}
	sub, err := fs.Sub(files, "files")
	if err != nil {
		return "", err
	}
	dir := Dir(dataDir)
	if err := unpackFS(sub, dir); err != nil {
		return "", err
	}
	removeOtherVersions(filepath.Dir(dir), Version)
	return dir, nil
}

func unpackFS(fsys fs.FS, dir string) error {
	return fs.WalkDir(fsys, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || name == "README.md" {
			return nil
		}
		want, err := fs.ReadFile(fsys, name)
		if err != nil {
			return err
		}
		dest := filepath.Join(dir, filepath.FromSlash(name))
		if have, err := os.ReadFile(dest); err == nil && sha256.Sum256(have) == sha256.Sum256(want) {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		// Write next to it and rename, so a worker never loads half a DLL.
		tmp := dest + ".tmp"
		if err := os.WriteFile(tmp, want, 0o644); err != nil {
			return err
		}
		if err := os.Rename(tmp, dest); err != nil {
			_ = os.Remove(tmp)
			// A DLL loaded by a running worker can't be replaced; if it is
			// identical in content after all, that's fine.
			if have, rerr := os.ReadFile(dest); rerr == nil && bytes.Equal(have, want) {
				return nil
			}
			return err
		}
		return nil
	})
}

func removeOtherVersions(parent, keep string) {
	entries, err := os.ReadDir(parent)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() && e.Name() != keep {
			_ = os.RemoveAll(filepath.Join(parent, e.Name())) // in use by an old worker: next time
		}
	}
}
