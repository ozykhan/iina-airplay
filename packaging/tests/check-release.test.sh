#!/usr/bin/env bash
# Exercises the tag gate against throwaway git repositories, so the assertions
# can be checked without pushing a deliberately bad tag at the real repo.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CHECK="$ROOT/packaging/check-release.sh"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
fails=0

# Builds a repo whose history is one commit per (version, ghVersion, tag,
# layout) tuple. Pass tuples as "version:ghVersion:tag[:layout]"; an empty tag
# leaves the commit untagged. Split declarations — see the bash 3.2 note in
# Global Constraints.
#
# layout selects where that commit puts the manifest, so the tag gate can be
# exercised across the move of Info.json from plugin/ to the repository root
# (IINA's update check reads the repo root, so that is where it now lives):
#   root   — $d/Info.json          (default; the layout from v0.3.0 onward)
#   plugin — $d/plugin/Info.json   (the layout of v0.1.0 and v0.2.0)
#   none   — no manifest at all    (neither location; must fail loudly)
make_repo() {
  local name="$1"
  shift
  local d="$TMP/$name"
  rm -rf "$d" && mkdir -p "$d/plugin"
  git -C "$d" init -q -b master
  git -C "$d" config user.email test@example.com
  git -C "$d" config user.name "Test"
  local triple version ghversion tag layout target
  for triple in "$@"; do
    version="${triple%%:*}"
    ghversion="$(echo "$triple" | cut -d: -f2)"
    tag="$(echo "$triple" | cut -d: -f3)"
    layout="$(echo "$triple" | cut -d: -f4)"
    [ -n "$layout" ] || layout=root
    # Clear both locations first, so a commit that changes layout records an
    # actual move rather than leaving a stale manifest behind at the old path.
    rm -f "$d/Info.json" "$d/plugin/Info.json"
    case "$layout" in
      root)   target="$d/Info.json" ;;
      plugin) target="$d/plugin/Info.json" ;;
      none)   target="" ;;
      *) echo "make_repo: unknown layout '$layout'" >&2; exit 2 ;;
    esac
    if [ -n "$target" ]; then
      cat > "$target" <<JSON
{"name":"AirPlay","identifier":"dev.faruk.iina-airplay","version":"$version",
 "ghRepo":"ozykhan/iina-airplay","ghVersion":$ghversion,"entry":"main.js"}
JSON
    else
      # git needs something to commit when there is no manifest.
      echo "$version" > "$d/placeholder"
    fi
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

# actions/checkout's default is fetch-depth: 1 with no tags, which truncates
# history the same way a genuine first release does: the previous-tag lookup
# comes back empty either way. Shallow-clone the "forgot" fixture (a real
# ghVersion bug) at v0.2.0 so its previous tag is unreachable, and confirm the
# gate refuses to guess rather than silently treating it as a first release.
# The file:// form is required for --depth to actually truncate a local path.
shallow="$TMP/shallow"
git clone -q --depth 1 --branch v0.2.0 "file://$forgot" "$shallow" 2>/dev/null
expect_fail "shallow clone hides the previous tag" "$shallow" v0.2.0 "shallow"

went_back="$(make_repo wentback "0.1.0:5:v0.1.0" "0.2.0:4:v0.2.0")"
expect_fail "ghVersion went backwards" "$went_back" v0.2.0 "ghVersion"

# ghVersion is an Int in IINA's schema. A quoted "2" parses as JSON and looks
# right in a diff.
strver="$(make_repo strver "0.1.0:\"2\":v0.1.0")"
expect_fail "ghVersion is a string" "$strver" v0.1.0 "integer"

# --- the move of Info.json from plugin/ to the repository root ---------------
# Info.json now lives at the repo root, because IINA's update check reads
# raw.githubusercontent.com/<ghRepo>/master/Info.json. The gate has to keep
# working ACROSS that move: v0.1.0 and v0.2.0 are already tagged with the
# manifest under plugin/, so the previous-tag lookup for the next release
# resolves to the old path, and every release after that to the new one.

# The immediate case: the next tag cut after the move. Previous tag still has
# the manifest under plugin/; this one has it at the root.
migrate="$(make_repo migrate "0.2.0:2:v0.2.0:plugin" "0.3.0:3:v0.3.0:root")"
expect_ok "previous tag kept the manifest under plugin/" "$migrate" v0.3.0 "OK"

# The steady state afterwards: both tags at the root.
settled="$(make_repo settled "0.3.0:3:v0.3.0:root" "0.4.0:4:v0.4.0:root")"
expect_ok "both tags have the manifest at the root" "$settled" v0.4.0 "OK"

# The fallback must not weaken the gate it exists to serve: a forgotten
# ghVersion bump across the move is still the silent failure that strands
# existing users, and must still be refused.
migrate_forgot="$(make_repo migforgot "0.2.0:2:v0.2.0:plugin" "0.3.0:2:v0.3.0:root")"
expect_fail "ghVersion not bumped across the move" "$migrate_forgot" v0.3.0 "ghVersion"

# And a previous tag carrying no manifest in EITHER location must fail loudly.
# A fallback written as "try root, else try plugin/, else carry on" would turn
# this into a skipped monotonicity check — the same silent hole the shallow
# clone case above exists to close.
no_manifest="$(make_repo nomanifest "0.2.0:2:v0.2.0:none" "0.3.0:3:v0.3.0:root")"
expect_fail "previous tag has no manifest in either location" "$no_manifest" v0.3.0 "cannot read"

if [ "$fails" -ne 0 ]; then
  echo "$fails check-release.sh test(s) failed"
  exit 1
fi
echo "all check-release.sh tests passed"
