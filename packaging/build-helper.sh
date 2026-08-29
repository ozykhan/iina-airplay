#!/usr/bin/env bash
# Builds the universal, ad-hoc-signed airplay-helper that ships in the package.
#
# Go's linker ad-hoc signs darwin/arm64 binaries but not amd64, and lipo does
# not carry slice signatures across — so the universal artifact is signed here
# explicitly. IINA's installer applies no quarantine (docs/distribution.md), so
# this signature is the only thing standing between the user and a Gatekeeper
# refusal.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT="$ROOT/build/helper"
rm -rf "$OUT" && mkdir -p "$OUT"

for arch in arm64 amd64; do
  ( cd "$ROOT/helper" && CGO_ENABLED=0 GOOS=darwin GOARCH="$arch" \
      go build -trimpath -ldflags="-s -w" -o "$OUT/airplay-helper-$arch" . )
done

lipo -create "$OUT/airplay-helper-arm64" "$OUT/airplay-helper-amd64" \
     -output "$OUT/airplay-helper.unsigned"
codesign --force --sign - "$OUT/airplay-helper.unsigned"
mv "$OUT/airplay-helper.unsigned" "$OUT/airplay-helper"
rm -f "$OUT/airplay-helper-arm64" "$OUT/airplay-helper-amd64"

echo "build-helper: wrote $OUT/airplay-helper"
