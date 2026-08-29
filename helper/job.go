package main

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
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
	// Subtitles are optional; zero values mean "no subtitle track". SubMap
	// holds the ff-index as a string so the zero value can't be mistaken
	// for stream 0. SubPath (external file, fed as input 1) wins over SubMap.
	SubPath  string
	SubMap   string
	SubLang  string
	SubName  string
	Duration float64
}

func (c JobConfig) HasSub() bool { return c.SubPath != "" || c.SubMap != "" }

// PlaylistName is the playlist the TV is handed: ffmpeg's master playlist
// when a subtitle rendition exists, the media playlist alone otherwise.
func (c JobConfig) PlaylistName() string {
	if c.HasSub() {
		return "master.m3u8"
	}
	return "index.m3u8"
}

// BuildArgs ports the proven serve.sh recipe (docs/prototype.md) to a live
// event playlist. Apple TV compatibility matrix: docs/feasibility.md.
func BuildArgs(c JobConfig) []string {
	args := []string{
		"-hide_banner", "-nostats", "-loglevel", "warning", "-y",
		"-progress", "pipe:1",
		"-i", c.Source,
	}
	if c.SubPath != "" {
		args = append(args, "-i", c.SubPath)
	}
	args = append(args,
		"-map", "0:"+strconv.Itoa(c.VMap),
		"-map", "0:"+strconv.Itoa(c.AMap),
	)
	switch {
	case c.SubPath != "":
		args = append(args, "-map", "1:0", "-dn")
	case c.SubMap != "":
		args = append(args, "-map", "0:"+c.SubMap, "-dn")
	default:
		args = append(args, "-sn", "-dn")
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
	if c.HasSub() {
		args = append(args, "-c:s", "webvtt")
	}
	args = append(args,
		"-f", "hls", "-hls_time", "6", "-hls_playlist_type", "event",
		"-hls_segment_type", "fmp4", "-hls_flags", "independent_segments",
		"-hls_fmp4_init_filename", "init.mp4",
		"-hls_segment_filename", filepath.Join(c.OutDir, "seg_%04d.m4s"),
	)
	if c.HasSub() {
		// v:0/a:0/s:0 are positions within each stream type among the MAPPED
		// output streams (BuildArgs always maps exactly one video, one audio,
		// one subtitle, in that order) — not ff-indices — so this is correct
		// for any -vmap/-amap/-smap values. Without -var_stream_map, ffmpeg
		// writes the subtitle rendition to disk but omits the
		// #EXT-X-MEDIA:TYPE=SUBTITLES line from the master playlist.
		args = append(args, "-var_stream_map", "v:0,a:0,s:0,sgroup:subs")
		args = append(args, "-master_pl_name", "master.m3u8")
	}
	return append(args, filepath.Join(c.OutDir, "index.m3u8"))
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

	// Flag to track when job has completed (0 = running, 1 = done)
	var jobDone atomic.Int32

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
		// Mark job as done before sending to channel
		jobDone.Store(1)
		done <- werr
	}()

	stop := func() {
		// Only signal if job is still running
		if jobDone.Load() == 0 {
			syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
			// Close stdout to help the scanner exit if it's still reading
			if c, ok := stdout.(io.Closer); ok {
				c.Close()
			}
		}
	}
	return stop, done
}
