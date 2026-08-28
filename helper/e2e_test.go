package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func findFFmpeg(t *testing.T) string {
	t.Helper()
	for _, p := range []string{"/opt/homebrew/bin/ffmpeg", "/usr/local/bin/ffmpeg"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	t.Skip("no local ffmpeg; skipping real-media e2e")
	return ""
}

// Synthesizes a small H.264 + stereo FLAC MKV — exercises video copy plus the
// lossless-audio re-encode branch.
func makeFixture(t *testing.T, ffmpeg string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "fixture.mkv")
	cmd := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=duration=8:size=640x360:rate=25",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=8",
		"-c:v", "libx264", "-preset", "ultrafast", "-c:a", "flac", p)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v\n%s", err, out)
	}
	return p
}

func TestRealFFmpegEndToEnd(t *testing.T) {
	ffmpeg := findFFmpeg(t)
	fixture := makeFixture(t, ffmpeg)
	bin := buildHelper(t)
	out := t.TempDir()

	cmd := exec.Command(bin, "serve",
		"-source", fixture, "-out", out, "-ffmpeg", ffmpeg,
		"-parent", fmt.Sprint(os.Getpid()), "-duration", "8",
		"-vcodec", "h264", "-acodec", "flac", "-achannels", "2", "-vmap", "0", "-amap", "1")
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()

	var url string
	sawPackaged := false
	sc := bufio.NewScanner(stdout)
	timeout := time.After(60 * time.Second)
	for url == "" || !sawPackaged {
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
				t.Fatal("stdout closed early")
			}
			var ev map[string]any
			json.Unmarshal([]byte(line), &ev)
			switch ev["event"] {
			case "ready":
				url, _ = ev["url"].(string)
			case "packaged":
				sawPackaged = true
			case "error":
				t.Fatalf("helper error: %v", ev)
			}
		case <-timeout:
			t.Fatalf("timeout; url=%q packaged=%v", url, sawPackaged)
		}
	}

	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	playlist := string(body)
	if !strings.Contains(playlist, "#EXTM3U") || !strings.Contains(playlist, "seg_0000.m4s") {
		t.Fatalf("implausible playlist:\n%s", playlist)
	}
	// AAC expected: stereo FLAC goes down the ≤2ch branch
	segURL := strings.Replace(url, "index.m3u8", "init.mp4", 1)
	resp2, err := http.Get(segURL)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Fatalf("init.mp4 fetch: %d", resp2.StatusCode)
	}
}
