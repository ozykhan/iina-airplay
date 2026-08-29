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
bad_ghrepo()     { /usr/bin/sed -i '' 's|"ozykhan/iina-airplay"|"not a slug!"|' "$1/Info.json"; }

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

expect_fail "missing ffmpeg"           "$(make_pkg missing  drop_ffmpeg)"     "missing from the package"
expect_fail "non-executable helper"    "$(make_pkg noexec   unexecutable)"    "executab"
expect_fail "malformed ghRepo"         "$(make_pkg ghrepo   bad_ghrepo)"      "ghRepo"
expect_fail "corrupted after signing"  "$(make_pkg corrupt  corrupt_signed)"  "signature"

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

if [ "$fails" -ne 0 ]; then
  echo "$fails verify.sh test(s) failed"
  exit 1
fi
echo "all verify.sh tests passed"
