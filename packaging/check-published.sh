#!/usr/bin/env bash
# Gates a release AFTER it is published, against BOTH of the mechanisms IINA
# uses — because satisfying one does not satisfy the other, and the failure is
# silent either way.
#
#   Install by slug:  api.github.com/repos/<ghRepo>/releases/latest, first asset
#                     ending .iinaplgz.
#   Update check:     raw.githubusercontent.com/<ghRepo>/<branch>/Info.json — the
#                     REPOSITORY ROOT of a branch IINA names itself, never the
#                     release. IINA 1.4.x reads master; 1.5.0 and later read
#                     main. Both are gated, because both are installed.
#
# v0.2.0 is why this exists: it published perfectly, passed every check in the
# chain, and reached nobody, because the manifest was not at the repo root and
# IINA 1.4.4 reports that 404 as "No update found." with no error. IINA 1.5.0
# is why it reads two branches: it moved the beacon to main, where this
# repository had nothing, and a gate that only looked at master stayed green.
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
# the release before the bump lands on master, so that the beacons never
# announce a version whose asset is not up yet — which leaves a deliberate
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

# IINA names the beacon branch itself, and which name depends on the IINA: 1.4.x
# reads master, 1.5.0 and later read main. main is a mirror of master, kept by
# .github/workflows/mirror-main.yml. Each is fetched and judged on its own,
# because each can be wrong while the other is right.
#
# Skipped entirely under --release-only, rather than fetched and ignored: in
# that window the manifests are EXPECTED to disagree with the tag, and a fetch
# whose result is discarded invites someone to later "fix" the discrepancy it
# prints. The empty paths below tell the checker there is nothing to judge.
#
# A miss here is THE silent one, so it is named for what it breaks rather than
# reported as a bare HTTP error.
raw_master=""
raw_main=""
if [ "$RELEASE_ONLY" != 1 ]; then
  raw_master="$TMPD/raw-master.json"
  raw_main="$TMPD/raw-main.json"
  fetch "$RAW_BASE/$ghrepo/master/Info.json" "$raw_master" \
    || fail "IINA 1.4.x's update check reads $RAW_BASE/$ghrepo/master/Info.json and it is not there.
    Existing users on 1.4.x will be told \"No update found.\" no matter how
    correct the release is — that IINA folds a failed fetch and \"no newer
    version\" into one branch. The manifest must be committed at the REPOSITORY
    ROOT of master."
  fetch "$RAW_BASE/$ghrepo/main/Info.json" "$raw_main" \
    || fail "IINA 1.5.0 and later run their update check against $RAW_BASE/$ghrepo/main/Info.json and it is not there.
    Every 1.5 install of this plugin gets \"Error checking for updates.\" — for
    ALL of its plugins, because one failed fetch aborts the whole check. main is
    a mirror of master kept by .github/workflows/mirror-main.yml: confirm its
    run for the latest push to master succeeded, then give
    raw.githubusercontent.com a few minutes of cache before re-running this."
fi

/usr/bin/python3 - "$TAG" "$TMPD/latest.json" "$raw_master" "$raw_main" <<'PY'
import json, sys

tag, latest_path = sys.argv[1], sys.argv[2]
# The update beacons: (branch, the IINA that reads it, path to what was fetched).
# Empty paths are --release-only: the manifests were never fetched, because in
# that window they are expected to disagree with the tag.
beacons = [
    ("master", "IINA 1.4.x", sys.argv[3]),
    ("main", "IINA 1.5.0 and later", sys.argv[4]),
]
release_only = not any(path for _, _, path in beacons)
# main is written by a workflow, not by hand. When it is wrong and master is
# right, no manifest needs editing — the mirror needs re-running — and the
# message has to say so.
MIRROR_HINT = (
    " main is a mirror of master kept by .github/workflows/mirror-main.yml: "
    "confirm its run for the latest push to master succeeded, then give "
    "raw.githubusercontent.com a few minutes of cache before re-running this."
)
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
reported = {}
found = {}
for branch, reader, path in [] if release_only else beacons:
    raw = load(path, f"the manifest on {branch}")
    found[branch] = []

    raw_version = raw.get("version")
    if raw_version != expected_version:
        found[branch].append(
            f"the manifest on {branch} says version {raw_version!r}, but this tag is "
            f"{tag!r} (expected {expected_version!r}). The update check in {reader} "
            f"reads {branch}, not the release: if the bump never reached {branch}, "
            f"those installs are never offered this release."
        )

    gh = raw.get("ghVersion")
    if isinstance(gh, bool) or not isinstance(gh, int):
        found[branch].append(
            f"ghVersion on {branch} must be a JSON integer, not {type(gh).__name__} — "
            f"IINA casts it as? Int, and a wrong type fails the update check for "
            f"every install that reads {branch}."
        )
    else:
        reported[branch] = (raw_version, gh)

# Only when main is wrong ON ITS OWN. If master is wrong too, the bump never
# landed, and pointing at the mirror would send the reader to the wrong place.
if found.get("main") and not found.get("master"):
    found["main"] = [problem + MIRROR_HINT for problem in found["main"]]
for branch_problems in found.values():
    problems.extend(branch_problems)

# The two can name the same version and still differ in the one number IINA
# compares — a ghVersion patched on master by a push the mirror missed.
if len(reported) == 2 and reported["master"][1] != reported["main"][1]:
    problems.append(
        f"master and main disagree on ghVersion ({reported['master'][1]} vs "
        f"{reported['main'][1]}). IINA compares that number and nothing else, so "
        f"installs reading the lower one are not offered what the other announces."
        f"{MIRROR_HINT}"
    )

if problems:
    print("check-published: FAILED —", file=sys.stderr)
    for p in problems:
        print(f"  - {p}", file=sys.stderr)
    sys.exit(1)

# Two distinct messages, because these are two distinct claims and the weaker
# one is read mid-sequence. A bare "OK" here could be mistaken for the full
# gate, which is the one that says the release is actually done.
if release_only:
    print(
        f"check-published: OK (release half only) — {tag} is /releases/latest with "
        f"one .iinaplgz. The manifests on master and main were NOT checked; run "
        f"without --release-only after merging the bump."
    )
else:
    seen = " and ".join(
        f"on {branch} ({reader})" for branch, reader, _ in beacons
    )
    version, gh = reported["master"]
    print(
        f"check-published: OK — {tag} is /releases/latest with one .iinaplgz, and "
        f"the manifests {seen} both report version {version} / ghVersion {gh}, so "
        f"IINA offers the update to existing installs."
    )
PY
