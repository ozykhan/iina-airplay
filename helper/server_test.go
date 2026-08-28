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
