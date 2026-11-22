package calendar

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// feedServer serves testdata/exchange.ics with an ETag, or fails.
type feedServer struct {
	fail     atomic.Bool
	requests atomic.Int32
	notMod   atomic.Int32
}

func (f *feedServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.requests.Add(1)
	if f.fail.Load() {
		http.Error(w, "down", http.StatusServiceUnavailable)
		return
	}
	if r.Header.Get("If-None-Match") == `"v1"` {
		f.notMod.Add(1)
		w.WriteHeader(http.StatusNotModified)
		return
	}
	b, _ := os.ReadFile("testdata/exchange.ics")
	w.Header().Set("ETag", `"v1"`)
	w.Write(b)
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestServiceRefreshCacheAndFailures(t *testing.T) {
	srv := &feedServer{}
	ts := httptest.NewServer(srv)
	defer ts.Close()
	var mu sync.Mutex
	now := at(2026, 11, 21, 12, 0)
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	cache := filepath.Join(t.TempDir(), "calendar.json")
	updates := make(chan struct{}, 100)
	s := &Service{CacheFile: cache, now: clock, OnUpdate: func() { updates <- struct{}{} }}
	ctx, cancel := context.WithCancel(context.Background())
	defer s.Wait()
	defer cancel()
	s.Start(ctx, ts.URL)
	waitFor(t, func() bool { return !s.Status().Updated.IsZero() })
	st := s.Status()
	if st.Name != "Kalender" || len(st.Meetings) != 2 || st.Err != "" || st.Warn {
		t.Fatalf("%+v", st)
	}
	// The link never reaches the cache.
	b, _ := os.ReadFile(cache)
	if strings.Contains(string(b), ts.URL) || !strings.Contains(string(b), "AG Projekt") {
		t.Fatalf("cache: %s", b)
	}
	// Unchanged feed: a conditional request, the same meetings.
	s.Refresh()
	waitFor(t, func() bool { return srv.notMod.Load() == 1 && !s.Status().Refreshing })
	if len(s.Status().Meetings) != 2 {
		t.Fatal("meetings lost on 304")
	}
	// Failures: silent for a day, then a warning; meetings are kept.
	srv.fail.Store(true)
	s.Refresh()
	waitFor(t, func() bool { return s.Status().Err != "" })
	if st := s.Status(); st.Warn || len(st.Meetings) != 2 || !strings.Contains(st.Err, "503") {
		t.Fatalf("after one failure: %+v", st)
	}
	mu.Lock()
	now = now.Add(25 * time.Hour)
	mu.Unlock()
	s.Refresh()
	waitFor(t, func() bool { return s.Status().Warn })
	// Recovered: the warning goes.
	srv.fail.Store(false)
	s.Refresh()
	waitFor(t, func() bool { return !s.Status().Warn && s.Status().Err == "" })

	// A restart shows the cached meetings before any request.
	s2 := &Service{CacheFile: cache, now: clock}
	s2.mu.Lock()
	s2.url = ts.URL
	s2.loadCache()
	s2.mu.Unlock()
	if len(s2.Status().Meetings) == 0 {
		t.Fatal("cache not restored")
	}
	// Another link: nothing of the old one.
	s.SetURL(ts.URL + "/other.ics")
	if st := s.Status(); len(st.Meetings) != 0 || st.Name != "" {
		t.Fatalf("old meetings kept for a new link: %+v", st)
	}
}

func TestServiceTestAndErrors(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "login") {
			w.Write([]byte("<html>Please sign in</html>"))
			return
		}
		b, _ := os.ReadFile("testdata/exchange.ics")
		w.Write(b)
	}))
	defer ts.Close()
	s := &Service{now: func() time.Time { return at(2026, 11, 21, 12, 0) }}
	res, err := s.Test(context.Background(), ts.URL+"/cal.ics")
	if err != nil || res.Events != 5 || len(res.Upcoming) != 2 {
		t.Fatalf("%+v %v", res, err)
	}
	if _, err := s.Test(context.Background(), ts.URL+"/login"); err != ErrNotCalendar {
		t.Fatalf("login page: %v", err)
	}
	// A refused connection says why, without the link.
	_, err = s.Test(context.Background(), "http://127.0.0.1:1/secret-token.ics")
	if err == nil || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("err %v", err)
	}
}

func TestNextRefreshAndNormalize(t *testing.T) {
	for in, want := range map[string]string{"10:00": "10:28", "10:28": "10:58", "10:30": "10:58", "10:58:30": "11:28", "23:59": "00:28"} {
		h, _ := time.Parse("15:04", in[:5])
		now := time.Date(2026, 11, 21, h.Hour(), h.Minute(), 0, 0, berlin)
		if len(in) > 5 {
			now = now.Add(30 * time.Second)
		}
		if got := nextRefresh(now).Format("15:04"); got != want {
			t.Errorf("%s → %s, want %s", in, got, want)
		}
	}
	if Normalize(" webcal://outlook.office365.com/x/calendar.ics ") != "https://outlook.office365.com/x/calendar.ics" {
		t.Fatal("webcal")
	}
}
