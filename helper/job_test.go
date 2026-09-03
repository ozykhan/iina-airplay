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

func TestBuildArgsNoSubByteIdentical(t *testing.T) {
	// Pin the exact no-subs command line: subtitle support must not disturb
	// the proven recipe when no sub is configured (spec §2).
	got := strings.Join(BuildArgs(baseCfg()), " ")
	want := strings.Join([]string{
		"-hide_banner", "-nostats", "-loglevel", "warning", "-y",
		"-progress", "pipe:1",
		"-i", "/movies/a.mkv",
		"-map", "0:0", "-map", "0:1", "-sn", "-dn",
		"-c:v", "copy", "-tag:v", "hvc1",
		"-c:a", "eac3", "-b:a", "640k", "-ac", "6",
		"-f", "hls", "-hls_time", "6", "-hls_playlist_type", "event",
		"-hls_segment_type", "fmp4", "-hls_flags", "independent_segments",
		"-hls_fmp4_init_filename", "init.mp4",
		"-hls_segment_filename", "/tmp/out/seg_%04d.m4s",
		"/tmp/out/index.m3u8",
	}, " ")
	if got != want {
		t.Fatalf("no-subs args changed:\ngot:  %s\nwant: %s", got, want)
	}
}

func TestBuildArgsEmbeddedSub(t *testing.T) {
	c := baseCfg()
	c.SubMap = "3"
	args := BuildArgs(c)
	argsHave(t, args, "-map", "0:3", "-dn")
	argsHave(t, args, "-c:s", "webvtt")
	argsHave(t, args, "-var_stream_map", "v:0,a:0,s:0,sgroup:subs")
	argsHave(t, args, "-master_pl_name", "master.m3u8")
	if slices.Contains(args, "-sn") {
		t.Fatal("-sn must be dropped when a subtitle is mapped")
	}
}

func TestBuildArgsExternalSub(t *testing.T) {
	c := baseCfg()
	c.SubPath = "/subs/en.srt"
	args := BuildArgs(c)
	argsHave(t, args, "-i", "/movies/a.mkv")
	argsHave(t, args, "-i", "/subs/en.srt")
	argsHave(t, args, "-map", "1:0", "-dn")
	argsHave(t, args, "-c:s", "webvtt")
	argsHave(t, args, "-var_stream_map", "v:0,a:0,s:0,sgroup:subs")
	if slices.Index(args, "/subs/en.srt") < slices.Index(args, "/movies/a.mkv") {
		t.Fatal("subtitle input must come after the media input (it must be input 1)")
	}
	if slices.Contains(args, "-sn") {
		t.Fatal("-sn must be dropped when a subtitle is mapped")
	}
}

func TestHasSubAndPlaylistName(t *testing.T) {
	c := baseCfg()
	if c.HasSub() || c.PlaylistName() != "index.m3u8" {
		t.Fatalf("no-subs config: HasSub=%v playlist=%q", c.HasSub(), c.PlaylistName())
	}
	c.SubMap = "0" // ff-index 0 is a valid stream — string zero value keeps it unambiguous
	if !c.HasSub() || c.PlaylistName() != "master.m3u8" {
		t.Fatalf("embedded-sub config: HasSub=%v playlist=%q", c.HasSub(), c.PlaylistName())
	}
	c = baseCfg()
	c.SubPath = "/subs/en.srt"
	if !c.HasSub() || c.PlaylistName() != "master.m3u8" {
		t.Fatalf("external-sub config: HasSub=%v playlist=%q", c.HasSub(), c.PlaylistName())
	}
}

// stop() must not return until ffmpeg is gone: the caller sweeps the output
// directory right after, and a segment ffmpeg is still flushing would come
// back. A stub that ignores TERM (sleep inherits the ignored disposition)
// proves the KILL escalation as well.
func TestRunJobStopWaitsForExit(t *testing.T) {
	stub := writeStubFFmpeg(t, `trap '' TERM; sleep 30`)
	c := baseCfg()
	c.FFmpeg = stub
	var buf bytes.Buffer
	stop, done := RunJob(c, &buf)
	time.Sleep(200 * time.Millisecond)
	stop()
	select {
	case <-done:
	default:
		t.Fatal("stop() returned before ffmpeg exited")
	}
}
