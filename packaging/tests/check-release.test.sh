#!/usr/bin/env bash
# Exercises the tag gate against throwaway git repositories, so the assertions
# can be checked without pushing a deliberately bad tag at the real repo.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CHECK="$ROOT/packaging/check-release.sh"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
fails=0

# Builds a repo whose history is one commit per (version, ghVersion, tag)
# triple. Pass triples as "version:ghVersion:tag"; an empty tag leaves the
# commit untagged. Split declarations — see the bash 3.2 note in Global
# Constraints.
make_repo() {
  local name="$1"
  shift
  local d="$TMP/$name"
  rm -rf "$d" && mkdir -p "$d/plugin"
  git -C "$d" init -q -b master
  git -C "$d" config user.email test@example.com
  git -C "$d" config user.name "Test"
  local triple version ghversion tag
  for triple in "$@"; do
    version="${triple%%:*}"
    ghversion="$(echo "$triple" | cut -d: -f2)"
    tag="$(echo "$triple" | cut -d: -f3)"
    cat > "$d/plugin/Info.json" <<JSON
{"name":"AirPlay","identifier":"dev.faruk.iina-airplay","version":"$version",
 "ghRepo":"ozykhan/iina-airplay","ghVersion":$ghversion,"entry":"main.js"}
JSON
    git -C "$d" add -A
    git -C "$d" commit -q -m "$version"
    [ -n "$tag" ] && git -C "$d" tag "$tag"
  done
  echo "$d"
}

expect_ok() {
  local label="$1" repo="$2" tag="$3" pattern="$4" out status
  out="$(CHECK_RELEASE_ROOT="$repo" "$CHECK" "$tag" 2>&1)"
  status=$?
  if [ "$status" -ne 0 ]; then
    echo "FAIL: $label — gate rejected a tag it should have accepted:"
    echo "$out" | sed 's/^/    /'
    fails=$((fails + 1))
  elif ! grep -qi "$pattern" <<<"$out"; then
    echo "FAIL: $label — accepted, but the message did not mention '$pattern':"
    echo "$out" | sed 's/^/    /'
    fails=$((fails + 1))
  else
    echo "ok: $label"
  fi
}

expect_fail() {
  local label="$1" repo="$2" tag="$3" pattern="$4" out status
  out="$(CHECK_RELEASE_ROOT="$repo" "$CHECK" "$tag" 2>&1)"
  status=$?
  if [ "$status" -eq 0 ]; then
    echo "FAIL: $label — gate accepted a tag it should have rejected:"
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

# The first release has no previous tag to compare against. A gate that cannot
# pass on v0.1.0 is a gate that gets disabled, so this case is load-bearing.
first="$(make_repo first "0.1.0:1:v0.1.0")"
expect_ok "first release, no previous tag" "$first" v0.1.0 "first release"

mismatch="$(make_repo mismatch "0.1.0:1:v0.2.0")"
expect_fail "tag does not match Info.json version" "$mismatch" v0.2.0 "does not match"

bumped="$(make_repo bumped "0.1.0:1:v0.1.0" "0.2.0:2:v0.2.0")"
expect_ok "ghVersion increased" "$bumped" v0.2.0 "OK"

# The silent one: the release ships fine and simply never reaches existing
# users, because IINA's update check compares ghVersion.
forgot="$(make_repo forgot "0.1.0:1:v0.1.0" "0.2.0:1:v0.2.0")"
expect_fail "ghVersion not bumped" "$forgot" v0.2.0 "ghVersion"

went_back="$(make_repo wentback "0.1.0:5:v0.1.0" "0.2.0:4:v0.2.0")"
expect_fail "ghVersion went backwards" "$went_back" v0.2.0 "ghVersion"

# ghVersion is an Int in IINA's schema. A quoted "2" parses as JSON and looks
# right in a diff.
strver="$(make_repo strver "0.1.0:\"2\":v0.1.0")"
expect_fail "ghVersion is a string" "$strver" v0.1.0 "integer"

if [ "$fails" -ne 0 ]; then
  echo "$fails check-release.sh test(s) failed"
  exit 1
fi
echo "all check-release.sh tests passed"
