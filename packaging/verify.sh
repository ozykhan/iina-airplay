#!/usr/bin/env bash
# Verifies a packed .iinaplgz — the artifact users receive, not the staging
# tree. Checks are ordered cheapest-and-most-structural first so a failure
# names the real problem rather than a downstream symptom.
set -uo pipefail

PKG="${1:-}"
[ -n "$PKG" ] && [ -f "$PKG" ] || { echo "usage: verify.sh <package.iinaplgz>" >&2; exit 2; }

fail() { echo "verify: FAILED — $*" >&2; exit 1; }

# --- quarantine ---------------------------------------------------------------
# Cheapest possible check, so run it before even unzipping. Checked on the
# package FILE, not on files walked after extraction: this machine's zip/unzip
# (Apple's Info-Zip build) does not store or restore extended attributes
# across a zip round trip at all — verified by experiment, a com.apple.quarantine
# xattr set on a file before `zip -r` is gone after `unzip`, and so is a plain
# custom xattr, so a walk over files extracted by this script's own `unzip -q`
# could never observe quarantine regardless of how the archive was built. The
# package file itself is what actually carries com.apple.quarantine after a
# browser/curl download or a Finder "expand archive" — that's the realistic
# vector, and it's what this check can actually detect.
#
# Capture xattr's output before grepping it, rather than piping straight into
# `grep -q`: with `pipefail` set, a `grep -q` that matches early closes its
# end of the pipe and exits 0, but the writer (xattr here, codesign below) can
# then get SIGPIPE on its next write and die with status 141 — which
# `pipefail` reports as the PIPELINE's status, silently overriding grep's own
# success (verified by experiment: `cmd | grep -q pat` intermittently reports
# 141 under `set -o pipefail` even though the pattern was found). Command
# substitution reads the writer to completion first, so this can't happen.
xattr_out="$(xattr "$PKG" 2>/dev/null)"
grep -q 'com.apple.quarantine' <<<"$xattr_out" \
  && fail "com.apple.quarantine is set on $(basename "$PKG") — clear it (xattr -d com.apple.quarantine \"$PKG\") or re-download from a trusted source before verifying"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
unzip -q "$PKG" -d "$TMP" || { echo "verify: cannot unzip $PKG" >&2; exit 1; }

# --- manifest ---------------------------------------------------------------
[ -f "$TMP/Info.json" ] || fail "Info.json missing from the package root"
/usr/bin/python3 - "$TMP/Info.json" <<'PY' || exit 1
import json, re, sys
try:
    info = json.load(open(sys.argv[1]))
except Exception as e:
    print(f"verify: FAILED — Info.json does not parse: {e}", file=sys.stderr); sys.exit(1)
for key in ("identifier", "version", "ghRepo", "ghVersion"):
    if key not in info:
        print(f"verify: FAILED — Info.json has no {key}", file=sys.stderr); sys.exit(1)
if not re.fullmatch(r"[\w-]+/[\w-]+", str(info["ghRepo"])):
    print(f"verify: FAILED — ghRepo {info['ghRepo']!r} does not match IINA's "
          r"githubRepoRegex ^[\w-]+/[\w-]+$; IINA will refuse to load the plugin",
          file=sys.stderr); sys.exit(1)
if not isinstance(info["ghVersion"], int) or isinstance(info["ghVersion"], bool):
    print("verify: FAILED — ghVersion must be a JSON integer, not "
          f"{type(info['ghVersion']).__name__}", file=sys.stderr); sys.exit(1)
PY

# --- binaries: presence and mode first ---------------------------------------
# A separate pass, ahead of anything that inspects architectures or
# signatures: airplay-helper and ffmpeg are checked in this fixed order below,
# so a missing/non-executable SECOND binary must still be reported as such
# rather than as an arch/signature failure on the FIRST binary that happens to
# be checked before it in a combined loop.
for rel in bin/airplay-helper bin/ffmpeg; do
  b="$TMP/$rel"
  [ -f "$b" ] || fail "$rel missing from the package"
  [ -x "$b" ] || fail "$rel is not executable (mode $(stat -f '%Lp' "$b"))"
done

# --- binaries: architectures, signature ---------------------------------------
for rel in bin/airplay-helper bin/ffmpeg; do
  b="$TMP/$rel"
  archs="$(lipo -archs "$b" 2>/dev/null)"
  for want in x86_64 arm64; do
    grep -qw "$want" <<<"$archs" || fail "$rel is missing the $want slice (has: ${archs:-none})"
  done
  # Two distinct checks, because they answer different questions:
  #   --verify is the cryptographic one — it re-hashes the file and fails if
  #     the bytes no longer match the signature. `codesign -dv` does NOT do
  #     this: on a binary corrupted after signing it still exits 0 and still
  #     prints Signature=adhoc (verified 2026-08-29).
  #   -dv | grep adhoc confirms it is an AD-HOC signature specifically, which
  #     --verify alone would not tell us.
  # codesign writes -dv's output to stderr unbuffered (one write per line), so
  # piping it straight into `grep -q` is unsafe under `pipefail`: grep exits
  # the instant it matches "Signature=adhoc" (an early line), closing the
  # pipe while codesign is still writing the remaining lines, which then hit
  # SIGPIPE — pipefail reports THAT (status 141) as the pipeline's exit
  # status, overriding grep's own success. Reproduced consistently on this
  # machine. Capture first, then grep the captured text.
  codesign --verify --strict "$b" 2>/dev/null \
    || fail "$rel fails signature verification — its bytes do not match its signature"
  dv_out="$(codesign -dv "$b" 2>&1)"
  grep -q 'Signature=adhoc' <<<"$dv_out" \
    || fail "$rel is not ad-hoc signed"
done

# --- ffmpeg licensing and capabilities --------------------------------------
FF="$TMP/bin/ffmpeg"
config="$("$FF" -hide_banner -version 2>/dev/null)" || fail "bin/ffmpeg does not run"
grep -q -- '--enable-gpl'     <<<"$config" && fail "bundled ffmpeg was built with --enable-gpl"
grep -q -- '--enable-nonfree' <<<"$config" && fail "bundled ffmpeg was built with --enable-nonfree"

encoders="$("$FF" -hide_banner -encoders 2>/dev/null)"
for e in aac eac3 hevc_videotoolbox h264_videotoolbox webvtt; do
  grep -qw "$e" <<<"$encoders" || fail "bundled ffmpeg lacks the $e encoder"
done
grep -qw libx264 <<<"$encoders" && fail "bundled ffmpeg contains libx264 (GPL)"

decoders="$("$FF" -hide_banner -decoders 2>/dev/null)"
for d in h264 hevc vp9 av1 mpeg2video; do
  grep -qw "$d" <<<"$decoders" \
    || fail "bundled ffmpeg lacks the $d decoder; helper/job.go's re-encode branch needs it"
done

# --- linkage ----------------------------------------------------------------
# The check that catches a package working only on the machine that built it.
# ffmpeg is a universal binary, and plain `otool -L` on a universal binary
# prints a per-architecture header line (e.g. "…/ffmpeg (architecture
# arm64):") for every slice after the first. `tail -n +2` only strips the
# first header, so a naive grep mistakes the second slice's header for a
# stray dependency. Inspect each slice on its own with `otool -arch` instead,
# which prints exactly one (unlabeled) header line per invocation.
for arch in x86_64 arm64; do
  strays="$(otool -arch "$arch" -L "$FF" 2>/dev/null | tail -n +2 | grep -v -E '^[[:space:]]+(/usr/lib/|/System/)' || true)"
  [ -z "$strays" ] || fail "bin/ffmpeg ($arch slice) links non-system libraries:
$strays"
done

echo "verify: OK — $(basename "$PKG")"
