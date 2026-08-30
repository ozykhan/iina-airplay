#!/usr/bin/env bash
# Exercises the post-publish gate against fixture endpoints served over file://,
# so every branch can be checked without publishing a deliberately broken
# release. The script fetches with `curl -fsSL` and judges by exit status rather
# than %{http_code} precisely so this indirection works: file:// reports a
# status of 000, but a missing file still fails the fetch.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CHECK="$ROOT/packaging/check-published.sh"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
fails=0

REPO="ozykhan/iina-airplay"

# Builds a fixture pair of endpoints: the releases/latest JSON the GitHub API
# would return, and the Info.json raw.githubusercontent would serve from the
# repo root of master. Split declarations — see the bash 3.2 note in Global
# Constraints.
make_endpoints() {
  local name="$1" latest_json="$2" raw_json="$3"
  local d="$TMP/$name"
  rm -rf "$d"
  mkdir -p "$d/api/repos/$REPO/releases" "$d/raw/$REPO/master" "$d/local"
  printf '%s' "$latest_json" > "$d/api/repos/$REPO/releases/latest"
  if [ -n "$raw_json" ]; then
    printf '%s' "$raw_json" > "$d/raw/$REPO/master/Info.json"
  fi
  # The local manifest the script reads ghRepo out of.
  cat > "$d/local/Info.json" <<JSON
{"name":"AirPlay","identifier":"dev.faruk.iina-airplay","version":"0.2.0",
 "ghRepo":"$REPO","ghVersion":2,"entry":"main.js"}
JSON
  echo "$d"
}

run_check() {
  local d="$1" tag="$2"
  CHECK_PUBLISHED_ROOT="$d/local" \
  CHECK_PUBLISHED_API_BASE="file://$d/api" \
  CHECK_PUBLISHED_RAW_BASE="file://$d/raw" \
    "$CHECK" "$tag" 2>&1
}

expect_ok() {
  local label="$1" d="$2" tag="$3" out status
  out="$(run_check "$d" "$tag")"
  status=$?
  if [ "$status" -ne 0 ]; then
    echo "FAIL: $label — gate rejected a release it should have accepted:"
    echo "$out" | sed 's/^/    /'
    fails=$((fails + 1))
  else
    echo "ok: $label"
  fi
}

expect_fail() {
  local label="$1" d="$2" tag="$3" pattern="$4" out status
  out="$(run_check "$d" "$tag")"
  status=$?
  if [ "$status" -eq 0 ]; then
    echo "FAIL: $label — gate accepted a release it should have rejected:"
    echo "$out" | sed 's/^/    /'
    fails=$((fails + 1))
  elif ! grep -qi "$pattern" <<<"$out"; then
    echo "FAIL: $label — rejected, but the message did not mention '$pattern':"
    echo "$out" | sed 's/^/    /'
    fails=$((fails + 1))
  else
    echo "ok: $label"
  fi
}

GOOD_LATEST='{"tag_name":"v0.2.0","draft":false,"prerelease":false,
 "assets":[{"name":"iina-airplay.iinaplgz"},{"name":"iina-airplay.iinaplgz.sha256"}]}'
GOOD_RAW='{"name":"AirPlay","version":"0.2.0","ghRepo":"ozykhan/iina-airplay","ghVersion":2,"entry":"main.js"}'

good="$(make_endpoints good "$GOOD_LATEST" "$GOOD_RAW")"
expect_ok "a correctly published release passes both mechanisms" "$good" v0.2.0

# --- the install mechanism ----------------------------------------------------
stale="$(make_endpoints stale '{"tag_name":"v0.1.0","draft":false,"prerelease":false,
 "assets":[{"name":"iina-airplay.iinaplgz"}]}' "$GOOD_RAW")"
expect_fail "releases/latest still points at the previous tag" "$stale" v0.2.0 "latest"

pre="$(make_endpoints pre '{"tag_name":"v0.2.0","draft":false,"prerelease":true,
 "assets":[{"name":"iina-airplay.iinaplgz"}]}' "$GOOD_RAW")"
expect_fail "marked as a pre-release" "$pre" v0.2.0 "pre-release"

noasset="$(make_endpoints noasset '{"tag_name":"v0.2.0","draft":false,"prerelease":false,
 "assets":[{"name":"notes.txt"}]}' "$GOOD_RAW")"
expect_fail "no .iinaplgz asset" "$noasset" v0.2.0 "iinaplgz"

two="$(make_endpoints two '{"tag_name":"v0.2.0","draft":false,"prerelease":false,
 "assets":[{"name":"a.iinaplgz"},{"name":"b.iinaplgz"}]}' "$GOOD_RAW")"
expect_fail "more than one .iinaplgz asset" "$two" v0.2.0 "exactly one"

# --- the update mechanism -----------------------------------------------------
# The whole reason this gate exists: v0.2.0 published correctly and still
# reached nobody, because the manifest was not at the repo root of master.
missing="$(make_endpoints missing "$GOOD_LATEST" "")"
expect_fail "no manifest at the repo root of master" "$missing" v0.2.0 "update check"

behind="$(make_endpoints behind "$GOOD_LATEST" \
  '{"name":"AirPlay","version":"0.1.0","ghRepo":"ozykhan/iina-airplay","ghVersion":1,"entry":"main.js"}')"
expect_fail "master's manifest was never bumped" "$behind" v0.2.0 "0.1.0"

strver="$(make_endpoints strver "$GOOD_LATEST" \
  '{"name":"AirPlay","version":"0.2.0","ghRepo":"ozykhan/iina-airplay","ghVersion":"2","entry":"main.js"}')"
expect_fail "ghVersion on master is a string" "$strver" v0.2.0 "integer"

# --- usage --------------------------------------------------------------------
noarg_out="$(CHECK_PUBLISHED_ROOT="$good/local" "$CHECK" 2>&1)"
if [ $? -eq 2 ]; then
  echo "ok: no argument exits 2"
else
  echo "FAIL: no argument — expected exit 2, got a different status:"
  echo "$noarg_out" | sed 's/^/    /'
  fails=$((fails + 1))
fi

if [ "$fails" -ne 0 ]; then
  echo "$fails check-published.sh test(s) failed"
  exit 1
fi
echo "all check-published.sh tests passed"
