# Distribution design — how strangers install this

> Decided 2026-08-29, after the Gatekeeper experiment below. Baseline for the real
> plugin: **one `.iinaplgz` bundling the JS plugin, a small Go supervisor binary,
> and a pinned static LGPL ffmpeg. No first-run downloads, no native code loaded
> into IINA, no Apple Developer ID.**

## The decision, and the two designs it beat

The plugin needs three things on a stranger's Mac: an ffmpeg-class remuxer, a LAN
HTTP server, and helper lifecycle management. Neither `python3` nor `ffmpeg` can be
assumed to exist (python3 ships with Xcode CLT, not macOS). Three candidates:

1. **Tiny bundled helper + first-run download of ffmpeg.** Dies on the download:
   it re-introduces a hidden prerequisite (GitHub reachable at first cast — a real
   problem for IINA's large Chinese user base, corporate TLS-inspection networks,
   and offline Macs), adds a permanent supply-chain surface (the release asset URL
   must resolve forever; re-tagging a release bricks fresh installs), and needs
   version-skew handling between plugin and cached binaries. Meanwhile it still
   requires maintaining a pinned ffmpeg build in CI — the cost it was supposed to
   avoid.
2. **One all-in-one binary linking libav.** Dies on the linking: stream-copy remux
   via the API is easy, but our pipeline already includes an audio transcode
   (lossless → E-AC-3), which means reimplementing ffmpeg's transcode loop and its
   twenty years of PTS/sync edge cases; the subtitle roadmap (WebVTT conversion,
   PGS burn-in via libavfilter graphs) multiplies that. It also puts a static-libav
   + CGo cross-compile in the contribution critical path.
3. **Bundle both: supervisor + pinned static ffmpeg CLI in the package.** What
   remains when 1 and 2 lose their signature features. Fully offline after install,
   media edge cases stay ffmpeg's problem, contributors build JS+Go trivially while
   CI owns the ffmpeg build. Cost: a ~35 MB package — a one-time download through
   IINA's installer, where a big file is least painful. **Chosen.**

## Gatekeeper: verified, not assumed (2026-08-29)

The design only works if binaries inside an IINA-installed package run without
notarization. Verified two ways:

- **Source:** IINA's installer downloads release assets via `Just.get` +
  `Data.write(to:)` and source zips via `curl -fsSL`, then extracts with
  `/bin/sh -c "unzip …"` (`JavascriptPlugin.create(fromPackageURL:)`). None of
  these apply `com.apple.quarantine`, and IINA's Info.plist has no
  `LSFileQuarantineEnabled`.
- **Experiment:** packed a `.iinaplgz` containing a compiled ad-hoc-signed binary,
  served it over HTTP, downloaded and extracted it with IINA's exact commands. The
  binary came out with only `com.apple.provenance` (does not trigger Gatekeeper)
  and executed cleanly. The counterfactual — the same binary with a browser-style
  quarantine xattr — was hard-blocked with the "Apple could not verify" dialog.

Consequences: **users must install through IINA** (browser-downloading the
`.iinaplgz` and opening it by hand puts quarantine on everything inside). Binaries
need only the ad-hoc signature the linker already adds. No $99/year, no
notarization — with the known risk that a future macOS could tighten this; if that
happens, Developer ID signing drops into the CI pipeline without design changes.

## Install channel

Users type the GitHub repo slug (`owner/iina-airplay`) into IINA → Settings →
Plugins → Install. IINA queries the repo's **latest GitHub release** for an
`.iinaplgz` asset and installs it (falling back to `archive/main.zip` of the
source if none — so every release must carry the asset, or users get an
uninstallable source tree). For users who cannot reach GitHub: document IINA's
"install from local package" with the `.iinaplgz` fetched from any mirror.

## Package layout

```
iina-airplay.iinaplgz            (~35 MB zipped)
├── Info.json                    sidebarTab, permissions, ghRepo/ghVersion (updates)
├── main.js / sidebar.html / …   the plugin
└── bin/
    ├── airplay-helper           Go, universal, ad-hoc signed (~10 MB)
    ├── ffmpeg                   static, universal, LGPL configure (~25 MB)
    ├── ffmpeg-LICENSE.md        LGPL notice + pinned source-tarball URL
    └── VERSIONS                 helper + ffmpeg versions, SHA-256 of both
```

`utils.exec` runs binaries from absolute paths and `@data`/`@tmp`; the plugin
resolves its own install directory and calls `bin/airplay-helper` by absolute path.

## The supervisor (`airplay-helper`, Go)

Replaces `serve.sh` + python entirely. One process, stdlib only. Responsibilities:

- **Serve** the HLS output directory: correct MIME types (`.m3u8`, `.m4s`, `.mp4`)
  and Range requests (Go's `http.FileServer` provides both), bound to `0.0.0.0` on
  a **free port picked at start** (no more hardcoded 8919), LAN IP discovery.
- **Watchdog:** exit when the parent IINA process dies (poll PPID/kill-0). This
  structurally kills the orphan problem found during prototyping. On start, detect
  and kill a stale instance (pidfile in the plugin data dir).
- **Run ffmpeg jobs:** spawn the bundled ffmpeg, translate its `-progress` output
  into the stdout protocol, kill on timeout or on a `stop` command.
- **Protocol:** JSON lines on stdout, consumed by the JS side through the
  `utils.exec` stdout callback — `{"event":"ready","url":…}`,
  `{"event":"progress","pct":…}`, `{"event":"error","msg":…}`. Commands arrive as
  argv (v0 needs only: source path, out dir, clip/full, stop). Remember the
  prototype lesson: the JS side must not touch `sidebar.*` from these callbacks.

Why Go: the whole server is stdlib including Range support, `GOOS/GOARCH`
cross-compilation plus `lipo` is a two-liner in CI, the artifact is a single
static binary, and `git clone && go build` works for contributors with no media
toolchain.

## The ffmpeg build

Pinned release, custom LGPL-only configure: no GPL components (no libx264 — any
re-encode uses VideoToolbox), demuxers/decoders broad (MKV et al. must all open),
encoders limited to `aac`, `eac3`, `hevc_videotoolbox`, `h264_videotoolbox`,
`webvtt`, muxers `hls`/`mp4`/`webvtt`. LGPL keeps the plugin itself MIT-licensable;
compliance = ship the license notice and link the exact source tarball in
`bin/ffmpeg-LICENSE.md` and the release notes.

## CI / release (GitHub Actions, macOS runner)

1. Build ffmpeg from the pinned recipe for arm64 + x86_64, `lipo` → universal.
   Cache by recipe hash — rebuilt only when the pin or flags change.
2. `go build` the helper for both arches, `lipo`, verify ad-hoc signatures exist.
3. Assemble the plugin directory, `iina-plugin pack`, attach the `.iinaplgz` and
   SHA-256s to the GitHub release. `Info.json`'s `ghVersion` bump lets IINA's
   own update check find new releases.

## Failure modes

- **Helper or ffmpeg missing/not executable** (hand-copied install, quarantined by
  a browser download): detect at cast time, show one clear sidebar message naming
  the fix ("reinstall through IINA's plugin installer"). Never attempt a download.
- **Port in use / stale helper:** supervisor handles both itself (free-port pick,
  pidfile takeover).
- **First media load flake** (see `docs/prototype.md` findings): JS retries the
  media element once before surfacing an error.

## Non-goals

Homebrew/ffmpeg detection or reuse (bundling makes it dead weight), Developer ID
signing (until macOS forces it), any runtime network access by the plugin or
helper, and Windows/Linux anything.
