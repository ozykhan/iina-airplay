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
	"time"
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
	// VDepth is the source video's bit depth (0 = unknown, treated as 8) and
	// VHeight its height in pixels (0 = unknown). Both only matter when the
	// video is re-encoded: they pick the HEVC profile and the target bitrate.
	VDepth  int
	VHeight int
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

// Transcodes reports whether the video is re-encoded rather than copied.
// Apple TV takes H.264 and HEVC as-is; everything else (AV1, VP9, MPEG-2,
// VC-1, ...) goes through hevc_videotoolbox.
func (c JobConfig) Transcodes() bool { return c.VCodec != "h264" && c.VCodec != "hevc" }

// TranscodeBitrate is the hevc_videotoolbox target in bits/s. 12M suits
// 1080p; UHD sources (common for AV1) get more, since 12M visibly smears
// 4K detail. Unknown height keeps the long-standing 12M.
func (c JobConfig) TranscodeBitrate() int {
	if c.VHeight > 1080 {
		return 30_000_000
	}
	return 12_000_000
}

// BuildArgs ports the proven serve.sh recipe (docs/prototype.md) to a live
// event playlist. Apple TV compatibility matrix: docs/feasibility.md.
func BuildArgs(c JobConfig) []string {
	args := []string{
		"-hide_banner", "-nostats", "-loglevel", "warning", "-y",
		"-progress", "pipe:1",
	}
	if c.VCodec == "av1" {
		// ffmpeg's native "av1" decoder is hardware-only: without a hwaccel
		// it fails every frame ("Your platform doesn't support hardware
		// accelerated AV1 decoding"), and no Mac before M3 has AV1 decode
		// hardware. dav1d decodes in software on every Mac; name it
		// explicitly so a build without it fails loudly at startup instead
		// of silently falling back to the decoder that can't work.
		args = append(args, "-c:v", "libdav1d")
	}
	args = append(args, "-i", c.Source)
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
		args = append(args, "-c:v", "hevc_videotoolbox",
			"-b:v", strconv.Itoa(c.TranscodeBitrate()/1_000_000)+"M", "-tag:v", "hvc1")
		if c.VDepth >= 10 {
			// 10-bit sources (most AV1, all HDR) stay 10-bit: Main 10 from
			// p010le. Left to itself ffmpeg would hand VideoToolbox an 8-bit
			// frame, banding every gradient and breaking HDR10/HLG, whose
			// PQ/HLG transfer only means anything at 10 bits. The colour
			// tags (bt2020, smpte2084/arib-std-b67) carry over from the
			// decoded frames on their own.
			args = append(args, "-profile:v", "main10", "-pix_fmt", "p010le")
		}
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
	// Closed once ffmpeg has exited and its result is on done, so stop can
	// block until the process is really gone.
	finished := make(chan struct{})

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
		close(finished)
	}()

	// stop signals ffmpeg's process group and returns only once ffmpeg has
	// exited: the caller sweeps the output directory next, and a segment
	// ffmpeg was still flushing would otherwise reappear after the sweep.
	// ffmpeg honours SIGTERM within a fraction of a second; a process that
	// ignores it is SIGKILLed after a grace period chosen to stay inside the
	// 3s the `stop` subcommand's TakeOver waits for the old instance.
	// Safe to call more than once and after done has fired.
	stop := func() {
		// Only signal if job is still running
		if jobDone.Load() == 0 {
			syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
			// Close stdout to help the scanner exit if it's still reading
			if c, ok := stdout.(io.Closer); ok {
				c.Close()
			}
		}
		select {
		case <-finished:
			return
		case <-time.After(2 * time.Second):
		}
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		select {
		case <-finished:
		case <-time.After(time.Second):
		}
	}
	return stop, done
}
