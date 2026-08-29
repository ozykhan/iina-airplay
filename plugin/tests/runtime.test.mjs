// Exercises the IINA-runtime block of main.js against a fake `iina` global, so
// the sidebar<->plugin wiring (menu item, onMessage handlers, helper spawns) is
// covered by node --test rather than only by clicking around in IINA.
import { test } from "node:test";
import assert from "node:assert/strict";
import { createRequire } from "node:module";
const require = createRequire(import.meta.url);
const MAIN = require.resolve("../main.js");

function loadPlugin(opts = {}) {
  const trackList = opts.tracks || [
    { type: "video", selected: true, codec: "hevc", "ff-index": 0 },
    { type: "audio", selected: true, codec: "aac", "ff-index": 1, "demux-channel-count": 2 },
  ];
  const execs = [];
  const messages = {};       // name -> handler registered via sidebar.onMessage
  const posted = [];         // [name, payload] sent to the page
  const osd = [];
  let menuCallback = null;
  let loadedFile = null;
  let shown = 0;

  const iina = {
    core: { osd: (m) => osd.push(m), pause: () => {} },
    mpv: {
      getString: (k) => (k === "path" ? (opts.path !== undefined ? opts.path : "/movies/a.mkv") : null),
      getNumber: (k) => (k === "pid" ? 4321 : k === "duration" ? 120 : 0),
      getNative: () => trackList,
    },
    menu: {
      item: (title, cb) => ({ title, cb }),
      addItem: (it) => { menuCallback = it.cb; },
    },
    sidebar: {
      loadFile: (f) => { loadedFile = f; for (const k of Object.keys(messages)) delete messages[k]; },
      onMessage: (name, cb) => { messages[name] = cb; },
      postMessage: (name, data) => posted.push([name, JSON.parse(JSON.stringify(data))]),
      show: () => { shown++; },
    },
    utils: {
      resolvePath: (p) => "/plugins/.data/dev.faruk.iina-airplay" + (p === "@tmp/hls" ? "/tmp/hls" : ""),
      exec: (bin, args) => {
        execs.push({ bin, args });
        // opts.binDirLookup scripts the /bin/sh lookup that resolveBinDir runs
        // when no bin dir is cached; everything else hangs, as before.
        if (bin === "/bin/sh" && opts.binDirLookup) return Promise.resolve(opts.binDirLookup);
        return new Promise(() => {});
      },
    },
    file: {
      read: () => (opts.binDirLookup ? "" : "/plugins/dev.faruk.iina-airplay/bin"),
      write: () => {},
    },
    console: { log: () => {} },
  };

  globalThis.iina = iina;
  delete require.cache[MAIN];
  try { require("../main.js"); } finally { delete globalThis.iina; delete require.cache[MAIN]; }

  return {
    execs, posted, osd,
    clickMenu: () => menuCallback(),
    send: (name, data) => {
      assert.ok(messages[name], `no onMessage handler registered for "${name}"`);
      return messages[name](data);
    },
    state: () => posted.filter(([n]) => n === "state").at(-1)?.[1],
    loadedFile: () => loadedFile,
    shown: () => shown,
  };
}

const serves = (p) => p.execs.filter((e) => e.args[0] === "serve");
const stops = (p) => p.execs.filter((e) => e.args[0] === "stop");

test("menu item opens the sidebar and starts a cast", () => {
  const p = loadPlugin();
  p.clickMenu();
  assert.equal(p.loadedFile(), "sidebar.html");
  assert.equal(p.shown(), 1);
  assert.equal(serves(p).length, 1);
  assert.match(serves(p)[0].bin, /\/bin\/airplay-helper$/);
});

test("the page can start a cast after stopping, without reloading the plugin", () => {
  const p = loadPlugin();
  p.clickMenu();
  assert.equal(serves(p).length, 1);

  p.send("stop", {});
  assert.equal(stops(p).length, 1);
  assert.equal(p.state().phase, "idle");

  p.send("start", {});
  assert.equal(serves(p).length, 2, "stopping must not leave the cast unstartable from the page");
  assert.equal(p.state().phase, "starting");
});

test("start is a no-op while a cast is already running", () => {
  const p = loadPlugin();
  p.clickMenu();
  p.send("start", {});
  assert.equal(serves(p).length, 1);
});

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

test("a broken install names the fix instead of leaking an exec error", async () => {
  // Exit 3 is resolveBinDir's signal for "found the plugin, but its binaries
  // are missing or not executable" — a hand-copied or quarantined install.
  const p = loadPlugin({ binDirLookup: { status: 3, stdout: "" } });
  p.clickMenu();
  await new Promise((r) => setImmediate(r)); // let the exec promise settle
  p.send("getState", {}); // simulate the page's poll to read the async result
  const s = p.state();
  assert.equal(s.phase, "error");
  assert.match(s.msg, /reinstall/i);
  assert.match(s.msg, /IINA/);
  assert.equal(serves(p).length, 0, "a broken install must not spawn the helper");
});

test("a network stream is declined with the stream-specific message, not silently absolute-path-rejected", () => {
  const p = loadPlugin({ path: "http://192.168.1.5:8080/video.mp4" });
  p.clickMenu();
  assert.ok(p.osd.some(m => /network stream/i.test(m)), "expected an OSD naming network streams");
  assert.equal(serves(p).length, 0, "a network stream must not spawn the helper");
  p.send("getState", {});
  assert.equal(p.state().phase, "error", "a sidebar-only viewer must see the error, not a stuck button");
  assert.match(p.state().msg, /network stream/i);
});

test("a relative path gets the local-file-path message, not the network-stream one", () => {
  const p = loadPlugin({ path: "movie.mkv" });
  p.clickMenu();
  assert.ok(p.osd.some(m => /local file path/i.test(m)), "expected an OSD naming the local-file-path problem");
  assert.ok(!p.osd.some(m => /network stream/i.test(m)), "a relative path is not a network stream");
  assert.equal(serves(p).length, 0, "a relative path must not spawn the helper");
  p.send("getState", {});
  assert.equal(p.state().phase, "error", "a sidebar-only viewer must see the error, not a stuck button");
  assert.match(p.state().msg, /local file path/i);
});

test("nothing playing sets the error state too, not just the OSD", () => {
  const p = loadPlugin({ path: null });
  p.clickMenu();
  assert.ok(p.osd.some(m => /nothing is playing/i.test(m)));
  assert.equal(serves(p).length, 0);
  p.send("getState", {});
  assert.equal(p.state().phase, "error", "a sidebar-only viewer must see the error, not a stuck button");
  assert.match(p.state().msg, /nothing is playing/i);
});

test("a file:// URI is normalized and cast, not declined as a network stream", () => {
  const p = loadPlugin({ path: "file:///movies/a.mkv" });
  p.clickMenu();
  assert.equal(serves(p).length, 1, "a file:// URI names a real local file that should be castable");
  assert.ok(!p.osd.some(m => /network stream/i.test(m)), "file:// must not be mislabelled a network stream");
});
