# Distribution, round 1: a real `.iinaplgz` built locally

> Design settled 2026-08-29. Implements the packaging half of
> `docs/distribution.md`, which decided *what* to ship and verified the
> Gatekeeper question. This spec decides *how it gets built*, and stops short
> of CI and a published release.

## Goal

`make pack` produces `build/iina-airplay.iinaplgz` — the plugin JS, a universal
ad-hoc-signed Go helper, and a pinned universal LGPL ffmpeg — and mechanically
verifies the package it just produced. The round ends with a human installing
that package into IINA through its local-package path and casting one real file
to a TV.

CI, the tagged GitHub release, Developer ID signing, and any fresh-user download
rehearsal are explicitly out of scope. They are round 2.

## Why local first

The ffmpeg configure line is the only genuinely uncertain piece of work here,
and it is the piece with the slowest feedback loop: a full universal build is
tens of minutes. Debugging it through GitHub Actions would mean discovering a
missing muxer forty minutes at a time. Building it locally first means round 2
wires up a recipe already known to produce a working artifact.

## Decisions taken during design

Three questions `docs/distribution.md` left open, now closed:

- **`ffprobe` is not bundled.** Nothing needs it. The plugin reads codecs,
  channel counts and track indices from mpv's `track-list`; the helper is handed
  those as argv. `docs/next-steps.md` assumed an `ffprobe` probe step that the
  implementation never needed.
- **Decoders stay broad.** An earlier instinct was to drop video decoders on the
  grounds that video is stream-copied. It isn't always: `helper/job.go`'s
  `default:` branch re-encodes with `hevc_videotoolbox` for any video that is not
  H.264 or HEVC, and encoding requires decoding. Dropping video decoders would
  turn "casts slowly" into "cannot cast at all" for VP9, AV1, MPEG-2, VC-1,
  DivX/Xvid and WMV — the MKV-era long tail — while saving perhaps 6–10 MB of a
  ~35 MB universal binary. Not worth it. The trimming happens on the output side
  (encoders, muxers, programs, protocols), not the input side.
- **Encoders are an allowlist, decoders are not.** Stated plainly because the
  asymmetry is the whole shape of the configure line.

## Components

### `packaging/build-ffmpeg.sh`

Pins one ffmpeg release and the SHA-256 of its source tarball, both as literals
at the top of the script. The pin is the newest stable point release available
on `https://ffmpeg.org/releases/` at implementation time — chosen then rather
than named here, because naming a version from memory is how you pin a tarball
that does not exist. The script downloads it, verifies the hash, and hard-fails
on mismatch: a silent substitution here would ship an unknown binary to
strangers.

Configure is **broad in, narrow out**:

- decoders, demuxers, parsers, bitstream filters: ffmpeg's defaults, untouched.
  Any file IINA can open, the bundled ffmpeg can open.
- disabled: `--disable-gpl` and nonfree (LGPL keeps the plugin MIT-licensable),
  `--disable-doc`, all programs but `ffmpeg` itself, all network protocols
  (the plugin's non-goals forbid runtime network access; this enforces it in the
  binary), and all encoders.
- re-enabled encoders, exactly: `aac`, `eac3`, `hevc_videotoolbox`,
  `h264_videotoolbox`, `webvtt`.
- muxers narrowed to what the HLS path writes: `hls`, `mp4`, `mov`, `webvtt`
  and the segment muxers `hls` pulls in. The exact set is confirmed by the e2e
  run below, not by reading the configure line — a missing muxer is invisible
  until ffmpeg tries to write one.
- no external libraries, so the result links only against system frameworks.
  This is what "static" means for our purposes — no Homebrew dylibs.

Builds arm64 natively and x86_64 via `--enable-cross-compile
--cc='clang -arch x86_64'`, then `lipo`s them into one universal binary.

Output and a recipe-hash stamp go in gitignored `build/ffmpeg/`. A rebuild
happens only when the pinned version or the flags change. A `--arch native`
flag builds one architecture for fast iteration during development.

### `packaging/pack.sh`

1. Builds the helper for `darwin/arm64` and `darwin/amd64`, `lipo`s them, and
   **re-applies an ad-hoc signature with `codesign -s -`**. Go's linker ad-hoc
   signs arm64 binaries; amd64 gets none, and `lipo` output cannot be assumed to
   carry a valid signature. The entire no-notarization finding in
   `docs/distribution.md` rests on those signatures existing, so this step is
   load-bearing rather than cosmetic.
2. Stages `Info.json`, `main.js`, `sidebar.html`, and `bin/`.
3. Generates `bin/VERSIONS`: helper version, ffmpeg version, SHA-256 of both
   binaries.
4. Generates `bin/ffmpeg-LICENSE.md`: the LGPL notice, the exact source tarball
   URL, and the verbatim configure line. This is the compliance artifact.
5. Runs `iina-plugin pack` into `build/iina-airplay.iinaplgz`.

### `packaging/verify.sh`

Run by `make pack` immediately after packing. It unzips **the produced package**
and checks the extracted contents — checking the staging directory would verify
something users never receive.

Assertions:

- `Info.json` parses; identifier, version and `ghRepo` present.
- `bin/airplay-helper` and `bin/ffmpeg` exist and are executable.
- `lipo -archs` reports both `x86_64` and `arm64` for each.
- `codesign -dv` succeeds on each.
- no `com.apple.quarantine` xattr anywhere in the tree.
- `ffmpeg -encoders` contains the full allowlist and does not contain `libx264`.
- the configure line reported by `ffmpeg -version` contains no `--enable-gpl`.
- `otool -L bin/ffmpeg` lists only `/usr/lib` and `/System` paths.

That last assertion is the one that earns its keep: a leaked `/opt/homebrew`
dylib produces a package that works perfectly on the build machine and fails on
every other Mac.

Any failure exits non-zero with a message naming the specific check.

### Testing the trimmed build

`findFFmpeg` in `helper/e2e_test.go` currently hardcodes the two Homebrew paths.
It learns an environment variable so the existing e2e suite can point its
pipeline at the freshly built bundled binary.

Fixtures continue to use the system ffmpeg, because they are synthesized with
`libx264`, the `flac` and `srt` encoders and the `matroska` muxer — precisely
the things the bundled build excludes. The result is a deliberate two-binary
test: Homebrew's ffmpeg authors a real MKV, the bundled ffmpeg casts it.

This matters more than any flag inspection. It exercises video stream-copy, the
lossless-audio-to-E-AC-3 branch, fMP4 segmentation, the event playlist and the
WebVTT subtitle rendition against the actual artifact being shipped. A configure
line can satisfy every grep in `verify.sh` and still have dropped a muxer the
HLS path needs.

### Smaller pieces

- **`LICENSE`** (MIT) at the repo root, which the repo currently lacks, plus a
  note that the distributed package includes an LGPL ffmpeg carrying its own
  notice.
- **`Info.json`**: `ghRepo` and `ghVersion` for IINA's installer and update
  check. The exact key names and `ghVersion`'s type are to be confirmed against
  IINA's source rather than taken from `docs/distribution.md` — the doc is
  reasoning from memory on this point.
- **Plugin failure message**: a missing or non-executable binary currently
  surfaces as a raw `String(e)` from the `utils.exec` rejection in
  `plugin/main.js`. It becomes one clear message naming the fix — reinstall
  through IINA's plugin installer — as `docs/distribution.md`'s first failure
  mode requires. Never a download attempt.
- **`Makefile`**: `ffmpeg`, `pack` and `verify` targets. `make dev` keeps
  symlinking Homebrew's ffmpeg, so the fast development loop never waits on a
  source build.
- **Docs**: README install instructions for strangers; `docs/distribution.md`
  updated with the decisions recorded above and its status line corrected.

## Build order

1. `LICENSE`, `Info.json` fields — trivial, unblocking.
2. `packaging/build-ffmpeg.sh` — the long pole. Everything downstream is shaped
   by whether it produces a working binary.
3. e2e suite against that binary — the gate on the configure line.
4. `packaging/pack.sh`.
5. `packaging/verify.sh`.
6. Plugin failure message.
7. Docs and README.

## Done means

`make pack` produces a `.iinaplgz` that passes every `verify.sh` assertion and
whose bundled ffmpeg passes the helper e2e suite; then a human installs that
package into IINA via its local-package path and casts one real file to a TV.

## Risks

- **The configure line drops something the HLS path needs.** Caught by step 3,
  which is why the e2e run gates the rest rather than trailing it.
- **x86_64 cross-compilation fails or produces an untested binary.** The build
  machine is Apple Silicon, so the x86_64 slice is verified structurally
  (`lipo`, `codesign`, `otool`) but never executed. Accepted for this round;
  it is CI's job to run it on an Intel runner or a Rosetta step.
- **`ghRepo`/`ghVersion` key names are wrong.** Low cost, caught by reading
  IINA's source, and invisible until round 2 anyway.
