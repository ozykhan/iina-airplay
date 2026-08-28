package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestTakeOverNoFile(t *testing.T) {
	if err := TakeOver(filepath.Join(t.TempDir(), "helper.pid")); err != nil {
		t.Fatal(err)
	}
}

func TestTakeOverStalePid(t *testing.T) {
	p := filepath.Join(t.TempDir(), "helper.pid")
	os.WriteFile(p, []byte("999999"), 0o644) // beyond macOS pid range
	if err := TakeOver(p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("stale pidfile not removed")
	}
}

func TestTakeOverLiveProcess(t *testing.T) {
	cmd := exec.Command("/bin/sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	p := filepath.Join(t.TempDir(), "helper.pid")
	os.WriteFile(p, []byte(strconv.Itoa(cmd.Process.Pid)), 0o644)
	if err := TakeOver(p); err != nil {
		t.Fatal(err)
	}
	// SIGTERM should have landed
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(4 * time.Second):
		t.Fatal("predecessor still alive after TakeOver")
	}
}
