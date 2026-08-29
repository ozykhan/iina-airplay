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

// Synthesizes H.264 + AAC + an embedded SRT track (streams 0/1/2).
func makeSubbedFixture(t *testing.T, ffmpeg string) string {
	t.Helper()
	dir := t.TempDir()
	srt := filepath.Join(dir, "subs.srt")
	os.WriteFile(srt, []byte("1\n00:00:01,000 --> 00:00:03,000\nhello from iina-airplay\n"), 0o644)
	p := filepath.Join(dir, "fixture-subs.mkv")
	cmd := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=duration=8:size=640x360:rate=25",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=8",
		"-i", srt,
		"-map", "0:v", "-map", "1:a", "-map", "2:s",
		"-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac", "-c:s", "srt", p)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("subbed fixture: %v\n%s", err, out)
	}
	return p
}

// mediaURI pulls the URI="..." value out of an EXT-X-MEDIA line.
func mediaURI(line string) string {
	const k = `URI="`
	i := strings.Index(line, k)
	if i < 0 {
		return ""
	}
	rest := line[i+len(k):]
	j := strings.Index(rest, `"`)
	if j < 0 {
		return ""
	}
	return rest[:j]
}

func TestRealFFmpegSubtitledEndToEnd(t *testing.T) {
	ffmpeg := findFFmpeg(t)
	fixture := makeSubbedFixture(t, ffmpeg)
	bin := buildHelper(t)
	out := t.TempDir()

	cmd := exec.Command(bin, "serve",
		"-source", fixture, "-out", out, "-ffmpeg", ffmpeg,
		"-parent", fmt.Sprint(os.Getpid()), "-duration", "8",
		"-vcodec", "h264", "-acodec", "aac", "-achannels", "2", "-vmap", "0", "-amap", "1",
		"-smap", "2", "-sublang", "en", "-subname", "English")
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()

	url := waitReady(t, stdout, 60*time.Second)
	if !strings.HasSuffix(url, "/master.m3u8") {
		t.Fatalf("subtitled cast must advertise master.m3u8, got %q", url)
	}

	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	master := string(body)
	var subLine string
	for _, line := range strings.Split(master, "\n") {
		if strings.HasPrefix(line, "#EXT-X-MEDIA:") && strings.Contains(line, "TYPE=SUBTITLES") {
			subLine = line
		}
	}
	if subLine == "" {
		t.Fatalf("no subtitle rendition in master:\n%s", master)
	}
	for _, want := range []string{"DEFAULT=YES", `NAME="English"`, `LANGUAGE="en"`} {
		if !strings.Contains(subLine, want) {
			t.Fatalf("subtitle line missing %s:\n%s", want, subLine)
		}
	}

	// The rendition playlist and its first segment must be fetchable, and the
	// cue text must have survived the WebVTT conversion.
	vttPlaylistURL := strings.Replace(url, "master.m3u8", mediaURI(subLine), 1)
	resp2, err := http.Get(vttPlaylistURL)
	if err != nil {
		t.Fatal(err)
	}
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if resp2.StatusCode != 200 || !strings.Contains(string(body2), "#EXTM3U") {
		t.Fatalf("vtt playlist fetch: %d\n%s", resp2.StatusCode, body2)
	}
	var seg string
	for _, line := range strings.Split(string(body2), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			seg = line
			break
		}
	}
	if seg == "" {
		t.Fatalf("no segment in vtt playlist:\n%s", body2)
	}
	resp3, err := http.Get(strings.Replace(url, "master.m3u8", seg, 1))
	if err != nil {
		t.Fatal(err)
	}
	body3, _ := io.ReadAll(resp3.Body)
	resp3.Body.Close()
	if !strings.Contains(string(body3), "hello from iina-airplay") {
		t.Fatalf("cue text missing from first vtt segment:\n%s", body3)
	}
}
