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
