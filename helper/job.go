package main

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

type JobConfig struct {
	FFmpeg    string
	Source    string
	OutDir    string
	VCodec    string
	ACodec    string
	AChannels int
	VMap      int
	AMap      int
	Duration  float64
}

// BuildArgs ports the proven serve.sh recipe (docs/prototype.md) to a live
// event playlist. Apple TV compatibility matrix: docs/feasibility.md.
func BuildArgs(c JobConfig) []string {
	args := []string{
		"-hide_banner", "-nostats", "-loglevel", "warning", "-y",
		"-progress", "pipe:1",
		"-i", c.Source,
		"-map", "0:" + strconv.Itoa(c.VMap),
		"-map", "0:" + strconv.Itoa(c.AMap),
		"-sn", "-dn",
	}
	switch c.VCodec {
	case "h264":
		args = append(args, "-c:v", "copy")
	case "hevc":
		args = append(args, "-c:v", "copy", "-tag:v", "hvc1")
	default:
		args = append(args, "-c:v", "hevc_videotoolbox", "-b:v", "12M", "-tag:v", "hvc1")
	}
	switch c.ACodec {
	case "aac", "ac3", "eac3":
		args = append(args, "-c:a", "copy")
	default:
		if c.AChannels > 2 {
			args = append(args, "-c:a", "eac3", "-b:a", "640k", "-ac", "6")
		} else {
			args = append(args, "-c:a", "aac", "-b:a", "256k")
		}
	}
	return append(args,
		"-f", "hls", "-hls_time", "6", "-hls_playlist_type", "event",
		"-hls_segment_type", "fmp4", "-hls_flags", "independent_segments",
		"-hls_fmp4_init_filename", "init.mp4",
		"-hls_segment_filename", filepath.Join(c.OutDir, "seg_%04d.m4s"),
		filepath.Join(c.OutDir, "index.m3u8"),
	)
}

func ParseProgressValue(line string) (string, string, bool) {
	k, v, found := strings.Cut(strings.TrimSpace(line), "=")
	if !found || k == "" {
		return "", "", false
	}
	return k, v, true
}

func ProgressPct(outTimeUS int64, duration float64) float64 {
	if duration <= 0 {
		return 0
	}
	pct := float64(outTimeUS) / 1e6 / duration * 100
	if pct > 100 {
		return 100
	}
	if pct < 0 {
		return 0
	}
	return pct
}

func RunJob(c JobConfig, out io.Writer) (func(), <-chan error) {
	done := make(chan error, 1)
	cmd := exec.Command(c.FFmpeg, BuildArgs(c)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err == nil {
		err = cmd.Start()
	}
	if err != nil {
		done <- err
		return func() {}, done
	}
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			k, v, ok := ParseProgressValue(sc.Text())
			if !ok {
				continue
			}
			switch k {
			case "out_time_us":
				if us, perr := strconv.ParseInt(v, 10, 64); perr == nil {
					Emit(out, "progress", map[string]any{"pct": ProgressPct(us, c.Duration)})
				}
			case "progress":
				if v == "end" {
					Emit(out, "packaged", nil)
				}
			}
		}
		// Handle any scanner error, then wait for process
		werr := cmd.Wait()
		if werr != nil {
			werr = fmt.Errorf("ffmpeg: %w; stderr: %s", werr, strings.TrimSpace(stderr.String()))
		}
		done <- werr
	}()
	stop := func() {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		// Close stdout to help the scanner exit if it's still reading
		if c, ok := stdout.(io.Closer); ok {
			c.Close()
		}
	}
	return stop, done
}
