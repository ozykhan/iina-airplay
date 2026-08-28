# IINA AirPlay plugin

A plugin that gets the file IINA is playing onto an Apple TV. Research is done and
a throwaway prototype exists; the real plugin is not written yet.

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

## The open question that gates the design

Does WebKit's AirPlay picker appear in the plugin's own `standaloneWindow`?

- **Yes** → the plugin is JS + ffmpeg + a small HTTP server. No native code at all.
- **No** → a Swift helper is needed: `AVPlayer` with `allowsExternalPlayback = true`
  (macOS 10.11+) and `AVRoutePickerView` (macOS 10.15+), spawned via `utils.exec`.

`prototype/` answers this in about ten minutes. See `docs/prototype.md`. It needs a
human to look at the window and click the button — do not claim it passed without
the user confirming.

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
