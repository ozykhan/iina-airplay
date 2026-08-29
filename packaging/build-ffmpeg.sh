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
  # Download to a temp name and move into place only on success. A curl that
  # fails partway (network drop, or the whole script killed mid-transfer)
  # must not leave a partial file at $TARBALL — the next run's
  # `[ ! -f "$TARBALL" ]` check would then skip re-downloading and hand a
  # truncated tarball straight to the SHA-256 check as a confusing mismatch.
  TMP_TARBALL="$TARBALL.part"
  curl -fsSL -o "$TMP_TARBALL" "$TARBALL_URL"
  mv "$TMP_TARBALL" "$TARBALL"
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
  local arch="$1"
  local minver="$2"
  shift 2
  local dest="$OUT/$arch"
  mkdir -p "$dest"

  # Remaining positional args are this arch's extra configure flags, kept as
  # an array and forwarded via "$@" — never collected back into one string.
  # A flag whose value contains a space ("-arch x86_64") cannot survive
  # unquoted word splitting, and quoting the backslash inside double quotes
  # ("-arch\ x86_64") makes it a literal backslash, not an escape: bash only
  # treats \ as an escape inside "..." before $ ` " \ or a newline. Verified:
  #   extra="--extra-cflags=-arch\ x86_64"; for a in $extra; do ...
  #   -> arg1: [--extra-cflags=-arch\]  arg2: [x86_64]
  # ffmpeg's add_cflags()/add_ldflags() use `append`, so repeating
  # --extra-cflags/--extra-ldflags accumulates with the -mmacosx-version-min
  # flags below rather than replacing them — passing them as separate
  # configure arguments is correct, not redundant.

  # A resume is only safe when the already-configured tree matches BOTH this
  # exact arch and this exact recipe. Comparing config.mak's prefix= against
  # $dest (an earlier version of this guard) caught arm64-vs-x86_64 but not a
  # same-arch edit to common_flags()/minver/extra flags: RECIPE_HASH changes,
  # the top-level stamp correctly stops short-circuiting, but prefix= is
  # unchanged, so the old guard would skip distclean/configure and rebuild
  # against the STALE config.mak — silently shipping a binary built from the
  # wrong flags while printing success. The marker below is keyed on
  # RECIPE_HASH (a hash of the whole script, so any flag/minver edit changes
  # it) plus $arch, and is written only after configure succeeds, so a
  # mismatch or a missing/interrupted marker always falls through to a full
  # reconfigure — this file is the only thing "resume" trusts, and it cannot
  # go missing without also un-configuring the tree (make distclean runs in
  # the same branch, before configure).
  local configured_marker="$SRC/.configured-recipe"
  local configured=""
  if [ -f "$configured_marker" ]; then
    configured="$(cat "$configured_marker" 2>/dev/null || true)"
  fi

  if [ "$configured" = "$RECIPE_HASH $arch" ]; then
    echo "build-ffmpeg: $arch already configured for this recipe; resuming make"
  else
    rm -f "$configured_marker"
    ( cd "$SRC" && make distclean >/dev/null 2>&1 || true )
    # shellcheck disable=SC2046
    ( cd "$SRC" && ./configure \
        --prefix="$dest" \
        $(common_flags) \
        --extra-cflags="-mmacosx-version-min=$minver" \
        --extra-ldflags="-mmacosx-version-min=$minver" \
        "$@" )
    echo "$RECIPE_HASH $arch" > "$configured_marker"
  fi

  ( cd "$SRC" && make -j"$(sysctl -n hw.ncpu)" && cp ffmpeg "$dest/ffmpeg" )
}

case "$ARCH_MODE" in
  native)
    build_one arm64 11.0
    cp "$OUT/arm64/ffmpeg" "$OUT/ffmpeg"
    ;;
  universal)
    build_one arm64 11.0
    # ffmpeg's configure appends repeated --extra-cflags/--extra-ldflags
    # (add_cflags/add_ldflags use `append`), so these accumulate with the
    # -mmacosx-version-min flags added inside build_one rather than
    # replacing them.
    build_one x86_64 10.15 \
      --enable-cross-compile --arch=x86_64 --cpu=x86_64 --target-os=darwin \
      --cc=clang --extra-cflags="-arch x86_64" --extra-ldflags="-arch x86_64"
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
