# Subtitle Support Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The subtitle track selected in IINA at cast time appears on the Apple TV as a WebVTT rendition in the HLS stream (text subs only; image subs are dropped with an OSD notice).

**Architecture:** Approach A from the spec (`docs/superpowers/specs/2026-08-29-subtitles-design.md`): the single existing ffmpeg job additionally maps the subtitle stream with `-c:s webvtt` and `-master_pl_name master.m3u8`; the helper advertises the master playlist and rewrites its subtitle rendition line at serve time (ffmpeg hardcodes `DEFAULT=NO` and a generic name). The plugin's `selectTracks` grows subtitle awareness and `startCast` passes four new helper flags.

**Tech Stack:** Go (helper, stdlib only), plain ES5-compatible JS (plugin — IINA's JSContext), `node --test` for plugin tests, `go test` for helper tests, real ffmpeg for the e2e test.

## Global Constraints

- **No-subs path must be byte-identical to today.** With no subtitle configured, `BuildArgs` output, the advertised URL (`.../index.m3u8`), and server behavior must not change.
- Subtitle optionality is encoded as **zero values**: `SubPath == "" && SubMap == ""` means no subtitles. `SubMap` is a **string** (the ff-index digits) precisely so the Go zero value can't be mistaken for stream 0.
- Plugin JS is ES5-style (`var`, no arrow functions, no template literals) in `plugin/main.js` — match the existing file. Test files (`.mjs`) use modern JS.
- Never call `sidebar.*` from `utils.exec` callbacks (SIGABRT — see main.js header comment). This plan's changes don't touch sidebar calls; keep it that way.
- Text sub codecs (castable): `subrip`, `srt`, `ass`, `ssa`, `mov_text`, `webvtt`, `text`. Everything else (incl. `hdmv_pgs_subtitle`, `dvd_subtitle`, unknown codecs) is dropped with an OSD notice.
- Helper flag names: `-smap` (embedded stream ff-index, string, empty = none), `-subpath` (external file, overrides `-smap`), `-sublang`, `-subname`.
- Commit after every task with the repo's existing style (`plugin:` / `helper:` / `docs:` prefixes) and the `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>` trailer.
- Run `gofmt -w` on every touched Go file before committing (a prior commit fixed unformatted Go — don't repeat that).

---

### Task 1: `selectTracks` subtitle awareness (plugin, pure function)

**Files:**
- Modify: `plugin/main.js:8-28` (the `selectTracks` function; add a codec table above it)
- Test: `plugin/tests/main.test.mjs`

**Interfaces:**
- Consumes: mpv track-list entries (`type`, `selected`, `codec`, `ff-index`, `external`, `external-filename`, `lang`, `title`).
- Produces: `selectTracks` result gains `sub` (object or `null`) and `subDropped` (bool). `sub` shape: `{ path: string|null, smap: number, lang: string, title: string }` — `path` non-null only for external tracks. Task 2 consumes exactly this shape.

- [ ] **Step 1: Write the failing tests**

In `plugin/tests/main.test.mjs`, first update the existing `deepEqual` test (the result grows two fields):

```js
test("selectTracks picks selected audio and first video", () => {
  const r = selectTracks(mpvTracks);
  assert.deepEqual(r, {
    vcodec: "hevc", acodec: "truehd", achannels: 8, vmap: 0, amap: 2,
    sub: { path: null, smap: 3, lang: "", title: "" }, subDropped: false,
  });
});
```

Then append new tests:

```js
test("selectTracks ignores unselected sub tracks", () => {
  const tracks = mpvTracks.map(t => (t.type === "sub" ? { ...t, selected: false } : t));
  const r = selectTracks(tracks);
  assert.equal(r.sub, null);
  assert.equal(r.subDropped, false);
});

test("selectTracks carries lang and title of the selected text sub", () => {
  const tracks = mpvTracks.map(t =>
    t.type === "sub" ? { ...t, lang: "en", title: "English (SDH)" } : t);
  assert.deepEqual(selectTracks(tracks).sub,
    { path: null, smap: 3, lang: "en", title: "English (SDH)" });
});

test("selectTracks uses the external file path for external subs", () => {
  const tracks = mpvTracks.map(t =>
    t.type === "sub" ? { ...t, external: true, "external-filename": "/subs/en.srt" } : t);
  const r = selectTracks(tracks);
  assert.equal(r.sub.path, "/subs/en.srt");
  assert.equal(r.subDropped, false);
});

test("selectTracks drops an external sub with no usable path", () => {
  const tracks = mpvTracks.map(t =>
    t.type === "sub" ? { ...t, external: true } : t);
  const r = selectTracks(tracks);
  assert.equal(r.sub, null);
  assert.equal(r.subDropped, true);
});

test("selectTracks drops image subs (PGS) and flags it", () => {
  const tracks = mpvTracks.map(t =>
    t.type === "sub" ? { ...t, codec: "hdmv_pgs_subtitle" } : t);
  const r = selectTracks(tracks);
  assert.equal(r.sub, null);
  assert.equal(r.subDropped, true);
});

test("selectTracks drops unknown sub codecs", () => {
  const tracks = mpvTracks.map(t =>
    t.type === "sub" ? { ...t, codec: "some_future_codec" } : t);
  assert.equal(selectTracks(tracks).subDropped, true);
});
```

- [ ] **Step 2: Run tests to verify the new ones fail**

Run: `node --test plugin/tests/main.test.mjs`
Expected: the updated `deepEqual` test and the new sub tests FAIL (result has no `sub`/`subDropped` fields); all other tests still pass.

- [ ] **Step 3: Implement**

In `plugin/main.js`, add above `selectTracks`:

```js
// Sub codecs ffmpeg can convert to WebVTT. Image codecs (hdmv_pgs_subtitle,
// dvd_subtitle) and anything unknown can't pass through to the TV without a
// burn-in re-encode — out of scope, see docs/superpowers/specs/.
var TEXT_SUB_CODECS = { subrip: 1, srt: 1, ass: 1, ssa: 1, mov_text: 1, webvtt: 1, text: 1 };
```

Replace `selectTracks` with:

```js
function selectTracks(trackList) {
  var video = null, audio = null, firstAudio = null, sub = null;
  for (var i = 0; i < trackList.length; i++) {
    var t = trackList[i];
    if (t.type === "video" && (!video || t.selected)) video = t;
    if (t.type === "audio") {
      if (firstAudio === null) firstAudio = t;
      if (t.selected) audio = t;
    }
    if (t.type === "sub" && t.selected) sub = t;
  }
  if (!video) return null;
  if (!audio) audio = firstAudio;
  if (!audio) return null;
  var r = {
    vcodec: video.codec,
    acodec: audio.codec,
    achannels: audio["demux-channel-count"] || 2,
    vmap: video["ff-index"],
    amap: audio["ff-index"],
    sub: null,
    subDropped: false,
  };
  if (sub) {
    var path = sub.external ? (sub["external-filename"] || null) : null;
    if (!TEXT_SUB_CODECS[sub.codec] || (sub.external && !path)) {
      r.subDropped = true;
    } else {
      r.sub = { path: path, smap: sub["ff-index"], lang: sub.lang || "", title: sub.title || "" };
    }
  }
  return r;
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `node --test plugin/tests/main.test.mjs`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add plugin/main.js plugin/tests/main.test.mjs
git commit -m "plugin: selectTracks picks the selected text subtitle track"
```

---

### Task 2: `startCast` passes subtitle flags and OSDs on drop (plugin runtime)

**Files:**
- Modify: `plugin/main.js:126-191` (`startCast`)
- Test: `plugin/tests/runtime.test.mjs`

**Interfaces:**
- Consumes: `tracks.sub` / `tracks.subDropped` from Task 1.
- Produces: helper argv gains, when a castable sub exists: `-subpath <path>` (external) **or** `-smap <n>` (embedded), always followed by `-sublang <lang> -subname <title>`. Task 6 registers these flags on the helper.

- [ ] **Step 1: Parameterize the runtime test harness**

In `plugin/tests/runtime.test.mjs`, let `loadPlugin` accept a track list. Change the signature and the `getNative` line:

```js
function loadPlugin(opts = {}) {
  const trackList = opts.tracks || [
    { type: "video", selected: true, codec: "hevc", "ff-index": 0 },
    { type: "audio", selected: true, codec: "aac", "ff-index": 1, "demux-channel-count": 2 },
  ];
```

and inside the fake `mpv`:

```js
      getNative: () => trackList,
```

- [ ] **Step 2: Write the failing tests**

Append to `plugin/tests/runtime.test.mjs`:

```js
const baseTracks = [
  { type: "video", selected: true, codec: "hevc", "ff-index": 0 },
  { type: "audio", selected: true, codec: "aac", "ff-index": 1, "demux-channel-count": 2 },
];

test("serve args carry -smap/-sublang/-subname for an embedded text sub", () => {
  const p = loadPlugin({ tracks: [...baseTracks,
    { type: "sub", selected: true, codec: "subrip", "ff-index": 2, lang: "en", title: "English" }] });
  p.clickMenu();
  const args = serves(p)[0].args;
  const i = args.indexOf("-smap");
  assert.notEqual(i, -1);
  assert.equal(args[i + 1], "2");
  assert.equal(args[args.indexOf("-sublang") + 1], "en");
  assert.equal(args[args.indexOf("-subname") + 1], "English");
  assert.equal(args.indexOf("-subpath"), -1);
});

test("serve args carry -subpath for an external sub, not -smap", () => {
  const p = loadPlugin({ tracks: [...baseTracks,
    { type: "sub", selected: true, codec: "subrip", "ff-index": 2,
      external: true, "external-filename": "/subs/en.srt", lang: "en" }] });
  p.clickMenu();
  const args = serves(p)[0].args;
  assert.equal(args[args.indexOf("-subpath") + 1], "/subs/en.srt");
  assert.equal(args.indexOf("-smap"), -1);
});

test("image subs cast without subs and OSD the reason", () => {
  const p = loadPlugin({ tracks: [...baseTracks,
    { type: "sub", selected: true, codec: "hdmv_pgs_subtitle", "ff-index": 2 }] });
  p.clickMenu();
  assert.equal(serves(p).length, 1, "cast must still start");
  const args = serves(p)[0].args;
  assert.equal(args.indexOf("-smap"), -1);
  assert.equal(args.indexOf("-subpath"), -1);
  assert.ok(p.osd.some(m => /subtitle/i.test(m)), "expected an OSD about dropped subtitles");
});

test("no sub flags when no sub is selected", () => {
  const p = loadPlugin();
  p.clickMenu();
  const args = serves(p)[0].args;
  assert.equal(args.indexOf("-smap"), -1);
  assert.equal(args.indexOf("-subpath"), -1);
  assert.equal(args.indexOf("-sublang"), -1);
});
```

- [ ] **Step 3: Run tests to verify the new ones fail**

Run: `node --test plugin/tests/runtime.test.mjs`
Expected: the three sub-flag tests FAIL (flags absent / no OSD); pre-existing tests still pass.

- [ ] **Step 4: Implement in `startCast`**

In `plugin/main.js` `startCast`, right after the `if (!tracks) { ... return; }` block, add:

```js
    if (tracks.subDropped) {
      core.osd("AirPlay: selected subtitles can't be cast (image-based or unsupported); casting without them");
    }
```

Then replace the inline argv literal in the `utils.exec(helper, [...])` call with a built-up array. Before `utils.exec`, add:

```js
      var serveArgs = [
        "serve",
        "-source", src, "-out", outDir,
        "-ffmpeg", ffmpeg,
        "-parent", String(pid),
        "-duration", String(duration),
        "-vcodec", tracks.vcodec, "-acodec", tracks.acodec,
        "-achannels", String(tracks.achannels),
        "-vmap", String(tracks.vmap), "-amap", String(tracks.amap),
      ];
      if (tracks.sub) {
        if (tracks.sub.path) serveArgs.push("-subpath", tracks.sub.path);
        else serveArgs.push("-smap", String(tracks.sub.smap));
        serveArgs.push("-sublang", tracks.sub.lang, "-subname", tracks.sub.title);
      }
```

and change the call to `utils.exec(helper, serveArgs, undefined, function (chunk) {`.

- [ ] **Step 5: Run all plugin tests**

Run: `node --test plugin/tests/`
Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add plugin/main.js plugin/tests/runtime.test.mjs
git commit -m "plugin: pass selected subtitle track to the helper, OSD when dropped"
```

---

### Task 3: `JobConfig` subtitle fields and `BuildArgs` mapping (helper)

**Files:**
- Modify: `helper/job.go:15-63` (`JobConfig`, `BuildArgs`; add `HasSub`/`PlaylistName` methods)
- Test: `helper/job_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: `JobConfig` fields `SubPath string`, `SubMap string`, `SubLang string`, `SubName string`; methods `func (c JobConfig) HasSub() bool` and `func (c JobConfig) PlaylistName() string` (returns `"master.m3u8"` or `"index.m3u8"`). Tasks 5 and 6 consume all of these.

- [ ] **Step 1: Write the failing tests**

Append to `helper/job_test.go`:

```go
func TestBuildArgsNoSubByteIdentical(t *testing.T) {
	// Pin the exact no-subs command line: subtitle support must not disturb
	// the proven recipe when no sub is configured (spec §2).
	got := strings.Join(BuildArgs(baseCfg()), " ")
	want := strings.Join([]string{
		"-hide_banner", "-nostats", "-loglevel", "warning", "-y",
		"-progress", "pipe:1",
		"-i", "/movies/a.mkv",
		"-map", "0:0", "-map", "0:1", "-sn", "-dn",
		"-c:v", "copy", "-tag:v", "hvc1",
		"-c:a", "eac3", "-b:a", "640k", "-ac", "6",
		"-f", "hls", "-hls_time", "6", "-hls_playlist_type", "event",
		"-hls_segment_type", "fmp4", "-hls_flags", "independent_segments",
		"-hls_fmp4_init_filename", "init.mp4",
		"-hls_segment_filename", "/tmp/out/seg_%04d.m4s",
		"/tmp/out/index.m3u8",
	}, " ")
	if got != want {
		t.Fatalf("no-subs args changed:\ngot:  %s\nwant: %s", got, want)
	}
}

func TestBuildArgsEmbeddedSub(t *testing.T) {
	c := baseCfg()
	c.SubMap = "3"
	args := BuildArgs(c)
	argsHave(t, args, "-map", "0:3", "-dn")
	argsHave(t, args, "-c:s", "webvtt")
	argsHave(t, args, "-master_pl_name", "master.m3u8")
	if slices.Contains(args, "-sn") {
		t.Fatal("-sn must be dropped when a subtitle is mapped")
	}
}

func TestBuildArgsExternalSub(t *testing.T) {
	c := baseCfg()
	c.SubPath = "/subs/en.srt"
	args := BuildArgs(c)
	argsHave(t, args, "-i", "/movies/a.mkv")
	argsHave(t, args, "-i", "/subs/en.srt")
	argsHave(t, args, "-map", "1:0", "-dn")
	argsHave(t, args, "-c:s", "webvtt")
	if slices.Index(args, "/subs/en.srt") < slices.Index(args, "/movies/a.mkv") {
		t.Fatal("subtitle input must come after the media input (it must be input 1)")
	}
	if slices.Contains(args, "-sn") {
		t.Fatal("-sn must be dropped when a subtitle is mapped")
	}
}

func TestHasSubAndPlaylistName(t *testing.T) {
	c := baseCfg()
	if c.HasSub() || c.PlaylistName() != "index.m3u8" {
		t.Fatalf("no-subs config: HasSub=%v playlist=%q", c.HasSub(), c.PlaylistName())
	}
	c.SubMap = "0" // ff-index 0 is a valid stream — string zero value keeps it unambiguous
	if !c.HasSub() || c.PlaylistName() != "master.m3u8" {
		t.Fatalf("embedded-sub config: HasSub=%v playlist=%q", c.HasSub(), c.PlaylistName())
	}
	c = baseCfg()
	c.SubPath = "/subs/en.srt"
	if !c.HasSub() || c.PlaylistName() != "master.m3u8" {
		t.Fatalf("external-sub config: HasSub=%v playlist=%q", c.HasSub(), c.PlaylistName())
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd helper && go test -run 'TestBuildArgs|TestHasSub' ./...`
Expected: compile FAILURE (`SubMap` etc. undefined) — that counts as the failing state.

- [ ] **Step 3: Implement**

In `helper/job.go`, extend `JobConfig`:

```go
type JobConfig struct {
	FFmpeg    string
	Source    string
	OutDir    string
	VCodec    string
	ACodec    string
	AChannels int
	VMap      int
	AMap      int
	// Subtitles are optional; zero values mean "no subtitle track". SubMap
	// holds the ff-index as a string so the zero value can't be mistaken
	// for stream 0. SubPath (external file, fed as input 1) wins over SubMap.
	SubPath string
	SubMap  string
	SubLang string
	SubName string
	Duration float64
}

func (c JobConfig) HasSub() bool { return c.SubPath != "" || c.SubMap != "" }

// PlaylistName is the playlist the TV is handed: ffmpeg's master playlist
// when a subtitle rendition exists, the media playlist alone otherwise.
func (c JobConfig) PlaylistName() string {
	if c.HasSub() {
		return "master.m3u8"
	}
	return "index.m3u8"
}
```

Rework the head and tail of `BuildArgs`:

```go
func BuildArgs(c JobConfig) []string {
	args := []string{
		"-hide_banner", "-nostats", "-loglevel", "warning", "-y",
		"-progress", "pipe:1",
		"-i", c.Source,
	}
	if c.SubPath != "" {
		args = append(args, "-i", c.SubPath)
	}
	args = append(args,
		"-map", "0:"+strconv.Itoa(c.VMap),
		"-map", "0:"+strconv.Itoa(c.AMap),
	)
	switch {
	case c.SubPath != "":
		args = append(args, "-map", "1:0", "-dn")
	case c.SubMap != "":
		args = append(args, "-map", "0:"+c.SubMap, "-dn")
	default:
		args = append(args, "-sn", "-dn")
	}
```

Keep the video and audio switches exactly as they are. After the audio switch, before the final append, add:

```go
	if c.HasSub() {
		args = append(args, "-c:s", "webvtt")
	}
```

Replace the final `return append(args, ...)` with:

```go
	args = append(args,
		"-f", "hls", "-hls_time", "6", "-hls_playlist_type", "event",
		"-hls_segment_type", "fmp4", "-hls_flags", "independent_segments",
		"-hls_fmp4_init_filename", "init.mp4",
		"-hls_segment_filename", filepath.Join(c.OutDir, "seg_%04d.m4s"),
	)
	if c.HasSub() {
		args = append(args, "-master_pl_name", "master.m3u8")
	}
	return append(args, filepath.Join(c.OutDir, "index.m3u8"))
}
```

- [ ] **Step 4: Run the full helper suite**

Run: `cd helper && gofmt -w job.go job_test.go && go test ./...`
Expected: all PASS (existing tests unaffected — no-subs path unchanged).

- [ ] **Step 5: Commit**

```bash
git add helper/job.go helper/job_test.go
git commit -m "helper: map the selected subtitle stream to a WebVTT HLS rendition"
```

---

### Task 4: `RewriteMasterPlaylist` (helper, pure function)

**Files:**
- Create: `helper/playlist.go`
- Test: `helper/playlist_test.go` (new)

**Interfaces:**
- Consumes: nothing.
- Produces: `func RewriteMasterPlaylist(content, name, lang string) string`. Task 5's server handler calls it.

- [ ] **Step 1: Write the failing tests**

Create `helper/playlist_test.go`:

```go
package main

import (
	"strings"
	"testing"
)

// Shape ffmpeg's hlsenc actually emits for a single variant + one subtitle
// rendition (attribute order matters to these tests only as "preserved").
const ffmpegMaster = `#EXTM3U
#EXT-X-VERSION:6
#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="default",NAME="subtitle_0",DEFAULT=NO,AUTOSELECT=YES,URI="index_vtt.m3u8"
#EXT-X-STREAM-INF:BANDWIDTH=1234567,CODECS="hvc1.2.4.L120.b0,mp4a.40.2",SUBTITLES="default"
index.m3u8
`

func TestRewriteSetsDefaultNameLanguage(t *testing.T) {
	out := RewriteMasterPlaylist(ffmpegMaster, "English (SDH)", "en")
	if !strings.Contains(out, "DEFAULT=YES") || strings.Contains(out, "DEFAULT=NO") {
		t.Fatalf("DEFAULT not rewritten:\n%s", out)
	}
	if !strings.Contains(out, `NAME="English (SDH)"`) {
		t.Fatalf("NAME not rewritten:\n%s", out)
	}
	if !strings.Contains(out, `LANGUAGE="en"`) {
		t.Fatalf("LANGUAGE not added:\n%s", out)
	}
	if !strings.Contains(out, `URI="index_vtt.m3u8"`) {
		t.Fatalf("URI must be preserved:\n%s", out)
	}
}

func TestRewriteLeavesOtherLinesAlone(t *testing.T) {
	out := RewriteMasterPlaylist(ffmpegMaster, "English", "en")
	for _, line := range []string{
		"#EXTM3U",
		"#EXT-X-VERSION:6",
		`#EXT-X-STREAM-INF:BANDWIDTH=1234567,CODECS="hvc1.2.4.L120.b0,mp4a.40.2",SUBTITLES="default"`,
		"index.m3u8",
	} {
		if !strings.Contains(out, line) {
			t.Fatalf("line %q disturbed:\n%s", line, out)
		}
	}
}

func TestRewriteFallbackNameAndNoLang(t *testing.T) {
	out := RewriteMasterPlaylist(ffmpegMaster, "", "")
	if !strings.Contains(out, `NAME="Subtitles"`) {
		t.Fatalf("empty name must fall back to Subtitles:\n%s", out)
	}
	if strings.Contains(out, "LANGUAGE=") {
		t.Fatalf("empty lang must not add LANGUAGE:\n%s", out)
	}
}

func TestRewriteRespectsQuotedCommasAndQuotesInName(t *testing.T) {
	in := `#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="a, b",NAME="x",DEFAULT=NO,URI="index_vtt.m3u8"`
	out := RewriteMasterPlaylist(in, `Eng "SDH", forced`, "en")
	if !strings.Contains(out, `GROUP-ID="a, b"`) {
		t.Fatalf("quoted comma split wrongly:\n%s", out)
	}
	// interior quotes are illegal in quoted-string attrs: stripped, comma kept
	if !strings.Contains(out, `NAME="Eng SDH, forced"`) {
		t.Fatalf("name not sanitized:\n%s", out)
	}
}

func TestRewriteIgnoresNonSubtitleMediaLines(t *testing.T) {
	in := `#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud",NAME="a",DEFAULT=NO,URI="a.m3u8"`
	if got := RewriteMasterPlaylist(in, "English", "en"); got != in {
		t.Fatalf("audio media line must pass through untouched:\ngot:  %s\nwant: %s", got, in)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd helper && go test -run TestRewrite ./...`
Expected: compile FAILURE (`RewriteMasterPlaylist` undefined).

- [ ] **Step 3: Implement**

Create `helper/playlist.go`:

```go
package main

import "strings"

// RewriteMasterPlaylist fixes up the subtitle rendition line of an
// ffmpeg-written HLS master playlist. ffmpeg hardcodes DEFAULT=NO and a
// generic NAME, which leaves subtitles off until the viewer digs through the
// TV's menu; the caller knows the track's real name and language, so the
// server rewrites the line at serve time.
func RewriteMasterPlaylist(content, name, lang string) string {
	if name == "" {
		name = "Subtitles"
	}
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if !strings.HasPrefix(line, "#EXT-X-MEDIA:") || !strings.Contains(line, "TYPE=SUBTITLES") {
			continue
		}
		attrs := splitAttrs(strings.TrimPrefix(line, "#EXT-X-MEDIA:"))
		attrs = setAttr(attrs, "DEFAULT", "YES")
		attrs = setAttr(attrs, "AUTOSELECT", "YES")
		attrs = setAttr(attrs, "NAME", quoteAttr(name))
		if lang != "" {
			attrs = setAttr(attrs, "LANGUAGE", quoteAttr(lang))
		}
		lines[i] = "#EXT-X-MEDIA:" + strings.Join(attrs, ",")
	}
	return strings.Join(lines, "\n")
}

// splitAttrs splits an m3u8 attribute list on commas outside double quotes.
func splitAttrs(s string) []string {
	var out []string
	inQuote := false
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			inQuote = !inQuote
		case ',':
			if !inQuote {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	return append(out, s[start:])
}

func setAttr(attrs []string, key, val string) []string {
	for i, a := range attrs {
		if strings.HasPrefix(a, key+"=") {
			attrs[i] = key + "=" + val
			return attrs
		}
	}
	return append(attrs, key+"="+val)
}

// quoteAttr renders a quoted-string attribute value; interior double quotes
// are illegal in the format, so they are stripped rather than escaped.
func quoteAttr(v string) string {
	return `"` + strings.ReplaceAll(v, `"`, "") + `"`
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd helper && gofmt -w playlist.go playlist_test.go && go test -run TestRewrite ./...`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add helper/playlist.go helper/playlist_test.go
git commit -m "helper: rewrite the master playlist's subtitle rendition attributes"
```

---

### Task 5: Server serves a rewritten `master.m3u8` and knows `.vtt` (helper)

**Files:**
- Modify: `helper/server.go` (whole file), `helper/main.go:94` (one call site), `helper/server_test.go`

**Interfaces:**
- Consumes: `RewriteMasterPlaylist` (Task 4); `JobConfig.SubName`/`SubLang` (Task 3).
- Produces: `func StartServer(dir, subName, subLang string) (int, func(), error)` — signature change; all callers updated in this task.

- [ ] **Step 1: Write the failing tests**

In `helper/server_test.go`, update the existing call site to `StartServer(dir, "", "")`, then append:

```go
func TestServerRewritesMasterPlaylist(t *testing.T) {
	dir := t.TempDir()
	master := "#EXTM3U\n#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID=\"default\",NAME=\"subtitle_0\",DEFAULT=NO,AUTOSELECT=YES,URI=\"index_vtt.m3u8\"\nindex.m3u8\n"
	os.WriteFile(filepath.Join(dir, "master.m3u8"), []byte(master), 0o644)
	os.WriteFile(filepath.Join(dir, "seg_0000.vtt"), []byte("WEBVTT\n"), 0o644)

	port, shutdown, err := StartServer(dir, "English", "en")
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
	port, shutdown, err := StartServer(dir, "", "")
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
```

Add `"strings"` to the test file's imports.

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd helper && go test -run TestServer ./...`
Expected: compile FAILURE (wrong `StartServer` arity).

- [ ] **Step 3: Implement**

Rewrite `helper/server.go`:

```go
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
```

In `helper/main.go`, change the call site (line 94):

```go
	port, shutdown, err := StartServer(c.OutDir, c.SubName, c.SubLang)
```

- [ ] **Step 4: Run the full helper suite**

Run: `cd helper && gofmt -w server.go server_test.go main.go && go test ./...`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add helper/server.go helper/server_test.go helper/main.go
git commit -m "helper: serve master.m3u8 with rewritten subtitle attributes, know .vtt"
```

---

### Task 6: Serve flags, ready-check, URL, and cleanup (helper `main.go`)

**Files:**
- Modify: `helper/main.go` (`runServe`: flag registration ~line 43-53, cleanup globs line 83, ready loop lines 115-151)
- Test: `helper/main_test.go`

**Interfaces:**
- Consumes: `JobConfig` sub fields + `HasSub`/`PlaylistName` (Task 3), rewriting server (Task 5). Flag names from Task 2: `-smap`, `-subpath`, `-sublang`, `-subname`.
- Produces: with subs, the `ready` event's `url` ends in `/master.m3u8`; stale `master.m3u8` / `*_vtt.m3u8` / `*.vtt` files are cleaned at start. Task 7's e2e relies on both.

- [ ] **Step 1: Write the failing tests**

Append to `helper/main_test.go`:

```go
func TestServeWithSubsAdvertisesRewrittenMaster(t *testing.T) {
	bin := buildHelper(t)
	out := t.TempDir()
	// Stub ffmpeg writes what hlsenc would for one variant + a subtitle
	// rendition, then idles like a long transcode.
	stub := filepath.Join(t.TempDir(), "ffmpeg")
	master := `#EXTM3U\n#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="default",NAME="subtitle_0",DEFAULT=NO,AUTOSELECT=YES,URI="index_vtt.m3u8"\nindex.m3u8`
	os.WriteFile(stub, []byte(fmt.Sprintf(
		"#!/bin/sh\necho '#EXTM3U' > %[1]s/index.m3u8\necho '#EXTM3U' > %[1]s/index_vtt.m3u8\nprintf '%%b\\n' '%[2]s' > %[1]s/master.m3u8\necho out_time_us=1000000\ntrap 'exit 0' TERM\nsleep 60\n",
		out, master)), 0o755)

	cmd := exec.Command(bin, "serve",
		"-source", "/dev/null", "-out", out, "-ffmpeg", stub,
		"-parent", fmt.Sprint(os.Getpid()), "-duration", "60",
		"-vcodec", "h264", "-acodec", "aac", "-achannels", "2", "-vmap", "0", "-amap", "1",
		"-smap", "2", "-sublang", "en", "-subname", "English")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()

	readyURL := waitReady(t, stdout, 15*time.Second)
	if !strings.HasSuffix(readyURL, "/master.m3u8") {
		t.Fatalf("with subs the helper must advertise the master playlist, got %q", readyURL)
	}
	resp, err := http.Get(readyURL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	s := string(body)
	if !strings.Contains(s, "DEFAULT=YES") || !strings.Contains(s, `NAME="English"`) || !strings.Contains(s, `LANGUAGE="en"`) {
		t.Fatalf("served master not rewritten:\n%s", s)
	}
}

func TestServeCleansStaleSubtitleFiles(t *testing.T) {
	bin := buildHelper(t)
	out := t.TempDir()
	// Stale leftovers from a previous subtitled cast; this cast has no subs,
	// so nothing recreates them — they must be gone once serving is up.
	for _, f := range []string{"master.m3u8", "index_vtt.m3u8", "seg_0000.vtt"} {
		os.WriteFile(filepath.Join(out, f), []byte("stale"), 0o644)
	}
	stub := filepath.Join(t.TempDir(), "ffmpeg")
	os.WriteFile(stub, []byte(fmt.Sprintf(
		"#!/bin/sh\necho '#EXTM3U' > %s/index.m3u8\ntrap 'exit 0' TERM\nsleep 60\n",
		out)), 0o755)

	cmd := exec.Command(bin, "serve",
		"-source", "/dev/null", "-out", out, "-ffmpeg", stub,
		"-parent", fmt.Sprint(os.Getpid()), "-duration", "60",
		"-vcodec", "h264", "-acodec", "aac", "-achannels", "2", "-vmap", "0", "-amap", "1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()

	waitReady(t, stdout, 15*time.Second)
	for _, f := range []string{"master.m3u8", "index_vtt.m3u8", "seg_0000.vtt"} {
		if _, err := os.Stat(filepath.Join(out, f)); err == nil {
			t.Fatalf("stale %s survived cleanup", f)
		}
	}
}

// waitReady scans helper stdout until a ready event and returns its url.
func waitReady(t *testing.T, stdout io.Reader, timeout time.Duration) string {
	t.Helper()
	sc := bufio.NewScanner(stdout)
	deadline := time.After(timeout)
	for {
		lineCh := make(chan string, 1)
		go func() {
			if sc.Scan() {
				lineCh <- sc.Text()
			} else {
				lineCh <- ""
			}
		}()
		select {
		case line := <-lineCh:
			if line == "" {
				t.Fatal("helper stdout closed before ready")
			}
			var ev map[string]any
			if err := json.Unmarshal([]byte(line), &ev); err != nil {
				t.Fatalf("bad line %q", line)
			}
			if ev["event"] == "error" {
				t.Fatalf("helper errored: %v", ev)
			}
			if ev["event"] == "ready" {
				url, _ := ev["url"].(string)
				return url
			}
		case <-deadline:
			t.Fatal("no ready event in time")
		}
	}
}
```

Add `"io"` to `helper/main_test.go` imports. (Optionally refactor `TestServeEndToEndWithStub` to use `waitReady` — nice, not required.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd helper && go test -run TestServe ./...`
Expected: `TestServeWithSubsAdvertisesRewrittenMaster` FAILS — the helper exits 2 on the unknown `-smap` flag, so the test dies with "stdout closed before ready". `TestServeCleansStaleSubtitleFiles` FAILS on surviving stale files.

- [ ] **Step 3: Implement in `runServe`**

Register the flags (after the `-amap` line):

```go
	fs.StringVar(&c.SubMap, "smap", "", "embedded subtitle stream ff-index (empty = no subtitles)")
	fs.StringVar(&c.SubPath, "subpath", "", "external subtitle file (used as input 1; overrides -smap)")
	fs.StringVar(&c.SubLang, "sublang", "", "subtitle language tag for the HLS rendition")
	fs.StringVar(&c.SubName, "subname", "", "subtitle display name for the HLS rendition")
```

Extend the cleanup globs (line 83):

```go
	for _, pat := range []string{"index.m3u8", "master.m3u8", "*_vtt.m3u8", "*.vtt", "init.mp4", "seg_*.m4s"} {
```

Rework the ready logic. Replace `playlist := filepath.Join(c.OutDir, "index.m3u8")` with:

```go
	// The TV can start pulling as soon as the advertised playlist exists.
	// With subs that's ffmpeg's master playlist — but the variant playlist
	// it points at must exist too before the URL is usable.
	playlistName := c.PlaylistName()
	readyURL := fmt.Sprintf("http://%s:%d/%s", ip, port, playlistName)
	playlistReady := func() bool {
		if _, err := os.Stat(filepath.Join(c.OutDir, playlistName)); err != nil {
			return false
		}
		if c.HasSub() {
			if _, err := os.Stat(filepath.Join(c.OutDir, "index.m3u8")); err != nil {
				return false
			}
		}
		return true
	}
```

Then replace both ready-emission sites:

- In the ticker case, `if _, err := os.Stat(playlist); err == nil {` becomes `if playlistReady() {`, and the `Emit` becomes `Emit(os.Stdout, "ready", map[string]any{"url": readyURL})`.
- In the `done` case, `if _, serr := os.Stat(playlist); serr == nil {` becomes `if playlistReady() {`, with the same `Emit` change.

- [ ] **Step 4: Run the full helper suite**

Run: `cd helper && gofmt -w main.go main_test.go && go test ./...`
Expected: all PASS, including the untouched no-subs stub test (URL still `/index.m3u8`).

- [ ] **Step 5: Commit**

```bash
git add helper/main.go helper/main_test.go
git commit -m "helper: advertise master.m3u8 for subtitled casts, clean stale vtt files"
```

---

### Task 7: Real-ffmpeg e2e with an embedded SRT + docs

**Files:**
- Modify: `helper/e2e_test.go`, `README.md:13-17` (status paragraph)
- Test: `helper/e2e_test.go` (this task IS the test)

**Interfaces:**
- Consumes: the complete pipeline from Tasks 3–6.
- Produces: proof that a real ffmpeg produces a master playlist + WebVTT rendition the helper serves correctly. Nothing downstream.

- [ ] **Step 1: Write the failing e2e test**

Append to `helper/e2e_test.go`:

```go
// Synthesizes H.264 + AAC + an embedded SRT track (streams 0/1/2).
func makeSubbedFixture(t *testing.T, ffmpeg string) string {
	t.Helper()
	dir := t.TempDir()
	srt := filepath.Join(dir, "subs.srt")
	os.WriteFile(srt, []byte("1\n00:00:01,000 --> 00:00:03,000\nhello from iina-airplay\n"), 0o644)
	p := filepath.Join(dir, "fixture-subs.mkv")
	cmd := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=duration=8:size=640x360:rate=25",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=8",
		"-i", srt,
		"-map", "0:v", "-map", "1:a", "-map", "2:s",
		"-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac", "-c:s", "srt", p)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("subbed fixture: %v\n%s", err, out)
	}
	return p
}

// mediaURI pulls the URI="..." value out of an EXT-X-MEDIA line.
func mediaURI(line string) string {
	const k = `URI="`
	i := strings.Index(line, k)
	if i < 0 {
		return ""
	}
	rest := line[i+len(k):]
	j := strings.Index(rest, `"`)
	if j < 0 {
		return ""
	}
	return rest[:j]
}

func TestRealFFmpegSubtitledEndToEnd(t *testing.T) {
	ffmpeg := findFFmpeg(t)
	fixture := makeSubbedFixture(t, ffmpeg)
	bin := buildHelper(t)
	out := t.TempDir()

	cmd := exec.Command(bin, "serve",
		"-source", fixture, "-out", out, "-ffmpeg", ffmpeg,
		"-parent", fmt.Sprint(os.Getpid()), "-duration", "8",
		"-vcodec", "h264", "-acodec", "aac", "-achannels", "2", "-vmap", "0", "-amap", "1",
		"-smap", "2", "-sublang", "en", "-subname", "English")
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()

	url := waitReady(t, stdout, 60*time.Second)
	if !strings.HasSuffix(url, "/master.m3u8") {
		t.Fatalf("subtitled cast must advertise master.m3u8, got %q", url)
	}

	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	master := string(body)
	var subLine string
	for _, line := range strings.Split(master, "\n") {
		if strings.HasPrefix(line, "#EXT-X-MEDIA:") && strings.Contains(line, "TYPE=SUBTITLES") {
			subLine = line
		}
	}
	if subLine == "" {
		t.Fatalf("no subtitle rendition in master:\n%s", master)
	}
	for _, want := range []string{"DEFAULT=YES", `NAME="English"`, `LANGUAGE="en"`} {
		if !strings.Contains(subLine, want) {
			t.Fatalf("subtitle line missing %s:\n%s", want, subLine)
		}
	}

	// The rendition playlist and its first segment must be fetchable, and the
	// cue text must have survived the WebVTT conversion.
	vttPlaylistURL := strings.Replace(url, "master.m3u8", mediaURI(subLine), 1)
	resp2, err := http.Get(vttPlaylistURL)
	if err != nil {
		t.Fatal(err)
	}
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if resp2.StatusCode != 200 || !strings.Contains(string(body2), "#EXTM3U") {
		t.Fatalf("vtt playlist fetch: %d\n%s", resp2.StatusCode, body2)
	}
	var seg string
	for _, line := range strings.Split(string(body2), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			seg = line
			break
		}
	}
	if seg == "" {
		t.Fatalf("no segment in vtt playlist:\n%s", body2)
	}
	resp3, err := http.Get(strings.Replace(url, "master.m3u8", seg, 1))
	if err != nil {
		t.Fatal(err)
	}
	body3, _ := io.ReadAll(resp3.Body)
	resp3.Body.Close()
	if !strings.Contains(string(body3), "hello from iina-airplay") {
		t.Fatalf("cue text missing from first vtt segment:\n%s", body3)
	}
}
```

- [ ] **Step 2: Run the e2e**

Run: `cd helper && go test -run TestRealFFmpegSubtitledEndToEnd -v ./...`
Expected: PASS (Tasks 3–6 are already in). If it fails on segment naming or playlist naming, the failure output shows what ffmpeg actually produced — fix the *test's assumptions only if the served stream is actually correct*; a wrong advertised URL or unrewritten master is a real bug, go back to the task that owns it. **This is the step that validates the spec's Approach-A risk (ffmpeg's HLS+WebVTT behavior); if ffmpeg fundamentally can't produce a usable rendition here, stop and report — the spec names the fallback (helper-authored playlists) and that's a design change, not something to improvise inline.**

- [ ] **Step 3: Run everything**

Run: `make test`
Expected: all Go and node tests PASS.

- [ ] **Step 4: Update docs**

In `README.md`, replace the status paragraph's feature summary sentence (lines 13-17) — after "full test suite." add:

```
Text subtitles (embedded or external SRT/ASS; the track selected in IINA) are
carried as a WebVTT rendition the TV shows via its own subtitle menu. ASS
styling is flattened to plain text; image subs (PGS/DVD) can't be cast and are
dropped with a notice — burn-in is a possible future round.
```

- [ ] **Step 5: Commit**

```bash
git add helper/e2e_test.go README.md
git commit -m "helper: e2e-test the subtitled pipeline against real ffmpeg; docs"
```

---

## Verification checklist (post-plan)

- `make test` green.
- `git log` shows one commit per task.
- Manual acceptance (user, at the TV): cast a file with an SRT selected in IINA → subtitles appear on the TV automatically; cast with subs off → none; cast a PGS file → OSD notice, video still casts.
