package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func baseCfg() JobConfig {
	return JobConfig{
		FFmpeg: "/opt/homebrew/bin/ffmpeg", Source: "/movies/a.mkv", OutDir: "/tmp/out",
		VCodec: "hevc", ACodec: "flac", AChannels: 6, VMap: 0, AMap: 1, Duration: 5400,
	}
}

func argsHave(t *testing.T, args []string, sub ...string) {
	t.Helper()
	joined := " " + strings.Join(args, " ") + " "
	if !strings.Contains(joined, " "+strings.Join(sub, " ")+" ") {
		t.Fatalf("args missing %q:\n%s", strings.Join(sub, " "), joined)
	}
}

func TestBuildArgsHEVCLossless(t *testing.T) {
	args := BuildArgs(baseCfg())
	argsHave(t, args, "-c:v", "copy")
	argsHave(t, args, "-tag:v", "hvc1")
	argsHave(t, args, "-c:a", "eac3", "-b:a", "640k", "-ac", "6")
	argsHave(t, args, "-map", "0:0", "-map", "0:1", "-sn", "-dn")
	argsHave(t, args, "-hls_playlist_type", "event")
	argsHave(t, args, "-progress", "pipe:1")
	if slices.Contains(args, "hevc_videotoolbox") {
		t.Fatal("HEVC source must not be re-encoded")
	}
}

func TestBuildArgsH264StereoOpus(t *testing.T) {
	c := baseCfg()
	c.VCodec, c.ACodec, c.AChannels = "h264", "opus", 2
	args := BuildArgs(c)
	argsHave(t, args, "-c:v", "copy")
	argsHave(t, args, "-c:a", "aac", "-b:a", "256k")
	if slices.Contains(args, "hvc1") {
		t.Fatal("h264 copy must not carry hvc1 tag")
	}
}

func TestBuildArgsUnsupportedVideo(t *testing.T) {
	c := baseCfg()
	c.VCodec = "vp9"
	args := BuildArgs(c)
	argsHave(t, args, "-c:v", "hevc_videotoolbox", "-b:v", "12M", "-tag:v", "hvc1")
}

func TestBuildArgsAudioCopy(t *testing.T) {
	for _, codec := range []string{"aac", "ac3", "eac3"} {
		c := baseCfg()
		c.ACodec = codec
		argsHave(t, BuildArgs(c), "-c:a", "copy")
	}
}

func TestParseProgress(t *testing.T) {
	k, v, ok := ParseProgressValue("out_time_us=123456789")
	if !ok || k != "out_time_us" || v != "123456789" {
		t.Fatalf("got %q %q %v", k, v, ok)
	}
	if _, _, ok := ParseProgressValue("frame dropped"); ok {
		t.Fatal("non key=value line must not parse")
	}
}

func TestProgressPct(t *testing.T) {
	if got := ProgressPct(2_700_000_000, 5400); got < 49.9 || got > 50.1 {
		t.Fatalf("want ~50, got %f", got)
	}
	if got := ProgressPct(999_999_999_999, 10); got != 100 {
		t.Fatalf("want clamp to 100, got %f", got)
	}
	if got := ProgressPct(5, 0); got != 0 {
		t.Fatalf("zero duration must yield 0, got %f", got)
	}
}

func writeStubFFmpeg(t *testing.T, script string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "ffmpeg")
	os.WriteFile(p, []byte("#!/bin/sh\n"+script), 0o755)
	return p
}

func collectEvents(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var evs []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("bad event line %q: %v", line, err)
		}
		evs = append(evs, m)
	}
	return evs
}

func TestRunJobEmitsProgressAndPackaged(t *testing.T) {
	stub := writeStubFFmpeg(t,
		`echo "out_time_us=2700000000"; echo "progress=continue"; echo "out_time_us=5400000000"; echo "progress=end"`)
	c := baseCfg()
	c.FFmpeg = stub
	var buf bytes.Buffer
	_, done := RunJob(c, &buf)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	evs := collectEvents(t, &buf)
	var sawHalf, sawPackaged bool
	for _, e := range evs {
		if e["event"] == "progress" {
			if pct, _ := e["pct"].(float64); pct > 49 && pct < 51 {
				sawHalf = true
			}
		}
		if e["event"] == "packaged" {
			sawPackaged = true
		}
	}
	if !sawHalf || !sawPackaged {
		t.Fatalf("missing events; got %v", evs)
	}
}

func TestRunJobFailureCarriesStderr(t *testing.T) {
	stub := writeStubFFmpeg(t, `echo "Unknown encoder 'nope'" >&2; exit 1`)
	c := baseCfg()
	c.FFmpeg = stub
	var buf bytes.Buffer
	_, done := RunJob(c, &buf)
	err := <-done
	if err == nil || !strings.Contains(err.Error(), "Unknown encoder") {
		t.Fatalf("want stderr in error, got %v", err)
	}
}

func TestRunJobStop(t *testing.T) {
	stub := writeStubFFmpeg(t, `trap 'exit 0' TERM; sleep 30`)
	c := baseCfg()
	c.FFmpeg = stub
	var buf bytes.Buffer
	stop, done := RunJob(c, &buf)
	time.Sleep(200 * time.Millisecond)
	stop()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("stop() did not terminate ffmpeg")
	}
}

func TestRunJobStopAfterDone(t *testing.T) {
	stub := writeStubFFmpeg(t, `exit 0`)
	c := baseCfg()
	c.FFmpeg = stub
	var buf bytes.Buffer
	stop, done := RunJob(c, &buf)
	// Wait for job to complete
	if err := <-done; err != nil {
		t.Fatalf("job failed: %v", err)
	}
	// Call stop() after job is done - should not panic or signal recycled PID
	stop()
	stop() // Call twice for good measure
}
