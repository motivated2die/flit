package calendar

import (
	"sort"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // IANA zones on PCs without Go's zoneinfo
)

// civil is a wall-clock date and time without a zone.
type civil struct {
	y           int
	m           time.Month
	d, h, mi, s int
}

func (c civil) in(loc *time.Location) time.Time {
	return time.Date(c.y, c.m, c.d, c.h, c.mi, c.s, 0, loc)
}

func civilOf(t time.Time) civil {
	return civil{t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second()}
}

// addDays moves a civil date by n days (clock unchanged).
func (c civil) addDays(n int) civil {
	t := time.Date(c.y, c.m, c.d+n, 0, 0, 0, 0, time.UTC)
	c.y, c.m, c.d = t.Year(), t.Month(), t.Day()
	return c
}

func (c civil) weekday() time.Weekday {
	return time.Date(c.y, c.m, c.d, 0, 0, 0, 0, time.UTC).Weekday()
}

// zone turns wall-clock times of one TZID into instants.
type zone struct {
	loc *time.Location // a known zone
	vtz *vtimezone     // else the feed's own rules
}

func (z zone) at(c civil) time.Time {
	if z.loc != nil {
		return c.in(z.loc)
	}
	if z.vtz != nil {
		return z.vtz.at(c)
	}
	return c.in(time.Local)
}

// windowsZones maps the Windows zone names Exchange and Outlook write as
// TZID to IANA zones (the common ones; others fall back to VTIMEZONE).
var windowsZones = map[string]string{
	"W. Europe Standard Time":         "Europe/Berlin",
	"Romance Standard Time":           "Europe/Paris",
	"Central Europe Standard Time":    "Europe/Budapest",
	"Central European Standard Time":  "Europe/Warsaw",
	"GMT Standard Time":               "Europe/London",
	"Greenwich Standard Time":         "Atlantic/Reykjavik",
	"E. Europe Standard Time":         "Europe/Chisinau",
	"FLE Standard Time":               "Europe/Kiev",
	"GTB Standard Time":               "Europe/Bucharest",
	"Russian Standard Time":           "Europe/Moscow",
	"Turkey Standard Time":            "Europe/Istanbul",
	"Israel Standard Time":            "Asia/Jerusalem",
	"South Africa Standard Time":      "Africa/Johannesburg",
	"Arabian Standard Time":           "Asia/Dubai",
	"India Standard Time":             "Asia/Kolkata",
	"China Standard Time":             "Asia/Shanghai",
	"Singapore Standard Time":         "Asia/Singapore",
	"Tokyo Standard Time":             "Asia/Tokyo",
	"Korea Standard Time":             "Asia/Seoul",
	"AUS Eastern Standard Time":       "Australia/Sydney",
	"New Zealand Standard Time":       "Pacific/Auckland",
	"Eastern Standard Time":           "America/New_York",
	"Central Standard Time":           "America/Chicago",
	"Mountain Standard Time":          "America/Denver",
	"Pacific Standard Time":           "America/Los_Angeles",
	"Atlantic Standard Time":          "America/Halifax",
	"E. South America Standard Time":  "America/Sao_Paulo",
	"SA Pacific Standard Time":        "America/Bogota",
	"UTC":                             "UTC",
	"Coordinated Universal Time":      "UTC",
	"W. Central Africa Standard Time": "Africa/Lagos",
}

// zones resolves the TZIDs of a feed.
type zones struct {
	defs  map[string]*vtimezone
	cache map[string]zone
}

func newZones(cal *component) *zones {
	z := &zones{defs: map[string]*vtimezone{}, cache: map[string]zone{}}
	for _, c := range cal.children {
		if c.name != "VTIMEZONE" {
			continue
		}
		if id, ok := c.get("TZID"); ok {
			if v := parseVTimezone(c); v != nil {
				z.defs[id.value] = v
			}
		}
	}
	return z
}

func (z *zones) get(tzid string) zone {
	if tzid == "" {
		return zone{loc: time.Local}
	}
	if r, ok := z.cache[tzid]; ok {
		return r
	}
	r := zone{}
	if loc, err := time.LoadLocation(tzid); err == nil && tzid != "Local" {
		r.loc = loc
	} else if name, ok := windowsZones[tzid]; ok {
		r.loc, _ = time.LoadLocation(name)
	} else if i := strings.LastIndex(tzid, "/"); i > 0 { // "/mozilla.org/…/Europe/Berlin"
		parts := strings.Split(strings.Trim(tzid, "/"), "/")
		if len(parts) >= 2 {
			if loc, err := time.LoadLocation(parts[len(parts)-2] + "/" + parts[len(parts)-1]); err == nil {
				r.loc = loc
			}
		}
	}
	if r.loc == nil {
		r.vtz = z.defs[tzid]
	}
	z.cache[tzid] = r
	return r
}

// vtimezone is a VTIMEZONE's rules: onsets of standard and daylight time.
type vtimezone struct {
	rules []tzRule
}

type tzRule struct {
	start    civil // first onset, local time before the change
	from, to int   // UTC offsets in seconds
	month    time.Month
	nth      int // BYDAY ordinal (−1 = last); 0 with day>0: BYMONTHDAY
	wd       time.Weekday
	day      int
	yearly   bool
}

func parseVTimezone(c *component) *vtimezone {
	v := &vtimezone{}
	for _, ch := range c.children {
		if ch.name != "STANDARD" && ch.name != "DAYLIGHT" {
			continue
		}
		ds, ok1 := ch.get("DTSTART")
		fr, ok2 := ch.get("TZOFFSETFROM")
		to, ok3 := ch.get("TZOFFSETTO")
		if !ok1 || !ok2 || !ok3 {
			continue
		}
		start, ok := parseCivil(ds.value)
		if !ok {
			continue
		}
		r := tzRule{start: start, from: parseOffset(fr.value), to: parseOffset(to.value), month: start.m, day: start.d}
		if rr, ok := ch.get("RRULE"); ok {
			rule := parseRule(rr.value)
			if rule.freq == "YEARLY" {
				r.yearly = true
				if len(rule.byMonth) > 0 {
					r.month = time.Month(rule.byMonth[0])
				}
				if len(rule.byDay) > 0 {
					r.nth, r.wd, r.day = rule.byDay[0].n, rule.byDay[0].wd, 0
				} else if len(rule.byMonthDay) > 0 {
					r.day = rule.byMonthDay[0]
				}
			}
		}
		v.rules = append(v.rules, r)
	}
	if len(v.rules) == 0 {
		return nil
	}
	return v
}

// onset returns rule r's change in year y as an instant.
func (r tzRule) onset(y int) (time.Time, bool) {
	if y < r.start.y || (!r.yearly && y != r.start.y) {
		return time.Time{}, false
	}
	d := r.start
	d.y = y
	if r.yearly {
		d.m = r.month
		if r.day > 0 {
			d.d = r.day
		} else if day, ok := nthWeekday(y, r.month, r.nth, r.wd); ok {
			d.d = day
		} else {
			return time.Time{}, false
		}
	}
	// The onset is given in the local time in force before it.
	return d.in(time.UTC).Add(-time.Duration(r.from) * time.Second), true
}

// at converts a wall time with the offset in force then.
func (v *vtimezone) at(c civil) time.Time {
	type change struct {
		t  time.Time
		to int
	}
	var changes []change
	for y := c.y - 1; y <= c.y+1; y++ {
		for _, r := range v.rules {
			if t, ok := r.onset(y); ok {
				changes = append(changes, change{t, r.to})
			}
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].t.Before(changes[j].t) })
	offsetAt := func(t time.Time) int {
		off := v.rules[0].from
		for _, ch := range changes {
			if !ch.t.After(t) {
				off = ch.to
			}
		}
		return off
	}
	// Try each offset the zone uses; take one that agrees with itself.
	seen := map[int]bool{}
	for _, r := range v.rules {
		for _, off := range []int{r.to, r.from} {
			if seen[off] {
				continue
			}
			seen[off] = true
			t := c.in(time.UTC).Add(-time.Duration(off) * time.Second)
			if offsetAt(t) == off {
				return t.In(time.FixedZone("", off))
			}
		}
	}
	off := v.rules[0].to
	return c.in(time.UTC).Add(-time.Duration(off) * time.Second).In(time.FixedZone("", off))
}

// nthWeekday is the day of month of the nth weekday wd (−1 = last).
func nthWeekday(y int, m time.Month, nth int, wd time.Weekday) (int, bool) {
	if nth == 0 {
		return 0, false
	}
	days := time.Date(y, m+1, 0, 0, 0, 0, 0, time.UTC).Day()
	if nth > 0 {
		first := time.Date(y, m, 1, 0, 0, 0, 0, time.UTC).Weekday()
		d := 1 + (int(wd)-int(first)+7)%7 + (nth-1)*7
		return d, d <= days
	}
	last := time.Date(y, m, days, 0, 0, 0, 0, time.UTC).Weekday()
	d := days - (int(last)-int(wd)+7)%7 + (nth+1)*7
	return d, d >= 1
}

// parseOffset reads ±HHMM[SS] into seconds.
func parseOffset(s string) int {
	s = strings.TrimSpace(s)
	if len(s) < 5 {
		return 0
	}
	sign := 1
	if s[0] == '-' {
		sign = -1
	}
	h, _ := strconv.Atoi(s[1:3])
	m, _ := strconv.Atoi(s[3:5])
	sec := 0
	if len(s) >= 7 {
		sec, _ = strconv.Atoi(s[5:7])
	}
	return sign * (h*3600 + m*60 + sec)
}

// parseCivil reads YYYYMMDD[THHMMSS[Z]] (the Z is handled by the caller).
func parseCivil(s string) (civil, bool) {
	s = strings.TrimSuffix(strings.TrimSpace(s), "Z")
	if len(s) < 8 {
		return civil{}, false
	}
	num := func(a, b int) int {
		if b > len(s) {
			return -1
		}
		n, err := strconv.Atoi(s[a:b])
		if err != nil {
			return -1
		}
		return n
	}
	c := civil{y: num(0, 4), m: time.Month(num(4, 6)), d: num(6, 8)}
	if len(s) >= 15 && s[8] == 'T' {
		c.h, c.mi, c.s = num(9, 11), num(11, 13), num(13, 15)
	}
	if c.y < 0 || c.m < 1 || c.m > 12 || c.d < 1 || c.d > 31 || c.h < 0 || c.h > 23 || c.mi < 0 || c.mi > 59 || c.s < 0 || c.s > 60 {
		return civil{}, false
	}
	return c, true
}
