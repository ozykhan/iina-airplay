package main

import (
	"bufio"
	"encoding/json"
	"errors"
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

// bundledHelperEnv names the environment variable that points the suite at an
// already-built helper — the universal binary inside a packed .iinaplgz —
// instead of compiling one from source. packaging/test-package.sh sets it.
const bundledHelperEnv = "IINA_AIRPLAY_HELPER"

// locateHelper is the pure decision behind buildHelper: given the value of
// IINA_AIRPLAY_HELPER and a stat function, it returns either the binary to use,
// a request to build one, or a fatal message. It takes no *testing.T so the
// decision can be table-tested with fake paths, mirroring locateSystemFFmpeg in
// e2e_test.go.
//
// A set-but-unusable path is FATAL, never a fall back to `go build`: the reason
// to set this variable at all is to exercise the shipped binary, so a silent
// fallback would let a typo be mistaken for a passing bundled-binary run.
func locateHelper(env string, stat func(string) error) (path string, build bool, fatal string) {
	if env == "" {
		return "", true, ""
	}
	if err := stat(env); err != nil {
		return "", false, fmt.Sprintf("%s=%q is not usable: %v", bundledHelperEnv, env, err)
	}
	return env, false, ""
}

// Builds the helper binary once for integration tests — or returns the bundled
// one when IINA_AIRPLAY_HELPER points at it. Every test that calls this then
// drives the shipped universal binary, not just the e2e ones, so the watchdog,
// the pidfile takeover and the stdout protocol get exercised on whichever
// architecture is running the suite.
func buildHelper(t *testing.T) string {
	t.Helper()
	path, build, fatal := locateHelper(os.Getenv(bundledHelperEnv), func(p string) error {
		_, err := os.Stat(p)
		return err
	})
	if fatal != "" {
		t.Fatal(fatal)
	}
	if !build {
		return path
	}
	bin := filepath.Join(t.TempDir(), "airplay-helper")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

func TestLocateHelper(t *testing.T) {
	statOK := func(string) error { return nil }
	statMissing := func(string) error { return errors.New("no such file or directory") }

	t.Run("unset builds from source", func(t *testing.T) {
		path, build, fatal := locateHelper("", statMissing)
		if !build || path != "" || fatal != "" {
			t.Fatalf("got (%q, %v, %q), want (\"\", true, \"\")", path, build, fatal)
		}
	})

	t.Run("set and present is used as-is", func(t *testing.T) {
		path, build, fatal := locateHelper("/pkg/bin/airplay-helper", statOK)
		if path != "/pkg/bin/airplay-helper" || build || fatal != "" {
			t.Fatalf("got (%q, %v, %q), want the bundled path with no build and no fatal", path, build, fatal)
		}
	})

	// The whole point of setting this variable is to exercise the SHIPPED
	// binary. A silent fallback to `go build` would let a typo in the CI
	// workflow read as a passing bundled-binary run — which is precisely the
	// failure the Intel job exists to rule out. Same contract as findFFmpeg.
	t.Run("set but unusable is fatal, never a fallback", func(t *testing.T) {
		_, build, fatal := locateHelper("/nope/airplay-helper", statMissing)
		if build {
			t.Fatal("a bad IINA_AIRPLAY_HELPER fell back to building from source; a typo must fail loudly")
		}
		if fatal == "" {
			t.Fatal("want a fatal message, got none")
		}
		for _, want := range []string{"IINA_AIRPLAY_HELPER", "/nope/airplay-helper"} {
			if !strings.Contains(fatal, want) {
				t.Errorf("fatal message %q does not mention %q", fatal, want)
			}
		}
	})
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

func TestServeWithSubsAdvertisesRewrittenMaster(t *testing.T) {
	bin := buildHelper(t)
	out := t.TempDir()
	// Stub ffmpeg writes what hlsenc would for one variant + a subtitle
	// rendition, then idles like a long transcode. All three files
	// (index.m3u8, index_vtt.m3u8, master.m3u8) are required before the
	// helper's ready gate fires — leaving any one out here would make
	// waitReady below time out (verified by hand: dropping the
	// index_vtt.m3u8 line makes this test fail with "no ready event in
	// time" instead of passing).
	stub := filepath.Join(t.TempDir(), "ffmpeg")
	master := `#EXTM3U\n#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="default",NAME="subtitle_0",DEFAULT=NO,AUTOSELECT=YES,URI="index_vtt.m3u8"\nindex.m3u8`
	os.WriteFile(stub, []byte(fmt.Sprintf(
		"#!/bin/sh\necho '#EXTM3U' > %[1]s/index.m3u8\necho '#EXTM3U' > %[1]s/index_vtt.m3u8\nprintf '%%b\\n' '%[2]s' > %[1]s/master.m3u8\necho out_time_us=1000000\ntrap 'exit 0' TERM\nsleep 60\n",
		out, master)), 0o755)

	cmd := exec.Command(bin, "serve",
		"-source", "/dev/null", "-out", out, "-ffmpeg", stub,
		"-parent", fmt.Sprint(os.Getpid()), "-duration", "60",
		"-vcodec", "h264", "-acodec", "aac", "-achannels", "2", "-vmap", "0", "-amap", "1",
		"-smap", "2", "-sublang", "en", "-subname", "English")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()

	readyURL := waitReady(t, stdout, 15*time.Second)
	if !strings.HasSuffix(readyURL, "/master.m3u8") {
		t.Fatalf("with subs the helper must advertise the master playlist, got %q", readyURL)
	}
	resp, err := http.Get(readyURL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	s := string(body)
	if !strings.Contains(s, "DEFAULT=YES") || !strings.Contains(s, `NAME="English"`) || !strings.Contains(s, `LANGUAGE="en"`) {
		t.Fatalf("served master not rewritten:\n%s", s)
	}
}

func TestServeCleansStaleSubtitleFiles(t *testing.T) {
	bin := buildHelper(t)
	out := t.TempDir()
	// Stale leftovers from a previous subtitled cast; this cast has no subs,
	// so nothing recreates them — they must be gone once serving is up.
	for _, f := range []string{"master.m3u8", "index_vtt.m3u8", "seg_0000.vtt"} {
		os.WriteFile(filepath.Join(out, f), []byte("stale"), 0o644)
	}
	stub := filepath.Join(t.TempDir(), "ffmpeg")
	os.WriteFile(stub, []byte(fmt.Sprintf(
		"#!/bin/sh\necho '#EXTM3U' > %s/index.m3u8\ntrap 'exit 0' TERM\nsleep 60\n",
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

	waitReady(t, stdout, 15*time.Second)
	for _, f := range []string{"master.m3u8", "index_vtt.m3u8", "seg_0000.vtt"} {
		if _, err := os.Stat(filepath.Join(out, f)); err == nil {
			t.Fatalf("stale %s survived cleanup", f)
		}
	}
}

// waitReady scans helper stdout until a ready event and returns its url.
func waitReady(t *testing.T, stdout io.Reader, timeout time.Duration) string {
	t.Helper()
	sc := bufio.NewScanner(stdout)
	deadline := time.After(timeout)
	for {
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
			if ev["event"] == "error" {
				t.Fatalf("helper errored: %v", ev)
			}
			if ev["event"] == "ready" {
				url, _ := ev["url"].(string)
				return url
			}
		case <-deadline:
			t.Fatal("no ready event in time")
		}
	}
}
