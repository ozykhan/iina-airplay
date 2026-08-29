# Native-Control Casting (Muted Mirror) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** IINA's own controls (space bar, scrubber, pause icon) drive the Apple TV while casting: mpv keeps playing muted and position-synced to the TV, and stopping a cast hands playback back locally, with sound, where the TV left off.

**Architecture:** A pure decision core (`newMirror` + three `mirrorOn*` functions) lives in `plugin/main.js` next to `selectTracks`, fully node-tested. The IINA runtime block wires it to mpv events and to the existing 500ms sidebar poll, which becomes the two-way transport: `state.sync` carries commands to the page, a new `tvState` message carries TV truth back. The sidebar page applies commands to the hidden AirPlay-owning `<video>` and reports its state.

**Tech Stack:** IINA plugin JS (JavaScriptCore, ES5-style to match the file), `node --test` for unit/runtime tests, Go helper and packaging untouched.

**Spec:** `docs/superpowers/specs/2026-08-30-native-controls-mirror-design.md`

## Global Constraints

- **The TV is the clock.** mpv position is never authoritative during a cast; drift correction only ever writes TV position into mpv, never the reverse.
- **Threading rule (docs/prototype.md):** never call `sidebar.*` from `utils.exec` callbacks or `event.on` callbacks. Event callbacks may ONLY mutate the `mirror`/`state` objects. `mpv.set`/`core.*` are called only from `onMessage` handlers, menu callbacks, and `iina.file-loaded`.
- **Constants (spec values, verbatim):** `DRIFT_TOLERANCE = 1.5` seconds; `ECHO_WINDOW_MS = 2000` ms; sidebar poll stays at **500ms**.
- **No volume/mute forwarding to the TV** — explicit user decision. The page's `v.muted` flag is only for local-vs-wireless routing, never a user control.
- **Code style:** match `plugin/main.js` — `var`, `function`, ES5 patterns (`Object.assign` is fine; JSC on macOS 13+ and node both have it). Comments state constraints, not narration.
- Run the full suite with `node --test 'plugin/tests/**/*.test.mjs'` from the repo root (zsh: keep the quotes).

---

### Task 1: Pure mirror core in `main.js`

**Files:**
- Modify: `plugin/main.js` (pure-functions section, before `module.exports`; extend `module.exports`)
- Create: `plugin/tests/mirror.test.mjs`

**Interfaces:**
- Consumes: nothing from other tasks.
- Produces (Task 2 and 3 rely on these exact shapes):
  - `newMirror(startPos, savedMute, paused) → m` where `m = { seq: 0, paused, seekTo: null, startPos, savedMute, expectMpvPause: null, lastSetPos: null }`
  - `mirrorOnMpvPause(m, mpvPaused) → m'` (pure; bumps `seq` only for user-initiated changes)
  - `mirrorOnMpvSeek(m, mpvPos, now) → m'` (pure; sets `seekTo = { seq, pos }` for user seeks, swallows echoes of our own `time-pos` sets)
  - `mirrorOnTvState(m, tv, mpvPos, mpvPaused, now) → { m, setMpvPos: number|null, setMpvPaused: bool|null, teardown: bool }` where `tv = { appliedSeq, pos, paused, wireless, ended }`
  - Constants `DRIFT_TOLERANCE` (1.5) and `ECHO_WINDOW_MS` (2000), also exported for tests.

- [ ] **Step 1: Write the failing tests**

Create `plugin/tests/mirror.test.mjs`:

```js
// The muted-mirror sync core (spec 2026-08-30): the TV is the clock, mpv the
// mirror. These are the only functions where sync correctness lives, so every
// branch — echo suppression both ways, ack handling, drift — is pinned here.
import { test } from "node:test";
import assert from "node:assert/strict";
import { createRequire } from "node:module";
const require = createRequire(import.meta.url);
const { newMirror, mirrorOnMpvPause, mirrorOnMpvSeek, mirrorOnTvState,
        DRIFT_TOLERANCE, ECHO_WINDOW_MS } = require("../main.js");

const tv = (over = {}) => ({
  appliedSeq: 0, pos: 0, paused: false, wireless: true, ended: false, ...over,
});

test("newMirror snapshots start position, saved mute, and current pause", () => {
  const m = newMirror(42, true, false);
  assert.deepEqual(m, {
    seq: 0, paused: false, seekTo: null, startPos: 42, savedMute: true,
    expectMpvPause: null, lastSetPos: null,
  });
});

// ---- mirrorOnMpvPause ----

test("a user pause bumps seq and records the desired state", () => {
  const m = mirrorOnMpvPause(newMirror(0, false, false), true);
  assert.equal(m.seq, 1);
  assert.equal(m.paused, true);
});

test("an expected echo of our own mpv.set(pause) is swallowed", () => {
  const m0 = { ...newMirror(0, false, false), expectMpvPause: true };
  const m = mirrorOnMpvPause(m0, true);
  assert.equal(m.seq, 0, "an echo must not become a command back to the TV");
  assert.equal(m.expectMpvPause, null, "the echo flag is single-use");
});

test("a user change that contradicts the expected echo still wins", () => {
  // We set mpv paused (expect echo true), but the user un-paused first.
  const m0 = { ...newMirror(0, false, false), paused: true, expectMpvPause: true };
  const m = mirrorOnMpvPause(m0, false);
  assert.equal(m.seq, 1);
  assert.equal(m.paused, false);
  assert.equal(m.expectMpvPause, null);
});

test("a pause event matching the desired state is a no-op", () => {
  const m0 = { ...newMirror(0, false, false), paused: true, seq: 3 };
  const m = mirrorOnMpvPause(m0, true);
  assert.equal(m.seq, 3);
});

// ---- mirrorOnMpvSeek ----

test("a user seek bumps seq and sets seekTo with its own seq", () => {
  const m = mirrorOnMpvSeek(newMirror(0, false, false), 300, 1000);
  assert.equal(m.seq, 1);
  assert.deepEqual(m.seekTo, { seq: 1, pos: 300 });
});

test("a seek echoing our own drift correction is swallowed", () => {
  const m0 = { ...newMirror(0, false, false), lastSetPos: { pos: 100, at: 1000 } };
  const m = mirrorOnMpvSeek(m0, 100.5, 1000 + ECHO_WINDOW_MS - 1);
  assert.equal(m.seq, 0);
  assert.equal(m.seekTo, null);
  assert.equal(m.lastSetPos, null, "the echo record is single-use");
});

test("an old lastSetPos outside the echo window does not mask a user seek", () => {
  const m0 = { ...newMirror(0, false, false), lastSetPos: { pos: 100, at: 1000 } };
  const m = mirrorOnMpvSeek(m0, 100.5, 1000 + ECHO_WINDOW_MS + 1);
  assert.deepEqual(m.seekTo, { seq: 1, pos: 100.5 });
});

test("a seek far from lastSetPos is a user seek even inside the window", () => {
  const m0 = { ...newMirror(0, false, false), lastSetPos: { pos: 100, at: 1000 } };
  const m = mirrorOnMpvSeek(m0, 100 + DRIFT_TOLERANCE + 0.1, 1100);
  assert.deepEqual(m.seekTo, { seq: 1, pos: 100 + DRIFT_TOLERANCE + 0.1 });
});

// ---- mirrorOnTvState ----

test("ended tears the cast down", () => {
  const r = mirrorOnTvState(newMirror(0, false, false), tv({ ended: true }), 0, false, 0);
  assert.equal(r.teardown, true);
});

test("no wireless target means no clock to follow: no actions", () => {
  const r = mirrorOnTvState(newMirror(0, false, false), tv({ wireless: false, pos: 90 }), 10, false, 0);
  assert.equal(r.setMpvPos, null);
  assert.equal(r.setMpvPaused, null);
  assert.equal(r.teardown, false);
});

test("commands in flight (appliedSeq < seq) suspend all reconciliation", () => {
  const m0 = { ...newMirror(0, false, false), seq: 2, paused: true };
  const r = mirrorOnTvState(m0, tv({ appliedSeq: 1, pos: 90, paused: false }), 10, true, 0);
  assert.equal(r.setMpvPos, null, "drift must not be corrected against a stale TV state");
  assert.equal(r.setMpvPaused, null);
});

test("an ack clears the pending seekTo", () => {
  const m0 = { ...newMirror(0, false, false), seq: 1, seekTo: { seq: 1, pos: 300 } };
  const r = mirrorOnTvState(m0, tv({ appliedSeq: 1, pos: 300 }), 300, false, 0);
  assert.equal(r.m.seekTo, null);
});

test("a TV-remote pause is mirrored into mpv with the echo flag armed", () => {
  const r = mirrorOnTvState(newMirror(0, false, false), tv({ paused: true, pos: 10 }), 10, false, 0);
  assert.equal(r.setMpvPaused, true);
  assert.equal(r.m.paused, true, "desired state follows the TV so no command echoes back");
  assert.equal(r.m.expectMpvPause, true);
});

test("drift beyond tolerance corrects mpv to the TV clock and records the set", () => {
  const r = mirrorOnTvState(newMirror(0, false, false), tv({ pos: 50 }), 42, false, 7777);
  assert.equal(r.setMpvPos, 50);
  assert.deepEqual(r.m.lastSetPos, { pos: 50, at: 7777 });
});

test("drift within tolerance is left alone", () => {
  const r = mirrorOnTvState(newMirror(0, false, false), tv({ pos: 43 }), 42, false, 0);
  assert.equal(r.setMpvPos, null);
});

test("the mirror functions never mutate their inputs", () => {
  const m0 = newMirror(0, false, false);
  const frozen = JSON.stringify(m0);
  mirrorOnMpvPause(m0, true);
  mirrorOnMpvSeek(m0, 10, 0);
  mirrorOnTvState(m0, tv({ pos: 99, paused: true }), 0, false, 0);
  assert.equal(JSON.stringify(m0), frozen);
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `node --test plugin/tests/mirror.test.mjs`
Expected: FAIL — `newMirror` etc. are `undefined` (not yet exported).

- [ ] **Step 3: Implement the mirror core**

In `plugin/main.js`, after `normalizeSource` and before the `module.exports` block, add:

```js
// ---- muted-mirror sync core (spec: docs/superpowers/specs/2026-08-30-native-controls-mirror-design.md) ----
// The TV is the clock; mpv is the mirror. AirPlay HLS runs seconds behind any
// local clock, so mpv's position is never authoritative during a cast. These
// functions are pure — they take the mirror bookkeeping object plus
// observations and return new state/actions without touching IINA APIs — so
// node tests can drive every branch.

var DRIFT_TOLERANCE = 1.5; // seconds of mpv/TV divergence tolerated before correcting
var ECHO_WINDOW_MS = 2000; // how long our own time-pos set may echo back as an mpv seek event

function newMirror(startPos, savedMute, paused) {
  return {
    seq: 0,               // last issued command sequence number
    paused: paused,       // desired TV pause state (follows mpv)
    seekTo: null,         // pending user seek {seq, pos}; cleared when the page acks it
    startPos: startPos,   // where IINA was at cast start; the page best-effort seeks the TV here
    savedMute: savedMute, // mpv mute flag to restore on teardown
    expectMpvPause: null, // swallow the next pause.changed that echoes our own mpv.set
    lastSetPos: null,     // {pos, at} of our last programmatic time-pos set (echo suppression)
  };
}

// mpv's pause flag changed. Bumps seq only for user-initiated changes; an
// expected echo of our own mpv.set(pause) must not become a command back to
// the TV, or TV-remote pauses would ping-pong.
function mirrorOnMpvPause(m, mpvPaused) {
  var n = Object.assign({}, m);
  if (m.expectMpvPause !== null && mpvPaused === m.expectMpvPause) {
    n.expectMpvPause = null;
    return n;
  }
  n.expectMpvPause = null;
  if (mpvPaused !== m.paused) {
    n.paused = mpvPaused;
    n.seq = m.seq + 1;
  }
  return n;
}

// mpv seeked. A seek near our own recent time-pos set is drift correction
// echoing back, not the user; anything else becomes a TV seek command.
// seekTo carries its own seq so the page never re-applies a seek it has
// already performed when a later pause command bumps the outer seq.
function mirrorOnMpvSeek(m, mpvPos, now) {
  var n = Object.assign({}, m);
  if (m.lastSetPos !== null &&
      now - m.lastSetPos.at < ECHO_WINDOW_MS &&
      Math.abs(mpvPos - m.lastSetPos.pos) <= DRIFT_TOLERANCE) {
    n.lastSetPos = null;
    return n;
  }
  n.seq = m.seq + 1;
  n.seekTo = { seq: n.seq, pos: mpvPos };
  return n;
}

// The page reported TV state. Decides what (if anything) to push into mpv.
// While commands are in flight (appliedSeq < seq) the TV state is stale, so
// nothing is reconciled against it — not even drift.
function mirrorOnTvState(m, tvState, mpvPos, mpvPaused, now) {
  var out = { m: m, setMpvPos: null, setMpvPaused: null, teardown: false };
  if (tvState.ended) { out.teardown = true; return out; }
  if (!tvState.wireless) return out;      // nothing on the TV yet: no clock to follow
  if (tvState.appliedSeq < m.seq) return out;
  var n = Object.assign({}, m);
  if (m.seekTo !== null) n.seekTo = null; // acked
  if (tvState.paused !== m.paused) {      // TV-remote initiated: mirror into mpv
    n.paused = tvState.paused;
    n.expectMpvPause = tvState.paused;
    out.setMpvPaused = tvState.paused;
  }
  if (Math.abs(mpvPos - tvState.pos) > DRIFT_TOLERANCE) {
    out.setMpvPos = tvState.pos;
    n.lastSetPos = { pos: tvState.pos, at: now };
  }
  out.m = n;
  return out;
}
```

Extend the existing `module.exports` block with the new names:

```js
if (typeof module !== "undefined") {
  module.exports = {
    selectTracks: selectTracks,
    parseHelperEvents: parseHelperEvents,
    pluginsDirFromDataDir: pluginsDirFromDataDir,
    isValidPid: isValidPid,
    hasURLScheme: hasURLScheme,
    normalizeSource: normalizeSource,
    newMirror: newMirror,
    mirrorOnMpvPause: mirrorOnMpvPause,
    mirrorOnMpvSeek: mirrorOnMpvSeek,
    mirrorOnTvState: mirrorOnTvState,
    DRIFT_TOLERANCE: DRIFT_TOLERANCE,
    ECHO_WINDOW_MS: ECHO_WINDOW_MS,
  };
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `node --test plugin/tests/mirror.test.mjs`
Expected: all PASS.

Then run the full suite for regressions: `node --test 'plugin/tests/**/*.test.mjs'`
Expected: all PASS (nothing else touched yet).

- [ ] **Step 5: Commit**

```bash
git add plugin/main.js plugin/tests/mirror.test.mjs
git commit -m "plugin: pure sync core for the muted mirror"
```

---

### Task 2: Runtime wiring in `main.js` + fake-harness extension

**Files:**
- Modify: `plugin/main.js` (the `if (typeof iina !== "undefined")` block)
- Modify: `plugin/tests/runtime.test.mjs` (fake `iina` global + new tests)

**Interfaces:**
- Consumes (from Task 1): `newMirror(startPos, savedMute, paused)`, `mirrorOnMpvPause(m, mpvPaused)`, `mirrorOnMpvSeek(m, mpvPos, now)`, `mirrorOnTvState(m, tv, mpvPos, mpvPaused, now)`.
- Produces (Task 3 relies on this wire format):
  - Every `state` post to the page now has shape `{ phase, url, pct, msg, sync }` where `sync` is `null` when not casting, else `{ seq, paused, seekTo, startPos }` (`seekTo` is `{ seq, pos }` or `null`).
  - `main.js` handles a new page message `tvState` with payload `{ appliedSeq, pos, paused, wireless, ended }`.
  - IINA plugin APIs newly used: `iina.event.on(name, cb)`, `mpv.set(name, value)`, `mpv.getFlag(name)`, `mpv.getNumber("time-pos")`.

- [ ] **Step 1: Extend the fake `iina` harness in `runtime.test.mjs`**

In `loadPlugin`, add mutable flag/event tracking. Replace the `core` and `mpv` fakes and add an `event` fake (keep everything else as is):

```js
  const flags = { pause: false, mute: opts.mute || false };
  const sets = [];           // [name, value] pushed by mpv.set
  const events = {};         // event name -> callback registered via event.on
  const stdouts = [];        // per-serve stdout callbacks, for scripting helper events
```

```js
    core: { osd: (m) => osd.push(m), pause: () => { flags.pause = true; } },
    mpv: {
      getString: (k) => (k === "path" ? (opts.path !== undefined ? opts.path : "/movies/a.mkv") : null),
      getNumber: (k) => (k === "pid" ? 4321 : k === "duration" ? 120
                       : k === "time-pos" ? (opts.timePos !== undefined ? opts.timePos : 42) : 0),
      getNative: () => trackList,
      getFlag: (k) => !!flags[k],
      set: (k, v) => { sets.push([k, v]); flags[k] = v; },
    },
    event: { on: (name, cb) => { events[name] = cb; } },
```

In the `utils.exec` fake, record the stdout callback that `startCast` passes for helper serves (the signature is `exec(bin, args, cwd, onStdout, onStderr)`):

```js
      exec: (bin, args, cwd, onStdout) => {
        execs.push({ bin, args });
        if (typeof onStdout === "function") stdouts.push(onStdout);
        if (bin === "/bin/sh" && opts.binDirLookup) return Promise.resolve(opts.binDirLookup);
        return new Promise(() => {});
      },
```

Extend the returned object:

```js
    flags, sets,
    fire: (name) => { assert.ok(events[name], `no event handler for "${name}"`); events[name](); },
    helperSays: (obj) => { assert.ok(stdouts.length, "no serve running"); stdouts.at(-1)(JSON.stringify(obj) + "\n"); },
```

- [ ] **Step 2: Write the failing runtime tests**

Append to `runtime.test.mjs`:

```js
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
    { seq: 0, paused: false, seekTo: null, startPos: 42 });
  p.send("stop", {});
  assert.equal(p.state().sync, null);
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

test("the poll restores mute after the helper dies on its own", () => {
  const p = loadPlugin({ mute: false });
  p.clickMenu();
  p.helperSays({ event: "stopped" });  // helper-initiated teardown: off-main, may not touch mpv
  assert.equal(p.flags.mute, true, "the exec callback itself must not touch mpv");
  p.send("getState", {});              // the next poll runs on-message: safe to restore
  assert.equal(p.flags.mute, false);
  assert.equal(p.state().sync, null);
});
```

- [ ] **Step 3: Run the new tests to verify they fail**

Run: `node --test plugin/tests/runtime.test.mjs`
Expected: the new tests FAIL (no `event` handlers registered, `sync` undefined, mute untouched); the pre-existing tests still PASS (the harness changes are additive).

- [ ] **Step 4: Wire the mirror into the runtime block**

All changes inside `if (typeof iina !== "undefined") { ... }` in `plugin/main.js`.

Add `event` to the module destructure at the top of the block:

```js
  var core = iina.core, mpv = iina.mpv, menu = iina.menu, sidebar = iina.sidebar,
      utils = iina.utils, file = iina.file, console = iina.console, event = iina.event;
```

Below `var castGen = 0;`, add the mirror slot, the composed state, and teardown:

```js
  // Active muted mirror (spec 2026-08-30), or null when not casting. Event
  // callbacks may ONLY swap this object via the pure mirrorOn* functions;
  // everything that touches mpv runs in onMessage/menu context.
  var mirror = null;

  // Every post to the page goes through this so the page always sees the live
  // sync block alongside the pipeline state.
  function stateForPage() {
    return {
      phase: state.phase, url: state.url, pct: state.pct, msg: state.msg,
      sync: mirror === null ? null : {
        seq: mirror.seq, paused: mirror.paused,
        seekTo: mirror.seekTo, startPos: mirror.startPos,
      },
    };
  }

  function endMirror() {
    if (mirror === null) return;
    mpv.set("mute", mirror.savedMute);
    mirror = null;
  }

  // Helper-initiated teardowns (stopped/error events, exec settling) happen in
  // exec callbacks, which must not touch mpv from off-main. They only mutate
  // `state`; the page's next poll lands here (onMessage: safe) and sweeps up.
  function reapMirror() {
    if (mirror !== null && (state.phase === "idle" || state.phase === "error")) endMirror();
  }

  event.on("mpv.pause.changed", function () {
    if (mirror === null) return;
    mirror = mirrorOnMpvPause(mirror, !!mpv.getFlag("pause"));
  });
  event.on("mpv.seek", function () {
    if (mirror === null) return;
    mirror = mirrorOnMpvSeek(mirror, mpv.getNumber("time-pos") || 0, Date.now());
  });
  // A new file invalidates the whole cast: the stream on the TV is the old
  // file. file-loaded is an app event (main thread), so stopCast is safe here.
  event.on("iina.file-loaded", function () {
    if (mirror === null) return;
    stopCast();
  });
```

In `startCast`, replace the two lines

```js
    state = { phase: "starting", url: null, pct: 0, msg: null };
    core.pause();
```

with

```js
    state = { phase: "starting", url: null, pct: 0, msg: null };
    // Muted mirror: keep mpv playing, silenced, instead of pausing. The page
    // will best-effort seek the TV to startPos once wireless playback begins.
    mirror = newMirror(mpv.getNumber("time-pos") || 0, !!mpv.getFlag("mute"), !!mpv.getFlag("pause"));
    mpv.set("mute", true);
```

In `stopCast`, add `endMirror();` as the first line (before `castGen++`).

In `openSidebar`, switch the three `sidebar.postMessage("state", state)` calls to `sidebar.postMessage("state", stateForPage())`, add the reap to the poll, and register the `tvState` handler after the others:

```js
    sidebar.onMessage("getState", function () {
      reapMirror();
      sidebar.postMessage("state", stateForPage()); // onMessage handlers are main-thread safe
    });
```

```js
    sidebar.onMessage("tvState", function (tv) {
      if (mirror === null || !tv) return;
      var r = mirrorOnTvState(mirror, tv, mpv.getNumber("time-pos") || 0,
                              !!mpv.getFlag("pause"), Date.now());
      mirror = r.m;
      if (r.teardown) { stopCast(); return; }
      if (r.setMpvPaused !== null) mpv.set("pause", r.setMpvPaused);
      if (r.setMpvPos !== null) mpv.set("time-pos", r.setMpvPos);
    });
```

- [ ] **Step 5: Run the full suite**

Run: `node --test 'plugin/tests/**/*.test.mjs'`
Expected: all PASS, including every pre-existing runtime test (the "start is a no-op while a cast is already running" and error-path tests must not regress — they exercise `startCast` paths that now also create/skip the mirror).

- [ ] **Step 6: Commit**

```bash
git add plugin/main.js plugin/tests/runtime.test.mjs
git commit -m "plugin: wire the muted mirror into mpv events and the sidebar poll"
```

---

### Task 3: Sidebar page — apply commands, report TV truth, fix the unmuted-local-audio bug

**Files:**
- Modify: `plugin/sidebar.html`

**Interfaces:**
- Consumes (from Task 2): `state` messages shaped `{ phase, url, pct, msg, sync }` with `sync = { seq, paused, seekTo: {seq,pos}|null, startPos } | null`; posts `tvState { appliedSeq, pos, paused, wireless, ended }` and the existing `getState`/`start`/`stop`.
- Produces: nothing consumed by later tasks.

No node tests exist for the page script (spec: runtime glue stays thin; decision logic already lives in Task 1's pure functions). Steps here are implement → full-suite regression → commit; real verification is Task 4.

- [ ] **Step 1: Make the hidden video start muted**

Change the video tag (the `muted` attribute fixes the latent bug where the hidden element plays audibly on the Mac between "ready" and picking a TV):

```html
<video id="v" autoplay muted playsinline x-webkit-airplay="allow"></video>
```

- [ ] **Step 2: Add sync bookkeeping and the mute/wireless rule**

Add state variables next to the existing ones:

```js
  var appliedSeq = 0;
  var initialSeekPending = false;
  var ended = false;
```

Extend the wireless handler — a muted element routed to AirPlay casts silence, so mute must track the route:

```js
  v.addEventListener("webkitcurrentplaybacktargetiswirelesschanged", function () {
    onTV = !!v.webkitCurrentPlaybackTargetIsWireless;
    v.muted = !onTV;   // silent on the Mac, audible on the TV
  });
  v.addEventListener("ended", function () { ended = true; });
```

- [ ] **Step 3: Apply sync commands and handle main-initiated teardowns in `render`**

Inside `render(s)`, right after the `casting = ...` / button-state lines and before the phase text handling, add: (a) teardown of the local element when the plugin side stopped the cast (previously only the page's own Stop button cleared it — now `main.js` can stop first via `ended`/file-loaded), and (b) command application:

```js
    if (!casting && loadedURL) {
      // main.js tore the cast down (helper died, file changed, TV ended):
      // drop the stream locally too, or the element keeps AirPlaying a corpse.
      v.pause(); v.removeAttribute("src"); v.load();
      loadedURL = null; retried = false; onTV = false;
      v.muted = true; appliedSeq = 0; ended = false; initialSeekPending = false;
    }
    applySync(s.sync);
```

Add `applySync` as a top-level function in the page script:

```js
  function applySync(sync) {
    if (!sync || !loadedURL) return;
    if (sync.seq > appliedSeq) {
      // seekTo carries its own seq so a later pause command doesn't replay a
      // seek this page already performed.
      if (sync.seekTo && sync.seekTo.seq > appliedSeq) {
        v.currentTime = sync.seekTo.pos;
        initialSeekPending = false;   // an explicit user seek supersedes it
      }
      if (sync.paused && !v.paused) v.pause();
      else if (!sync.paused && v.paused) v.play().catch(function () {});
      appliedSeq = sync.seq;
    }
    if (initialSeekPending && onTV && sync.startPos > 0) {
      // Best-effort (spec): jump to where IINA was, but only once packaging
      // has reached that point; until then the TV plays from the start and
      // the mirror follows it.
      try {
        if (v.seekable.length &&
            v.seekable.end(v.seekable.length - 1) >= sync.startPos) {
          v.currentTime = sync.startPos;
          initialSeekPending = false;
        }
      } catch (e) { initialSeekPending = false; }
    }
  }
```

In the existing `render` branch that loads a new URL (`s.url !== loadedURL`), reset the per-cast bookkeeping:

```js
    if ((s.phase === "ready" || s.phase === "packaged") && s.url && s.url !== loadedURL) {
      loadedURL = s.url;
      retried = false;
      appliedSeq = 0; ended = false; initialSeekPending = true;
      v.src = s.url;
      v.play().catch(function () {});
    }
```

Also update the Stop button handler to reset the same bookkeeping (add to its clearing block): `v.muted = true; appliedSeq = 0; ended = false; initialSeekPending = false;`

- [ ] **Step 4: Report TV truth alongside the poll**

Replace the poll interval:

```js
  setInterval(function () {
    iina.postMessage("getState", {});
    if (casting && loadedURL) {
      iina.postMessage("tvState", {
        appliedSeq: appliedSeq,
        pos: v.currentTime || 0,
        paused: v.paused,
        wireless: onTV,
        ended: ended,
      });
    }
  }, 500);
```

- [ ] **Step 5: Run the full suite for regressions**

Run: `node --test 'plugin/tests/**/*.test.mjs'`
Expected: all PASS (nothing in the suite parses sidebar.html, this is a pure regression gate).

- [ ] **Step 6: Commit**

```bash
git add plugin/sidebar.html
git commit -m "plugin: sidebar page applies sync commands and reports TV truth"
```

---

### Task 4: Manual acceptance on the Apple TV

**Files:**
- Modify: `docs/prototype.md` only if a new hard-won constraint surfaces (e.g. the mpv seek event fallback below becomes necessary).

**Interfaces:**
- Consumes: the complete feature from Tasks 1–3.

- [ ] **Step 1: Install the dev build**

```bash
make dev
```

Then restart IINA (JS changes need a restart; plugin console is IINA Settings → Plugins → iina-airplay).

- [ ] **Step 2: Walk the acceptance checklist with a real file + Apple TV**

Every item is from the spec's testing section. Watch the plugin console for errors throughout.

1. Start playback in IINA ~2 minutes into a file, menu → Cast to TV, Send to TV. **Expect:** Mac audio goes silent immediately (no hidden-video audio during "Ready" either — the latent bug fix); after the TV starts, it jumps near the 2-minute mark once packaging catches up; IINA's video keeps playing silently, tracking the TV within ~1.5s.
2. Press space in IINA. **Expect:** TV pauses within ~1s; IINA's pause icon shows paused. Space again resumes both.
3. Drag IINA's scrubber forward a few minutes. **Expect:** TV follows; IINA settles back to the TV's clock (a small backwards nudge seconds later is correct behavior — the TV is the clock).
4. Pause from the TV remote. **Expect:** IINA pauses itself within ~1s and does NOT ping-pong the TV back to playing.
5. Click Stop casting. **Expect:** local playback continues from where the TV was, unmuted. If mpv was muted before the cast, verify mute is preserved instead.
6. Cast again, then load a different file in IINA mid-cast. **Expect:** cast tears down, mute restored.
7. Let a short file play to the end on the TV. **Expect:** cast tears down cleanly.

- [ ] **Step 3: Contingency — if scrubber seeks in IINA never reach the TV**

The spec flags one API risk: IINA may not forward mpv's `seek` event as `event.on("mpv.seek")`. If checklist item 3 fails while everything else works, apply the documented fallback (jump detection — `mirrorTick` consumers are position-based, so only the runtime subscription changes): replace the `mpv.seek` subscription with a poll-side check in the `getState` handler, before `reapMirror()`:

```js
      // Fallback: IINA doesn't deliver mpv.seek. Detect user seeks as jumps
      // beyond what half a second of playback (plus tolerance) can explain.
      if (mirror !== null) {
        var pos = mpv.getNumber("time-pos") || 0;
        if (lastPollPos !== null && Math.abs(pos - lastPollPos) > DRIFT_TOLERANCE + 1) {
          mirror = mirrorOnMpvSeek(mirror, pos, Date.now());
        }
        lastPollPos = pos;
      } else {
        lastPollPos = null;
      }
```

with `var lastPollPos = null;` next to `var mirror = null;`. Add a runtime test mirroring "an mpv seek becomes a seekTo command" through two `getState` polls with `timePos` changed between them (make `timePos` mutable in the harness: `p.setTimePos = (v) => { timePos = v; }`). Record the finding in `docs/prototype.md`.

- [ ] **Step 4: Commit any fallback/doc changes**

```bash
git add -A
git commit -m "plugin: mpv seek fallback + acceptance notes"
```

(Skip if the checklist passed clean with nothing to change.)
