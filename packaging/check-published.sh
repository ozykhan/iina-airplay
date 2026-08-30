#!/usr/bin/env bash
# Gates a release AFTER it is published, against BOTH of the mechanisms IINA
# uses — because satisfying one does not satisfy the other, and the failure is
# silent either way.
#
#   Install by slug:  api.github.com/repos/<ghRepo>/releases/latest, first asset
#                     ending .iinaplgz.
#   Update check:     raw.githubusercontent.com/<ghRepo>/master/Info.json — the
#                     REPOSITORY ROOT of master, never the release.
#
# v0.2.0 is why this exists: it published perfectly, passed every check in the
# chain, and reached nobody, because the manifest was not at the repo root and
# IINA 1.4.4 reports that 404 as "No update found." with no error.
# packaging/check-release.sh gates the tag BEFORE the build; this gates the
# published result after. See docs/releasing.md.
set -uo pipefail

# Overridable so packaging/tests/check-published.test.sh can point the gate at
# fixture endpoints instead of the network. Nothing outside the tests should
# set them.
ROOT="${CHECK_PUBLISHED_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
API_BASE="${CHECK_PUBLISHED_API_BASE:-https://api.github.com}"
RAW_BASE="${CHECK_PUBLISHED_RAW_BASE:-https://raw.githubusercontent.com}"
INFO="$ROOT/Info.json"

# --release-only gates the INSTALL half alone. The release sequence publishes
# the release before the bump lands on master, so that master's beacon never
# announces a version whose asset is not up yet — which leaves a deliberate
# window in which the update half is SUPPOSED to disagree. This flag is what
# gates the irreversible step in that window: merging the bump. Run the gate
# without it afterwards; that is still the check that says the release is done.
RELEASE_ONLY=0
TAG=""
while [ $# -gt 0 ]; do
  case "$1" in
    --release-only) RELEASE_ONLY=1 ;;
    -*) echo "check-published: unknown option $1" >&2; exit 2 ;;
    *)
      [ -z "$TAG" ] || { echo "check-published: unexpected extra argument $1" >&2; exit 2; }
      TAG="$1"
      ;;
  esac
  shift
done
[ -n "$TAG" ] || { echo "check-published: usage: check-published.sh [--release-only] <tag>" >&2; exit 2; }

fail() { echo "check-published: FAILED — $*" >&2; exit 1; }

[ -f "$INFO" ] || fail "$INFO not found"

# The repo slug comes from the manifest rather than a hardcoded string, so a
# fork is gated against its own endpoints.
ghrepo="$(/usr/bin/python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("ghRepo",""))' "$INFO" 2>/dev/null)"
[ -n "$ghrepo" ] || fail "Info.json has no ghRepo"

TMPD="$(mktemp -d)"
trap 'rm -rf "$TMPD"' EXIT

# Judged by exit status, not %{http_code}: -f makes curl fail on 4xx/5xx, and
# the same code path then works against the file:// fixtures the tests use,
# where a status is never reported at all.
fetch() { curl -fsSL "$1" > "$2" 2>/dev/null; }

fetch "$API_BASE/repos/$ghrepo/releases/latest" "$TMPD/latest.json" \
  || fail "cannot read releases/latest for $ghrepo — is anything published?"

# Skipped entirely under --release-only, rather than fetched and ignored: in
# that window the manifest on master is EXPECTED to disagree, and a fetch whose
# result is discarded invites someone to later "fix" the discrepancy it prints.
# The empty path below tells the checker there is nothing to judge.
#
# A miss here is THE silent one, so it is named for what it breaks rather than
# reported as a bare HTTP error.
if [ "$RELEASE_ONLY" = 1 ]; then
  raw_arg=""
elif ! fetch "$RAW_BASE/$ghrepo/master/Info.json" "$TMPD/raw.json"; then
  fail "IINA's update check reads $RAW_BASE/$ghrepo/master/Info.json and it is not there.
    Existing users will be told \"No update found.\" no matter how correct the
    release is — IINA folds a failed fetch and \"no newer version\" into one
    branch. The manifest must be committed at the REPOSITORY ROOT of master."
else
  raw_arg="$TMPD/raw.json"
fi

/usr/bin/python3 - "$TAG" "$TMPD/latest.json" "$raw_arg" <<'PY'
import json, sys

tag, latest_path, raw_path = sys.argv[1], sys.argv[2], sys.argv[3]
expected_version = tag[1:] if tag.startswith("v") else tag
problems = []


def load(path, what):
    try:
        with open(path) as fh:
            return json.load(fh)
    except Exception as e:
        print(f"check-published: FAILED — {what} does not parse: {e}", file=sys.stderr)
        sys.exit(1)


latest = load(latest_path, "releases/latest")
# An empty raw_path is --release-only: the manifest on master was never
# fetched, because in that window it is expected to disagree.
raw = load(raw_path, "the manifest on master") if raw_path else None

# --- the install mechanism ---------------------------------------------------
if latest.get("tag_name") != tag:
    problems.append(
        f"releases/latest is {latest.get('tag_name')!r}, not {tag!r}. IINA installs "
        f"whatever /releases/latest names, so this tag is not what a new user gets. "
        f"A draft, a pre-release, or an unchecked \"Set as the latest release\" all "
        f"look like this."
    )
if latest.get("draft"):
    problems.append("the release is still a draft; /releases/latest excludes drafts")
if latest.get("prerelease"):
    problems.append(
        "the release is marked as a pre-release, so /releases/latest skips it"
    )

assets = [a.get("name", "") for a in latest.get("assets") or []]
plgz = [n for n in assets if n.endswith(".iinaplgz")]
if len(plgz) != 1:
    problems.append(
        f"expected exactly one .iinaplgz asset, found {len(plgz)} ({assets or 'no assets'}). "
        f"IINA takes the FIRST asset whose name ends in .iinaplgz, so more than one is "
        f"ambiguous and none is a broken install path."
    )

# --- the update mechanism ----------------------------------------------------
raw_version = raw.get("version") if raw is not None else None
if raw is None:
    pass
elif raw_version != expected_version:
    problems.append(
        f"the manifest on master says version {raw_version!r}, but this tag is {tag!r} "
        f"(expected {expected_version!r}). IINA's update check reads master, not the "
        f"release: if the bump never landed on master, existing users are never offered "
        f"this release."
    )

gh = raw.get("ghVersion") if raw is not None else None
if raw is not None and (isinstance(gh, bool) or not isinstance(gh, int)):
    problems.append(
        f"ghVersion on master must be a JSON integer, not {type(gh).__name__} — "
        f"IINA casts it as? Int, and a wrong type silently disables update checks"
    )

if problems:
    print("check-published: FAILED —", file=sys.stderr)
    for p in problems:
        print(f"  - {p}", file=sys.stderr)
    sys.exit(1)

# Two distinct messages, because these are two distinct claims and the weaker
# one is read mid-sequence. A bare "OK" here could be mistaken for the full
# gate, which is the one that says the release is actually done.
if raw is None:
    print(
        f"check-published: OK (release half only) — {tag} is /releases/latest with "
        f"one .iinaplgz. master's manifest was NOT checked; run without "
        f"--release-only after merging the bump."
    )
else:
    print(
        f"check-published: OK — {tag} is /releases/latest with one .iinaplgz, and "
        f"master's manifest reports version {raw_version} / ghVersion {gh}, so IINA "
        f"offers the update to existing installs."
    )
PY
