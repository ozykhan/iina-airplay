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
  const flags = { pause: false, mute: opts.mute || false,
                  speed: opts.speed !== undefined ? opts.speed : 1 };
  const sets = [];           // [name, value] pushed by mpv.set
  const events = {};         // event name -> callback registered via event.on
  const mpvCalls = [];       // every mpv.* call, to prove none happen after the player is gone
  const stdouts = [];        // per-serve stdout callbacks, for scripting helper events
  const stderrs = [];        // per-serve stderr callbacks
  const logs = [];           // console.log lines
  let menuCallback = null;
  let loadedFile = null;
  let loads = 0;
  let shown = 0;
  // IINA's sidebar.loadFile throws until the player window exists (see
  // JavascriptAPISidebarView.swift); a plugin loaded at launch runs before
  // that, one enabled from Settings runs after.
  let windowLoaded = opts.windowLoaded !== undefined ? opts.windowLoaded : true;

  const iina = {
    core: { osd: (m) => osd.push(m), pause: () => { flags.pause = true; } },
    mpv: {
      getString: (k) => {
        mpvCalls.push(k);
        if (k === "path") return opts.path !== undefined ? opts.path : "/movies/a.mkv";
        if (k === "video-params/gamma") return opts.gamma !== undefined ? opts.gamma : "bt.1886";
        if (opts.videoParams && k in opts.videoParams) return opts.videoParams[k];
        return null;
      },
      getNumber: (k) => (mpvCalls.push(k), k === "pid" ? 4321 : k === "duration" ? 120
                       : k === "time-pos" ? (opts.timePos !== undefined ? opts.timePos : 42)
                       : k === "speed" ? flags.speed : 0),
      getNative: (k) => (mpvCalls.push(k), trackList),
      getFlag: (k) => (mpvCalls.push(k), !!flags[k]),
      set: (k, v) => { mpvCalls.push(k); sets.push([k, v]); flags[k] = v; },
    },
    event: { on: (name, cb) => { events[name] = cb; } },
    menu: {
      item: (title, cb) => ({ title, cb }),
      addItem: (it) => { menuCallback = it.cb; },
    },
    sidebar: {
      loadFile: (f) => {
        if (!windowLoaded) throw new Error("sidebar.loadFile called when window is not available.");
        loadedFile = f; loads++;
        for (const k of Object.keys(messages)) delete messages[k];
      },
      onMessage: (name, cb) => { messages[name] = cb; },
      postMessage: (name, data) => posted.push([name, JSON.parse(JSON.stringify(data))]),
      show: () => { shown++; },
    },
    utils: {
      resolvePath: (p) => (opts.dataDir || "/plugins/.data/dev.faruk.iina-airplay") + (p === "@tmp/hls" ? "/tmp/hls" : ""),
      exec: (bin, args, cwd, onStdout, onStderr) => {
        execs.push({ bin, args });
        if (typeof onStdout === "function") stdouts.push(onStdout);
        if (typeof onStderr === "function") stderrs.push(onStderr);
        // opts.binDirLookup scripts the /bin/sh lookup that resolveBinDir runs
        // when no bin dir is cached; everything else hangs, as before.
        if (bin === "/bin/sh" && opts.binDirLookup) return Promise.resolve(opts.binDirLookup);
        // opts.curl scripts the reachability probe after a page media error
        // (issue #42): a result object resolves, an Error rejects.
        if (bin === "/usr/bin/curl" && opts.curl) {
          return opts.curl instanceof Error ? Promise.reject(opts.curl) : Promise.resolve(opts.curl);
        }
        return new Promise(() => {});
      },
    },
    file: {
      read: () => (opts.binDirLookup ? "" : "/plugins/dev.faruk.iina-airplay/bin"),
      write: () => {},
    },
    // Issue #33: preferences.get is synchronous in the JSContext and returns
    // the stored value or Info.json's preferenceDefaults entry. The default
    // for lanIP is "" (automatic), so that's the default here too.
    preferences: {
      get: (k) => (k === "lanIP" ? (opts.lanIP !== undefined ? opts.lanIP : "") : undefined),
    },
    console: { log: (m) => logs.push(m) },
  };

  globalThis.iina = iina;
  delete require.cache[MAIN];
  try { require("../main.js"); } finally { delete globalThis.iina; delete require.cache[MAIN]; }

  return {
    execs, posted, osd, logs,
    flags, sets, mpvCalls,
    fire: (name) => { assert.ok(events[name], `no event handler for "${name}"`); events[name](); },
    helperSays: (obj) => { assert.ok(stdouts.length, "no serve running"); stdouts.at(-1)(JSON.stringify(obj) + "\n"); },
    helperStderr: (chunk) => { assert.ok(stderrs.length, "no serve running"); stderrs.at(-1)(chunk); },
    clickMenu: () => menuCallback(),
    send: (name, data) => {
      assert.ok(messages[name], `no onMessage handler registered for "${name}"`);
      return messages[name](data);
    },
    state: () => posted.filter(([n]) => n === "state").at(-1)?.[1],
    loadedFile: () => loadedFile,
    loads: () => loads,
    shown: () => shown,
    loadWindow: () => {
      windowLoaded = true;
      if (events["iina.window-loaded"]) events["iina.window-loaded"]();
    },
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

// Issue #20: Info.json declares a sidebarTab, so IINA shows an "AirPlay" tab
// the user can open without ever touching the Plugins menu. That tab is an
// empty WKWebView until the plugin calls sidebar.loadFile, so the page (and
// its Start casting button) must be loaded as soon as the window exists,
// not only from the menu item.
test("the sidebar tab works when opened directly, without the menu item", () => {
  const p = loadPlugin();               // window already up: plugin enabled from Settings
  assert.equal(p.loadedFile(), "sidebar.html");
  p.send("getState", {});
  assert.equal(p.state().phase, "idle");
  p.send("start", {});
  assert.equal(serves(p).length, 1);
});

test("a plugin loaded before the window loads the sidebar page on window-loaded", () => {
  const p = loadPlugin({ windowLoaded: false }); // normal launch order
  assert.equal(p.loadedFile(), null, "must not blow up before the window exists");
  p.loadWindow();
  assert.equal(p.loadedFile(), "sidebar.html");
  p.send("getState", {});
  assert.equal(p.state().phase, "idle");
});

test("the menu item shows the already-loaded page instead of reloading it", () => {
  const p = loadPlugin();
  p.clickMenu();
  assert.equal(p.loads(), 1, "a reload would tear down the page's <video> mid-cast");
  assert.equal(p.shown(), 1);
  assert.equal(serves(p).length, 1);
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

test("the bin dir lookup searches for this copy's own identifier, read off its data dir", async () => {
  // A `make dev` build sits next to the installed plugin under a different
  // identifier; a lookup hardcoding the installed one would hand the dev
  // build the installed copy's helper.
  const { identifierPattern } = require("../main.js");
  const p = loadPlugin({
    dataDir: "/plugins/.data/dev.faruk.iina-airplay.dev",
    binDirLookup: { status: 0, stdout: "/plugins/iina-airplay.iinaplugin-dev.iinaplugin-dev/bin" },
  });
  p.clickMenu();
  const sh = p.execs.find((e) => e.bin === "/bin/sh");
  assert.ok(sh, "no bin dir lookup ran");
  assert.deepEqual(sh.args.slice(3), ["/plugins", identifierPattern("dev.faruk.iina-airplay.dev")]);
  assert.ok(!sh.args[1].includes("dev.faruk"), "the identifier must be an argument, not part of the script");
  await new Promise((r) => setImmediate(r));
  assert.equal(serves(p)[0].bin, "/plugins/iina-airplay.iinaplugin-dev.iinaplugin-dev/bin/airplay-helper");
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

test("no castable tracks sets the error state too, not just the OSD", () => {
  const p = loadPlugin({ tracks: [] });
  p.clickMenu();
  assert.ok(p.osd.some(m => /no castable video\/audio tracks/i.test(m)));
  assert.equal(serves(p).length, 0);
  p.send("getState", {});
  assert.equal(p.state().phase, "error", "a sidebar-only viewer must see the error, not a stuck button");
  assert.match(p.state().msg, /no castable video\/audio tracks/i);
});

test("a file:// URI is normalized and cast, not declined as a network stream", () => {
  const p = loadPlugin({ path: "file:///movies/a.mkv" });
  p.clickMenu();
  assert.equal(serves(p).length, 1, "a file:// URI names a real local file that should be castable");
  assert.ok(!p.osd.some(m => /network stream/i.test(m)), "file:// must not be mislabelled a network stream");
});

// ---- muted mirror wiring (spec 2026-08-30) ----

test("starting a cast mutes mpv and does NOT pause it", () => {
  const p = loadPlugin();
  p.clickMenu();
  assert.deepEqual(p.sets.filter(([k]) => k === "mute").at(-1), ["mute", true]);
  assert.equal(p.flags.pause, false, "the muted mirror replaced core.pause()");
});

test("state posts carry the sync block while casting, null when idle", () => {
  const p = loadPlugin();
  p.clickMenu();
  p.send("getState", {});
  assert.deepEqual(p.state().sync,
    { seq: 0, paused: false, seekTo: null, startPos: 42, rate: 1, ignoredRate: null });
  p.send("stop", {});
  assert.equal(p.state().sync, null);
});

// ---- playback speed (issue #48) ----

test("a cast asks the TV for mpv's current speed", () => {
  const p = loadPlugin({ speed: 1.25 });
  p.clickMenu();
  p.send("getState", {});
  assert.equal(p.state().sync.rate, 1.25);
});

test("an mpv speed change during a cast is passed on to the TV", () => {
  const p = loadPlugin();
  p.clickMenu();
  p.flags.speed = 1.5;
  p.fire("mpv.speed.changed");
  p.send("getState", {});
  assert.equal(p.state().sync.rate, 1.5);
});

// The TV plays at 1x for 11 s with the page's playbackRate at 1.25.
function tvIgnoresSpeed(p) {
  for (let ms = 0; ms <= 11000; ms += 500) {
    p.send("tvState", { appliedSeq: 0, pos: 42 + ms / 1000, paused: false, wireless: true,
                        ended: false, rate: 1.25, seeking: false, at: ms });
  }
}

test("a TV that ignores the speed drops mpv to 1x, says so, and stop restores it", () => {
  const p = loadPlugin({ speed: 1.25 });
  p.clickMenu();
  tvIgnoresSpeed(p);
  assert.deepEqual(p.sets.filter(([k]) => k === "speed"), [["speed", 1]]);
  assert.ok(p.osd.some((m) => /speed/i.test(m)), "expected an OSD saying the TV ignored the speed");
  p.fire("mpv.speed.changed");         // the echo of our own set
  p.send("getState", {});
  assert.equal(p.state().sync.rate, 1);
  assert.equal(p.state().sync.ignoredRate, 1.25, "the echo must not read as a new request");
  p.send("stop", {});
  assert.equal(p.flags.speed, 1.25, "the user's speed comes back once the TV no longer sets the clock");
});

test("stop restores the pre-cast mute flag (false case)", () => {
  const p = loadPlugin({ mute: false });
  p.clickMenu();
  p.send("stop", {});
  assert.equal(p.flags.mute, false);
});

test("stop preserves a user's pre-cast mute (true case)", () => {
  const p = loadPlugin({ mute: true });
  p.clickMenu();
  assert.equal(p.flags.mute, true, "muting an already-muted mpv is fine");
  p.send("stop", {});
  assert.equal(p.flags.mute, true, "the user's own mute must survive the cast");
});

test("an mpv pause bumps the sync seq and desired state", () => {
  const p = loadPlugin();
  p.clickMenu();
  p.flags.pause = true;
  p.fire("mpv.pause.changed");
  p.send("getState", {});
  assert.equal(p.state().sync.seq, 1);
  assert.equal(p.state().sync.paused, true);
});

test("an mpv seek becomes a seekTo command", () => {
  const p = loadPlugin({ timePos: 300 });
  p.clickMenu();
  p.fire("mpv.seek");
  p.send("getState", {});
  assert.deepEqual(p.state().sync.seekTo, { seq: 1, pos: 300 });
});

test("a TV-remote pause is mirrored into mpv", () => {
  const p = loadPlugin();
  p.clickMenu();
  p.send("tvState", { appliedSeq: 0, pos: 42, paused: true, wireless: true, ended: false });
  assert.deepEqual(p.sets.filter(([k]) => k === "pause").at(-1), ["pause", true]);
});

test("drift beyond tolerance corrects mpv's clock to the TV's", () => {
  const p = loadPlugin({ timePos: 42 });
  p.clickMenu();
  p.send("tvState", { appliedSeq: 0, pos: 50, paused: false, wireless: true, ended: false });
  assert.deepEqual(p.sets.filter(([k]) => k === "time-pos").at(-1), ["time-pos", 50]);
});

test("tvState.ended tears the cast down and restores mute", () => {
  const p = loadPlugin({ mute: false });
  p.clickMenu();
  p.send("tvState", { appliedSeq: 0, pos: 120, paused: false, wireless: true, ended: true });
  assert.equal(stops(p).length, 1);
  assert.equal(p.flags.mute, false);
  p.send("getState", {});
  assert.equal(p.state().phase, "idle");
});

test("loading a new file during a cast tears it down", () => {
  const p = loadPlugin();
  p.clickMenu();
  p.fire("iina.file-loaded");
  assert.equal(stops(p).length, 1);
  assert.equal(p.flags.mute, false);
});

test("file-loaded without a cast is a no-op", () => {
  const p = loadPlugin();
  p.fire("iina.file-loaded");
  assert.equal(stops(p).length, 0);
});

test("a pre-handshake tvState must not un-pause a paused cast", () => {
  const p = loadPlugin();
  p.flags.pause = true;         // mpv is paused when the user starts the cast
  p.clickMenu();
  // appliedSeq -1 is what the page sends before its own seq-0 sync (carrying
  // the initial paused snapshot) has been applied: appliedSeq -1 < mirror.seq
  // 0 suspends reconciliation, so this pre-handshake tvState must not be
  // misread as a TV-initiated un-pause.
  p.send("tvState", { appliedSeq: -1, pos: 0, paused: false, wireless: true, ended: false });
  assert.ok(!p.sets.some(([k, v]) => k === "pause" && v === false),
    "a pre-handshake tvState must not un-pause mpv");
  p.send("getState", {});
  assert.equal(p.state().sync.paused, true, "the initial snapshot must still be the desired state");
});

test("the poll restores mute after the helper dies on its own", () => {
  const p = loadPlugin({ mute: false });
  p.clickMenu();
  p.helperSays({ event: "stopped" });  // helper-initiated teardown: off-main, may not touch mpv
  assert.equal(p.flags.mute, true, "the exec callback itself must not touch mpv");
  p.send("getState", {});              // the next poll runs on-message: safe to restore
  assert.equal(p.flags.mute, false);
  assert.equal(p.state().sync, null);
});

test("tvState reaps a stale mirror after the helper dies, before touching mpv", () => {
  const p = loadPlugin({ mute: false });
  p.clickMenu();
  p.helperSays({ event: "stopped" }); // helper-initiated teardown: off-main, only state mutates
  p.send("tvState", { appliedSeq: 0, pos: 50, paused: false, wireless: true, ended: false });
  assert.equal(p.flags.mute, false, "tvState must reap the stale mirror and restore mute");
  assert.ok(!p.sets.some(([k, v]) => k === "time-pos" && v === 50),
    "must not mpv.set against a dead stream");
});

test("restarting a cast after a helper-initiated teardown does not corrupt the saved mute", () => {
  const p = loadPlugin({ mute: false });
  p.clickMenu();
  p.helperSays({ event: "stopped" });   // teardown lands off-main: only state mutates
  p.send("start", {});                   // restart before any getState poll ran
  p.send("stop", {});
  assert.equal(p.flags.mute, false, "savedMute must come from the user's real pre-cast value");
});

test("state carries duration and a subtitle label", () => {
  const p = loadPlugin();
  p.clickMenu();
  p.send("getState", {}); // simulate the page's poll so a "state" message is posted
  const s = p.state();
  assert.equal(s.duration, 120);
  assert.deepEqual(s.subs, { label: "No subtitles", warn: false });
});

test("state reports the selected subtitle track", () => {
  const p = loadPlugin({ tracks: [
    { type: "video", selected: true, codec: "hevc", "ff-index": 0 },
    { type: "audio", selected: true, codec: "aac", "ff-index": 1, "demux-channel-count": 2 },
    { type: "sub", selected: true, codec: "subrip", "ff-index": 2, lang: "eng" },
  ] });
  p.clickMenu();
  p.send("getState", {}); // simulate the page's poll so a "state" message is posted
  assert.equal(p.state().subs.label, "Subtitles: eng");
});

test("subtitle label is available before any cast starts", () => {
  const p = loadPlugin();
  p.clickMenu();
  p.send("stop");
  const s = p.state();
  assert.equal(s.phase, "idle");
  assert.equal(s.subs.label, "No subtitles");
});

// Issue #33: the "LAN address" preference reaches the helper as `serve -ip`.
// Issue #45: ffmpeg writes no colr box for a source that tags its HDR
// transfer only in the bitstream, so the helper needs mpv's reading of the
// decoded stream to announce the variant as HDR.
test("serve args carry -vrange PQ when mpv decodes a PQ transfer", () => {
  const p = loadPlugin({ gamma: "pq" });
  p.clickMenu();
  const args = serves(p)[0].args;
  assert.equal(args[args.indexOf("-vrange") + 1], "PQ");
});

test("serve args carry -vrange HLG when mpv decodes an HLG transfer", () => {
  const p = loadPlugin({ gamma: "hlg" });
  p.clickMenu();
  const args = serves(p)[0].args;
  assert.equal(args[args.indexOf("-vrange") + 1], "HLG");
});

test("serve args carry no -vrange for SDR or an unknown transfer", () => {
  for (const gamma of ["bt.1886", "auto", null]) {
    const p = loadPlugin({ gamma });
    p.clickMenu();
    assert.equal(serves(p)[0].args.indexOf("-vrange"), -1, `gamma ${gamma}`);
  }
});

test("serve args carry no -ip when the LAN address setting is empty", () => {
  const p = loadPlugin();
  p.clickMenu();
  assert.equal(serves(p)[0].args.indexOf("-ip"), -1);
});

test("serve args carry -ip when the LAN address setting is set", () => {
  const p = loadPlugin({ lanIP: "192.168.1.20" });
  p.clickMenu();
  const args = serves(p)[0].args;
  const i = args.indexOf("-ip");
  assert.notEqual(i, -1, "expected -ip in serve args");
  assert.equal(args[i + 1], "192.168.1.20");
});

test("the LAN address setting is trimmed before it reaches the helper", () => {
  const p = loadPlugin({ lanIP: "  192.168.1.20 " });
  p.clickMenu();
  const args = serves(p)[0].args;
  assert.equal(args[args.indexOf("-ip") + 1], "192.168.1.20");
});

test("a whitespace-only LAN address setting means automatic", () => {
  const p = loadPlugin({ lanIP: "   " });
  p.clickMenu();
  assert.equal(serves(p)[0].args.indexOf("-ip"), -1);
});

// Issue #38: IINA's exec stderr readabilityHandler keeps firing with "" after
// the helper's stderr hits EOF, until the process terminationHandler clears it
// (JavascriptAPIUtils.swift, exec). Those empty chunks must not reach the log.
test("empty helper stderr chunks are not logged", () => {
  const p = loadPlugin();
  p.clickMenu();
  for (let i = 0; i < 3; i++) p.helperStderr("");
  p.helperStderr("\n");
  assert.deepEqual(p.logs.filter((l) => l.startsWith("[helper:stderr]")), []);
});

test("non-empty helper stderr is logged, trimmed", () => {
  const p = loadPlugin();
  p.clickMenu();
  p.helperStderr("ffmpeg: something went wrong\n");
  assert.deepEqual(p.logs.filter((l) => l.startsWith("[helper:stderr]")),
    ["[helper:stderr] ffmpeg: something went wrong"]);
});

// Issue #42: a page-side "media error 4" arrived with no URL and no reason in
// either the sidebar or IINA's log, so a report couldn't say what failed.
const STREAM = "http://192.168.1.2:5000/master.m3u8";
const curls = (p) => p.execs.filter((e) => e.bin === "/usr/bin/curl");
const tick = () => new Promise((r) => setImmediate(r));

test("the stream URL is logged when the helper reports ready", () => {
  const p = loadPlugin();
  p.clickMenu();
  p.helperSays({ event: "ready", url: STREAM });
  assert.ok(p.logs.some((l) => l.includes(STREAM)), "expected the stream URL in the log");
});

test("a page media error probes the stream and reports why it failed", async () => {
  const p = loadPlugin({ curl: { status: 7, stdout: "000",
    stderr: "curl: (7) Failed to connect to 192.168.1.2 port 5000 after 1 ms: Couldn't connect to server" } });
  p.clickMenu();
  p.helperSays({ event: "ready", url: STREAM });
  p.send("mediaError", { url: STREAM, code: 4 });
  assert.equal(curls(p).length, 1);
  assert.equal(curls(p)[0].args.at(-1), STREAM);
  assert.ok(p.logs.some((l) => l.includes("media error 4") && l.includes(STREAM)));
  await tick();
  p.send("getState");
  const diag = p.state().diag;
  assert.equal(diag.url, STREAM);
  assert.match(diag.msg, /can't reach its own stream/);
  assert.ok(p.logs.some((l) => l.includes("Couldn't connect to server")));
});

test("a curl that fails to run still leaves a diagnosis naming the URL", async () => {
  const p = loadPlugin({ curl: new Error("no such file") });
  p.clickMenu();
  p.helperSays({ event: "ready", url: STREAM });
  p.send("mediaError", { url: STREAM, code: 4 });
  await tick();
  p.send("getState");
  assert.match(p.state().diag.msg, /media error 4/);
});

test("a media error for a stream that is no longer current is ignored", () => {
  const p = loadPlugin({ curl: { status: 0, stdout: "200", stderr: "" } });
  p.clickMenu();
  p.helperSays({ event: "ready", url: STREAM });
  p.send("mediaError", { url: "http://192.168.1.2:4000/master.m3u8", code: 4 });
  assert.equal(curls(p).length, 0);
});

test("a probe that settles after the cast was stopped is dropped", async () => {
  const p = loadPlugin({ curl: { status: 0, stdout: "200", stderr: "" } });
  p.clickMenu();
  p.helperSays({ event: "ready", url: STREAM });
  p.send("mediaError", { url: STREAM, code: 4 });
  p.send("stop");
  await tick();
  p.send("getState");
  assert.equal(p.state().diag, null);
});

// ---- AV1 / 10-bit: what startCast tells the helper about the video ----

const argAfter = (args, flag) => args[args.indexOf(flag) + 1];

test("a software-decoded 10-bit AV1 cast passes -vdepth 10 and the height", () => {
  const p = loadPlugin({
    tracks: [
      { type: "video", selected: true, codec: "av1", "ff-index": 0, "demux-h": 2160 },
      { type: "audio", selected: true, codec: "opus", "ff-index": 1, "demux-channel-count": 2 },
    ],
    videoParams: { "video-params/pixelformat": "yuv420p10" }, // no hw-pixelformat: software decode
  });
  p.clickMenu();
  const args = serves(p)[0].args;
  assert.equal(argAfter(args, "-vcodec"), "av1");
  assert.equal(argAfter(args, "-vdepth"), "10");
  assert.equal(argAfter(args, "-vheight"), "2160");
});

test("a hardware-decoded source reads its depth from hw-pixelformat", () => {
  const p = loadPlugin({
    videoParams: { "video-params/pixelformat": "videotoolbox", "video-params/hw-pixelformat": "p010" },
  });
  p.clickMenu();
  assert.equal(argAfter(serves(p)[0].args, "-vdepth"), "10");
});

test("unknown depth and height are sent as 0, not omitted", () => {
  const p = loadPlugin(); // fixture video has no demux-h, mpv reports no pixel format
  p.clickMenu();
  const args = serves(p)[0].args;
  assert.equal(argAfter(args, "-vdepth"), "0");
  assert.equal(argAfter(args, "-vheight"), "0");
});

// ---- quitting IINA: never touch mpv after the window starts closing ----
// IINA 1.5.0-beta2 on macOS 27 segfaulted in mpv_get_property (address 0x48,
// property "duration") when the page's getState poll landed after IINA had
// destroyed the mpv core during quit. A native crash: try/catch can't help.

test("after window-will-close, getState answers without calling mpv", () => {
  const p = loadPlugin();
  p.send("getState", {});
  assert.equal(p.state().duration, 120);
  p.fire("iina.window-will-close");
  p.mpvCalls.length = 0;
  p.send("getState", {});
  assert.deepEqual(p.mpvCalls, [], "mpv must not be called while the player is going away");
  assert.equal(p.state().duration, 120, "the last known duration is still reported");
});

test("after window-will-close, start, stop and tvState leave mpv alone", () => {
  const p = loadPlugin();
  p.clickMenu(); // casting, so the mirror is live
  p.fire("iina.window-will-close");
  p.mpvCalls.length = 0;
  p.send("tvState", { rate: 0, time: 10, ended: false });
  p.send("stop", {});
  p.send("start", {});
  assert.deepEqual(p.mpvCalls, []);
  assert.equal(serves(p).length, 1, "no new cast while the player is gone");
});

// Cmd-W fires window-will-close but IINA keeps running, and the next file
// reuses the same player, plugin instance and window: window-loaded does not
// fire again, iina.file-loaded does.
test("after a plain window close, the next file re-enables casting", () => {
  const p = loadPlugin();
  p.fire("iina.window-will-close");
  p.fire("iina.file-loaded");
  p.mpvCalls.length = 0;
  p.send("start", {});
  assert.equal(serves(p).length, 1, "start must cast again once a file has loaded");
  p.send("getState", {});
  assert.ok(p.mpvCalls.includes("duration"), "getState reads mpv again");
});

test("closing the window mid-cast restores mute when the next file loads", () => {
  const p = loadPlugin();
  p.clickMenu(); // casting: mpv is muted for the mirror
  assert.equal(p.flags.mute, true);
  p.fire("iina.window-will-close");
  p.fire("iina.file-loaded");
  assert.equal(p.flags.mute, false, "the user's pre-cast mute is restored");
});

test("a cast that ends while the window is closed still restores mute later", () => {
  const p = loadPlugin();
  p.clickMenu();
  p.fire("iina.window-will-close");
  p.mpvCalls.length = 0;
  p.send("stop", {}); // ends the mirror while mpv is gone
  assert.deepEqual(p.mpvCalls, [], "nothing touches mpv while it is gone");
  p.fire("iina.file-loaded");
  assert.equal(p.flags.mute, false, "the deferred mute restore lands on file-loaded");
});

test("after window-will-close, a speed change leaves mpv alone", () => {
  const p = loadPlugin();
  p.clickMenu();
  p.fire("iina.window-will-close");
  p.mpvCalls.length = 0;
  p.fire("mpv.speed.changed");
  assert.deepEqual(p.mpvCalls, []);
});

test("a speed the TV ignored is restored later if the cast ends while the window is closed", () => {
  const p = loadPlugin({ speed: 1.25 });
  p.clickMenu();
  tvIgnoresSpeed(p);
  assert.equal(p.flags.speed, 1);
  p.fire("iina.window-will-close");
  p.mpvCalls.length = 0;
  p.send("stop", {});
  assert.deepEqual(p.mpvCalls, [], "nothing touches mpv while it is gone");
  p.fire("iina.file-loaded");
  assert.equal(p.flags.speed, 1.25, "the deferred speed restore lands on file-loaded");
});
