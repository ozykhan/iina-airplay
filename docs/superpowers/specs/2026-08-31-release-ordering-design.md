# Release ordering: closing the update window

**Status:** approved, not yet implemented
**Date:** 2026-08-31
**Applies from:** v0.4.0. v0.3.0 shipped under the old order and is correct now
that both sides agree; nothing needs re-cutting.

## The problem

Cutting v0.3.0 left a window of roughly ten minutes in which IINA offered
existing installs an update and handed them the **previous** release's package.

The cause is ordering. The bump to `Info.json` lands on `master` first (it must,
to be in the tagged tree), and `master` is the update beacon — so the beacon
announces the new version while `releases/latest` still points at the old one.
For the length of the tag build plus however long the draft sits unreviewed,
every update check in the world resolves to a lie.

## What IINA actually compares

From `iina/JavascriptPlugin.swift`, `checkNewVersion()`:

```swift
guard let ghVersion = githubVersion, let ghRepo = githubRepo else { ... }
Just.get("https://raw.githubusercontent.com/\(ghRepo)/master/Info.json", ...) { result in
    if result.ok,
       let json = result.json as? [String: Any],
       let newGHVersion = json["ghVersion"] as? Int,
       let newVersion = json["version"] as? String {
        if newGHVersion > ghVersion { /* offer the update */ }
```

`githubVersion` is read from the **installed package's own** `Info.json`. So the
comparison is:

> `master`'s `ghVersion`  vs.  the `ghVersion` inside the package the user has.

Two consequences, and the whole design rests on them:

1. **The shipped package must carry the same `ghVersion` as `master`.** Bumping
   only `master` — the obvious reading of "let the release workflow update
   `Info.json` as its last step" — is worse than the gap it fixes. Every install
   would keep reporting the old number, `master` would always compare greater,
   and IINA would offer an update on every check forever, re-downloading the
   same package each time.

2. **The skew has a direction, and only one direction is harmful.**

   | State | Existing installs | New installs |
   | --- | --- | --- |
   | `master` ahead of `releases/latest` | offered an update, handed the **old** asset | fine |
   | `releases/latest` ahead of `master` | no update offered yet; they wait | get the new version, correctly told they are current |

## The invariant

> **`master`'s `ghVersion` may never exceed the `ghVersion` inside the package
> at `releases/latest`.**

Everything below follows from it. The bump to `master` stops being step one and
becomes the switch that turns the release on, thrown only once the asset is
provably in place.

## New sequence

1. Branch `release/vX.Y.Z`, bump `version` **and** `ghVersion` in the root
   `Info.json`, open the PR. **Do not merge it.**
2. Warm the ffmpeg cache: `gh workflow run package.yml --ref master`. Unchanged,
   and still worth about eight minutes on the tag build.
3. Tag the **branch head** and push the tag. CI gates, builds, verifies, drafts.
4. Read the `verify-intel` log — the three lines `docs/releasing.md` names.
   Review the draft.
5. Publish. Pre-release unchecked, "Set as the latest release" checked.
6. `./packaging/check-published.sh --release-only vX.Y.Z`.
7. **Merge the PR.** This is the moment the release turns on for existing users.
8. `./packaging/check-published.sh vX.Y.Z`.

Between 5 and 7 the skew is the safe direction. Before 5 there is no skew at
all: `master` and `releases/latest` both still describe the previous release.

## Changes

### `packaging/check-release.sh` — previous-tag lookup

The current lookup is reachability-based:

```bash
prev="$(git -C "$ROOT" describe --tags --abbrev=0 "$TAG^" 2>/dev/null || true)"
```

Under the new order the tag sits on an unmerged branch. Squash-merging that
branch puts an equivalent commit on `master` at a different SHA, so the tag
never enters `master`'s history — and the next release's lookup walks back past
it. Gating v0.4.0 would find v0.2.0, compare `ghVersion 4 > 2`, and pass even if
the bump from 3 had been forgotten. It fails **open**, which is the one
direction this gate must not fail.

Replace it with a version-sorted lookup — the highest tag that exists, rather
than the nearest one reachable:

```bash
prev="$(git -C "$ROOT" tag --sort=-v:refname --list 'v*' | grep -Fxv "$TAG" | head -n1)"
```

- Independent of merge strategy. Squash merges stay.
- Strictly stronger: it compares against the highest release in existence, so a
  ghVersion that does not exceed *every* prior release is rejected.
- Works before the tag exists locally as well as after, since `grep -Fxv`
  removes `$TAG` when present and is a no-op when it is not. The old lookup
  reported "first release; skipping" when run pre-tag — a skip that read as
  reassurance while checking nothing.

Keep: the shallow-clone guard (its rationale shifts from "needs history" to
"needs the full tag list", and its failure mode is the same silent skip), the
dual-path `Info.json` / `plugin/Info.json` read for v0.1.0 and v0.2.0, and every
existing assertion. Note for the implementer: this file follows the bash 3.2
split-declaration convention used across `packaging/`.

**Behaviour change to call out in review:** the gate now fires in cases where it
previously skipped itself. That is the fix, not a side effect.

**Verified against a throwaway repository** (tags v0.1.0, v0.2.0, v0.9.0,
v0.10.0 on `master`; v0.11.0 on a branch, squash-merged):

| Query | Result |
| --- | --- |
| proposed lookup, gating v0.11.0 | `v0.10.0` |
| old lookup from `master`'s tip, as the next release would run it | `v0.10.0` — **v0.11.0 skipped**, the fail-open hole, reproduced |
| sort order | `v0.11.0 v0.10.0 v0.9.0 v0.2.0 v0.1.0` — version-aware, so v0.10.0 outranks v0.9.0 |
| proposed lookup pre-tag, gating v0.12.0 | `v0.11.0` |
| no tags at all | empty, so the "first release" skip is preserved |

### `packaging/check-published.sh` — `--release-only`

Add a flag that asserts the install half alone:

- `/releases/latest` names this tag
- not a draft, not a pre-release
- exactly one `.iinaplgz` asset

It must **skip the `raw.githubusercontent.com` fetch entirely** rather than
fetching and ignoring the result — under the new order that manifest is
*expected* to disagree, and a fetch whose outcome is discarded invites someone
to later "fix" the discrepancy it prints.

The success line must not be mistakable for the full gate:

```
check-published: OK (release half only) — vX.Y.Z is /releases/latest with one
.iinaplgz. master's manifest was NOT checked; run without --release-only after
merging the bump.
```

Argument parsing accepts the flag in either position. An unknown flag is an
error, not a tag.

The default two-sided behaviour is unchanged, including its message and its
exit codes. Step 8 still runs exactly what it runs today.

### Tests

Both scripts have fixture-driven suites that already cover their existing
branches. Add:

`packaging/tests/check-release.test.sh` — `make_repo` builds one commit per
`version:ghVersion:tag[:layout]` tuple, so it needs a way to place a commit on a
branch off `master` rather than on it.

- the version-sorted lookup picks the **highest** tag, not the nearest
  reachable, where the two differ
- a tag on an unmerged branch is gated against the highest tag on `master`, and
  a forgotten bump there **fails** (the regression this change exists to prevent)
- `v0.10.0` sorts above `v0.9.0` — `-v:refname` is version-aware; a lexical sort
  would invert this and silently weaken the gate
- no tags at all still reports "first release" and skips

`packaging/tests/check-published.test.sh` — `make_endpoints` already serves both
endpoints over `file://`.

- `--release-only` passes on fixtures where the full check fails **because the
  raw manifest is stale** — the exact mid-sequence state of step 6
- `--release-only` still fails on a draft, a pre-release, a wrong tag, and on
  zero or two `.iinaplgz` assets
- `--release-only` passes when the raw manifest is **absent entirely**, proving
  the fetch is skipped rather than fetched-and-ignored
- an unknown flag exits non-zero and is not treated as a tag

### Docs

`docs/releasing.md` — the reordered sequence, and a new subsection on why the
skew has a direction, carrying the `checkNewVersion` snippet and the two-row
table above. That the comparison target is the *installed package's* number is
the load-bearing fact of this design and is currently written down nowhere.
Also document tag recovery: a build that fails now leaves a tag on an unmerged
branch, recovered with `git push --delete origin vX.Y.Z`, re-tag, re-push —
strictly cheaper than the old failure mode, where the beacon was already live
and lying.

`.claude/skills/cutting-a-release/SKILL.md` — the new step order, plus a red
flag: *"Merged the bump before publishing the release" → you have re-opened the
window this ordering exists to close.*

`CLAUDE.md` — the install/update bullet gains the comparison target, in one
clause.

## Out of scope

- Automating the merge. It needs a token that bypasses branch protection and
  merges to `master` without review, to save one command.
- Any change to `package.yml`. It already drafts and stops, which is exactly
  right; the draft state is what makes the safe ordering possible at all.
- Re-cutting v0.3.0.

## Success criteria

- `make test` passes, including the new cases.
- A dry run of the new lookup against the real repository reports v0.3.0 as the
  previous tag when gating v0.4.0, from a branch, after a squash merge.
- Cutting v0.4.0 by the new sequence leaves no interval in which
  `raw.githubusercontent.com/ozykhan/iina-airplay/master/Info.json` reports a
  `ghVersion` higher than the one inside the package at `releases/latest`.
