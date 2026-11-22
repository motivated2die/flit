package capture

// Aligner places a stream's packets on the session's time line.
//
// Each packet carries the time its first frame was captured (Windows' QPC
// position, in 100 ns units). Loopback delivers nothing while nothing
// plays, a device being reopened leaves a gap, and two devices' clocks
// drift apart; writing packets back to back would let the tracks slide
// against each other. The aligner compares where a packet belongs with how
// much was written: a gap larger than the tolerance is filled with silence,
// an overlap is trimmed off the packet, and within the tolerance packets
// are written as they come (so jitter never inserts clicks).
type Aligner struct {
	Rate      int   // the stream's sample rate
	Origin    int64 // session start, 100 ns units
	Tolerance int64 // frames of deviation accepted (default Rate/50: 20 ms)
	written   int64 // frames placed so far
}

// Place says how to write a packet of n frames captured at at (100 ns):
// first silence frames of zeros, then the packet without its first skip
// frames. It records the frames as written.
func (a *Aligner) Place(at int64, n int) (silence, skip int) {
	tol := a.Tolerance
	if tol == 0 {
		tol = int64(a.Rate / 50)
	}
	want := (at - a.Origin) * int64(a.Rate) / 10_000_000 // frame index the packet starts at
	switch d := want - a.written; {
	case d > tol:
		silence = int(d)
	case d < -tol:
		skip = int(min(int64(n), -d))
	}
	a.written += int64(silence) + int64(n-skip)
	return silence, skip
}

// PadTo returns how many frames of silence bring the stream up to time at
// (for a stream that has delivered nothing for a while, and at the end).
func (a *Aligner) PadTo(at int64) int {
	want := (at - a.Origin) * int64(a.Rate) / 10_000_000
	if want <= a.written {
		return 0
	}
	n := want - a.written
	a.written = want
	return int(n)
}

// Written is the number of frames placed.
func (a *Aligner) Written() int64 { return a.written }

// Rebase continues the time line at another sample rate (a reopened device
// with a different mix format), keeping the time already written.
func (a *Aligner) Rebase(rate int) {
	if rate == a.Rate || a.Rate == 0 {
		a.Rate = rate
		return
	}
	a.written = a.written * int64(rate) / int64(a.Rate)
	a.Rate = rate
}
