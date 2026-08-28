package main

import (
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func WritePidfile(path string) error {
	return os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0o644)
}

func processAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

func TakeOver(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err == nil && pid > 0 && processAlive(pid) {
		syscall.Kill(pid, syscall.SIGTERM)
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) && processAlive(pid) {
			time.Sleep(100 * time.Millisecond)
		}
	}
	return os.Remove(path)
}
