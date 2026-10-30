// Runs sidebar.html's inline script against stub DOM elements and a fake
// `iina` bridge, so the page's state table and button wiring are covered by
// node --test rather than only by clicking around in IINA.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import vm from "node:vm";

const html = readFileSync(new URL("../sidebar.html", import.meta.url), "utf8");
const script = html.match(/<script>([\s\S]*?)<\/script>/)[1];

const URL_ = "http://192.168.1.2:5000/master.m3u8";

function loadPage() {
  const els = {};
  const el = (id) => (els[id] ||= { id, textContent: "", className: "", title: "",
                                    disabled: false, style: {}, onclick: null });
  const listeners = {};
  const trackListeners = {};
  const video = Object.assign(el("v"), {
    src: "", paused: true, muted: true, currentTime: 0, error: null,
    playbackRate: 1, defaultPlaybackRate: 1, seeking: false,
    seekable: { length: 0 },
    textTracks: { addEventListener: (name, cb) => { trackListeners[name] = cb; } },
    addEventListener: (name, cb) => { listeners[name] = cb; },
    play() { this.paused = false; return Promise.resolve(); },
    pause() { this.paused = true; },
    load() {},
    removeAttribute(k) { if (k === "src") this.src = ""; },
  });
  const posted = [];
  const handlers = {};
  let poll = null;
  const context = {
    document: { getElementById: el },
    iina: {
      postMessage: (name, data) => posted.push([name, data]),
      onMessage: (name, cb) => { handlers[name] = cb; },
    },
    setInterval: (cb) => { poll = cb; return 0; },
  };
  vm.runInNewContext(script, context);
  return {
    video, posted, els,
    poll: () => poll(),
    state: (s) => handlers.state(Object.assign(
      { pct: 0, msg: null, duration: 100, sync: null, subs: { label: "", warn: false } }, s)),
    fire: (name, ev) => listeners[name](ev || {}),
    addTrack: (track) => trackListeners.addtrack({ track }),
    click: (id) => els[id].onclick(),
  };
}

// The element fails once (auto-retried), then again: a page-side failure.
function failTwice(p) {
  p.video.error = { code: 3 };
  p.fire("error");
  p.fire("error");
}

test("a page-side media error shows Couldn't cast with Try again", () => {
  const p = loadPage();
  p.state({ phase: "ready", url: URL_ });
  assert.equal(p.video.src, URL_);
  failTwice(p);
  p.state({ phase: "ready", url: URL_ });
  assert.equal(p.els.headline.textContent, "Couldn't cast");
  assert.match(p.els.sub.textContent, /media error 3/);
  assert.equal(p.els.primary.textContent, "Try again");
});

// The helper is still serving: Try again reloads the stream in the page. It
// must not ask main.js to start (a no-op while a cast is live) nor restart
// the pipeline (a repackage can't fix a page-side load failure).
test("Try again after a page-side error reloads the stream, no start", () => {
  const p = loadPage();
  p.state({ phase: "ready", url: URL_ });
  failTwice(p);
  p.state({ phase: "ready", url: URL_ });
  p.posted.length = 0;

  p.click("primary");
  assert.deepEqual(p.posted.map(([n]) => n).filter((n) => n === "start" || n === "stop"), []);

  p.video.error = null;
  p.state({ phase: "ready", url: URL_ });
  assert.equal(p.video.src, URL_);
  assert.equal(p.els.headline.textContent, "Ready to send");
});

// WebKit deselects every subtitle option on the AVPlayerItem when it first
// loads the stream's tracks on the Mac, and a TV picked afterwards inherits
// that selection: no subtitles on a first cast (#38). The only subtitle track
// is the one the user had on in IINA, so the page selects it again.
test("the stream's subtitle track is selected, so the TV inherits it", () => {
  const p = loadPage();
  p.state({ phase: "ready", url: URL_ });
  const subs = { kind: "subtitles", mode: "disabled" };
  const meta = { kind: "metadata", mode: "disabled" };
  p.addTrack(subs);
  p.addTrack(meta);
  assert.equal(subs.mode, "hidden");
  assert.equal(meta.mode, "disabled");
});

test("Try again after a pipeline error starts a new cast", () => {
  const p = loadPage();
  p.state({ phase: "error", url: null, msg: "ffmpeg: exit status 1" });
  assert.equal(p.els.primary.textContent, "Try again");
  p.click("primary");
  assert.deepEqual(p.posted.map(([n]) => n).filter((n) => n === "start"), ["start"]);
});

// Issue #42: WebKit's media error 4 carries no reason, so the page hands the
// failure to main.js, which probes the URL and sends back a diagnosis.
test("a page-side media error is reported to main.js with its URL and code", () => {
  const p = loadPage();
  p.state({ phase: "ready", url: URL_ });
  p.video.error = { code: 4 };
  p.fire("error");
  assert.deepEqual(p.posted.filter(([n]) => n === "mediaError"), [], "the automatic retry is not a failure yet");
  p.fire("error");
  // JSON round-trip: the page's object literal comes from the vm's realm.
  assert.deepEqual(JSON.parse(JSON.stringify(p.posted.filter(([n]) => n === "mediaError"))),
    [["mediaError", { url: URL_, code: 4 }]]);
});

test("main.js's diagnosis for the loaded stream replaces the bare media error", () => {
  const p = loadPage();
  p.state({ phase: "ready", url: URL_ });
  p.video.error = { code: 4 };
  p.fire("error");
  p.fire("error");
  p.state({ phase: "ready", url: URL_, diag: { url: URL_, msg: "This Mac can't reach its own stream" } });
  assert.equal(p.els.headline.textContent, "Couldn't cast");
  assert.equal(p.els.sub.textContent, "This Mac can't reach its own stream");
  assert.equal(p.els.sub.title, "This Mac can't reach its own stream");
});

test("a diagnosis for another stream is not shown", () => {
  const p = loadPage();
  p.state({ phase: "ready", url: URL_ });
  p.video.error = { code: 4 };
  p.fire("error");
  p.fire("error");
  p.state({ phase: "ready", url: URL_, diag: { url: "http://192.168.1.2:4000/master.m3u8", msg: "stale" } });
  assert.match(p.els.sub.textContent, /media error 4/);
});

test("a diagnosis alone does not put the page in the error row", () => {
  const p = loadPage();
  p.state({ phase: "ready", url: URL_, diag: { url: URL_, msg: "old" } });
  assert.equal(p.els.headline.textContent, "Ready to send");
});

// ---- playback speed (issue #48) ----

const sync = (over = {}) => ({ seq: 0, paused: false, seekTo: null, startPos: 0,
                               rate: 1, ignoredRate: null, ...over });

// Loads the stream, then puts the element on the TV.
function onTV(p, s) {
  p.state({ phase: "ready", url: URL_, sync: sync(s) });
  p.video.webkitCurrentPlaybackTargetIsWireless = true;
  p.fire("webkitcurrentplaybacktargetiswirelesschanged");
  p.state({ phase: "ready", url: URL_, sync: sync(s) });
}

test("the requested rate is applied to the video, and survives a reload", () => {
  const p = loadPage();
  onTV(p, { rate: 1.25 });
  assert.equal(p.video.playbackRate, 1.25);
  // A load resets playbackRate to defaultPlaybackRate, so set both.
  assert.equal(p.video.defaultPlaybackRate, 1.25);
});

test("the poll reports the element's rate, seeking flag and sample time", () => {
  const p = loadPage();
  onTV(p, { rate: 1.25 });
  p.video.seeking = true;
  p.poll();
  const tv = p.posted.filter(([n]) => n === "tvState").at(-1)[1];
  assert.equal(tv.rate, 1.25);
  assert.equal(tv.seeking, true);
  assert.equal(typeof tv.at, "number");
});

test("the TV row names a speed the TV ignored", () => {
  const p = loadPage();
  onTV(p, { ignoredRate: 1.25 });
  assert.equal(p.els.headline.textContent, "Playing on TV");
  assert.match(p.els.sub.textContent, /1\.25×/);
  assert.equal(p.els.sub.className, "warn");
});
