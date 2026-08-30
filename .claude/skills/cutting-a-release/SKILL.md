---
name: cutting-a-release
description: Use when cutting, tagging, publishing or verifying a release of this plugin, when bumping version or ghVersion, or when an update is not reaching existing IINA installs.
---

# Cutting a release

`docs/releasing.md` is the reference and the only place the reasoning lives.
This skill owns sequence and gates. Read the doc; nothing here restates it.

## The rule

**A release is not done when CI is green. It is done when
`./packaging/check-published.sh <tag>` passes.**

IINA installs and updates through two different mechanisms, and every failure
mode in both is silent: the release page renders correctly, every job is a green
tick, and the release reaches nobody. `v0.2.0` shipped exactly that way.

## Sequence

1. Bump `version` **and** `ghVersion` in `Info.json` — the one at the repository
   root. Commit it to `master`.
2. **Warm the cache before tagging:** `gh workflow run package.yml --ref master`,
   and let it finish. Tag refs cannot read caches written on PRs or other tags.
   Skipping this costs about 8 minutes on the tag build.
3. `./packaging/check-release.sh v<version>` locally, then push the tag.
4. Read the `verify-intel` **log**, not its checkmark — the three lines the doc
   names.
5. Review the draft, then publish: **Set as a pre-release** unchecked, **Set as
   the latest release** checked.
6. `./packaging/check-published.sh v<version>`.

## Red flags — you are about to ship to nobody

- "CI is green, so the release is done." → step 6 has not run.
- "The release page looks right." → that is how every one of these failures looks.
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
