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
# the result to a stable name. It also only succeeds with a path RELATIVE to
# that cwd: an absolute path fails with "Cannot read plugin package content."
# (verified by experiment), so pass "stage/iina-airplay", not "$STAGE".
version="$(sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$STAGE/Info.json" | head -1)"
[ -n "$version" ] || { echo "pack: cannot read version from Info.json" >&2; exit 1; }
( cd "$ROOT/build" && rm -f "iina-airplay-$version.iinaplgz" \
  && "$IINA_PLUGIN" pack "stage/iina-airplay" >/dev/null )
mv "$ROOT/build/iina-airplay-$version.iinaplgz" "$ROOT/build/iina-airplay.iinaplgz"

echo "pack: wrote $ROOT/build/iina-airplay.iinaplgz"
