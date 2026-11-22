package calendar

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// Service keeps the meetings of a calendar feed up to date: it fetches the
// feed on start, when the link changes, on Refresh, and two minutes before
// every full and half hour, and keeps the meetings from yesterday to seven
// days ahead. One goroutine, bounded requests, each refresh replacing the
// last: it can run for months.
type Service struct {
	CacheFile string       // where meetings survive a restart ("" = none)
	Client    *http.Client // nil: a client with a 30 s timeout
	Logf      func(format string, args ...any)
	// OnUpdate is called (on the service's goroutine) after each refresh.
	OnUpdate func()
	now      func() time.Time

	mu        sync.Mutex
	url       string
	feed      *Feed // the last parsed feed, re-expanded as days pass
	meetings  []Meeting
	name      string
	etag      string
	lastMod   string
	lastOK    time.Time
	failSince time.Time
	lastErr   string
	busy      bool
	kick      chan struct{}
	started   bool
	done      chan struct{}
}

// Status is what the app shows.
type Status struct {
	Configured bool
	Name       string
	Meetings   []Meeting // yesterday to 7 days ahead, by start
	Updated    time.Time // last successful refresh
	Refreshing bool
	Err        string // the last failure, if the last refresh failed
	// Warn: refreshing has failed for at least a day.
	Warn      bool
	FailSince time.Time
}

const (
	fetchTimeout = 30 * time.Second
	warnAfter    = 24 * time.Hour
	horizon      = 7 // days ahead
)

func (s *Service) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *Service) logf(f string, a ...any) {
	if s.Logf != nil {
		s.Logf(f, a...)
	}
}

// Start loads the cache and starts refreshing for url ("" = none yet).
func (s *Service) Start(ctx context.Context, url string) {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.kick = make(chan struct{}, 1)
	s.done = make(chan struct{})
	s.url = Normalize(url)
	s.loadCache()
	s.mu.Unlock()
	go s.run(ctx)
}

// SetURL changes the feed: what is known is dropped and it loads at once.
func (s *Service) SetURL(u string) {
	u = Normalize(u)
	s.mu.Lock()
	if u == s.url {
		s.mu.Unlock()
		return
	}
	s.url, s.feed, s.meetings, s.name = u, nil, nil, ""
	s.etag, s.lastMod, s.lastOK, s.failSince, s.lastErr = "", "", time.Time{}, time.Time{}, ""
	s.saveCache()
	s.mu.Unlock()
	s.Refresh()
}

// Refresh asks for a refresh now (it never blocks).
func (s *Service) Refresh() {
	s.mu.Lock()
	k := s.kick
	s.mu.Unlock()
	if k == nil {
		return
	}
	select {
	case k <- struct{}{}:
	default: // one is pending already
	}
}

// Status returns a copy of what is known.
func (s *Service) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock()
	st := Status{Configured: s.url != "", Name: s.name, Updated: s.lastOK, Refreshing: s.busy, FailSince: s.failSince}
	if !s.failSince.IsZero() {
		st.Err = s.lastErr
		st.Warn = now.Sub(s.failSince) >= warnAfter
	}
	from, to := window(now)
	for _, m := range s.meetings {
		if m.End.After(from) && m.Start.Before(to) {
			st.Meetings = append(st.Meetings, m)
		}
	}
	return st
}

// window is yesterday's start to the end of the seventh day ahead.
func window(now time.Time) (time.Time, time.Time) {
	y, m, d := now.Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, now.Location())
	return today.AddDate(0, 0, -1), today.AddDate(0, 0, horizon+1)
}

// nextRefresh is the next xx:28 or xx:58 after now.
func nextRefresh(now time.Time) time.Time {
	t := now.Truncate(time.Minute)
	for i := 0; i < 61; i++ {
		t = t.Add(time.Minute)
		if t.Minute() == 28 || t.Minute() == 58 {
			return t
		}
	}
	return now.Add(30 * time.Minute)
}

// Wait returns once the service has stopped (its context ended).
func (s *Service) Wait() {
	s.mu.Lock()
	done := s.done
	s.mu.Unlock()
	if done != nil {
		<-done
	}
}

func (s *Service) run(ctx context.Context) {
	defer close(s.done)
	due := time.Time{} // zero: refresh at once
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		kicked := false
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		case <-s.kick:
			kicked = true
		}
		now := s.clock()
		// Wall-clock check: after the PC slept, a missed slot runs at once.
		if kicked || due.IsZero() || !now.Before(due) {
			s.refresh(ctx)
			due = nextRefresh(s.clock())
		}
		wait := min(time.Until(due), time.Minute)
		if wait < time.Second {
			wait = time.Second
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(wait)
	}
}

// refresh fetches and parses the feed once, keeping the old meetings (moved
// on to today's window) when it fails.
func (s *Service) refresh(ctx context.Context) {
	s.mu.Lock()
	u, etag, lastMod := s.url, s.etag, s.lastMod
	if u == "" {
		s.mu.Unlock()
		return
	}
	s.busy = true
	s.mu.Unlock()
	if s.OnUpdate != nil {
		s.OnUpdate()
	}

	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	res, err := s.fetch(ctx, u, etag, lastMod)
	cancel()

	s.mu.Lock()
	s.busy = false
	if s.url != u { // the link changed meanwhile: this result is stale
		s.mu.Unlock()
		return
	}
	now := s.clock()
	switch {
	case err != nil:
		if s.failSince.IsZero() {
			s.failSince = now
		}
		s.lastErr = err.Error()
		s.logf("calendar %s: %v", hostOf(u), err)
	default:
		if !res.notModified {
			s.feed, s.name = res.feed, res.feed.Name
			s.etag, s.lastMod = res.etag, res.lastMod
		}
		s.lastOK, s.failSince, s.lastErr = now, time.Time{}, ""
	}
	if s.feed != nil {
		from, to := window(now)
		s.meetings = s.feed.Meetings(from, to)
	}
	s.saveCache()
	n := len(s.meetings)
	s.mu.Unlock()
	if err == nil {
		s.logf("calendar %s: %d meetings in the window", hostOf(u), n)
	}
	if s.OnUpdate != nil {
		s.OnUpdate()
	}
}

type fetched struct {
	feed          *Feed
	etag, lastMod string
	notModified   bool
}

func (s *Service) fetch(ctx context.Context, u, etag, lastMod string) (fetched, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return fetched{}, errors.New("not a valid link")
	}
	req.Header.Set("User-Agent", "flit")
	req.Header.Set("Accept", "text/calendar, */*;q=0.5")
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	if lastMod != "" {
		req.Header.Set("If-Modified-Since", lastMod)
	}
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: fetchTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // without the link, which is a secret
		}
		return fetched{}, fmt.Errorf("couldn't load: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return fetched{notModified: true}, nil
	}
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return fetched{}, fmt.Errorf("the server answered %s", resp.Status)
	}
	feed, err := Parse(resp.Body)
	if err != nil {
		return fetched{}, err
	}
	return fetched{feed: feed, etag: resp.Header.Get("ETag"), lastMod: resp.Header.Get("Last-Modified")}, nil
}

// TestResult is what loading a link once found.
type TestResult struct {
	Name     string
	Events   int
	Upcoming []Meeting // the next 7 days
}

// Test loads and parses a link once, without changing the service.
func (s *Service) Test(ctx context.Context, u string) (TestResult, error) {
	u = Normalize(u)
	if u == "" {
		return TestResult{}, errors.New("no link")
	}
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	res, err := s.fetch(ctx, u, "", "")
	if err != nil {
		return TestResult{}, err
	}
	now := s.clock()
	return TestResult{Name: res.feed.Name, Events: res.feed.Events(), Upcoming: res.feed.Meetings(now, now.AddDate(0, 0, horizon))}, nil
}

// Normalize makes a pasted link fetchable: trimmed, webcal:// as https://.
func Normalize(u string) string {
	u = strings.TrimSpace(u)
	switch {
	case strings.HasPrefix(strings.ToLower(u), "webcals://"):
		u = "https://" + u[len("webcals://"):]
	case strings.HasPrefix(strings.ToLower(u), "webcal://"):
		u = "https://" + u[len("webcal://"):]
	}
	return u
}

// hostOf is a link's host, for logs (the rest of a subscription link is a
// secret).
func hostOf(u string) string {
	if p, err := url.Parse(u); err == nil && p.Host != "" {
		return p.Host
	}
	return "(link)"
}

// --- cache -----------------------------------------------------------------

type cacheFile struct {
	Link      string    `json:"link"` // a hash of the link, not the link
	Name      string    `json:"name,omitempty"`
	Meetings  []Meeting `json:"meetings"`
	LastOK    time.Time `json:"lastOk"`
	FailSince time.Time `json:"failSince,omitempty"`
	LastErr   string    `json:"lastErr,omitempty"`
}

func linkHash(u string) string {
	h := sha256.Sum256([]byte(u))
	return hex.EncodeToString(h[:8])
}

// loadCache restores the last meetings for the same link (mu held).
func (s *Service) loadCache() {
	if s.CacheFile == "" || s.url == "" {
		return
	}
	b, err := os.ReadFile(s.CacheFile)
	if err != nil {
		return
	}
	var c cacheFile
	if json.Unmarshal(b, &c) != nil || c.Link != linkHash(s.url) {
		return
	}
	s.name, s.meetings, s.lastOK, s.failSince, s.lastErr = c.Name, c.Meetings, c.LastOK, c.FailSince, c.LastErr
}

// saveCache writes what is known (mu held).
func (s *Service) saveCache() {
	if s.CacheFile == "" {
		return
	}
	if s.url == "" {
		_ = os.Remove(s.CacheFile)
		return
	}
	b, err := json.MarshalIndent(cacheFile{Link: linkHash(s.url), Name: s.name, Meetings: s.meetings,
		LastOK: s.lastOK, FailSince: s.failSince, LastErr: s.lastErr}, "", " ")
	if err != nil {
		return
	}
	tmp := s.CacheFile + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		_ = os.Rename(tmp, s.CacheFile)
	}
}
