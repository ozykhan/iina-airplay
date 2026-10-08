# LAN Address Setting Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Expose the helper's `serve -ip <addr>` override as a "LAN address" field in IINA's plugin preferences, passed through by the plugin only when non-empty.

**Architecture:** A one-field `plugin/preferences.html` bound by IINA's own injected `data-pref-key` script, two manifest keys, a pure `lanIPOverride` function in `plugin/main.js` read at cast start and appended to `serveArgs`, and the packaging chain (pack.sh, verify.sh, their tests) taught about the new file. Validation stays in the helper; its `error` event already reaches the sidebar.

**Tech Stack:** Plain ES5 JavaScript in IINA's JSContext (no bundler), `node --test` with `node:assert/strict`, bash 3.2-compatible shell scripts, `make test`.

Spec: `docs/superpowers/specs/2026-10-08-lan-address-setting-design.md`.

## Global Constraints

- `plugin/main.js` runs in IINA's JSContext: ES5 only (`var`, `function`), no `const`/`let`/arrow functions outside the test files. The `module.exports` block at the bottom of the pure section is what node tests import.
- `startCast` runs only in main-thread contexts (menu callback, `sidebar.onMessage`). Read the preference there, never inside a `utils.exec` callback or `.then`.
- Shell scripts must stay bash 3.2 compatible (macOS `/bin/bash`): no `local a=1 b=2` combined declarations, no associative arrays.
- The preference key is exactly `lanIP`; its default is the empty string `""`, which means automatic.
- The page file is exactly `plugin/preferences.html`; the manifest refers to it as `preferences.html` (relative to the plugin root).
- No `version` or `ghVersion` bump in this work.
- Never commit `plugin/Info.json` (a gitignored `make dev` symlink).
- Run the full suite with `make test` from the repository root before every commit. It must stay green.
- Commit messages end with `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>`.

---

### Task 1: `lanIPOverride` pure function

**Files:**
- Modify: `plugin/main.js` (pure-functions section, after `normalizeSource`, and the `module.exports` block)
- Test: `plugin/tests/main.test.mjs`

**Interfaces:**
- Consumes: nothing.
- Produces: `lanIPOverride(value)` — takes any value, returns a string: `value.trim()` when `value` is a string, otherwise `""`. Exported via `module.exports`. Task 3 calls it.

- [ ] **Step 1: Write the failing tests**

Append to `plugin/tests/main.test.mjs`, and add `lanIPOverride` to the destructured `require("../main.js")` on line 5:

```js
const { selectTracks, subtitleLabel, parseHelperEvents, pluginsDirFromDataDir, isValidPid, hasURLScheme, normalizeSource, lanIPOverride } = require("../main.js");
```

```js
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
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `node --test plugin/tests/main.test.mjs`
Expected: FAIL with `TypeError: lanIPOverride is not a function`.

- [ ] **Step 3: Implement `lanIPOverride`**

In `plugin/main.js`, directly after the `normalizeSource` function (before the `// ---- muted-mirror sync core` comment), add:

```js
// The "LAN address" preference (Info.json preferenceDefaults.lanIP, edited in
// Settings → Plugins → AirPlay → Preferences) is handed to the helper as
// `serve -ip <addr>` when non-empty. The helper validates it (IPv4, assigned
// to this Mac, interface up) and reports a bad value as an `error` event the
// sidebar already shows, so the plugin only trims. Anything that isn't a
// string — unset, or a stale non-string in the preferences store — means
// automatic.
function lanIPOverride(value) {
  return typeof value === "string" ? value.trim() : "";
}
```

Then add it to the `module.exports` block, after `normalizeSource: normalizeSource,`:

```js
    lanIPOverride: lanIPOverride,
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `node --test plugin/tests/main.test.mjs`
Expected: all tests pass, including the two new ones.

- [ ] **Step 5: Run the full suite and commit**

Run: `make test`
Expected: green.

```bash
git add plugin/main.js plugin/tests/main.test.mjs
git commit -m "feat(plugin): add lanIPOverride for the LAN address preference (#33)

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 2: Manifest keys and the preferences page

**Files:**
- Modify: `Info.json`
- Create: `plugin/preferences.html`
- Test: `plugin/tests/manifest.test.mjs`

**Interfaces:**
- Consumes: nothing.
- Produces: `Info.json` keys `preferencesPage: "preferences.html"` and `preferenceDefaults: { "lanIP": "" }`; the file `plugin/preferences.html` with one `<input type="text" data-pref-key="lanIP">`. Task 3 reads the `lanIP` key; Task 4 packages the file.

- [ ] **Step 1: Write the failing tests**

Append to `plugin/tests/manifest.test.mjs`. It already imports `readFileSync` from `node:fs`; add `existsSync` to that import:

```js
import { readFileSync, existsSync } from "node:fs";
```

```js
// Issue #33: the "LAN address" setting lives on an IINA preferences page.
// IINA resolves preferencesPage relative to the plugin root (the package
// root, which pack.sh fills from plugin/), reads preferenceDefaults for the
// value before the user ever opens the page, and warns-and-ignores a
// malformed preferenceDefaults — so both are asserted here, cheaply.
test("preferencesPage names a file that exists under plugin/", () => {
  assert.equal(typeof info.preferencesPage, "string");
  assert.ok(existsSync(new URL("../" + info.preferencesPage, import.meta.url)),
    `plugin/${info.preferencesPage} must exist — IINA resolves preferencesPage against the plugin root`);
});

test("preferenceDefaults.lanIP defaults to empty, meaning automatic", () => {
  assert.equal(typeof info.preferenceDefaults, "object");
  assert.equal(info.preferenceDefaults.lanIP, "");
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `node --test plugin/tests/manifest.test.mjs`
Expected: both new tests FAIL (`preferencesPage` is `undefined`; `preferenceDefaults` is `undefined`).

- [ ] **Step 3: Add the manifest keys**

Replace the whole of `Info.json` with:

```json
{
  "name": "AirPlay",
  "identifier": "dev.faruk.iina-airplay",
  "version": "0.3.2",
  "ghRepo": "ozykhan/iina-airplay",
  "ghVersion": 5,
  "author": { "name": "Faruk Can Ozkan" },
  "description": "Cast the current file to an AirPlay device: remuxes to HLS, serves on the LAN, and hands the stream to the TV. IINA stays your remote.",
  "entry": "main.js",
  "permissions": ["file-system", "show-osd"],
  "sidebarTab": { "name": "AirPlay" },
  "preferencesPage": "preferences.html",
  "preferenceDefaults": { "lanIP": "" }
}
```

(Only the last two keys are new. `version` and `ghVersion` are unchanged.)

- [ ] **Step 4: Create the preferences page**

Create `plugin/preferences.html`:

```html
<!doctype html>
<!--
  Shown under Settings → Plugins → AirPlay → Preferences. IINA injects the
  binding: every input[data-pref-key] is filled from the stored value (or
  Info.json's preferenceDefaults) and written back on every change event. It
  also injects the stylesheet that defines .pref-section and .pref-help and
  handles dark mode, so this page carries no script and almost no CSS of its
  own. The value is read by main.js at cast start (lanIPOverride) and passed
  to the helper as `serve -ip <addr>`; the helper rejects anything that is
  not an IPv4 address assigned to this Mac on an interface that is up.
-->
<html>
<head>
  <meta charset="utf-8">
  <style>
    #lanIP { width: 14em; }
  </style>
</head>
<body>
  <div class="pref-section">
    <label for="lanIP">LAN address</label>
    <input type="text" id="lanIP" data-pref-key="lanIP" placeholder="Automatic"
           autocomplete="off" autocorrect="off" autocapitalize="off" spellcheck="false">
    <p class="pref-help">
      The IPv4 address the Apple TV uses to reach this Mac. Leave it empty to
      detect it automatically.
    </p>
    <p class="pref-help">
      Set it when a cast says ready but the TV never starts playing. That
      happens with a VPN whose tunnel is not named <code>utun</code>, or when
      this Mac has more than one network interface and the wrong one is picked.
      It must be an address assigned to this Mac on the network the TV is on,
      or the next cast refuses it.
    </p>
  </div>
</body>
</html>
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `node --test plugin/tests/manifest.test.mjs`
Expected: all tests pass.

- [ ] **Step 6: Check the manifest still parses and `plugin/Info.json` is not staged**

Run: `python3 -c 'import json; json.load(open("Info.json"))' && git status --short`
Expected: no parse error; `git status` lists only `Info.json`, `plugin/preferences.html`, `plugin/tests/manifest.test.mjs`. If `plugin/Info.json` appears, it is the gitignored dev symlink — do not add it.

- [ ] **Step 7: Run the full suite and commit**

Run: `make test`
Expected: green.

```bash
git add Info.json plugin/preferences.html plugin/tests/manifest.test.mjs
git commit -m "feat(plugin): LAN address preferences page (#33)

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 3: Pass the setting to the helper as `-ip`

**Files:**
- Modify: `plugin/main.js` (IINA runtime block: the module `var` list at the top of `if (typeof iina !== "undefined")`, and `startCast`)
- Test: `plugin/tests/runtime.test.mjs`

**Interfaces:**
- Consumes: `lanIPOverride(value)` from Task 1; the `lanIP` preference key from Task 2.
- Produces: `serve` is spawned with `"-ip", <value>` appended to `serveArgs` when the trimmed preference is non-empty, and without `-ip` otherwise.

- [ ] **Step 1: Teach the fake `iina` about preferences**

In `plugin/tests/runtime.test.mjs`, inside `loadPlugin`, add a `preferences` module to the `iina` object, after the `file:` entry and before `console:`:

```js
    // Issue #33: preferences.get is synchronous in the JSContext and returns
    // the stored value or Info.json's preferenceDefaults entry. The default
    // for lanIP is "" (automatic), so that's the default here too.
    preferences: {
      get: (k) => (k === "lanIP" ? (opts.lanIP !== undefined ? opts.lanIP : "") : undefined),
    },
```

- [ ] **Step 2: Write the failing tests**

Append to `plugin/tests/runtime.test.mjs`:

```js
// Issue #33: the "LAN address" preference reaches the helper as `serve -ip`.
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
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `node --test plugin/tests/runtime.test.mjs`
Expected: the "no -ip when empty" test passes already (nothing emits `-ip` yet); the "-ip when set" and "trimmed" tests FAIL (`-ip` not found, index -1).

- [ ] **Step 4: Wire the preference into `startCast`**

In `plugin/main.js`, in the runtime block, extend the module `var` list so it reads:

```js
  var core = iina.core, mpv = iina.mpv, menu = iina.menu, sidebar = iina.sidebar,
      utils = iina.utils, file = iina.file, console = iina.console, event = iina.event,
      preferences = iina.preferences;
```

In `startCast`, directly after the `pid` guard (the block ending `state = { phase: "error", url: null, pct: 0, msg: "cannot determine IINA process id" }; return; }`) and before `var duration = mpv.getNumber("duration") || 0;`, add:

```js
    // Read here, in the menu/onMessage (main-thread) context, and capture it:
    // the serve args are built inside resolveBinDir's callback, which runs
    // from a utils.exec promise, and nothing IINA-facing is called from there.
    var lanIP = lanIPOverride(preferences.get("lanIP"));
```

Then, inside the `resolveBinDir` callback, after the `if (tracks.sub) { ... }` block and before `utils.exec(helper, serveArgs, ...)`, add:

```js
      if (lanIP) serveArgs.push("-ip", lanIP);
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `node --test plugin/tests/runtime.test.mjs`
Expected: all tests pass, including the four new ones.

- [ ] **Step 6: Run the full suite and commit**

Run: `make test`
Expected: green.

```bash
git add plugin/main.js plugin/tests/runtime.test.mjs
git commit -m "feat(plugin): pass the LAN address setting to the helper as -ip (#33)

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 4: Package and verify `preferences.html`

**Files:**
- Modify: `packaging/pack.sh` (declarations near line 17, the source existence loop near line 38, the `cp` near line 113)
- Modify: `packaging/verify.sh` (payload presence checks near line 91, staleness loop near line 103)
- Test: `packaging/tests/pack-paths.test.sh` (declaration regex near line 26, loop near line 33)
- Test: `packaging/tests/verify.test.sh` (`make_pkg` fixture, hooks, `expect_fail` list, `$stale_root` and `$sroot` fixtures)

**Interfaces:**
- Consumes: `plugin/preferences.html` from Task 2.
- Produces: the packed `.iinaplgz` carries `preferences.html` at its root next to `sidebar.html`; `verify.sh` rejects a package without it, or with one that differs from `plugin/preferences.html`.

- [ ] **Step 1: Write the failing packaging tests**

In `packaging/tests/pack-paths.test.sh`, change the declaration grep and the loop:

```bash
decls="$(grep -E '^PLUGIN_(INFO|MAIN|SIDEBAR|PREFS)=' "$PACK")"
if [ -z "$decls" ]; then
  echo "FAIL: no PLUGIN_INFO/PLUGIN_MAIN/PLUGIN_SIDEBAR/PLUGIN_PREFS declarations found in pack.sh"
  echo "      (renamed? this test is asserting nothing until it is updated)"
  exit 1
fi
eval "$decls"

for var in PLUGIN_INFO PLUGIN_MAIN PLUGIN_SIDEBAR PLUGIN_PREFS; do
```

(The loop body is unchanged. `eval "$decls"` leaves `PLUGIN_PREFS` unset until pack.sh declares it; with `set -u` the loop's `eval "printf '%s' \"\$$var\""` then errors, which is the failure.)

In `packaging/tests/verify.test.sh`:

1. In `make_pkg`, after `echo "<html></html>" > "$d/src/sidebar.html"`, add:

```bash
  echo "<html></html>" > "$d/src/preferences.html"
```

   and extend the comment above `make_pkg` so its list reads `(sidebar.html, preferences.html, bin/VERSIONS, bin/ffmpeg-LICENSE.md, bin/COPYING.LGPLv2.1)`.

2. After `drop_sidebar()   { rm -f "$1/sidebar.html"; }`, add:

```bash
drop_prefs()     { rm -f "$1/preferences.html"; }
```

3. After the `expect_fail "missing sidebar.html" ...` line, add:

```bash
expect_fail "missing preferences.html" "$(make_pkg noprefs    drop_prefs)"        "preferences.html"
```

4. In the `$stale_root` fixture, after `echo "<html></html>" > "$stale_root/sidebar.html"`, add:

```bash
echo "<html></html>" > "$stale_root/preferences.html"
```

5. In the `$sroot` fixture (the split-roots test), after `echo "<html></html>" > "$sroot/sidebar.html"`, add:

```bash
echo "<html></html>" > "$sroot/preferences.html"
```

Leave `$partial_root` alone: that test deliberately omits payload files to prove each file is compared independently, and its guard greps for the old "skipping the stale-package comparison" wording, which the per-file "skipping its stale-package comparison" note does not match.

- [ ] **Step 2: Run the packaging tests to verify they fail**

Run: `./packaging/tests/pack-paths.test.sh; ./packaging/tests/verify.test.sh`
Expected: pack-paths fails on `PLUGIN_PREFS` (unbound variable, or "declared but empty"); verify.test.sh reports `FAIL: missing preferences.html — verify.sh accepted a package it should have rejected`.

- [ ] **Step 3: Stage the page in pack.sh**

In `packaging/pack.sh`, after `PLUGIN_SIDEBAR="$ROOT/plugin/sidebar.html"`, add:

```bash
PLUGIN_PREFS="$ROOT/plugin/preferences.html"
```

Change the source existence loop to:

```bash
for f in "$PLUGIN_INFO" "$PLUGIN_MAIN" "$PLUGIN_SIDEBAR" "$PLUGIN_PREFS"; do
  [ -f "$f" ] || { echo "pack: missing $f — the plugin source tree is incomplete" >&2; exit 1; }
done
```

Change the staging copy to:

```bash
cp "$PLUGIN_INFO" "$PLUGIN_MAIN" "$PLUGIN_SIDEBAR" "$PLUGIN_PREFS" "$STAGE/"
```

- [ ] **Step 4: Check the page in verify.sh**

In `packaging/verify.sh`, after the line

```bash
[ -s "$TMP/sidebar.html" ] || fail "sidebar.html is missing or empty from the package"
```

add:

```bash
[ -s "$TMP/preferences.html" ] || fail "preferences.html is missing or empty from the package (Info.json's preferencesPage; IINA shows an empty Preferences tab without it)"
```

Change the staleness loop header to:

```bash
for rel in "$entry" sidebar.html preferences.html Info.json; do
```

(Keep `"$entry"` first: the partial-source-tree test expects the `main.js` mismatch to be the one reported.) Also update the comment above the payload checks, which lists `sidebar.html`, to read `nor sidebar.html or preferences.html`.

- [ ] **Step 5: Run the packaging tests to verify they pass**

Run: `./packaging/tests/pack-paths.test.sh; ./packaging/tests/verify.test.sh`
Expected: `all pack.sh path tests passed` and `all verify.sh tests passed`, with `ok: missing preferences.html` in the output.

- [ ] **Step 6: Run the full suite and commit**

Run: `make test`
Expected: green.

```bash
git add packaging/pack.sh packaging/verify.sh packaging/tests/pack-paths.test.sh packaging/tests/verify.test.sh
git commit -m "build: package and verify preferences.html (#33)

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 5: Docs

**Files:**
- Modify: `docs/distribution.md:164-170` (LAN address selection)
- Modify: `README.md:131-132`
- Modify: `CONTRIBUTING.md:82`

**Interfaces:**
- Consumes: the setting's name and location from Task 2.
- Produces: nothing code-facing.

- [ ] **Step 1: Update the LAN address selection section**

In `docs/distribution.md`, replace the paragraph that begins `For a manual helper run, append` and ends `The plugin has no UI setting for it.` with:

```markdown
To override the choice, enter the Mac's address on the TV's LAN in the
**LAN address** field under Settings → Plugins → AirPlay → Preferences
(`preferenceDefaults.lanIP` in `Info.json`; empty means automatic). The
plugin passes it to the helper as `serve -ip <lan-ip>` on the next cast; for
a manual helper run, append the same flag to the normal `serve` arguments.
The override rejects malformed, IPv6, loopback, link-local and unassigned
addresses, and addresses on down interfaces. It checks local assignment and
interface state, not reachability — use an interface the TV can reach. A
rejected value ends the cast with the helper's message in the sidebar. The
server still listens on `0.0.0.0`; the setting only changes the advertised
host.
```

- [ ] **Step 2: Update the README pointer**

In `README.md`, replace the two lines

```markdown
For VPNs, multiple network interfaces and the helper's manual `-ip` override,
see [LAN address selection](docs/distribution.md#lan-address-selection).
```

with:

```markdown
If a cast says ready but the TV never plays, set the **LAN address** under
Settings → Plugins → AirPlay → Preferences to the Mac's address on the TV's
network. For VPNs, multiple network interfaces and the helper's manual `-ip`
flag, see [LAN address selection](docs/distribution.md#lan-address-selection).
```

- [ ] **Step 3: Update the CONTRIBUTING layout table**

In `CONTRIBUTING.md`, replace the `plugin/` row with:

```markdown
| `plugin/` | The IINA plugin: `main.js` (JSContext), `sidebar.html` (the cast UI webview) and `preferences.html` (the settings page IINA shows under Plugins → Preferences) |
```

- [ ] **Step 4: Check the rendered links still resolve**

Run: `grep -n "lan-address-selection" README.md && grep -n "^### LAN address selection" docs/distribution.md`
Expected: both lines print; the anchor target still exists.

- [ ] **Step 5: Run the full suite and commit**

Run: `make test`
Expected: green.

```bash
git add docs/distribution.md README.md CONTRIBUTING.md
git commit -m "docs: describe the LAN address setting (#33)

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 6: Manual check in IINA

Not automatable; do it once before opening the PR, from the repository root.

- [ ] **Step 1: Link the dev build and restart IINA**

Run: `make dev`
Then quit and relaunch IINA. `make dev` symlinks `plugin/Info.json` to the root manifest and links `plugin/` into IINA, so the new page is picked up.

- [ ] **Step 2: Confirm the page renders and persists**

Open Settings → Plugins → AirPlay → Preferences. Expected: a "LAN address" field with placeholder "Automatic" and the two help paragraphs, in the same type and dark-mode treatment as other plugin pages. Type `192.0.2.1` (an address not assigned to the Mac), press Tab so the `change` event fires, switch to another Settings pane and back. Expected: the value persisted.

- [ ] **Step 3: Confirm a bad value is rejected in the sidebar**

Play a local file and run Plugins → Cast to TV. Expected: the sidebar ends in the error state with the helper's message `LAN IPv4 override "192.0.2.1" is not assigned to this Mac`.

- [ ] **Step 4: Confirm a good value is used**

Enter the Mac's real LAN address (from `ipconfig getifaddr en0`), press Tab, cast again. Expected: the sidebar reaches ready and the AirPlay picker opens. Clear the field, press Tab, cast once more. Expected: same, with the helper auto-detecting.

- [ ] **Step 5: Clean up**

Run: `git status --short`
Expected: nothing to commit. `plugin/Info.json` and `plugin/bin/` are gitignored; if either shows up, do not add it.
