# IINA AirPlay — de-risking prototype

Two tests, in order. Each one kills a different assumption. Do not write any real
code until both pass.

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

## Two traps already accounted for

- **`utils.exec` wipes the environment.** IINA sets only `LC_ALL` on the spawned
  process, so `PATH` is empty and nothing is findable. `serve.sh` rebuilds it on
  line 1. Any helper you write later needs the same.
- **App Transport Security.** IINA's `Info.plist` sets
  `NSAllowsArbitraryLoadsInWebContent`, so a plain-HTTP stream loads fine in the
  plugin's WKWebView. Without that key this whole approach would have been dead.

## Known scope limits of the prototype

Single video + single audio track, subtitles dropped entirely, no seeking beyond
what the clip contains, and the helper only stops when IINA quits. All of that is
deliberate — it is a yes/no experiment, not a v0.
