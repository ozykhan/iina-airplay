#!/usr/bin/env bash
# pack.sh names the plugin source files it stages by absolute path. Those paths
# are the one part of packaging that a file MOVE breaks silently from the point
# of view of `make test`: every other suite builds its own fixtures, so the
# whole suite stays green while pack.sh points at a file that no longer exists,
# and the failure only surfaces from `make pack` — after the ~25-minute ffmpeg
# build, and only on a machine that runs packaging at all.
#
# This ran green through the move of Info.json from plugin/ to the repository
# root (IINA's update check reads the repo root), which is precisely the class
# of silent breakage it now closes. Assert the declarations resolve, cheaply,
# ahead of the long build — the same reasoning package.yml uses to run
# `make test` before the ffmpeg leg.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
PACK="$ROOT/packaging/pack.sh"
fails=0

[ -f "$PACK" ] || { echo "FAIL: $PACK not found"; exit 1; }

# Read the real declarations out of the real script rather than restating them
# here — a copy would drift and assert nothing. These lines are plain
# VAR="$ROOT/..." assignments, so evaluating just them is safe.
decls="$(grep -E '^PLUGIN_(INFO|MAIN|SIDEBAR)=' "$PACK")"
if [ -z "$decls" ]; then
  echo "FAIL: no PLUGIN_INFO/PLUGIN_MAIN/PLUGIN_SIDEBAR declarations found in pack.sh"
  echo "      (renamed? this test is asserting nothing until it is updated)"
  exit 1
fi
eval "$decls"

for var in PLUGIN_INFO PLUGIN_MAIN PLUGIN_SIDEBAR; do
  path="$(eval "printf '%s' \"\$$var\"")"
  if [ -z "$path" ]; then
    echo "FAIL: $var is declared in pack.sh but empty"
    fails=$((fails + 1))
  elif [ -f "$path" ]; then
    echo "ok: $var resolves to an existing file (${path#$ROOT/})"
  else
    echo "FAIL: $var points at ${path#$ROOT/}, which does not exist — pack.sh would"
    echo "      fail only after the ffmpeg build; a source file moved without"
    echo "      updating pack.sh"
    fails=$((fails + 1))
  fi
done

# The manifest specifically must come from the repository root: IINA's update
# check fetches raw.githubusercontent.com/<ghRepo>/master/Info.json, so a
# manifest staged from anywhere else would package a version IINA can never see.
if [ "$PLUGIN_INFO" = "$ROOT/Info.json" ]; then
  echo "ok: the manifest is staged from the repository root, where IINA's update check reads it"
else
  echo "FAIL: pack.sh stages the manifest from ${PLUGIN_INFO#$ROOT/}, not the repository root."
  echo "      IINA reads raw.githubusercontent.com/<ghRepo>/master/Info.json; a manifest"
  echo "      kept anywhere else leaves the update check reading a 404."
  fails=$((fails + 1))
fi

if [ "$fails" -ne 0 ]; then
  echo "$fails pack.sh path test(s) failed"
  exit 1
fi
echo "all pack.sh path tests passed"
