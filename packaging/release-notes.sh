#!/usr/bin/env bash
# Emits the GitHub release notes for a packed .iinaplgz, on stdout.
#
# Every fact here is read out of the package itself. The FFmpeg version, its
# upstream source URL and its source SHA-256 are an LGPL compliance
# requirement, not decoration — restating them by hand is how they drift from
# the binary actually shipped.
set -euo pipefail

PKG="${1:-}"
[ -n "$PKG" ] && [ -f "$PKG" ] \
  || { echo "release-notes: usage: release-notes.sh <package.iinaplgz>" >&2; exit 2; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
unzip -q "$PKG" -d "$TMP" || { echo "release-notes: cannot unzip $PKG" >&2; exit 1; }

need() {
  [ -n "$2" ] || { echo "release-notes: could not read $1 from $PKG" >&2; exit 1; }
}

ffmpeg_version="$(grep '^ffmpeg_version=' "$TMP/bin/VERSIONS" | cut -d= -f2-)" || true
source_sha256="$(grep '^ffmpeg_source_sha256=' "$TMP/bin/VERSIONS" | cut -d= -f2-)" || true
source_url="$(sed -n 's/^- Upstream source: //p' "$TMP/bin/ffmpeg-LICENSE.md" | head -1)"
pkg_sha256="$(shasum -a 256 "$PKG" | cut -d' ' -f1)"

need ffmpeg_version "$ffmpeg_version"
need ffmpeg_source_sha256 "$source_sha256"
need "the upstream source URL" "$source_url"

cat <<EOF
## Install

In IINA: **Settings → Plugins → Install**, and enter:

\`\`\`
ozykhan/iina-airplay
\`\`\`

Install **through IINA**, not by downloading the package below in a browser.
IINA's installer applies no \`com.apple.quarantine\`, so the bundled binaries run
under Gatekeeper on their ad-hoc signatures alone. Downloading the
\`.iinaplgz\` by hand and opening it quarantines everything inside it.

On macOS 15+, grant IINA the **Local Network** permission the first time it
casts, or the Apple TV cannot reach the stream.

Everything the plugin needs ships inside the package. There are no
prerequisites and nothing is downloaded at runtime.

## Checksums

| File | SHA-256 |
| --- | --- |
| \`$(basename "$PKG")\` | \`$pkg_sha256\` |

## Bundled FFmpeg

This package bundles an unmodified build of FFmpeg $ffmpeg_version, licensed
under the GNU Lesser General Public License version 2.1 or later. No GPL
components are enabled and \`--enable-gpl\` was never passed.

- Upstream source: $source_url
- Source SHA-256: \`$source_sha256\`
- Build recipe: \`packaging/build-ffmpeg.sh\` at this tag, which contains the
  complete configure line used to produce this binary.

A copy of the license text ships inside the package as
\`bin/COPYING.LGPLv2.1\`.
EOF
