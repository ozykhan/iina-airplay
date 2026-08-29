// Exercises the IINA-runtime block of main.js against a fake `iina` global, so
// the sidebar<->plugin wiring (menu item, onMessage handlers, helper spawns) is
// covered by node --test rather than only by clicking around in IINA.
import { test } from "node:test";
import assert from "node:assert/strict";
import { createRequire } from "node:module";
const require = createRequire(import.meta.url);
const MAIN = require.resolve("../main.js");

function loadPlugin() {
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
      getString: (k) => (k === "path" ? "/movies/a.mkv" : null),
      getNumber: (k) => (k === "pid" ? 4321 : k === "duration" ? 120 : 0),
      getNative: () => ([
        { type: "video", selected: true, codec: "hevc", "ff-index": 0 },
        { type: "audio", selected: true, codec: "aac", "ff-index": 1, "demux-channel-count": 2 },
      ]),
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
      exec: (bin, args) => { execs.push({ bin, args }); return new Promise(() => {}); },
    },
    file: { read: () => "/plugins/dev.faruk.iina-airplay/bin", write: () => {} },
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
