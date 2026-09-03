# iina-airplay

**Cast what IINA is playing straight to your Apple TV.** No screen mirroring, no
re-encode — the plugin hands the file to the TV, and IINA stays the remote.

<img src="https://github.com/user-attachments/assets/57781b27-13fe-4c9a-a96c-7818986025c0" width="800" height="365" alt="The iina-airplay sidebar casting to an Apple TV">

<sub>Demo footage: _Sintel_ © [Blender Foundation](https://durian.blender.org), licensed [CC BY 3.0](https://creativecommons.org/licenses/by/3.0/).</sub>

```
IINA → Settings → Plugins → Install → ozykhan/iina-airplay
```

macOS 12+ · any AirPlay 2 receiver · MIT · everything bundled, nothing downloads at runtime

- **The picture is the file.** Remux-first, so in the normal case the TV plays the
  original bitstream bit-for-bit. Only unusual codecs get re-encoded, through VideoToolbox.
- **IINA is the remote, both ways.** Play, pause and seek in IINA drive the Apple TV;
  the Siri Remote comes back the other way.
- **Your subtitles come along.** The text track you selected in IINA (embedded or
  external SRT/ASS) is carried as a WebVTT rendition the TV shows through its own menu.
- **Nothing to install first.** A pinned LGPL ffmpeg build and a small Go helper ship
  inside the package. No Homebrew, no runtime downloads, works offline.

### Limits, up front

|                 |                                                                                                                                                                     |
| --------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| macOS 15+       | Grant IINA the **Local Network** permission on first cast, or the TV can't reach the stream                                                                         |
| Image subtitles | PGS/VOBSUB are dropped with a notice rather than burned in. SRT/ASS work                                                                                            |
| Start position  | The TV starts from the beginning, then jumps to where IINA was once packaging has reached that point. Best-effort: if the TV ends up elsewhere, IINA follows the TV |
| Disk space      | A cast writes a second copy of the file (the HLS remux) to IINA's temp directory for as long as it runs; it is deleted when the cast stops or IINA quits            |

<details>
<summary><b>Why this is a handoff and not a mirror</b></summary>

IINA cannot AirPlay its own video output: it renders through mpv into a
Metal/OpenGL layer, and macOS only exposes AirPlay video sending through
AVFoundation. So instead of mirroring the window, the plugin remuxes (or, when
needed, transcodes) the current file to a live HLS stream with a Go supervisor
driving `ffmpeg`, serves it on the LAN, and a hidden `<video>` element in the
sidebar hands the stream URL to the TV through WebKit's own AirPlay picker. The
player keeps running muted as a mirror, which is what makes the two-way remote
control work. `docs/feasibility.md` has the full reasoning and the format-handling
matrix.

</details>

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
                 # and the packaging shell tests
```

CI runs exactly this. The helper's real-media end-to-end tests **skip themselves**
when no `ffmpeg` is on `PATH` and `IINA_AIRPLAY_FFMPEG` is unset — so without
Homebrew's ffmpeg the suite goes green having never driven the pipeline. If you're
touching the transcode path, check those tests actually ran.

## Contributing

Contributions are welcome. [`CONTRIBUTING.md`](CONTRIBUTING.md) covers the dev
loop, the test suite, what you can verify without an Apple TV, the branch and
issue conventions, and the handful of platform constraints that bite everyone
once. New here? Look for
[`good first issue`](https://github.com/ozykhan/iina-airplay/labels/good%20first%20issue).

Trunk-based: branch off `master`, open a PR back into it, squash-merge; CI's
`test` gate must pass. `master` is not a stylistic choice — IINA's update check
reads `Info.json` from that branch by name, so renaming it would cut existing
installs off from updates.

## Docs

- [ozykhan.github.io/iina-airplay](https://ozykhan.github.io/iina-airplay/) — the
  landing page, served by GitHub Pages from `docs/` on `master`
- `docs/feasibility.md` — why the direct route is closed, what's possible instead,
  and the codec/subtitle handling matrix
- `docs/prototype.md` — the three throwaway experiments that de-risked the design
  (Apple TV accepts the packaged stream, picker-in-plugin-window,
  picker-in-sidebar)
- `docs/distribution.md` — the packaging and install design for strangers'
  machines
- `docs/releasing.md` — the release runbook, and why install and update are two
  different mechanisms that must both be satisfied
