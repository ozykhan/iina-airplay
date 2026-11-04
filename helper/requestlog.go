package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// requestLogSize is how many of the most recent requests RequestLog keeps.
// A stall report needs the last few segment fetches, not a history.
const requestLogSize = 12

// RequestLog remembers the server's most recent requests, unfinished ones
// included. When the TV freezes mid-cast, the picture alone can't tell a TV
// that stopped asking for segments from one that is still asking, getting
// errors, or waiting on a transfer that never finishes (issue #58). The
// plugin sends SIGUSR1 when the TV has been stuck long enough to reload it,
// and the helper prints this log to stderr, which lands in IINA's log. It
// logs nothing per request, so a normal cast adds no log lines.
type RequestLog struct {
	mu   sync.Mutex
	ring []*reqEntry
	next int // where the next entry goes once the ring is full
}

type reqEntry struct {
	start  time.Time
	done   bool
	dur    time.Duration
	remote string
	method string
	path   string
	status int
	bytes  int64
}

func NewRequestLog() *RequestLog {
	return &RequestLog{ring: make([]*reqEntry, 0, requestLogSize)}
}

func (l *RequestLog) begin(r *http.Request, now time.Time) *reqEntry {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	e := &reqEntry{start: now, remote: host, method: r.Method, path: r.URL.Path}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.ring) < requestLogSize {
		l.ring = append(l.ring, e)
	} else {
		l.ring[l.next] = e
	}
	l.next = (l.next + 1) % requestLogSize
	return e
}

func (l *RequestLog) finish(e *reqEntry, status int, n int64, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e.done, e.dur, e.status, e.bytes = true, now.Sub(e.start), status, n
}

// Wrap records every request h serves. A nil log records nothing.
func (l *RequestLog) Wrap(h http.Handler) http.Handler {
	if l == nil {
		return h
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e := l.begin(r, time.Now())
		cw := &countingWriter{ResponseWriter: w}
		defer func() { l.finish(e, cw.statusCode(), cw.n, time.Now()) }()
		h.ServeHTTP(cw, r)
	})
}

// Dump renders the log newest first, with each request's age relative to now.
func (l *RequestLog) Dump(now time.Time) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := len(l.ring)
	if n == 0 {
		return "no requests yet\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "last %d requests, newest first:\n", n)
	for i := 0; i < n; i++ {
		e := l.ring[((l.next-1-i)%n+n)%n]
		fmt.Fprintf(&b, "  %5.1fs ago  %s %s  ", now.Sub(e.start).Seconds(), e.method, e.path)
		if e.done {
			fmt.Fprintf(&b, "%d, %s in %.2fs", e.status, humanBytes(e.bytes), e.dur.Seconds())
		} else {
			b.WriteString("unfinished")
		}
		fmt.Fprintf(&b, "  from %s\n", e.remote)
	}
	return b.String()
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// countingWriter records the status and body size of a response.
type countingWriter struct {
	http.ResponseWriter
	status int
	n      int64
}

func (c *countingWriter) WriteHeader(code int) {
	if c.status == 0 {
		c.status = code
	}
	c.ResponseWriter.WriteHeader(code)
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.ResponseWriter.Write(p)
	c.n += int64(n)
	return n, err
}

// ReadFrom keeps http.FileServer's sendfile path: io.Copy into a plain
// wrapper would copy every segment through a userspace buffer.
func (c *countingWriter) ReadFrom(r io.Reader) (int64, error) {
	if rf, ok := c.ResponseWriter.(io.ReaderFrom); ok {
		n, err := rf.ReadFrom(r)
		c.n += n
		return n, err
	}
	return io.Copy(struct{ io.Writer }{c}, r)
}

func (c *countingWriter) Unwrap() http.ResponseWriter { return c.ResponseWriter }

// statusCode is what the client got: an untouched response is a 200.
func (c *countingWriter) statusCode() int {
	if c.status == 0 {
		return http.StatusOK
	}
	return c.status
}
