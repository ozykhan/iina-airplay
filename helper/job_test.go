package main

import (
	"slices"
	"strings"
	"testing"
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
