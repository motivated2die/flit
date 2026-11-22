package calendar

import (
	"sort"
	"strconv"
	"strings"
	"time"
)

// rule is an RRULE: how an event repeats.
type rule struct {
	freq       string // DAILY, WEEKLY, MONTHLY, YEARLY ("" = unsupported)
	interval   int
	count      int
	untilRaw   untilValue
	until      time.Time
	byDay      []dayNum
	byMonthDay []int
	byMonth    []int
	bySetPos   []int
	wkst       time.Weekday
}

type dayNum struct {
	n  int // 0 = every such weekday; 2 = the second; −1 = the last
	wd time.Weekday
}

// untilValue keeps UNTIL until the event's zone is known.
type untilValue struct {
	c   civil
	utc bool
	set bool
	day bool
}

func (u untilValue) isZero() bool { return !u.set }

func (u untilValue) instant(z zone) time.Time {
	switch {
	case u.utc:
		return u.c.in(time.UTC)
	case u.day: // the whole day counts
		return z.at(u.c.addDays(1)).Add(-time.Second)
	}
	return z.at(u.c)
}

var weekdays = map[string]time.Weekday{"SU": time.Sunday, "MO": time.Monday, "TU": time.Tuesday, "WE": time.Wednesday, "TH": time.Thursday, "FR": time.Friday, "SA": time.Saturday}

func parseRule(s string) rule {
	r := rule{interval: 1, wkst: time.Monday}
	ints := func(v string) []int {
		var out []int
		for _, f := range strings.Split(v, ",") {
			if n, err := strconv.Atoi(strings.TrimSpace(f)); err == nil {
				out = append(out, n)
			}
		}
		return out
	}
	for _, part := range strings.Split(s, ";") {
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch strings.ToUpper(strings.TrimSpace(k)) {
		case "FREQ":
			switch f := strings.ToUpper(v); f {
			case "DAILY", "WEEKLY", "MONTHLY", "YEARLY":
				r.freq = f
			}
		case "INTERVAL":
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				r.interval = n
			}
		case "COUNT":
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				r.count = n
			}
		case "UNTIL":
			if c, ok := parseCivil(v); ok {
				r.untilRaw = untilValue{c: c, utc: strings.HasSuffix(v, "Z"), set: true, day: len(strings.TrimSuffix(v, "Z")) == 8}
			}
		case "BYDAY":
			for _, f := range strings.Split(v, ",") {
				f = strings.ToUpper(strings.TrimSpace(f))
				if len(f) < 2 {
					continue
				}
				wd, ok := weekdays[f[len(f)-2:]]
				if !ok {
					continue
				}
				n := 0
				if num := f[:len(f)-2]; num != "" {
					n, _ = strconv.Atoi(num)
				}
				r.byDay = append(r.byDay, dayNum{n, wd})
			}
		case "BYMONTHDAY":
			r.byMonthDay = ints(v)
		case "BYMONTH":
			r.byMonth = ints(v)
		case "BYSETPOS":
			r.bySetPos = ints(v)
		case "WKST":
			if wd, ok := weekdays[strings.ToUpper(v)]; ok {
				r.wkst = wd
			}
		}
	}
	return r
}

// expand calls emit with each occurrence's start (an instant) from the
// series' start until `to`, UNTIL or COUNT, in time order. Occurrences
// before `from` are counted (for COUNT) but not emitted.
func (r *rule) expand(ev *event, from, to time.Time, emit func(time.Time)) {
	start := ev.start
	n := 0
	done := false
	visit := func(c civil) {
		if done {
			return
		}
		t := ev.zone.at(c)
		if t.Before(ev.startT) {
			return
		}
		if !r.until.IsZero() && t.After(r.until) || !t.Before(to) {
			done = true
			return
		}
		n++
		if r.count > 0 && n > r.count {
			done = true
			return
		}
		if !t.Before(from) {
			emit(t)
		}
	}
	at := func(y int, m time.Month, d int) civil {
		return civil{y, m, d, start.h, start.mi, start.s}
	}
	for k := 0; k < maxPeriods && !done; k++ {
		var days []civil
		switch r.freq {
		case "DAILY":
			c := start.addDays(k * r.interval)
			if r.keepDay(c) {
				days = []civil{c}
			}
		case "WEEKLY":
			base := start.addDays(-((int(start.weekday()) - int(r.wkst) + 7) % 7)).addDays(7 * k * r.interval)
			wds := r.byDay
			if len(wds) == 0 {
				wds = []dayNum{{0, start.weekday()}}
			}
			for _, d := range wds {
				c := base.addDays((int(d.wd) - int(r.wkst) + 7) % 7)
				if r.keepMonth(c.m) {
					days = append(days, c)
				}
			}
		case "MONTHLY":
			t := time.Date(start.y, start.m+time.Month(k*r.interval), 1, 0, 0, 0, 0, time.UTC)
			if r.keepMonth(t.Month()) {
				days = r.daysInMonth(t.Year(), t.Month(), start.d, at)
			}
		case "YEARLY":
			y := start.y + k*r.interval
			months := r.byMonth
			if len(months) == 0 {
				months = []int{int(start.m)}
			}
			for _, m := range months {
				if m < 1 || m > 12 {
					continue
				}
				if len(r.byDay) == 0 && len(r.byMonthDay) == 0 {
					if start.d <= daysIn(y, time.Month(m)) {
						days = append(days, at(y, time.Month(m), start.d))
					}
					continue
				}
				days = append(days, r.daysInMonth(y, time.Month(m), start.d, at)...)
			}
		default:
			return
		}
		sort.Slice(days, func(i, j int) bool { return days[i].in(time.UTC).Before(days[j].in(time.UTC)) })
		days = r.setPos(days)
		for _, c := range days {
			visit(c)
		}
		// Past `to` already: the next periods are later still.
		if len(days) == 0 && k > 0 {
			probe := start
			switch r.freq {
			case "DAILY":
				probe = start.addDays(k * r.interval)
			case "WEEKLY":
				probe = start.addDays(7 * k * r.interval)
			case "MONTHLY":
				probe.m += time.Month(k * r.interval)
			case "YEARLY":
				probe.y += k * r.interval
			}
			if !probe.in(time.UTC).Before(to.UTC().AddDate(0, 0, 2)) {
				return
			}
		}
	}
}

// daysInMonth is the days of one month a MONTHLY (or YEARLY with BY…)
// rule picks; without BYDAY/BYMONTHDAY, the series' own day.
func (r *rule) daysInMonth(y int, m time.Month, startDay int, at func(int, time.Month, int) civil) []civil {
	n := daysIn(y, m)
	var out []civil
	switch {
	case len(r.byMonthDay) > 0:
		for _, d := range r.byMonthDay {
			if d < 0 {
				d = n + 1 + d
			}
			if d >= 1 && d <= n && r.keepWeekday(y, m, d) {
				out = append(out, at(y, m, d))
			}
		}
	case len(r.byDay) > 0:
		for _, bd := range r.byDay {
			if bd.n != 0 {
				if d, ok := nthWeekday(y, m, bd.n, bd.wd); ok {
					out = append(out, at(y, m, d))
				}
				continue
			}
			for d := 1; d <= n; d++ {
				if time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Weekday() == bd.wd {
					out = append(out, at(y, m, d))
				}
			}
		}
	default:
		if startDay <= n {
			out = append(out, at(y, m, startDay))
		}
	}
	return out
}

func (r *rule) keepMonth(m time.Month) bool {
	if len(r.byMonth) == 0 {
		return true
	}
	for _, x := range r.byMonth {
		if time.Month(x) == m {
			return true
		}
	}
	return false
}

// keepDay filters a DAILY candidate by BYMONTH, BYMONTHDAY and BYDAY.
func (r *rule) keepDay(c civil) bool {
	if !r.keepMonth(c.m) {
		return false
	}
	if len(r.byMonthDay) > 0 {
		n := daysIn(c.y, c.m)
		ok := false
		for _, d := range r.byMonthDay {
			if d == c.d || (d < 0 && n+1+d == c.d) {
				ok = true
			}
		}
		if !ok {
			return false
		}
	}
	return r.keepWeekday(c.y, c.m, c.d)
}

func (r *rule) keepWeekday(y int, m time.Month, d int) bool {
	if len(r.byDay) == 0 {
		return true
	}
	wd := time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Weekday()
	for _, bd := range r.byDay {
		if bd.wd == wd {
			return true
		}
	}
	return false
}

// setPos keeps the BYSETPOS positions of one period's days.
func (r *rule) setPos(days []civil) []civil {
	if len(r.bySetPos) == 0 || len(days) == 0 {
		return days
	}
	var out []civil
	for _, p := range r.bySetPos {
		i := p - 1
		if p < 0 {
			i = len(days) + p
		}
		if i >= 0 && i < len(days) {
			out = append(out, days[i])
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].in(time.UTC).Before(out[j].in(time.UTC)) })
	return out
}

func daysIn(y int, m time.Month) int { return time.Date(y, m+1, 0, 0, 0, 0, 0, time.UTC).Day() }
