# Distribution, round 2: CI and the first tagged release

> Design settled 2026-08-30. Round 1
> (`2026-08-29-distribution-local-pack-design.md`) made `make pack` produce a
> `.iinaplgz` that a human installed into IINA and cast to a real Apple TV.
> This round automates that build, gates it on hardware round 1 could not
> reach, and ends with `v0.1.0` published and installed by repo slug.

## Goal

A tagged `v*` push builds the package on GitHub Actions, verifies it on both
architectures — including executing the x86_64 slice on a real Intel runner —
and attaches it to a **draft** GitHub release. A human publishes that draft and
then installs the plugin the way a stranger would: by typing `ozykhan/iina-airplay`
into IINA.

## Why this round exists

Two things are unverified, and CI is the instrument for one of them.

1. **The x86_64 slice has never executed on Intel hardware.** Round 1 checked
   it structurally (`lipo`, `codesign`, `otool`) and behaviourally under
   Rosetta. Rosetta is a translation layer, not the target: it shares the host's
   kernel, dyld and frameworks, and translates rather than executes the
   instruction stream. Every Intel user currently depends on a binary that no
   Intel CPU has run.
2. **Nothing has confirmed that a package IINA downloads from a release stays
   unquarantined.** `docs/distribution.md`'s Gatekeeper experiment used a
   synthetic package served over local HTTP, and the 2026-08-29 acceptance run
   used IINA's *local package* path. The release path — `api.github.com` →
   `Just.get` → `Data.write(to:)` → `unzip` — has never been exercised with
   these binaries. That one is closed by a human, after publication, not by CI.

Automation is the means. These two are the point.

## Established facts this design rests on

Checked 2026-08-30 against GitHub's runner documentation, `actions/runner-images`,
and IINA's source — not from memory, because every one of them is the kind of
detail that is expensive to be wrong about.

### Runner images

- arm64 labels: `macos-15`, `macos-26`, `macos-latest`. Intel is a **separate
  label set**: `macos-15-intel`, `macos-26-intel`. The old `macos-13` Intel
  image was fully retired in December 2025; any design written around it is dead.
- `macos-15-intel` is GitHub's designated last x86_64 image and is available
  **until August 2027**. Intel verification has an expiry date, and it belongs
  in the docs rather than in a surprise.
- Neither image ships `nasm` or `ffmpeg`. Both ship Go 1.26.5 (satisfying
  `helper/go.mod`'s `go 1.26.4`), Node 22/24, Homebrew, and Xcode Command Line
  Tools — so `/usr/bin/python3`, `codesign`, `lipo`, `otool` and `clang` are all
  present, which `verify.sh` and `packaging/tests/verify.test.sh` require.
- **The arm64 runners have no Rosetta.** `verify.sh` already handles that by
  printing a note and skipping its x86_64-slice assertions. That skip is the
  single most dangerous thing in this design: it makes an arm64-only CI run
  green while never having checked the x86_64 slice's licensing or codec sets.
  The Intel job is not redundant coverage; it is the only coverage.

### IINA's installer

- IINA queries `https://api.github.com/repos/<slug>/releases/latest` and takes
  the **first asset whose name ends in `.iinaplgz`**. `/releases/latest`
  excludes drafts *and* prereleases, which is what makes the draft-release model
  below safe: a draft is invisible to IINA until a human publishes it.
- The source fallback is `archive/**main**.zip`, not `master.zip`. This repo's
  default branch is `master`, so today a repo-slug install does not silently
  install a binary-less tree — it 404s and fails outright. Still broken, but
  louder than assumed. Either way the fix is the same: every release carries the
  asset.
- `iina-plugin pack` is exactly `zip -ryq <out> . -x 'node_modules/*' -x '.*'`
  run from inside the plugin directory. The archive root holds the plugin's
  contents, not a wrapping directory.

## Decisions taken during design

- **The release is created as a draft; a human publishes it.** Publication is
  the act that exposes strangers to the build, and it is the same act that
  enables the acceptance test. Drafts are invisible to `/releases/latest`, so
  there is no window in which a half-reviewed release is installable.
- **The packaging pipeline runs on tags, on `workflow_dispatch`, and on PRs
  touching `packaging/**`, `helper/**`, `plugin/**`, `Makefile`, or the workflow
  itself.** A broken configure line should fail on the PR that breaks it, not
  thirty minutes into a tag push. The cache makes the common case cheap; only a
  recipe edit pays the full build, which is exactly when it should be paid.
- **The Intel job executes both shipped binaries, not just ffmpeg.**
  `buildHelper()` in `helper/main_test.go` compiles from source, so pointing
  only `IINA_AIRPLAY_FFMPEG` at the package would test the shipped ffmpeg
  against a freshly built native helper — leaving the packaged helper's x86_64
  slice as unexecuted as it is today. A symmetric `IINA_AIRPLAY_HELPER`
  override closes that for one small change.
- **`iina-plugin` stops being a build prerequisite.** `pack.sh` performs the
  `zip` itself. This is one code path for local and CI rather than two, it makes
  `make pack` work for contributors without IINA installed, and the divergence
  risk is near zero: IINA only ever *reads* the package by unzipping it, and
  `verify.sh` proves the result unzips into the expected tree. IINA remains
  required only for `make dev`'s link step.
- **Both legs pin macOS 15, not `macos-latest`.** `macos-15` and
  `macos-15-intel` are the same OS on two architectures, so a discrepancy
  between the jobs is attributable to architecture rather than to OS version.
  `macos-latest` moves underneath the pin without a commit.
- **No signing beyond ad-hoc.** No Developer ID, no notarization, no secrets of
  any kind. Round 1 verified ad-hoc signatures suffice through IINA's installer;
  adding key material would create a maintenance and compromise surface the
  design deliberately does not have.

## Architecture

Two workflows.

### `.github/workflows/ci.yml` — the ordinary signal

On `push` (branches, not tags) and `pull_request`. One job, `macos-15`:
pin Go via `go-version-file: helper/go.mod` and Node via `actions/setup-node`
so an image refresh cannot silently change the toolchain, `brew install ffmpeg`,
then `make test`.

The `brew install ffmpeg` is load-bearing rather than convenience.
`locateSystemFFmpeg` *skips* the real-media e2e when no system ffmpeg is found
and `IINA_AIRPLAY_FFMPEG` is unset. Without it, PR CI would report green having
never driven the pipeline through ffmpeg at all.

### `.github/workflows/package.yml` — the expensive chain

Triggers: tags `v*`, `workflow_dispatch`, and the packaging-path PR filter above.

```
build (macos-15, arm64) ──► verify-intel (macos-15-intel) ──► release (tags only, draft)
```

Workflow-level `permissions: contents: read`; only the `release` job elevates to
`contents: write`. Concurrency is grouped by ref with
`cancel-in-progress: ${{ !startsWith(github.ref, 'refs/tags/') }}` — superseding
a branch run is fine, cancelling a release mid-flight is not.

`build` runs `make test` before packing, so the release chain is self-contained
rather than depending on a second workflow's result. That double-runs the suite
on packaging-path PRs. Roughly three minutes, and preferable to coupling two
workflows through an external status check.

#### Job 1 — `build`

Checkout at `fetch-depth: 0`. Both `pack.sh`'s `git describe --tags` (which
supplies `helper_version` in `bin/VERSIONS`) and the tag gate's previous-tag
lookup need real history and tags; the default shallow checkout gives neither,
and the failure mode is a package that quietly records a short SHA where a
version belongs.

Steps, in order:

1. **Tag gate** (`packaging/check-release.sh`, tag refs only). First, so a
   mismatched tag fails in seconds instead of after the ffmpeg leg.
2. `brew install nasm ffmpeg`. `nasm` is needed only for the x86_64 slice's
   hand-written assembly (`build-ffmpeg.sh` prechecks it and says so); `ffmpeg`
   authors the test fixtures, for the same reason it is installed in `ci.yml`.
3. `make test` — before the long build, so an ordinary unit-test failure costs
   seconds rather than half an hour.
4. **Cache restore.**
5. `./packaging/build-ffmpeg.sh` — a no-op on a cache hit, via its own stamp.
6. `./packaging/build-helper.sh`, `./packaging/pack.sh`.
7. `./packaging/verify.sh build/iina-airplay.iinaplgz`.
8. `./packaging/test-package.sh build/iina-airplay.iinaplgz` — the package
   acceptance run described below.
9. Write `build/iina-airplay.iinaplgz.sha256`; upload both as an artifact.

**The cache key is `hashFiles('packaging/build-ffmpeg.sh')`.** That is not the
same number as the script's own `RECIPE_HASH`: `hashFiles()` hashes a list of
per-file SHA-256 digests, while the script computes `shasum -a 256` on the file
itself — a hash of hashes versus a direct digest. What they share is the
condition under which they change: the key changes if and only if
`build-ffmpeg.sh` changes, which is exactly the condition that invalidates the
script's own `.recipe-hash` stamp. So a cache restore hits precisely when the
stamp would also call the build up to date, and misses precisely when the stamp
would call for a rebuild — they agree on *when*, never on the *value*. Exact key
only; no `restore-keys` prefix fallback, because a near-miss restore has no
value here — a mismatched stamp forces a full rebuild anyway.

Cached paths are four files, not the tree:

```
build/ffmpeg/ffmpeg
build/ffmpeg/VERSION
build/ffmpeg/.recipe-hash
build/ffmpeg/src/COPYING.LGPLv2.1
```

That last path is easy to omit and `pack.sh` hard-fails without it: it is the
LGPL 2.1 §1 license copy, which must ship in the package. Caching these four
restores ~45 MB instead of a multi-gigabyte configured source tree.

`verify.sh` here executes the **arm64** slice natively and skips its x86_64
assertions for want of Rosetta. That is expected and is why job 2 exists.

Step 8 is the other half, and it is not a duplicate of `verify.sh`. Round 1's
spec is explicit that flag inspection is insufficient — "a configure line can
satisfy every grep in `verify.sh` and still have dropped a muxer the HLS path
needs" — so something has to drive the bundled binaries through a real remux.
Running that here, against the **packed** artifact rather than against
`build/ffmpeg/ffmpeg`, makes `build` and `verify-intel` perform the identical
package acceptance and differ only in architecture. Without it, nothing in CI
would ever exercise the arm64 slices of the shipped binaries at all; the round-1
gate ran only on a developer's machine.

#### Job 2 — `verify-intel`

`macos-15-intel`, `needs: build`. Its first step is a hard assertion that
`uname -m` is `x86_64`. If GitHub ever remaps that label to Apple Silicon, the
entire purpose of the job evaporates while the check stays green — the one
failure this design cannot tolerate silently.

It **builds nothing**. It checks out the same ref (for `helper/`'s test sources
and for `plugin/`, which `verify.sh`'s staleness comparison reads), downloads
the artifact `build` produced, re-checks its SHA-256 against the sidecar file,
and unzips it. Then, against those exact bytes:

- `./packaging/verify.sh <pkg>` — here x86_64 is the *native* slice, so the
  licensing assertions, the encoder allowlist and the decoder set are checked by
  running real Intel code rather than a Rosetta stand-in.
- `./packaging/test-package.sh <pkg>` — the same package acceptance `build`
  ran, now on Intel, with `brew install ffmpeg` supplying fixtures. Homebrew's
  Intel prefix is `/usr/local`, already in `systemFFmpegCandidates`, so fixture
  discovery needs no test change.

These are the same two commands `build` runs, in the same order, on the same
bytes. Any difference in outcome is a difference in architecture, which is
precisely the question this job exists to answer.

**Upload the packed `.iinaplgz`, never the unpacked tree.**
`actions/upload-artifact` does not preserve POSIX permission bits, so an
unpacked tree would arrive on the Intel runner with its executable bits stripped
and `verify.sh` would fail for a reason that has nothing to do with the package.
The `.iinaplgz` is a single opaque file whose internal zip entries carry their
own modes, so it survives the round trip intact. The SHA-256 re-check makes that
an assertion rather than an assumption.

#### Job 3 — `release`

`needs: [build, verify-intel]`, `if: startsWith(github.ref, 'refs/tags/v')`,
`permissions: contents: write`.

Downloads the artifact, asserts exactly one file matching `*.iinaplgz` is
present, and runs `gh release create <tag> --draft --notes-file <body>` with the
package and its `.sha256` sidecar attached. The sidecar is safe alongside the
package: IINA matches on the `.iinaplgz` suffix, and `iina-airplay.iinaplgz.sha256`
does not end in it.

The notes body is composed by the workflow, not by `--generate-notes`. It
carries the install instructions, the package SHA-256, and the ffmpeg version,
source URL and source tarball SHA-256 read out of the package's own
`bin/VERSIONS` — the release-notes half of the LGPL obligation
`docs/distribution.md` names. Whether `gh` will combine `--generate-notes` with
`--notes-file` for a commit changelog is an implementation-time question; if it
does not compose cleanly, the hand-composed body stands alone.

## Components

### `packaging/pack.sh` (changed)

Replace the `iina-plugin pack` invocation and the `IINA_PLUGIN` prerequisite
check with the equivalent `zip` run from inside the staging directory:

```sh
( cd "$STAGE" && zip -ryq "$CANONICAL_PKG" . -x 'node_modules/*' -x '.*' )
```

`zip` **appends to an existing archive** rather than replacing it, so the target
must be removed first. `pack.sh` already removes `$CANONICAL_PKG` at the top for
a different reason (never leaving a stale package at the canonical path after a
failed run); that removal now also carries this weight, and the code should say
so. The intermediate `iina-airplay-$version.iinaplgz` name and the `mv` that
followed it disappear with the CLI, since we now write the canonical path
directly.

Nothing else about the staging tree changes. Exec bits are stored in the zip
entries, which is why `verify.sh`'s `-x` checks pass today under IINA's own
`zip` and will continue to under ours.

### `helper/main_test.go` (changed)

`buildHelper()` gains an `IINA_AIRPLAY_HELPER` override, mirroring
`findFFmpeg`'s contract exactly: when set, the named binary is used, and a path
that does not stat is a **hard failure, never a fallback to `go build`**. A
silent fallback would let a typo in the workflow read as a passing
bundled-binary run, which is the specific failure this whole job is meant to
rule out.

Following `locateSystemFFmpeg`'s precedent, the decision is factored into a pure
function taking a stat callback so it can be table-tested with fake paths rather
than only through the filesystem.

Every helper test that calls `buildHelper()` — not just the e2e ones — then
drives the packaged universal binary on the Intel runner. That is the intent:
the watchdog, the pidfile takeover and the stdout protocol get exercised on
x86_64 too, not only the ffmpeg supervision path.

### `packaging/verify.sh` (changed)

One refinement: skip the `arch -x86_64` pass when the native architecture is
already x86_64. On the Intel runner the current code runs the same assertions
twice under two different labels, which reads in the log as coverage it is not.
Covered by a new case in `packaging/tests/verify.test.sh`.

### `packaging/test-package.sh` (new)

Takes a `.iinaplgz`. Unzips it to a temporary directory and runs
`go test ./...` in `helper/` with `IINA_AIRPLAY_FFMPEG` and
`IINA_AIRPLAY_HELPER` pointed at the extracted `bin/`. One script, called
identically by both jobs and by `make test-package` locally, rather than the
same six lines of shell duplicated in two YAML blocks where they can drift
apart unnoticed.

It fails if the extracted binaries are missing or non-executable rather than
letting `go test` skip — a package acceptance that can pass without running the
package is worthless.

### `packaging/check-release.sh` (new) + `packaging/tests/check-release.test.sh` (new)

Takes a tag name. Asserts:

- The tag matches `v<version>` where `<version>` is `Info.json`'s `version`
  string. A mismatch produces a release whose name and payload disagree.
- `ghVersion` is a JSON integer strictly greater than the `ghVersion` at the
  previous tag, read offline via `git show <prev-tag>:plugin/Info.json` — no API
  call, so the gate works on a fork or with no network. The previous tag is
  `git describe --tags --abbrev=0 <tag>^`.
- **When there is no previous tag, the monotonicity check is skipped**, not
  failed. `v0.1.0` is exactly that case, and a gate that cannot pass on the
  first release is a gate that gets disabled.

Structured as a script with its own test file because that is how `verify.sh` is
already structured in this repo, and because the alternative — the same
assertions inline in YAML — is untestable: the only way to exercise it is to
push a deliberately bad tag, and a bug in the gate presents as a passing gate.
`make test` runs it alongside `verify.test.sh`.

### `Makefile` (changed)

`make test` gains `packaging/tests/check-release.test.sh`. A new
`make test-package` target wraps `packaging/test-package.sh` against
`build/iina-airplay.iinaplgz`, and `make pack` runs it after `verify`, so the
local `make pack` and the CI `build` job perform the same sequence.
`test-bundled` stays as-is: it points at `build/ffmpeg/ffmpeg` directly and
remains the fast loop for iterating on the configure line without paying for a
pack.

### Docs

- `docs/distribution.md`: status line corrected, and its "CI / release" section
  rewritten from a three-line sketch into a description of what exists.
- `README.md`: status paragraph updated; the "CI and the tagged GitHub release
  are not wired up yet" sentence goes away.
- A release runbook (`docs/releasing.md`): bump `version` **and** `ghVersion`,
  tag, wait for the chain, review the draft, publish, run the acceptance test.
  The `ghVersion` bump is the step most likely to be forgotten and the one whose
  omission is invisible — the release ships fine and simply never reaches
  existing users as an update.

## Build order

1. `packaging/pack.sh` de-IINA-ification; local `make pack` still green.
2. `IINA_AIRPLAY_HELPER` override in `helper/main_test.go`, with its unit test.
3. `verify.sh` native-x86_64 refinement, with its test case.
4. `packaging/test-package.sh`, the `Makefile` targets, and
   `packaging/check-release.sh` with its tests.
5. `.github/workflows/ci.yml`.
6. `.github/workflows/package.yml` — all three jobs.
7. `workflow_dispatch` dry run on master; fix whatever it surfaces.
8. Docs, README, release runbook.
9. Tag `v0.1.0`; review the draft; publish; acceptance test.

Steps 1–4 are ordinary local work with tests. Step 7 is not optional and not a
formality: until a workflow has run, nothing about it has been verified, and the
dry run is the only way to learn that before a tag exists.

## Done means

- A `workflow_dispatch` run on master completes all of `build` and
  `verify-intel` green, uploads the artifact, and creates no release.
- `v0.1.0` is pushed; the chain produces a draft release carrying exactly one
  `.iinaplgz`, its SHA-256 sidecar, and notes naming the ffmpeg source tarball.
- Both jobs' logs show `verify.sh` and `test-package.sh` passing against the
  same `.iinaplgz`: `build` reporting its native slice as `arm64`, and
  `verify-intel` reporting `uname -m` = `x86_64` and its native slice as
  `x86_64`. Between them, both slices of both shipped binaries have been
  executed through a real remux.
- A human publishes the draft, then installs `ozykhan/iina-airplay` **by repo
  slug** through IINA — not the local-package path — confirms the plugin loads,
  casts one real file to a TV, and confirms there is no `com.apple.quarantine`
  on the binaries under
  `~/Library/Application Support/com.colliderli.iina/plugins/`.

The last bullet is the one that closes the second unverified assumption. Until
it happens, the release path is still theory.

## Risks

- **`macos-15-intel` disappears in August 2027.** After that, GitHub Actions
  offers no x86_64 macOS runner at all and the Intel gate has to be dropped,
  moved to self-hosted hardware, or the x86_64 slice retired along with it. Not
  actionable now; recorded so the deadline is not rediscovered by a red build.
- **A silently arm64 `macos-15-intel`.** Covered by the hard `uname -m`
  assertion, which is the only reason it is worth writing.
- **`ffmpeg.org` unreachable.** The build fails on `curl` with no mirror
  fallback. Acceptable: the cache means it is hit rarely, and the pinned
  SHA-256 already guarantees a substituted tarball can never build.
- **A cold cache on an unrelated release.** A recipe edit or a 7-day cache
  eviction costs a 25–30 minute build on the tag push. Accepted; the
  `workflow_dispatch` rehearsal warms the cache under the same key before the
  tag exists.
- **`gh`'s notes composition.** Whether `--generate-notes` composes with
  `--notes-file` is unverified. Low cost, resolved at implementation time, and
  the hand-composed body is sufficient on its own.
