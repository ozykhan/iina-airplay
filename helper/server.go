package main

import (
	"context"
	"io"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Safari refuses HLS served as application/octet-stream, and Go's built-in
// table doesn't know these extensions.
func init() {
	mime.AddExtensionType(".m3u8", "application/vnd.apple.mpegurl")
	mime.AddExtensionType(".m4s", "video/iso.segment")
	mime.AddExtensionType(".mp4", "video/mp4")
	mime.AddExtensionType(".vtt", "text/vtt")
}

// StartServer serves dir. master.m3u8 is rewritten on the way out
// (RewriteMasterPlaylist) so the subtitle rendition is on by default and
// labeled with the real track name/language; everything else is a plain
// file serve. subName/subLang may be empty (no-subs casts never produce a
// master.m3u8, so the rewrite path simply never runs for them).
func StartServer(dir, subName, subLang string) (int, func(), error) {
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		return 0, nil, err
	}
	files := http.FileServer(http.Dir(dir))
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/master.m3u8" {
			if b, rerr := os.ReadFile(filepath.Join(dir, "master.m3u8")); rerr == nil {
				w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
				io.WriteString(w, RewriteMasterPlaylist(string(b), subName, subLang))
				return
			}
		}
		files.ServeHTTP(w, r)
	})
	srv := &http.Server{Handler: handler}
	go srv.Serve(ln)
	shutdown := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}
	return ln.Addr().(*net.TCPAddr).Port, shutdown, nil
}
