package sysinfo

import (
	"os"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

// TestThreadsOfThisProcess: a goroutine spinning on its own thread shows
// as a core about fully used; a reading costs little.
func TestThreadsOfThisProcess(t *testing.T) {
	var stop atomic.Bool
	started := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		close(started)
		for !stop.Load() {
		}
	}()
	<-started // its thread exists before the first reading (new threads count from the next)
	defer stop.Store(true)
	var m CoreMeter
	read := func() []float64 {
		t0 := time.Now()
		th, err := ReadThreads(uint32(os.Getpid()))
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%d threads read in %v", len(th.List), time.Since(t0))
		return m.Cores(th, time.Now())
	}
	read()
	time.Sleep(300 * time.Millisecond)
	cores := read()
	if len(cores) == 0 || cores[0] < 0.3 { // other tests may share the machine
		t.Fatalf("cores %v: the spinning thread isn't seen", cores)
	}
}
