package main

import (
	"os"
	"path/filepath"
)

// castArtifacts are the files a cast writes into the output directory:
// media and subtitle playlists, the fMP4 init segment, media segments and
// WebVTT segments. The pidfile is deliberately not here — it belongs to
// the process lifecycle, not the cast.
var castArtifacts = []string{"index.m3u8", "master.m3u8", "*_vtt.m3u8", "*.vtt", "init.mp4", "seg_*.m4s"}

// cleanOutDir removes every cast artifact from dir. A remux is a second
// copy of the source's bitstreams, so this runs on every exit path, not
// just at the next start (issue #12). Missing files and a missing dir are
// fine; errors are ignored because there is nothing useful to do with them
// on the way out.
func cleanOutDir(dir string) {
	for _, pat := range castArtifacts {
		matches, _ := filepath.Glob(filepath.Join(dir, pat))
		for _, m := range matches {
			os.Remove(m)
		}
	}
}
