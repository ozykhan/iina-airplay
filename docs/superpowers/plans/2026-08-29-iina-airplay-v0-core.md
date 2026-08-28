# IINA AirPlay v0 Core Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A working IINA plugin that casts the currently playing local file to an AirPlay TV: Go supervisor (HTTP serve + watchdog + ffmpeg jobs) driven by a JS plugin with a sidebar UI.

**Architecture:** The JS plugin reads track metadata from mpv, spawns `airplay-helper serve` via `utils.exec`, and consumes JSON-line events from its stdout. The helper packages the file with ffmpeg into a live HLS event playlist, serves it on a free port bound to `0.0.0.0`, watches IINA's PID, and dies with it. The sidebar page hosts a hidden `<video>` and the AirPlay picker button. Rationale and constraints: `docs/distribution.md`, `docs/prototype.md` (findings section), `docs/feasibility.md`.

**Tech Stack:** Go (stdlib only) for the helper; plain single-file JS for the plugin (no bundler); `node --test` for JS pure-function tests; `go test` for the helper; brew ffmpeg stands in for the bundled static ffmpeg during development.

## Global Constraints

- Helper uses **Go stdlib only** — no third-party modules (contributors must build with `go build` alone).
- **Never call `sidebar.*` from a `utils.exec` callback or promise** — it SIGABRTs IINA (AutoLayout thread assertion; see docs/prototype.md). Sidebar calls happen only in menu callbacks and `onMessage` handlers; the page polls for state.
- `utils.exec` wipes the environment (only `LC_ALL` survives) and resolves bare binary names against IINA's bundled dirs — **always pass absolute paths** for binaries and files.
- **No runtime network access** by plugin or helper other than serving the stream on the LAN (no downloads, no update checks).
- HLS output: fMP4 segments, **`-hls_playlist_type event`** (live-growing), 6-second segments.
- The helper binds `0.0.0.0` on a **kernel-assigned free port** (`:0`) and advertises the LAN IP — never 127.0.0.1, never a hardcoded port.
- v0 scope: current file from position 0, IINA's selected audio track, one video track, **no subtitles** (`-sn`), remux-first (H.264/HEVC copy; else `hevc_videotoolbox`), audio copy for aac/ac3/eac3 else E-AC-3 5.1 (>2ch) / AAC (≤2ch).
- The media element in the sidebar must **retry a failed first load exactly once** (known WebKit flake, docs/prototype.md).
- macOS only. Plugin `Info.json` permissions: `file-system`, `show-osd` only.

## File Structure

```
helper/                      Go supervisor (module github.com/ozykhan/iina-airplay/helper)
  go.mod
  main.go                    CLI wiring: serve / stop subcommands
  protocol.go(+_test)        JSON-line stdout emitter
  server.go(+_test)          HTTP file server: MIME, Range, free port
  netinfo.go(+_test)         LAN IPv4 discovery
  pidfile.go(+_test)         pidfile write/read/takeover
  watchdog.go(+_test)        parent-PID watcher
  job.go(+_test)             ffmpeg arg builder, progress parser, runner
  e2e_test.go                end-to-end with real ffmpeg (build tag)
plugin/                      the .iinaplugin content
  Info.json
  main.js                    menu, state machine, exec orchestration, pure fns
  sidebar.html               cast UI: status, progress, picker, hidden video
plugin/tests/
  main.test.mjs              node --test over main.js pure exports
Makefile                     dev loop: build helper into plugin/bin, link into IINA
```

---

### Task 1: Helper scaffold + JSON protocol emitter

**Files:**
- Create: `helper/go.mod`, `helper/protocol.go`
- Test: `helper/protocol_test.go`

**Interfaces:**
- Produces: `Emit(w io.Writer, event string, fields map[string]any)` — writes one JSON line `{"event":"<event>",...fields}` and a trailing `\n`; concurrency-safe via package-level mutex. Event names used by later tasks and the JS side: `ready`, `progress`, `packaged`, `error`, `stopped`.

- [ ] **Step 1: Create the module**

```bash
mkdir -p helper && cd helper && go mod init github.com/ozykhan/iina-airplay/helper
```

- [ ] **Step 2: Write the failing test**

```go
// helper/protocol_test.go
package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestEmitWritesOneJSONLine(t *testing.T) {
	var buf bytes.Buffer
	Emit(&buf, "ready", map[string]any{"url": "http://10.0.0.5:49152/index.m3u8"})
	line := buf.String()
	if !strings.HasSuffix(line, "\n") || strings.Count(line, "\n") != 1 {
		t.Fatalf("want exactly one newline-terminated line, got %q", line)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if got["event"] != "ready" || got["url"] != "http://10.0.0.5:49152/index.m3u8" {
		t.Fatalf("wrong payload: %v", got)
	}
}

func TestEmitNilFields(t *testing.T) {
	var buf bytes.Buffer
	Emit(&buf, "stopped", nil)
	if strings.TrimSpace(buf.String()) != `{"event":"stopped"}` {
		t.Fatalf("got %q", buf.String())
	}
}
```

- [ ] **Step 3: Run to verify failure**

Run: `cd helper && go test ./... -run TestEmit -v`
Expected: FAIL (Emit undefined).

- [ ] **Step 4: Implement**

```go
// helper/protocol.go
package main

import (
	"encoding/json"
	"io"
	"sync"
)

var emitMu sync.Mutex

// Emit writes one JSON event line. The JS side splits helper stdout on
// newlines, so an event must never span or share a line.
func Emit(w io.Writer, event string, fields map[string]any) {
	obj := map[string]any{"event": event}
	for k, v := range fields {
		obj[k] = v
	}
	b, err := json.Marshal(obj)
	if err != nil {
		b = []byte(`{"event":"error","msg":"marshal failure"}`)
	}
	emitMu.Lock()
	defer emitMu.Unlock()
	w.Write(append(b, '\n'))
}
```

- [ ] **Step 5: Run to verify pass**

Run: `cd helper && go test ./... -run TestEmit -v` — Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add helper/go.mod helper/protocol.go helper/protocol_test.go
git commit -m "helper: scaffold module and JSON-line protocol emitter"
```

---

### Task 2: HTTP server — MIME, Range, free port

**Files:**
- Create: `helper/server.go`
- Test: `helper/server_test.go`

**Interfaces:**
- Produces: `StartServer(dir string) (port int, shutdown func(), err error)` — serves `dir` on `0.0.0.0:<free port>`; `.m3u8` → `application/vnd.apple.mpegurl`, `.m4s` → `video/iso.segment`, `.mp4` → `video/mp4`; Range requests honored (Go's `http.FileServer` provides this).

- [ ] **Step 1: Write the failing test**

```go
// helper/server_test.go
package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestServerMIMEAndRange(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.m3u8"), []byte("#EXTM3U\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "seg_0000.m4s"), []byte("0123456789"), 0o644)

	port, shutdown, err := StartServer(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer shutdown()

	base := fmt.Sprintf("http://127.0.0.1:%d", port)

	resp, err := http.Get(base + "/index.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "application/vnd.apple.mpegurl" {
		t.Fatalf("m3u8 content-type = %q", ct)
	}

	req, _ := http.NewRequest("GET", base+"/seg_0000.m4s", nil)
	req.Header.Set("Range", "bytes=2-4")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusPartialContent || string(body) != "234" {
		t.Fatalf("range request: status=%d body=%q", resp2.StatusCode, body)
	}
	if ct := resp2.Header.Get("Content-Type"); ct != "video/iso.segment" {
		t.Fatalf("m4s content-type = %q", ct)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd helper && go test -run TestServerMIMEAndRange -v` — Expected: FAIL (StartServer undefined).

- [ ] **Step 3: Implement**

```go
// helper/server.go
package main

import (
	"context"
	"mime"
	"net"
	"net/http"
	"time"
)

// Safari refuses HLS served as application/octet-stream, and Go's built-in
// table doesn't know these extensions.
func init() {
	mime.AddExtensionType(".m3u8", "application/vnd.apple.mpegurl")
	mime.AddExtensionType(".m4s", "video/iso.segment")
	mime.AddExtensionType(".mp4", "video/mp4")
}

func StartServer(dir string) (int, func(), error) {
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		return 0, nil, err
	}
	srv := &http.Server{Handler: http.FileServer(http.Dir(dir))}
	go srv.Serve(ln)
	shutdown := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}
	return ln.Addr().(*net.TCPAddr).Port, shutdown, nil
}
```

- [ ] **Step 4: Run to verify pass**

Run: `cd helper && go test -run TestServerMIMEAndRange -v` — Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add helper/server.go helper/server_test.go
git commit -m "helper: HLS file server with correct MIME types, Range, free port"
```

---

### Task 3: LAN IP discovery

**Files:**
- Create: `helper/netinfo.go`
- Test: `helper/netinfo_test.go`

**Interfaces:**
- Produces: `LanIP() (string, error)` — first non-loopback, non-link-local IPv4 of an up interface; error if none (the TV pulls the stream itself, so 127.0.0.1 is useless — docs/feasibility.md). Also `pickIPv4(addrs []net.Addr) string` (pure, testable): same selection over a candidate list, `""` if none.

- [ ] **Step 1: Write the failing test**

```go
// helper/netinfo_test.go
package main

import (
	"net"
	"testing"
)

func mustCIDR(t *testing.T, s string) net.Addr {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPickIPv4(t *testing.T) {
	cases := []struct {
		addrs []net.Addr
		want  string
	}{
		{[]net.Addr{mustCIDR(t, "127.0.0.1/8")}, ""},                                  // loopback rejected
		{[]net.Addr{mustCIDR(t, "169.254.10.1/16")}, ""},                              // link-local rejected
		{[]net.Addr{mustCIDR(t, "fe80::1/64"), mustCIDR(t, "192.168.1.18/24")}, "192.168.1.18"}, // v6 skipped
		{[]net.Addr{mustCIDR(t, "10.0.0.7/8")}, "10.0.0.7"},
	}
	for i, c := range cases {
		if got := pickIPv4(c.addrs); got != c.want {
			t.Errorf("case %d: got %q want %q", i, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd helper && go test -run TestPickIPv4 -v` — Expected: FAIL.

- [ ] **Step 3: Implement**

```go
// helper/netinfo.go
package main

import (
	"errors"
	"net"
)

func pickIPv4(addrs []net.Addr) string {
	for _, a := range addrs {
		var ip net.IP
		switch v := a.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		}
		ip4 := ip.To4()
		if ip4 == nil || ip4.IsLoopback() || ip4.IsLinkLocalUnicast() {
			continue
		}
		return ip4.String()
	}
	return ""
}

func LanIP() (string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		if ip := pickIPv4(addrs); ip != "" {
			return ip, nil
		}
	}
	return "", errors.New("no LAN IPv4 address found; the TV pulls the stream itself, so a routable address is required")
}
```

- [ ] **Step 4: Run to verify pass**

Run: `cd helper && go test -run TestPickIPv4 -v` — Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add helper/netinfo.go helper/netinfo_test.go
git commit -m "helper: LAN IPv4 discovery"
```

---

### Task 4: Pidfile takeover + parent watchdog

**Files:**
- Create: `helper/pidfile.go`, `helper/watchdog.go`
- Test: `helper/pidfile_test.go`, `helper/watchdog_test.go`

**Interfaces:**
- Produces:
  - `WritePidfile(path string) error` — writes own PID.
  - `TakeOver(path string) error` — if the pidfile names a live process, SIGTERM it and wait (up to 3 s) for it to die; then remove the file. Handles: no file, stale file (dead PID), live predecessor. This is stale-instance handling from docs/distribution.md.
  - `WatchParent(pid int, onGone func())` — goroutine polling `syscall.Kill(pid, 0)` every 2 s; calls `onGone` once when the process is gone. Kills the orphaned-helper failure found in prototyping.

- [ ] **Step 1: Write the failing tests**

```go
// helper/pidfile_test.go
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestTakeOverNoFile(t *testing.T) {
	if err := TakeOver(filepath.Join(t.TempDir(), "helper.pid")); err != nil {
		t.Fatal(err)
	}
}

func TestTakeOverStalePid(t *testing.T) {
	p := filepath.Join(t.TempDir(), "helper.pid")
	os.WriteFile(p, []byte("999999"), 0o644) // beyond macOS pid range
	if err := TakeOver(p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("stale pidfile not removed")
	}
}

func TestTakeOverLiveProcess(t *testing.T) {
	cmd := exec.Command("/bin/sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	p := filepath.Join(t.TempDir(), "helper.pid")
	os.WriteFile(p, []byte(strconv.Itoa(cmd.Process.Pid)), 0o644)
	if err := TakeOver(p); err != nil {
		t.Fatal(err)
	}
	// SIGTERM should have landed
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(4 * time.Second):
		t.Fatal("predecessor still alive after TakeOver")
	}
}
```

```go
// helper/watchdog_test.go
package main

import (
	"os/exec"
	"testing"
	"time"
)

func TestWatchParentFires(t *testing.T) {
	cmd := exec.Command("/bin/sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	fired := make(chan struct{})
	WatchParent(cmd.Process.Pid, func() { close(fired) })
	cmd.Process.Kill()
	cmd.Wait()
	select {
	case <-fired:
	case <-time.After(6 * time.Second):
		t.Fatal("watchdog did not fire after parent death")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd helper && go test -run 'TestTakeOver|TestWatchParent' -v` — Expected: FAIL.

- [ ] **Step 3: Implement**

```go
// helper/pidfile.go
package main

import (
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func WritePidfile(path string) error {
	return os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0o644)
}

func processAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

func TakeOver(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err == nil && pid > 0 && processAlive(pid) {
		syscall.Kill(pid, syscall.SIGTERM)
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) && processAlive(pid) {
			time.Sleep(100 * time.Millisecond)
		}
	}
	return os.Remove(path)
}
```

```go
// helper/watchdog.go
package main

import "time"

// WatchParent polls rather than using kqueue: ~nothing at 2s intervals, and
// it works no matter how the parent died (quit, crash, kill -9).
func WatchParent(pid int, onGone func()) {
	go func() {
		for {
			time.Sleep(2 * time.Second)
			if !processAlive(pid) {
				onGone()
				return
			}
		}
	}()
}
```

- [ ] **Step 4: Run to verify pass**

Run: `cd helper && go test -run 'TestTakeOver|TestWatchParent' -v` — Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add helper/pidfile.go helper/pidfile_test.go helper/watchdog.go helper/watchdog_test.go
git commit -m "helper: pidfile takeover and parent watchdog"
```

---

### Task 5: ffmpeg argument builder + progress parser (pure)

**Files:**
- Create: `helper/job.go` (pure parts)
- Test: `helper/job_test.go`

**Interfaces:**
- Consumes: nothing (pure).
- Produces:
  - `type JobConfig struct { FFmpeg, Source, OutDir, VCodec, ACodec string; AChannels, VMap, AMap int; Duration float64 }` — codec names use mpv's spelling, which matches ffmpeg's for everything we branch on (`h264`, `hevc`, `aac`, `ac3`, `eac3`).
  - `BuildArgs(c JobConfig) []string` — the full ffmpeg argv (excluding argv[0]).
  - `ParseProgressValue(line string) (key, value string, ok bool)` — parses one `key=value` line of `-progress` output.
  - `ProgressPct(outTimeUS int64, duration float64) float64` — clamped 0–100.

- [ ] **Step 1: Write the failing tests**

```go
// helper/job_test.go
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
```

- [ ] **Step 2: Run to verify failure**

Run: `cd helper && go test -run 'TestBuildArgs|TestParseProgress|TestProgressPct' -v` — Expected: FAIL.

- [ ] **Step 3: Implement**

```go
// helper/job.go
package main

import (
	"path/filepath"
	"strconv"
	"strings"
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
```

- [ ] **Step 4: Run to verify pass**

Run: `cd helper && go test -run 'TestBuildArgs|TestParseProgress|TestProgressPct' -v` — Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add helper/job.go helper/job_test.go
git commit -m "helper: ffmpeg argument builder and progress parser"
```

---

### Task 6: Job runner — spawn ffmpeg, pump progress, report completion

**Files:**
- Modify: `helper/job.go` (append)
- Test: `helper/job_test.go` (append)

**Interfaces:**
- Consumes: `BuildArgs`, `ParseProgressValue`, `ProgressPct`, `Emit` (Task 1).
- Produces: `RunJob(c JobConfig, out io.Writer) (stop func(), done <-chan error)` — starts ffmpeg with `BuildArgs(c)`; reads ffmpeg's stdout (`-progress pipe:1` key=value blocks), emitting `{"event":"progress","pct":N}` on each `out_time_us` and `{"event":"packaged"}` on `progress=end`; `done` receives ffmpeg's exit error (nil on success); `stop()` SIGTERMs ffmpeg. ffmpeg stderr is captured and included in the error on failure.

- [ ] **Step 1: Write the failing test** (uses a stub "ffmpeg" shell script — no real ffmpeg needed)

```go
// append to helper/job_test.go
import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

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
```

- [ ] **Step 2: Run to verify failure**

Run: `cd helper && go test -run TestRunJob -v` — Expected: FAIL (RunJob undefined).

- [ ] **Step 3: Implement** (append to `helper/job.go`)

```go
import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
	"strings" // already imported; shown for completeness
	"syscall"
)

func RunJob(c JobConfig, out io.Writer) (func(), <-chan error) {
	done := make(chan error, 1)
	cmd := exec.Command(c.FFmpeg, BuildArgs(c)...)
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
		werr := cmd.Wait()
		if werr != nil {
			werr = fmt.Errorf("ffmpeg: %w; stderr: %s", werr, strings.TrimSpace(stderr.String()))
		}
		done <- werr
	}()
	stop := func() { cmd.Process.Signal(syscall.SIGTERM) }
	return stop, done
}
```

- [ ] **Step 4: Run to verify pass**

Run: `cd helper && go test -run TestRunJob -v` — Expected: PASS. Then `go test ./...` — all PASS.

- [ ] **Step 5: Commit**

```bash
git add helper/job.go helper/job_test.go
git commit -m "helper: ffmpeg job runner with progress events and stop"
```

---

### Task 7: `serve` / `stop` commands — wire everything in main.go

**Files:**
- Create: `helper/main.go`
- Test: `helper/main_test.go`

**Interfaces:**
- Consumes: everything from Tasks 1–6.
- Produces the helper's CLI, which is the contract with the JS side (Task 9):
  - `airplay-helper serve -source <path> -out <dir> -ffmpeg <path> -parent <pid> -duration <sec> -vcodec <name> -acodec <name> -achannels <n> -vmap <ffindex> -amap <ffindex>`
  - `airplay-helper stop -out <dir>`
  - `serve` flow: `TakeOver(out/helper.pid)` → clean stale segments → `WritePidfile` → `StartServer` → `RunJob` → poll for `out/index.m3u8` (200 ms interval, 120 s timeout) → `Emit ready {url: "http://<LanIP>:<port>/index.m3u8"}` → `WatchParent(parent, exit)` → on SIGTERM or job failure: stop job, emit `stopped`/`error`, remove pidfile, exit. Runs until killed.
  - `stop` flow: `TakeOver(out/helper.pid)` and exit 0.

- [ ] **Step 1: Write the failing integration test** (stub ffmpeg that produces a playlist, real HTTP)

```go
// helper/main_test.go
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Builds the helper binary once for integration tests.
func buildHelper(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "airplay-helper")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
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
```

- [ ] **Step 2: Run to verify failure**

Run: `cd helper && go test -run TestServeEndToEnd -v` — Expected: FAIL (no main).

- [ ] **Step 3: Implement**

```go
// helper/main.go
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
		if err := TakeOver(filepath.Join(*out, "helper.pid")); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", os.Args[1])
		os.Exit(2)
	}
}

func fail(msg string) {
	Emit(os.Stdout, "error", map[string]any{"msg": msg})
	os.Exit(1)
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
	fs.Parse(argv)

	if c.Source == "" || c.OutDir == "" || c.FFmpeg == "" || parent == 0 {
		fail("missing required flags: -source, -out, -ffmpeg, -parent")
	}
	if _, err := os.Stat(c.FFmpeg); err != nil {
		fail("ffmpeg not found at " + c.FFmpeg + " — reinstall the plugin through IINA's plugin installer")
	}
	if err := os.MkdirAll(c.OutDir, 0o755); err != nil {
		fail(err.Error())
	}
	pidfile := filepath.Join(c.OutDir, "helper.pid")
	if err := TakeOver(pidfile); err != nil {
		fail("stale instance: " + err.Error())
	}
	for _, pat := range []string{"index.m3u8", "init.mp4", "seg_*.m4s"} {
		matches, _ := filepath.Glob(filepath.Join(c.OutDir, pat))
		for _, m := range matches {
			os.Remove(m)
		}
	}
	if err := WritePidfile(pidfile); err != nil {
		fail(err.Error())
	}
	defer os.Remove(pidfile)

	port, shutdown, err := StartServer(c.OutDir)
	if err != nil {
		fail(err.Error())
	}
	defer shutdown()

	ip, err := LanIP()
	if err != nil {
		fail(err.Error())
	}

	stopJob, done := RunJob(c, os.Stdout)

	WatchParent(parent, func() {
		stopJob()
		os.Remove(pidfile)
		os.Exit(0)
	})
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)

	// The TV can start pulling as soon as the event playlist exists.
	playlist := filepath.Join(c.OutDir, "index.m3u8")
	readyDeadline := time.Now().Add(120 * time.Second)
	readyTick := time.NewTicker(200 * time.Millisecond)
	defer readyTick.Stop()
	ready := false
	for {
		select {
		case <-readyTick.C:
			if !ready {
				if _, err := os.Stat(playlist); err == nil {
					Emit(os.Stdout, "ready", map[string]any{
						"url": fmt.Sprintf("http://%s:%d/index.m3u8", ip, port),
					})
					ready = true
				} else if time.Now().After(readyDeadline) {
					stopJob()
					fail("packaging produced no playlist within 120s")
				}
			}
		case err := <-done:
			if err != nil {
				fail(err.Error())
			}
			if !ready {
				// Job finished before the poll noticed the playlist (tiny file).
				if _, serr := os.Stat(playlist); serr == nil {
					Emit(os.Stdout, "ready", map[string]any{
						"url": fmt.Sprintf("http://%s:%d/index.m3u8", ip, port),
					})
					ready = true
				} else {
					fail("ffmpeg finished but produced no playlist")
				}
			}
			// Keep serving completed VOD until killed.
			<-sigs
			Emit(os.Stdout, "stopped", nil)
			return
		case <-sigs:
			stopJob()
			Emit(os.Stdout, "stopped", nil)
			return
		}
	}
}
```

- [ ] **Step 4: Run to verify pass**

Run: `cd helper && go test ./... -v` — Expected: all PASS (including Task 1–6 suites).

- [ ] **Step 5: Commit**

```bash
git add helper/main.go helper/main_test.go
git commit -m "helper: serve and stop commands wiring server, watchdog, and jobs"
```

---

### Task 8: End-to-end helper test with real ffmpeg

**Files:**
- Create: `helper/e2e_test.go`

**Interfaces:**
- Consumes: the built helper CLI (Task 7 contract). Skips cleanly when no ffmpeg is installed, so CI without ffmpeg stays green.

- [ ] **Step 1: Write the test**

```go
// helper/e2e_test.go
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
```

- [ ] **Step 2: Run it**

Run: `cd helper && go test -run TestRealFFmpegEndToEnd -v` — Expected: PASS locally (SKIP where ffmpeg is absent).

- [ ] **Step 3: Run the whole suite**

Run: `cd helper && go test ./...` — Expected: all PASS.

- [ ] **Step 4: Commit**

```bash
git add helper/e2e_test.go
git commit -m "helper: end-to-end test against real ffmpeg with synthesized fixture"
```

---

### Task 9: JS plugin — Info.json + main.js with node-tested pure functions

**Files:**
- Create: `plugin/Info.json`, `plugin/main.js`
- Test: `plugin/tests/main.test.mjs`

**Interfaces:**
- Consumes: the helper CLI contract (Task 7) and its event names (Task 1).
- Produces (used by Task 10's sidebar page via the message bridge):
  - `onMessage("getState")` → replies `postMessage("state", state)` where `state = { phase: "idle"|"starting"|"ready"|"packaged"|"error", url: string|null, pct: number, msg: string|null }`.
  - Pure exports for node tests: `selectTracks(trackList)` → `{vcodec, acodec, achannels, vmap, amap}` or `null` (no video track); `parseHelperEvents(buffer, chunk)` → `{events: object[], rest: string}` (stdout chunks may split JSON lines arbitrarily).

- [ ] **Step 1: Write the failing node tests**

```js
// plugin/tests/main.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import { createRequire } from "node:module";
const require = createRequire(import.meta.url);
const { selectTracks, parseHelperEvents } = require("../main.js");

const mpvTracks = [
  { type: "video", id: 1, selected: true, codec: "hevc", "ff-index": 0 },
  { type: "audio", id: 1, selected: false, codec: "aac", "ff-index": 1, "demux-channel-count": 2 },
  { type: "audio", id: 2, selected: true, codec: "truehd", "ff-index": 2, "demux-channel-count": 8 },
  { type: "sub",   id: 1, selected: true, codec: "subrip", "ff-index": 3 },
];

test("selectTracks picks selected audio and first video", () => {
  const r = selectTracks(mpvTracks);
  assert.deepEqual(r, { vcodec: "hevc", acodec: "truehd", achannels: 8, vmap: 0, amap: 2 });
});

test("selectTracks falls back to first audio when none selected", () => {
  const tracks = mpvTracks.map(t => ({ ...t, selected: false }));
  const r = selectTracks(tracks);
  assert.equal(r.amap, 1);
  assert.equal(r.acodec, "aac");
});

test("selectTracks returns null without a video track", () => {
  assert.equal(selectTracks(mpvTracks.filter(t => t.type !== "video")), null);
});

test("selectTracks defaults channels to 2 when missing", () => {
  const tracks = [
    { type: "video", id: 1, selected: true, codec: "h264", "ff-index": 0 },
    { type: "audio", id: 1, selected: true, codec: "opus", "ff-index": 1 },
  ];
  assert.equal(selectTracks(tracks).achannels, 2);
});

test("parseHelperEvents handles split and batched lines", () => {
  let r = parseHelperEvents("", '{"event":"progress","pct":1}\n{"event":"re');
  assert.equal(r.events.length, 1);
  assert.equal(r.events[0].pct, 1);
  r = parseHelperEvents(r.rest, 'ady","url":"http://x/index.m3u8"}\n');
  assert.equal(r.events.length, 1);
  assert.equal(r.events[0].event, "ready");
  assert.equal(r.rest, "");
});

test("parseHelperEvents skips non-JSON noise lines", () => {
  const r = parseHelperEvents("", "some stray warning\n{\"event\":\"packaged\"}\n");
  assert.equal(r.events.length, 1);
  assert.equal(r.events[0].event, "packaged");
});
```

- [ ] **Step 2: Run to verify failure**

Run: `node --test plugin/tests/` — Expected: FAIL (main.js missing).

- [ ] **Step 3: Write Info.json**

```json
{
  "name": "AirPlay",
  "identifier": "dev.faruk.iina-airplay",
  "version": "0.1.0",
  "author": { "name": "Faruk Can Ozkan" },
  "description": "Cast the current file to an AirPlay device: remuxes to HLS, serves on the LAN, and hands the stream to the TV. IINA stays your remote.",
  "entry": "main.js",
  "permissions": ["file-system", "show-osd"],
  "sidebarTab": { "name": "AirPlay" }
}
```

- [ ] **Step 4: Write main.js**

The `typeof iina` guard is what lets node require this file for the pure functions without an IINA runtime.

```js
// plugin/main.js — orchestration. HARD RULE (docs/prototype.md): never call
// sidebar.* from utils.exec callbacks/promises; IINA runs those off the main
// thread and sidebar.show() SIGABRTs the app. Sidebar calls live only in the
// menu callback and onMessage handlers; the page polls "getState".

// ---- pure functions (node-testable) ----

function selectTracks(trackList) {
  var video = null, audio = null, firstAudio = null;
  for (var i = 0; i < trackList.length; i++) {
    var t = trackList[i];
    if (t.type === "video" && (!video || t.selected)) video = t;
    if (t.type === "audio") {
      if (firstAudio === null) firstAudio = t;
      if (t.selected) audio = t;
    }
  }
  if (!video) return null;
  if (!audio) audio = firstAudio;
  if (!audio) return null;
  return {
    vcodec: video.codec,
    acodec: audio.codec,
    achannels: audio["demux-channel-count"] || 2,
    vmap: video["ff-index"],
    amap: audio["ff-index"],
  };
}

function parseHelperEvents(buffer, chunk) {
  var data = buffer + chunk;
  var lines = data.split("\n");
  var rest = lines.pop(); // trailing partial line (or "")
  var events = [];
  for (var i = 0; i < lines.length; i++) {
    var line = lines[i].trim();
    if (!line) continue;
    try {
      var obj = JSON.parse(line);
      if (obj && obj.event) events.push(obj);
    } catch (e) { /* stray non-JSON output; ignore */ }
  }
  return { events: events, rest: rest };
}

if (typeof module !== "undefined") {
  module.exports = { selectTracks: selectTracks, parseHelperEvents: parseHelperEvents };
}

// ---- IINA runtime ----

if (typeof iina !== "undefined") {
  var core = iina.core, mpv = iina.mpv, menu = iina.menu, sidebar = iina.sidebar,
      utils = iina.utils, file = iina.file, console = iina.console;

  var state = { phase: "idle", url: null, pct: 0, msg: null };
  var stdoutRest = "";

  // Resolve our own install dir: @data is <plugins-data>/<identifier>; the
  // plugin itself lives in <.../plugins>/<something>. Locate bin/ relative to
  // the data dir's sibling plugins folder at runtime via a glob through /bin/sh
  // once, then cache it in @data so later launches skip the lookup.
  function resolveBinDir(cb) {
    var cached = null;
    try { cached = file.read("@data/bindir.txt"); } catch (e) {}
    if (cached && cached.trim()) { cb(cached.trim()); return; }
    var script =
      'for d in "$HOME/Library/Application Support/com.colliderli.iina/plugins/"*/; do' +
      '  if grep -qs \'"identifier": *"dev.faruk.iina-airplay"\' "$d/Info.json"; then' +
      '    printf %s "$d"bin; exit 0; fi; done; exit 1';
    utils.exec("/bin/sh", ["-c", script]).then(function (r) {
      if (r.status === 0 && r.stdout) {
        file.write("@data/bindir.txt", r.stdout);
        cb(r.stdout);
      } else {
        state = { phase: "error", url: null, pct: 0, msg: "cannot locate plugin bin directory" };
      }
    });
  }

  function startCast() {
    if (state.phase === "starting" || state.phase === "ready") return;
    var src = mpv.getString("path");
    if (!src || src.charAt(0) !== "/") {
      core.osd("AirPlay: only local files can be cast");
      return;
    }
    var tracks = selectTracks(mpv.getNative("track-list") || []);
    if (!tracks) {
      core.osd("AirPlay: no castable video/audio tracks");
      return;
    }
    var duration = mpv.getNumber("duration") || 0;
    var outDir = utils.resolvePath("@tmp/hls");
    state = { phase: "starting", url: null, pct: 0, msg: null };
    core.pause();

    resolveBinDir(function (binDir) {
      var helper = binDir + "/airplay-helper";
      var ffmpeg = binDir + "/ffmpeg";
      utils.exec(helper, [
        "serve",
        "-source", src, "-out", outDir,
        "-ffmpeg", ffmpeg,
        "-parent", String(getIINAPid()),
        "-duration", String(duration),
        "-vcodec", tracks.vcodec, "-acodec", tracks.acodec,
        "-achannels", String(tracks.achannels),
        "-vmap", String(tracks.vmap), "-amap", String(tracks.amap),
      ], undefined, function (chunk) {
        var parsed = parseHelperEvents(stdoutRest, chunk);
        stdoutRest = parsed.rest;
        for (var i = 0; i < parsed.events.length; i++) {
          var ev = parsed.events[i];
          if (ev.event === "ready") { state.phase = "ready"; state.url = ev.url; }
          else if (ev.event === "progress") { state.pct = ev.pct; }
          else if (ev.event === "packaged") { state.phase = state.phase === "ready" ? "packaged" : state.phase; state.pct = 100; }
          else if (ev.event === "error") { state = { phase: "error", url: null, pct: 0, msg: ev.msg }; }
        }
      }, function (errChunk) {
        console.log("[helper:stderr] " + errChunk.trim());
      }).then(function () {
        if (state.phase !== "error") state = { phase: "idle", url: null, pct: 0, msg: null };
      }).catch(function (e) {
        state = { phase: "error", url: null, pct: 0, msg: String(e) };
      });
    });
  }

  function getIINAPid() {
    // mpv exposes the process pid of IINA's mpv core, which lives inside IINA
    // itself (libmpv), so it IS IINA's pid.
    return mpv.getNumber("pid");
  }

  function stopCast() {
    resolveBinDir(function (binDir) {
      utils.exec(binDir + "/airplay-helper", ["stop", "-out", utils.resolvePath("@tmp/hls")]);
    });
    state = { phase: "idle", url: null, pct: 0, msg: null };
  }

  function openSidebar() {
    sidebar.loadFile("sidebar.html"); // clears message listeners — register after
    sidebar.onMessage("getState", function () {
      sidebar.postMessage("state", state); // onMessage handlers are main-thread safe
    });
    sidebar.onMessage("stop", function () {
      stopCast();
      sidebar.postMessage("state", state);
    });
    sidebar.show();
  }

  menu.addItem(menu.item("Cast to TV", function () {
    openSidebar();  // main-thread menu callback: safe
    startCast();    // helper events only mutate `state`; the page polls
  }));
}
```

- [ ] **Step 5: Run node tests to verify pass**

Run: `node --test plugin/tests/` — Expected: all PASS.

- [ ] **Step 6: Verify `mpv.getNumber("pid")` exists**

mpv's `pid` property returns the process id (mpv ≥ 0.32; IINA embeds libmpv so this is IINA's own pid). Verify in IINA's console after Task 10's dev-link: `iina.mpv.getNumber("pid")` should equal `pgrep -x IINA`. If it is unavailable in IINA's mpv build, replace `getIINAPid()` with a `/bin/sh -c 'pgrep -x IINA | head -1'` exec at startup and cache the value — note the outcome in the commit message.

- [ ] **Step 7: Commit**

```bash
git add plugin/Info.json plugin/main.js plugin/tests/main.test.mjs
git commit -m "plugin: main.js orchestration with node-tested track selection and event parsing"
```

---

### Task 10: Sidebar UI + dev loop Makefile + in-IINA verification

**Files:**
- Create: `plugin/sidebar.html`, `Makefile`

**Interfaces:**
- Consumes: `getState`/`state` and `stop` messages (Task 9); the prototype's proven picker pattern (`prototype/iina-airplay-test.iinaplugin-dev/sidebar.html`).

- [ ] **Step 1: Write sidebar.html**

```html
<!doctype html>
<meta charset="utf-8">
<title>AirPlay</title>
<style>
  html, body { margin: 0; background: transparent; color: #e8e8ee;
    font: 13px/1.5 -apple-system, BlinkMacSystemFont, sans-serif; }
  video { width: 1px; height: 1px; opacity: 0.01; position: absolute; }
  #wrap { padding: 14px; }
  button { display: block; width: 100%; padding: 10px; margin: 8px 0;
    font: inherit; font-weight: 600; border-radius: 8px; border: none;
    background: #2f6fed; color: white; cursor: pointer; }
  button:disabled { background: #444; color: #999; }
  #bar { height: 4px; background: #333; border-radius: 2px; margin: 10px 0; }
  #fill { height: 100%; width: 0; background: #2f6fed; border-radius: 2px; }
  #status { min-height: 2.6em; }
  #err { color: #e8b84a; white-space: pre-wrap; font-size: 11px; }
</style>

<div id="wrap">
  <p id="status">Preparing…</p>
  <div id="bar"><div id="fill"></div></div>
  <button id="pick" disabled>Send to TV</button>
  <button id="stop">Stop casting</button>
  <p id="err"></p>
</div>
<video id="v" autoplay playsinline x-webkit-airplay="allow"></video>

<script>
  var v = document.getElementById("v");
  var pick = document.getElementById("pick");
  var statusEl = document.getElementById("status");
  var errEl = document.getElementById("err");
  var fill = document.getElementById("fill");
  var loadedURL = null;
  var retried = false;      // known WebKit flake: first load may fail once
  var airplayAvailable = false;
  var onTV = false;

  function render(s) {
    fill.style.width = (s.pct || 0) + "%";
    if (s.phase === "error") { statusEl.textContent = "Failed."; errEl.textContent = s.msg || ""; return; }
    errEl.textContent = "";
    if (onTV) { statusEl.textContent = "Playing on TV."; return; }
    if (s.phase === "idle") statusEl.textContent = "Not casting.";
    else if (s.phase === "starting") statusEl.textContent = "Packaging…";
    else if (s.phase === "ready" || s.phase === "packaged") {
      statusEl.textContent = airplayAvailable ? "Ready — click Send to TV."
                                              : "Ready — waiting for AirPlay devices…";
    }
    if ((s.phase === "ready" || s.phase === "packaged") && s.url && s.url !== loadedURL) {
      loadedURL = s.url;
      retried = false;
      v.src = s.url;
      v.play().catch(function () {});
    }
  }

  v.addEventListener("error", function () {
    if (!retried && loadedURL) {           // retry exactly once (docs/prototype.md)
      retried = true;
      v.src = loadedURL;
      v.play().catch(function () {});
    } else {
      errEl.textContent = "Stream failed to load (media error " +
        (v.error ? v.error.code : "?") + ").";
    }
  });
  v.addEventListener("webkitplaybacktargetavailabilitychanged", function (ev) {
    airplayAvailable = ev.availability === "available";
    pick.disabled = !airplayAvailable;
  });
  v.addEventListener("webkitcurrentplaybacktargetiswirelesschanged", function () {
    onTV = !!v.webkitCurrentPlaybackTargetIsWireless;
  });
  pick.onclick = function () {
    if (v.webkitShowPlaybackTargetPicker) v.webkitShowPlaybackTargetPicker();
  };
  document.getElementById("stop").onclick = function () {
    v.pause(); v.removeAttribute("src"); v.load();
    loadedURL = null; onTV = false;
    iina.postMessage("stop", {});
  };
  iina.onMessage("state", render);
  setInterval(function () { iina.postMessage("getState", {}); }, 500);
  iina.postMessage("getState", {});
</script>
```

- [ ] **Step 2: Write the Makefile**

```makefile
IINA_PLUGIN := /Applications/IINA.app/Contents/MacOS/iina-plugin
DEVLINK := build/iina-airplay.iinaplugin-dev

.PHONY: helper test dev clean

helper:
	cd helper && go build -o ../plugin/bin/airplay-helper .

test:
	cd helper && go test ./...
	node --test plugin/tests/

# Dev loop: helper from source, brew ffmpeg standing in for the bundled one,
# plugin symlinked into IINA. Restart IINA to pick up JS changes.
dev: helper
	ln -sf /opt/homebrew/bin/ffmpeg plugin/bin/ffmpeg
	mkdir -p build
	ln -sfn ../plugin $(DEVLINK)
	$(IINA_PLUGIN) link $(DEVLINK)

clean:
	rm -rf plugin/bin build
```

- [ ] **Step 3: Run the full automated suite**

Run: `make test` — Expected: Go suite and node suite all PASS.

- [ ] **Step 4: Set up the dev link and verify in IINA**

Run: `make dev`, restart IINA, enable the "AirPlay" plugin in Settings → Plugins if listed as disabled. Then with a local file playing, use menu → Plugins → AirPlay → **Cast to TV** and verify in order:
1. Sidebar tab "AirPlay" opens inside the player window (no crash — this exercises the main-thread rule).
2. Status goes "Packaging…" → "Ready", progress bar moves, and for a plain H.264/AAC file "Ready" appears within ~2 s.
3. "Send to TV" enables once availability fires.
4. Plugin console (Settings → Plugins → AirPlay → console) shows no `[helper:stderr]` noise beyond ffmpeg warnings.
5. Quit IINA; confirm no orphan: `lsof -nP -iTCP -sTCP:LISTEN | grep airplay-helper` is empty (the watchdog worked — this was a prototype bug).

- [ ] **Step 5: Commit**

```bash
git add plugin/sidebar.html Makefile
git commit -m "plugin: sidebar cast UI and dev-loop Makefile"
```

---

### Task 11: Human acceptance test + contributor README

**Files:**
- Modify: `README.md`

**Interfaces:**
- Consumes: everything. This is the gate before the release-pipeline plan.

- [ ] **Step 1: Human acceptance run (requires the user at the TV)**

With `make dev` active and a real file playing in IINA: Cast to TV → Send to TV → pick the device. **PASS = video and audio play on the TV, progress advances, Stop casting returns cleanly, and re-casting a second file works without restarting IINA.** Do not claim this passed without the user confirming (CLAUDE.md rule). Record the file's codecs and which transcode branch it took.

- [ ] **Step 2: Update README.md**

Replace the research-era README body with: what the plugin does (one paragraph, handoff-not-mirror per docs/feasibility.md), current status (v0 core works from source; packaged releases pending the release-pipeline plan), dev quickstart (`brew install ffmpeg go node`, `make dev`, restart IINA), test commands (`make test`), and pointers to `docs/feasibility.md`, `docs/prototype.md`, `docs/distribution.md`. Keep the existing docs' terse voice; no marketing.

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "docs: v0 status and contributor quickstart"
```

---

## Out of scope for this plan (next plan: release pipeline)

Pinned LGPL static ffmpeg build in CI, universal (lipo) binaries, `iina-plugin pack`, GitHub release with `.iinaplgz` asset + SHA-256s, `ghRepo`/`ghVersion` update wiring, and the install-from-local-package documentation for GitHub-blocked users — all per `docs/distribution.md`.
