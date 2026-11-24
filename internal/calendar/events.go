package calendar

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Meeting is one occurrence of a calendar event: what flit shows and
// matches recordings with. Descriptions are not kept.
type Meeting struct {
	UID       string    `json:"uid"`
	Title     string    `json:"title"`
	Start     time.Time `json:"start"`
	End       time.Time `json:"end"`
	AllDay    bool      `json:"allDay,omitempty"`
	Recurring bool      `json:"recurring,omitempty"`
	Location  string    `json:"location,omitempty"` // a place in words ("" when it was only a link)
	Link      string    `json:"link,omitempty"`     // a meeting link (or the location's link)
	Provider  string    `json:"provider,omitempty"` // the link's meeting app: zoom, teams, meet, webex
	Free      bool      `json:"free,omitempty"`     // marked as not busy (TRANSP:TRANSPARENT)
}

// Key identifies an occurrence (a series' events share the UID).
func (m Meeting) Key() string { return m.UID + "@" + strconv.FormatInt(m.Start.Unix(), 10) }

// event is a VEVENT reduced to what flit uses.
type event struct {
	uid, title, location, link, provider string
	start                                civil
	zone                                 zone
	startT, endT                         time.Time
	allDay, cancelled, free, recurring   bool
	rule                                 *rule
	exdates                              map[int64]bool // instants (Unix seconds); dates for all-day: local midnight
	recurrenceID                         time.Time      // set on an override of one occurrence
}

// Feed is a parsed calendar: its events, ready to be expanded.
type Feed struct {
	Name   string // X-WR-CALNAME
	events []event
}

// Events is how many events (series count once) the feed has.
func (f *Feed) Events() int { return len(f.events) }

// Parse reads a calendar feed. A malformed event is skipped, not fatal;
// a panic in parsing comes back as an error.
func Parse(r io.Reader) (f *Feed, err error) {
	defer func() {
		if p := recover(); p != nil {
			f, err = nil, fmt.Errorf("calendar feed could not be read: %v", p)
		}
	}()
	cal, err := parse(r)
	if err != nil {
		return nil, err
	}
	zs := newZones(cal)
	f = &Feed{}
	if n, ok := cal.get("X-WR-CALNAME"); ok {
		f.Name = text(n.value)
	}
	for _, c := range cal.children {
		if c.name != "VEVENT" {
			continue
		}
		if ev, ok := readEvent(c, zs); ok {
			f.events = append(f.events, ev)
		}
	}
	return f, nil
}

func readEvent(c *component, zs *zones) (event, bool) {
	ev := event{exdates: map[int64]bool{}}
	if p, ok := c.get("UID"); ok {
		ev.uid = strings.TrimSpace(p.value)
	}
	if p, ok := c.get("SUMMARY"); ok {
		ev.title = strings.TrimSpace(text(p.value))
	}
	if ev.title == "" {
		ev.title = "(no title)"
	}
	ds, ok := c.get("DTSTART")
	if !ok {
		return ev, false
	}
	start, z, allDay, ok := readTime(ds, zs)
	if !ok {
		return ev, false
	}
	ev.start, ev.zone, ev.allDay = start, z, allDay
	ev.startT = z.at(start)
	ev.endT = ev.startT
	if de, ok := c.get("DTEND"); ok {
		if end, ez, _, ok := readTime(de, zs); ok {
			ev.endT = ez.at(end)
		}
	} else if du, ok := c.get("DURATION"); ok {
		ev.endT = ev.startT.Add(parseDuration(du.value))
	} else if allDay {
		ev.endT = z.at(start.addDays(1))
	}
	if ev.endT.Before(ev.startT) {
		ev.endT = ev.startT
	}
	if p, ok := c.get("STATUS"); ok && strings.EqualFold(strings.TrimSpace(p.value), "CANCELLED") {
		ev.cancelled = true
	}
	if p, ok := c.get("TRANSP"); ok && strings.EqualFold(strings.TrimSpace(p.value), "TRANSPARENT") {
		ev.free = true
	}
	if p, ok := c.get("RRULE"); ok {
		r := parseRule(p.value)
		if r.freq != "" {
			if !r.untilRaw.isZero() {
				r.until = r.untilRaw.instant(z)
			}
			ev.rule = &r
		}
	}
	for _, p := range c.all("EXDATE") {
		for _, v := range strings.Split(p.value, ",") {
			if t, _, _, ok := readTime(prop{params: p.params, value: v}, zs); ok {
				ev.exdates[zs.get(p.params["TZID"]).at(t).Unix()] = true
				if strings.HasSuffix(strings.TrimSpace(v), "Z") {
					ev.exdates[t.in(time.UTC).Unix()] = true
				}
			}
		}
	}
	if p, ok := c.get("RECURRENCE-ID"); ok {
		if t, rz, _, ok := readTime(p, zs); ok {
			ev.recurrenceID = rz.at(t)
		}
	}
	ev.recurring = ev.rule != nil || !ev.recurrenceID.IsZero() || len(c.all("RDATE")) > 0
	var loc, desc string
	if p, ok := c.get("LOCATION"); ok {
		loc = strings.TrimSpace(text(p.value))
	}
	if p, ok := c.get("DESCRIPTION"); ok {
		desc = text(p.value)
	}
	ev.location, ev.link, ev.provider = place(loc, desc)
	return ev, true
}

// readTime reads a DATE-TIME or DATE property: the wall time, its zone,
// and whether it is a date (all day).
func readTime(p prop, zs *zones) (civil, zone, bool, bool) {
	v := strings.TrimSpace(p.value)
	c, ok := parseCivil(v)
	if !ok {
		return c, zone{}, false, false
	}
	allDay := strings.EqualFold(p.params["VALUE"], "DATE") || len(strings.TrimSuffix(v, "Z")) == 8
	switch {
	case strings.HasSuffix(v, "Z"):
		return c, zone{loc: time.UTC}, allDay, true
	case allDay:
		return c, zone{loc: time.Local}, true, true // a date is the same day wherever the user is
	default:
		return c, zs.get(p.params["TZID"]), false, true
	}
}

// parseDuration reads an RFC 5545 DURATION (P1DT2H30M, PT45M, P1W).
func parseDuration(s string) time.Duration {
	s = strings.TrimSpace(s)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimLeft(s, "+-")
	s = strings.TrimPrefix(s, "P")
	var d time.Duration
	num := 0
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			num = num*10 + int(r-'0')
		case r == 'W':
			d += time.Duration(num) * 7 * 24 * time.Hour
			num = 0
		case r == 'D':
			d += time.Duration(num) * 24 * time.Hour
			num = 0
		case r == 'H':
			d += time.Duration(num) * time.Hour
			num = 0
		case r == 'M':
			d += time.Duration(num) * time.Minute
			num = 0
		case r == 'S':
			d += time.Duration(num) * time.Second
			num = 0
		}
	}
	if neg {
		return -d
	}
	return d
}

// --- places and meeting links -------------------------------------------------

var urlRe = regexp.MustCompile(`https?://[^\s<>"']+`)

// meetingHosts names the meeting apps by their links' hosts.
var meetingHosts = []struct{ suffix, provider string }{
	{"zoom.us", "zoom"}, {"zoom-x.de", "zoom"}, {"zoomgov.com", "zoom"}, {"zoom.com", "zoom"},
	{"teams.microsoft.com", "teams"}, {"teams.live.com", "teams"},
	{"meet.google.com", "meet"},
	{"webex.com", "webex"},
	{"slack.com", "slack"},
	{"discord.gg", "discord"}, {"discord.com", "discord"},
}

// place returns the location in words, a meeting link and its app. A link
// in the description counts only if it is a meeting app's (not a privacy
// notice or a signature's homepage); the location's own link always does.
func place(loc, desc string) (words, link, provider string) {
	if u := urlRe.FindString(loc); u != "" && strings.TrimSpace(urlRe.ReplaceAllString(loc, "")) == "" {
		link = cleanURL(u)
		provider = providerOf(link)
	} else {
		words = loc
		if u != "" {
			link = cleanURL(u)
			provider = providerOf(link)
			// The words without the link ("Teams: https://…" → "Teams").
			words = strings.TrimRight(strings.TrimSpace(urlRe.ReplaceAllString(loc, "")), ":-–,; ")
		}
	}
	if provider == "" {
		for _, u := range urlRe.FindAllString(desc, 50) {
			u = cleanURL(u)
			if p := providerOf(u); p != "" && isJoinLink(u, p) {
				link, provider = u, p
				break
			}
		}
	}
	if len([]rune(words)) > 200 {
		words = string([]rune(words)[:200]) + "…"
	}
	return words, link, provider
}

func cleanURL(u string) string { return strings.TrimRight(u, ").,;:>]}") }

func providerOf(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return ""
	}
	host := strings.ToLower(p.Hostname())
	for _, h := range meetingHosts {
		if host == h.suffix || strings.HasSuffix(host, "."+h.suffix) {
			return h.provider
		}
	}
	return ""
}

// isJoinLink tells a meeting's join link from other links of the same
// app in a description (dial-in pages, the app's homepage, logos).
func isJoinLink(u, provider string) bool {
	p, err := url.Parse(u)
	if err != nil {
		return false
	}
	path := p.Path
	switch provider {
	case "zoom":
		return strings.HasPrefix(path, "/j/") || strings.HasPrefix(path, "/my/") || strings.HasPrefix(path, "/w/") || strings.HasPrefix(path, "/s/")
	case "teams":
		return strings.Contains(path, "meetup-join") || strings.HasPrefix(path, "/meet/")
	case "meet":
		return len(strings.Trim(path, "/")) >= 10
	case "webex":
		return strings.Contains(path, "/j.php") || strings.Contains(path, "/meet/") || strings.Contains(path, "/join")
	}
	return len(strings.Trim(path, "/")) > 0
}

// --- expansion -------------------------------------------------------------------

// maxPeriods caps how many periods (days, weeks, …) a series is walked.
const maxPeriods = 20000

// ErrNoFeed is returned for a nil feed.
var ErrNoFeed = errors.New("no calendar")

// Meetings expands the feed's events into the occurrences that overlap
// [from, to), sorted by start. Cancelled ones are left out.
func (f *Feed) Meetings(from, to time.Time) []Meeting {
	if f == nil {
		return nil
	}
	// Overrides of single occurrences, by series and original start.
	overridden := map[string]bool{}
	for _, ev := range f.events {
		if !ev.recurrenceID.IsZero() {
			overridden[ev.uid+"@"+strconv.FormatInt(ev.recurrenceID.Unix(), 10)] = true
		}
	}
	var out []Meeting
	for i := range f.events {
		ev := &f.events[i]
		if ev.cancelled {
			continue
		}
		dur := ev.endT.Sub(ev.startT)
		add := func(t time.Time) {
			end := t.Add(dur)
			if !end.After(from) && !(dur == 0 && !t.Before(from)) || !t.Before(to) {
				return
			}
			out = append(out, Meeting{UID: ev.uid, Title: ev.title, Start: t, End: end, AllDay: ev.allDay,
				Recurring: ev.recurring, Location: ev.location, Link: ev.link, Provider: ev.provider, Free: ev.free})
		}
		if ev.rule == nil || !ev.recurrenceID.IsZero() {
			add(ev.startT)
			continue
		}
		ev.rule.expand(ev, from.Add(-dur), to, func(t time.Time) {
			if ev.exdates[t.Unix()] || overridden[ev.uid+"@"+strconv.FormatInt(t.Unix(), 10)] {
				return
			}
			add(t)
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].Start.Equal(out[j].Start) {
			return out[i].Start.Before(out[j].Start)
		}
		return out[i].Title < out[j].Title
	})
	return out
}
