#!/usr/bin/env bash
# Assembles the staging tree and produces build/iina-airplay.iinaplgz.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
STAGE="$ROOT/build/stage/iina-airplay"
FFMPEG="$ROOT/build/ffmpeg/ffmpeg"
FFVERSION="$ROOT/build/ffmpeg/VERSION"
FFLICENSE="$ROOT/build/ffmpeg/src/COPYING.LGPLv2.1"
HELPER="$ROOT/build/helper/airplay-helper"
PLUGIN_INFO="$ROOT/plugin/Info.json"
PLUGIN_MAIN="$ROOT/plugin/main.js"
PLUGIN_SIDEBAR="$ROOT/plugin/sidebar.html"
CANONICAL_PKG="$ROOT/build/iina-airplay.iinaplgz"

# A failed run past this point must not leave the PREVIOUS package sitting at
# the canonical path: verify.sh and `make verify` both just check whatever is
# there, and would silently bless stale bits from an earlier, unrelated build.
# Remove it before any work that can fail, so a failed pack leaves no package
# rather than a misleading one.
rm -f "$CANONICAL_PKG"

for f in "$FFMPEG" "$FFVERSION" "$HELPER"; do
  [ -f "$f" ] || { echo "pack: missing $f — run build-ffmpeg.sh and build-helper.sh first" >&2; exit 1; }
done

# LGPL 2.1 §1 requires distribution to be accompanied by a copy of the licence,
# not merely a link to it. build-ffmpeg.sh's extracted source tree carries the
# upstream COPYING.LGPLv2.1 at this path — fail loudly rather than silently
# shipping a package that only links gnu.org.
[ -f "$FFLICENSE" ] || { echo "pack: missing $FFLICENSE — the ffmpeg source tree is missing its LGPL license text; re-run build-ffmpeg.sh" >&2; exit 1; }

for f in "$PLUGIN_INFO" "$PLUGIN_MAIN" "$PLUGIN_SIDEBAR"; do
  [ -f "$f" ] || { echo "pack: missing $f — the plugin source tree is incomplete" >&2; exit 1; }
done

# A binary that merely exists is not a binary a user's Mac will run: IINA's
# installer applies no com.apple.quarantine, so the ad-hoc signature is the
# only Gatekeeper gate, and a thin (single-arch) binary breaks half of all
# Macs outright. Verify both properties before anything gets staged.
verify_binary() {
  bin_label="$1"
  bin_path="$2"
  rebuild_hint="$3"

  # codesign -dv only parses and prints the embedded signature blob — it does
  # not re-hash the file, so a binary corrupted or truncated after signing
  # (partial copy, disk error, interrupted write) still prints a clean
  # "Signature=adhoc" line. codesign --verify does the actual cryptographic
  # check that the bytes on disk still match that signature. Neither check
  # implies the other, so run both, integrity first — a corrupted binary's
  # label isn't worth trusting.
  set +e
  verify_out="$(codesign --verify --strict "$bin_path" 2>&1)"
  verify_status=$?
  set -e
  if [ "$verify_status" -ne 0 ]; then
    echo "pack: $bin_label ($bin_path) failed signature verification — its bytes do not match its signature (corrupted or truncated after signing) — re-run $rebuild_hint" >&2
    exit 1
  fi

  set +e
  codesign_out="$(codesign -dv "$bin_path" 2>&1)"
  codesign_status=$?
  set -e
  if [ "$codesign_status" -ne 0 ] || ! printf '%s\n' "$codesign_out" | grep -q '^Signature=adhoc$'; then
    echo "pack: $bin_label ($bin_path) is not ad-hoc signed — re-run $rebuild_hint" >&2
    exit 1
  fi

  set +e
  archs="$(lipo -archs "$bin_path" 2>&1)"
  lipo_status=$?
  set -e
  if [ "$lipo_status" -ne 0 ]; then
    echo "pack: $bin_label ($bin_path) — lipo could not read its architectures — re-run $rebuild_hint" >&2
    exit 1
  fi
  case " $archs " in
    *' x86_64 '*) ;;
    *) echo "pack: $bin_label ($bin_path) is missing the x86_64 slice (has: $archs) — re-run $rebuild_hint" >&2; exit 1 ;;
  esac
  case " $archs " in
    *' arm64 '*) ;;
    *) echo "pack: $bin_label ($bin_path) is missing the arm64 slice (has: $archs) — re-run $rebuild_hint" >&2; exit 1 ;;
  esac
}

verify_binary "ffmpeg" "$FFMPEG" "build-ffmpeg.sh"
verify_binary "airplay-helper" "$HELPER" "build-helper.sh"

ffmpeg_version="$(grep '^ffmpeg_version=' "$FFVERSION" | cut -d= -f2-)"
source_sha256="$(grep '^source_sha256=' "$FFVERSION" | cut -d= -f2-)"
source_url="$(grep '^source_url=' "$FFVERSION" | cut -d= -f2-)"

rm -rf "$ROOT/build/stage" && mkdir -p "$STAGE/bin"
cp "$PLUGIN_INFO" "$PLUGIN_MAIN" "$PLUGIN_SIDEBAR" "$STAGE/"
cp "$FFMPEG" "$STAGE/bin/ffmpeg"
cp "$HELPER" "$STAGE/bin/airplay-helper"
cp "$FFLICENSE" "$STAGE/bin/COPYING.LGPLv2.1"
chmod 755 "$STAGE/bin/ffmpeg" "$STAGE/bin/airplay-helper"

helper_version="$(git -C "$ROOT" describe --tags --always --dirty 2>/dev/null || echo unknown)"

# Name the exact revision this binary's build recipe came from — the LGPL
# compliance pointer below is worthless once build-ffmpeg.sh changes if it
# doesn't say which version of the script it means. Degrade gracefully (no
# git, or no commits yet) rather than aborting the pack over a doc detail.
if commit_sha="$(git -C "$ROOT" rev-parse HEAD 2>/dev/null)" && [ -n "$commit_sha" ]; then
  commit_note=" at commit \`$commit_sha\`"
else
  commit_note=" (commit unknown — git unavailable or repository has no commits)"
fi

cat > "$STAGE/bin/VERSIONS" <<EOF
helper_version=$helper_version
helper_sha256=$(shasum -a 256 "$STAGE/bin/airplay-helper" | cut -d' ' -f1)
ffmpeg_version=$ffmpeg_version
ffmpeg_sha256=$(shasum -a 256 "$STAGE/bin/ffmpeg" | cut -d' ' -f1)
ffmpeg_source_sha256=$source_sha256
EOF

REPO_URL="https://github.com/ozykhan/iina-airplay"

cat > "$STAGE/bin/ffmpeg-LICENSE.md" <<EOF
# FFmpeg

This package bundles an unmodified build of FFmpeg $ffmpeg_version, licensed
under the GNU Lesser General Public License version 2.1 or later. It was
configured with LGPL-licensed components only: no GPL components are enabled
and \`--enable-gpl\` was never passed.

- Upstream source: $source_url
- Source SHA-256: $source_sha256
- Build recipe: \`packaging/build-ffmpeg.sh\` in $REPO_URL$commit_note,
  which contains the complete configure line used to produce this binary.

A copy of the license text is included alongside this file as
\`bin/COPYING.LGPLv2.1\` (also available at
https://www.gnu.org/licenses/lgpl-2.1.html).

FFmpeg is a trademark of Fabrice Bellard, originator of the FFmpeg project.
EOF

# Packs to the canonical path directly. The old iina-plugin CLI wrote
# <dirname>-<version>.iinaplgz into the current directory and had to be moved;
# zip-plugin.sh takes the destination as an argument, so the version-extraction
# and the mv both go away with it. See zip-plugin.sh for why we no longer
# shell out to IINA.app.
"$ROOT/packaging/zip-plugin.sh" "$STAGE" "$CANONICAL_PKG"

echo "pack: wrote $CANONICAL_PKG"
