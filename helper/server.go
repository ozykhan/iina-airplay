package main

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
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
// labeled with the real track name/language, and given a variant line
// (EnsureVariant, advertising bandwidth) while ffmpeg has not yet written
// one. That variant line then gets CODECS and VIDEO-RANGE from init.mp4,
// and for HDR a FRAME-RATE from the first segment (AddVariantAttrs,
// VariantAttrs; rangeHint stands in for a missing colr box): without them an
// HDR stream is announced as SDR, which AVFoundation fails (issue #45).
// Everything else is a plain file serve. subName/subLang/rangeHint may be
// empty (no-subs casts never produce a master.m3u8, so the rewrite path
// simply never runs for them).
func StartServer(dir, subName, subLang string, bandwidth int, rangeHint string) (int, func(), error) {
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		return 0, nil, err
	}
	files := http.FileServer(http.Dir(dir))
	var warnedFrameRate atomic.Bool
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/master.m3u8" {
			if b, rerr := os.ReadFile(filepath.Join(dir, "master.m3u8")); rerr == nil {
				w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
				master := EnsureVariant(RewriteMasterPlaylist(string(b), subName, subLang), bandwidth)
				v := VariantAttrs(dir, rangeHint)
				if v.VideoRange != "" && v.FrameRate == "" && warnedFrameRate.CompareAndSwap(false, true) {
					// The TV will reject this master; say why in the plugin log.
					fmt.Fprintf(os.Stderr, "HDR variant (%s) has no FRAME-RATE: could not read the frame rate from %s\n", v.VideoRange, firstSegment)
				}
				io.WriteString(w, AddVariantAttrs(master, v))
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
