package main

import (
	"os/exec"
	"testing"
	"time"
)

func TestWatchParentFires(t *testing.T) {
	cmd := exec.Command("/bin/sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	fired := make(chan struct{})
	WatchParent(cmd.Process.Pid, func() { close(fired) })
	cmd.Process.Kill()
	cmd.Wait()
	select {
	case <-fired:
	case <-time.After(6 * time.Second):
		t.Fatal("watchdog did not fire after parent death")
	}
}
