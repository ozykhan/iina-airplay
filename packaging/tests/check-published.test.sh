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

# Builds a fixture set of endpoints: the releases/latest JSON the GitHub API
# would return, and the Info.json raw.githubusercontent would serve from the
# repo root of each beacon branch — master, which IINA 1.4.x reads, and main,
# which IINA 1.5.0 and later read. main is a mirror of master, so it gets the
# same manifest unless a fourth argument says otherwise: a different manifest
# to make the two disagree, or "" to leave main with none at all. Split
# declarations — see the bash 3.2 note in Global Constraints.
make_endpoints() {
  local name="$1" latest_json="$2" raw_json="$3"
  local main_json="${4-$raw_json}"
  local d="$TMP/$name"
  rm -rf "$d"
  mkdir -p "$d/api/repos/$REPO/releases" "$d/raw/$REPO/master" "$d/raw/$REPO/main" "$d/local"
  printf '%s' "$latest_json" > "$d/api/repos/$REPO/releases/latest"
  if [ -n "$raw_json" ]; then
    printf '%s' "$raw_json" > "$d/raw/$REPO/master/Info.json"
  fi
  if [ -n "$main_json" ]; then
    printf '%s' "$main_json" > "$d/raw/$REPO/main/Info.json"
  fi
  # The local manifest the script reads ghRepo out of.
  cat > "$d/local/Info.json" <<JSON
{"name":"AirPlay","identifier":"dev.faruk.iina-airplay","version":"0.2.0",
 "ghRepo":"$REPO","ghVersion":2,"entry":"main.js"}
JSON
  echo "$d"
}

# RELEASE_ONLY, when set, is passed through as an extra argument, so the same
# fixtures and the same expect_* helpers drive both modes.
RELEASE_ONLY=""
run_check() {
  local d="$1" tag="$2"
  CHECK_PUBLISHED_ROOT="$d/local" \
  CHECK_PUBLISHED_API_BASE="file://$d/api" \
  CHECK_PUBLISHED_RAW_BASE="file://$d/raw" \
    "$CHECK" ${RELEASE_ONLY:+"$RELEASE_ONLY"} "$tag" 2>&1
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

BEHIND_RAW='{"name":"AirPlay","version":"0.1.0","ghRepo":"ozykhan/iina-airplay","ghVersion":1,"entry":"main.js"}'

good="$(make_endpoints good "$GOOD_LATEST" "$GOOD_RAW")"
expect_ok "a correctly published release passes both mechanisms" "$good" v0.2.0

# The success line is the release's sign-off, so it has to say which beacons it
# actually read. "master is fine" was true of every release that 1.5.0 broke.
good_out="$(run_check "$good" v0.2.0)"
if grep -q "on master" <<<"$good_out" && grep -q "on main" <<<"$good_out"; then
  echo "ok: the success line names both beacon branches"
else
  echo "FAIL: success line — expected it to name the manifest on master AND on main:"
  echo "$good_out" | sed 's/^/    /'
  fails=$((fails + 1))
fi

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

behind="$(make_endpoints behind "$GOOD_LATEST" "$BEHIND_RAW")"
expect_fail "master's manifest was never bumped" "$behind" v0.2.0 "0.1.0"

strver="$(make_endpoints strver "$GOOD_LATEST" \
  '{"name":"AirPlay","version":"0.2.0","ghRepo":"ozykhan/iina-airplay","ghVersion":"2","entry":"main.js"}')"
expect_fail "ghVersion on master is a string" "$strver" v0.2.0 "integer"

# --- the update mechanism, IINA 1.5.0 and later -------------------------------
# 1.5.0 moved the beacon from master/Info.json to main/Info.json, and turned a
# failed fetch into a thrown error: a missing manifest there is no longer a
# quiet "No update found." but "Error checking for updates." for EVERY plugin
# the user has. main is a mirror of master, kept by
# .github/workflows/mirror-main.yml — so these are the ways the mirror can be
# wrong while master, and everything this gate used to look at, is right.
STRVER_RAW='{"name":"AirPlay","version":"0.2.0","ghRepo":"ozykhan/iina-airplay","ghVersion":"2","entry":"main.js"}'

nomain="$(make_endpoints nomain "$GOOD_LATEST" "$GOOD_RAW" "")"
expect_fail "no manifest on main, the branch IINA 1.5 reads" "$nomain" v0.2.0 "main/Info.json"

mainbehind="$(make_endpoints mainbehind "$GOOD_LATEST" "$GOOD_RAW" "$BEHIND_RAW")"
expect_fail "main's manifest is behind master's" "$mainbehind" v0.2.0 "manifest on main"

# A stale main is fixed by re-running the mirror, not by editing a manifest, so
# the message has to point there — but only when main is wrong ON ITS OWN. When
# master is behind too, the bump never landed at all, and naming the mirror
# would send the reader to the wrong place.
mainbehind_out="$(run_check "$mainbehind" v0.2.0)"
if grep -q "mirror-main" <<<"$mainbehind_out"; then
  echo "ok: a main that is behind on its own points at the mirror workflow"
else
  echo "FAIL: main behind — the message does not name the mirror workflow:"
  echo "$mainbehind_out" | sed 's/^/    /'
  fails=$((fails + 1))
fi
behind_out="$(run_check "$behind" v0.2.0)"
if grep -q "mirror-main" <<<"$behind_out"; then
  echo "FAIL: both branches behind — the message blames the mirror, but master is behind too:"
  echo "$behind_out" | sed 's/^/    /'
  fails=$((fails + 1))
else
  echo "ok: when master is behind too, the mirror is not blamed"
fi

mainstr="$(make_endpoints mainstr "$GOOD_LATEST" "$GOOD_RAW" "$STRVER_RAW")"
expect_fail "ghVersion on main is a string" "$mainstr" v0.2.0 "ghVersion on main"

# The manifest-fix case: ghVersion was forgotten in a release and patched on
# master afterwards, and the mirror missed that push. Both branches name the
# right version, so only comparing the counters shows that 1.5 installs are
# still being told there is nothing newer.
GHBEHIND_RAW='{"name":"AirPlay","version":"0.2.0","ghRepo":"ozykhan/iina-airplay","ghVersion":1,"entry":"main.js"}'
ghskew="$(make_endpoints ghskew "$GOOD_LATEST" "$GOOD_RAW" "$GHBEHIND_RAW")"
expect_fail "main agrees on version but carries an older ghVersion" "$ghskew" v0.2.0 "disagree"

# And the other way round: a correct main must not excuse master, which is what
# every 1.4.x install still reads.
nomaster="$(make_endpoints nomaster "$GOOD_LATEST" "" "$GOOD_RAW")"
expect_fail "no manifest on master, the branch IINA 1.4 reads" "$nomaster" v0.2.0 "master/Info.json"

masterbehind="$(make_endpoints masterbehind "$GOOD_LATEST" "$BEHIND_RAW" "$GOOD_RAW")"
expect_fail "master's manifest is behind main's" "$masterbehind" v0.2.0 "manifest on master"

# --- --release-only: the half that must be true before the bump is merged -----
# The release sequence publishes the release BEFORE the bump lands on master, so
# that master's beacon never announces a version whose asset is not up yet. That
# leaves a deliberate intermediate state — release live, master still on the old
# version — in which the full two-sided gate is SUPPOSED to fail. --release-only
# is what gates the irreversible step (merging the bump) in that state.
RELEASE_ONLY="--release-only"

# The step-6 state itself: the same fixture the full gate rejects above.
expect_ok "--release-only passes while master's manifest is still behind" "$behind" v0.2.0

# Proves the raw fetch is SKIPPED, not fetched and ignored. Fetching a manifest
# whose result is discarded invites someone to later "fix" the discrepancy it
# prints, which would reintroduce the coupling this flag exists to break.
expect_ok "--release-only passes with no manifest on master at all" "$missing" v0.2.0
expect_ok "--release-only passes with no manifest on main" "$nomain" v0.2.0

# The install half must still be gated exactly as hard.
expect_fail "--release-only still rejects a stale releases/latest" "$stale" v0.2.0 "latest"
expect_fail "--release-only still rejects a pre-release" "$pre" v0.2.0 "pre-release"
expect_fail "--release-only still rejects a missing asset" "$noasset" v0.2.0 "iinaplgz"
expect_fail "--release-only still rejects two .iinaplgz assets" "$two" v0.2.0 "exactly one"

# The success line must not be mistakable for the full gate — this flag is run
# mid-sequence, and someone reading a bare "OK" could take it for step 8.
relonly_out="$(run_check "$behind" v0.2.0)"
if grep -q "NOT checked" <<<"$relonly_out"; then
  echo "ok: --release-only says the beacons were not checked"
else
  echo "FAIL: --release-only — success line does not say the beacons were unchecked:"
  echo "$relonly_out" | sed 's/^/    /'
  fails=$((fails + 1))
fi

RELEASE_ONLY=""

# The flag is accepted after the tag as well as before it.
after_out="$(CHECK_PUBLISHED_ROOT="$behind/local" \
  CHECK_PUBLISHED_API_BASE="file://$behind/api" \
  CHECK_PUBLISHED_RAW_BASE="file://$behind/raw" \
  "$CHECK" v0.2.0 --release-only 2>&1)"
if [ $? -eq 0 ]; then
  echo "ok: --release-only is accepted after the tag"
else
  echo "FAIL: --release-only after the tag — expected exit 0:"
  echo "$after_out" | sed 's/^/    /'
  fails=$((fails + 1))
fi

# An unknown flag must be an error, never mistaken for a tag: silently treating
# --relase-only as the tag would compare it against releases/latest, fail with a
# tag-mismatch message, and send the reader hunting the wrong problem.
typo_out="$(CHECK_PUBLISHED_ROOT="$good/local" \
  CHECK_PUBLISHED_API_BASE="file://$good/api" \
  CHECK_PUBLISHED_RAW_BASE="file://$good/raw" \
  "$CHECK" --relase-only v0.2.0 2>&1)"
if [ $? -ne 0 ] && grep -qi "unknown option" <<<"$typo_out"; then
  echo "ok: an unknown flag is rejected as a flag, not treated as a tag"
else
  echo "FAIL: unknown flag — expected a non-zero exit naming the unknown option:"
  echo "$typo_out" | sed 's/^/    /'
  fails=$((fails + 1))
fi

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
