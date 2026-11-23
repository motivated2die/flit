// Package update finds flit's latest published release on GitHub and
// downloads its exe, checked against the release's SHA256SUMS.txt
// (docs/decisions/0010-updates-from-github-releases.md).
//
// Only full releases count: GitHub's "latest release" already skips drafts
// and pre-releases, and a tag with a pre-release suffix (v0.2.0-rc.1) is
// refused here as well. Installing is not done here: the downloaded exe is
// started and installs itself like any downloaded copy (package install).
package update

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/motivated2die/flit/internal/download"
)

const (
	// DefaultAPI is the GitHub API base of flit's repository.
	DefaultAPI = "https://api.github.com/repos/motivated2die/flit"
	// ReleasesPage lists every release.
	ReleasesPage = "https://github.com/motivated2die/flit/releases"

	// AssetName and SumsName are the files every release carries
	// (.github/workflows/release.yml).
	AssetName = "flit.exe"
	SumsName  = "SHA256SUMS.txt"

	maxJSONBytes = 1 << 20
	maxSumsBytes = 64 << 10
	// MaxExeBytes bounds the exe a release may offer (flit.exe with its
	// engine embedded is about 30 MB).
	MaxExeBytes = 256 << 20
)

// ErrNoRelease means the repository has no published full release (yet),
// or the latest one carries no exe.
var ErrNoRelease = errors.New("no published release")

// Release is a published release with an exe and its checksums.
type Release struct {
	Version string // "0.2.0", the tag without its "v"
	Page    string // the release's page on GitHub (its notes)
	exeURL  string
	sumsURL string
	exeSize int64
}

// Client asks GitHub's API (or, for testing, another API base).
type Client struct {
	// API is the repository's API base; "" means DefaultAPI. Downloads
	// are only taken from GitHub's hosts, or with another API base from
	// that base's host alone.
	API string
	// UserAgent is required by GitHub. Keep it generic ("flit/0.2.0"):
	// it must not identify the user.
	UserAgent string
	// HTTP is the base client; nil means one with timeouts.
	HTTP *http.Client
}

func (c Client) api() string {
	if c.API == "" {
		return DefaultAPI
	}
	return strings.TrimRight(c.API, "/")
}

// CheckAPI reports whether api may replace DefaultAPI: HTTPS, or plain
// HTTP to this PC only (flitdev release-server).
func CheckAPI(api string) error {
	u, err := url.Parse(api)
	if err != nil {
		return err
	}
	switch {
	case u.Scheme == "https" && u.Host != "":
		return nil
	case u.Scheme == "http" && isLoopback(u.Hostname()):
		return nil
	}
	return fmt.Errorf("%q must be https, or http to this PC", api)
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// allowed reports whether u may be fetched: GitHub over HTTPS (the API,
// github.com, and the *.githubusercontent.com hosts assets redirect to),
// or the test API's own host.
func (c Client) allowed(u *url.URL) bool {
	if c.api() != DefaultAPI {
		base, err := url.Parse(c.api())
		return err == nil && u.Scheme == base.Scheme && strings.EqualFold(u.Host, base.Host)
	}
	host := strings.ToLower(u.Hostname())
	return u.Scheme == "https" &&
		(host == "github.com" || host == "api.github.com" || strings.HasSuffix(host, ".githubusercontent.com"))
}

// http is the base client with every redirect held to allowed hosts.
func (c Client) http(timeout time.Duration) *http.Client {
	cl := http.Client{Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ResponseHeaderTimeout: 30 * time.Second,
		IdleConnTimeout:       30 * time.Second,
	}}
	if c.HTTP != nil {
		cl = *c.HTTP
	}
	if timeout > 0 && cl.Timeout == 0 {
		cl.Timeout = timeout
	}
	cl.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		if !c.allowed(req.URL) {
			return fmt.Errorf("redirected to an unexpected host %s", req.URL.Host)
		}
		return nil
	}
	return &cl
}

func (c Client) get(ctx context.Context, u string, timeout time.Duration, accept string) (*http.Response, error) {
	parsed, err := url.Parse(u)
	if err != nil {
		return nil, err
	}
	if !c.allowed(parsed) {
		return nil, fmt.Errorf("unexpected host %s", parsed.Host)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	req.Header.Set("Accept", accept)
	return c.http(timeout).Do(req)
}

// Latest returns the latest published full release.
func (c Client) Latest(ctx context.Context) (Release, error) {
	resp, err := c.get(ctx, c.api()+"/releases/latest", 20*time.Second, "application/vnd.github+json")
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return Release{}, ErrNoRelease
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests:
		return Release{}, fmt.Errorf("GitHub refused the request (%s), probably its rate limit; try again later", resp.Status)
	case resp.StatusCode != http.StatusOK:
		return Release{}, fmt.Errorf("GitHub answered %s", resp.Status)
	}
	var body struct {
		Tag        string `json:"tag_name"`
		Page       string `json:"html_url"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
		Assets     []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
			Size int64  `json:"size"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxJSONBytes)).Decode(&body); err != nil {
		return Release{}, fmt.Errorf("reading GitHub's answer: %w", err)
	}
	ver, ok := strings.CutPrefix(body.Tag, "v")
	v, valid := parse(ver)
	if !ok || !valid {
		return Release{}, fmt.Errorf("the latest release has no version tag (%q)", body.Tag)
	}
	if body.Draft || body.Prerelease || v.pre != "" {
		return Release{}, ErrNoRelease
	}
	rel := Release{Version: ver, Page: body.Page}
	for _, a := range body.Assets {
		switch a.Name {
		case AssetName:
			rel.exeURL, rel.exeSize = a.URL, a.Size
		case SumsName:
			rel.sumsURL = a.URL
		}
	}
	if rel.exeURL == "" || rel.sumsURL == "" {
		return Release{}, fmt.Errorf("release %s lacks %s or %s: %w", body.Tag, AssetName, SumsName, ErrNoRelease)
	}
	if rel.exeSize <= 0 || rel.exeSize > MaxExeBytes {
		return Release{}, fmt.Errorf("release %s: %s has an unexpected size (%d bytes)", body.Tag, AssetName, rel.exeSize)
	}
	return rel, nil
}

// Download fetches the release's exe into dir as flit.exe, checked against
// SHA256SUMS.txt, and returns its path. An interrupted earlier download of
// the same release resumes. progress may be nil.
func (c Client) Download(ctx context.Context, rel Release, dir string, progress download.Progress) (string, error) {
	sum, err := c.checksum(ctx, rel)
	if err != nil {
		return "", err
	}
	// Each release gets its own folder, so a .part of another release is
	// never resumed into this one.
	path := filepath.Join(dir, rel.Version, AssetName)
	parsed, err := url.Parse(rel.exeURL)
	if err != nil || !c.allowed(parsed) {
		return "", fmt.Errorf("unexpected download host for %s", AssetName)
	}
	dl := download.Client{HTTP: c.http(0), UserAgent: c.UserAgent}
	f := download.File{URL: rel.exeURL, Dest: path, SHA256: sum, Size: rel.exeSize}
	if err := dl.Get(ctx, f, progress); err != nil {
		return "", fmt.Errorf("downloading %s: %w", AssetName, err)
	}
	return path, nil
}

// checksum reads the exe's SHA-256 from the release's SHA256SUMS.txt.
func (c Client) checksum(ctx context.Context, rel Release) (string, error) {
	resp, err := c.get(ctx, rel.sumsURL, time.Minute, "application/octet-stream")
	if err != nil {
		return "", fmt.Errorf("downloading %s: %w", SumsName, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("downloading %s: the server answered %s", SumsName, resp.Status)
	}
	sum, ok := parseSums(io.LimitReader(resp.Body, maxSumsBytes), AssetName)
	if !ok {
		return "", fmt.Errorf("%s lists no checksum for %s", SumsName, AssetName)
	}
	return sum, nil
}

// parseSums finds name in sha256sum output: "<hex>  <name>", or
// "<hex> *<name>" in binary mode.
func parseSums(r io.Reader, name string) (string, bool) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) != 2 || strings.TrimPrefix(fields[1], "*") != name {
			continue
		}
		sum := strings.ToLower(fields[0])
		if b, err := hex.DecodeString(sum); err == nil && len(b) == sha256.Size {
			return sum, true
		}
	}
	if sc.Err() != nil {
		return "", false
	}
	return "", false
}

// --- versions ----------------------------------------------------------------

type version struct {
	nums [3]int
	pre  string // the pre-release suffix without its "-": "rc.1", "dev"
}

// parse reads major.minor.patch with an optional -suffix.
func parse(s string) (version, bool) {
	core, pre, hasPre := strings.Cut(s, "-")
	if hasPre && pre == "" {
		return version{}, false
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return version{}, false
	}
	var v version
	for i, p := range parts {
		if p == "" || p[0] < '0' || p[0] > '9' {
			return version{}, false
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return version{}, false
		}
		v.nums[i] = n
	}
	v.pre = pre
	return v, true
}

// Newer reports whether the release latest is newer than the running
// version current. A full release is newer than a pre-release or
// development build of the same numbers (0.2.0 > 0.2.0-dev); a
// pre-release is never offered. An unreadable current version counts as
// older than any release.
func Newer(latest, current string) bool {
	l, ok := parse(latest)
	if !ok || l.pre != "" {
		return false
	}
	c, ok := parse(current)
	if !ok {
		return true
	}
	for i := range l.nums {
		if l.nums[i] != c.nums[i] {
			return l.nums[i] > c.nums[i]
		}
	}
	return c.pre != ""
}
