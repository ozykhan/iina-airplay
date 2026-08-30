# CI and the First Tagged Release — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A `v*` tag push builds the `.iinaplgz` on GitHub Actions, verifies and remux-tests it on both arm64 and real Intel hardware, and attaches it to a draft GitHub release that a human publishes.

**Architecture:** Two workflows. `ci.yml` runs `make test` on every branch push and PR. `package.yml` runs a three-job chain — `build` (macos-15, arm64) → `verify-intel` (macos-15-intel) → `release` (draft, tags only). Both packaging jobs run the *same* two commands against the *same* artifact, so the only difference between them is the architecture executing it. Four new/changed shell scripts carry the logic so the YAML stays thin and the logic stays testable.

**Tech Stack:** GitHub Actions (macOS runners), bash 3.2 (macOS system bash), Go 1.26, `/usr/bin/python3` (Xcode CLT), `zip`/`unzip`, `codesign`/`lipo`/`otool`, `gh` CLI.

## Global Constraints

Copied verbatim from `docs/superpowers/specs/2026-08-30-ci-release-design.md` and `CLAUDE.md`. Every task's requirements implicitly include these.

- **Never commit a binary.** `build/` is gitignored and stays that way.
- **Ad-hoc signing only.** No Developer ID, no notarization, no secrets of any kind in any workflow.
- **NEVER run two builds in the same tree.** Concurrent `make`/`ar` writes silently corrupt the static archives.
- **macOS ships bash 3.2.** Under `set -u`, all words on a `local` line are expanded before any assignment lands — so `local a="$1" b="$a"` throws "unbound variable". Split declarations onto separate lines.
- **`cmd | grep -q pat` is unsafe under `set -o pipefail`.** `grep -q` exits on first match, the writer gets SIGPIPE, and pipefail reports 141 as the pipeline status. Capture into a variable first, then `grep <<<"$var"`.
- **Runner labels, checked 2026-08-30:** arm64 is `macos-15` / `macos-26` / `macos-latest`; Intel is the separate label set `macos-15-intel` / `macos-26-intel`. `macos-13` was retired in December 2025. `macos-15-intel` is the last x86_64 image and is available **until August 2027**.
- **The arm64 runners have no Rosetta.** `verify.sh`'s x86_64 assertions skip there. The Intel job is the only coverage, not redundant coverage.
- **Neither runner image ships `nasm` or `ffmpeg`.** Both ship Go 1.26.5, Node 22/24, Homebrew, and Xcode CLT (so `/usr/bin/python3`, `codesign`, `lipo`, `otool`, `clang` are present).
- **`nasm` is needed only for the x86_64 ffmpeg slice.** An arm64-only build gives no hint it is missing; `build-ffmpeg.sh` prechecks it.
- **The ffmpeg build is 25+ minutes** and stamps `build/ffmpeg/.recipe-hash` with `<sha256-of-build-ffmpeg.sh> <arch-mode>`, no-opping when it matches.
- **`Info.json`:** `ghRepo` is a String matching `^[\w-]+/[\w-]+$` (a malformed one makes IINA refuse to load the plugin entirely); `ghVersion` is an **Int**, a monotonic counter, **not** the semver string.
- **IINA reads `https://api.github.com/repos/<slug>/releases/latest`** and takes the **first asset whose name ends in `.iinaplgz`**. That endpoint excludes drafts *and* prereleases.
- **`iina-plugin pack` is exactly** `zip -ryq <out> . -x 'node_modules/*' -x '.*'` run from inside the plugin directory, producing an archive whose **root holds the plugin's contents** with no wrapping directory.
- **Action versions to use** (latest majors as of 2026-08-30): `actions/checkout@v7`, `actions/setup-go@v7`, `actions/setup-node@v7`, `actions/cache@v6`, `actions/upload-artifact@v7`, `actions/download-artifact@v8`.

---

### Task 1: `packaging/zip-plugin.sh` — pack without IINA

**Why a separate script rather than three lines inside `pack.sh`:** the claim this change rests on is that our zip is *equivalent to `iina-plugin pack`*, and equivalence is only credible if tests pin it. Logic inside `pack.sh` cannot be tested without first building ffmpeg (25+ minutes) and a signed universal helper. A 20-line script with one job can be tested in under a second.

**Files:**
- Create: `packaging/zip-plugin.sh`
- Create: `packaging/tests/zip-plugin.test.sh`
- Modify: `packaging/pack.sh` (remove lines 14 and 38; replace lines 147–157)

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: `packaging/zip-plugin.sh <stage-dir> <absolute-output.iinaplgz>` — exits 0 on success, 2 on usage error, 1 on a bad staging tree. Task 5 wires its test into `make test`.

- [ ] **Step 1: Write the failing test**

Create `packaging/tests/zip-plugin.test.sh` (mode 755):

```bash
#!/usr/bin/env bash
# Pins zip-plugin.sh's equivalence to `iina-plugin pack`. The properties below
# are the ones IINA's installer actually depends on: contents at the archive
# root (it unzips straight into the plugin directory), and executable bits
# preserved (utils.exec runs bin/ffmpeg and bin/airplay-helper directly).
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
ZIP_PLUGIN="$ROOT/packaging/zip-plugin.sh"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
fails=0

pass() { echo "ok: $1"; }
bad()  { echo "FAIL: $1"; fails=$((fails + 1)); }

# A staging tree with one of everything the exclusion rules care about.
# Split declarations — see the bash 3.2 note in Global Constraints.
make_stage() {
  local d="$1"
  rm -rf "$d" && mkdir -p "$d/bin" "$d/node_modules/leftpad"
  echo '{"name":"AirPlay","version":"0.1.0","entry":"main.js"}' > "$d/Info.json"
  echo '// plugin' > "$d/main.js"
  printf '#!/bin/sh\nexit 0\n' > "$d/bin/airplay-helper"
  chmod 755 "$d/bin/airplay-helper"
  echo 'secret' > "$d/.hidden"
  echo 'module.exports=1' > "$d/node_modules/leftpad/index.js"
}

stage="$TMP/stage"
out="$TMP/out.iinaplgz"
make_stage "$stage"

if ! "$ZIP_PLUGIN" "$stage" "$out" >/dev/null 2>&1; then
  bad "zip-plugin.sh failed on a valid staging tree"
else
  ex="$TMP/ex"
  rm -rf "$ex" && mkdir -p "$ex"
  unzip -q "$out" -d "$ex"

  # Contents at the archive ROOT, not wrapped in a directory named after the
  # staging dir. IINA unzips a .iinaplgz straight into the plugin folder, so a
  # wrapping directory would produce a plugin with no Info.json where IINA
  # looks for it.
  [ -f "$ex/Info.json" ] && pass "contents land at the archive root" \
    || bad "Info.json is not at the archive root (a wrapping directory got in)"

  # Executable bits survive. Without these, utils.exec cannot run the bundled
  # binaries and verify.sh's -x checks fail on the extracted tree.
  [ -x "$ex/bin/airplay-helper" ] && pass "executable bits survive the round trip" \
    || bad "bin/airplay-helper is not executable after unzip"

  [ ! -e "$ex/.hidden" ] && pass "dotfiles excluded" \
    || bad ".hidden was included; iina-plugin pack excludes it with -x '.*'"

  [ ! -e "$ex/node_modules" ] && pass "node_modules excluded" \
    || bad "node_modules was included; iina-plugin pack excludes it"
fi

# zip APPENDS to an existing archive rather than replacing it. A stale output
# file would silently ship the union of the old and new trees — the single
# most likely way this change goes wrong.
rm -f "$stage/main.js"
echo '// replaced' > "$stage/other.js"
"$ZIP_PLUGIN" "$stage" "$out" >/dev/null 2>&1
ex2="$TMP/ex2"
rm -rf "$ex2" && mkdir -p "$ex2"
unzip -q "$out" -d "$ex2"
if [ -e "$ex2/main.js" ]; then
  bad "re-packing appended to the existing archive; main.js survived its deletion"
else
  pass "re-packing replaces the archive instead of appending"
fi

# A relative output path would be resolved against the staging dir, since zip
# runs from inside it — the package would land in the wrong place, or inside
# itself. Refuse rather than surprise.
if "$ZIP_PLUGIN" "$stage" "relative.iinaplgz" >/dev/null 2>&1; then
  bad "a relative output path was accepted"
else
  pass "a relative output path is refused"
fi

if "$ZIP_PLUGIN" "$TMP/nope" "$TMP/x.iinaplgz" >/dev/null 2>&1; then
  bad "a non-existent staging directory was accepted"
else
  pass "a non-existent staging directory is refused"
fi

notplugin="$TMP/notplugin"
rm -rf "$notplugin" && mkdir -p "$notplugin"
echo hi > "$notplugin/readme.txt"
if "$ZIP_PLUGIN" "$notplugin" "$TMP/y.iinaplgz" >/dev/null 2>&1; then
  bad "a directory with no Info.json was accepted as a plugin"
else
  pass "a directory with no Info.json is refused"
fi

if [ "$fails" -ne 0 ]; then
  echo "$fails zip-plugin.sh test(s) failed"
  exit 1
fi
echo "all zip-plugin.sh tests passed"
```

Then `chmod 755 packaging/tests/zip-plugin.test.sh`.

- [ ] **Step 2: Run test to verify it fails**

Run: `./packaging/tests/zip-plugin.test.sh`
Expected: FAIL — every case reports failure because `packaging/zip-plugin.sh` does not exist yet.

- [ ] **Step 3: Write minimal implementation**

Create `packaging/zip-plugin.sh` (mode 755):

```bash
#!/usr/bin/env bash
# Packs a staged plugin directory into a .iinaplgz.
#
# This reproduces `iina-plugin pack` exactly. IINA's CLI runs, from inside the
# plugin directory:
#     zip -ryq <out> . -x 'node_modules/*' -x '.*'
# and that is the whole of it — a .iinaplgz is a zip whose ROOT holds the
# plugin's contents, with no wrapping directory. Doing it ourselves rather than
# shelling out to IINA.app keeps `make pack` working on machines that have no
# IINA installed (every CI runner, and any contributor who hasn't installed it),
# and gives local and CI packaging one code path instead of two. IINA only ever
# READS the package, by unzipping it, so there is nothing to diverge from.
set -euo pipefail

STAGE="${1:-}"
OUT="${2:-}"
[ -n "$STAGE" ] && [ -n "$OUT" ] \
  || { echo "zip-plugin: usage: zip-plugin.sh <stage-dir> <absolute-output.iinaplgz>" >&2; exit 2; }

# zip runs from inside $STAGE, so a relative output path would resolve against
# the staging tree — landing the package in the wrong place, or inside the
# archive it is being written from.
case "$OUT" in
  /*) ;;
  *) echo "zip-plugin: output path must be absolute (got '$OUT')" >&2; exit 2 ;;
esac

[ -d "$STAGE" ] || { echo "zip-plugin: $STAGE is not a directory" >&2; exit 1; }
[ -f "$STAGE/Info.json" ] \
  || { echo "zip-plugin: $STAGE has no Info.json — not a plugin directory" >&2; exit 1; }

# zip APPENDS to an existing archive rather than replacing it. Without this, a
# re-pack ships the union of the old and new trees, and the stale files are
# invisible in every check that only asserts presence.
rm -f "$OUT"

( cd "$STAGE" && zip -ryq "$OUT" . -x 'node_modules/*' -x '.*' )
```

Then `chmod 755 packaging/zip-plugin.sh`.

- [ ] **Step 4: Run test to verify it passes**

Run: `./packaging/tests/zip-plugin.test.sh`
Expected: PASS — `all zip-plugin.sh tests passed`

- [ ] **Step 5: Wire it into `pack.sh` and drop the IINA dependency**

Delete line 14 of `packaging/pack.sh`:

```bash
IINA_PLUGIN="${IINA_PLUGIN:-/Applications/IINA.app/Contents/MacOS/iina-plugin}"
```

Delete line 38:

```bash
[ -x "$IINA_PLUGIN" ] || { echo "pack: IINA_PLUGIN ($IINA_PLUGIN) not found or not executable — install IINA or set IINA_PLUGIN to the iina-plugin CLI path" >&2; exit 1; }
```

Replace this block at the end of the file:

```bash
# iina-plugin pack writes <dirname>-<version>.iinaplgz into the CURRENT
# directory, not next to the source dir — so run it from a known cwd and move
# the result to a stable name. It also only succeeds with a path RELATIVE to
# that cwd: an absolute path fails with "Cannot read plugin package content."
# (verified by experiment), so pass "stage/iina-airplay", not "$STAGE".
version="$(sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$STAGE/Info.json" | head -1)"
[ -n "$version" ] || { echo "pack: cannot read version from Info.json" >&2; exit 1; }
( cd "$ROOT/build" && rm -f "iina-airplay-$version.iinaplgz" \
  && "$IINA_PLUGIN" pack "stage/iina-airplay" >/dev/null )
mv "$ROOT/build/iina-airplay-$version.iinaplgz" "$CANONICAL_PKG"
```

with:

```bash
# Packs to the canonical path directly. The old iina-plugin CLI wrote
# <dirname>-<version>.iinaplgz into the current directory and had to be moved;
# zip-plugin.sh takes the destination as an argument, so the version-extraction
# and the mv both go away with it. See zip-plugin.sh for why we no longer
# shell out to IINA.app.
"$ROOT/packaging/zip-plugin.sh" "$STAGE" "$CANONICAL_PKG"
```

- [ ] **Step 6: Verify `pack.sh` no longer references IINA and still parses**

Run: `bash -n packaging/pack.sh && ! grep -nE '\$IINA_PLUGIN\b|"\$IINA_PLUGIN"' packaging/pack.sh && echo CLEAN`
Expected: prints `CLEAN`. (`bash -n` is a syntax check; the grep looks for an
actual use of the `$IINA_PLUGIN` variable — an invocation of the CLI — not for
the bare string "iina-plugin", which Step 5's replacement comment still
legitimately contains ("The old iina-plugin CLI wrote…"). A check for that
string would never pass.)

- [ ] **Step 7: Commit**

```bash
git add packaging/zip-plugin.sh packaging/tests/zip-plugin.test.sh packaging/pack.sh
git commit -m "packaging: pack the .iinaplgz without IINA installed

iina-plugin pack is exactly \`zip -ryq <out> . -x 'node_modules/*' -x '.*'\`
run from inside the plugin dir. Doing it ourselves gives local and CI one
packing code path and makes \`make pack\` work without IINA.app present.

Extracted rather than inlined because the claim this rests on is equivalence
to IINA's packer, and equivalence is only credible if tests pin it — logic
inside pack.sh is unreachable without a 25-minute ffmpeg build first.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 2: `IINA_AIRPLAY_HELPER` — run the shipped helper, not a fresh build

`buildHelper()` in `helper/main_test.go:18` compiles from source. Without this, pointing only `IINA_AIRPLAY_FFMPEG` at the package would test the shipped ffmpeg against a *freshly built native* helper, leaving the packaged helper's x86_64 slice as unexecuted on Intel as it is today.

**Files:**
- Modify: `helper/main_test.go:1-26` (imports and `buildHelper`)
- Test: `helper/main_test.go` (new `TestLocateHelper`)

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: `locateHelper(env string, stat func(string) error) (path string, build bool, fatal string)` and the constant `bundledHelperEnv = "IINA_AIRPLAY_HELPER"`. Task 5's `packaging/test-package.sh` sets that variable.

- [ ] **Step 1: Write the failing test**

Add to `helper/main_test.go`, immediately after the `buildHelper` function:

```go
func TestLocateHelper(t *testing.T) {
	statOK := func(string) error { return nil }
	statMissing := func(string) error { return errors.New("no such file or directory") }

	t.Run("unset builds from source", func(t *testing.T) {
		path, build, fatal := locateHelper("", statMissing)
		if !build || path != "" || fatal != "" {
			t.Fatalf("got (%q, %v, %q), want (\"\", true, \"\")", path, build, fatal)
		}
	})

	t.Run("set and present is used as-is", func(t *testing.T) {
		path, build, fatal := locateHelper("/pkg/bin/airplay-helper", statOK)
		if path != "/pkg/bin/airplay-helper" || build || fatal != "" {
			t.Fatalf("got (%q, %v, %q), want the bundled path with no build and no fatal", path, build, fatal)
		}
	})

	// The whole point of setting this variable is to exercise the SHIPPED
	// binary. A silent fallback to `go build` would let a typo in the CI
	// workflow read as a passing bundled-binary run — which is precisely the
	// failure the Intel job exists to rule out. Same contract as findFFmpeg.
	t.Run("set but unusable is fatal, never a fallback", func(t *testing.T) {
		_, build, fatal := locateHelper("/nope/airplay-helper", statMissing)
		if build {
			t.Fatal("a bad IINA_AIRPLAY_HELPER fell back to building from source; a typo must fail loudly")
		}
		if fatal == "" {
			t.Fatal("want a fatal message, got none")
		}
		for _, want := range []string{"IINA_AIRPLAY_HELPER", "/nope/airplay-helper"} {
			if !strings.Contains(fatal, want) {
				t.Errorf("fatal message %q does not mention %q", fatal, want)
			}
		}
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd helper && go test -run TestLocateHelper ./...`
Expected: FAIL — `undefined: locateHelper` (and `undefined: errors` if the import is not yet added).

- [ ] **Step 3: Write minimal implementation**

In `helper/main_test.go`, add `"errors"` to the import block (alphabetically, after `"encoding/json"`):

```go
import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)
```

Replace the existing `buildHelper` (lines 17–26) with:

```go
// bundledHelperEnv names the environment variable that points the suite at an
// already-built helper — the universal binary inside a packed .iinaplgz —
// instead of compiling one from source. packaging/test-package.sh sets it.
const bundledHelperEnv = "IINA_AIRPLAY_HELPER"

// locateHelper is the pure decision behind buildHelper: given the value of
// IINA_AIRPLAY_HELPER and a stat function, it returns either the binary to use,
// a request to build one, or a fatal message. It takes no *testing.T so the
// decision can be table-tested with fake paths, mirroring locateSystemFFmpeg in
// e2e_test.go.
//
// A set-but-unusable path is FATAL, never a fall back to `go build`: the reason
// to set this variable at all is to exercise the shipped binary, so a silent
// fallback would let a typo be mistaken for a passing bundled-binary run.
func locateHelper(env string, stat func(string) error) (path string, build bool, fatal string) {
	if env == "" {
		return "", true, ""
	}
	if err := stat(env); err != nil {
		return "", false, fmt.Sprintf("%s=%q is not usable: %v", bundledHelperEnv, env, err)
	}
	return env, false, ""
}

// Builds the helper binary once for integration tests — or returns the bundled
// one when IINA_AIRPLAY_HELPER points at it. Every test that calls this then
// drives the shipped universal binary, not just the e2e ones, so the watchdog,
// the pidfile takeover and the stdout protocol get exercised on whichever
// architecture is running the suite.
func buildHelper(t *testing.T) string {
	t.Helper()
	path, build, fatal := locateHelper(os.Getenv(bundledHelperEnv), func(p string) error {
		_, err := os.Stat(p)
		return err
	})
	if fatal != "" {
		t.Fatal(fatal)
	}
	if !build {
		return path
	}
	bin := filepath.Join(t.TempDir(), "airplay-helper")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd helper && go test ./...`
Expected: PASS — the whole suite, not just `TestLocateHelper`. `buildHelper` is used by four tests; a regression there shows up here.

- [ ] **Step 5: Prove the override actually reaches the binaries**

Run:

```bash
cd helper && go build -o /tmp/ap-helper-probe . && IINA_AIRPLAY_HELPER=/tmp/ap-helper-probe go test -run TestServeEndToEndWithStub ./... && IINA_AIRPLAY_HELPER=/does/not/exist go test -run TestServeEndToEndWithStub ./... ; echo "exit=$?"
```

Expected: the first `go test` PASSES using the prebuilt binary; the second FAILS with a message naming `IINA_AIRPLAY_HELPER` and `/does/not/exist`, and the final line prints a non-zero `exit=`.

- [ ] **Step 6: Commit**

```bash
git add helper/main_test.go
git commit -m "helper: let the test suite drive a bundled helper binary

IINA_AIRPLAY_HELPER mirrors IINA_AIRPLAY_FFMPEG's contract, including its
refusal to fall back: a bad path is fatal, so a typo in CI can never read as
a passing bundled-binary run. Without this the Intel job would test the
shipped ffmpeg against a freshly built native helper, leaving the packaged
helper's x86_64 slice as unexecuted as it is today.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 3: `verify.sh` — don't fake a Rosetta pass on a native x86_64 host

On the Intel runner the x86_64 slice *is* the native slice, so running the same assertions again through `arch -x86_64` reads in the log as coverage it is not.

**Files:**
- Modify: `packaging/verify.sh` (add `HOST_ARCH` near line 10; replace lines 180–186)
- Modify: `packaging/tests/verify.test.sh` (new case before the "positive case" block at the end)

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: the `VERIFY_HOST_ARCH` test seam and two stable note strings (`skipping the redundant Rosetta pass`, `Rosetta (arch -x86_64) is unavailable`). Task 7's workflow reads neither; only the tests do.

- [ ] **Step 1: Write the failing test**

In `packaging/tests/verify.test.sh`, insert this block immediately **before** the `# --- positive case: a real, freshly-packed .iinaplgz must verify OK` comment:

```bash
# --- a native-x86_64 host runs no Rosetta pass --------------------------------
# On Intel the x86_64 slice IS the native slice, so a second pass through
# `arch -x86_64` re-runs identical assertions under a different label and reads
# in the log as coverage it is not. VERIFY_HOST_ARCH exists so this can be
# exercised from an arm64 machine.
#
# The fixture's stub ffmpeg makes the NATIVE pass fail at the encoder
# assertions, and that is deliberate: verify.sh must announce its slice plan
# BEFORE running any slice check, or a failing native pass would exit first and
# leave no record of whether x86_64 was covered.
arch_pkg="$(make_pkg archnote fake_ffmpeg_binary)"
arch_out="$(VERIFY_SRC_ROOT="$DUMMY_SRC_ROOT" VERIFY_HOST_ARCH=x86_64 "$VERIFY" "$arch_pkg" 2>&1)"
if grep -q 'skipping the redundant Rosetta pass' <<<"$arch_out"; then
  echo "ok: native-x86_64 host announces that it skips the Rosetta pass"
else
  echo "FAIL: native-x86_64 host — verify.sh did not announce skipping the Rosetta pass:"
  echo "$arch_out" | sed 's/^/    /'
  fails=$((fails + 1))
fi
# The native pass labels itself "x86_64 (native)"; a separate Rosetta pass
# labels itself "x86_64". Finding the bare label means the second pass ran.
if grep -q '(x86_64 slice)' <<<"$arch_out"; then
  echo "FAIL: native-x86_64 host — verify.sh still ran a separate x86_64 (Rosetta) pass"
  fails=$((fails + 1))
else
  echo "ok: native-x86_64 host runs no separate Rosetta pass"
fi
```

- [ ] **Step 2: Run test to verify it fails**

Run: `./packaging/tests/verify.test.sh`
Expected: FAIL — `native-x86_64 host — verify.sh did not announce skipping the Rosetta pass`. (The second assertion may already pass by accident on an arm64 host without Rosetta; the first is the one that must fail.)

- [ ] **Step 3: Write minimal implementation**

In `packaging/verify.sh`, immediately after the `SRC_ROOT=` line (~line 10), add:

```bash
# Overridable so packaging/tests/verify.test.sh can exercise the x86_64-native
# path from an arm64 machine, the same way VERIFY_SRC_ROOT lets it isolate the
# staleness check. Nothing outside the tests should set it.
HOST_ARCH="${VERIFY_HOST_ARCH:-$(uname -m)}"
```

Replace this block (lines 180–186):

```bash
check_ffmpeg_slice "$(uname -m) (native)" ""

if /usr/bin/arch -x86_64 /usr/bin/true >/dev/null 2>&1; then
  check_ffmpeg_slice "x86_64" "1"
else
  echo "verify: note — Rosetta (arch -x86_64) is unavailable on this machine; skipping the x86_64-slice ffmpeg assertions" >&2
fi
```

with:

```bash
# Decide and ANNOUNCE which slices get checked before running any of them. A
# note printed after the native pass would never appear when that pass fails
# and exits — leaving no record in the log of whether the x86_64 slice was
# covered, which is exactly the question CI needs the log to answer.
run_rosetta_pass=""
if [ "$HOST_ARCH" = "x86_64" ]; then
  echo "verify: note — the native slice is x86_64, so its assertions run natively; skipping the redundant Rosetta pass" >&2
elif /usr/bin/arch -x86_64 /usr/bin/true >/dev/null 2>&1; then
  run_rosetta_pass=1
else
  echo "verify: note — Rosetta (arch -x86_64) is unavailable on this machine; skipping the x86_64-slice ffmpeg assertions" >&2
fi

check_ffmpeg_slice "$HOST_ARCH (native)" ""
if [ -n "$run_rosetta_pass" ]; then
  check_ffmpeg_slice "x86_64" "1"
fi
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `./packaging/tests/verify.test.sh`
Expected: PASS — `all verify.sh tests passed`, including both new `ok:` lines.

- [ ] **Step 5: Commit**

```bash
git add packaging/verify.sh packaging/tests/verify.test.sh
git commit -m "packaging: skip the Rosetta pass when x86_64 is already native

On the Intel runner the x86_64 slice is the native slice, so the second pass
re-ran identical assertions under a different label. The slice plan now prints
before any check runs, so a failing native pass still leaves a record of
whether x86_64 was covered.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 4: `packaging/check-release.sh` — the tag gate

Runs in seconds, before the 25-minute build. A tag that disagrees with `Info.json`, or a forgotten `ghVersion` bump, both produce a release that looks fine and misbehaves — the second one silently, since existing users are simply never offered the update.

**Files:**
- Create: `packaging/check-release.sh`
- Create: `packaging/tests/check-release.test.sh`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: `packaging/check-release.sh <tag>` — exit 0 on OK, 1 on a gate failure, 2 on usage error. Honors `CHECK_RELEASE_ROOT` (defaults to the repo root) so the tests can point it at a throwaway repository. Task 5 wires its test into `make test`; Task 7 calls it from the workflow.

- [ ] **Step 1: Write the failing test**

Create `packaging/tests/check-release.test.sh` (mode 755):

```bash
#!/usr/bin/env bash
# Exercises the tag gate against throwaway git repositories, so the assertions
# can be checked without pushing a deliberately bad tag at the real repo.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CHECK="$ROOT/packaging/check-release.sh"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
fails=0

# Builds a repo whose history is one commit per (version, ghVersion, tag)
# triple. Pass triples as "version:ghVersion:tag"; an empty tag leaves the
# commit untagged. Split declarations — see the bash 3.2 note in Global
# Constraints.
make_repo() {
  local name="$1"
  shift
  local d="$TMP/$name"
  rm -rf "$d" && mkdir -p "$d/plugin"
  git -C "$d" init -q -b master
  git -C "$d" config user.email test@example.com
  git -C "$d" config user.name "Test"
  local triple version ghversion tag
  for triple in "$@"; do
    version="${triple%%:*}"
    ghversion="$(echo "$triple" | cut -d: -f2)"
    tag="$(echo "$triple" | cut -d: -f3)"
    cat > "$d/plugin/Info.json" <<JSON
{"name":"AirPlay","identifier":"dev.faruk.iina-airplay","version":"$version",
 "ghRepo":"ozykhan/iina-airplay","ghVersion":$ghversion,"entry":"main.js"}
JSON
    git -C "$d" add -A
    git -C "$d" commit -q -m "$version"
    [ -n "$tag" ] && git -C "$d" tag "$tag"
  done
  echo "$d"
}

expect_ok() {
  local label="$1" repo="$2" tag="$3" pattern="$4" out status
  out="$(CHECK_RELEASE_ROOT="$repo" "$CHECK" "$tag" 2>&1)"
  status=$?
  if [ "$status" -ne 0 ]; then
    echo "FAIL: $label — gate rejected a tag it should have accepted:"
    echo "$out" | sed 's/^/    /'
    fails=$((fails + 1))
  elif ! grep -qi "$pattern" <<<"$out"; then
    echo "FAIL: $label — accepted, but the message did not mention '$pattern':"
    echo "$out" | sed 's/^/    /'
    fails=$((fails + 1))
  else
    echo "ok: $label"
  fi
}

expect_fail() {
  local label="$1" repo="$2" tag="$3" pattern="$4" out status
  out="$(CHECK_RELEASE_ROOT="$repo" "$CHECK" "$tag" 2>&1)"
  status=$?
  if [ "$status" -eq 0 ]; then
    echo "FAIL: $label — gate accepted a tag it should have rejected:"
    echo "$out" | sed 's/^/    /'
    fails=$((fails + 1))
  elif ! grep -qi "$pattern" <<<"$out"; then
    echo "FAIL: $label — rejected, but the message did not mention '$pattern':"
    echo "$out" | sed 's/^/    /'
    fails=$((fails + 1))
  else
    echo "ok: $label"
  fi
}

# The first release has no previous tag to compare against. A gate that cannot
# pass on v0.1.0 is a gate that gets disabled, so this case is load-bearing.
first="$(make_repo first "0.1.0:1:v0.1.0")"
expect_ok "first release, no previous tag" "$first" v0.1.0 "first release"

mismatch="$(make_repo mismatch "0.1.0:1:v0.2.0")"
expect_fail "tag does not match Info.json version" "$mismatch" v0.2.0 "does not match"

bumped="$(make_repo bumped "0.1.0:1:v0.1.0" "0.2.0:2:v0.2.0")"
expect_ok "ghVersion increased" "$bumped" v0.2.0 "OK"

# The silent one: the release ships fine and simply never reaches existing
# users, because IINA's update check compares ghVersion.
forgot="$(make_repo forgot "0.1.0:1:v0.1.0" "0.2.0:1:v0.2.0")"
expect_fail "ghVersion not bumped" "$forgot" v0.2.0 "ghVersion"

went_back="$(make_repo wentback "0.1.0:5:v0.1.0" "0.2.0:4:v0.2.0")"
expect_fail "ghVersion went backwards" "$went_back" v0.2.0 "ghVersion"

# ghVersion is an Int in IINA's schema. A quoted "2" parses as JSON and looks
# right in a diff.
strver="$(make_repo strver "0.1.0:\"2\":v0.1.0")"
expect_fail "ghVersion is a string" "$strver" v0.1.0 "integer"

if [ "$fails" -ne 0 ]; then
  echo "$fails check-release.sh test(s) failed"
  exit 1
fi
echo "all check-release.sh tests passed"
```

Then `chmod 755 packaging/tests/check-release.test.sh`.

- [ ] **Step 2: Run test to verify it fails**

Run: `./packaging/tests/check-release.test.sh`
Expected: FAIL — every case fails because `packaging/check-release.sh` does not exist.

- [ ] **Step 3: Write minimal implementation**

Create `packaging/check-release.sh` (mode 755):

```bash
#!/usr/bin/env bash
# Gates a release tag against plugin/Info.json — before the 25-minute build,
# so a mismatch costs seconds.
#
# Two things IINA cares about that nothing else checks:
#   - the tag must name the version the package will report, or the release
#     page and its payload disagree about what this is;
#   - ghVersion must go UP, or IINA's update check never fires. That failure is
#     silent: the release ships fine, installs fine for new users, and simply
#     never reaches anyone who already has the plugin.
set -uo pipefail

# Overridable so packaging/tests/check-release.test.sh can point the gate at a
# throwaway repository instead of this one.
ROOT="${CHECK_RELEASE_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
INFO="$ROOT/plugin/Info.json"

TAG="${1:-}"
[ -n "$TAG" ] || { echo "check-release: usage: check-release.sh <tag>" >&2; exit 2; }

fail() { echo "check-release: FAILED — $*" >&2; exit 1; }

[ -f "$INFO" ] || fail "$INFO not found"

# One python call reads and type-checks both fields, printing them on two
# lines. Capturing stderr into the same variable means a parse error or a type
# error arrives as the failure message rather than as an empty result.
if ! info_fields="$(/usr/bin/python3 - "$INFO" 2>&1 <<'PY'
import json, sys
try:
    info = json.load(open(sys.argv[1]))
except Exception as e:
    sys.exit(f"Info.json does not parse: {e}")
version = info.get("version")
if not isinstance(version, str) or not version:
    sys.exit("Info.json has no string version")
gh = info.get("ghVersion")
# bool is a subclass of int in Python, and `true` is not a version counter.
if not isinstance(gh, int) or isinstance(gh, bool):
    sys.exit(f"ghVersion must be a JSON integer, not {type(gh).__name__}")
print(version)
print(gh)
PY
)"; then
  fail "$info_fields"
fi

version="$(sed -n 1p <<<"$info_fields")"
ghversion="$(sed -n 2p <<<"$info_fields")"

[ "$TAG" = "v$version" ] \
  || fail "tag $TAG does not match plugin/Info.json version $version — expected tag v$version"

# --abbrev=0 on the tag's PARENT gives the nearest tag strictly before this
# one. Requires unshallowed history with tags: actions/checkout must run with
# fetch-depth: 0, or this finds nothing and the monotonicity check is skipped
# exactly when it is needed.
prev="$(git -C "$ROOT" describe --tags --abbrev=0 "$TAG^" 2>/dev/null || true)"

if [ -z "$prev" ]; then
  echo "check-release: no tag before $TAG; skipping the ghVersion monotonicity check (first release)"
else
  prev_info="$(git -C "$ROOT" show "$prev:plugin/Info.json" 2>/dev/null)" \
    || fail "cannot read plugin/Info.json at $prev"
  # Read offline from git rather than from the GitHub API, so the gate works on
  # a fork, on a detached checkout, and with no network.
  if ! prev_gh="$(/usr/bin/python3 -c 'import json,sys; g=json.load(sys.stdin).get("ghVersion"); sys.exit("previous tag ghVersion is not an integer") if not isinstance(g,int) or isinstance(g,bool) else print(g)' <<<"$prev_info" 2>&1)"; then
    fail "$prev_gh (at $prev)"
  fi
  if [ "$ghversion" -le "$prev_gh" ]; then
    fail "ghVersion did not increase: $prev has $prev_gh, $TAG has $ghversion. IINA's update check compares ghVersion, so existing users would never be offered this release."
  fi
fi

echo "check-release: OK — $TAG matches version $version, ghVersion $ghversion"
```

Then `chmod 755 packaging/check-release.sh`.

- [ ] **Step 4: Run test to verify it passes**

Run: `./packaging/tests/check-release.test.sh`
Expected: PASS — `all check-release.sh tests passed`

- [ ] **Step 5: Sanity-check it against the real repo**

Run: `./packaging/check-release.sh v0.1.0`
Expected: `check-release: OK — v0.1.0 matches version 0.1.0, ghVersion 1` followed by the no-previous-tag note — this repo has no tags yet, so the monotonicity branch is skipped. Then run `./packaging/check-release.sh v9.9.9` and expect a `does not match` failure with exit 1.

- [ ] **Step 6: Commit**

```bash
git add packaging/check-release.sh packaging/tests/check-release.test.sh
git commit -m "packaging: gate release tags against Info.json

Asserts the tag names the version the package reports, and that ghVersion
increased since the previous tag — read offline via git show, so the gate
works with no network. No previous tag skips the monotonicity check rather
than failing it; v0.1.0 is exactly that case, and a gate that cannot pass on
the first release is a gate that gets disabled.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 5: `packaging/test-package.sh` and the Makefile targets

`verify.sh` proves the package is *shaped* right. It cannot prove the configure line kept every muxer the HLS path writes — round 1's spec: "a configure line can satisfy every grep in `verify.sh` and still have dropped a muxer the HLS path needs." Only a real remux can. This is the one command both CI jobs run against the same artifact.

**Files:**
- Create: `packaging/test-package.sh`
- Modify: `Makefile` (`.PHONY`, `test`, new `test-package`, `pack`)

**Interfaces:**
- Consumes: `IINA_AIRPLAY_HELPER` (Task 2), `packaging/tests/zip-plugin.test.sh` (Task 1), `packaging/tests/check-release.test.sh` (Task 4).
- Produces: `packaging/test-package.sh <package.iinaplgz>` — exit 0 on pass, 1 on a bad package or a failing suite, 2 on usage error. Task 7 calls it from both packaging jobs.

- [ ] **Step 1: Write the failing test**

There is no unit-test harness for this script — it is a five-line wrapper whose only real logic is refusing to run against a package it cannot execute. That refusal is what the check below exercises, using a deliberately broken package.

Run this now, before writing anything, to confirm it fails:

```bash
tmp=$(mktemp -d) && mkdir -p "$tmp/src/bin" \
  && echo '{"entry":"main.js"}' > "$tmp/src/Info.json" \
  && printf '#!/bin/sh\nexit 0\n' > "$tmp/src/bin/ffmpeg" && chmod 644 "$tmp/src/bin/ffmpeg" \
  && ( cd "$tmp/src" && zip -qr "$tmp/broken.iinaplgz" . ) \
  && ./packaging/test-package.sh "$tmp/broken.iinaplgz"; echo "exit=$?"
```

Expected: `no such file or directory` and a non-zero `exit=` — the script does not exist yet.

- [ ] **Step 2: Write the implementation**

Create `packaging/test-package.sh` (mode 755):

```bash
#!/usr/bin/env bash
# Drives the helper test suite against the binaries inside a packed .iinaplgz.
#
# verify.sh proves the package is SHAPED right — architectures, signatures,
# licensing, the encoder allowlist, linkage. It cannot prove the configure line
# kept every muxer the HLS path writes; only a real remux can. Round 1's spec
# put it plainly: "a configure line can satisfy every grep in verify.sh and
# still have dropped a muxer the HLS path needs."
#
# Both CI jobs run this against the same artifact, so the arm64 and x86_64 runs
# differ only in the architecture executing it.
#
# Fixtures still come from a full-featured system ffmpeg (libx264, flac, srt,
# the matroska muxer — everything the bundled LGPL build deliberately excludes),
# which findSystemFFmpeg locates on its own.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PKG="${1:-}"
[ -n "$PKG" ] && [ -f "$PKG" ] \
  || { echo "test-package: usage: test-package.sh <package.iinaplgz>" >&2; exit 2; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
unzip -q "$PKG" -d "$TMP" || { echo "test-package: cannot unzip $PKG" >&2; exit 1; }

# Fail rather than let the suite skip. An acceptance run that can pass without
# executing the packaged binaries is worse than no acceptance run at all,
# because it reports success.
for rel in bin/ffmpeg bin/airplay-helper; do
  [ -x "$TMP/$rel" ] \
    || { echo "test-package: $rel is missing or not executable inside $PKG" >&2; exit 1; }
done

echo "test-package: driving $(basename "$PKG") through the helper suite on $(uname -m)"
cd "$ROOT/helper"
IINA_AIRPLAY_FFMPEG="$TMP/bin/ffmpeg" \
IINA_AIRPLAY_HELPER="$TMP/bin/airplay-helper" \
  go test ./...
```

Then `chmod 755 packaging/test-package.sh`.

- [ ] **Step 3: Run the check to verify it now rejects the broken package**

Run the same command from Step 1 again.
Expected: `test-package: bin/ffmpeg is missing or not executable inside …` and `exit=1`.

Then confirm the usage guard:

Run: `./packaging/test-package.sh; echo "exit=$?"`
Expected: the usage message and `exit=2`.

- [ ] **Step 4: Update the Makefile**

Replace the `.PHONY` line and the `test` and `pack` targets in `Makefile`, and add `test-package`:

```make
.PHONY: helper test test-package dev clean ffmpeg pack verify test-bundled

PKG := $(CURDIR)/build/iina-airplay.iinaplgz

test:
	cd helper && go test ./...
	node --test 'plugin/tests/**/*.test.mjs'
	./packaging/tests/verify.test.sh
	./packaging/tests/zip-plugin.test.sh
	./packaging/tests/check-release.test.sh

# The gate on the configure line, run against the artifact that ships rather
# than against build/ffmpeg/ffmpeg. Both CI jobs run exactly this.
test-package:
	./packaging/test-package.sh $(PKG)

pack:
	rm -f $(PKG)
	$(MAKE) ffmpeg
	./packaging/build-helper.sh
	./packaging/pack.sh
	./packaging/verify.sh $(PKG)
	./packaging/test-package.sh $(PKG)
```

Leave `verify`, `test-bundled`, `helper`, `dev`, `clean` and `ffmpeg` as they are, but change the `verify` target's argument to `$(PKG)` for consistency:

```make
verify:
	./packaging/verify.sh $(PKG)
```

`test-bundled` stays: it points at `build/ffmpeg/ffmpeg` directly and remains the fast loop for iterating on the configure line without paying for a pack.

- [ ] **Step 5: Run the full test suite**

Run: `make test`
Expected: PASS — go tests, node tests, and all three shell suites reporting `all … tests passed`. `verify.test.sh` will print `note: build/iina-airplay.iinaplgz not present; skipping the positive real-package test` in a fresh checkout; that is expected.

- [ ] **Step 6: Commit**

```bash
git add packaging/test-package.sh Makefile
git commit -m "packaging: run the helper suite against the packed artifact

verify.sh checks the package's shape; only a real remux checks that the
configure line kept every muxer the HLS path writes. One script so both CI
jobs run identical commands against identical bytes, differing only in the
architecture executing them.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 6: `.github/workflows/ci.yml`

**Files:**
- Create: `.github/workflows/ci.yml`

**Interfaces:**
- Consumes: `make test` as updated in Task 5.
- Produces: nothing other tasks depend on.

- [ ] **Step 1: Write the workflow**

Create `.github/workflows/ci.yml`:

```yaml
name: ci

# Tags are excluded: package.yml owns them and runs `make test` itself, so the
# release chain is self-contained rather than depending on a second workflow's
# status.
on:
  push:
    branches: ['**']
  pull_request:

permissions:
  contents: read

concurrency:
  group: ci-${{ github.ref }}
  cancel-in-progress: true

jobs:
  test:
    # Pinned, not macos-latest: package.yml pins macos-15 and macos-15-intel so
    # its two legs differ only in architecture, and this job should be running
    # the same OS they do. macos-latest moves without a commit.
    runs-on: macos-15
    steps:
      - uses: actions/checkout@v7

      - uses: actions/setup-go@v7
        with:
          go-version-file: helper/go.mod

      - uses: actions/setup-node@v7
        with:
          node-version: '22'

      # Load-bearing, not convenience. locateSystemFFmpeg SKIPS the real-media
      # e2e when no system ffmpeg is found and IINA_AIRPLAY_FFMPEG is unset, so
      # without this the suite reports green having never driven the pipeline
      # through ffmpeg at all. Fixtures need libx264, flac, srt and the
      # matroska muxer — exactly what the bundled LGPL build excludes.
      - name: Install ffmpeg for test fixtures
        run: brew install ffmpeg

      - name: Confirm the e2e suite will not silently skip
        run: test -x /opt/homebrew/bin/ffmpeg

      - run: make test
```

- [ ] **Step 2: Validate the YAML parses**

Run: `ruby -ryaml -e 'YAML.load_file(".github/workflows/ci.yml"); puts "ok"'`
Expected: `ok`. (macOS ships Ruby with a YAML parser; there is no system PyYAML, so this is the parse check available without installing anything.)

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: run the test suite on every branch push and PR

brew install ffmpeg is load-bearing rather than convenience: without a system
ffmpeg the real-media e2e skips instead of failing, and the job would report
green having never driven the pipeline through ffmpeg.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 7: `.github/workflows/package.yml`

**Files:**
- Create: `.github/workflows/package.yml`

**Files (continued):**
- Create: `packaging/release-notes.sh`

**Interfaces:**
- Consumes: `packaging/check-release.sh` (Task 4), `packaging/verify.sh` (Task 3), `packaging/test-package.sh` (Task 5), `packaging/pack.sh` (Task 1).
- Produces: `packaging/release-notes.sh <package.iinaplgz>` writing markdown to stdout; the artifact `iina-airplay-package` containing `iina-airplay.iinaplgz` and `iina-airplay.iinaplgz.sha256`; and a draft release on tag pushes.

- [ ] **Step 1: Write the release-notes generator**

The LGPL obligation in `docs/distribution.md` requires the release notes to name
the exact upstream source. Read it out of the package's own `bin/VERSIONS` and
`bin/ffmpeg-LICENSE.md` rather than restating it, so the notes cannot drift from
the binary they describe.

This is a script and not an inline workflow step for a concrete reason: a shell
heredoc inside a YAML block scalar carries the block's indentation into every
line it writes, which would render the whole markdown body as one code block.

Create `packaging/release-notes.sh` (mode 755):

```bash
#!/usr/bin/env bash
# Emits the GitHub release notes for a packed .iinaplgz, on stdout.
#
# Every fact here is read out of the package itself. The FFmpeg version, its
# upstream source URL and its source SHA-256 are an LGPL compliance
# requirement, not decoration — restating them by hand is how they drift from
# the binary actually shipped.
set -euo pipefail

PKG="${1:-}"
[ -n "$PKG" ] && [ -f "$PKG" ] \
  || { echo "release-notes: usage: release-notes.sh <package.iinaplgz>" >&2; exit 2; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
unzip -q "$PKG" -d "$TMP" || { echo "release-notes: cannot unzip $PKG" >&2; exit 1; }

need() {
  [ -n "$2" ] || { echo "release-notes: could not read $1 from $PKG" >&2; exit 1; }
}

ffmpeg_version="$(grep '^ffmpeg_version=' "$TMP/bin/VERSIONS" | cut -d= -f2-)"
source_sha256="$(grep '^ffmpeg_source_sha256=' "$TMP/bin/VERSIONS" | cut -d= -f2-)"
source_url="$(sed -n 's/^- Upstream source: //p' "$TMP/bin/ffmpeg-LICENSE.md" | head -1)"
pkg_sha256="$(shasum -a 256 "$PKG" | cut -d' ' -f1)"

need ffmpeg_version "$ffmpeg_version"
need ffmpeg_source_sha256 "$source_sha256"
need "the upstream source URL" "$source_url"

cat <<EOF
## Install

In IINA: **Settings → Plugins → Install**, and enter:

\`\`\`
ozykhan/iina-airplay
\`\`\`

Install **through IINA**, not by downloading the package below in a browser.
IINA's installer applies no \`com.apple.quarantine\`, so the bundled binaries run
under Gatekeeper on their ad-hoc signatures alone. Downloading the
\`.iinaplgz\` by hand and opening it quarantines everything inside it.

On macOS 15+, grant IINA the **Local Network** permission the first time it
casts, or the Apple TV cannot reach the stream.

Everything the plugin needs ships inside the package. There are no
prerequisites and nothing is downloaded at runtime.

## Checksums

| File | SHA-256 |
| --- | --- |
| \`$(basename "$PKG")\` | \`$pkg_sha256\` |

## Bundled FFmpeg

This package bundles an unmodified build of FFmpeg $ffmpeg_version, licensed
under the GNU Lesser General Public License version 2.1 or later. No GPL
components are enabled and \`--enable-gpl\` was never passed.

- Upstream source: $source_url
- Source SHA-256: \`$source_sha256\`
- Build recipe: \`packaging/build-ffmpeg.sh\` at this tag, which contains the
  complete configure line used to produce this binary.

A copy of the license text ships inside the package as
\`bin/COPYING.LGPLv2.1\`.
EOF
```

Then `chmod 755 packaging/release-notes.sh`.

Verify it parses and rejects bad input:

Run: `bash -n packaging/release-notes.sh && ./packaging/release-notes.sh; echo "exit=$?"`
Expected: the usage message and `exit=2`. (It cannot be run against a real package yet — no package exists in a fresh checkout. Task 8 exercises it against the dry-run artifact, which is the only chance to test the release job's one piece of real logic before a tag exists.)

- [ ] **Step 2: Write the workflow**

Create `.github/workflows/package.yml`:

```yaml
name: package

on:
  push:
    tags: ['v*']
  workflow_dispatch:
  pull_request:
    paths:
      - 'packaging/**'
      - 'helper/**'
      - 'plugin/**'
      - 'Makefile'
      - '.github/workflows/package.yml'

permissions:
  contents: read

concurrency:
  group: package-${{ github.ref }}
  # Superseding a branch run is fine. Cancelling a release mid-flight is not.
  cancel-in-progress: ${{ !startsWith(github.ref, 'refs/tags/') }}

jobs:
  build:
    runs-on: macos-15
    steps:
      - uses: actions/checkout@v7
        with:
          # pack.sh's `git describe --tags` (which supplies helper_version in
          # bin/VERSIONS) and check-release.sh's previous-tag lookup both need
          # real history WITH tags. The default shallow checkout gives neither,
          # and the failure is quiet: a package that records a short SHA where
          # a version belongs, and a monotonicity check that skips itself.
          fetch-depth: 0

      # First, so a mismatched tag fails in seconds instead of after the
      # 25-minute ffmpeg leg.
      - name: Gate the release tag
        if: startsWith(github.ref, 'refs/tags/v')
        run: ./packaging/check-release.sh "${{ github.ref_name }}"

      - name: Confirm this is an arm64 host
        run: |
          test "$(uname -m)" = arm64 || {
            echo "build must run on Apple Silicon: build-ffmpeg.sh's arm64 leg builds native and cross-compiles nothing" >&2
            exit 1
          }

      - uses: actions/setup-go@v7
        with:
          go-version-file: helper/go.mod

      - uses: actions/setup-node@v7
        with:
          node-version: '22'

      # nasm assembles the x86_64 slice's hand-written assembly; arm64 never
      # needs it, so an arm64-only build gives no hint it is missing.
      # ffmpeg authors the test fixtures, same reason as in ci.yml.
      - name: Install build and fixture dependencies
        run: brew install nasm ffmpeg

      # Before the long build, so an ordinary unit-test failure costs seconds.
      - run: make test

      # Split into restore + save (rather than a single actions/cache step):
      # actions/cache's own action.yml declares its save as a post-if:
      # "success()" step, so a single `cache` step here would only bank the
      # build if EVERY later step in this job also succeeded — discarding a
      # ffmpeg build that had already finished successfully if
      # build-helper.sh, pack.sh, verify.sh, test-package.sh, the checksum, or
      # the artifact upload then failed. A retry would pay for the full
      # ~25-minute build all over again, which is exactly the cost this cache
      # exists to avoid. Restoring here and saving right after the build step
      # below banks it the moment it exists, not at the end of a job that may
      # never get there.
      #
      # The key changes if and only if build-ffmpeg.sh changes: hashFiles()
      # hashes this file's bytes into the key, which is the same condition
      # that invalidates the script's own RECIPE_HASH stamp (also derived from
      # this file, via `shasum -a 256`) — so a restore hits exactly when the
      # recipe is unchanged, and misses exactly when the script's own stamp
      # would also call for a rebuild. (The two are not the same number —
      # hashFiles() hashes a list of per-file digests, not the file's own
      # digest — they just always change together.)
      #
      # Four files, not the tree: the configured source tree is gigabytes and
      # nothing downstream reads it — except COPYING.LGPLv2.1, which pack.sh
      # hard-fails without because LGPL 2.1 §1 requires shipping the license
      # text itself.
      #
      # No restore-keys: a near-miss restore has no value, since a mismatched
      # stamp forces a full rebuild anyway.
      - name: Restore the pinned ffmpeg build
        id: restore-ffmpeg-cache
        uses: actions/cache/restore@v6
        with:
          path: |
            build/ffmpeg/ffmpeg
            build/ffmpeg/VERSION
            build/ffmpeg/.recipe-hash
            build/ffmpeg/src/COPYING.LGPLv2.1
          key: ffmpeg-universal-macos15-${{ hashFiles('packaging/build-ffmpeg.sh') }}

      # A no-op on a cache hit, via the .recipe-hash stamp it writes itself.
      - name: Build the pinned LGPL ffmpeg
        run: ./packaging/build-ffmpeg.sh

      # Saved immediately after the build exists, rather than left to
      # actions/cache's own post-job save (see above) — `if: always()` banks
      # it even when a later step in this job fails. Saving under a key that
      # already exists (a cache hit above, where the build step was a no-op)
      # is a benign no-op, so this is safe to run unconditionally.
      - name: Save the pinned ffmpeg build
        if: always()
        uses: actions/cache/save@v6
        with:
          path: |
            build/ffmpeg/ffmpeg
            build/ffmpeg/VERSION
            build/ffmpeg/.recipe-hash
            build/ffmpeg/src/COPYING.LGPLv2.1
          key: ${{ steps.restore-ffmpeg-cache.outputs.cache-primary-key }}

      - name: Build the universal helper
        run: ./packaging/build-helper.sh

      - name: Pack
        run: ./packaging/pack.sh

      # Executes the arm64 slice natively; its x86_64 assertions skip for want
      # of Rosetta and are covered natively by verify-intel.
      - name: Verify the package
        run: ./packaging/verify.sh build/iina-airplay.iinaplgz

      # The other half. verify.sh checks shape; this drives a real remux, which
      # is the only thing that catches a dropped muxer. Without it nothing in
      # CI ever exercises the arm64 slices of the shipped binaries.
      - name: Drive the packed binaries through the helper suite
        run: ./packaging/test-package.sh build/iina-airplay.iinaplgz

      - name: Checksum
        run: cd build && shasum -a 256 iina-airplay.iinaplgz > iina-airplay.iinaplgz.sha256

      # Upload the PACKED file, never an unpacked tree: upload-artifact does
      # not preserve POSIX permission bits, so an unpacked tree would arrive on
      # the Intel runner with its executable bits stripped and verify.sh would
      # fail for a reason that has nothing to do with the package. The
      # .iinaplgz is one opaque file whose internal zip entries carry their own
      # modes, so it survives the round trip intact.
      - uses: actions/upload-artifact@v7
        with:
          name: iina-airplay-package
          path: |
            build/iina-airplay.iinaplgz
            build/iina-airplay.iinaplgz.sha256
          if-no-files-found: error

  verify-intel:
    needs: build
    runs-on: macos-15-intel
    steps:
      # First and unconditional. If GitHub ever remaps this label to Apple
      # Silicon, the entire purpose of this job evaporates while the check
      # stays green — the one failure this design cannot tolerate silently.
      # macos-15-intel is the last x86_64 image and disappears in August 2027.
      - name: Confirm this is genuinely Intel hardware
        run: |
          arch="$(uname -m)"
          echo "runner architecture: $arch"
          test "$arch" = x86_64 || {
            echo "macos-15-intel did not provide an x86_64 host; this job cannot verify the x86_64 slice" >&2
            exit 1
          }

      # Same ref as build, for helper/'s test sources and for plugin/, which
      # verify.sh's staleness comparison reads.
      - uses: actions/checkout@v7

      - uses: actions/setup-go@v7
        with:
          go-version-file: helper/go.mod

      # Homebrew's Intel prefix is /usr/local, already in
      # systemFFmpegCandidates, so fixture discovery needs no change.
      - name: Install ffmpeg for test fixtures
        run: brew install ffmpeg

      - uses: actions/download-artifact@v8
        with:
          name: iina-airplay-package
          path: dist

      - name: Confirm the artifact survived the round trip
        run: cd dist && shasum -a 256 -c iina-airplay.iinaplgz.sha256

      # The same two commands build ran, in the same order, on the same bytes.
      # Any difference in outcome is a difference in architecture, which is
      # precisely the question this job exists to answer.
      - name: Verify the package on Intel
        run: ./packaging/verify.sh dist/iina-airplay.iinaplgz

      - name: Drive the packed binaries through the helper suite on Intel
        run: ./packaging/test-package.sh dist/iina-airplay.iinaplgz

  release:
    needs: [build, verify-intel]
    if: startsWith(github.ref, 'refs/tags/v')
    runs-on: ubuntu-latest
    permissions:
      contents: write
    steps:
      # Needed for packaging/release-notes.sh; the artifact carries no scripts.
      - uses: actions/checkout@v7

      - uses: actions/download-artifact@v8
        with:
          name: iina-airplay-package
          path: dist

      # IINA takes the FIRST asset whose name ends in .iinaplgz, so more than
      # one is ambiguous. The .sha256 sidecar is safe: it does not end in
      # .iinaplgz.
      - name: Confirm exactly one .iinaplgz will be attached
        run: |
          count="$(find dist -maxdepth 1 -name '*.iinaplgz' | wc -l | tr -d ' ')"
          test "$count" = 1 || { echo "expected exactly one .iinaplgz, found $count" >&2; exit 1; }

      # A script rather than an inline heredoc: a heredoc inside a YAML block
      # scalar carries the block's indentation into every line it writes, which
      # would silently render the entire markdown body as one code block.
      - name: Compose the release notes
        run: ./packaging/release-notes.sh dist/iina-airplay.iinaplgz > notes.md && cat notes.md

      # Draft, not published: /releases/latest excludes drafts, so IINA cannot
      # see this until a human publishes it. Publishing is also what enables the
      # repo-slug acceptance test, so it stays a deliberate act.
      - name: Create the draft release
        env:
          GH_TOKEN: ${{ github.token }}
        run: |
          gh release create "${{ github.ref_name }}" \
            --draft \
            --title "${{ github.ref_name }}" \
            --notes-file notes.md \
            --repo "${{ github.repository }}" \
            dist/iina-airplay.iinaplgz \
            dist/iina-airplay.iinaplgz.sha256
```

- [ ] **Step 3: Validate the YAML parses**

Run: `ruby -ryaml -e 'YAML.load_file(".github/workflows/package.yml"); puts "ok"'`
Expected: `ok`

- [ ] **Step 4: Commit and push the branch**

```bash
git add .github/workflows/package.yml packaging/release-notes.sh
git commit -m "ci: build, verify and draft-release the .iinaplgz

Three jobs: build on arm64, verify-intel on macos-15-intel, release as a
draft on tags. Both packaging jobs run the same verify.sh + test-package.sh
pair against the same artifact, so they differ only in the architecture
executing it — which is the whole point, since the x86_64 slice has never run
on Intel hardware and the arm64 runners have no Rosetta to stand in for it.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
git push -u origin HEAD
```

---

### Task 8: Dry run, then documentation

The workflows are untestable until they have run. This task is where they first do.

**Files:**
- Modify: `docs/distribution.md` (status block near the top; the "CI / release" section)
- Consumes (no edit): `packaging/release-notes.sh` from Task 7
- Modify: `README.md` (the Status paragraph)
- Create: `docs/releasing.md`

**Interfaces:**
- Consumes: everything from Tasks 1–7.
- Produces: nothing other tasks depend on.

- [ ] **Step 1: Open a PR so the packaging pipeline runs against it**

The branch touches `packaging/**`, `helper/**` and the workflow, so the path filter fires.

```bash
gh pr create --fill --base master
gh pr checks --watch
```

Expected: `ci` passes; `package` runs `build` then `verify-intel`, and skips `release`. This is a cold cache, so `build` takes 30+ minutes.

- [ ] **Step 2: Read the Intel job's log and confirm what it actually proved**

```bash
gh run list --workflow=package.yml --limit 1
gh run view <run-id> --log --job "verify-intel" | grep -E 'runner architecture|native slice|test-package: driving|ok$|PASS|FAIL'
```

Expected, and each line matters:
- `runner architecture: x86_64` — the label really is Intel.
- `verify: note — the native slice is x86_64, so its assertions run natively` — the licensing and encoder assertions ran on Intel, not under Rosetta.
- `test-package: driving iina-airplay.iinaplgz through the helper suite on x86_64` — a real remux happened with both shipped binaries.

If any of these is absent, stop and fix it. A green check without these three lines does not close the gap this round exists to close.

- [ ] **Step 3: Confirm the cache key works**

Re-run the workflow (`gh run rerun <run-id>`) and confirm `build` completes in single-digit minutes, with the log line `build-ffmpeg: up to date (universal); delete … to force`. A second full build means the cache key or the cached path list is wrong, and every future release pays 30 minutes.

- [ ] **Step 4: Exercise the release job's only real logic before a tag exists**

The `release` job is gated on `refs/tags/v*`, so nothing in the dry run executes
it. `packaging/release-notes.sh` is the only part of it that can fail on
something other than a typo — and a broken generator would first surface while
cutting the real release. Run it against the artifact the dry run produced:

```bash
gh run download <run-id> --name iina-airplay-package --dir /tmp/dryrun
./packaging/release-notes.sh /tmp/dryrun/iina-airplay.iinaplgz
```

Expected: markdown with **no leading indentation**, a filled-in FFmpeg version, a
real `https://ffmpeg.org/releases/…` source URL, and two 64-character SHA-256
values. An empty field means `bin/VERSIONS` or `bin/ffmpeg-LICENSE.md` changed
shape and the LGPL block would have shipped blank.

- [ ] **Step 5: Update `docs/distribution.md`**

Replace the `> **Status 2026-08-29:**` block with:

```markdown
> **Status 2026-08-30:** `make pack` builds the package locally and
> `packaging/verify.sh` gates it. **A packed `.iinaplgz` has been installed
> through IINA and cast to a real Apple TV** (2026-08-29), confirming the
> Gatekeeper finding below in practice. CI now builds, verifies and remux-tests
> the package on both architectures — including **executing the x86_64 slice
> natively on an Intel runner**, which local building could not do — and
> attaches it to a draft GitHub release. See
> `docs/superpowers/specs/2026-08-30-ci-release-design.md` and
> `docs/releasing.md`.
```

Replace the whole `## CI / release (GitHub Actions, macOS runner)` section with:

```markdown
## CI / release (GitHub Actions)

`.github/workflows/ci.yml` runs `make test` on `macos-15` for every branch push
and pull request. `.github/workflows/package.yml` runs the packaging chain on
`v*` tags, on manual dispatch, and on PRs touching `packaging/`, `helper/`,
`plugin/` or the `Makefile`:

1. **`build`** (`macos-15`, arm64) gates the tag, builds the pinned ffmpeg —
   cached on a key that is the SHA-256 of `build-ffmpeg.sh`, the same number the
   script stamps for itself — builds the universal helper, packs, then runs
   `verify.sh` and `test-package.sh`.
2. **`verify-intel`** (`macos-15-intel`) downloads that exact artifact and runs
   the same two commands on real Intel hardware. It builds nothing. It asserts
   `uname -m` is `x86_64` first: the arm64 runners have no Rosetta, so
   `verify.sh`'s x86_64 assertions *skip* there, and this job is the only
   coverage of that slice rather than redundant coverage.
3. **`release`** attaches the package and its SHA-256 to a **draft** release.
   `api.github.com/.../releases/latest` excludes drafts, so IINA cannot see it
   until a human publishes it.

**`macos-15-intel` is GitHub's last x86_64 macOS image and disappears in August
2027.** After that the Intel gate has to move to self-hosted hardware or be
dropped along with the x86_64 slice.

No signing beyond ad-hoc, and no secrets of any kind.
```

- [ ] **Step 6: Update `README.md`**

In the **Status** paragraph, replace:

```
CI and the tagged GitHub release are not wired up yet — see `docs/distribution.md`.
```

with:

```
CI builds, verifies and remux-tests the package on both Apple Silicon and Intel
runners and attaches it to a draft release; see `docs/releasing.md`.
```

- [ ] **Step 7: Write `docs/releasing.md`**

```markdown
# Releasing

Users install by typing `ozykhan/iina-airplay` into IINA, which pulls the
`.iinaplgz` from the repository's **latest GitHub release**. A release without
that asset is a broken install path, so every release carries it — CI refuses to
publish one that does not.

## Cutting a release

1. **Bump both numbers in `plugin/Info.json`.**

   - `version` — the semver string, e.g. `0.2.0`. The tag must be `v` + this.
   - `ghVersion` — an **Int**, a monotonic counter. **Not** the semver string.

   `ghVersion` is the step most likely to be forgotten and the only one whose
   omission is silent: the release ships fine, installs fine for new users, and
   simply never reaches anyone who already has the plugin, because IINA's update
   check compares this number. `packaging/check-release.sh` refuses a tag that
   does not increase it — but only once a previous tag exists.

2. **Commit, then tag and push.**

   ```sh
   git tag v0.2.0
   git push origin master v0.2.0
   ```

3. **Watch the chain.** `gh run watch`. `build` gates the tag first, so a
   mismatch fails in seconds. A cold ffmpeg cache costs 30 minutes.

4. **Read the `verify-intel` log**, not just its checkmark. Three lines are the
   point of the job:

   - `runner architecture: x86_64`
   - `verify: note — the native slice is x86_64, so its assertions run natively`
   - `test-package: driving iina-airplay.iinaplgz through the helper suite on x86_64`

5. **Review the draft release.** Confirm exactly one `.iinaplgz` asset, its
   `.sha256` sidecar, and notes naming the FFmpeg version, upstream source URL
   and source SHA-256 — that last part is the LGPL obligation, not decoration.

6. **Publish it.** Drafts are invisible to `api.github.com/.../releases/latest`,
   so nothing reaches users until this click.

7. **Install it the way a stranger would.** IINA → Settings → Plugins →
   Install → `ozykhan/iina-airplay`. Not the local-package path — the point is
   to exercise the download-from-release path, which is the one thing local
   packaging can never test. Cast one real file, then confirm nothing picked up
   quarantine:

   ```sh
   xattr -r ~/Library/Application\ Support/com.colliderli.iina/plugins/*/bin/
   ```

   Expect `com.apple.provenance` at most, and never `com.apple.quarantine`.

## Rebuilding without releasing

`workflow_dispatch` on `package.yml` runs `build` and `verify-intel` and creates
no release, leaving the `.iinaplgz` as a workflow artifact. Use it to rehearse,
and to warm the ffmpeg cache before a tag push.

The `release` job never runs on a dispatch, so preview its notes by hand against
the artifact:

```sh
gh run download <run-id> --name iina-airplay-package --dir /tmp/dryrun
./packaging/release-notes.sh /tmp/dryrun/iina-airplay.iinaplgz
```
```

- [ ] **Step 8: Commit and merge**

```bash
git add docs/distribution.md README.md docs/releasing.md
git commit -m "docs: record the CI pipeline and write the release runbook

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
git push
gh pr merge --squash --delete-branch
```

---

### Task 9: Cut `v0.1.0` and close the second unverified assumption

**Requires the user.** Publishing exposes strangers to the build, and the acceptance test needs a real Apple TV. Do not perform steps 2–5 without the user's explicit go-ahead.

**Files:** none — this task produces a release and a verified install.

**Interfaces:**
- Consumes: everything from Tasks 1–8.
- Produces: the `v0.1.0` release, and the answer to whether an IINA-from-release install stays unquarantined.

- [ ] **Step 1: Confirm `Info.json` is release-ready**

Run: `git checkout master && git pull && ./packaging/check-release.sh v0.1.0`
Expected: `check-release: OK — v0.1.0 matches version 0.1.0, ghVersion 1` and the first-release note. `version` is already `0.1.0` and `ghVersion` is already `1`, so no bump is needed for this one.

- [ ] **Step 2: Tag and push** *(ask the user first)*

```bash
git tag v0.1.0
git push origin v0.1.0
gh run watch
```

- [ ] **Step 3: Read the Intel log and review the draft**

Follow `docs/releasing.md` steps 4 and 5. Report to the user what the three Intel log lines actually said, quoting them — not a summary.

- [ ] **Step 4: The user publishes the draft**

Hand them the draft release URL. Publishing is theirs.

- [ ] **Step 5: Acceptance test** *(the user, on their own Mac, with their Apple TV)*

IINA → Settings → Plugins → Install → `ozykhan/iina-airplay`. The **repo slug**, not the local-package path. Then cast one real file, and check for quarantine:

```bash
xattr -r ~/Library/Application\ Support/com.colliderli.iina/plugins/*/bin/
```

Expected: `com.apple.provenance` at most; never `com.apple.quarantine`. This is the assertion the whole no-notarization design rests on, tested for the first time on the path real users take.

- [ ] **Step 6: Record the result**

Whatever the answer is, write it down — this is the round's finding.

```bash
# On success:
git commit --allow-empty -m "docs: v0.1.0 installed from the release through IINA, unquarantined

The download-from-release path is now verified with the real binaries, not a
synthetic package: IINA's installer applies no com.apple.quarantine, and the
ad-hoc signatures alone satisfy Gatekeeper. Both assumptions this round
existed to test are now closed.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

If quarantine *is* present, do not paper over it: `docs/distribution.md`'s central finding is wrong for the release path, and that is a design-level result deserving its own brainstorming round.

---

## Notes for the implementer

- **`gh release create` and `--generate-notes`.** The plan composes the notes by
  hand and does not use `--generate-notes`. If you want a commit changelog too,
  check whether `gh` composes the two before relying on it; the hand-composed
  body is sufficient on its own and is what the LGPL obligation needs.
- **Testing YAML.** There is no linter in this repo and none is being added. The
  `ruby -ryaml` parse check catches syntax errors; everything else is caught by
  the dry run in Task 8, which is why that task is not optional.
- **If `build` fails inside `build-ffmpeg.sh`**, read its message before
  anything else — it prechecks `nasm` and the host architecture specifically
  because both failures otherwise surface forty minutes later as something else
  entirely.
- **Never run two builds in the same tree.** Separate runners have separate
  trees, so CI is safe by construction, but if you reproduce a failure locally,
  do not start a second `make` while one is running: concurrent `ar` writes
  corrupt the static archives and surface as a bogus "undefined symbol" link
  error.
