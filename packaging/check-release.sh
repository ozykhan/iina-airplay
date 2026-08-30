#!/usr/bin/env bash
# Gates a release tag against Info.json — before the 25-minute build,
# so a mismatch costs seconds.
#
# Two things IINA cares about that nothing else checks:
#   - the tag must name the version the package will report, or the release
#     page and its payload disagree about what this is;
#   - ghVersion must go UP, or IINA's update check never fires. That failure is
#     silent: the release ships fine, installs fine for new users, and simply
#     never reaches anyone who already has the plugin.
set -uo pipefail

# Overridable so packaging/tests/check-release.test.sh can point the gate at a
# throwaway repository instead of this one.
ROOT="${CHECK_RELEASE_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
# The manifest lives at the REPOSITORY root, not under plugin/: IINA's update
# check fetches raw.githubusercontent.com/<ghRepo>/master/Info.json, and while
# it sat under plugin/ that URL 404'd and updates were never offered.
INFO="$ROOT/Info.json"

TAG="${1:-}"
[ -n "$TAG" ] || { echo "check-release: usage: check-release.sh <tag>" >&2; exit 2; }

fail() { echo "check-release: FAILED — $*" >&2; exit 1; }

[ -f "$INFO" ] || fail "$INFO not found"

# One python call reads and type-checks both fields, printing them on two
# lines. Capturing stderr into the same variable means a parse error or a type
# error arrives as the failure message rather than as an empty result.
if ! info_fields="$(/usr/bin/python3 - "$INFO" 2>&1 <<'PY'
import json, sys
try:
    info = json.load(open(sys.argv[1]))
except Exception as e:
    sys.exit(f"Info.json does not parse: {e}")
version = info.get("version")
if not isinstance(version, str) or not version:
    sys.exit("Info.json has no string version")
gh = info.get("ghVersion")
# bool is a subclass of int in Python, and `true` is not a version counter.
if not isinstance(gh, int) or isinstance(gh, bool):
    sys.exit(f"ghVersion must be a JSON integer, not {type(gh).__name__}")
print(version)
print(gh)
PY
)"; then
  fail "$info_fields"
fi

version="$(sed -n 1p <<<"$info_fields")"
ghversion="$(sed -n 2p <<<"$info_fields")"

[ "$TAG" = "v$version" ] \
  || fail "tag $TAG does not match Info.json version $version — expected tag v$version"

# A shallow clone truncates history in exactly the way that makes the
# previous-tag lookup below come back empty whether or not a previous tag
# actually exists — the empty result is ambiguous, and letting it fall
# through unchecked resolves that ambiguity in the dangerous direction: a
# forgotten ghVersion bump on a truncated checkout would sail through as if
# this were the first release. Settle the ambiguity here, before the lookup,
# instead of guessing from its result.
if ! is_shallow="$(git -C "$ROOT" rev-parse --is-shallow-repository 2>&1)"; then
  fail "cannot determine whether $ROOT has full git history: $is_shallow"
fi
[ "$is_shallow" = "false" ] \
  || fail "checkout has truncated history (shallow clone) — the ghVersion monotonicity check needs full history with tags; re-run actions/checkout with fetch-depth: 0"

# --abbrev=0 on the tag's PARENT gives the nearest tag strictly before this
# one. Requires unshallowed history with tags: actions/checkout must run with
# fetch-depth: 0, or this finds nothing and the monotonicity check is skipped
# exactly when it is needed.
prev="$(git -C "$ROOT" describe --tags --abbrev=0 "$TAG^" 2>/dev/null || true)"

if [ -z "$prev" ]; then
  echo "check-release: no tag before $TAG; skipping the ghVersion monotonicity check (first release)"
else
  # Info.json moved from plugin/ to the repository root, so the previous tag's
  # copy may be at either path: v0.1.0 and v0.2.0 predate the move, everything
  # from v0.3.0 on does not. Try the root first — it is the authoritative
  # location, and a tag from after the move could still carry a leftover file
  # at the old path. Both misses are fatal rather than skipped: treating "no
  # manifest found" as "nothing to compare" would silently disable the
  # monotonicity check, which is the exact failure this gate exists to catch.
  prev_info=""
  for prev_path in Info.json plugin/Info.json; do
    prev_info="$(git -C "$ROOT" show "$prev:$prev_path" 2>/dev/null)" && break
    prev_info=""
  done
  [ -n "$prev_info" ] \
    || fail "cannot read Info.json at $prev (looked at both Info.json and plugin/Info.json)"
  # Read offline from git rather than from the GitHub API, so the gate works on
  # a fork, on a detached checkout, and with no network.
  if ! prev_gh="$(/usr/bin/python3 -c 'import json,sys; g=json.load(sys.stdin).get("ghVersion"); sys.exit("previous tag ghVersion is not an integer") if not isinstance(g,int) or isinstance(g,bool) else print(g)' <<<"$prev_info" 2>&1)"; then
    fail "$prev_gh (at $prev)"
  fi
  if [ "$ghversion" -le "$prev_gh" ]; then
    fail "ghVersion did not increase: $prev has $prev_gh, $TAG has $ghversion. IINA's update check compares ghVersion, so existing users would never be offered this release."
  fi
fi

echo "check-release: OK — $TAG matches version $version, ghVersion $ghversion"
