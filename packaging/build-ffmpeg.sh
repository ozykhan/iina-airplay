#!/usr/bin/env bash
# Builds the ffmpeg binary bundled in the .iinaplgz.
#
# Broad in, narrow out: decoders, demuxers, parsers and filters stay at
# upstream defaults so any file IINA opens, this ffmpeg opens — and so the
# hevc_videotoolbox re-encode branch in helper/job.go has decoders to work
# with. Trimming happens only on the output side.
set -euo pipefail

FFMPEG_VERSION="9.0.1"
FFMPEG_SHA256="cf38e0e28c7e5605942c4a77755349b0145804a397af37eb1fb4c77cb237f635"

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT="$ROOT/build/ffmpeg"
SRC="$OUT/src"
TARBALL_URL="https://ffmpeg.org/releases/ffmpeg-${FFMPEG_VERSION}.tar.xz"

ARCH_MODE="universal"
[ "${1:-}" = "--arch" ] && ARCH_MODE="${2:-universal}"

# Any change to this script — a flag, the pinned version — invalidates the
# cached build. Hashing the script itself is the whole cache-key story.
RECIPE_HASH="$(shasum -a 256 "${BASH_SOURCE[0]}" | cut -d' ' -f1)"
STAMP="$OUT/.recipe-hash"
if [ -f "$OUT/ffmpeg" ] && [ -f "$STAMP" ] && [ "$(cat "$STAMP")" = "$RECIPE_HASH $ARCH_MODE" ]; then
  echo "build-ffmpeg: up to date ($ARCH_MODE); delete $OUT to force"
  exit 0
fi

mkdir -p "$OUT"
TARBALL="$OUT/ffmpeg-${FFMPEG_VERSION}.tar.xz"
if [ ! -f "$TARBALL" ]; then
  echo "build-ffmpeg: fetching $TARBALL_URL"
  curl -fsSL -o "$TARBALL" "$TARBALL_URL"
fi

ACTUAL="$(shasum -a 256 "$TARBALL" | cut -d' ' -f1)"
if [ "$ACTUAL" != "$FFMPEG_SHA256" ]; then
  echo "build-ffmpeg: SHA-256 mismatch for ffmpeg-${FFMPEG_VERSION}.tar.xz" >&2
  echo "  expected $FFMPEG_SHA256" >&2
  echo "  actual   $ACTUAL" >&2
  echo "Refusing to build an unverified source tree." >&2
  exit 1
fi

# Re-extracting and reconfiguring from zero on every invocation makes any
# interruption (Ctrl-C, a killed backgrounded shell, a machine sleep) throw
# away a 10-25 minute build and restart from nothing. Skip work that a prior,
# still-valid run already did; `delete $OUT to force` (see the stamp check
# above) remains the escape hatch for a genuinely clean rebuild.
if [ -f "$SRC/RELEASE" ] && [ "$(cat "$SRC/RELEASE")" = "$FFMPEG_VERSION" ]; then
  echo "build-ffmpeg: source tree already extracted for $FFMPEG_VERSION"
else
  rm -rf "$SRC"
  mkdir -p "$SRC"
  tar -xJf "$TARBALL" -C "$SRC" --strip-components=1
fi

# --disable-autodetect is load-bearing: without it configure links whatever
# Homebrew libraries happen to be installed, and the package works only on the
# machine that built it. Everything the pipeline needs is re-enabled by hand.
common_flags() {
  cat <<'FLAGS'
--disable-autodetect
--enable-videotoolbox
--enable-zlib
--disable-doc
--disable-debug
--disable-network
--disable-programs
--enable-ffmpeg
--disable-encoders
--enable-encoder=aac
--enable-encoder=eac3
--enable-encoder=hevc_videotoolbox
--enable-encoder=h264_videotoolbox
--enable-encoder=webvtt
--disable-muxers
--enable-muxer=hls
--enable-muxer=mp4
--enable-muxer=mov
--enable-muxer=webvtt
--enable-muxer=mpegts
FLAGS
}

build_one() {
  # Split from a single `local a=.. d=$OUT/$a` because macOS ships bash 3.2:
  # under `set -u`, all words on a `local` line are expanded before any
  # assignment lands, so referencing $arch on the same line as its own
  # assignment throws "arch: unbound variable".
  local arch="$1" minver="$2" extra="$3"
  local dest="$OUT/$arch"
  mkdir -p "$dest"

  # config.mak's prefix= line tells us which dest (i.e. which arch) the tree
  # is currently configured for. Check this BEFORE distclean, which deletes
  # config.mak — the whole point is to skip distclean when a resume is safe.
  local config_mak="$SRC/ffbuild/config.mak"
  local configured_prefix=""
  if [ -f "$config_mak" ]; then
    configured_prefix="$(grep -m1 '^prefix=' "$config_mak" | cut -d= -f2-)"
  fi

  if [ "$configured_prefix" = "$dest" ]; then
    echo "build-ffmpeg: $arch already configured; resuming make"
  else
    ( cd "$SRC" && make distclean >/dev/null 2>&1 || true )
    # shellcheck disable=SC2046
    ( cd "$SRC" && ./configure \
        --prefix="$dest" \
        $(common_flags) \
        --extra-cflags="-mmacosx-version-min=$minver" \
        --extra-ldflags="-mmacosx-version-min=$minver" \
        $extra )
  fi

  ( cd "$SRC" && make -j"$(sysctl -n hw.ncpu)" && cp ffmpeg "$dest/ffmpeg" )
}

case "$ARCH_MODE" in
  native)
    build_one arm64 11.0 ""
    cp "$OUT/arm64/ffmpeg" "$OUT/ffmpeg"
    ;;
  universal)
    build_one arm64 11.0 ""
    build_one x86_64 10.15 "--enable-cross-compile --arch=x86_64 --cpu=x86_64 --target-os=darwin --cc=clang --extra-cflags=-arch\ x86_64 --extra-ldflags=-arch\ x86_64"
    lipo -create "$OUT/arm64/ffmpeg" "$OUT/x86_64/ffmpeg" -output "$OUT/ffmpeg"
    ;;
  *)
    echo "build-ffmpeg: unknown --arch '$ARCH_MODE' (want native or universal)" >&2
    exit 2
    ;;
esac

# lipo does not carry slice signatures across; sign the artifact that ships.
codesign --force --sign - "$OUT/ffmpeg"

cat > "$OUT/VERSION" <<EOF
ffmpeg_version=$FFMPEG_VERSION
source_sha256=$FFMPEG_SHA256
source_url=$TARBALL_URL
EOF

echo "$RECIPE_HASH $ARCH_MODE" > "$STAMP"
echo "build-ffmpeg: wrote $OUT/ffmpeg ($ARCH_MODE)"
