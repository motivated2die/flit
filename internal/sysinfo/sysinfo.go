// Package sysinfo measures flit's own resource use for the debug view:
// memory and CPU of the app and of its engine worker.
package sysinfo

import (
	"fmt"
	"runtime"
	"sort"
	"time"
)

// Stats are a process's figures at one moment.
type Stats struct {
	WorkingSet uint64        // bytes in RAM
	Private    uint64        // bytes committed by the process alone
	CPU        time.Duration // user + kernel time so far
}

// CPUMeter turns successive CPU times into a percentage of the whole
// machine (100% = every core busy).
type CPUMeter struct {
	last time.Duration
	at   time.Time
}

// Percent returns the CPU use since the previous call (0 the first time).
func (m *CPUMeter) Percent(cpu time.Duration, now time.Time) float64 {
	defer func() { m.last, m.at = cpu, now }()
	if m.at.IsZero() || !now.After(m.at) || cpu < m.last {
		return 0
	}
	return 100 * float64(cpu-m.last) / float64(now.Sub(m.at)) / float64(runtime.NumCPU())
}

// Bytes formats a byte count for people: 48 MB, 1.12 GB.
func Bytes(n uint64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.2f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.0f MB", float64(n)/(1<<20))
	default:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
}

// ThreadTime is one thread's CPU time so far, and when it started.
type ThreadTime struct {
	ID      uint32
	CPU     time.Duration
	Started time.Time
}

// Threads is one reading of some processes: their threads, and the CPU
// time of the processes as a whole (which includes threads that have
// already ended).
type Threads struct {
	List []ThreadTime
	CPU  time.Duration
}

// CoreMeter turns successive readings into how busy the cores the
// processes engage are. The engine's thread pool replaces its threads
// every second or two (measured: 188 threads over 22 s, 8 busy at a
// time), so it can't follow threads alone: a thread new since the last
// reading counts with all its CPU time, and the CPU the visible threads
// don't account for (threads that came and went between readings) tops
// up the squares, then adds more.
type CoreMeter struct {
	last  map[uint32]time.Duration
	cpu   time.Duration
	at    time.Time
	Limit int // at most this many cores (0: runtime.NumCPU())
}

// coreBusy is the share of a core a thread must use to count as engaging
// one (threads waiting on a pipe use next to nothing).
const coreBusy = 0.05

// Cores returns the share of a core (0..1) of each engaged core since the
// previous reading, busiest first (nothing the first time).
func (m *CoreMeter) Cores(r Threads, now time.Time) []float64 {
	prev, prevCPU, at := m.last, m.cpu, m.at
	m.last, m.cpu, m.at = make(map[uint32]time.Duration, len(r.List)), r.CPU, now
	for _, t := range r.List {
		m.last[t.ID] = t.CPU
	}
	if at.IsZero() || !now.After(at) {
		return nil
	}
	wall := float64(now.Sub(at))
	var out []float64
	seen := 0.0
	for _, t := range r.List {
		var d time.Duration
		if p, ok := prev[t.ID]; ok && t.CPU >= p {
			d = t.CPU - p
		} else if !ok && !t.Started.Before(at) {
			d = t.CPU // started since the last reading: all of it is new
		} else {
			continue // not seen before though older (or a reused ID): unknown
		}
		u := float64(d) / wall
		seen += u
		if u >= coreBusy {
			out = append(out, min(1, u))
		}
	}
	limit := m.Limit
	if limit <= 0 {
		limit = runtime.NumCPU()
	}
	// What the visible threads don't explain ran on threads that ended
	// meanwhile: fill the squares up with it, then add more.
	if r.CPU > prevCPU {
		rest := float64(r.CPU-prevCPU)/wall - seen
		sort.Sort(sort.Reverse(sort.Float64Slice(out)))
		for i := range out {
			if rest < coreBusy {
				break
			}
			add := min(1-out[i], rest)
			out[i] += add
			rest -= add
		}
		for rest >= coreBusy && len(out) < limit {
			add := min(1, rest)
			out = append(out, add)
			rest -= add
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	sort.Sort(sort.Reverse(sort.Float64Slice(out)))
	return out
}
