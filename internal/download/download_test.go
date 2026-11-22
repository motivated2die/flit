package download

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func payload(n int) ([]byte, string) {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i * 7)
	}
	s := sha256.Sum256(b)
	return b, hex.EncodeToString(s[:])
}

func serve(t *testing.T, body []byte, ranges bool) (*httptest.Server, *atomic.Int32) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if ranges {
			http.ServeContent(w, r, "f", time.Time{}, bytes.NewReader(body))
			return
		}
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestGetVerifiesAndRenames(t *testing.T) {
	body, sum := payload(100_000)
	srv, _ := serve(t, body, true)
	dest := filepath.Join(t.TempDir(), "sub", "model.gguf")
	var last int64
	err := Client{}.Get(context.Background(), File{URL: srv.URL, Dest: dest, SHA256: strings.ToUpper(sum), Size: int64(len(body))},
		func(done, total int64) { last = done })
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dest)
	if !bytes.Equal(got, body) {
		t.Fatal("content differs")
	}
	if last != int64(len(body)) {
		t.Fatalf("last progress %d, want %d", last, len(body))
	}
	if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
		t.Fatal("part file left behind")
	}
}

func TestGetSkipsCompleteFile(t *testing.T) {
	body, sum := payload(1000)
	srv, hits := serve(t, body, true)
	dest := filepath.Join(t.TempDir(), "f")
	os.WriteFile(dest, body, 0o644)
	if err := (Client{}).Get(context.Background(), File{URL: srv.URL, Dest: dest, SHA256: sum}, nil); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 0 {
		t.Fatalf("%d requests for a file already there", hits.Load())
	}
}

func TestGetResumesPartFile(t *testing.T) {
	body, sum := payload(50_000)
	var gotRange string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRange = r.Header.Get("Range")
		http.ServeContent(w, r, "f", time.Time{}, bytes.NewReader(body))
	}))
	defer srv.Close()
	dest := filepath.Join(t.TempDir(), "f")
	os.WriteFile(dest+".part", body[:20_000], 0o644)
	if err := (Client{}).Get(context.Background(), File{URL: srv.URL, Dest: dest, SHA256: sum, Size: int64(len(body))}, nil); err != nil {
		t.Fatal(err)
	}
	if gotRange != "bytes=20000-" {
		t.Fatalf("Range %q", gotRange)
	}
	got, _ := os.ReadFile(dest)
	if !bytes.Equal(got, body) {
		t.Fatal("resumed content differs")
	}
}

func TestGetRestartsWithoutRangeSupport(t *testing.T) {
	body, sum := payload(30_000)
	srv, _ := serve(t, body, false)
	dest := filepath.Join(t.TempDir(), "f")
	os.WriteFile(dest+".part", []byte("garbage"), 0o644)
	if err := (Client{}).Get(context.Background(), File{URL: srv.URL, Dest: dest, SHA256: sum}, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dest)
	if !bytes.Equal(got, body) {
		t.Fatal("content differs")
	}
}

func TestGetRejectsWrongHash(t *testing.T) {
	body, _ := payload(1000)
	srv, hits := serve(t, body, true)
	dest := filepath.Join(t.TempDir(), "f")
	err := Client{}.Get(context.Background(), File{URL: srv.URL, Dest: dest, SHA256: strings.Repeat("0", 64)}, nil)
	if !errors.Is(err, ErrChecksum) {
		t.Fatalf("err = %v, want ErrChecksum", err)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatal("unverified file was put in place")
	}
	if hits.Load() != 2 {
		t.Fatalf("%d requests, want a retry from scratch (2)", hits.Load())
	}
}

func TestGetHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	err := Client{}.Get(context.Background(), File{URL: srv.URL + "/x?token=secret", Dest: filepath.Join(t.TempDir(), "f")}, nil)
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("err = %v (must fail and not leak the query)", err)
	}
}
