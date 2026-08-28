# IINA AirPlay — de-risking prototype

> **Status: all three tests PASSED on 2026-08-29** (user-confirmed on a TV named
> "Smart TV Pro"). Test 1 with an HEVC + FLAC 5.1 source (video copied with `hvc1`
> retag, audio re-encoded to E-AC-3; packaging a 180 s clip took 0.9 s — the pure
> copy/copy remux took 0.14 s). Tests 2 and 3 both surfaced the picker and played
> on the TV. **Design settled: JS + ffmpeg + tiny HTTP server, no native code, and
> no extra window — the real plugin's UI lives in the sidebar.** See the findings
> at the bottom before writing the real plugin.

Three tests, in order. Each one kills a different assumption.

Prerequisite: `brew install ffmpeg`. The Mac and the Apple TV must be on the same
network, and macOS 15+ will ask for **Local Network** permission the first time —
grant it, or the Apple TV cannot reach the stream.

---

## Test 1 — will the Apple TV accept the stream at all? (5 minutes)

```sh
./prototype/serve.sh "/path/to/some/movie.mkv" /tmp/ap-hls 8919 180
```

It probes the file, remuxes or transcodes only what has to change, writes a
fMP4/HLS stream, prints `READY http://<your-lan-ip>:8919/`, and serves it.

Open that URL **in Safari**, click the AirPlay button in the player controls, pick
your Apple TV. If the video lands on the TV, the packaging recipe is sound and
every remaining question is about UI.

Arguments: `serve.sh <source> <outdir> [port] [seconds]`. The last one clips the
test to the first N seconds so you are not waiting on a two-hour transcode — pass
`0` for the whole file.

Things worth deliberately trying: an HEVC/HDR remux, something with DTS-HD or
TrueHD, and a 10-bit H.264 anime encode. The `NOTE re-encoding …` lines tell you
which path each file took.

Note the LAN IP: the Apple TV **pulls** the stream itself, so `127.0.0.1` is useless
here. That is why the script binds `0.0.0.0`.

---

## Test 2 — does the AirPlay picker show up inside IINA? (10 minutes)

This is the one that decides how much work the real plugin is. If WebKit's picker
appears in the plugin's own window, you never have to write the AVKit/AVPlayer
helper at all.

```sh
mkdir -p ~/Library/Application\ Support/com.colliderli.iina/plugins
cp -R prototype/iina-airplay-test.iinaplugin-dev \
      ~/Library/Application\ Support/com.colliderli.iina/plugins/
```

Restart IINA, enable the plugin in Settings → Plugins, play a file, then use
**Plugins → AirPlay Test → Cast current file to Apple TV (test)**.

It pauses IINA, runs the same `serve.sh` (embedded in `main.js`, written out to the
plugin's `@data` directory since `exec` will only run binaries from there, `@tmp`,
or an absolute path), and opens a standalone plugin window with an HTML5 `<video>`
on the stream.

**Look at the player controls in that window.** An AirPlay button means the design
collapses to: plugin + ffmpeg + tiny server, no native code. No button means you
fall back to a small Swift helper using `AVPlayer.allowsExternalPlayback` and
`AVRoutePickerView`.

Watch the plugin console (Settings → Plugins → the plugin → console) for the
`[serve]` lines if anything misbehaves.

---

## Test 3 — does the picker also work in the sidebar? (5 minutes)

Test 2 proves the design works with a standalone window. This one proves the real
plugin doesn't need any window at all: the plugin's *sidebar* tab is also a
WKWebView with a default configuration, so the same trick works inside the player
window the user already has.

Same install as test 2 (`Info.json` declares `"sidebarTab": {"name": "AirPlay"}`),
then **Plugins → AirPlay Test → Cast via sidebar (test)**. A sidebar tab opens with
a 1×1 px hidden `<video>` and a **Send to TV** button that calls
`video.webkitShowPlaybackTargetPicker()` — the picker needs a real user gesture
inside web content, which is why a native menu item can't summon it directly.

The sidebar page runs a source matrix (HLS/MP4 × LAN/loopback) and prints which
source reaches `PLAYING`. Passing looks like: `PLAYING [HLS LAN]`, then
`wireless target active: true` after picking the TV.

---

## Findings from the passing runs — read before writing the real plugin

- **`sidebar.show()` crashes IINA (SIGABRT) when called off the main thread.**
  `JavascriptAPISidebarView.show()` runs AppKit AutoLayout on whatever thread the
  JS call arrives on, and `utils.exec` callbacks run on a background thread — the
  AutoLayout thread assertion kills the whole app. `standaloneWindow.*` dispatches
  internally and is safe from anywhere. Rule: only call `sidebar.*` from menu
  callbacks or `onMessage` handlers; when a background result needs to reach the
  sidebar, let the page poll via `postMessage` instead of pushing to it.
- **The serve helper outlives IINA** — through clean quits *and* crashes. It
  orphaned twice during testing, squatting on port 8919. The real plugin needs an
  explicit stop (kill the helper) and stale-server detection at startup.
- **The first media load flakes in both webviews.** Both the standalone window and
  the sidebar showed `media error code=4` on their first-ever load of a URL that
  played fine on the next attempt. Build in one automatic retry/reload.
- **`fetch()` cannot reach the stream from the plugin page** (`file://` origin →
  CORS), but `<video>` element loads are exempt. Don't health-check the stream
  with `fetch()` from the webview; do it from the main script or the helper.
- `webkitplaybacktargetavailabilitychanged` fires `not-available` once or twice
  before `available` — enable the picker button on the event, don't gate on the
  first value.

## Two traps already accounted for

- **`utils.exec` wipes the environment.** IINA sets only `LC_ALL` on the spawned
  process, so `PATH` is empty and nothing is findable. `serve.sh` rebuilds it on
  line 1. Any helper you write later needs the same.
- **App Transport Security.** IINA's `Info.plist` sets
  `NSAllowsArbitraryLoadsInWebContent`, so a plain-HTTP stream loads fine in the
  plugin's WKWebView. Without that key this whole approach would have been dead.

## Known scope limits of the prototype

Single video + single audio track, subtitles dropped entirely, no seeking beyond
what the clip contains, and nothing ever stops the helper (it survives IINA
quitting — kill it by hand: `lsof -nP -iTCP:8919 -sTCP:LISTEN`). All of that is
deliberate — it is a yes/no experiment, not a v0.
