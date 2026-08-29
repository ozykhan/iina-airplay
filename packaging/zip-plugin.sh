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
