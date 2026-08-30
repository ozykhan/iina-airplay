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
