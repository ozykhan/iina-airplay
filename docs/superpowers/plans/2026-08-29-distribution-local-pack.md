# Distribution (local pack) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `make pack` produces a `.iinaplgz` containing the plugin JS, a universal ad-hoc-signed Go helper and a pinned universal LGPL ffmpeg, and mechanically verifies the package it just produced.

**Architecture:** Four independent shell scripts under `packaging/`, each with one responsibility — build ffmpeg, build the helper, assemble and pack, verify a produced package. They communicate only through files in the gitignored `build/` tree, so any one can be run and debugged alone. The gate on the whole thing is the existing Go e2e suite, pointed at the freshly built ffmpeg via an environment variable.

**Tech Stack:** bash, ffmpeg 9.x built from source, Go 1.26.4 (`CGO_ENABLED=0`), `lipo`/`codesign`/`otool` from Xcode CLT, `iina-plugin pack`, `node --test`, `go test`.

**Spec:** `docs/superpowers/specs/2026-08-29-distribution-local-pack-design.md`

## Global Constraints

Every task's requirements implicitly include this section.

- **Build host:** macOS 26.5.2, Apple Silicon (arm64). The x86_64 slice is built but never executed this round.
- **Never pass `--enable-gpl` or `--enable-nonfree` to ffmpeg configure.** LGPL is what keeps this plugin MIT-licensable. There is no reason to pass `--disable-gpl`; GPL is off by default.
- **Encoder allowlist, exactly:** `aac`, `eac3`, `hevc_videotoolbox`, `h264_videotoolbox`, `webvtt`. Nothing else.
- **Never disable decoders, demuxers, parsers or bitstream filters.** `helper/job.go:74` re-encodes non-H.264/HEVC video with `hevc_videotoolbox`, and encoding requires decoding.
- **Never disable filters.** ffmpeg auto-inserts `aresample` for the `-ac 6` audio downmix in `helper/job.go:81`. `--disable-filters` would break the lossless-audio path in a way no configure-line grep would catch.
- **`--disable-autodetect` is mandatory**, with `--enable-videotoolbox` and `--enable-zlib` re-enabled explicitly. Without it, configure silently links whatever Homebrew libraries it finds, producing a package that works only on the build machine.
- **Deployment targets:** `-mmacosx-version-min=10.15` for x86_64, `-mmacosx-version-min=11.0` for arm64 (arm64 cannot go lower).
- **Every binary is ad-hoc signed after `lipo`:** `codesign --force --sign - <path>`. `lipo` does not preserve slice signatures, and the entire no-notarization finding in `docs/distribution.md` rests on those signatures existing.
- **Go builds:** `CGO_ENABLED=0 GOOS=darwin GOARCH=arm64|amd64`. Module path is `github.com/ozykhan/iina-airplay/helper`.
- **`Info.json` update keys** (verified against IINA's `JavascriptPlugin.swift`): `ghRepo` is a **String** matching `^[\w-]+/[\w-]+$`; `ghVersion` is an **Int**, a monotonic counter, *not* the semver `version` string. An invalid `ghRepo` makes IINA refuse to load the plugin entirely.
- **`iina-plugin pack <dir>`** writes `<dirname>-<version>.iinaplgz` into the **current working directory**, not next to `<dir>`. The archive is flat — `Info.json` and `bin/` sit at the archive root, with no wrapper directory — and it preserves the executable bit. (All three verified by experiment 2026-08-29.)
- **Every script must run under bash 3.2.** `#!/usr/bin/env bash` on macOS
  resolves to `/bin/bash`, which Apple still ships as 3.2.57 for GPLv3 reasons.
  The trap that has already bitten this plan: in bash 3.2 a single `local`
  statement creates *all* its names unset before assigning any of them, so
  `local a="$1" b="$OUT/$a"` fails under `set -u` with `a: unbound variable`.
  Split any `local` whose later name references an earlier one. Also avoid
  bash 4+ features generally: associative arrays, `mapfile`/`readarray`,
  `${var,,}`/`${var^^}`, and `&>>`. Herestrings (`<<<`), process substitution
  (`< <(...)`) and `$(...)` are fine in 3.2.
- **`build/` is gitignored. Never commit a binary.**
- Run `make test` before every commit; it must stay green.

---

### Task 1: Repository licence and plugin manifest fields

The package cannot be distributed without a licence, and IINA cannot offer updates without the two `gh*` keys. Both are trivial and unblock nothing else, so they go first to get a clean base.

**Files:**
- Create: `LICENSE`
- Create: `plugin/tests/manifest.test.mjs`
- Modify: `plugin/Info.json`

**Interfaces:**
- Consumes: nothing.
- Produces: `plugin/Info.json` gains `"ghRepo": "ozykhan/iina-airplay"` (String) and `"ghVersion": 1` (Int). Task 6 reads `version` from this file; Task 7 asserts all three fields.

- [ ] **Step 1: Write the failing test**

Create `plugin/tests/manifest.test.mjs`:

```javascript
// Info.json is the one file IINA parses before any of our code runs: a bad
// ghRepo makes IINA refuse to load the plugin outright, and a ghVersion of the
// wrong JSON type silently disables update checks. Both are cheap to assert.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const info = JSON.parse(readFileSync(new URL("../Info.json", import.meta.url), "utf8"));

test("ghRepo matches IINA's githubRepoRegex", () => {
  assert.equal(typeof info.ghRepo, "string");
  assert.match(info.ghRepo, /^[\w-]+\/[\w-]+$/);
});

test("ghVersion is an integer, not the semver string", () => {
  assert.equal(typeof info.ghVersion, "number");
  assert.ok(Number.isInteger(info.ghVersion), "IINA casts ghVersion as? Int");
});

test("version is a semver string, kept separate from ghVersion", () => {
  assert.match(info.version, /^\d+\.\d+\.\d+$/);
});
```

- [ ] **Step 2: Run it to make sure it fails**

Run: `node --test plugin/tests/manifest.test.mjs`
Expected: FAIL — `ghRepo` and `ghVersion` are `undefined`, so the first two tests fail on the `typeof` assertions.

- [ ] **Step 3: Add the fields to Info.json**

Add both keys to `plugin/Info.json`, after `"version"`:

```json
  "ghRepo": "ozykhan/iina-airplay",
  "ghVersion": 1,
```

- [ ] **Step 4: Run the tests and make sure they pass**

Run: `node --test plugin/tests/manifest.test.mjs`
Expected: PASS, 3 tests.

- [ ] **Step 5: Add the MIT licence**

Create `LICENSE` with the standard MIT text, copyright `2026 Faruk Can Ozkan`, and append this paragraph after the MIT text so the ffmpeg situation is stated where people look for it:

```
---

Distributed release packages (.iinaplgz) additionally bundle an unmodified
build of FFmpeg (https://ffmpeg.org), configured with LGPL-licensed components
only and licensed under the GNU Lesser General Public License version 2.1 or
later. See bin/ffmpeg-LICENSE.md inside the package for the notice, the exact
upstream source tarball, and the configure line used to build it.
```

- [ ] **Step 6: Run the full suite and commit**

```bash
make test
git add LICENSE plugin/Info.json plugin/tests/manifest.test.mjs
git commit -m "dist: MIT licence and IINA update-check manifest fields"
```

---

### Task 2: The ffmpeg recipe, single architecture

The long pole. Build one architecture first — a universal build is roughly twice the wall clock, and every configure mistake shows up identically in one arch. Task 4 adds the second slice once the flags are known good.

**Files:**
- Create: `packaging/build-ffmpeg.sh`
- Modify: `.gitignore`

**Interfaces:**
- Consumes: nothing.
- Produces: `packaging/build-ffmpeg.sh [--arch native|universal]`, default `universal`. Writes the binary to `build/ffmpeg/ffmpeg`, a stamp to `build/ffmpeg/.recipe-hash`, and metadata to `build/ffmpeg/VERSION` containing three `key=value` lines: `ffmpeg_version=`, `source_sha256=`, `source_url=`. Tasks 3, 6 and 9 read these paths.

- [ ] **Step 1: Resolve the version pin and its hash**

Do not invent these values. Run:

```bash
curl -fsSL https://ffmpeg.org/releases/ | grep -oE 'ffmpeg-9\.[0-9]+(\.[0-9]+)?\.tar\.xz' | sort -uV | tail -3
```

Take the newest. **ffmpeg.org publishes no `.sha256` sidecar** — that URL 404s, verified 2026-08-29 — so verify the tarball against its GPG signature, which ffmpeg.org does publish, and only then compute the hash you will pin:

```bash
V=<version>   # e.g. 9.0.1
curl -fsSLO "https://ffmpeg.org/releases/ffmpeg-$V.tar.xz"
curl -fsSLO "https://ffmpeg.org/releases/ffmpeg-$V.tar.xz.asc"
gpg --verify "ffmpeg-$V.tar.xz.asc" "ffmpeg-$V.tar.xz"
shasum -a 256 "ffmpeg-$V.tar.xz"
```

The signature must verify against FFmpeg's documented release key before you trust the tarball. If it does not verify, stop and report it — that is a supply-chain signal, not a hiccup. Otherwise record the version and the computed hash as the literals in Step 2; from then on the pinned hash is what guards every later build.

- [ ] **Step 2: Write the recipe script**

Create `packaging/build-ffmpeg.sh`. Substitute the two literals resolved in Step 1 for `FFMPEG_VERSION` and `FFMPEG_SHA256`:

```bash
#!/usr/bin/env bash
# Builds the ffmpeg binary bundled in the .iinaplgz.
#
# Broad in, narrow out: decoders, demuxers, parsers and filters stay at
# upstream defaults so any file IINA opens, this ffmpeg opens — and so the
# hevc_videotoolbox re-encode branch in helper/job.go has decoders to work
# with. Trimming happens only on the output side.
set -euo pipefail

FFMPEG_VERSION="REPLACE_ME"
FFMPEG_SHA256="REPLACE_ME"

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT="$ROOT/build/ffmpeg"
SRC="$OUT/src"
TARBALL_URL="https://ffmpeg.org/releases/ffmpeg-${FFMPEG_VERSION}.tar.xz"

ARCH_MODE="universal"
[ "${1:-}" = "--arch" ] && ARCH_MODE="${2:-universal}"

# Any change to this script — a flag, the pinned version — invalidates the
# cached build. Hashing the script itself is the whole cache-key story.
RECIPE_HASH="$(shasum -a 256 "${BASH_SOURCE[0]}" | cut -d' ' -f1)"
STAMP="$OUT/.recipe-hash"
if [ -f "$OUT/ffmpeg" ] && [ -f "$STAMP" ] && [ "$(cat "$STAMP")" = "$RECIPE_HASH $ARCH_MODE" ]; then
  echo "build-ffmpeg: up to date ($ARCH_MODE); delete $OUT to force"
  exit 0
fi

mkdir -p "$OUT"
TARBALL="$OUT/ffmpeg-${FFMPEG_VERSION}.tar.xz"
if [ ! -f "$TARBALL" ]; then
  echo "build-ffmpeg: fetching $TARBALL_URL"
  curl -fsSL -o "$TARBALL" "$TARBALL_URL"
fi

ACTUAL="$(shasum -a 256 "$TARBALL" | cut -d' ' -f1)"
if [ "$ACTUAL" != "$FFMPEG_SHA256" ]; then
  echo "build-ffmpeg: SHA-256 mismatch for ffmpeg-${FFMPEG_VERSION}.tar.xz" >&2
  echo "  expected $FFMPEG_SHA256" >&2
  echo "  actual   $ACTUAL" >&2
  echo "Refusing to build an unverified source tree." >&2
  exit 1
fi

rm -rf "$SRC"
mkdir -p "$SRC"
tar -xJf "$TARBALL" -C "$SRC" --strip-components=1

# --disable-autodetect is load-bearing: without it configure links whatever
# Homebrew libraries happen to be installed, and the package works only on the
# machine that built it. Everything the pipeline needs is re-enabled by hand.
common_flags() {
  cat <<'FLAGS'
--disable-autodetect
--enable-videotoolbox
--enable-zlib
--disable-doc
--disable-debug
--disable-network
--disable-programs
--enable-ffmpeg
--disable-encoders
--enable-encoder=aac
--enable-encoder=eac3
--enable-encoder=hevc_videotoolbox
--enable-encoder=h264_videotoolbox
--enable-encoder=webvtt
--disable-muxers
--enable-muxer=hls
--enable-muxer=mp4
--enable-muxer=mov
--enable-muxer=webvtt
--enable-muxer=mpegts
FLAGS
}

build_one() {
  # Split declarations: bash 3.2 (what /bin/bash is on macOS) creates every
  # name in a single `local` unset before assigning any, so a later name
  # cannot reference an earlier one without tripping `set -u`.
  local arch="$1"
  local minver="$2"
  local extra="$3"
  local dest="$OUT/$arch"
  rm -rf "$dest" && mkdir -p "$dest"
  ( cd "$SRC" && make distclean >/dev/null 2>&1 || true )
  # shellcheck disable=SC2046
  ( cd "$SRC" && ./configure \
      --prefix="$dest" \
      $(common_flags) \
      --extra-cflags="-mmacosx-version-min=$minver" \
      --extra-ldflags="-mmacosx-version-min=$minver" \
      $extra \
    && make -j"$(sysctl -n hw.ncpu)" \
    && cp ffmpeg "$dest/ffmpeg" )
}

case "$ARCH_MODE" in
  native)
    build_one arm64 11.0 ""
    cp "$OUT/arm64/ffmpeg" "$OUT/ffmpeg"
    ;;
  universal)
    build_one arm64 11.0 ""
    build_one x86_64 10.15 "--enable-cross-compile --arch=x86_64 --cpu=x86_64 --target-os=darwin --cc=clang --extra-cflags=-arch\ x86_64 --extra-ldflags=-arch\ x86_64"
    lipo -create "$OUT/arm64/ffmpeg" "$OUT/x86_64/ffmpeg" -output "$OUT/ffmpeg"
    ;;
  *)
    echo "build-ffmpeg: unknown --arch '$ARCH_MODE' (want native or universal)" >&2
    exit 2
    ;;
esac

# lipo does not carry slice signatures across; sign the artifact that ships.
codesign --force --sign - "$OUT/ffmpeg"

cat > "$OUT/VERSION" <<EOF
ffmpeg_version=$FFMPEG_VERSION
source_sha256=$FFMPEG_SHA256
source_url=$TARBALL_URL
EOF

echo "$RECIPE_HASH $ARCH_MODE" > "$STAMP"
echo "build-ffmpeg: wrote $OUT/ffmpeg ($ARCH_MODE)"
```

Then `chmod +x packaging/build-ffmpeg.sh`.

- [ ] **Step 3: Add build outputs to .gitignore**

`build/` is already ignored, which covers `build/ffmpeg/`. Confirm with `git check-ignore -v build/ffmpeg/ffmpeg` and only edit `.gitignore` if that command prints nothing.

- [ ] **Step 4: Run the single-arch build**

Run: `./packaging/build-ffmpeg.sh --arch native`
Expected: completes in roughly 10–25 minutes and prints `build-ffmpeg: wrote .../build/ffmpeg/ffmpeg (native)`.

If configure rejects a flag, fix the flag rather than deleting the constraint it enforces — re-read Global Constraints before changing any `--disable-*`.

- [ ] **Step 5: Assert the build's properties by hand**

Run each and confirm the stated expectation:

```bash
build/ffmpeg/ffmpeg -hide_banner -version | head -1
```
Expected: the pinned version.

```bash
build/ffmpeg/ffmpeg -hide_banner -version | grep -c -- --enable-gpl || true
```
Expected: `0`.

```bash
for e in aac eac3 hevc_videotoolbox h264_videotoolbox webvtt; do
  build/ffmpeg/ffmpeg -hide_banner -encoders 2>/dev/null | grep -qw "$e" && echo "ok $e" || echo "MISSING $e"
done
```
Expected: five `ok` lines.

```bash
build/ffmpeg/ffmpeg -hide_banner -decoders 2>/dev/null | grep -cwE 'vp9|av1|hevc|h264|mpeg2video|vc1'
```
Expected: 6 or more — the decoders the transcode branch depends on.

```bash
otool -L build/ffmpeg/ffmpeg | tail -n +2 | grep -v -E '^\s+(/usr/lib|/System)' || echo "clean"
```
Expected: `clean`. Anything else — especially `/opt/homebrew` — means `--disable-autodetect` did not take, and the build must be redone.

- [ ] **Step 6: Commit**

```bash
git add packaging/build-ffmpeg.sh
git commit -m "packaging: pinned LGPL ffmpeg build recipe"
```

---

### Task 3: Gate the recipe on the real e2e suite

A configure line can satisfy every assertion in Task 2 and still have dropped a muxer the HLS path needs. The existing e2e suite is the only thing that proves otherwise, so it runs before any packaging work is built on top.

Fixtures keep using Homebrew's ffmpeg: they are synthesized with `libx264`, the `flac` and `srt` encoders and the `matroska` muxer, all of which the bundled build deliberately excludes. The result is a two-binary test — brew's ffmpeg authors a real MKV, the bundled ffmpeg casts it.

**Files:**
- Modify: `helper/e2e_test.go:17-27` (`findFFmpeg`)

**Interfaces:**
- Consumes: `build/ffmpeg/ffmpeg` from Task 2.
- Produces: `findFFmpeg` honours `$IINA_AIRPLAY_FFMPEG`; `findSystemFFmpeg` keeps the Homebrew lookup for fixture authoring. Task 9 wires both into a Makefile target.

- [ ] **Step 1: Write the failing test**

Add to `helper/e2e_test.go`:

```go
func TestFindFFmpegPrefersEnvOverride(t *testing.T) {
	fake := filepath.Join(t.TempDir(), "ffmpeg")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("IINA_AIRPLAY_FFMPEG", fake)
	if got := findFFmpeg(t); got != fake {
		t.Fatalf("findFFmpeg = %q, want the env override %q", got, fake)
	}
}
```

- [ ] **Step 2: Run it to make sure it fails**

Run: `cd helper && go test -run TestFindFFmpegPrefersEnvOverride ./...`
Expected: FAIL — `findFFmpeg` ignores the environment and returns the Homebrew path (or skips).

- [ ] **Step 3: Split the lookup in two**

Replace `findFFmpeg` in `helper/e2e_test.go` with:

```go
// findFFmpeg returns the ffmpeg under test — the pipeline's ffmpeg. Set
// IINA_AIRPLAY_FFMPEG to point the suite at the bundled build produced by
// packaging/build-ffmpeg.sh; a bad path fails loudly rather than falling back,
// so a typo can never be mistaken for a passing bundled build.
func findFFmpeg(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("IINA_AIRPLAY_FFMPEG"); p != "" {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("IINA_AIRPLAY_FFMPEG=%q is not usable: %v", p, err)
		}
		return p
	}
	return findSystemFFmpeg(t)
}

// findSystemFFmpeg returns a full-featured ffmpeg for authoring test fixtures.
// Fixtures need libx264, flac, srt and the matroska muxer — exactly what the
// bundled LGPL build excludes — so this deliberately stays on Homebrew's.
func findSystemFFmpeg(t *testing.T) string {
	t.Helper()
	for _, p := range []string{"/opt/homebrew/bin/ffmpeg", "/usr/local/bin/ffmpeg"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	t.Skip("no local ffmpeg; skipping real-media e2e")
	return ""
}
```

- [ ] **Step 4: Point fixture authoring at the system ffmpeg**

In both `TestRealFFmpegEndToEnd` and the subtitled e2e test, the fixture call must use the system binary while the helper under test uses `findFFmpeg`. Change each test's opening lines from the current single-binary form to:

```go
	ffmpeg := findFFmpeg(t)
	fixture := makeFixture(t, findSystemFFmpeg(t))
```

and, in the subtitled test:

```go
	ffmpeg := findFFmpeg(t)
	fixture := makeSubbedFixture(t, findSystemFFmpeg(t))
```

- [ ] **Step 5: Run the suite against the bundled ffmpeg**

```bash
cd helper && IINA_AIRPLAY_FFMPEG="$(cd .. && pwd)/build/ffmpeg/ffmpeg" go test ./... -v -run 'E2E|EndToEnd|Sub'
```
Expected: PASS. This is the gate — a failure here means the configure line is wrong, and Task 2 must be corrected before continuing. A missing muxer typically surfaces as an ffmpeg error about an unknown output format; a missing filter as a failure in the FLAC-to-E-AC-3 test only.

- [ ] **Step 6: Run the suite the ordinary way and commit**

```bash
make test
git add helper/e2e_test.go
git commit -m "helper: let the e2e suite run against the bundled ffmpeg"
```

---

### Task 4: The second architecture

Now that the flags are proven by a real cast, add the x86_64 slice. Kept separate because it can fail on its own terms — cross-compilation, not configuration — and because a reviewer might accept the recipe while rejecting how the slices are joined.

**Files:**
- Modify: `packaging/build-ffmpeg.sh` (the `universal` branch written in Task 2)

**Interfaces:**
- Consumes: `packaging/build-ffmpeg.sh` from Task 2.
- Produces: `build/ffmpeg/ffmpeg` as a two-slice universal binary. Task 7 asserts both slices are present.

- [ ] **Step 1: Run the universal build**

Run: `./packaging/build-ffmpeg.sh --arch universal`
Expected: both slices build and `lipo` succeeds. Roughly twice the single-arch wall clock.

The `universal` branch already exists from Task 2; this step exercises it for the first time. If the x86_64 pass fails in configure, the usual cause is `--cc` and the `-arch` flags disagreeing — confirm the SDK has an x86_64 slice with `ls $(xcrun --show-sdk-path)/usr/lib/libSystem.tbd` and that clang accepts `-arch x86_64` via `clang -arch x86_64 -x c -c /dev/null -o /dev/null`.

- [ ] **Step 2: Assert both slices are present and signed**

```bash
lipo -archs build/ffmpeg/ffmpeg
```
Expected: `x86_64 arm64` (order may vary).

```bash
codesign -dv build/ffmpeg/ffmpeg 2>&1 | grep -E 'Signature=adhoc|adhoc'
```
Expected: a line reporting an ad-hoc signature.

```bash
otool -L build/ffmpeg/ffmpeg | tail -n +2 | grep -v -E '^\s+(/usr/lib|/System)' || echo "clean"
```
Expected: `clean`.

- [ ] **Step 3: Re-run the e2e gate against the universal binary**

```bash
cd helper && IINA_AIRPLAY_FFMPEG="$(cd .. && pwd)/build/ffmpeg/ffmpeg" go test ./... -run 'E2E|EndToEnd|Sub'
```
Expected: PASS. The arm64 slice is what executes here; this confirms `lipo` and the re-signing did not damage it.

- [ ] **Step 4: Commit**

Nothing changed in the tree if Task 2's script was written correctly — the universal branch was already there. If Step 1 required fixes to the cross-compile flags:

```bash
git add packaging/build-ffmpeg.sh
git commit -m "packaging: fix x86_64 cross-compilation in the ffmpeg recipe"
```

Otherwise skip the commit and note in the task record that no changes were needed.

---

### Task 5: The universal, signed helper binary

Its own script and its own task, because the signing step is what the whole Gatekeeper finding rests on and it deserves to be independently rejectable.

**Files:**
- Create: `packaging/build-helper.sh`

**Interfaces:**
- Consumes: `helper/` Go module.
- Produces: `packaging/build-helper.sh` writing `build/helper/airplay-helper`, universal and ad-hoc signed. Task 6 copies it into the package.

- [ ] **Step 1: Write the script**

Create `packaging/build-helper.sh`:

```bash
#!/usr/bin/env bash
# Builds the universal, ad-hoc-signed airplay-helper that ships in the package.
#
# Go's linker ad-hoc signs darwin/arm64 binaries but not amd64, and lipo does
# not carry slice signatures across — so the universal artifact is signed here
# explicitly. IINA's installer applies no quarantine (docs/distribution.md), so
# this signature is the only thing standing between the user and a Gatekeeper
# refusal.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT="$ROOT/build/helper"
rm -rf "$OUT" && mkdir -p "$OUT"

for arch in arm64 amd64; do
  ( cd "$ROOT/helper" && CGO_ENABLED=0 GOOS=darwin GOARCH="$arch" \
      go build -trimpath -ldflags="-s -w" -o "$OUT/airplay-helper-$arch" . )
done

lipo -create "$OUT/airplay-helper-arm64" "$OUT/airplay-helper-amd64" \
     -output "$OUT/airplay-helper"
codesign --force --sign - "$OUT/airplay-helper"
rm -f "$OUT/airplay-helper-arm64" "$OUT/airplay-helper-amd64"

echo "build-helper: wrote $OUT/airplay-helper"
```

Then `chmod +x packaging/build-helper.sh`.

- [ ] **Step 2: Run it**

Run: `./packaging/build-helper.sh`
Expected: `build-helper: wrote .../build/helper/airplay-helper`, in seconds.

- [ ] **Step 3: Assert the result**

```bash
lipo -archs build/helper/airplay-helper
```
Expected: `x86_64 arm64`.

```bash
codesign -dv build/helper/airplay-helper 2>&1 | grep -i adhoc
```
Expected: an ad-hoc signature line. If this is empty the package will be refused on other people's machines — do not proceed.

```bash
build/helper/airplay-helper 2>&1 | head -3
```
Expected: the helper's usage output, proving the arm64 slice runs.

- [ ] **Step 4: Commit**

```bash
git add packaging/build-helper.sh
git commit -m "packaging: universal ad-hoc-signed helper build"
```

---

### Task 6: Assemble and pack

**Files:**
- Create: `packaging/pack.sh`

**Interfaces:**
- Consumes: `build/ffmpeg/ffmpeg` and `build/ffmpeg/VERSION` (Task 2/4), `build/helper/airplay-helper` (Task 5), `plugin/Info.json` (Task 1).
- Produces: `packaging/pack.sh` writing `build/iina-airplay.iinaplgz`, plus `bin/VERSIONS` and `bin/ffmpeg-LICENSE.md` inside it. Task 7 verifies that file.

- [ ] **Step 1: Write the script**

Create `packaging/pack.sh`:

```bash
#!/usr/bin/env bash
# Assembles the staging tree and produces build/iina-airplay.iinaplgz.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
STAGE="$ROOT/build/stage/iina-airplay"
FFMPEG="$ROOT/build/ffmpeg/ffmpeg"
FFVERSION="$ROOT/build/ffmpeg/VERSION"
HELPER="$ROOT/build/helper/airplay-helper"
IINA_PLUGIN="${IINA_PLUGIN:-/Applications/IINA.app/Contents/MacOS/iina-plugin}"

for f in "$FFMPEG" "$FFVERSION" "$HELPER"; do
  [ -f "$f" ] || { echo "pack: missing $f — run build-ffmpeg.sh and build-helper.sh first" >&2; exit 1; }
done

# shellcheck disable=SC1090
ffmpeg_version="$(grep '^ffmpeg_version=' "$FFVERSION" | cut -d= -f2-)"
source_sha256="$(grep '^source_sha256=' "$FFVERSION" | cut -d= -f2-)"
source_url="$(grep '^source_url=' "$FFVERSION" | cut -d= -f2-)"

rm -rf "$ROOT/build/stage" && mkdir -p "$STAGE/bin"
cp "$ROOT/plugin/Info.json" "$ROOT/plugin/main.js" "$ROOT/plugin/sidebar.html" "$STAGE/"
cp "$FFMPEG" "$STAGE/bin/ffmpeg"
cp "$HELPER" "$STAGE/bin/airplay-helper"
chmod 755 "$STAGE/bin/ffmpeg" "$STAGE/bin/airplay-helper"

helper_version="$(git -C "$ROOT" describe --tags --always --dirty 2>/dev/null || echo unknown)"

cat > "$STAGE/bin/VERSIONS" <<EOF
helper_version=$helper_version
helper_sha256=$(shasum -a 256 "$STAGE/bin/airplay-helper" | cut -d' ' -f1)
ffmpeg_version=$ffmpeg_version
ffmpeg_sha256=$(shasum -a 256 "$STAGE/bin/ffmpeg" | cut -d' ' -f1)
ffmpeg_source_sha256=$source_sha256
EOF

cat > "$STAGE/bin/ffmpeg-LICENSE.md" <<EOF
# FFmpeg

This package bundles an unmodified build of FFmpeg $ffmpeg_version, licensed
under the GNU Lesser General Public License version 2.1 or later. It was
configured with LGPL-licensed components only: no GPL components are enabled
and \`--enable-gpl\` was never passed.

- Upstream source: $source_url
- Source SHA-256: $source_sha256
- Build recipe: \`packaging/build-ffmpeg.sh\` in the iina-airplay repository,
  which contains the complete configure line used to produce this binary.

The LGPL text is available at https://www.gnu.org/licenses/lgpl-2.1.html.

FFmpeg is a trademark of Fabrice Bellard, originator of the FFmpeg project.
EOF

# iina-plugin pack writes <dirname>-<version>.iinaplgz into the CURRENT
# directory, not next to the source dir — so run it from a known cwd and move
# the result to a stable name.
version="$(sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$STAGE/Info.json" | head -1)"
[ -n "$version" ] || { echo "pack: cannot read version from Info.json" >&2; exit 1; }
( cd "$ROOT/build" && rm -f "iina-airplay-$version.iinaplgz" \
  && "$IINA_PLUGIN" pack "$STAGE" >/dev/null )
mv "$ROOT/build/iina-airplay-$version.iinaplgz" "$ROOT/build/iina-airplay.iinaplgz"

echo "pack: wrote $ROOT/build/iina-airplay.iinaplgz"
```

Then `chmod +x packaging/pack.sh`.

- [ ] **Step 2: Run it**

Run: `./packaging/pack.sh`
Expected: `pack: wrote .../build/iina-airplay.iinaplgz`.

- [ ] **Step 3: Inspect the archive by hand, once**

```bash
unzip -l build/iina-airplay.iinaplgz
```
Expected: `Info.json`, `main.js`, `sidebar.html` and `bin/` at the archive root with no wrapper directory, and `bin/` containing `airplay-helper`, `ffmpeg`, `VERSIONS`, `ffmpeg-LICENSE.md`.

```bash
unzip -p build/iina-airplay.iinaplgz bin/VERSIONS
```
Expected: five populated `key=value` lines, no empty values.

- [ ] **Step 4: Commit**

```bash
git add packaging/pack.sh
git commit -m "packaging: assemble and pack the .iinaplgz"
```

---

### Task 7: Verify the produced package

The safety net, so it gets tested itself. A `verify.sh` that passes everything is worse than none — it converts an unchecked package into a package everyone believes is checked. The tests here synthesize deliberately broken packages and confirm each is rejected.

Verification runs against the **extracted archive**, never the staging tree: the staging tree is not what users receive.

**Files:**
- Create: `packaging/verify.sh`
- Create: `packaging/tests/verify.test.sh`

**Interfaces:**
- Consumes: a `.iinaplgz` path as `$1`.
- Produces: `packaging/verify.sh <package.iinaplgz>`, exit 0 on success, non-zero with a message naming the failed check otherwise. Task 9 calls it from `make pack`.

- [ ] **Step 1: Write the failing test**

Create `packaging/tests/verify.test.sh`:

```bash
#!/usr/bin/env bash
# Tests that verify.sh actually rejects broken packages. Structural checks run
# before any check that executes ffmpeg, so these fixtures need only a stub
# binary to reach the assertion under test.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
VERIFY="$ROOT/packaging/verify.sh"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
fails=0

# Builds a minimal package; callers mutate the staging dir via the hook first.
make_pkg() {
  # Split declarations — see the bash 3.2 note in Global Constraints.
  local name="$1"
  local hook="$2"
  local d="$TMP/$name"
  rm -rf "$d" && mkdir -p "$d/src/bin"
  cat > "$d/src/Info.json" <<'JSON'
{"name":"AirPlay","identifier":"dev.faruk.iina-airplay","version":"0.1.0",
 "ghRepo":"ozykhan/iina-airplay","ghVersion":1,"entry":"main.js","permissions":[]}
JSON
  echo "// plugin" > "$d/src/main.js"
  printf '#!/bin/sh\nexit 0\n' > "$d/src/bin/airplay-helper"
  printf '#!/bin/sh\nexit 0\n' > "$d/src/bin/ffmpeg"
  chmod 755 "$d/src/bin/airplay-helper" "$d/src/bin/ffmpeg"
  "$hook" "$d/src"
  ( cd "$d/src" && zip -q -r "$d/pkg.iinaplgz" . )
  echo "$d/pkg.iinaplgz"
}

expect_fail() {
  local label="$1" pkg="$2" pattern="$3" out
  out="$("$VERIFY" "$pkg" 2>&1)"
  if [ $? -eq 0 ]; then
    echo "FAIL: $label — verify.sh accepted a package it should have rejected"
    fails=$((fails + 1))
  elif ! grep -qi "$pattern" <<<"$out"; then
    echo "FAIL: $label — rejected, but the message did not mention '$pattern':"
    echo "$out" | sed 's/^/    /'
    fails=$((fails + 1))
  else
    echo "ok: $label"
  fi
}

noop() { :; }
drop_ffmpeg()    { rm -f "$1/bin/ffmpeg"; }
unexecutable()   { chmod 644 "$1/bin/airplay-helper"; }
quarantine()     { xattr -w com.apple.quarantine "0081;0;test;" "$1/bin/ffmpeg"; }
bad_ghrepo()     { /usr/bin/sed -i '' 's|"ozykhan/iina-airplay"|"not a slug!"|' "$1/Info.json"; }

expect_fail "missing ffmpeg"        "$(make_pkg missing  drop_ffmpeg)"  "ffmpeg"
expect_fail "non-executable helper" "$(make_pkg noexec   unexecutable)" "executab"
expect_fail "quarantined binary"    "$(make_pkg quar     quarantine)"   "quarantine"
expect_fail "malformed ghRepo"      "$(make_pkg ghrepo   bad_ghrepo)"   "ghRepo"

if [ "$fails" -ne 0 ]; then
  echo "$fails verify.sh test(s) failed"
  exit 1
fi
echo "all verify.sh tests passed"
```

Then `chmod +x packaging/tests/verify.test.sh`.

- [ ] **Step 2: Run it to make sure it fails**

Run: `./packaging/tests/verify.test.sh`
Expected: FAIL — `packaging/verify.sh` does not exist yet, so every case errors.

- [ ] **Step 3: Write verify.sh**

Create `packaging/verify.sh`:

```bash
#!/usr/bin/env bash
# Verifies a packed .iinaplgz — the artifact users receive, not the staging
# tree. Checks are ordered cheapest-and-most-structural first so a failure
# names the real problem rather than a downstream symptom.
set -uo pipefail

PKG="${1:-}"
[ -n "$PKG" ] && [ -f "$PKG" ] || { echo "usage: verify.sh <package.iinaplgz>" >&2; exit 2; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
unzip -q "$PKG" -d "$TMP" || { echo "verify: cannot unzip $PKG" >&2; exit 1; }

fail() { echo "verify: FAILED — $*" >&2; exit 1; }

# --- manifest ---------------------------------------------------------------
[ -f "$TMP/Info.json" ] || fail "Info.json missing from the package root"
/usr/bin/python3 - "$TMP/Info.json" <<'PY' || exit 1
import json, re, sys
try:
    info = json.load(open(sys.argv[1]))
except Exception as e:
    print(f"verify: FAILED — Info.json does not parse: {e}", file=sys.stderr); sys.exit(1)
for key in ("identifier", "version", "ghRepo", "ghVersion"):
    if key not in info:
        print(f"verify: FAILED — Info.json has no {key}", file=sys.stderr); sys.exit(1)
if not re.fullmatch(r"[\w-]+/[\w-]+", str(info["ghRepo"])):
    print(f"verify: FAILED — ghRepo {info['ghRepo']!r} does not match IINA's "
          r"githubRepoRegex ^[\w-]+/[\w-]+$; IINA will refuse to load the plugin",
          file=sys.stderr); sys.exit(1)
if not isinstance(info["ghVersion"], int) or isinstance(info["ghVersion"], bool):
    print("verify: FAILED — ghVersion must be a JSON integer, not "
          f"{type(info['ghVersion']).__name__}", file=sys.stderr); sys.exit(1)
PY

# --- quarantine -------------------------------------------------------------
# Cheap, and ahead of the binary checks so a quarantined package is reported as
# quarantined rather than as some downstream symptom. IINA's installer applies
# no quarantine, so anything here means the package was assembled from
# already-quarantined inputs.
while IFS= read -r f; do
  xattr "$f" 2>/dev/null | grep -q 'com.apple.quarantine' \
    && fail "com.apple.quarantine is set on ${f#$TMP/}"
done < <(find "$TMP" -type f)

# --- binaries: presence, mode, architectures, signature ---------------------
for rel in bin/airplay-helper bin/ffmpeg; do
  b="$TMP/$rel"
  [ -f "$b" ] || fail "$rel missing from the package"
  [ -x "$b" ] || fail "$rel is not executable (mode $(stat -f '%Lp' "$b"))"
  archs="$(lipo -archs "$b" 2>/dev/null)"
  for want in x86_64 arm64; do
    grep -qw "$want" <<<"$archs" || fail "$rel is missing the $want slice (has: ${archs:-none})"
  done
  codesign -dv "$b" >/dev/null 2>&1 || fail "$rel has no valid code signature"
done

# --- ffmpeg licensing and capabilities --------------------------------------
FF="$TMP/bin/ffmpeg"
config="$("$FF" -hide_banner -version 2>/dev/null)" || fail "bin/ffmpeg does not run"
grep -q -- '--enable-gpl'     <<<"$config" && fail "bundled ffmpeg was built with --enable-gpl"
grep -q -- '--enable-nonfree' <<<"$config" && fail "bundled ffmpeg was built with --enable-nonfree"

encoders="$("$FF" -hide_banner -encoders 2>/dev/null)"
for e in aac eac3 hevc_videotoolbox h264_videotoolbox webvtt; do
  grep -qw "$e" <<<"$encoders" || fail "bundled ffmpeg lacks the $e encoder"
done
grep -qw libx264 <<<"$encoders" && fail "bundled ffmpeg contains libx264 (GPL)"

decoders="$("$FF" -hide_banner -decoders 2>/dev/null)"
for d in h264 hevc vp9 av1 mpeg2video; do
  grep -qw "$d" <<<"$decoders" \
    || fail "bundled ffmpeg lacks the $d decoder; helper/job.go's re-encode branch needs it"
done

# --- linkage ----------------------------------------------------------------
# The check that catches a package working only on the machine that built it.
strays="$(otool -L "$FF" | tail -n +2 | grep -v -E '^[[:space:]]+(/usr/lib|/System)' || true)"
[ -z "$strays" ] || fail "bin/ffmpeg links non-system libraries:
$strays"

echo "verify: OK — $(basename "$PKG")"
```

Then `chmod +x packaging/verify.sh`.

- [ ] **Step 4: Run the tests and make sure they pass**

Run: `./packaging/tests/verify.test.sh`
Expected: four `ok:` lines and `all verify.sh tests passed`.

The stub fixtures are shell scripts, so `lipo -archs` reports no slices for them. That is why the check order in `verify.sh` matters and is not arbitrary: manifest, then quarantine, then the binary loop (presence and mode before `lipo`), then ffmpeg behaviour, then linkage. Each fixture is designed to trip exactly one check before reaching one it cannot satisfy. If a case fails with the wrong message, fix the ordering in `verify.sh` — do not weaken a check to make a fixture pass.

- [ ] **Step 5: Verify the real package**

Run: `./packaging/verify.sh build/iina-airplay.iinaplgz`
Expected: `verify: OK — iina-airplay.iinaplgz`.

- [ ] **Step 6: Commit**

```bash
git add packaging/verify.sh packaging/tests/verify.test.sh
git commit -m "packaging: verify the produced .iinaplgz, and test the verifier"
```

---

### Task 8: Honest failure when the install is broken or the source is a stream

Two user-visible failures that the packaged build introduces or sharpens.

The first is the spec's requirement: a missing or non-executable binary currently surfaces as a raw `String(e)` from the `utils.exec` rejection, which tells the user nothing actionable.

The second falls out of `--disable-network`. `plugin/main.js` passes `mpv.getString("path")` straight to ffmpeg as `-i`, and that path is a URL whenever IINA is playing a network stream. Homebrew's ffmpeg would open it; the bundled one deliberately cannot. Without a guard this becomes a confusing ffmpeg error rather than a plain statement of scope, so the guard ships alongside the change that creates the need for it.

**Files:**
- Modify: `plugin/main.js` — `resolveBinDir` shell script and its `.then` handler, `startCast`, `module.exports`
- Modify: `plugin/tests/main.test.mjs`
- Modify: `plugin/tests/runtime.test.mjs` — `loadPlugin` harness

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: `isLocalSource(path) -> boolean`, exported from `main.js` alongside the existing four exports.

- [ ] **Step 1: Write the failing test for the source guard**

Add to `plugin/tests/main.test.mjs`, extending the destructured require at the top to include `isLocalSource`:

```javascript
test("isLocalSource accepts filesystem paths", () => {
  assert.equal(isLocalSource("/movies/a.mkv"), true);
  assert.equal(isLocalSource("/Volumes/Media/Show S01E01.mkv"), true);
});

test("isLocalSource rejects network streams the bundled ffmpeg cannot open", () => {
  // The bundled ffmpeg is built --disable-network, so these have no protocol
  // handler at all. Say so plainly rather than letting ffmpeg fail obscurely.
  for (const url of ["http://x/y.mp4", "https://x/y.mp4", "rtsp://x/y", "ytdl://abc"]) {
    assert.equal(isLocalSource(url), false, url);
  }
});
```

- [ ] **Step 2: Run it to make sure it fails**

Run: `node --test plugin/tests/main.test.mjs`
Expected: FAIL — `isLocalSource is not a function`.

- [ ] **Step 3: Implement and export isLocalSource**

Add near the other pure helpers in `plugin/main.js`, above the `module.exports` block:

```javascript
// mpv's "path" is a URL whenever IINA is playing a network stream. The bundled
// ffmpeg is built --disable-network and has no protocol handler for those, so
// the plugin declines up front instead of letting ffmpeg fail obscurely.
function isLocalSource(p) {
  return !!p && !/^[a-zA-Z][a-zA-Z0-9+.-]*:\/\//.test(p);
}
```

and add `isLocalSource: isLocalSource,` to `module.exports`.

- [ ] **Step 4: Run the test to verify it passes**

Run: `node --test plugin/tests/main.test.mjs`
Expected: PASS.

- [ ] **Step 5: Wire the guard into startCast**

In `plugin/main.js`, inside `startCast`, immediately after `var src = mpv.getString("path");` and its existing empty-check, add:

```javascript
    if (!isLocalSource(src)) {
      var streamMsg = "AirPlay can only cast local files, not network streams";
      core.osd("AirPlay: " + streamMsg);
      state = { phase: "error", url: null, pct: 0, msg: streamMsg };
      return;
    }
```

- [ ] **Step 6: Write the failing test for the broken-install message**

First extend the `loadPlugin` harness in `plugin/tests/runtime.test.mjs` so a test can script the `/bin/sh` bin-dir lookup. Replace the `utils` and `file` entries of the fake `iina` object with:

```javascript
    utils: {
      resolvePath: (p) => "/plugins/.data/dev.faruk.iina-airplay" + (p === "@tmp/hls" ? "/tmp/hls" : ""),
      exec: (bin, args) => {
        execs.push({ bin, args });
        // opts.binDirLookup scripts the /bin/sh lookup that resolveBinDir runs
        // when no bin dir is cached; everything else hangs, as before.
        if (bin === "/bin/sh" && opts.binDirLookup) return Promise.resolve(opts.binDirLookup);
        return new Promise(() => {});
      },
    },
    file: {
      read: () => (opts.binDirLookup ? "" : "/plugins/dev.faruk.iina-airplay/bin"),
      write: () => {},
    },
```

Then add the test:

```javascript
test("a broken install names the fix instead of leaking an exec error", async () => {
  // Exit 3 is resolveBinDir's signal for "found the plugin, but its binaries
  // are missing or not executable" — a hand-copied or quarantined install.
  const p = loadPlugin({ binDirLookup: { status: 3, stdout: "" } });
  p.clickMenu();
  await new Promise((r) => setImmediate(r)); // let the exec promise settle
  const s = p.state();
  assert.equal(s.phase, "error");
  assert.match(s.msg, /reinstall/i);
  assert.match(s.msg, /IINA/);
  assert.equal(serves(p).length, 0, "a broken install must not spawn the helper");
});
```

- [ ] **Step 7: Run it to make sure it fails**

Run: `node --test plugin/tests/runtime.test.mjs`
Expected: FAIL — the message is `cannot locate plugin bin directory`, which matches neither `/reinstall/i` nor `/IINA/`.

- [ ] **Step 8: Distinguish "not found" from "found but broken"**

In `plugin/main.js`, replace the shell script in `resolveBinDir` so it reports the two cases separately:

```javascript
    var script =
      'for d in "$1"/*/; do' +
      '  if /usr/bin/grep -qs \'"identifier": *"dev.faruk.iina-airplay"\' "$d/Info.json"; then' +
      '    if [ -x "$d"bin/airplay-helper ] && [ -x "$d"bin/ffmpeg ]; then' +
      '      printf %s "$d"bin; exit 0;' +
      '    fi;' +
      '    exit 3;' +           // found the plugin, but its binaries won't run
      '  fi;' +
      'done; exit 1';
```

and replace the `.then` handler's else-branch with:

```javascript
    utils.exec("/bin/sh", ["-c", script, "sh", pluginsDir]).then(function (r) {
      if (r.status === 0 && r.stdout) {
        file.write("@data/bindir.txt", r.stdout);
        cb(r.stdout, null);
      } else if (r.status === 3) {
        // Never offer to download anything — docs/distribution.md non-goals.
        cb(null, "the bundled helper or ffmpeg is missing or not executable; " +
                 "reinstall the plugin through IINA (Settings → Plugins → Install)");
      } else {
        cb(null, "cannot locate plugin bin directory");
      }
    }).catch(function (e) {
```

- [ ] **Step 9: Run the tests and make sure they pass**

Run: `make test`
Expected: PASS, all suites.

- [ ] **Step 10: Commit**

```bash
git add plugin/main.js plugin/tests/main.test.mjs plugin/tests/runtime.test.mjs
git commit -m "plugin: actionable errors for a broken install and for stream sources"
```

---

### Task 9: Makefile targets and documentation

**Files:**
- Modify: `Makefile`
- Modify: `README.md`
- Modify: `docs/distribution.md`

**Interfaces:**
- Consumes: all four `packaging/` scripts.
- Produces: `make ffmpeg`, `make pack`, `make verify`, `make test-bundled`.

- [ ] **Step 1: Add the packaging targets**

Replace the `.PHONY` line in `Makefile` and append the new targets, leaving `dev` untouched so the fast development loop keeps using Homebrew's ffmpeg and never waits on a source build:

```make
.PHONY: helper test dev clean ffmpeg pack verify test-bundled

BUNDLED_FFMPEG := $(CURDIR)/build/ffmpeg/ffmpeg

# Builds the pinned LGPL ffmpeg. Slow the first time (tens of minutes) and a
# no-op afterwards until packaging/build-ffmpeg.sh changes.
ffmpeg:
	./packaging/build-ffmpeg.sh

# The gate on the ffmpeg recipe: runs the real-media e2e suite with the bundled
# binary in the pipeline. Fixtures still use Homebrew's ffmpeg, which has the
# encoders the bundled build deliberately excludes.
test-bundled:
	cd helper && IINA_AIRPLAY_FFMPEG=$(BUNDLED_FFMPEG) go test ./...

pack: ffmpeg
	./packaging/build-helper.sh
	./packaging/pack.sh
	./packaging/verify.sh build/iina-airplay.iinaplgz

verify:
	./packaging/verify.sh build/iina-airplay.iinaplgz
```

- [ ] **Step 2: Add the verifier's own tests to `make test`**

Change the `test` target to:

```make
test:
	cd helper && go test ./...
	node --test 'plugin/tests/**/*.test.mjs'
	./packaging/tests/verify.test.sh
```

- [ ] **Step 3: Run everything**

```bash
make test
make pack
```
Expected: `make test` green including the verifier tests; `make pack` ends with `verify: OK — iina-airplay.iinaplgz`. `make pack` should not rebuild ffmpeg — the recipe-hash stamp makes it a no-op.

- [ ] **Step 4: Document installation for strangers**

In `README.md`, replace the "Packaged releases … are not built yet" sentence in the Status paragraph, and add this section directly above `## Dev quickstart`:

```markdown
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
```

- [ ] **Step 5: Bring docs/distribution.md up to date**

At the top of `docs/distribution.md`, under the existing decision block, add:

```markdown
> **Status 2026-08-29:** the package is built locally by `make pack` and
> verified by `packaging/verify.sh`. CI, the tagged GitHub release, and the
> x86_64 slice actually being *executed* rather than only structurally checked
> remain outstanding — see
> `docs/superpowers/specs/2026-08-29-distribution-local-pack-design.md`.
```

Then correct three details in that document that implementation settled:

- The "Package layout" block lists `bin/VERSIONS` as holding "helper + ffmpeg versions, SHA-256 of both". It now also records the ffmpeg *source* tarball SHA-256; update the line to say so.
- Add a line under "The ffmpeg build" noting that `--disable-autodetect` is required, and that filters and decoders must stay enabled because the `-ac 6` downmix auto-inserts `aresample` and the `hevc_videotoolbox` branch needs decoders.
- Add to "Non-goals": network-protocol support in the bundled ffmpeg — network stream sources are declined by the plugin with a clear message.

- [ ] **Step 6: Commit**

```bash
make test
git add Makefile README.md docs/distribution.md
git commit -m "dist: packaging make targets and install documentation"
```

---

### Task 10: Human acceptance

The done bar from the spec. Not automatable — someone has to watch the picture.

**Files:** none.

- [ ] **Step 1: Build a clean package from scratch**

```bash
rm -rf build && make pack
```
Expected: full rebuild, ending in `verify: OK`. This is the first end-to-end exercise of the whole chain with no cached state, and it is the last chance to catch a script that only works against a warm `build/` tree.

- [ ] **Step 2: Uninstall the development plugin**

In IINA → Settings → Plugins, remove the linked development plugin, so the packaged one cannot be confused with the symlinked source tree. Confirm with:

```bash
/Applications/IINA.app/Contents/MacOS/iina-plugin unlink build/iina-airplay.iinaplugin-dev
```

- [ ] **Step 3: Install the package**

IINA → Settings → Plugins → Install from local package → `build/iina-airplay.iinaplgz`. Restart IINA and enable the plugin.

- [ ] **Step 4: Cast a real file**

Open a video, use the AirPlay sidebar tab, pick the Apple TV, confirm the picture appears and plays. Prefer a file that exercises the interesting paths: an MKV with lossless audio (FLAC or TrueHD) and an embedded SRT subtitle track, which covers stream-copy video, the E-AC-3 downmix, and the WebVTT rendition in one go.

- [ ] **Step 5: Record the outcome**

If it works, note it in `README.md`'s status paragraph and commit. If it does not, capture the plugin console output and the helper's stderr before changing anything — a failure here is most likely a missing ffmpeg component that the e2e suite's synthetic fixtures did not exercise, and the exact ffmpeg error names it.

---

## Notes for the implementer

- **The ffmpeg build is the only slow step.** Everything else runs in seconds. Do not put a `make pack` in a tight edit loop until `build/ffmpeg/ffmpeg` exists and its stamp is warm.
- **When a check in `verify.sh` fails, fix the package, not the check.** The checks encode constraints from `docs/distribution.md`; weakening one to get a green run defeats the point of having it.
- **The x86_64 slice is never executed this round.** `lipo`, `codesign` and `otool` all pass on a binary that could still crash on an Intel Mac. That gap is deliberate and belongs to the CI round.
