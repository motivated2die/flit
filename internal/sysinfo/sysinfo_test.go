package sysinfo

import (
	"runtime"
	"testing"
	"time"
)

func TestCPUMeter(t *testing.T) {
	var m CPUMeter
	t0 := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	if p := m.Percent(time.Second, t0); p != 0 {
		t.Fatalf("first %v", p)
	}
	// One core busy for the whole second: 100% / cores.
	p := m.Percent(2*time.Second, t0.Add(time.Second))
	want := 100 / float64(runtime.NumCPU())
	if p < want*0.99 || p > want*1.01 {
		t.Fatalf("%v, want %v", p, want)
	}
}

func TestBytes(t *testing.T) {
	for n, want := range map[uint64]string{512 << 10: "512 KB", 48 << 20: "48 MB", 1203 << 20: "1.17 GB"} {
		if got := Bytes(n); got != want {
			t.Errorf("Bytes(%d) = %s, want %s", n, got, want)
		}
	}
}

func TestProcessSelf(t *testing.T) {
	st, err := Process(0)
	if err != nil {
		t.Fatal(err)
	}
	if st.WorkingSet < 1<<20 || st.Private < 1<<20 {
		t.Fatalf("%+v", st)
	}
}

func TestCoreMeter(t *testing.T) {
	m := CoreMeter{Limit: 8}
	t0 := time.Now()
	ms := time.Millisecond
	old := t0.Add(-time.Hour)
	if got := m.Cores(Threads{List: []ThreadTime{{1, 0, old}, {2, 0, old}, {3, 0, old}}}, t0); got != nil {
		t.Fatalf("first reading: %v", got)
	}
	// Over a second: thread 1 ran 900 ms, thread 2 300 ms, thread 3 10 ms
	// (waiting: not a core), thread 4 started meanwhile and ran 500 ms.
	r := Threads{List: []ThreadTime{{1, 900 * ms, old}, {2, 300 * ms, old}, {3, 10 * ms, old}, {4, 500 * ms, t0.Add(200 * ms)}},
		CPU: 1710 * ms}
	got := m.Cores(r, t0.Add(time.Second))
	if len(got) != 3 || got[0] < 0.89 || got[0] > 0.91 || got[1] < 0.49 || got[1] > 0.51 || got[2] < 0.29 || got[2] > 0.31 {
		t.Fatalf("cores %v", got)
	}
}

// TestCoreMeterThreadChurn: the engine's pool replaces its threads between
// readings (measured: 7 new threads every second or two). Eight cores
// busy on threads that came and went still show as eight cores.
func TestCoreMeterThreadChurn(t *testing.T) {
	m := CoreMeter{Limit: 8}
	t0 := time.Now()
	old := t0.Add(-time.Hour)
	m.Cores(Threads{List: []ThreadTime{{1, 0, old}}, CPU: 0}, t0)
	// A second later: the main thread used 10%, and the pool's threads,
	// all gone again, used 7.4 cores' worth.
	got := m.Cores(Threads{List: []ThreadTime{{1, 100 * time.Millisecond, old}}, CPU: 7500 * time.Millisecond}, t0.Add(time.Second))
	if len(got) != 8 {
		t.Fatalf("%d cores: %v", len(got), got)
	}
	sum := 0.0
	for _, v := range got {
		sum += v
	}
	if sum < 7.45 || sum > 7.55 {
		t.Fatalf("cores add up to %.2f, the processes used 7.5: %v", sum, got)
	}
	// More than the limit: capped.
	got = m.Cores(Threads{List: []ThreadTime{{1, 200 * time.Millisecond, old}}, CPU: 20 * time.Second}, t0.Add(2*time.Second))
	if len(got) != 8 {
		t.Fatalf("over the limit: %d cores", len(got))
	}
}
