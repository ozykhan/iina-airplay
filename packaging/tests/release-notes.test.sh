#!/usr/bin/env bash
# Pins release-notes.sh's LGPL-compliance logic: it reads four facts out of a
# packed .iinaplgz (bin/VERSIONS, bin/ffmpeg-LICENSE.md, and the package's own
# bytes) and must refuse to emit notes with any of them blank rather than
# silently shipping a hollow attribution block.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
RELEASE_NOTES="$ROOT/packaging/release-notes.sh"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
fails=0

pass() { echo "ok: $1"; }
bad()  { echo "FAIL: $1"; fails=$((fails + 1)); }

# Builds a minimal package with everything release-notes.sh reads; callers
# mutate the staging dir via the hook first. Split declarations — see the
# bash 3.2 note in Global Constraints.
make_pkg() {
  local name="$1"
  local hook="$2"
  local d="$TMP/$name"
  rm -rf "$d" && mkdir -p "$d/src/bin"
  echo '{"name":"AirPlay","version":"0.1.0","entry":"main.js"}' > "$d/src/Info.json"
  echo '// plugin' > "$d/src/main.js"
  printf 'helper_version=test\nhelper_sha256=0\nffmpeg_version=7.1\nffmpeg_sha256=0\nffmpeg_source_sha256=deadbeef\n' \
    > "$d/src/bin/VERSIONS"
  printf '# FFmpeg\n\n- Upstream source: https://ffmpeg.org/releases/ffmpeg-7.1.tar.xz\n' \
    > "$d/src/bin/ffmpeg-LICENSE.md"
  "$hook" "$d/src"
  ( cd "$d/src" && zip -q -r "$d/pkg.iinaplgz" . )
  echo "$d/pkg.iinaplgz"
}

noop() { :; }
drop_ffmpeg_version() { /usr/bin/sed -i '' '/^ffmpeg_version=/d' "$1/bin/VERSIONS"; }
drop_upstream_source() { /usr/bin/sed -i '' '/^- Upstream source: /d' "$1/bin/ffmpeg-LICENSE.md"; }

# --- positive case: a well-formed package fills in every field ---------------
good_pkg="$(make_pkg good noop)"
good_out="$("$RELEASE_NOTES" "$good_pkg" 2>&1)"
good_status=$?
good_sha="$(shasum -a 256 "$good_pkg" | cut -d' ' -f1)"

if [ "$good_status" -ne 0 ]; then
  bad "well-formed package — release-notes.sh exited $good_status:"
  echo "$good_out" | sed 's/^/    /'
else
  pass "well-formed package exits 0"

  grep -q 'FFmpeg 7.1' <<<"$good_out" \
    && pass "notes contain the FFmpeg version" \
    || bad "notes do not contain the FFmpeg version (7.1)"

  grep -q 'https://ffmpeg.org/releases/ffmpeg-7.1.tar.xz' <<<"$good_out" \
    && pass "notes contain the upstream source URL" \
    || bad "notes do not contain the upstream source URL"

  grep -q 'deadbeef' <<<"$good_out" \
    && pass "notes contain the source SHA-256" \
    || bad "notes do not contain the source SHA-256 (deadbeef)"

  grep -q "$good_sha" <<<"$good_out" \
    && pass "notes contain the package's own SHA-256" \
    || bad "notes do not contain the package's own SHA-256 ($good_sha)"

  # --- no leading indentation on any line -------------------------------------
  # The design-review-caught defect: a heredoc nested inside a YAML block
  # scalar carries the block's indentation into EVERY line it writes,
  # including headings and blank lines, rendering the whole markdown body —
  # LGPL attribution included — as one code block. A markdown list item
  # legitimately wraps with a couple of spaces of its own (see "Build
  # recipe:" above), so banning all leading whitespace would fail on correct
  # output. Check instead for the specific signature of the YAML-indentation
  # bug: top-level heading lines land at column 0, and blank lines are truly
  # empty rather than carrying the block scalar's whitespace.
  for heading in '## Install' '## Checksums' '## Bundled FFmpeg'; do
    grep -qE "^${heading}\$" <<<"$good_out" \
      && pass "heading '$heading' has no leading indentation" \
      || bad "heading '$heading' is missing or indented — would render as one code block in a YAML block scalar"
  done
  blank_with_whitespace="$(grep -E '^[[:space:]]+$' <<<"$good_out" || true)"
  if [ -z "$blank_with_whitespace" ]; then
    pass "blank lines carry no stray indentation"
  else
    bad "a blank line carries leading whitespace (the YAML block-scalar indentation signature)"
  fi
fi

# --- missing ffmpeg_version in bin/VERSIONS: rejected, not blanked -----------
noversion_pkg="$(make_pkg noversion drop_ffmpeg_version)"
noversion_out="$("$RELEASE_NOTES" "$noversion_pkg" 2>&1)"
noversion_status=$?
if [ "$noversion_status" -eq 0 ]; then
  bad "missing ffmpeg_version — release-notes.sh exited 0 instead of rejecting:"
  echo "$noversion_out" | sed 's/^/    /'
elif ! grep -qi 'ffmpeg_version' <<<"$noversion_out"; then
  bad "missing ffmpeg_version — rejected, but the message did not name ffmpeg_version:"
  echo "$noversion_out" | sed 's/^/    /'
else
  pass "a package missing ffmpeg_version is rejected, naming what could not be read"
fi

# --- missing the Upstream source line in ffmpeg-LICENSE.md: rejected --------
nosource_pkg="$(make_pkg nosource drop_upstream_source)"
nosource_out="$("$RELEASE_NOTES" "$nosource_pkg" 2>&1)"
nosource_status=$?
if [ "$nosource_status" -eq 0 ]; then
  bad "missing Upstream source line — release-notes.sh exited 0 instead of rejecting:"
  echo "$nosource_out" | sed 's/^/    /'
elif ! grep -qi 'upstream source' <<<"$nosource_out"; then
  bad "missing Upstream source line — rejected, but the message did not name the upstream source URL:"
  echo "$nosource_out" | sed 's/^/    /'
else
  pass "a package missing the Upstream source line is rejected, naming what could not be read"
fi

# --- usage guard --------------------------------------------------------------
usage_out="$("$RELEASE_NOTES" 2>&1)"
usage_status=$?
if [ "$usage_status" -eq 2 ]; then
  pass "no argument exits 2"
else
  bad "no argument exited $usage_status, not 2:"
  echo "$usage_out" | sed 's/^/    /'
fi

if [ "$fails" -ne 0 ]; then
  echo "$fails release-notes.sh test(s) failed"
  exit 1
fi
echo "all release-notes.sh tests passed"
