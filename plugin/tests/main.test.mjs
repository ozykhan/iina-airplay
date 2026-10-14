import { test } from "node:test";
import assert from "node:assert/strict";
import { createRequire } from "node:module";
const require = createRequire(import.meta.url);
const { selectTracks, subtitleLabel, parseHelperEvents, pluginsDirFromDataDir, isValidPid, hasURLScheme, normalizeSource, lanIPOverride, describeMediaFailure } = require("../main.js");

const mpvTracks = [
  { type: "video", id: 1, selected: true, codec: "hevc", "ff-index": 0 },
  { type: "audio", id: 1, selected: false, codec: "aac", "ff-index": 1, "demux-channel-count": 2 },
  { type: "audio", id: 2, selected: true, codec: "truehd", "ff-index": 2, "demux-channel-count": 8 },
  { type: "sub",   id: 1, selected: true, codec: "subrip", "ff-index": 3 },
];

test("selectTracks picks selected audio and first video", () => {
  const r = selectTracks(mpvTracks);
  assert.deepEqual(r, {
    vcodec: "hevc", acodec: "truehd", achannels: 8, vmap: 0, amap: 2,
    sub: { path: null, smap: 3, lang: "", title: "" }, subDropped: false,
  });
});

test("selectTracks falls back to first audio when none selected", () => {
  const tracks = mpvTracks.map(t => ({ ...t, selected: false }));
  const r = selectTracks(tracks);
  assert.equal(r.amap, 1);
  assert.equal(r.acodec, "aac");
});

test("selectTracks returns null without a video track", () => {
  assert.equal(selectTracks(mpvTracks.filter(t => t.type !== "video")), null);
});

test("selectTracks defaults channels to 2 when missing", () => {
  const tracks = [
    { type: "video", id: 1, selected: true, codec: "h264", "ff-index": 0 },
    { type: "audio", id: 1, selected: true, codec: "opus", "ff-index": 1 },
  ];
  assert.equal(selectTracks(tracks).achannels, 2);
});

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

test("parseHelperEvents handles split and batched lines", () => {
  let r = parseHelperEvents("", '{"event":"progress","pct":1}\n{"event":"re');
  assert.equal(r.events.length, 1);
  assert.equal(r.events[0].pct, 1);
  r = parseHelperEvents(r.rest, 'ady","url":"http://x/index.m3u8"}\n');
  assert.equal(r.events.length, 1);
  assert.equal(r.events[0].event, "ready");
  assert.equal(r.rest, "");
});

test("parseHelperEvents skips non-JSON noise lines", () => {
  const r = parseHelperEvents("", "some stray warning\n{\"event\":\"packaged\"}\n");
  assert.equal(r.events.length, 1);
  assert.equal(r.events[0].event, "packaged");
});

test("pluginsDirFromDataDir derives the parent of the .data component", () => {
  const dataDir = "/Users/x/Library/Application Support/com.colliderli.iina/plugins/.data/dev.faruk.iina-airplay";
  assert.equal(
    pluginsDirFromDataDir(dataDir),
    "/Users/x/Library/Application Support/com.colliderli.iina/plugins"
  );
});

test("pluginsDirFromDataDir handles a trailing slash", () => {
  const dataDir = "/Users/x/Library/Application Support/com.colliderli.iina/plugins/.data/dev.faruk.iina-airplay/";
  assert.equal(
    pluginsDirFromDataDir(dataDir),
    "/Users/x/Library/Application Support/com.colliderli.iina/plugins"
  );
});

test("pluginsDirFromDataDir returns null for an unexpected shape", () => {
  assert.equal(pluginsDirFromDataDir("/Users/x/somewhere/else"), null);
  assert.equal(pluginsDirFromDataDir(""), null);
  assert.equal(pluginsDirFromDataDir(null), null);
});

test("hasURLScheme rejects filesystem paths", () => {
  assert.equal(hasURLScheme("/movies/a.mkv"), false);
  assert.equal(hasURLScheme("/Volumes/Media/Show S01E01.mkv"), false);
});

test("hasURLScheme accepts network streams the bundled ffmpeg cannot open", () => {
  // The bundled ffmpeg is built --disable-network, so these have no protocol
  // handler at all. Say so plainly rather than letting ffmpeg fail obscurely.
  for (const url of ["http://x/y.mp4", "https://x/y.mp4", "rtsp://x/y", "ytdl://abc"]) {
    assert.equal(hasURLScheme(url), true, url);
  }
});

test("normalizeSource strips the file:// scheme", () => {
  assert.equal(normalizeSource("file:///movies/a.mkv"), "/movies/a.mkv");
});

test("normalizeSource strips file://localhost/ too", () => {
  assert.equal(normalizeSource("file://localhost/movies/a.mkv"), "/movies/a.mkv");
});

test("normalizeSource percent-decodes the path", () => {
  assert.equal(normalizeSource("file:///movies/My%20Film.mkv"), "/movies/My Film.mkv");
});

test("normalizeSource passes a bare path through unchanged", () => {
  assert.equal(normalizeSource("/movies/a.mkv"), "/movies/a.mkv");
});

test("normalizeSource does not throw on a malformed percent-escape", () => {
  assert.doesNotThrow(() => normalizeSource("file:///bad%ZZ.mkv"));
  assert.equal(normalizeSource("file:///bad%ZZ.mkv"), "/bad%ZZ.mkv");
});

test("isValidPid accepts positive finite numbers only", () => {
  assert.equal(isValidPid(1234), true);
  assert.equal(isValidPid(0), false);
  assert.equal(isValidPid(-1), false);
  assert.equal(isValidPid(NaN), false);
  assert.equal(isValidPid(undefined), false);
  assert.equal(isValidPid(null), false);
  assert.equal(isValidPid("1234"), false);
});

test("subtitleLabel names the track by language", () => {
  const tracks = selectTracks([
    { type: "video", selected: true, codec: "hevc", "ff-index": 0 },
    { type: "audio", selected: true, codec: "aac", "ff-index": 1 },
    { type: "sub", selected: true, codec: "subrip", "ff-index": 2, lang: "eng" },
  ]);
  assert.deepEqual(subtitleLabel(tracks), { label: "Subtitles: eng", warn: false });
});

test("subtitleLabel falls back to the track title, then to On", () => {
  const base = [
    { type: "video", selected: true, codec: "hevc", "ff-index": 0 },
    { type: "audio", selected: true, codec: "aac", "ff-index": 1 },
  ];
  const titled = selectTracks(base.concat([
    { type: "sub", selected: true, codec: "subrip", "ff-index": 2, title: "Forced" },
  ]));
  assert.equal(subtitleLabel(titled).label, "Subtitles: Forced");

  const bare = selectTracks(base.concat([
    { type: "sub", selected: true, codec: "subrip", "ff-index": 2 },
  ]));
  assert.equal(subtitleLabel(bare).label, "Subtitles: On");
});

test("subtitleLabel warns when the subtitle track was dropped", () => {
  const tracks = selectTracks([
    { type: "video", selected: true, codec: "hevc", "ff-index": 0 },
    { type: "audio", selected: true, codec: "aac", "ff-index": 1 },
    { type: "sub", selected: true, codec: "hdmv_pgs_subtitle", "ff-index": 2 },
  ]);
  assert.deepEqual(subtitleLabel(tracks),
    { label: "Subtitles not supported", warn: true });
});

test("subtitleLabel reports no subtitles when none is selected", () => {
  const tracks = selectTracks([
    { type: "video", selected: true, codec: "hevc", "ff-index": 0 },
    { type: "audio", selected: true, codec: "aac", "ff-index": 1 },
  ]);
  assert.deepEqual(subtitleLabel(tracks), { label: "No subtitles", warn: false });
});

test("subtitleLabel is empty when there are no castable tracks", () => {
  assert.deepEqual(subtitleLabel(null), { label: "", warn: false });
});

// Issue #33: the "LAN address" preference is passed to the helper as -ip.
// The helper validates it; the plugin only trims and passes it through, and
// treats anything that isn't a string (unset, or a stale non-string value
// in the preferences store) as "automatic".
test("lanIPOverride trims a string value", () => {
  assert.equal(lanIPOverride("192.168.1.20"), "192.168.1.20");
  assert.equal(lanIPOverride("  192.168.1.20\n"), "192.168.1.20");
  assert.equal(lanIPOverride("   "), "");
});

test("lanIPOverride is empty (automatic) for anything that isn't a string", () => {
  assert.equal(lanIPOverride(""), "");
  assert.equal(lanIPOverride(null), "");
  assert.equal(lanIPOverride(undefined), "");
  assert.equal(lanIPOverride(42), "");
  assert.equal(lanIPOverride({ ip: "192.168.1.20" }), "");
});

// Issue #42: WebKit reports any stream it can't fetch as media error 4 with an
// empty MediaError.message, so the plugin probes the URL with curl from IINA
// and turns the result into something a user (or a bug report) can act on.
const STREAM = "http://192.168.1.2:5000/master.m3u8";

test("describeMediaFailure names an unreachable stream and curl's reason", () => {
  const msg = describeMediaFailure(STREAM, 4, {
    status: 7, stdout: "000",
    stderr: "curl: (7) Failed to connect to 192.168.1.2 port 5000 after 1 ms: Couldn't connect to server\n",
  });
  assert.match(msg, /can't reach its own stream at 192\.168\.1\.2:5000/);
  assert.match(msg, /Failed to connect to 192\.168\.1\.2 port 5000 after 1 ms: Couldn't connect to server/);
  assert.doesNotMatch(msg, /curl: \(7\)/);
  assert.match(msg, /LAN address/);
});

test("describeMediaFailure reports the HTTP status of a non-200 answer", () => {
  const msg = describeMediaFailure(STREAM, 4, { status: 0, stdout: "404", stderr: "" });
  assert.match(msg, /HTTP 404/);
  assert.match(msg, /media error 4/);
});

test("describeMediaFailure says WebKit refused a stream curl could fetch", () => {
  const msg = describeMediaFailure(STREAM, 4, { status: 0, stdout: "200", stderr: "" });
  assert.match(msg, /reaches the stream/);
  assert.match(msg, /media error 4/);
  assert.match(msg, new RegExp(STREAM.replace(/[.]/g, "\\.")));
});

test("describeMediaFailure falls back to the bare error when curl didn't run", () => {
  const msg = describeMediaFailure(STREAM, 4, null);
  assert.match(msg, /media error 4/);
  assert.match(msg, /192\.168\.1\.2:5000/);
});

test("describeMediaFailure copes with an unknown error code and empty stderr", () => {
  const msg = describeMediaFailure(STREAM, undefined, { status: 28, stdout: "000", stderr: "" });
  assert.match(msg, /can't reach its own stream/);
  assert.match(msg, /curl exit 28/);
});
