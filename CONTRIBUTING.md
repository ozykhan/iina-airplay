# Contributing

Thanks for taking an interest. This is a small, opinionated repo: an IINA plugin
(JavaScript), a Go helper that drives `ffmpeg`, and the packaging that turns the
two into an `.iinaplgz` a stranger can install. Everything below is what you'd
otherwise have to reverse-engineer.

Start with [`README.md`](README.md) for what the plugin does and
[`docs/feasibility.md`](docs/feasibility.md) for why it does it that way. If
you're proposing an architecture change, read the feasibility doc *first* — most
"why don't you just mirror the screen?" ideas are closed off by the platform,
and the doc says which and why.

## Prerequisites

macOS, [IINA](https://iina.io) (everything here is verified against
1.4.4 — the repo declares no minimum, but that's the version the behaviour
notes below were checked on), and:

```bash
brew install go node ffmpeg
```

`nasm` is needed **only** if you build the shipped package (`make pack` /
`make ffmpeg`) — the x86_64 ffmpeg slice assembles hand-written x86 assembly
with it. The everyday dev loop never builds ffmpeg from source.

## Dev loop

```bash
make dev
```

That builds the Go helper, symlinks Homebrew's `ffmpeg` into `plugin/bin/`,
symlinks the root `Info.json` into `plugin/`, and registers the plugin
directory with IINA.

Then **restart IINA** and enable the plugin under Settings → Plugins. Restarting
is not optional: the sidebar and standalone window are `WKWebView`s and don't
hot-reload their JS. Plugin logs live in the same settings panel — select the
plugin and open its console. Safari's Web Inspector can also attach to the
plugin's webviews (`isInspectable` is on).

On macOS 15+, grant IINA the **Local Network** permission the first time it
serves a stream, or the Apple TV cannot reach it.

## Tests

```bash
make test
```

That runs `go test ./...` in `helper/`, `node --test` over `plugin/tests/`, and
the packaging shell tests. CI runs exactly this on `macos-15`, plus a full
package build verified natively on both Apple Silicon and Intel.

**Homebrew's `ffmpeg` is load-bearing for the test suite, not a convenience.**
The helper's real-media end-to-end tests *skip themselves* when no system
`ffmpeg` is found and `IINA_AIRPLAY_FFMPEG` is unset — so without it the suite
goes green having never driven the pipeline through `ffmpeg` at all. If you're
touching the transcode path, confirm those tests actually ran.

### What you cannot test without hardware

Casting itself needs a real Apple TV on the same LAN. Everything up to the
handoff — probing the file, choosing remux vs. transcode, the HLS segments the
helper serves, subtitle conversion, the packaged binaries — is covered by the
automated suite and by pointing a browser or `ffprobe` at the helper's stream
URL. Only the final "the TV picked it up and played it" step needs the device.

Say so in your PR if you couldn't test that last step. It's an expected gap, not
a problem — it just tells the maintainer what to check before merging.

## Layout

| Path | What lives there |
| --- | --- |
| `plugin/` | The IINA plugin: `main.js` (JSContext) and `sidebar.html` (the cast UI webview) |
| `helper/` | The Go supervisor — spawns `ffmpeg`, serves HLS, watchdog, playlist |
| `packaging/` | `build-ffmpeg.sh`, `pack.sh`, `verify.sh` and the release gates |
| `docs/` | Design reasoning, the prototype record, the distribution design, the release runbook |
| `Info.json` | The plugin manifest — **at the repo root**, see below |

## Constraints that bite newcomers

These are established facts, not guesses. Fuller detail in
[`CLAUDE.md`](CLAUDE.md) and [`docs/prototype.md`](docs/prototype.md).

- **The plugin API is JavaScript only.** No native code, no access to IINA's
  render pipeline. This is why the design is a handoff rather than a mirror.
- **`utils.exec` replaces the process environment** and sets only `LC_ALL`, so
  `PATH` is empty in anything you spawn. The symptom is "command not found" for
  a binary that is obviously installed. Always pass an absolute or `@data/` path.
- **`sidebar.show()` crashes IINA when called from a `utils.exec` callback** —
  those callbacks run off-main and trip an AutoLayout assertion. Call `sidebar.*`
  only from menu/`onMessage` callbacks; let the page poll for background results.
- **The first media load in a plugin webview flakes** (`error code=4`, fine on
  reload). Retry once, automatically.
- **`plugin/Info.json` is a gitignored symlink** created by `make dev`. Never
  commit it — `raw.githubusercontent.com` serves a symlink's target path as
  plain text, which would break IINA's update check for every existing user.
- **Prefer remuxing over re-encoding, always.** A remux of an HEVC file takes
  seconds; a re-encode of a UHD remux is not something anyone will wait for.

### `master` is load-bearing — do not rename it

IINA's update check fetches
`raw.githubusercontent.com/<repo>/master/Info.json` — the repository root of the
**`master` branch**, hardcoded, never the release. Renaming the default branch
to `main` would silently cut every installed copy off from updates, and IINA
reports that failure as a bland "No update found." Details in
[`docs/releasing.md`](docs/releasing.md).

## Branch and PR workflow

Trunk-based. `master` is the single long-lived branch and stays green.

- Branch off `master`, named `<your-gh-name>/<short-description>`.
- Conventional-commit messages (`feat:`, `fix:`, `test:`, `docs:`, `chore:`).
- Run `make test` before pushing — it's the same gate CI applies.
- Open the PR into `master`. Reference the issue in the body (`Part of #N`, or
  `Closes #N` when the merge fully finishes it).
- PRs are **squash-merged**, so the PR title and body become the commit on
  `master`. Write them as the changelog entry you'd want to read later.
- CI's `test` job must pass, branches must be up to date, and review
  conversations must be resolved before merge.

## Issues

Title every issue with a lowercase `[area]` prefix so a glance down the list
finds everything touching one surface:

| Prefix | Surface |
| --- | --- |
| `[plugin]` | `plugin/` — the JS entry point and the sidebar webview |
| `[helper]` | `helper/` — the Go supervisor, ffmpeg pipeline, HLS server |
| `[packaging]` | `packaging/` — the ffmpeg build, packing, verification, releases |
| `[repo]` | Governance: CI, branch protection, tooling |
| `[docs]` | Docs, design, README |

Label a new issue when you open it: a **type** (`bug` / `enhancement` /
`documentation`), a **priority** (`P1` / `P2` / `P3`), and a **size**
(`size/XS` … `size/XL`), plus the area label it lands in. Priority and size are
orthogonal — a change can be small but `P1`.

New here? [`good first issue`](https://github.com/ozykhan/iina-airplay/labels/good%20first%20issue).

### Reporting a cast bug

Casting failures depend on the file, the Mac and the TV, so a report without
those is rarely actionable. Include:

- macOS version, Mac model, IINA version, plugin version
- Apple TV model and tvOS version
- `ffprobe` output for the file (or at least container, video codec, audio
  codec, subtitle tracks)
- The plugin console log from the failed attempt

The bug report template asks for all of this.

## Scope

Reasonable additions: format coverage, subtitle handling (PGS burn-in is the
open one), playlist behaviour, packaging robustness, tests. Things that are out
of scope by design: native code in the plugin, mirroring IINA's actual video
output, and anything that makes the common path re-encode when it could remux.

If you're planning something large, open an issue first — a `[repo]` or design
issue costs you nothing and saves a rewrite.

## License

By contributing you agree that your contributions are licensed under the
repository's [MIT license](LICENSE).
