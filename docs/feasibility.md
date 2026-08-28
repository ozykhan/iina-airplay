# IINA AirPlay Plugin — Feasibility & Architecture

Status: research complete, no code written yet. Date: 2026-08-28.

## Verdict

**Yes, but not "AirPlay the mpv output."** A plugin cannot route IINA's rendered video to an
AirPlay receiver. It *can* hand the file off to the Apple TV and turn IINA into the remote
control. That is buildable today with the public plugin API plus one bundled helper binary.

## Why the direct route is closed

- IINA renders through mpv into a Metal/OpenGL layer. On macOS, AirPlay **video sending** is
  exposed only through AVFoundation's external-playback path (`AVPlayer.allowsExternalPlayback`,
  macOS 10.11+; `AVRoutePickerView`, macOS 10.15+). There is no public API to AirPlay an
  arbitrary `CALayer` or the frames mpv produces. Whole-screen mirroring is the only OS-level
  escape hatch and it is a system function, not something a plugin can toggle.
- The plugin API is JavaScript in a `JSContext`. No native code loading, no access to the render
  pipeline. Modules: `core`, `mpv`, `event`, `menu`, `overlay`, `sidebar`, `standaloneWindow`,
  `playlist`, `input`, `http` (client only), `ws` (server, but WebSocket only), `file`, `utils`,
  `preferences`.
- Audio-only AirPlay already works and needs no plugin: pick an AirPlay output device at the
  macOS level and mpv follows. (Sync/delay complaints in the IINA tracker are about this path.)

## The capability that makes it possible: `utils.exec`

From `JavascriptAPIUtils.swift`:

- `exec(file, args[], cwd?, stdoutHook?, stderrHook?) -> Promise<{status, stdout, stderr}>`
- Runs **any** binary given an absolute path, or one bundled in the plugin's `@data` directory —
  IINA even `chmod 755`s a plugin-owned binary automatically if it is not executable.
- Gated only by the `file-system` permission in `Info.json`.
- `file` without a `/` is only resolved against IINA's own bundled-binaries dir, so **always pass
  an absolute path or an `@data/...` path**.

IINA is **not App Sandboxed** (`IINA.entitlements` carries only
`allow-unsigned-executable-memory` and `disable-library-validation`), so a spawned helper can bind
a local port and talk to the LAN.

## Proposed architecture

1. **Plugin (JS)** adds a menu item "Play on Apple TV…". On invoke it reads
   `stream-open-filename` / `path`, `time-pos`, and the active audio/subtitle track via `iina.mpv`.
2. Plugin spawns the **helper** with `utils.exec` (bundled in `@data`).
3. Helper `ffprobe`s the file, decides remux vs. transcode, runs `ffmpeg` into fMP4/HLS, and
   serves it over a local HTTP server.
4. Helper does the AirPlay send. Three options, in order of preference:
   - **(A) Swift helper with AVKit** — `AVPlayer` on the local HLS URL, `allowsExternalPlayback =
     true`, `AVRoutePickerView` for the picker. Apple-sanctioned, AirPlay 2, handles pairing/auth.
   - **(B) WKWebView trick** — the plugin's `standaloneWindow` is a `WKWebView` built with a
     default `WKWebViewConfiguration`, where `allowsAirPlayForMediaPlayback` defaults to `true`.
     An `<video controls>` pointing at the local HLS URL should surface WebKit's own AirPlay
     picker. If this works, no AppKit UI is needed at all. **Prototype this first.**
   - **(C) Protocol level** — `pyatv` `play_url`, or a raw AirPlay `POST /play`. pyatv's own docs
     call AirPlay video support "very limited", and tvOS 15 folded it into AirPlay 2. Fallback only.
5. Pause IINA; keep the IINA window as the controller. Transport commands go to the helper over
   `iina.ws` or the helper's own HTTP endpoint. On stop, seek IINA back to the TV's position.

## The real work is transcoding, not AirPlay

Apple TV 4K (3rd gen) accepts H.264 / HEVC (Main, Main 10) in `.mp4` / `.m4v` / `.mov` / HLS,
Dolby Vision **Profile 5**, HDR10 / HDR10+ / HLG; audio AAC, AC-3, E-AC-3, Atmos, ALAC, FLAC.

| Source trait | Action |
|---|---|
| MKV container, H.264/HEVC inside | Remux to fMP4/HLS — no re-encode, fast |
| DTS / DTS-HD / TrueHD | Transcode to E-AC-3 or AAC — cheap |
| PGS / VOBSUB subtitles | Cannot pass through: burn in (forces full video re-encode) or use an SRT track converted to WebVTT |
| ASS/SSA styling | Will not survive as WebVTT; burn-in is the only faithful path |
| Dolby Vision Profile 7 (UHD remuxes) | Drop the enhancement layer, ship the HDR10 base layer |
| H.264 10-bit, VC-1, MPEG-2 | Full re-encode (VideoToolbox HW encode) |

IINA does **not** bundle an `ffmpeg` CLI (it links the `libav*` dylibs). So the plugin must either
require a user-installed `ffmpeg` or ship a static build — mind the GPL/LGPL implications of what
gets bundled.

## Known friction

- macOS 15+ **Local Network** privacy prompt for LAN serving; the responsible process is IINA.
- Files on network shares / seedbox mounts are fine — the helper reads and re-serves them.
  A remote URL that is already ATV-compatible could be flung directly with no local server.
- No frame-accurate position sync between IINA and the Apple TV; poll and accept drift.
- **Distribution risk to verify:** IINA installs plugins from GitHub (`ghRepo` / `ghVersion`).
  A downloaded helper binary may pick up a `com.apple.quarantine` xattr and be refused by
  Gatekeeper. Likely needs ad-hoc signing / notarization, or stripping the xattr on first run.

## Recommended first prototype (about a day)

1. By hand: `ffmpeg -i movie.mkv -c:v copy -c:a eac3 -f hls ...` into a temp dir, then
   `python3 -m http.server` over it.
2. Point Safari (and then a bare `WKWebView`) at an `<video controls>` on that URL and hit the
   AirPlay button. If the Apple TV plays it, the whole design is de-risked.
3. Only then scaffold the plugin: `Info.json` with `file-system` + `network-request` permissions,
   a menu item, and `utils.exec` wiring.

## Source evidence

- IINA source at `iina/JavascriptAPIUtils.swift`, `iina/JavascriptPlugin.swift`,
  `iina/PluginStandaloneWindow.swift`, `iina/IINA.entitlements` (shallow clone of `iina/iina`).
- IINA Plugin API docs: https://docs.iina.io/
- Apple: `AVPlayer.allowsExternalPlayback` (macOS 10.11+), `AVRoutePickerView` (macOS 10.15+)
- Apple TV 4K (3rd gen) tech specs: https://support.apple.com/en-us/111839
- pyatv supported features: https://pyatv.dev/documentation/supported_features/
- Long-standing IINA requests: issues #63, #81, #2814, #3599
