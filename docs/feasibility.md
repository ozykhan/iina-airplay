# IINA AirPlay Plugin — Feasibility & Architecture

Research done 2026-08-28; the open questions it lists were closed by the
prototype on 2026-08-29 and the design has shipped since. This doc is kept for
the **why** — why the obvious route is impossible, and what the format handling
has to do. For what was actually tried, see `docs/prototype.md`; for how the
package reaches a stranger's Mac, `docs/distribution.md`.

## Verdict

**Yes, but not "AirPlay the mpv output."** A plugin cannot route IINA's rendered video to an
AirPlay receiver. It *can* hand the file off to the Apple TV and turn IINA into the remote
control. That is buildable with the public plugin API plus bundled helper binaries — no native
code in IINA's process.

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

## The architecture that shipped

1. **Plugin (JS)** reads `path`, `time-pos` and the selected video/audio/subtitle tracks via
   `iina.mpv`, and spawns the **Go helper** with `utils.exec` by absolute path out of the
   package's own `bin/`.
2. **Helper** decides remux vs. transcode from the track info it is handed, runs the bundled
   `ffmpeg` into fMP4/HLS, and serves the output directory on `0.0.0.0` on a free port. It
   reports `ready` / `progress` / `packaged` / `error` as JSON lines on stdout, and dies with
   IINA via a PPID watchdog.
3. **The AirPlay send happens in web content, not in a helper.** The plugin's sidebar tab is a
   `WKWebView` with a default configuration, where `allowsAirPlayForMediaPlayback` defaults to
   `true`; a hidden 1×1 px `<video>` on the stream URL plus a button calling
   `webkitShowPlaybackTargetPicker()` surfaces WebKit's own picker. The picker requires a user
   gesture inside web content, which is why a native menu item cannot summon it.

   This was option (B) of three — the alternatives were a Swift helper using `AVPlayer` /
   `AVRoutePickerView`, and driving the TV at protocol level via `pyatv`. (B) winning is what
   collapsed the design to JS + ffmpeg + a small server with no native code and no extra window.
   `docs/prototype.md` is the record of that test.
4. **IINA stays the remote, literally.** Rather than pausing on handoff, mpv keeps playing
   *muted* as a mirror of the TV: IINA's own play/pause/seek drive the Apple TV, and pause or
   seek from the TV remote mirrors back into mpv. The TV is the clock; the sync core is pure and
   unit-tested (`plugin/main.js`, spec in
   `docs/superpowers/specs/2026-08-30-native-controls-mirror-design.md`).

Note that the shipped manifest declares only `file-system` and `show-osd`. `network-request` is
deliberately absent: neither the plugin nor the helper makes an outbound request at runtime.

## The real work is transcoding, not AirPlay

Apple TV 4K (3rd gen) accepts H.264 / HEVC (Main, Main 10) in `.mp4` / `.m4v` / `.mov` / HLS,
Dolby Vision **Profile 5**, HDR10 / HDR10+ / HLG; audio AAC, AC-3, E-AC-3, Atmos, ALAC, FLAC.

| Source trait | Action |
|---|---|
| MKV container, H.264/HEVC inside | Remux to fMP4/HLS — no re-encode, fast |
| DTS / DTS-HD / TrueHD | Transcode to E-AC-3 or AAC — cheap |
| Text subtitles (SRT/ASS/SSA, embedded or external) | Converted to WebVTT and carried as an HLS rendition the TV shows in its own subtitle menu; ASS styling flattens to plain text |
| PGS / VOBSUB subtitles | Cannot pass through. Burn-in forces a full video re-encode, so v0 drops them and says so; burn-in is a possible future round |
| Dolby Vision Profile 7 (UHD remuxes) | Drop the enhancement layer, ship the HDR10 base layer |
| H.264 10-bit, VC-1, MPEG-2 | Full re-encode (VideoToolbox HW encode) |

IINA does **not** bundle an `ffmpeg` CLI (it links the `libav*` dylibs), so the plugin ships its
own: a pinned static **LGPL** build, which is what keeps the plugin itself MIT-licensable. The
build recipe and the compliance obligations are in `docs/distribution.md`.

## Known friction

- macOS 15+ **Local Network** privacy prompt for LAN serving; the responsible process is IINA.
- Files on network shares / seedbox mounts are fine — the helper reads and re-serves them.
  Remote URLs as *sources* are declined: the bundled ffmpeg is built `--disable-network`.
- No frame-accurate position sync between IINA and the Apple TV. The muted mirror corrects drift
  toward the TV rather than trying to be sample-exact.
- **A cast costs disk for its duration.** The remux is a byte-for-byte copy of the video and audio
  bitstreams into `@tmp/hls`, so a UHD remux means tens of gigabytes of temp while the cast runs.
  `-hls_playlist_type event` is what makes the TV timeline seekable, so segments cannot be expired
  mid-cast; instead the helper sweeps the directory on every exit path — stop, IINA quitting or
  crashing, a failed cast — and again at the next start in case it was killed outright.
- **Gatekeeper — resolved, not open.** A binary inside a package IINA downloads and extracts
  itself picks up only `com.apple.provenance`, never `com.apple.quarantine`, so ad-hoc signing
  is enough and no Developer ID is needed. Verified against IINA's source *and* by experiment,
  then confirmed end to end by installing `v0.1.0` from the published release. The consequence
  is that users must install **through IINA** — a browser download quarantines everything
  inside. Full record in `docs/distribution.md`.

## Source evidence

- IINA source at `iina/JavascriptAPIUtils.swift`, `iina/JavascriptPlugin.swift`,
  `iina/PluginStandaloneWindow.swift`, `iina/IINA.entitlements` (shallow clone of `iina/iina`).
- IINA Plugin API docs: https://docs.iina.io/
- Apple: `AVPlayer.allowsExternalPlayback` (macOS 10.11+), `AVRoutePickerView` (macOS 10.15+)
- Apple TV 4K (3rd gen) tech specs: https://support.apple.com/en-us/111839
- pyatv supported features: https://pyatv.dev/documentation/supported_features/
- Long-standing IINA requests: issues #63, #81, #2814, #3599
