package llm

import (
	"os/exec"
	"testing"
	"time"
)

// Closing the job ends the process in it: what happens to the server when
// flit quits or crashes.
func TestJobKillsOnClose(t *testing.T) {
	cmd := exec.Command("ping", "-n", "30", "127.0.0.1")
	if err := cmd.Start(); err != nil {
		t.Skip("no ping:", err)
	}
	done := make(chan struct{})
	go func() { cmd.Wait(); close(done) }()
	release, err := jobProcess(cmd.Process.Pid, 50, true)
	if err != nil {
		cmd.Process.Kill()
		t.Fatal(err)
	}
	select {
	case <-done:
		t.Fatal("ended before the job closed")
	case <-time.After(300 * time.Millisecond):
	}
	release()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		cmd.Process.Kill()
		t.Fatal("the process outlived its job")
	}
}
