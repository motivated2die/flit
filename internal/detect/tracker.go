package detect

// EventKind is what changed.
type EventKind int

const (
	Started EventKind = iota // a provider's meeting began
	Ended                    // it ended
)

// Event is a meeting starting or ending.
type Event struct {
	Kind EventKind
	Candidate
}

// Tracker turns per-poll candidates into start and end events, with
// hysteresis: a meeting starts after StartPolls polls in a row (a quick mic
// test or a voice message doesn't prompt) and ends after EndPolls polls
// without it (a moment of the app releasing the mic doesn't end it).
type Tracker struct {
	StartPolls, EndPolls int
	state                map[string]*tracked
}

type tracked struct {
	present, absent int
	active          bool
	last            Candidate
}

// NewTracker uses 2 polls to start and 4 to end (at a 2.5 s poll: 5 s and
// 10 s).
func NewTracker() *Tracker {
	return &Tracker{StartPolls: 2, EndPolls: 4, state: map[string]*tracked{}}
}

// Update takes one poll's candidates and returns the events it causes.
func (t *Tracker) Update(cands []Candidate) []Event {
	var events []Event
	now := map[string]Candidate{}
	for _, c := range cands {
		now[c.Provider] = c
	}
	for id, c := range now {
		st := t.state[id]
		if st == nil {
			st = &tracked{}
			t.state[id] = st
		}
		st.present++
		st.absent = 0
		st.last = c
		if !st.active && st.present >= t.StartPolls {
			st.active = true
			events = append(events, Event{Kind: Started, Candidate: c})
		}
	}
	for id, st := range t.state {
		if _, ok := now[id]; ok {
			continue
		}
		st.absent++
		st.present = 0
		if st.active && st.absent >= t.EndPolls {
			st.active = false
			events = append(events, Event{Kind: Ended, Candidate: st.last})
		}
		if !st.active && st.absent >= t.EndPolls {
			delete(t.state, id)
		}
	}
	return events
}

// Active returns the meetings going on now.
func (t *Tracker) Active() []Candidate {
	var out []Candidate
	for _, st := range t.state {
		if st.active {
			out = append(out, st.last)
		}
	}
	return out
}
