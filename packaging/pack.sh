#!/usr/bin/env bash
# Assembles the staging tree and produces build/iina-airplay.iinaplgz.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
STAGE="$ROOT/build/stage/iina-airplay"
FFMPEG="$ROOT/build/ffmpeg/ffmpeg"
FFVERSION="$ROOT/build/ffmpeg/VERSION"
HELPER="$ROOT/build/helper/airplay-helper"
PLUGIN_INFO="$ROOT/plugin/Info.json"
PLUGIN_MAIN="$ROOT/plugin/main.js"
PLUGIN_SIDEBAR="$ROOT/plugin/sidebar.html"
IINA_PLUGIN="${IINA_PLUGIN:-/Applications/IINA.app/Contents/MacOS/iina-plugin}"

for f in "$FFMPEG" "$FFVERSION" "$HELPER"; do
  [ -f "$f" ] || { echo "pack: missing $f — run build-ffmpeg.sh and build-helper.sh first" >&2; exit 1; }
done

for f in "$PLUGIN_INFO" "$PLUGIN_MAIN" "$PLUGIN_SIDEBAR"; do
  [ -f "$f" ] || { echo "pack: missing $f — the plugin source tree is incomplete" >&2; exit 1; }
done

[ -x "$IINA_PLUGIN" ] || { echo "pack: IINA_PLUGIN ($IINA_PLUGIN) not found or not executable — install IINA or set IINA_PLUGIN to the iina-plugin CLI path" >&2; exit 1; }

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

cat > "$STAGE/bin/ffmpeg-LICENSE.md" <<EOF
# FFmpeg

This package bundles an unmodified build of FFmpeg $ffmpeg_version, licensed
under the GNU Lesser General Public License version 2.1 or later. It was
configured with LGPL-licensed components only: no GPL components are enabled
and \`--enable-gpl\` was never passed.

- Upstream source: $source_url
- Source SHA-256: $source_sha256
- Build recipe: \`packaging/build-ffmpeg.sh\` in the iina-airplay repository$commit_note,
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
