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

# verify.sh compares the package's plugin files against $VERIFY_SRC_ROOT/... to
# catch a stale pack (see FIX 1/2). Every synthetic fixture below is a fake
# plugin, not this repo's real one, so it would spuriously fail that check
# against this repo's actual plugin/ tree. Point it at a directory that does
# not exist instead, which makes verify.sh print a skip note rather than a
# false "stale" failure — the staleness check itself gets its own dedicated
# test further down, with a fixture built to match on purpose.
DUMMY_SRC_ROOT="$TMP/no-such-plugin-source"

# Builds a minimal package; callers mutate the staging dir via the hook first.
# Includes every file verify.sh now asserts is present (sidebar.html,
# bin/VERSIONS, bin/ffmpeg-LICENSE.md, bin/COPYING.LGPLv2.1) so a hook that
# targets one specific check doesn't also trip an unrelated presence check.
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
  echo "<html></html>" > "$d/src/sidebar.html"
  printf '#!/bin/sh\nexit 0\n' > "$d/src/bin/airplay-helper"
  printf '#!/bin/sh\nexit 0\n' > "$d/src/bin/ffmpeg"
  chmod 755 "$d/src/bin/airplay-helper" "$d/src/bin/ffmpeg"
  printf 'helper_version=test\nhelper_sha256=0\nffmpeg_version=test\nffmpeg_sha256=0\nffmpeg_source_sha256=0\n' \
    > "$d/src/bin/VERSIONS"
  printf '# FFmpeg\n\nTest fixture license notice.\n' > "$d/src/bin/ffmpeg-LICENSE.md"
  printf 'GNU LESSER GENERAL PUBLIC LICENSE\nVersion 2.1, test fixture text.\n' \
    > "$d/src/bin/COPYING.LGPLv2.1"
  "$hook" "$d/src"
  ( cd "$d/src" && zip -q -r "$d/pkg.iinaplgz" . )
  echo "$d/pkg.iinaplgz"
}

expect_fail() {
  local label="$1" pkg="$2" pattern="$3" out
  out="$(VERIFY_SRC_ROOT="$DUMMY_SRC_ROOT" "$VERIFY" "$pkg" 2>&1)"
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
bad_ghrepo()     { /usr/bin/sed -i '' 's|"ozykhan/iina-airplay"|"not a slug!"|' "$1/Info.json"; }
drop_main()      { rm -f "$1/main.js"; }
drop_sidebar()   { rm -f "$1/sidebar.html"; }
drop_copying()   { rm -f "$1/bin/COPYING.LGPLv2.1"; }

# A real Mach-O, ad-hoc signed, then corrupted after signing (bytes appended).
# `codesign -dv` alone would not catch this — it just prints the signature
# blob without re-hashing — so this fixture exists to pin `codesign --verify`
# doing the actual integrity check. Needs a real universal binary (not a
# shell-script stub) to get past the earlier presence/mode/arch checks and
# actually reach the signature-verification check.
corrupt_signed() {
  local d="$1"
  local src="$TMP/corrupt_signed_src.c"
  printf 'int main(void) { return 0; }\n' > "$src"
  clang -arch x86_64 -arch arm64 -o "$d/bin/airplay-helper" "$src" 2>/dev/null
  codesign -s - "$d/bin/airplay-helper" >/dev/null 2>&1
  printf 'GARBAGE' >> "$d/bin/airplay-helper"
  chmod 755 "$d/bin/airplay-helper"
}

# A real universal, ad-hoc-signed binary standing in for bin/ffmpeg that is
# NOT the bundled ffmpeg (just a `return 0` stub) — proof the ffmpeg-behaviour
# section (licensing grep, encoder allowlist, libx264 rejection, decoder list)
# actually gets reached and actually inspects output, not just that ffmpeg
# "runs". airplay-helper must be a real signed universal binary too, or the
# arch/signature loop would fail on it FIRST (it's checked before ffmpeg) and
# the fixture would never reach the check under test.
fake_ffmpeg_binary() {
  local d="$1"
  local src="$TMP/fake_ffmpeg_src.c"
  printf 'int main(void) { return 0; }\n' > "$src"
  for bin in "$d/bin/airplay-helper" "$d/bin/ffmpeg"; do
    clang -arch x86_64 -arch arm64 -o "$bin" "$src" 2>/dev/null
    codesign -s - "$bin" >/dev/null 2>&1
    chmod 755 "$bin"
  done
}

expect_fail "missing ffmpeg"           "$(make_pkg missing    drop_ffmpeg)"       "missing from the package"
expect_fail "non-executable helper"    "$(make_pkg noexec     unexecutable)"      "executab"
expect_fail "malformed ghRepo"         "$(make_pkg ghrepo     bad_ghrepo)"        "ghRepo"
expect_fail "corrupted after signing"  "$(make_pkg corrupt    corrupt_signed)"    "signature"
expect_fail "missing entry file"       "$(make_pkg noentry    drop_main)"         "entry file"
expect_fail "missing sidebar.html"     "$(make_pkg nosidebar  drop_sidebar)"      "sidebar.html"
expect_fail "missing LGPL license copy" "$(make_pkg nocopying drop_copying)"      "COPYING"
expect_fail "ffmpeg stand-in trips the encoder assertions" \
  "$(make_pkg fakeffmpeg fake_ffmpeg_binary)" "encoder"

# Quarantine cannot be exercised by planting the xattr on a file inside the
# staging dir before zipping: this system's zip/unzip does not store or
# restore extended attributes at all (verified by experiment — a
# com.apple.quarantine set pre-zip, or even a plain custom xattr, is gone
# after unzip), so verify.sh's own extraction could never see it either way.
# The realistic vector — and the one verify.sh actually checks — is the
# package FILE carrying quarantine (as it would after a browser/curl
# download), so set it there, on the already-built .iinaplgz.
quar_pkg="$(make_pkg quar noop)"
xattr -w com.apple.quarantine "0081;0;test;" "$quar_pkg"
expect_fail "quarantined package" "$quar_pkg" "quarantine"

# --- staleness: a package whose plugin files don't match the source it claims
# to be built from must be rejected, not silently blessed (FIX 1's failure
# mode). Point VERIFY_SRC_ROOT at a fixture "source tree" that matches the
# package's sidebar.html/Info.json byte-for-byte but NOT its main.js.
stale_root="$TMP/stale-plugin-src"
mkdir -p "$stale_root"
echo "<html></html>" > "$stale_root/sidebar.html"
cat > "$stale_root/Info.json" <<'JSON'
{"name":"AirPlay","identifier":"dev.faruk.iina-airplay","version":"0.1.0",
 "ghRepo":"ozykhan/iina-airplay","ghVersion":1,"entry":"main.js","permissions":[]}
JSON
echo "// authoritative source — deliberately different from the packaged main.js" > "$stale_root/main.js"
stale_pkg="$(make_pkg stale noop)"
stale_out="$(VERIFY_SRC_ROOT="$stale_root" "$VERIFY" "$stale_pkg" 2>&1)"
if [ $? -eq 0 ]; then
  echo "FAIL: stale main.js — verify.sh accepted a package built from different source"
  fails=$((fails + 1))
elif ! grep -qi "stale" <<<"$stale_out"; then
  echo "FAIL: stale main.js — rejected, but the message did not mention 'stale':"
  echo "$stale_out" | sed 's/^/    /'
  fails=$((fails + 1))
else
  echo "ok: stale main.js is caught by the staleness check"
fi

# --- positive case: a real, freshly-packed .iinaplgz must verify OK ----------
# All the fixtures above prove verify.sh REJECTS broken packages; none of them
# prove it ACCEPTS a good one — a regression that made verify.sh always fail
# would still print "all tests passed" without this. Skipped (not silently
# passed) when the package hasn't been built in this checkout.
REAL_PKG="$ROOT/build/iina-airplay.iinaplgz"
if [ -f "$REAL_PKG" ]; then
  real_out="$("$VERIFY" "$REAL_PKG" 2>&1)"
  real_status=$?
  if [ "$real_status" -ne 0 ]; then
    echo "FAIL: real package — verify.sh rejected build/iina-airplay.iinaplgz:"
    echo "$real_out" | sed 's/^/    /'
    fails=$((fails + 1))
  elif ! grep -q '^verify: OK' <<<"$real_out"; then
    echo "FAIL: real package — verify.sh exited 0 but did not print OK:"
    echo "$real_out" | sed 's/^/    /'
    fails=$((fails + 1))
  else
    echo "ok: real package (build/iina-airplay.iinaplgz) verifies OK"
  fi
else
  echo "note: build/iina-airplay.iinaplgz not present; skipping the positive real-package test (run packaging/pack.sh first)"
fi

if [ "$fails" -ne 0 ]; then
  echo "$fails verify.sh test(s) failed"
  exit 1
fi
echo "all verify.sh tests passed"
