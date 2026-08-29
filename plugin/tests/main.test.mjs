import { test } from "node:test";
import assert from "node:assert/strict";
import { createRequire } from "node:module";
const require = createRequire(import.meta.url);
const { selectTracks, parseHelperEvents, pluginsDirFromDataDir, isValidPid, isLocalSource } = require("../main.js");

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

test("isLocalSource accepts filesystem paths", () => {
  assert.equal(isLocalSource("/movies/a.mkv"), true);
  assert.equal(isLocalSource("/Volumes/Media/Show S01E01.mkv"), true);
});

test("isLocalSource rejects network streams the bundled ffmpeg cannot open", () => {
  // The bundled ffmpeg is built --disable-network, so these have no protocol
  // handler at all. Say so plainly rather than letting ffmpeg fail obscurely.
  for (const url of ["http://x/y.mp4", "https://x/y.mp4", "rtsp://x/y", "ytdl://abc"]) {
    assert.equal(isLocalSource(url), false, url);
  }
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
