package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRequestLogRecordsWhatTheServerAnswered(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "seg_0001.m4s"), []byte(strings.Repeat("x", 3000)), 0o644)
	reqs := NewRequestLog()
	port, shutdown, err := StartServer(dir, "", "", 0, "", reqs)
	if err != nil {
		t.Fatal(err)
	}
	defer shutdown()
	base := fmt.Sprintf("http://127.0.0.1:%d", port)

	get := func(path, rng string) {
		req, _ := http.NewRequest("GET", base+path, nil)
		if rng != "" {
			req.Header.Set("Range", rng)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	get("/seg_0001.m4s", "")
	get("/seg_0001.m4s", "bytes=0-99")
	get("/seg_0002.m4s", "") // not packaged: a 404 is what a stall report must show

	dump := reqs.Dump(time.Now())
	lines := strings.Split(strings.TrimSpace(dump), "\n")
	if len(lines) != 4 || lines[0] != "last 3 requests, newest first:" {
		t.Fatalf("dump:\n%s", dump)
	}
	for i, want := range []string{
		"GET /seg_0002.m4s  404",
		"GET /seg_0001.m4s  206, 100 B in",
		"GET /seg_0001.m4s  200, 2.9 KB in",
	} {
		if !strings.Contains(lines[i+1], want) || !strings.HasSuffix(lines[i+1], "from 127.0.0.1") {
			t.Errorf("line %d = %q, want it to contain %q and name the client", i+1, lines[i+1], want)
		}
	}
}

func TestRequestLogShowsUnfinishedRequests(t *testing.T) {
	reqs := NewRequestLog()
	release := make(chan struct{})
	started := make(chan struct{})
	h := reqs.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
	}))
	go h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/seg_0193.m4s", nil))
	<-started
	if dump := reqs.Dump(time.Now()); !strings.Contains(dump, "GET /seg_0193.m4s  unfinished") {
		t.Fatalf("a transfer that never ends must show as unfinished, got:\n%s", dump)
	}
	close(release)
}

func TestRequestLogKeepsTheNewest(t *testing.T) {
	reqs := NewRequestLog()
	h := reqs.Wrap(http.NotFoundHandler())
	for i := 0; i < requestLogSize+5; i++ {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", fmt.Sprintf("/seg_%04d.m4s", i), nil))
	}
	lines := strings.Split(strings.TrimSpace(reqs.Dump(time.Now())), "\n")[1:]
	if len(lines) != requestLogSize {
		t.Fatalf("kept %d entries, want %d", len(lines), requestLogSize)
	}
	newest := fmt.Sprintf("/seg_%04d.m4s", requestLogSize+4)
	oldest := fmt.Sprintf("/seg_%04d.m4s", 5)
	if !strings.Contains(lines[0], newest) || !strings.Contains(lines[len(lines)-1], oldest) {
		t.Fatalf("want %s first and %s last, got:\n%s", newest, oldest, strings.Join(lines, "\n"))
	}
}

func TestRequestLogEmpty(t *testing.T) {
	if got := NewRequestLog().Dump(time.Now()); got != "no requests yet\n" {
		t.Fatalf("got %q", got)
	}
	var nilLog *RequestLog
	h := http.NotFoundHandler()
	if nilLog.Wrap(h) == nil {
		t.Fatal("a nil log must pass the handler through")
	}
}
