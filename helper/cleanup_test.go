package main

import (
	"os"
	"path/filepath"
	"testing"
)

// cleanOutDir must remove everything a cast writes — playlists, the fMP4
// init segment, media segments, the WebVTT rendition — and nothing else:
// the pidfile belongs to the process lifecycle, and a stray file the user
// dropped in the directory is not ours to touch.
func TestCleanOutDirRemovesCastArtifactsOnly(t *testing.T) {
	dir := t.TempDir()
	artifacts := []string{"index.m3u8", "master.m3u8", "index_vtt.m3u8", "init.mp4", "seg_0000.m4s", "seg_0001.m4s", "seg_0000.vtt"}
	keep := []string{"helper.pid", "notes.txt"}
	for _, f := range append(append([]string{}, artifacts...), keep...) {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cleanOutDir(dir)
	for _, f := range artifacts {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			t.Errorf("%s survived the sweep", f)
		}
	}
	for _, f := range keep {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("%s was removed but is not a cast artifact", f)
		}
	}
}

func TestCleanOutDirMissingDirIsNoop(t *testing.T) {
	cleanOutDir(filepath.Join(t.TempDir(), "never-created")) // must not panic
}
