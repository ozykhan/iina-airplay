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
  const video = Object.assign(el("v"), {
    src: "", paused: true, muted: true, currentTime: 0, error: null,
    seekable: { length: 0 },
    addEventListener: (name, cb) => { listeners[name] = cb; },
    play() { this.paused = false; return Promise.resolve(); },
    pause() { this.paused = true; },
    load() {},
    removeAttribute(k) { if (k === "src") this.src = ""; },
  });
  const posted = [];
  const handlers = {};
  const context = {
    document: { getElementById: el },
    iina: {
      postMessage: (name, data) => posted.push([name, data]),
      onMessage: (name, cb) => { handlers[name] = cb; },
    },
    setInterval: () => 0,
  };
  vm.runInNewContext(script, context);
  return {
    video, posted, els,
    state: (s) => handlers.state(Object.assign(
      { pct: 0, msg: null, duration: 100, sync: null, subs: { label: "", warn: false } }, s)),
    fire: (name, ev) => listeners[name](ev || {}),
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

test("Try again after a pipeline error starts a new cast", () => {
  const p = loadPage();
  p.state({ phase: "error", url: null, msg: "ffmpeg: exit status 1" });
  assert.equal(p.els.primary.textContent, "Try again");
  p.click("primary");
  assert.deepEqual(p.posted.map(([n]) => n).filter((n) => n === "start"), ["start"]);
});
