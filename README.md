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
Go helper (`helper/`), full test suite. Text subtitles (embedded or external
SRT/ASS; the track selected in IINA) are carried as a WebVTT rendition the TV
shows via its own subtitle menu. ASS styling is flattened to plain text; image
subs (PGS/DVD) can't be cast and are dropped with a notice — burn-in is a
possible future round. The package (`.iinaplgz`, pinned static ffmpeg,
ad-hoc-signed helper) is built locally by `make pack`, verified by
`packaging/verify.sh`, and has been **installed through IINA and cast to a real
Apple TV** (2026-08-29) — so the Gatekeeper assumption the whole design rests on
holds in practice, not just on paper. CI builds, verifies and remux-tests the
package on both Apple Silicon and Intel runners and attaches it to a draft
release; see `docs/releasing.md`.

## Install

Install **through IINA**, not by downloading the package in a browser:

IINA → Settings → Plugins → Install → enter `ozykhan/iina-airplay`

IINA fetches the `.iinaplgz` from the latest GitHub release and extracts it
without applying `com.apple.quarantine`, so the bundled binaries run under
Gatekeeper with only their ad-hoc signatures. Downloading the package in a
browser and opening it by hand quarantines everything inside it, and the plugin
will tell you to reinstall through IINA when that happens.

On macOS 15+, grant IINA the **Local Network** permission the first time it
casts, or the Apple TV cannot reach the stream.

Everything the plugin needs ships inside the package — a pinned LGPL build of
ffmpeg and the Go helper. There are no prerequisites and nothing is downloaded
at runtime.

### Building the package yourself

```sh
make pack       # builds ffmpeg (slow, once), the helper, packs and verifies
```

The result is `build/iina-airplay.iinaplgz`. Install it via IINA → Settings →
Plugins → the `+` menu → Install from local package.

Building ffmpeg from source needs `nasm` (see Dev quickstart below) — the
x86_64 slice assembles hand-optimized x86 assembly with it; arm64 never
needed it, which is why this only shows up once you build the universal
binary.

## Dev quickstart

```sh
brew install ffmpeg go node
make dev        # builds the helper, symlinks brew's ffmpeg into plugin/bin,
                 # links the plugin into IINA
```

`make dev` only needs the three packages above — it uses brew's `ffmpeg`, it
never builds one from source. `nasm` is a separate prerequisite, needed only
if you'll also run `make pack` / `make ffmpeg` (see above): `brew install nasm`.

Restart IINA to pick up plugin changes (`standaloneWindow`/`sidebar` are
WKWebViews and don't hot-reload JS on their own). Enable the plugin under
IINA Settings → Plugins. Plugin logs are in the same panel — select the plugin
and open its console.

On macOS 15+, grant IINA the **Local Network** permission the first time it
tries to serve, or the Apple TV can't reach the stream.

## Tests

```sh
make test       # go test ./... in helper/, node --test over plugin/tests/,
                 # plus packaging/tests/verify.test.sh
```

## Docs

- `docs/feasibility.md` — why the direct route is closed, what's possible instead,
  and the codec/subtitle handling matrix
- `docs/prototype.md` — the three throwaway experiments that de-risked the design
  (Apple TV accepts the packaged stream, picker-in-plugin-window,
  picker-in-sidebar)
- `docs/distribution.md` — the packaging and install design for strangers'
  machines
