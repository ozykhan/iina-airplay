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
    setInterval: (fn) => { poll = fn; return 0; },
    Date,
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

const sync = (over = {}) => ({ seq: 0, paused: false, seekTo: null, startPos: 0, ...over });

test("a stream loaded while IINA is paused does not start playing", () => {
  const p = loadPage();
  p.state({ phase: "ready", url: URL_, sync: sync({ paused: true }) });
  assert.equal(p.video.src, URL_);
  assert.equal(p.video.paused, true);
});

test("commands wait until the element has the stream, then apply once", () => {
  const p = loadPage();
  p.state({ phase: "ready", url: URL_, sync: sync() });          // loads, readyState 0
  p.state({ phase: "ready", url: URL_, sync: sync({ seq: 1, paused: true, seekTo: { seq: 1, pos: 300 } }) });
  assert.equal(p.video.paused, false, "a pause mid-load could be lost on the way to the TV");
  assert.equal(p.video.currentTime, 0);
  p.video.readyState = 1;
  p.state({ phase: "ready", url: URL_, sync: sync({ seq: 1, paused: true, seekTo: { seq: 1, pos: 300 } }) });
  assert.equal(p.video.paused, true);
  assert.equal(p.video.currentTime, 300);
});


test("a TV reporting playing at a frozen position is reported as waiting", () => {
  const p = loadPage();
  // Capture the poll so the test can drive it with a controlled clock.
  const page = p;
  p.state({ phase: "ready", url: URL_, sync: sync() });
  p.video.webkitCurrentPlaybackTargetIsWireless = true;
  p.fire("webkitcurrentplaybacktargetiswirelesschanged");
  p.video.paused = false; p.video.currentTime = 32.35;
  assert.ok(page.poll, "harness exposes the poll");
  const realNow = Date.now;
  try {
    let t = 1000;
    Date.now = () => t;
    page.poll();               // first sight of 32.35
    t += 2000; page.poll();    // unchanged for 2s
    const tv = p.posted.filter(([n]) => n === "tvState").at(-1)[1];
    assert.equal(tv.waiting, true);
    p.video.currentTime = 32.9;
    t += 500; page.poll();
    assert.equal(p.posted.filter(([n]) => n === "tvState").at(-1)[1].waiting, false);
    p.state({ phase: "ready", url: URL_, sync: sync() });
    assert.equal(p.els.headline.textContent, "Playing on TV");
    t += 2000; page.poll(); p.state({ phase: "ready", url: URL_, sync: sync() });
    assert.equal(p.els.headline.textContent, "TV is buffering…");
    // Stuck past RELOAD_AFTER_MS: the stream is reloaded there once (#58)...
    t += 16000; page.poll(); p.state({ phase: "ready", url: URL_, sync: sync() });
    assert.equal(p.els.headline.textContent, "Syncing with TV…");
    // ...and a TV that still doesn't move is reported as before.
    t += 1000; page.poll();
    t += 16000; page.poll(); p.state({ phase: "ready", url: URL_, sync: sync() });
    assert.equal(p.els.headline.textContent, "TV isn't responding");
    assert.equal(p.posted.filter(([n]) => n === "tvState").at(-1)[1].waiting, true,
      "still held: lifting it would restart the yank-back loop");
  } finally { Date.now = realNow; }
});

// Issue #58: an Apple TV froze mid-episode and stayed frozen until the user
// stopped, recast and pressed play. The page now reloads the stream at the
// stuck position itself, once per stall, and logs the stall either way.
function onTVAt(pos) {
  const p = loadPage();
  let loads = 0;
  let src = "";
  Object.defineProperty(p.video, "src", {
    get: () => src, set: (u) => { src = u; if (u) loads++; },
  });
  p.state({ phase: "ready", url: URL_, sync: sync() });
  p.video.readyState = 4;
  p.video.webkitCurrentPlaybackTargetIsWireless = true;
  p.fire("webkitcurrentplaybacktargetiswirelesschanged");
  p.video.paused = false; p.video.currentTime = pos;
  return Object.assign(p, {
    loads: () => loads,
    logs: () => p.posted.filter(([n]) => n === "tvLog").map(([, d]) => d),
  });
}

function withClock(fn) {
  const realNow = Date.now;
  let t = 1000;
  Date.now = () => t;
  try { fn((ms) => { t += ms; }); } finally { Date.now = realNow; }
}

test("a TV stuck for 10s gets the stream reloaded where it stopped, once", () => {
  const p = onTVAt(1162.4);
  withClock((advance) => {
    p.poll();
    advance(5000); p.poll();
    assert.equal(p.loads(), 1, "5s of buffering is left alone");
    advance(6000); p.poll();
    assert.equal(p.loads(), 2, "reloaded after 10s stuck");
    assert.equal(p.video.src, URL_);
    assert.equal(p.video.paused, false, "the reload plays: the TV was playing");
    const stuck = p.logs().at(-1);
    assert.match(stuck.msg, /^stuck at 1162\.4s for 11\.0s; reloading the stream there$/);
    assert.equal(stuck.requests, true, "the helper is asked for the TV's requests");
    assert.equal(p.posted.filter(([n]) => n === "tvState").at(-1)[1].waiting, true,
      "IINA stays held while the reload lands");
    // The new load starts at 0; once it has metadata, the TV goes back.
    p.video.currentTime = 0;
    p.fire("loadedmetadata");
    assert.equal(p.video.currentTime, 1162.4);
    // Still frozen there: no second reload for the same stall.
    for (let i = 0; i < 60; i++) { advance(500); p.poll(); }
    assert.equal(p.loads(), 2);
  });
});

test("after a reload the TV plays again, and a later stall can reload again", () => {
  const p = onTVAt(300);
  withClock((advance) => {
    p.poll();
    advance(11000); p.poll();
    assert.equal(p.loads(), 2);
    p.video.currentTime = 0;              // the new load starts at 0...
    advance(500); p.poll();
    p.fire("loadedmetadata");             // ...and jumps back: not playback yet
    advance(500); p.poll();
    assert.match(p.logs().at(-1).msg, /^stuck at/);
    p.video.currentTime = 300.5;
    advance(500); p.poll();
    assert.equal(p.logs().at(-1).msg, "playing again at 300.5s after 12.5s, after a reload");
    advance(11000); p.poll();             // stuck again at 301
    assert.equal(p.loads(), 3);
  });
});

test("a stall that clears by itself is logged both ways and not reloaded", () => {
  const p = onTVAt(50);
  withClock((advance) => {
    p.poll();
    advance(2000); p.poll();
    assert.equal(p.logs().at(-1).msg, "stopped at 50.0s while playing");
    assert.equal(p.logs().at(-1).requests, undefined);
    p.video.currentTime = 50.5;
    advance(500); p.poll();
    assert.equal(p.logs().at(-1).msg, "playing again at 50.5s after 2.5s");
    assert.equal(p.loads(), 1);
  });
});

test("a paused TV is never reloaded", () => {
  const p = onTVAt(50);
  p.video.paused = true;
  withClock((advance) => {
    for (let i = 0; i < 40; i++) { advance(500); p.poll(); }
  });
  assert.equal(p.loads(), 1);
  assert.deepEqual(p.logs().filter((l) => /stopped|stuck/.test(l.msg)), []);
});

test("a stream error mid-cast reloads at the TV's position, not at 0", () => {
  const p = onTVAt(600);
  p.video.error = { code: 2 };
  p.fire("error");
  assert.equal(p.loads(), 2);
  assert.equal(p.logs().at(-1).msg, "stream error 2; reloading at 600.0s");
  p.video.currentTime = 0;
  p.fire("loadedmetadata");
  assert.equal(p.video.currentTime, 600);
});

test("a reload that fails before landing retries at the same position", () => {
  const p = onTVAt(700);
  withClock((advance) => {
    p.poll();
    advance(11000); p.poll();             // stuck: reloaded at 700
  });
  p.video.currentTime = 0;                // the new load hasn't got metadata yet
  p.video.error = { code: 2 };
  p.fire("error");
  assert.equal(p.loads(), 3);
  p.fire("loadedmetadata");
  assert.equal(p.video.currentTime, 700);
});

test("AirPlay route changes are logged", () => {
  const p = onTVAt(10);
  assert.equal(p.logs().at(-1).msg, "AirPlay to the TV started");
  p.video.webkitCurrentPlaybackTargetIsWireless = false;
  p.fire("webkitcurrentplaybacktargetiswirelesschanged");
  assert.equal(p.logs().at(-1).msg, "AirPlay to the TV ended");
});

test("the TV's own pauses are logged with the element's state, IINA's are marked", () => {
  withClock((advance) => {
    const p = onTVAt(1111.3);
    p.video.readyState = 1; p.video.networkState = 2;
    p.video.buffered = { length: 1, end: () => 1111.4 };
    advance(5000);
    p.video.paused = true;
    p.fire("pause");                      // nothing asked for it: the TV did
    assert.equal(p.logs().at(-1).msg,
      "pause at 1111.3s; readyState 1, networkState 2, buffered to 1111.4s");
    p.video.paused = false;
    p.state({ phase: "ready", url: URL_, sync: sync({ seq: 1, paused: true }) });
    p.fire("pause");                      // IINA's pause, applied by the page
    assert.match(p.logs().at(-1).msg, /^pause at 1111\.3s \(sent from IINA\); readyState 1/);
    p.fire("waiting");
    assert.match(p.logs().at(-1).msg, /^waiting at 1111\.3s/);
  });
});
