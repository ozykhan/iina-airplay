# Sidebar Liquid Glass Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rebuild the AirPlay sidebar tab as a single translucent glass card with one phase-driven primary action, elapsed time, and a persistent subtitle line, in both macOS appearances.

**Architecture:** Two derived fields (`duration`, `subs`) are added to the existing `stateForPage()` payload in `plugin/main.js`; everything else is a rewrite of the markup, CSS, and `render()` function inside `plugin/sidebar.html`. The muted-mirror sync logic in that page is preserved verbatim. The Go helper and its event protocol are untouched.

**Tech Stack:** Plain ES5-style JavaScript in a `WKWebView` (`plugin/sidebar.html`), plain JavaScript in a `JSContext` (`plugin/main.js`), `node --test` for the pure-function and runtime tests.

Spec: `docs/superpowers/specs/2026-08-30-sidebar-liquid-glass-design.md`

## Global Constraints

- **Everything ships in `plugin/sidebar.html`.** `packaging/pack.sh:101` copies an explicit three-file list — `Info.json`, `plugin/main.js`, `plugin/sidebar.html`. A new `sidebar.css` or `sidebar.js` would be silently omitted from the package and the sidebar would ship unstyled. Do not split the file.
- **Never call `sidebar.*` from a `utils.exec` callback or promise.** IINA runs those off the main thread and `sidebar.show()` SIGABRTs the app. See the header comment in `plugin/main.js:1-4`.
- **Accent is reserved for the affirmative action** — `Start casting`, `Send to TV`, `Try again`. `Stop casting` is always a neutral glass button.
- **Preserve verbatim** in `plugin/sidebar.html`: `applySync()`, the `v.addEventListener("error", ...)` retry-once handler, the three `webkit*` listeners, the `tvState` polling block, and the teardown branch inside `render()`. These carry the muted-mirror contract from `docs/superpowers/specs/2026-08-30-native-controls-mirror-design.md`.
- **Match the existing JS style** in both files: `var`, `function` expressions, no arrow functions, no template literals.
- **No AirPlay device name exists.** WebKit exposes only the `webkitCurrentPlaybackTargetIsWireless` boolean. Never write UI that implies a named target.
- Full test suite: `make test`. Plugin tests alone: `node --test 'plugin/tests/**/*.test.mjs'`.

---

### Task 1: `subtitleLabel` pure function

**Files:**
- Modify: `plugin/main.js` (add function after `selectTracks`, which ends at line 44; add to `module.exports` at line 184)
- Test: `plugin/tests/main.test.mjs` (append)

**Interfaces:**
- Consumes: `selectTracks(trackList)`, already exported, returning `{ vcodec, acodec, achannels, vmap, amap, sub, subDropped }` or `null`. Its `sub` is `{ path, smap, lang, title }` or `null`.
- Produces: `subtitleLabel(tracks)` → `{ label: string, warn: boolean }`. Task 2 calls it; Task 4 renders `label` and uses `warn` to pick the text color.

- [ ] **Step 1: Write the failing tests**

Append to `plugin/tests/main.test.mjs`:

```javascript
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
```

Add `subtitleLabel` to the destructured `require` on line 5 of that file:

```javascript
const { selectTracks, subtitleLabel, parseHelperEvents, pluginsDirFromDataDir, isValidPid, hasURLScheme, normalizeSource } = require("../main.js");
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `node --test plugin/tests/main.test.mjs`
Expected: FAIL — `TypeError: subtitleLabel is not a function`

- [ ] **Step 3: Write the implementation**

In `plugin/main.js`, directly after the closing brace of `selectTracks` (line 44) and before `parseHelperEvents`:

```javascript
// selectTracks already decided what happens to subtitles; this just names the
// decision for the sidebar. Before the redesign that answer existed only as a
// one-shot OSD flash, which vanished before it could be read.
function subtitleLabel(tracks) {
  if (!tracks) return { label: "", warn: false };
  if (tracks.subDropped) return { label: "Subtitles not supported", warn: true };
  if (!tracks.sub) return { label: "No subtitles", warn: false };
  return { label: "Subtitles: " + (tracks.sub.lang || tracks.sub.title || "On"),
           warn: false };
}
```

In the `module.exports` block, after the `selectTracks` line:

```javascript
    subtitleLabel: subtitleLabel,
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `node --test plugin/tests/main.test.mjs`
Expected: PASS, all tests in the file

- [ ] **Step 5: Commit**

```bash
git add plugin/main.js plugin/tests/main.test.mjs
git commit -m "feat: name the subtitle decision with subtitleLabel"
```

---

### Task 2: Derive `duration` and `subs` in `stateForPage()`

**Files:**
- Modify: `plugin/main.js:223-231` (the `stateForPage` function)
- Test: `plugin/tests/runtime.test.mjs` (append)

**Interfaces:**
- Consumes: `subtitleLabel(tracks)` from Task 1.
- Produces: the `state` payload posted to the page gains `duration: number` (seconds, `0` when unknown) and `subs: { label: string, warn: boolean }`. Task 4 reads both.

The fake `iina` in `plugin/tests/runtime.test.mjs` already returns `120` for `mpv.getNumber("duration")` and the harness's `trackList` for `mpv.getNative`, so no harness changes are needed. `p.state()` returns the most recently posted state payload.

- [ ] **Step 1: Write the failing tests**

Append to `plugin/tests/runtime.test.mjs`:

```javascript
test("state carries duration and a subtitle label", () => {
  const p = loadPlugin();
  p.clickMenu();
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
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `node --test plugin/tests/runtime.test.mjs`
Expected: FAIL — `s.duration` is `undefined`, `s.subs` is `undefined`

- [ ] **Step 3: Write the implementation**

Replace `stateForPage` in `plugin/main.js` (currently lines 223-231) with:

```javascript
  function stateForPage() {
    // duration and subs are derived here, not stored on `state`: selectTracks
    // otherwise runs only at cast start, so a stored label would be empty
    // before the first cast — exactly when the user is deciding whether to
    // cast — and would go stale if the subtitle track changed mid-session.
    // Safe to read mpv here: every caller is a sidebar.onMessage handler,
    // which IINA runs on the main thread.
    return {
      phase: state.phase, url: state.url, pct: state.pct, msg: state.msg,
      duration: mpv.getNumber("duration") || 0,
      subs: subtitleLabel(selectTracks(mpv.getNative("track-list") || [])),
      sync: mirror === null ? null : {
        seq: mirror.seq, paused: mirror.paused,
        seekTo: mirror.seekTo, startPos: mirror.startPos,
      },
    };
  }
```

- [ ] **Step 4: Run the full plugin test suite to verify it passes**

Run: `node --test 'plugin/tests/**/*.test.mjs'`
Expected: PASS, including the pre-existing runtime and mirror tests

- [ ] **Step 5: Commit**

```bash
git add plugin/main.js plugin/tests/runtime.test.mjs
git commit -m "feat: send duration and subtitle status to the sidebar page"
```

---

### Task 3: The glass card — markup and tokens

**Files:**
- Modify: `plugin/sidebar.html` (the `<style>` block and the `#wrap` markup; the `<script>` gains new element handles and a rewritten `render()`)

**Interfaces:**
- Consumes: the `state` payload from Task 2 (`phase`, `pct`, `msg`, `url`, `sync`, `duration`, `subs`).
- Produces: DOM ids that Task 4 drives — `#dot`, `#headline`, `#sub`, `#bar`, `#fill`, `#times`, `#elapsed`, `#total`, `#primary`, `#secondary`. Task 4 replaces the body of `render()` and both button handlers.

This task lands a page that looks right and still casts. Task 4 makes every row of the state table correct.

- [ ] **Step 1: Replace the `<style>` block**

In `plugin/sidebar.html`, replace everything between `<style>` and `</style>` with:

```css
  :root {
    --glass-fill-top: rgba(0, 0, 0, 0.06);
    --glass-fill-bot: rgba(0, 0, 0, 0.02);
    --glass-edge: rgba(0, 0, 0, 0.08);
    --glass-spec: rgba(255, 255, 255, 0.70);
    --track: rgba(0, 0, 0, 0.10);
    --neutral-btn: rgba(0, 0, 0, 0.06);
    --ink: #1C1C1E;
    --ink-2: rgba(0, 0, 0, 0.55);
    --accent: #007AFF;
    --accent-hi: #40A0FF;
    --warn: #8A5B06;
    --shade: rgba(255, 255, 255, 0.45);
  }
  @media (prefers-color-scheme: dark) {
    :root {
      --glass-fill-top: rgba(255, 255, 255, 0.13);
      --glass-fill-bot: rgba(255, 255, 255, 0.035);
      --glass-edge: rgba(255, 255, 255, 0.10);
      --glass-spec: rgba(255, 255, 255, 0.22);
      --track: rgba(255, 255, 255, 0.13);
      --neutral-btn: rgba(255, 255, 255, 0.09);
      --ink: #F2F3F5;
      --ink-2: rgba(255, 255, 255, 0.58);
      --accent: #0A84FF;
      --accent-hi: #3D9BFF;
      --warn: #F0C169;
      --shade: rgba(255, 255, 255, 0.90);
    }
  }

  html, body { margin: 0; background: transparent; color: var(--ink);
    font: 13px/1.45 -apple-system, BlinkMacSystemFont, sans-serif;
    -webkit-font-smoothing: antialiased; }
  video { width: 1px; height: 1px; opacity: 0.01; position: absolute; }
  #wrap { padding: 12px; }

  #card {
    display: flex; flex-direction: column; gap: 12px;
    padding: 14px; border-radius: 16px;
    background: linear-gradient(180deg, var(--glass-fill-top), var(--glass-fill-bot));
    -webkit-backdrop-filter: blur(22px) saturate(180%);
    backdrop-filter: blur(22px) saturate(180%);
    border: 1px solid var(--glass-edge);
    box-shadow: inset 0 1px 0 var(--glass-spec), 0 8px 22px rgba(0, 0, 0, 0.16);
  }

  .row { display: flex; align-items: center; gap: 8px; }
  .spread { justify-content: space-between; }
  .grow { min-width: 0; }

  #head { color: var(--ink-2); }
  #head svg { width: 16px; height: 16px; flex: none; }
  #cap { font-size: 10px; font-weight: 600; letter-spacing: 0.09em;
    text-transform: uppercase; }

  #dot { width: 6px; height: 6px; border-radius: 50%; flex: none;
    background: transparent; }
  #dot.busy { background: #E0A93C; box-shadow: 0 0 7px rgba(224, 169, 60, 0.75); }
  #dot.live { background: #30C24E; box-shadow: 0 0 7px rgba(48, 194, 78, 0.75); }
  #dot.bad  { background: #E0523C; box-shadow: 0 0 7px rgba(224, 82, 60, 0.75); }

  #headline { margin: 0; font-size: 15px; font-weight: 590;
    letter-spacing: -0.01em; }
  #sub { margin: 0; font-size: 12px; color: var(--ink-2); }
  #sub.warn { color: var(--warn); }
  #headline, #sub { overflow: hidden; text-overflow: ellipsis;
    white-space: nowrap; }

  #meter { display: flex; flex-direction: column; gap: 6px; }
  #bar { height: 5px; border-radius: 3px; background: var(--track);
    overflow: hidden; }
  #fill { height: 100%; width: 0; border-radius: 3px;
    background: linear-gradient(180deg, var(--accent-hi), var(--accent));
    transition: width 0.4s cubic-bezier(0.32, 0.72, 0, 1); }
  #fill.done { background: var(--shade); }
  #times { font-size: 11.5px; color: var(--ink-2);
    font-variant-numeric: tabular-nums; }

  #actions { display: flex; flex-direction: column; gap: 6px; }
  button { display: block; width: 100%; padding: 9px 12px; border: none;
    border-radius: 11px; font: inherit; font-size: 13px; font-weight: 590;
    cursor: pointer; color: var(--ink); background: var(--neutral-btn);
    box-shadow: inset 0 1px 0 var(--glass-spec); }
  button.accent { color: #fff;
    background: linear-gradient(180deg, var(--accent-hi), var(--accent));
    box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.35),
                0 4px 12px rgba(10, 111, 232, 0.30); }
  button.quiet { background: transparent; box-shadow: none;
    color: var(--ink-2); font-weight: 500; font-size: 12.5px; padding: 5px; }
  button:disabled { color: var(--ink-2); background: var(--neutral-btn);
    box-shadow: none; cursor: default; opacity: 0.6; }
  button:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
  .hide { display: none; }

  @media (prefers-reduced-motion: reduce) { #fill { transition: none; } }
```

- [ ] **Step 2: Replace the `#wrap` markup**

Replace the `<div id="wrap">…</div>` block (leave the `<video>` element that follows it exactly as it is) with:

```html
<div id="wrap">
  <div id="card">
    <div class="row spread" id="head">
      <div class="row" style="gap:6px">
        <svg viewBox="0 0 24 24" fill="none" aria-hidden="true"><rect x="2.4" y="3.4" width="19.2" height="13" rx="2.6" stroke="currentColor" stroke-width="1.6" opacity="0.75"/><path d="M12 14.4 17 21H7z" fill="currentColor"/></svg>
        <span id="cap">AirPlay</span>
      </div>
      <span id="dot"></span>
    </div>
    <div class="grow">
      <p id="headline">Preparing…</p>
      <p id="sub"></p>
    </div>
    <div id="meter">
      <div id="bar"><div id="fill"></div></div>
      <div class="row spread" id="times">
        <span id="elapsed"></span><span id="total"></span>
      </div>
    </div>
    <div id="actions">
      <button id="primary" type="button"></button>
      <button id="secondary" type="button" class="quiet"></button>
    </div>
  </div>
</div>
```

- [ ] **Step 3: Update the element handles**

Replace the first block of `var` declarations at the top of the `<script>` — the six lines from `var v = document.getElementById("v");` through `var fill = document.getElementById("fill");` — with:

```javascript
  var v = document.getElementById("v");
  var dot = document.getElementById("dot");
  var headline = document.getElementById("headline");
  var subEl = document.getElementById("sub");
  var meter = document.getElementById("meter");
  var fill = document.getElementById("fill");
  var elapsedEl = document.getElementById("elapsed");
  var totalEl = document.getElementById("total");
  var primary = document.getElementById("primary");
  var secondary = document.getElementById("secondary");
```

Leave every other `var` in that block (`loadedURL`, `retried`, `airplayAvailable`, `onTV`, `casting`, `appliedSeq`, `initialSeekPending`, `ended`) untouched, and add two new flags at the end of it:

```javascript
  var localErr = "";        // page-side media failure, distinct from state.msg
  var primaryAct = "start"; // what #primary does: "start" | "pick" | "stop"
```

`localErr` replaces the removed `#err` paragraph. The `v.addEventListener("error", ...)` handler currently writes into `errEl`, which no longer exists — Step 4 rewires it.

- [ ] **Step 4: Make the page compile and still cast**

The old `render()`, `pick.onclick`, and `castBtn.onclick` reference `pick`, `castBtn`, `statusEl`, and `errEl`, which no longer exist. Replace `render()` with this interim version, which keeps the teardown and load branches intact, and replace both `onclick` assignments with the two handlers below. Task 4 rewrites the presentation half of `render()`.

```javascript
  function render(s) {
    casting = s.phase === "starting" || s.phase === "ready" || s.phase === "packaged";
    if (!casting && loadedURL) {
      // main.js tore the cast down (helper died, file changed, TV ended):
      // drop the stream locally too, or the element keeps AirPlaying a corpse.
      v.pause(); v.removeAttribute("src"); v.load();
      loadedURL = null; retried = false; onTV = false;
      v.muted = true; appliedSeq = 0; ended = false; initialSeekPending = false;
    }
    applySync(s.sync);
    present(s);
    if ((s.phase === "ready" || s.phase === "packaged") && s.url && s.url !== loadedURL) {
      loadedURL = s.url;
      retried = false;
      // -1, not 0: the mirror's very first sync (seq 0) carries the cast's
      // initial paused snapshot, and applySync's `sync.seq > appliedSeq` gate
      // would otherwise never let it through, autoplaying over a paused mpv.
      appliedSeq = -1; ended = false; initialSeekPending = true;
      v.src = s.url;
      v.play().catch(function () {});
    }
  }

  function present(s) {
    headline.textContent = casting ? "Casting" : "Not casting";
    subEl.textContent = (s.subs && s.subs.label) || "";
    fill.style.width = (s.pct || 0) + "%";
    primary.textContent = casting ? "Send to TV" : "Start casting";
    secondary.textContent = "Stop casting";
  }
```

```javascript
  primary.onclick = function () {
    if (casting) { if (v.webkitShowPlaybackTargetPicker) v.webkitShowPlaybackTargetPicker(); }
    else { localErr = ""; iina.postMessage("start", {}); }
  };
  secondary.onclick = function () {
    v.pause(); v.removeAttribute("src"); v.load();
    loadedURL = null; retried = false; onTV = false;
    v.muted = true; appliedSeq = 0; ended = false; initialSeekPending = false;
    localErr = "";
    iina.postMessage("stop", {});
  };
```

Two existing listeners still reference removed elements. Update the availability listener, which assigns `pick.disabled`:

```javascript
  v.addEventListener("webkitplaybacktargetavailabilitychanged", function (ev) {
    airplayAvailable = ev.availability === "available";
  });
```

And the media-error listener, whose `else` branch writes into `errEl`. Keep the retry-once behavior exactly as it is; only the reporting changes:

```javascript
  v.addEventListener("error", function () {
    if (!loadedURL) return;                // src cleared by a stop, not a real failure
    if (!retried) {                        // retry exactly once (docs/prototype.md)
      retried = true;
      v.src = loadedURL;
      v.play().catch(function () {});
    } else {
      localErr = "Stream failed to load (media error " +
        (v.error ? v.error.code : "?") + ").";
    }
  });
```

Clear `localErr` wherever a fresh attempt begins — in `teardownLocal()` (Task 4) and in the `"start"` branch of the primary handler below.

- [ ] **Step 5: Verify nothing regressed and the page loads**

Run: `make test`
Expected: PASS — this task touches no tested code, so a failure means something else broke.

Then load the plugin in IINA (`make dev`, restart IINA, open a file, run the **Cast to TV** menu item) and confirm: the glass card renders, the buttons work, and a cast still reaches the TV. Check both appearances via System Settings → Appearance.

- [ ] **Step 6: Commit**

```bash
git add plugin/sidebar.html
git commit -m "feat: rebuild the sidebar as a glass card in both appearances"
```

---

### Task 4: The state table

**Files:**
- Modify: `plugin/sidebar.html` (the `present()` function, the two button handlers, and the `webkitcurrentplaybacktargetiswirelesschanged` listener)

**Interfaces:**
- Consumes: `#dot`, `#headline`, `#sub`, `#meter`, `#fill`, `#elapsed`, `#total`, `#primary`, `#secondary` from Task 3; `s.duration` and `s.subs` from Task 2; the page-local `casting`, `onTV`, and `airplayAvailable` flags.
- Produces: nothing downstream. This is the last code task.

- [ ] **Step 1: Add the time formatter**

Insert above `present()`:

```javascript
  function clock(sec) {
    if (!(sec > 0)) return "0:00";
    var t = Math.floor(sec), h = Math.floor(t / 3600),
        m = Math.floor((t % 3600) / 60), s = t % 60;
    var mm = h > 0 && m < 10 ? "0" + m : String(m);
    return (h > 0 ? h + ":" : "") + mm + ":" + (s < 10 ? "0" + s : s);
  }
```

- [ ] **Step 2: Replace `present()` with the full state table**

```javascript
  // One row of the spec's state table per branch. `act` is what the primary
  // button does when clicked: "start", "pick", or "stop".
  function present(s) {
    var subs = s.subs || { label: "", warn: false };
    var r;
    if (s.phase === "error" || localErr) {
      // A page-side media failure is still a failed cast from the user's side,
      // so it takes the same row. state.msg wins when both are set: main.js
      // knows why the pipeline died, the <video> element only knows it choked.
      r = { dot: "bad", headline: "Couldn't cast", sub: s.msg || localErr,
            warn: true, meter: false, pct: 0, done: false,
            primary: "Try again", act: "start", accent: true, off: false,
            secondary: casting ? "Stop casting" : null };
    } else if (!casting) {
      r = { dot: "", headline: "Not casting", sub: subs.label,
            warn: subs.warn, meter: false, pct: 0, done: false,
            primary: "Start casting", act: "start", accent: true, off: false,
            secondary: null };
    } else if (s.phase === "starting") {
      r = { dot: "busy", headline: "Packaging", sub: subs.label,
            warn: subs.warn, meter: true, pct: s.pct || 0, done: false,
            primary: "Send to TV", act: "pick", accent: true, off: true,
            secondary: "Cancel" };
    } else if (onTV) {
      var pos = v.currentTime || 0;
      r = { dot: "live", headline: "Playing on TV", sub: subs.label,
            warn: subs.warn, meter: true, done: false,
            pct: s.duration > 0 ? (pos / s.duration) * 100 : 0,
            primary: "Stop casting", act: "stop", accent: false, off: false,
            secondary: "Send to another TV" };
    } else {
      r = { dot: "", headline: "Ready to send",
            sub: airplayAvailable ? subs.label : "Waiting for AirPlay devices…",
            warn: airplayAvailable && subs.warn,
            meter: true, pct: 100, done: true,
            primary: "Send to TV", act: "pick", accent: true,
            off: !airplayAvailable, secondary: "Stop casting" };
    }

    dot.className = r.dot;
    headline.textContent = r.headline;
    subEl.textContent = r.sub;
    subEl.title = r.sub;                 // the full string, when it ellipsizes
    subEl.className = r.warn ? "warn" : "";

    meter.className = r.meter ? "" : "hide";
    fill.style.width = Math.max(0, Math.min(100, r.pct)) + "%";
    fill.className = r.done ? "done" : "";
    elapsedEl.textContent = onTV ? clock(v.currentTime || 0)
                          : s.phase === "starting" ? Math.round(s.pct || 0) + "%"
                          : "Packaged";
    totalEl.textContent = s.duration > 0 ? clock(s.duration) : "";

    primary.textContent = r.primary;
    primary.disabled = r.off;
    primary.className = r.accent && !r.off ? "accent" : "";
    primaryAct = r.act;

    secondary.textContent = r.secondary || "";
    secondary.className = r.secondary ? "quiet" : "quiet hide";
  }
```

- [ ] **Step 3: Dispatch the primary action**

`primaryAct` was declared in Task 3, Step 3. Replace the two button handlers from Task 3 with:

```javascript
  function teardownLocal() {
    v.pause(); v.removeAttribute("src"); v.load();
    loadedURL = null; retried = false; onTV = false;
    v.muted = true; appliedSeq = 0; ended = false; initialSeekPending = false;
    localErr = "";
  }
  function showPicker() {
    if (v.webkitShowPlaybackTargetPicker) v.webkitShowPlaybackTargetPicker();
  }
  primary.onclick = function () {
    if (primaryAct === "pick") showPicker();
    else if (primaryAct === "stop") { teardownLocal(); iina.postMessage("stop", {}); }
    else { localErr = ""; iina.postMessage("start", {}); }
  };
  secondary.onclick = function () {
    // "Send to another TV" while playing; "Cancel"/"Stop casting" otherwise.
    if (onTV) { showPicker(); return; }
    teardownLocal();
    iina.postMessage("stop", {});
  };
```

Then replace the teardown block inside `render()` with a call to the shared helper, so the same eight assignments do not live in three places:

```javascript
    if (!casting && loadedURL) {
      // main.js tore the cast down (helper died, file changed, TV ended):
      // drop the stream locally too, or the element keeps AirPlaying a corpse.
      teardownLocal();
    }
```

- [ ] **Step 4: Keep the elapsed time ticking**

`render()` runs only when `main.js` posts state, which the page requests every 500ms — fast enough for a seconds display, so no extra timer is needed. Confirm the existing `setInterval` block at the bottom of the file still posts `getState` every 500ms and is otherwise unmodified.

- [ ] **Step 5: Run the test suite**

Run: `make test`
Expected: PASS — no tested code changed in this task; a failure means Task 2's changes regressed.

- [ ] **Step 6: Commit**

```bash
git add plugin/sidebar.html
git commit -m "feat: drive the sidebar card from the cast state table"
```

---

### Task 5: Manual verification against the state table

**Files:**
- Modify: none (fixes only, if the pass finds something)

**Interfaces:**
- Consumes: everything from Tasks 1-4.
- Produces: a verified build.

There is no DOM harness for this page — it renders in a `WKWebView` inside IINA — so this pass is the only coverage the render path gets. Do not skip it.

- [ ] **Step 1: Install the dev build**

```bash
make dev
```

Restart IINA, open a video file with a text subtitle track selected, and run the **Cast to TV** menu item.

- [ ] **Step 2: Walk every row of the state table in dark appearance**

Confirm each, in System Settings → Appearance → Dark:

- Before casting: dot hidden, "Not casting", subtitle line populated, no progress bar, one accent "Start casting" button, no secondary.
- While packaging: amber dot, "Packaging", percentage on the left of the times row, duration on the right, "Send to TV" disabled, "Cancel" below it.
- Packaged, no Apple TV powered on: "Waiting for AirPlay devices…", "Send to TV" disabled.
- Packaged, Apple TV available: "Ready to send", accent "Send to TV" enabled, "Stop casting" below.
- Playing on the TV: green dot, "Playing on TV", elapsed time ticking, **"Stop casting" is neutral, not blue**, "Send to another TV" below it.
- Error (cast a file with no audio track): red dot, "Couldn't cast", the message text, accent "Try again".
- Page-side media error: the retry-once path is hard to force deliberately; if you do see "Stream failed to load (media error …)", it must appear on the error row, not as a bare line, and "Try again" must recover.

- [ ] **Step 3: Repeat in light appearance**

Switch to System Settings → Appearance → Light and walk the same six rows. The card must stay legible — dark text on the light sidebar material, no white-on-white.

- [ ] **Step 4: Check the narrow-sidebar case**

Drag the IINA sidebar as narrow as it goes. The headline and subtitle line must ellipsize rather than wrap or overflow; both buttons stay full width; the times row must not collide.

- [ ] **Step 5: Confirm the mirror still works**

With a cast playing on the TV: press space in IINA (the TV pauses), scrub the IINA seek bar (the TV seeks), and pause from the Apple TV remote (IINA's play/pause icon follows). This is the muted-mirror contract; Tasks 3 and 4 must not have disturbed it.

- [ ] **Step 6: Commit any fixes**

```bash
git add plugin/sidebar.html
git commit -m "fix: <what the verification pass turned up>"
```

If the pass found nothing, skip this step.
