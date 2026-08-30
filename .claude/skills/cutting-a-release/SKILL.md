---
name: cutting-a-release
description: Use when cutting, tagging, publishing or verifying a release of this plugin, when bumping version or ghVersion, or when an update is not reaching existing IINA installs.
---

# Cutting a release

`docs/releasing.md` is the reference and the only place the reasoning lives.
This skill owns sequence and gates. Read the doc; nothing here restates it.

## The rule

**A release is not done when CI is green. It is done when
`./packaging/check-published.sh <tag>` passes** — the full two-sided form, run
after the bump has been merged. `--release-only` is a mid-sequence gate, not
that one.

IINA installs and updates through two different mechanisms, and every failure
mode in both is silent: the release page renders correctly, every job is a green
tick, and the release reaches nobody. `v0.2.0` shipped exactly that way.

## Sequence

**The bump lands on `master` LAST.** `master` is the update beacon, so merging
it before the asset is published tells every install an update exists and then
hands it the previous release. Publish first; merge last.

1. Bump `version` **and** `ghVersion` in `Info.json` — the one at the repository
   root — on a `release/v<version>` branch. Open the PR. **Do not merge it.**
2. **Warm the cache before tagging:** `gh workflow run package.yml --ref master`,
   and let it finish. Tag refs cannot read caches written on PRs or other tags.
   Skipping this costs about 8 minutes on the tag build.
3. `./packaging/check-release.sh v<version>` locally, then tag the **branch
   head** and push the tag. Not `master` — it does not carry the bump yet.
4. Read the `verify-intel` **log**, not its checkmark — the three lines the doc
   names.
5. Review the draft, then publish: **Set as a pre-release** unchecked, **Set as
   the latest release** checked.
6. `./packaging/check-published.sh --release-only v<version>`.
7. **Merge the PR.** The only irreversible step, which is why step 6 gates it.
8. `./packaging/check-published.sh v<version>`.

## Red flags — you are about to ship to nobody

- "CI is green, so the release is done." → step 8 has not run.
- **Merged the bump before publishing.** → you have re-opened the window this
  ordering exists to close: `master` now advertises a version whose asset is not
  up, so every update check in the gap hands out the *previous* release. Publish
  and finish the sequence; do not merge anything else meanwhile.
- "I'll merge the PR first so I don't forget." → that is the failure above.
- "The release page looks right." → that is how every one of these failures looks.
  Step 6 exists so the merge is gated by a command instead of by that glance.
- `version` bumped but not `ghVersion`. Existing installs are never offered it.
- The bump never landed on `master`. The update check reads the **branch**, not
  the release, so a perfect release page changes nothing.
- Committing `plugin/Info.json`. It is a gitignored `make dev` symlink, and
  `raw.githubusercontent.com` serves a symlink's target path as text, not JSON.

## When IINA still says "No update found."

That is not a release problem and re-cutting the release will not fix it. Check
the update beacon directly — this is the URL IINA actually fetches:

```sh
curl -fsS https://raw.githubusercontent.com/<ghRepo>/master/Info.json
```
