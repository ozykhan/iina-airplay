package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServerMIMEAndRange(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.m3u8"), []byte("#EXTM3U\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "seg_0000.m4s"), []byte("0123456789"), 0o644)

	port, shutdown, err := StartServer(dir, "", "", 0)
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

func TestServerRewritesMasterPlaylist(t *testing.T) {
	dir := t.TempDir()
	master := "#EXTM3U\n#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID=\"default\",NAME=\"subtitle_0\",DEFAULT=NO,AUTOSELECT=YES,URI=\"index_vtt.m3u8\"\nindex.m3u8\n"
	os.WriteFile(filepath.Join(dir, "master.m3u8"), []byte(master), 0o644)
	os.WriteFile(filepath.Join(dir, "seg_0000.vtt"), []byte("WEBVTT\n"), 0o644)

	port, shutdown, err := StartServer(dir, "English", "en", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer shutdown()
	base := fmt.Sprintf("http://127.0.0.1:%d", port)

	resp, err := http.Get(base + "/master.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "application/vnd.apple.mpegurl" {
		t.Fatalf("master content-type = %q", ct)
	}
	s := string(body)
	if !strings.Contains(s, "DEFAULT=YES") || !strings.Contains(s, `NAME="English"`) || !strings.Contains(s, `LANGUAGE="en"`) {
		t.Fatalf("master not rewritten:\n%s", s)
	}

	resp2, err := http.Get(base + "/seg_0000.vtt")
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if ct := resp2.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/vtt") {
		t.Fatalf("vtt content-type = %q", ct)
	}
}

func TestServerMissingMasterFallsThrough(t *testing.T) {
	dir := t.TempDir()
	port, shutdown, err := StartServer(dir, "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer shutdown()
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/master.m3u8", port))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing master should 404, got %d", resp.StatusCode)
	}
}

// Issue #26: while ffmpeg is still packaging a stream copy, its master.m3u8
// names the subtitle rendition but no variant, and AVFoundation (WebKit's
// <video>, the Apple TV) rejects it outright. The served master must carry a
// variant from the first request.
func TestServerAddsVariantToIncompleteMaster(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "master.m3u8"), []byte(ffmpegMasterNoVariant), 0o644)

	port, shutdown, err := StartServer(dir, "English", "en", 7_000_000)
	if err != nil {
		t.Fatal(err)
	}
	defer shutdown()

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/master.m3u8", port))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	s := string(body)
	if !strings.Contains(s, "#EXT-X-STREAM-INF:BANDWIDTH=7000000,SUBTITLES=\"subs\"\nindex.m3u8\n") {
		t.Fatalf("served master has no variant:\n%s", s)
	}
	if !strings.Contains(s, `NAME="English"`) {
		t.Fatalf("subtitle rewrite must still apply:\n%s", s)
	}
}
