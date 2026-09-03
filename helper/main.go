package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: airplay-helper serve|stop ...")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "serve":
		runServe(os.Args[2:])
	case "stop":
		fs := flag.NewFlagSet("stop", flag.ExitOnError)
		out := fs.String("out", "", "output dir of the instance to stop")
		fs.Parse(os.Args[2:])
		if *out == "" {
			fmt.Fprintln(os.Stderr, "missing required flag: -out")
			os.Exit(2)
		}
		if err := TakeOver(filepath.Join(*out, "helper.pid")); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		// The serving instance sweeps its own output on SIGTERM, and
		// TakeOver waited for it. This catches a helper that died without
		// sweeping (kill -9): after TakeOver nobody else owns the directory.
		cleanOutDir(*out)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", os.Args[1])
		os.Exit(2)
	}
}

func runServe(argv []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	var c JobConfig
	var parent int
	fs.StringVar(&c.Source, "source", "", "media file")
	fs.StringVar(&c.OutDir, "out", "", "HLS output dir")
	fs.StringVar(&c.FFmpeg, "ffmpeg", "", "ffmpeg binary")
	fs.IntVar(&parent, "parent", 0, "IINA pid to watch")
	fs.Float64Var(&c.Duration, "duration", 0, "source duration seconds")
	fs.StringVar(&c.VCodec, "vcodec", "", "source video codec (mpv name)")
	fs.StringVar(&c.ACodec, "acodec", "", "source audio codec (mpv name)")
	fs.IntVar(&c.AChannels, "achannels", 2, "audio channel count")
	fs.IntVar(&c.VMap, "vmap", 0, "video stream ff-index")
	fs.IntVar(&c.AMap, "amap", 1, "audio stream ff-index")
	fs.StringVar(&c.SubMap, "smap", "", "embedded subtitle stream ff-index (empty = no subtitles)")
	fs.StringVar(&c.SubPath, "subpath", "", "external subtitle file (used as input 1; overrides -smap)")
	fs.StringVar(&c.SubLang, "sublang", "", "subtitle language tag for the HLS rendition")
	fs.StringVar(&c.SubName, "subname", "", "subtitle display name for the HLS rendition")
	fs.Parse(argv)

	// pidfile is set once -out is known to be non-empty (right after the
	// flag-validation check below). fail removes it before exiting — a
	// no-op, error ignored, if it's still empty (validation failed before
	// -out was usable) or if it was never actually written (e.g.
	// WritePidfile itself failed) — so every error exit leaves no stale
	// pidfile behind, matching the WatchParent-triggered exit path below.
	var pidfile string
	// outOwned flips once TakeOver has cleared the output directory of any
	// previous instance. Only then may an exit sweep it: before that, the
	// files there may belong to a cast another instance is still serving.
	outOwned := false
	// stopJob is replaced once ffmpeg is running; fail can run before that.
	stopJob := func() {}
	fail := func(msg string) {
		stopJob()
		if outOwned {
			cleanOutDir(c.OutDir)
		}
		if pidfile != "" {
			os.Remove(pidfile)
		}
		Emit(os.Stdout, "error", map[string]any{"msg": msg})
		os.Exit(1)
	}

	if c.Source == "" || c.OutDir == "" || c.FFmpeg == "" || parent == 0 {
		fail("missing required flags: -source, -out, -ffmpeg, -parent")
	}
	if _, err := os.Stat(c.FFmpeg); err != nil {
		fail("ffmpeg not found at " + c.FFmpeg + " — reinstall the plugin through IINA's plugin installer")
	}
	if err := os.MkdirAll(c.OutDir, 0o755); err != nil {
		fail(err.Error())
	}
	pidfile = filepath.Join(c.OutDir, "helper.pid")
	if err := TakeOver(pidfile); err != nil {
		fail("stale instance: " + err.Error())
	}
	outOwned = true
	// Every exit path below sweeps, so this only matters for a previous
	// instance that died without getting to its own sweep (kill -9).
	cleanOutDir(c.OutDir)
	if err := WritePidfile(pidfile); err != nil {
		fail(err.Error())
	}
	defer os.Remove(pidfile)

	port, shutdown, err := StartServer(c.OutDir, c.SubName, c.SubLang)
	if err != nil {
		fail(err.Error())
	}
	defer shutdown()

	ip, err := LanIP()
	if err != nil {
		fail(err.Error())
	}

	var done <-chan error
	stopJob, done = RunJob(c, os.Stdout)

	// teardown is every non-error way out: stop ffmpeg (blocks until it has
	// exited), then sweep. The remux leaves with the cast, not at the next
	// one — a UHD remux is tens of gigabytes of temp (issue #12).
	teardown := func() {
		stopJob()
		cleanOutDir(c.OutDir)
	}

	WatchParent(parent, func() {
		teardown()
		os.Remove(pidfile)
		os.Exit(0)
	})
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)

	// The TV can start pulling as soon as the advertised playlist exists.
	// With subs that's ffmpeg's master playlist — but the variant playlists
	// it points at (media + subtitle rendition) must exist too before the
	// URL is usable.
	playlistName := c.PlaylistName()
	readyURL := fmt.Sprintf("http://%s:%d/%s", ip, port, playlistName)
	playlistReady := func() bool {
		if _, err := os.Stat(filepath.Join(c.OutDir, playlistName)); err != nil {
			return false
		}
		if c.HasSub() {
			if _, err := os.Stat(filepath.Join(c.OutDir, "index.m3u8")); err != nil {
				return false
			}
			// master.m3u8's subtitle rendition URI= points at this file; the
			// TV will 404 on it if the master is advertised before it exists.
			if _, err := os.Stat(filepath.Join(c.OutDir, "index_vtt.m3u8")); err != nil {
				return false
			}
		}
		return true
	}
	readyDeadline := time.Now().Add(120 * time.Second)
	readyTick := time.NewTicker(200 * time.Millisecond)
	defer readyTick.Stop()
	ready := false
	for {
		select {
		case <-readyTick.C:
			if !ready {
				if playlistReady() {
					Emit(os.Stdout, "ready", map[string]any{"url": readyURL})
					ready = true
					readyTick.Stop()
				} else if time.Now().After(readyDeadline) {
					fail("packaging produced no playlist within 120s")
				}
			}
		case err := <-done:
			if err != nil {
				fail(err.Error())
			}
			if !ready {
				// Job finished before the poll noticed the playlist (tiny file).
				if playlistReady() {
					Emit(os.Stdout, "ready", map[string]any{"url": readyURL})
					ready = true
					readyTick.Stop()
				} else {
					fail("ffmpeg finished but produced no playlist")
				}
			}
			// Keep serving completed VOD until killed.
			<-sigs
			teardown()
			Emit(os.Stdout, "stopped", nil)
			return
		case <-sigs:
			teardown()
			Emit(os.Stdout, "stopped", nil)
			return
		}
	}
}
