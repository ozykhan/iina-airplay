# IINA AirPlay plugin

A plugin that gets the file IINA is playing onto an Apple TV. Shipping: the
plugin (`plugin/`), the Go helper (`helper/`) and the packaging chain are all
built, and `v0.2.0` is published and installs through IINA by repo slug. The
throwaway prototype under `prototype/` is kept as the record of what de-risked
the design, not as live code.

## The shape of the thing

IINA cannot AirPlay its own video output and never will be able to from a plugin:
it renders through mpv into a Metal/OpenGL layer, and macOS only exposes AirPlay
video sending through AVFoundation's external-playback path. So this plugin does a
**handoff**, not a mirror: package the file into something an Apple TV accepts,
serve it on the LAN, hand the URL to the TV, and leave IINA as the remote control.

Read `docs/feasibility.md` before proposing architecture changes. It has the full
reasoning and the source citations.

## Facts already established — do not re-research these

From reading the IINA source (`github.com/iina/iina`) and Apple's docs:

- **The plugin API is JavaScript in a JSContext.** No native code loading, no
  render pipeline access. Modules: `core`, `mpv`, `event`, `menu`, `overlay`,
  `sidebar`, `standaloneWindow`, `playlist`, `input`, `http` (client only),
  `ws` (a WebSocket *server*, not HTTP), `file`, `utils`, `preferences`.
- **`utils.exec` runs arbitrary binaries** — absolute paths, or paths under
  `@data/` and `@tmp/`. IINA `chmod 755`s anything it runs out of its own data
  directory. Gated on the `file-system` permission in `Info.json`.
- **`utils.exec` replaces the process environment.** It sets only `LC_ALL`, so
  `PATH` is empty in anything you spawn. Every helper must rebuild `PATH` itself.
  This bites silently — the symptom is "command not found" for a binary that is
  obviously installed.
- **A binary without `/` in its name is only looked up in IINA's own bundled
  binaries directory**, not your `PATH`. Always pass an absolute or `@data/` path.
- **IINA is not App Sandboxed.** `IINA.entitlements` carries only
  `allow-unsigned-executable-memory` and `disable-library-validation`. A spawned
  helper can bind a port and talk to the LAN.
- **`NSAllowsArbitraryLoadsInWebContent` is set** in IINA's `Info.plist`, so plain
  HTTP loads fine inside plugin WebViews. The whole WKWebView approach depends on
  this.
- **`standaloneWindow` is a `WKWebView`** built with a default
  `WKWebViewConfiguration`, where `allowsAirPlayForMediaPlayback` defaults to
  `true`. `loadFile(path)` is relative to the plugin root and **clears message
  listeners**, so register `onMessage` handlers *after* calling it. Inside the page
  the bridge is `iina.postMessage(name, data)` / `iina.onMessage(name, cb)`.
- **The Apple TV pulls the stream itself.** Serve on `0.0.0.0` and advertise the
  Mac's LAN IP; `127.0.0.1` will not work.
- **Apple TV 4K accepts** H.264 and HEVC (Main / Main 10) in MP4/M4V/MOV/HLS,
  Dolby Vision Profile 5, HDR10 / HDR10+ / HLG; audio AAC, AC-3, E-AC-3, Atmos,
  ALAC, FLAC. Not MKV, not DTS/DTS-HD, not TrueHD, not PGS subtitles.
- **IINA does not bundle an `ffmpeg` CLI**, only the `libav*` dylibs.
- **Install and update are two different mechanisms**, and satisfying one does
  not satisfy the other. Installing by slug reads
  `api.github.com/repos/<ghRepo>/releases/latest` and takes the first asset
  ending `.iinaplgz`. The update check reads
  `raw.githubusercontent.com/<ghRepo>/master/Info.json` — the **repository root
  of `master`**, never the release — and compares its `ghVersion`; only then
  does it fetch the asset. This is why `Info.json` lives at the repo root and
  `packaging/pack.sh` copies it into the package. IINA 1.4.4 folds a failed
  fetch and "no newer version" into one branch (`JavascriptPlugin.swift`,
  `checkForUpdates`), so a 404 there surfaces as **"No update found."** with no
  error — `v0.2.0` shipped perfectly and still reached nobody. Bumping
  `ghVersion` is necessary but not sufficient. The beacon is *branch state*, so
  a manifest fix reaches existing users on a plain push to `master`, with no new
  tag or rebuild. `plugin/Info.json` is a gitignored `make dev` symlink so IINA
  can load the plugin directory — never commit it: `raw.githubusercontent.com`
  serves a symlink's target path as text, not JSON. Full reasoning in
  `docs/releasing.md`.

## The design is settled — all prototype tests passed (2026-08-29, user-confirmed)

WebKit's AirPlay picker works in the plugin's `standaloneWindow` **and** in the
plugin's sidebar webview, and the packaged stream played on the user's TV both
ways. So the real plugin is **JS + ffmpeg + a small HTTP server, no native code,
and no extra window**: the cast UI lives in a sidebar tab (Info.json
`"sidebarTab"`) with a hidden 1×1 px `<video>` and a button calling
`webkitShowPlaybackTargetPicker()` — the picker requires a user gesture inside
web content, so a native menu item cannot summon it directly.

Hard-won constraints from the prototype runs (details in `docs/prototype.md`):

- **`sidebar.show()` SIGABRTs IINA when called from a `utils.exec` callback**
  (AutoLayout thread assertion; exec callbacks run off-main). `standaloneWindow.*`
  is safe from anywhere. Only call `sidebar.*` from menu/onMessage callbacks; let
  the page poll for background results.
- **The spawned helper outlives IINA** (quit *and* crash). The real plugin needs
  explicit helper kill and stale-port detection on start.
- **First media load flakes** (`error code=4`, then plays on reload) in both
  webviews. Retry once automatically.
- `fetch()` from the plugin page cannot reach the stream (CORS, `file://` origin);
  `<video>` loads are exempt.

## Dev loop

```sh
# symlink the plugin so edits are picked up on IINA restart
/Applications/IINA.app/Contents/MacOS/iina-plugin link ./iina-airplay.iinaplugin-dev
# ...and to scaffold a fresh plugin with the official TS template:
/Applications/IINA.app/Contents/MacOS/iina-plugin create <name>
```

Plugin logs: IINA Settings → Plugins → select the plugin → console. There is also a
JS dev tool (`isInspectable` is on for macOS 13.3+, so Safari's Web Inspector can
attach to plugin WebViews).

Requires a local `ffmpeg` (`brew install ffmpeg`) and, on macOS 15+, Local Network
permission granted to IINA.

## Working notes

- Prefer remuxing over re-encoding, always. A remux of an HEVC file is seconds; a
  re-encode of a UHD remux is not something anyone will wait for.
- Subtitles are the ugly part. PGS cannot pass through; burning in forces a full
  video re-encode. Text subtitles convert to WebVTT.
- The prototype is deliberately crude (one video track, one audio track, no subs,
  no seeking, helper only dies with IINA). Do not treat its shortcuts as decisions.
