package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Builds the helper binary once for integration tests.
func buildHelper(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "airplay-helper")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

func TestServeEndToEndWithStub(t *testing.T) {
	bin := buildHelper(t)
	out := t.TempDir()
	// Stub ffmpeg: writes a playlist like the real one would, then idles like a
	// long transcode so `serve` stays up.
	stub := filepath.Join(t.TempDir(), "ffmpeg")
	os.WriteFile(stub, []byte(fmt.Sprintf(
		"#!/bin/sh\necho '#EXTM3U' > %s/index.m3u8\necho out_time_us=1000000\ntrap 'exit 0' TERM\nsleep 60\n",
		out)), 0o755)

	cmd := exec.Command(bin, "serve",
		"-source", "/dev/null", "-out", out, "-ffmpeg", stub,
		"-parent", fmt.Sprint(os.Getpid()), "-duration", "60",
		"-vcodec", "h264", "-acodec", "aac", "-achannels", "2", "-vmap", "0", "-amap", "1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()

	var readyURL string
	sc := bufio.NewScanner(stdout)
	deadline := time.After(15 * time.Second)
	for readyURL == "" {
		lineCh := make(chan string, 1)
		go func() {
			if sc.Scan() {
				lineCh <- sc.Text()
			} else {
				lineCh <- ""
			}
		}()
		select {
		case line := <-lineCh:
			if line == "" {
				t.Fatal("helper stdout closed before ready")
			}
			var ev map[string]any
			if err := json.Unmarshal([]byte(line), &ev); err != nil {
				t.Fatalf("bad line %q", line)
			}
			if ev["event"] == "ready" {
				readyURL, _ = ev["url"].(string)
			}
			if ev["event"] == "error" {
				t.Fatalf("helper errored: %v", ev)
			}
		case <-deadline:
			t.Fatal("no ready event within 15s")
		}
	}
	if !strings.HasSuffix(readyURL, "/index.m3u8") || strings.Contains(readyURL, "127.0.0.1") {
		t.Fatalf("bad ready url %q", readyURL)
	}
	resp, err := http.Get(readyURL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("playlist fetch: %d", resp.StatusCode)
	}
	// pidfile exists while serving
	if _, err := os.Stat(filepath.Join(out, "helper.pid")); err != nil {
		t.Fatal("pidfile missing while serving")
	}

	// `stop` kills the serving instance
	if err := exec.Command(bin, "stop", "-out", out).Run(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("serve instance survived stop")
	}
}
