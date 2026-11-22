package update

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewer(t *testing.T) {
	for _, tc := range []struct {
		latest, current string
		want            bool
	}{
		{"0.2.0", "0.1.0", true},
		{"0.2.0", "0.2.0", false},
		{"0.1.9", "0.2.0", false},
		{"0.10.0", "0.9.0", true}, // numbers, not text
		{"1.0.0", "0.99.99", true},
		{"0.2.1", "0.2.0", true},
		{"0.2.0", "0.2.0-dev", true}, // a release beats a dev build of its numbers
		{"0.2.0", "0.2.0-rc.2", true},
		{"0.2.0", "0.3.0-dev", false},
		{"0.3.0-rc.1", "0.2.0", false}, // pre-releases are never offered
		{"0.2.0", "", true},            // unknown running version
		{"garbage", "0.1.0", false},
		{"0.2", "0.1.0", false},
		{"0.2.0-", "0.1.0", false},
		{"+1.0.0", "0.1.0", false},
	} {
		if got := Newer(tc.latest, tc.current); got != tc.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", tc.latest, tc.current, got, tc.want)
		}
	}
}

func TestParseSums(t *testing.T) {
	sum := strings.Repeat("ab", 32)
	for _, tc := range []struct {
		in, want string
		ok       bool
	}{
		{sum + "  flit.exe\n", sum, true},
		{sum + " *flit.exe\r\n", sum, true},
		{strings.ToUpper(sum) + "  flit.exe", sum, true},
		{"other  x.txt\n" + sum + "  flit.exe\n", sum, true},
		{sum + "  other.exe\n", "", false},
		{"abcd  flit.exe\n", "", false}, // too short
		{"", "", false},
	} {
		got, ok := parseSums(strings.NewReader(tc.in), AssetName)
		if got != tc.want || ok != tc.ok {
			t.Errorf("parseSums(%q) = %q, %v; want %q, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestCheckAPI(t *testing.T) {
	for api, ok := range map[string]bool{
		"https://example.com/repos/a/b": true,
		"http://127.0.0.1:8090":         true,
		"http://localhost:8090":         true,
		"http://[::1]:8090":             true,
		"http://example.com":            false,
		"ftp://127.0.0.1":               false,
		"https://":                      false,
	} {
		if err := CheckAPI(api); (err == nil) != ok {
			t.Errorf("CheckAPI(%q) = %v, want ok=%v", api, err, ok)
		}
	}
}

func TestAllowedHosts(t *testing.T) {
	gh := Client{}
	for u, ok := range map[string]bool{
		"https://api.github.com/repos/motivated2die/flit/releases/latest":         true,
		"https://github.com/motivated2die/flit/releases/download/v0.2.0/flit.exe": true,
		"https://objects.githubusercontent.com/x":                                 true,
		"https://release-assets.githubusercontent.com/x":                          true,
		"http://github.com/x":                 false, // not https
		"https://github.com.evil.example/x":   false,
		"https://evilgithubusercontent.com/x": false,
	} {
		p, _ := url.Parse(u)
		if got := gh.allowed(p); got != ok {
			t.Errorf("allowed(%q) = %v, want %v", u, got, ok)
		}
	}
	test := Client{API: "http://127.0.0.1:8090"}
	for u, ok := range map[string]bool{
		"http://127.0.0.1:8090/download/flit.exe": true,
		"http://127.0.0.1:9999/download/flit.exe": false,
		"https://github.com/x":                    false, // a test API stays on its host
	} {
		p, _ := url.Parse(u)
		if got := test.allowed(p); got != ok {
			t.Errorf("test API allowed(%q) = %v, want %v", u, got, ok)
		}
	}
}

// fakeGitHub serves one release the way GitHub's API does.
type fakeGitHub struct {
	tag        string
	prerelease bool
	exe        []byte
	sum        string // "" means the exe's real SHA-256
	status     int    // answer /releases/latest with this instead
	exeURL     string // "" means the server's own /download/flit.exe
	noSums     bool
	redirect   string // the exe's URL redirects here
	agents     []string
}

func (f *fakeGitHub) start(t *testing.T) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		f.agents = append(f.agents, r.UserAgent())
		if f.status != 0 {
			w.WriteHeader(f.status)
			return
		}
		exeURL := f.exeURL
		if exeURL == "" {
			exeURL = srv.URL + "/download/flit.exe"
		}
		assets := []map[string]any{{"name": "flit.exe", "browser_download_url": exeURL, "size": len(f.exe)}}
		if !f.noSums {
			assets = append(assets, map[string]any{"name": "SHA256SUMS.txt", "browser_download_url": srv.URL + "/download/SHA256SUMS.txt", "size": 80})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": f.tag, "html_url": srv.URL + "/notes", "draft": false, "prerelease": f.prerelease, "assets": assets,
		})
	})
	mux.HandleFunc("/download/flit.exe", func(w http.ResponseWriter, r *http.Request) {
		if f.redirect != "" {
			http.Redirect(w, r, f.redirect, http.StatusFound)
			return
		}
		http.ServeContent(w, r, "flit.exe", time.Time{}, bytes.NewReader(f.exe))
	})
	mux.HandleFunc("/download/SHA256SUMS.txt", func(w http.ResponseWriter, r *http.Request) {
		sum := f.sum
		if sum == "" {
			h := sha256.Sum256(f.exe)
			sum = hex.EncodeToString(h[:])
		}
		fmt.Fprintf(w, "%s  flit.exe\n", sum)
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestLatestAndDownload(t *testing.T) {
	gh := &fakeGitHub{tag: "v0.2.0", exe: bytes.Repeat([]byte("flit"), 50_000)}
	srv := gh.start(t)
	c := Client{API: srv.URL, UserAgent: "flit/0.1.0"}

	rel, err := c.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rel.Version != "0.2.0" || rel.Page != srv.URL+"/notes" {
		t.Fatalf("release %+v", rel)
	}
	if len(gh.agents) != 1 || gh.agents[0] != "flit/0.1.0" {
		t.Fatalf("user agents %q", gh.agents)
	}
	var last int64
	path, err := c.Download(context.Background(), rel, t.TempDir(), func(done, total int64) { last = done })
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, gh.exe) {
		t.Fatalf("downloaded %d bytes, %v", len(got), err)
	}
	if last != int64(len(gh.exe)) {
		t.Fatalf("progress ended at %d of %d", last, len(gh.exe))
	}
}

func TestDownloadChecksumMismatch(t *testing.T) {
	gh := &fakeGitHub{tag: "v0.2.0", exe: []byte("not the real flit"), sum: strings.Repeat("0", 64)}
	srv := gh.start(t)
	c := Client{API: srv.URL}
	rel, err := c.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if _, err := c.Download(context.Background(), rel, dir, nil); err == nil {
		t.Fatal("a wrong checksum was accepted")
	}
	if _, err := os.Stat(filepath.Join(dir, "0.2.0", AssetName)); err == nil {
		t.Fatal("the unchecked exe was left in place")
	}
}

func TestLatestNoRelease(t *testing.T) {
	for name, gh := range map[string]*fakeGitHub{
		"none yet":         {status: http.StatusNotFound},
		"pre-release":      {tag: "v0.2.0-rc.1", exe: []byte("x"), prerelease: true},
		"pre-release tag":  {tag: "v0.2.0-rc.1", exe: []byte("x")},
		"without checksum": {tag: "v0.2.0", exe: []byte("x"), noSums: true},
	} {
		srv := gh.start(t)
		if _, err := (Client{API: srv.URL}).Latest(context.Background()); !errors.Is(err, ErrNoRelease) {
			t.Errorf("%s: %v, want ErrNoRelease", name, err)
		}
	}
}

func TestLatestErrors(t *testing.T) {
	for name, gh := range map[string]*fakeGitHub{
		"rate limit": {status: http.StatusForbidden},
		"server":     {status: http.StatusBadGateway},
		"no v":       {tag: "0.2.0", exe: []byte("x")},
		"empty exe":  {tag: "v0.2.0"},
	} {
		srv := gh.start(t)
		_, err := (Client{API: srv.URL}).Latest(context.Background())
		if err == nil || errors.Is(err, ErrNoRelease) {
			t.Errorf("%s: %v, want an error", name, err)
		}
	}
}

func TestDownloadForeignHost(t *testing.T) {
	gh := &fakeGitHub{tag: "v0.2.0", exe: []byte("x"), exeURL: "http://127.0.0.2:1/flit.exe"}
	srv := gh.start(t)
	c := Client{API: srv.URL}
	rel, err := c.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Download(context.Background(), rel, t.TempDir(), nil); err == nil || !strings.Contains(err.Error(), "host") {
		t.Fatalf("an exe from another host: %v", err)
	}
}

func TestDownloadRedirectAway(t *testing.T) {
	away := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("x")) }))
	defer away.Close()
	// The exe's URL is on the API's host but redirects to another.
	gh := &fakeGitHub{tag: "v0.2.0", exe: []byte("x"), redirect: away.URL + "/flit.exe"}
	srv := gh.start(t)
	c := Client{API: srv.URL}
	rel, err := c.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Download(context.Background(), rel, t.TempDir(), nil); err == nil || !strings.Contains(err.Error(), "unexpected host") {
		t.Fatalf("a redirect to another host: %v", err)
	}
}
