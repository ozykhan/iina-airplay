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
