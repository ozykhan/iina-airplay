# iina-airplay

An [IINA](https://github.com/iina/iina) plugin that gets the file IINA is playing
onto an Apple TV. IINA cannot AirPlay its own video output — it renders through
mpv into a Metal/OpenGL layer, and macOS only exposes AirPlay video sending
through AVFoundation. So this is a **handoff, not a mirror**: the plugin remuxes
(or, when needed, transcodes) the current file to a live HLS stream with a Go
supervisor driving `ffmpeg`, serves it on the LAN, and a hidden `<video>` element
in the sidebar hands the stream URL to the TV through WebKit's own AirPlay picker.
IINA stays the remote control. See `docs/feasibility.md` for the full reasoning
and the format-handling matrix.

**Status:** v0 core is implemented and works from source — plugin (`plugin/`),
Go helper (`helper/`), full test suite. Human end-to-end acceptance at a real TV
is pending (a person has to watch the picture and click the button). Packaged
releases (`.iinaplgz`, pinned static ffmpeg, GitHub-release install flow) are not
built yet — that's the next plan; see `docs/distribution.md`.

## Dev quickstart

```sh
brew install ffmpeg go node
make dev        # builds the helper, symlinks brew's ffmpeg into plugin/bin,
                 # links the plugin into IINA
```

Restart IINA to pick up plugin changes (`standaloneWindow`/`sidebar` are
WKWebViews and don't hot-reload JS on their own). Enable the plugin under
IINA Settings → Plugins. Plugin logs are in the same panel — select the plugin
and open its console.

On macOS 15+, grant IINA the **Local Network** permission the first time it
tries to serve, or the Apple TV can't reach the stream.

## Tests

```sh
make test       # go test ./... in helper/, plus node --test over plugin/tests/
```

## Docs

- `docs/feasibility.md` — why the direct route is closed, what's possible instead,
  and the codec/subtitle handling matrix
- `docs/prototype.md` — the two throwaway experiments that de-risked the design
  (picker-in-plugin-window, picker-in-sidebar)
- `docs/distribution.md` — the packaging and install design for strangers'
  machines (not built yet)
