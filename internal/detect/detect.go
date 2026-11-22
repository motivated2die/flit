// Package detect notices meetings: a known meeting app holding an active
// microphone session (docs/research/meeting-detection.md). The Windows side
// takes a snapshot of the system every few seconds; this file decides, from
// snapshots, which provider is in a meeting, when one starts and ends, and
// which process tree to record.
package detect

import (
	"regexp"
	"strings"
)

// Provider is a meeting app.
type Provider struct {
	ID   string
	Name string
	// Exes are process names (lower case) of the app. A capture session
	// held by one of them, or by a process below one in the process tree
	// (Teams' WebView2 helpers), counts.
	Exes []string
	// Titles, for browsers: a visible window title of the browser must
	// match one of them.
	Titles []*regexp.Regexp
	// Browser marks a provider that lives in a browser tab.
	Browser bool
	// On says whether it prompts by default (Discord is off: gaming voice
	// chat isn't a meeting for most).
	On bool
}

var browsers = []string{"chrome.exe", "msedge.exe", "firefox.exe", "brave.exe", "opera.exe", "vivaldi.exe", "arc.exe"}

// Providers is the built-in table; the user switches each on or off.
var Providers = []Provider{
	{ID: "teams", Name: "Microsoft Teams", Exes: []string{"ms-teams.exe", "teams.exe"}, On: true},
	{ID: "zoom", Name: "Zoom", Exes: []string{"zoom.exe"}, On: true},
	{ID: "webex", Name: "Webex", Exes: []string{"webex.exe", "ciscowebexstart.exe", "atmgr.exe", "webexmta.exe"}, On: true},
	{ID: "slack", Name: "Slack", Exes: []string{"slack.exe"}, On: true},
	{ID: "discord", Name: "Discord", Exes: []string{"discord.exe"}, On: false},
	{ID: "meet", Name: "Google Meet", Exes: browsers, Browser: true, On: true,
		Titles: []*regexp.Regexp{regexp.MustCompile(`(?i)\bmeet\.google\.com\b|^meet\s*[-–—:]|[-–—]\s*google meet\b|^google meet\b`)}},
	{ID: "teams-web", Name: "Microsoft Teams (browser)", Exes: browsers, Browser: true, On: true,
		Titles: []*regexp.Regexp{regexp.MustCompile(`(?i)\|\s*microsoft teams\b|teams\.microsoft\.com|^microsoft teams\b`)}},
	{ID: "zoom-web", Name: "Zoom (browser)", Exes: browsers, Browser: true, On: true,
		Titles: []*regexp.Regexp{regexp.MustCompile(`(?i)\bzoom meeting\b|app\.zoom\.us`)}},
}

// ProviderByID looks one up.
func ProviderByID(id string) (Provider, bool) {
	for _, p := range Providers {
		if p.ID == id {
			return p, true
		}
	}
	return Provider{}, false
}

// Proc is a process in a snapshot.
type Proc struct {
	PID, Parent uint32
	Exe         string // lower case
}

// Snapshot is the system at one poll.
type Snapshot struct {
	Procs map[uint32]Proc
	// Capturing are processes with an active capture (microphone) session.
	Capturing []uint32
	// MicOf is the microphone (endpoint ID) each capturing process uses.
	MicOf map[uint32]string
	// Titles are visible top-level window titles by process.
	Titles map[uint32][]string
	Self   uint32
}

// Candidate is a provider in a meeting in one snapshot.
type Candidate struct {
	Provider string
	// Target is the process tree to record (process loopback): the
	// top-most ancestor running one of the provider's exes.
	Target uint32
	Title  string // the matching window title, for browsers
	// Mic is the microphone the meeting app captures from (endpoint ID):
	// the one to record, so flit hears what the meeting hears.
	Mic string
}

// Find returns the providers in a meeting in s, one candidate per
// provider, considering only those enabled.
func Find(s Snapshot, enabled func(Provider) bool) []Candidate {
	var out []Candidate
	seen := map[string]bool{}
	for _, pid := range s.Capturing {
		if pid == s.Self || pid == 0 {
			continue
		}
		for _, p := range Providers {
			if seen[p.ID] || !enabled(p) {
				continue
			}
			target := treeRoot(s, pid, p.Exes)
			if target == 0 {
				continue
			}
			c := Candidate{Provider: p.ID, Target: target, Mic: s.MicOf[pid]}
			if p.Browser {
				c.Title = matchingTitle(s, s.Procs[target].Exe, p.Titles)
				if c.Title == "" {
					continue
				}
			}
			seen[p.ID] = true
			out = append(out, c)
			break // one provider per capturing process
		}
	}
	return out
}

// treeRoot walks from pid up the process tree and returns the top-most
// process running one of exes, or 0 if none is on the way.
func treeRoot(s Snapshot, pid uint32, exes []string) uint32 {
	var root uint32
	for depth := 0; depth < 8 && pid != 0; depth++ {
		p, ok := s.Procs[pid]
		if !ok {
			break
		}
		if contains(exes, p.Exe) {
			root = pid
		} else if root != 0 {
			break // above the app: done
		}
		if p.Parent == pid {
			break
		}
		pid = p.Parent
	}
	return root
}

// matchingTitle finds a visible window of any process running exe whose
// title matches.
func matchingTitle(s Snapshot, exe string, res []*regexp.Regexp) string {
	for pid, titles := range s.Titles {
		if s.Procs[pid].Exe != exe {
			continue
		}
		for _, t := range titles {
			for _, re := range res {
				if re.MatchString(t) {
					return t
				}
			}
		}
	}
	return ""
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// MicUser is a process using a microphone, for showing what detection
// sees.
type MicUser struct {
	PID      uint32
	Exe      string
	Mic      string // endpoint ID
	Provider string // the meeting app it belongs to, "" if none
}

// MicUsers lists the processes using a microphone in s (flit excluded),
// with the provider each belongs to, enabled or not.
func MicUsers(s Snapshot) []MicUser {
	var out []MicUser
	for _, pid := range s.Capturing {
		if pid == s.Self {
			continue
		}
		u := MicUser{PID: pid, Exe: s.Procs[pid].Exe, Mic: s.MicOf[pid]}
		for _, p := range Providers {
			if treeRoot(s, pid, p.Exes) == 0 {
				continue
			}
			if p.Browser && matchingTitle(s, s.Procs[treeRoot(s, pid, p.Exes)].Exe, p.Titles) == "" {
				continue
			}
			u.Provider = p.ID
			break
		}
		out = append(out, u)
	}
	return out
}

// Normalize lower-cases a process name.
func Normalize(exe string) string { return strings.ToLower(exe) }
