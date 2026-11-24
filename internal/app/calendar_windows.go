package app

import (
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/motivated2die/flit/internal/calendar"
	"github.com/motivated2die/flit/internal/detect"
	"github.com/motivated2die/flit/internal/logx"
	"github.com/motivated2die/flit/internal/session"
	"github.com/motivated2die/flit/internal/ui"
)

// The calendar: a subscription link's meetings of the next days, shown on
// the Calendar page and Home, and matched to recordings
// (docs/architecture/calendar.md).

const pageCalendar = "calendar"

// calendarState is the app's side of the calendar.
type calendarState struct {
	svc      *calendar.Service
	cancel   context.CancelFunc
	status   calendar.Status // the last snapshot, taken on the UI thread
	test     string          // the result of Settings' Test
	testOK   bool
	busy     bool // a test is running
	draft    string
	draftSet bool
}

func (a *App) startCalendar() {
	ctx, cancel := context.WithCancel(context.Background())
	svc := &calendar.Service{
		CacheFile: filepath.Join(a.dataDir, "calendar.json"),
		Logf:      logx.Printf, // the service logs the link's host only
		OnUpdate:  func() { a.tray.Post(a.onCalendar) },
	}
	a.cal = &calendarState{svc: svc, cancel: cancel}
	svc.Start(ctx, a.cfg.CalendarURL)
	a.cal.status = svc.Status()
	if a.tray != nil {
		go func() {
			t := time.NewTicker(5 * time.Second)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case now := <-t.C:
					a.tray.Post(func() { a.checkUpcomingMeetings(now) })
				}
			}
		}()
	}
}

func (a *App) stopCalendar() {
	if a.cal != nil {
		a.cal.cancel()
		a.cal.svc.Wait()
	}
}

// onCalendar takes a new snapshot (UI thread).
func (a *App) onCalendar() {
	if a.cal == nil {
		return
	}
	a.cal.status = a.cal.svc.Status()
	a.checkUpcomingMeetings(time.Now())
	a.invalidate()
}

// meetingReminderKey identifies one calendar occurrence for join reminders.
func meetingReminderKey(m calendar.Meeting) string {
	return m.UID + "|" + m.Start.UTC().Format(time.RFC3339)
}

// upcomingJoinMeeting returns the first timed, joinable meeting starting
// within joinPromptLead (1 minute) that hasn't already been prompted.
func upcomingJoinMeeting(meetings []calendar.Meeting, now time.Time, prompted map[string]bool) (calendar.Meeting, bool) {
	for _, m := range meetings {
		if m.AllDay || m.Link == "" {
			continue
		}
		if prompted[meetingReminderKey(m)] {
			continue
		}
		until := m.Start.Sub(now)
		if until <= joinPromptLead && until > -15*time.Second {
			return m, true
		}
	}
	return calendar.Meeting{}, false
}

// checkUpcomingMeetings opens the notch with a Join button 1 minute before a
// scheduled meeting starts (when idle and not already showing a higher-priority
// prompt).
func (a *App) checkUpcomingMeetings(now time.Time) {
	if a.cal == nil || a.rec != nil || a.detected != nil || a.joinPrompt != nil || a.stashDragging {
		return
	}
	if a.joinPrompted == nil {
		a.joinPrompted = map[string]bool{}
	}
	m, ok := upcomingJoinMeeting(a.meetings(), now, a.joinPrompted)
	if !ok {
		return
	}
	a.joinPrompted[meetingReminderKey(m)] = true
	a.showJoinPrompt(m)
}

func (a *App) meetings() []calendar.Meeting {
	if a.cal == nil {
		return nil
	}
	return a.cal.status.Meetings
}

// setCalendarURL keeps a new link and loads it.
func (a *App) setCalendarURL(u string) {
	if a.cal != nil {
		a.cal.draft, a.cal.draftSet = "", false
	}
	u = calendar.Normalize(u)
	if u == a.cfg.CalendarURL {
		a.invalidate()
		return
	}
	a.cfg.CalendarURL = u
	a.save()
	a.cal.test, a.cal.testOK = "", false
	a.cal.svc.SetURL(u)
	a.onCalendar()
}

// testCalendar loads the link once and says what it found.
func (a *App) testCalendar() {
	if a.cal.busy || a.cfg.CalendarURL == "" {
		return
	}
	a.cal.busy, a.cal.test = true, "Loading…"
	a.invalidate()
	u := a.cfg.CalendarURL
	go func() {
		res, err := a.cal.svc.Test(context.Background(), u)
		a.tray.Post(func() {
			a.cal.busy = false
			if err != nil {
				a.cal.test, a.cal.testOK = "Didn't work: "+err.Error(), false
			} else {
				name := ""
				if res.Name != "" {
					name = "“" + res.Name + "”: "
				}
				a.cal.test, a.cal.testOK = fmt.Sprintf("Works. %s%s, %s in the next 7 days.", name, plural(res.Events, "event"), plural(len(res.Upcoming), "meeting")), true
				a.cal.svc.Refresh()
			}
			a.invalidate()
		})
	}()
}

// --- matching -------------------------------------------------------------

func infoOf(m calendar.Meeting) session.MeetingInfo {
	return session.MeetingInfo{UID: m.UID, Title: m.Title, Start: m.Start, End: m.End, Recurring: m.Recurring, Location: m.Location, Link: m.Link, Provider: m.Provider}
}

// matchMeeting names a recording after its calendar meeting, or offers the
// candidates; end is zero at the start. A choice the user made stays.
func (a *App) matchMeeting(s *session.Session, end time.Time, windowTitle string) {
	if s.Meta.MeetingChosen || a.cal == nil {
		return
	}
	m := calendar.MatchRecording(a.meetings(), s.Meta.Started, end, s.Meta.Provider, windowTitle)
	switch {
	case m.Best != nil:
		info := infoOf(*m.Best)
		if err := s.SetMeeting(&info, nil, false); err != nil {
			logx.Printf("calendar match for %s: %v", s.Meta.ID, err)
		}
		logx.Printf("%s matched a calendar meeting", s.Meta.ID)
	case len(m.Choices) > 0:
		var cs []session.MeetingInfo
		for _, c := range m.Choices {
			cs = append(cs, infoOf(c))
		}
		if err := s.SetMeeting(nil, cs, false); err != nil {
			logx.Printf("calendar choices for %s: %v", s.Meta.ID, err)
		}
		logx.Printf("%s could be %d calendar meetings", s.Meta.ID, len(cs))
	}
}

// chooseMeeting is the user's answer to "which meeting was this?".
func (a *App) chooseMeeting(s *session.Session, m *session.MeetingInfo) {
	if err := s.SetMeeting(m, nil, true); err != nil {
		logx.Printf("choosing the meeting of %s: %v", s.Meta.ID, err)
	}
	a.forget(s)
	a.invalidate()
}

// detectedName is what the notch calls a detected meeting: its calendar
// title when the calendar is sure.
func (a *App) detectedName(provider, windowTitle string) (string, bool) {
	m := calendar.MatchRecording(a.meetings(), time.Now(), time.Time{}, provider, windowTitle)
	if m.Best != nil {
		return m.Best.Title, true
	}
	return "", false
}

// --- pages ----------------------------------------------------------------

// meetingRow shows a calendar meeting; recorded ones link to their
// recording, others with a link offer Join.
func (a *App) meetingRow(m calendar.Meeting, id string, now time.Time) ui.Node {
	row := ui.MeetingRow{ID: id, Title: m.Title, Place: placeText(m), Past: !m.End.After(now),
		Now: !m.Start.After(now) && m.End.After(now) && !m.AllDay, Recurring: m.Recurring}
	if m.AllDay {
		row.Time = "All day"
	} else {
		row.Time, row.Until = m.Start.Local().Format("15:04"), m.End.Local().Format("15:04")
	}
	if s := a.recordingOf(m); s != nil {
		row.Actions = []ui.Node{ui.Button{ID: id + ":rec", Label: "Recording", Glyph: ui.GlyphArticle, OnClick: func() { a.go_(pageSession + s.Meta.ID) }}}
	} else if m.Link != "" && !row.Past {
		link := m.Link
		row.Actions = []ui.Node{ui.Button{ID: id + ":join", Label: "Join", Glyph: ui.GlyphOpenLink, OnClick: func() { openURL(link) }}}
	}
	if !m.AllDay && a.rec == nil && (!row.Past || m.Start.After(now.Add(-12*time.Hour))) {
		mm := m
		row.HoverActions = []ui.Node{ui.Button{ID: id + ":record", Label: "Record", Glyph: ui.GlyphRecord, OnClick: func() { a.recordMeeting(mm) }}}
	}
	return row
}

// recordMeeting starts recording now for a calendar meeting, whenever it
// is scheduled, and names the recording after it. The meeting app's own
// audio is recorded when it is the one detected; else everything the PC
// plays.
func (a *App) recordMeeting(m calendar.Meeting) {
	if a.rec != nil {
		return
	}
	var c *detect.Candidate
	if d := a.detected; d != nil && m.Provider != "" && strings.TrimSuffix(d.Provider, "-web") == m.Provider {
		cc := *d
		c = &cc
	}
	a.startRecording(c)
	if a.rec == nil {
		return // failed; startRecording said why
	}
	info := infoOf(m)
	if err := a.rec.sess.SetMeeting(&info, nil, true); err != nil {
		logx.Printf("recording a calendar meeting: %v", err)
	}
	logx.Printf("recording started from the calendar")
	a.reloadSessions()
	a.go_(pageHome)
}

// placeText is a meeting's place in words, or its app when only a link.
func placeText(m calendar.Meeting) string {
	if m.Location != "" {
		return m.Location
	}
	switch m.Provider {
	case "zoom":
		return "Zoom"
	case "teams":
		return "Microsoft Teams"
	case "meet":
		return "Google Meet"
	case "webex":
		return "Webex"
	}
	if m.Link != "" {
		if u, err := url.Parse(m.Link); err == nil {
			return u.Host
		}
	}
	return ""
}

// recordingOf finds the recording made of a meeting occurrence.
func (a *App) recordingOf(m calendar.Meeting) *session.Session {
	for _, s := range a.sessions {
		if mi := s.Meta.Meeting; mi != nil && mi.UID == m.UID && mi.Start.Equal(m.Start) {
			return s
		}
	}
	return nil
}

// dayName is "Today", "Tomorrow" or the date.
func dayName(d, today time.Time) string {
	switch int(d.Sub(today).Hours()/24 + 0.5) {
	case 0:
		return "Today"
	case 1:
		return "Tomorrow"
	}
	return d.Format("Monday, 2 January")
}

func (a *App) calendarPage() ui.Node {
	items := []ui.Node{ui.Title("Calendar")}
	if a.cal == nil || !a.cal.status.Configured {
		return ui.VStack{Gap: 12, Items: append(items,
			ui.Para{Text: "Add your calendar's subscription link (an ICS link) in Settings, and flit shows your meetings of the next seven days here, names recordings after them, and lets you pick the right one when meetings overlap. No sign-in; the link stays on this PC.", Muted: true},
			ui.HStack{Items: []ui.Node{ui.Button{ID: "cal:settings", Label: "Add a calendar", Glyph: ui.GlyphSettings, Primary: true, OnClick: func() { a.go_(pageSettings) }}}},
		)}
	}
	st := a.cal.status
	line := "Not loaded yet"
	switch {
	case st.Refreshing:
		line = "Loading…"
	case !st.Updated.IsZero():
		line = "Updated " + st.Updated.Local().Format("15:04")
		if st.Name != "" {
			line = st.Name + " · " + line
		}
		line += "; refreshes every half hour"
	}
	items = append(items, ui.Para{Text: line, Muted: true},
		ui.HStack{Gap: 8, Items: []ui.Node{
			ui.Button{ID: "cal:refresh", Label: "Refresh", Glyph: ui.GlyphRefresh, OnClick: a.cal.svc.Refresh},
			ui.Button{ID: "cal:settings", Label: "Settings", Glyph: ui.GlyphSettings, OnClick: func() { a.go_(pageSettings) }},
		}})
	if st.Warn {
		items = append(items, ui.Card{Gap: 6, Items: []ui.Node{
			ui.Label{Text: "The calendar couldn't be loaded since " + st.FailSince.Local().Format("Mon 2 Jan, 15:04"), Strong: true},
			ui.Para{Text: st.Err + ". The meetings below are from the last time it worked.", Muted: true},
		}})
	}
	now := time.Now()
	y, mo, d := now.Date()
	today := time.Date(y, mo, d, 0, 0, 0, 0, time.Local)
	var day time.Time
	var rows []ui.Node
	flush := func() {
		if len(rows) > 0 {
			items = append(items, ui.Space(4), ui.Caption(dayName(day, today)), ui.Card{Gap: 0, Items: rows})
			rows = nil
		}
	}
	shown := 0
	for i, m := range st.Meetings {
		start := m.Start.Local()
		if m.End.Before(today) && !m.AllDay || start.Before(today.AddDate(0, 0, -1)) {
			continue // yesterday's: kept for matching, not shown
		}
		if m.AllDay && !m.Start.Local().After(today.Add(-time.Second)) && m.End.Before(today.Add(time.Second)) {
			continue
		}
		md := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.Local)
		if md.Before(today) {
			md = today // began before today, still going
		}
		if !md.Equal(day) {
			flush()
			day = md
		} else if len(rows) > 0 {
			rows = append(rows, ui.Divider{})
		}
		rows = append(rows, a.meetingRow(m, fmt.Sprintf("cal:%d", i), now))
		shown++
	}
	flush()
	if shown == 0 && !st.Updated.IsZero() {
		items = append(items, ui.Card{Items: []ui.Node{ui.Para{Text: "No meetings in the next seven days.", Muted: true}}})
	}
	return ui.VStack{Gap: 10, Items: items}
}

// upcomingSection is Home's next two meetings (nil without a calendar).
func (a *App) upcomingSection() []ui.Node {
	if a.cal == nil || !a.cal.status.Configured {
		return nil
	}
	now := time.Now()
	var rows []ui.Node
	for i, m := range a.nextMeetings(2, now) {
		if i > 0 {
			rows = append(rows, ui.Divider{})
		}
		row := a.meetingRow(m, fmt.Sprintf("home:cal:%d", i), now).(ui.MeetingRow)
		row.Place = withDay(m, row.Place, now)
		rows = append(rows, row)
	}
	items := []ui.Node{ui.Space(8), ui.CaptionRow{Text: "Upcoming meetings",
		Right: ui.Link{ID: "home:calendar", Glyph: ui.GlyphCalendar, Label: "Calendar", OnClick: func() { a.go_(pageCalendar) }}}}
	if a.cal.status.Warn {
		items = append(items, ui.Para{Text: "The calendar couldn't be loaded for a day; these may be out of date.", Muted: true, Small: true})
	}
	if len(rows) == 0 {
		return append(items, ui.Card{Items: []ui.Node{ui.Para{Text: "No more meetings in the next seven days.", Muted: true}}})
	}
	return append(items, ui.Card{Gap: 0, Items: rows})
}

// nextMeetings are the next n timed meetings not over yet.
func (a *App) nextMeetings(n int, now time.Time) []calendar.Meeting {
	if a.cal == nil {
		return nil
	}
	var out []calendar.Meeting
	for _, m := range a.cal.status.Meetings {
		if m.AllDay || !m.End.After(now) {
			continue
		}
		out = append(out, m)
		if len(out) == n {
			break
		}
	}
	return out
}

// withDay puts the day before a place unless the meeting is today
// ("Tomorrow · Zoom").
func withDay(m calendar.Meeting, place string, now time.Time) string {
	s := m.Start.Local()
	day := dayName(time.Date(s.Year(), s.Month(), s.Day(), 0, 0, 0, 0, time.Local), startOfDay(now))
	if day == "Today" {
		return place
	}
	return strings.TrimSuffix(day+" · "+place, " · ")
}

// panelMeetings is the tray panel's next two meetings, each with small
// icon buttons to join and to record it (nil without any).
func (a *App) panelMeetings() []ui.Node {
	now := time.Now()
	next := a.nextMeetings(2, now)
	if len(next) == 0 {
		return nil
	}
	items := []ui.Node{ui.Divider{}, ui.Caption("Upcoming meetings")}
	for i, m := range next {
		m := m
		id := fmt.Sprintf("panel:cal:%d", i)
		row := ui.MeetingRow{ID: id, NoIcon: true, Title: m.Title, Place: withDay(m, placeText(m), now),
			Time: m.Start.Local().Format("15:04"), Until: m.End.Local().Format("15:04"),
			Now: !m.Start.After(now) && m.End.After(now), OnClick: a.act(func() { a.openPage(pageCalendar) })}
		if a.rec == nil {
			row.Actions = append(row.Actions, ui.RoundButton{ID: id + ":record", Glyph: ui.GlyphRecord, H: 32, OnClick: a.act(func() { a.recordMeeting(m) })})
		}
		if m.Link != "" {
			link := m.Link
			row.Actions = append(row.Actions, ui.RoundButton{ID: id + ":join", Glyph: ui.GlyphOpenLink, H: 32, OnClick: a.act(func() { openURL(link) })})
		}
		items = append(items, row)
	}
	return items
}

func startOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.Local)
}

// calendarSettings is Settings' calendar card.
func (a *App) calendarSettings() []ui.Node {
	if a.cal == nil {
		return nil
	}
	var actions []ui.Node
	if a.cfg.CalendarURL != "" {
		label := "Test"
		if a.cal.busy {
			label = "Testing…"
		}
		actions = append(actions, ui.Button{ID: "set:caltest", Label: label, Glyph: ui.GlyphRefresh, Disabled: a.cal.busy, OnClick: a.testCalendar},
			ui.Button{ID: "set:calremove", Label: "Remove", OnClick: func() { a.setCalendarURL("") }})
	}
	result := a.cal.test
	if result == "" && a.cfg.CalendarURL != "" {
		if st := a.cal.status; !st.Updated.IsZero() {
			result = fmt.Sprintf("Last loaded %s: %s in the next 7 days.", st.Updated.Local().Format("Mon 15:04"), plural(len(st.Meetings), "meeting"))
		}
	}
	urlVal := a.cfg.CalendarURL
	if a.cal.draftSet {
		urlVal = a.cal.draft
	}
	dirty := a.cal.draftSet && calendar.Normalize(a.cal.draft) != a.cfg.CalendarURL
	card := []ui.Node{
		ui.Label{Text: "Calendar subscription link"},
		ui.Para{Text: "An ICS link: Outlook “Publish calendar” (ICS), Google “Secret address in iCal format”, or any webcal:// link. It opens your calendar, so it stays on this PC and is never written to the log.", Muted: true, Small: true},
		ui.HStack{Gap: 8, Items: []ui.Node{
			ui.VStack{Items: []ui.Node{ui.TextField{ID: "set:calurl", Value: urlVal, Placeholder: "https://…/calendar.ics", MaxLen: 2048,
				OnChange: func(t string) { a.cal.draft, a.cal.draftSet = t, true },
				OnCommit: a.setCalendarURL}}},
			ui.Button{ID: "set:calapply", Glyph: ui.GlyphCheck, Primary: dirty, OnClick: func() {
				u := a.cfg.CalendarURL
				if a.cal.draftSet {
					u = a.cal.draft
				}
				a.setCalendarURL(u)
			}},
		}},
	}
	if len(actions) > 0 || result != "" {
		row := append([]ui.Node{}, actions...)
		card = append(card, ui.HStack{Gap: 8, Items: row})
		if result != "" {
			card = append(card, ui.Para{Text: result, Muted: !a.cal.testOK, Small: true})
		}
	}
	return []ui.Node{ui.Caption("Calendar"), ui.Card{Gap: 8, Items: card}}
}

// meetingCard asks which meeting a recording belongs to when calendar events
// overlapped. Once a recording is linked to a meeting, its details live in
// meetingBadge beside the page title instead of duplicating the title here.
func (a *App) meetingCard(s *session.Session) ui.Node {
	if len(s.Meta.MeetingChoices) > 0 && !s.Meta.MeetingChosen {
		rows := []ui.Node{ui.Label{Text: "Which meeting was this?", Strong: true},
			ui.Para{Text: "Your calendar has more than one meeting at this time.", Muted: true, Small: true}}
		for i, c := range s.Meta.MeetingChoices {
			c := c
			rows = append(rows, ui.ChoiceRow{ID: fmt.Sprintf("session:choice:%d", i), Title: c.Title,
				Detail: c.Start.Local().Format("15:04") + "–" + c.End.Local().Format("15:04") + placeSuffix(c), Level: -1,
				OnClick: func() { a.chooseMeeting(s, &c) }})
		}
		rows = append(rows, ui.ChoiceRow{ID: "session:choice:none", Title: "None of these", Level: -1, OnClick: func() { a.chooseMeeting(s, nil) }})
		return ui.Card{Gap: 2, Items: rows}
	}
	return nil
}

// meetingBadge is the calendar icon to the left of a recording's title, with a
// hover popover showing the linked calendar entry and (for recurring series)
// links to up to three previous recorded meetings in the same series.
func (a *App) meetingBadge(s *session.Session) ui.Node {
	if s == nil || s.Meta.Meeting == nil {
		return nil
	}
	m := s.Meta.Meeting
	prev := previousInSeries(a.sessions, s, 3)
	recurring := m.Recurring || hasOtherInSeries(a.sessions, s)
	glyph := ui.GlyphMeeting
	header := "From your calendar"
	if recurring {
		glyph = ui.GlyphCalendarRepeat
		header = "Recurring meeting"
	}
	when := m.Start.Local().Format("Mon 2 Jan, 15:04") + "–" + m.End.Local().Format("15:04")
	place := placeText(calendar.Meeting{Location: m.Location, Link: m.Link, Provider: m.Provider})
	var links []ui.PopoverLink
	curYear := s.Meta.Started.Local().Year()
	for _, p := range prev {
		pid := p.Meta.ID
		name := p.Meta.Title
		if name == "" {
			name = defaultTitle(p)
		}
		dateFmt := "2 Jan"
		if p.Meta.Started.Local().Year() != curYear {
			dateFmt = "2 Jan 2006"
		}
		links = append(links, ui.PopoverLink{
			ID:      pid,
			Title:   name,
			Detail:  p.Meta.Started.Local().Format(dateFmt),
			OnClick: func() { a.go_(pageSession + pid) },
		})
	}
	return ui.CalendarBadge{
		ID:        "session:cal",
		Glyph:     glyph,
		Header:    header,
		Title:     m.Title,
		When:      when,
		Place:     place,
		Action:    "Not this meeting",
		OnAction:  func() { a.chooseMeeting(s, nil) },
		PrevTitle: "Previous meetings",
		Prev:      links,
	}
}

// previousInSeries returns up to limit earlier recordings sharing s's calendar
// series UID, newest first. Matching by UID survives renaming or rescheduling
// individual occurrences.
func previousInSeries(sessions []*session.Session, s *session.Session, limit int) []*session.Session {
	if s == nil || s.Meta.Meeting == nil || s.Meta.Meeting.UID == "" || limit <= 0 {
		return nil
	}
	uid := s.Meta.Meeting.UID
	var out []*session.Session
	for _, other := range sessions {
		if other == nil || other.Meta.ID == s.Meta.ID {
			continue
		}
		if other.Meta.Meeting == nil || other.Meta.Meeting.UID != uid {
			continue
		}
		if !other.Meta.Started.Before(s.Meta.Started) {
			continue
		}
		out = append(out, other)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Meta.Started.After(out[j].Meta.Started) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// hasOtherInSeries reports whether any other recording shares s's calendar UID
// (handles recurring recordings saved before MeetingInfo.Recurring existed).
func hasOtherInSeries(sessions []*session.Session, s *session.Session) bool {
	if s == nil || s.Meta.Meeting == nil || s.Meta.Meeting.UID == "" {
		return false
	}
	uid := s.Meta.Meeting.UID
	for _, other := range sessions {
		if other != nil && other.Meta.ID != s.Meta.ID && other.Meta.Meeting != nil && other.Meta.Meeting.UID == uid {
			return true
		}
	}
	return false
}

func placeSuffix(m session.MeetingInfo) string {
	p := placeText(calendar.Meeting{Location: m.Location, Link: m.Link, Provider: m.Provider})
	if p == "" {
		return ""
	}
	return " · " + p
}

// openURL opens a web link in the default browser (http and https only).
func openURL(u string) {
	p, err := url.Parse(u)
	if err != nil || (p.Scheme != "https" && p.Scheme != "http") {
		return
	}
	_ = exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", p.String()).Start()
}
