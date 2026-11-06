package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Lines lifted from a real fansub script (the sign scene that froze a cast).
const fansubASS = `[Script Info]
ScriptType: v4.00+

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
Dialogue: 10,0:17:22.37,0:17:28.94,Default,,0,0,0,,reaching that goal hinges on lower-scorers like me working hard.
Dialogue: 1,0:17:28.53,0:17:28.57,Sign #3,,0,0,0,,{=137}{\bord3\pos(1076.865,410.46)}Okano Hinata\N108th out of 186
Dialogue: 0,0:17:28.53,0:17:28.57,Sign #3,,0,0,0,,{=135}{\an7\pos(729.825,279)\p1}m 171 58.5 l 520.5 58.5 520.5 210 171 210
Dialogue: 1,0:17:19.14,0:17:22.10,Sign #3,,0,0,0,,{=2}{\bord3\pos(443,834)}Fake Ritsu-san
Dialogue: 2,0:17:19.14,0:17:22.10,Sign #3,,0,0,0,,{=3}{\bord0\pos(443,834)}Fake Ritsu-san
Dialogue: 0,0:17:30.00,0:17:31.00,Default,,0,0,0,,{\an8}
Dialogue: 0,0:17:31.37,0:17:32.85,Default,,0,0,0,,{\i1}We're just gonna, have to go for it!{\i0}
`

func TestCleanASS(t *testing.T) {
	got := CleanASS(fansubASS)
	var dialogue []string
	for _, l := range strings.Split(got, "\n") {
		if strings.HasPrefix(l, "Dialogue:") {
			dialogue = append(dialogue, l)
		}
	}
	want := []string{
		"Dialogue: 10,0:17:22.37,0:17:28.94,Default,,0,0,0,,reaching that goal hinges on lower-scorers like me working hard.",
		"Dialogue: 1,0:17:19.14,0:17:22.10,Sign #3,,0,0,0,,{\\bord3\\pos(443,834)}Fake Ritsu-san",
		"Dialogue: 0,0:17:31.37,0:17:32.85,Default,,0,0,0,,{\\i1}We're just gonna, have to go for it!{\\i0}",
	}
	if strings.Join(dialogue, "\n") != strings.Join(want, "\n") {
		t.Fatalf("dialogue:\n%s\nwant:\n%s", strings.Join(dialogue, "\n"), strings.Join(want, "\n"))
	}
	if !strings.Contains(got, "[Script Info]") || !strings.Contains(got, "Format: Layer") {
		t.Fatal("non-Dialogue lines must pass through")
	}
}

func TestPrepareASSExternal(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.ass")
	os.WriteFile(src, []byte(fansubASS), 0o644)
	c, err := PrepareASS(JobConfig{OutDir: dir, SubPath: src, SubCodec: "ass"})
	if err != nil || c.SubPath != filepath.Join(dir, cleanSubName) {
		t.Fatalf("SubPath %q err %v", c.SubPath, err)
	}
	// Anything that isn't ASS is left alone.
	c, _ = PrepareASS(JobConfig{OutDir: dir, SubMap: "2", SubCodec: "subrip"})
	if c.SubMap != "2" || c.SubPath != "" {
		t.Fatalf("subrip job changed: %+v", c)
	}
}
