// Package download fetches large files over HTTP(S): resumable, size- and
// SHA-256-checked, written to a ".part" file that is renamed into place only
// once complete and verified. Used for models, the optional engine
// downloads and the development test data.
package download

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// File describes one file to fetch.
type File struct {
	URL    string
	Dest   string // final path
	SHA256 string // hex, lower or upper case; "" skips the check
	Size   int64  // expected size in bytes; 0 if unknown
}

// Progress reports bytes done of total (total 0 if unknown).
type Progress func(done, total int64)

// ErrChecksum means the downloaded bytes didn't match the expected SHA-256.
var ErrChecksum = errors.New("checksum mismatch")

// Client fetches files. The zero value uses a client with sensible timeouts.
type Client struct {
	HTTP *http.Client
	// UserAgent is sent with every request. Keep it generic: it must not
	// identify the user.
	UserAgent string
}

func (c Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 0, Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ResponseHeaderTimeout: 30 * time.Second,
		IdleConnTimeout:       30 * time.Second,
	}}
}

// Get downloads f unless f.Dest already exists with the right size and
// hash. An interrupted earlier download (Dest + ".part") is resumed when
// the server supports ranges.
func (c Client) Get(ctx context.Context, f File, progress Progress) error {
	if ok, err := Verify(f); err == nil && ok {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(f.Dest), 0o755); err != nil {
		return err
	}
	part := f.Dest + ".part"
	for attempt := 0; ; attempt++ {
		err := c.fetch(ctx, f, part, progress)
		if err == nil {
			break
		}
		// A resumed file that turns out corrupt is discarded once and
		// fetched from the start.
		if errors.Is(err, ErrChecksum) && attempt == 0 {
			_ = os.Remove(part)
			continue
		}
		return err
	}
	return os.Rename(part, f.Dest)
}

func (c Client) fetch(ctx context.Context, f File, part string, progress Progress) error {
	var have int64
	if st, err := os.Stat(part); err == nil {
		have = st.Size()
		if f.Size > 0 && have > f.Size {
			_ = os.Remove(part)
			have = 0
		}
	}
	if f.Size > 0 && have == f.Size {
		return checkFile(part, f)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.URL, nil)
	if err != nil {
		return err
	}
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	if have > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", have))
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	flags := os.O_CREATE | os.O_WRONLY
	switch {
	case resp.StatusCode == http.StatusPartialContent && have > 0:
		flags |= os.O_APPEND
	case resp.StatusCode == http.StatusOK:
		flags |= os.O_TRUNC // no range support: start over
		have = 0
	case resp.StatusCode == http.StatusRequestedRangeNotSatisfiable && have > 0:
		// The part file is already complete (or the server changed);
		// check it as it is.
		return checkFile(part, f)
	default:
		return fmt.Errorf("GET %s: %s", redact(f.URL), resp.Status)
	}
	total := f.Size
	if total == 0 && resp.ContentLength > 0 {
		total = have + resp.ContentLength
	}

	out, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		return err
	}
	w := &countingWriter{w: out, done: have, total: total, progress: progress}
	_, copyErr := io.Copy(w, resp.Body)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	return checkFile(part, f)
}

// Verify reports whether f.Dest exists with the expected size and hash.
func Verify(f File) (bool, error) {
	st, err := os.Stat(f.Dest)
	if err != nil {
		return false, err
	}
	if f.Size > 0 && st.Size() != f.Size {
		return false, nil
	}
	if err := checkFile(f.Dest, f); err != nil {
		if errors.Is(err, ErrChecksum) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func checkFile(path string, f File) error {
	if f.Size > 0 {
		st, err := os.Stat(path)
		if err != nil {
			return err
		}
		if st.Size() != f.Size {
			return fmt.Errorf("%s: %d bytes, want %d: %w", filepath.Base(f.Dest), st.Size(), f.Size, ErrChecksum)
		}
	}
	if f.SHA256 == "" {
		return nil
	}
	sum, err := FileSHA256(path)
	if err != nil {
		return err
	}
	if !strings.EqualFold(sum, f.SHA256) {
		return fmt.Errorf("%s: sha256 %s, want %s: %w", filepath.Base(f.Dest), sum, strings.ToLower(f.SHA256), ErrChecksum)
	}
	return nil
}

// FileSHA256 returns the hex SHA-256 of a file.
func FileSHA256(path string) (string, error) {
	in, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer in.Close()
	h := sha256.New()
	if _, err := io.Copy(h, in); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// redact drops the query of a URL (signed URLs carry tokens there).
func redact(u string) string {
	if i := strings.IndexByte(u, '?'); i >= 0 {
		return u[:i] + "?…"
	}
	return u
}

type countingWriter struct {
	w           io.Writer
	done, total int64
	progress    Progress
	last        time.Time
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.done += int64(n)
	if c.progress != nil && (time.Since(c.last) > 100*time.Millisecond || (c.total > 0 && c.done == c.total)) {
		c.last = time.Now()
		c.progress(c.done, c.total)
	}
	return n, err
}
