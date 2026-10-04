# Releasing

Users install from IINA's plugin store (IINA 1.5 and later, where the plugin is
listed under Community Plugins) or by typing `ozykhan/iina-airplay` into IINA
(1.4). Both pull the `.iinaplgz` from the repository's **latest GitHub
release**. A release without that asset is a broken install path, so every
release carries it — CI refuses to publish one that does not.

## Cutting a release

**The order below is load-bearing.** The bump to `master` comes *last*, after
the release is published — see "The skew has a direction" below for why. Do not
merge the bump early to get it out of the way; that is the one mistake this
sequence exists to prevent.

1. **Bump both numbers in `Info.json` — the one at the repository root — on a
   release branch, and open the PR without merging it.**

   - `version` — the semver string, e.g. `0.2.0`. The tag must be `v` + this.
   - `ghVersion` — an **Int**, a monotonic counter. **Not** the semver string.

   `ghVersion` is the step most likely to be forgotten and the only one whose
   omission is silent: the release ships fine, installs fine for new users, and
   simply never reaches anyone who already has the plugin, because IINA's update
   check compares this number. `packaging/check-release.sh` refuses a tag that
   does not increase it — but only once a previous tag exists.

   The manifest's **location** is load-bearing for the same reason, and cost a
   release to learn — see "Two mechanisms" below.

   ```sh
   git checkout -b release/v0.3.0
   # edit Info.json, commit
   gh pr create --base master
   ```

2. **Warm the ffmpeg cache.** `gh workflow run package.yml --ref master`, and
   let it finish. Actions caches are scoped by ref: only ones written on the
   default branch are readable from a tag, so a PR run warms nothing. Skipping
   this costs about eight minutes on the tag build. See "Rebuilding without
   releasing" below.

3. **Tag the branch head and push the tag.** Not `master` — `master` does not
   carry the bump yet, and will not until step 9.

   ```sh
   ./packaging/check-release.sh v0.3.0   # seconds, before the build
   git tag v0.3.0
   git push origin v0.3.0
   ```

4. **Watch the chain.** `gh run watch`. `build` gates the tag first, so a
   mismatch fails in seconds. On GitHub's `macos-15` runner a cold ffmpeg
   cache costs about 5 minutes to build and about 9 minutes for the whole
   `build` → `verify-intel` chain; warm, `build` finishes in under 2 minutes.
   A local `make pack` is considerably slower on a developer machine — see
   `README.md`.

5. **Read the `verify-intel` log**, not just its checkmark. Three lines are the
   point of the job:

   - `runner architecture: x86_64`
   - `verify: note — the native slice is x86_64, so its assertions run natively`
   - `test-package: driving iina-airplay.iinaplgz through the helper suite on x86_64`

6. **Review the draft release.** Confirm exactly one `.iinaplgz` asset, its
   `.sha256` sidecar, and notes naming the FFmpeg version, upstream source URL
   and source SHA-256 — that last part is the LGPL obligation, not decoration.

7. **Publish it.** The publish dialog offers two checkboxes — leave **Set as
   a pre-release** unchecked, and confirm **Set as the latest release** is
   checked. Either one wrong and `api.github.com/.../releases/latest` skips
   this release exactly as it skips a draft: every check stays green, the
   release page looks fine, and nothing reaches users.

8. **Prove the release half is live, before throwing the switch.**

   ```sh
   ./packaging/check-published.sh --release-only v0.3.0
   ```

   Asserts `/releases/latest` names this tag, is neither a draft nor a
   pre-release, and carries exactly one `.iinaplgz`. It does not look at
   `master` at all — at this point `master` is *supposed* to disagree.

9. **Merge the PR.** This is the moment the release turns on for every existing
   install, and it is the only irreversible step in the sequence — which is why
   step 8 gates it with a command rather than a glance at the release page.

   The merge is a push to `master`, which starts `mirror-main`: the workflow
   that carries the bump on to `main`, the branch IINA 1.5 reads. It takes
   seconds; let it finish before step 10.

   ```sh
   gh run list --workflow mirror-main.yml --limit 1   # expect: completed, success
   ```

10. **Gate the published result.** Both mechanisms, one command:

    ```sh
    ./packaging/check-published.sh v0.3.0
    ```

    `check-release.sh` gates the tag before the build; this gates what actually
    shipped. It asserts `/releases/latest` names this tag with exactly one
    `.iinaplgz`, **and** that the manifest is readable at the repo root of
    **both** beacon branches — `master` for IINA 1.4, `main` for IINA 1.5 —
    with a matching version and the same Int `ghVersion`. That is the half
    nothing checked before `v0.2.0` shipped to nobody, and the branch nothing
    checked when IINA 1.5.0 moved the beacon. See "Two mechanisms" below.

    If it fails on `main` alone, the mirror has not caught up: confirm the
    `mirror-main` run from step 9 succeeded, give `raw.githubusercontent.com`
    its five minutes of cache, and run the gate again.

    **A release is not done until this passes.** Not when CI is green, not when
    the release page looks right.

11. **Install it the way a stranger would.** On IINA 1.5: Settings → Plugins →
    Get Plugins… → AirPlay → Install. On 1.4: Settings → Plugins → Install →
    `ozykhan/iina-airplay`. Not the local-package path — the point is
    to exercise the download-from-release path, which is the one thing local
    packaging can never test. Cast one real file, then confirm nothing picked up
    quarantine:

    ```sh
    xattr -r ~/Library/Application\ Support/com.colliderli.iina/plugins/*/bin/
    ```

    Expect `com.apple.provenance` at most, and never `com.apple.quarantine`.

### If the build fails after the tag is pushed

The tag is on an unmerged branch, so nothing user-facing has moved: `master`
still describes the previous release and `releases/latest` still serves it.
Delete the tag, fix the branch, tag again.

```sh
git push --delete origin v0.3.0 && git tag -d v0.3.0
```

This is strictly cheaper than the failure it replaces. Under the old order the
bump was already on `master`, so a failed build left the beacon announcing a
version whose asset did not exist.

## Two mechanisms, two URLs

Installing and updating are separate paths in IINA, and satisfying one does not
satisfy the other. Both must be right or the release reaches nobody.

| | What IINA fetches | Satisfied by |
| --- | --- | --- |
| **Install**, from the store or by slug | `api.github.com/repos/<ghRepo>/releases/latest`, first asset ending `.iinaplgz` | the published release + its asset |
| **Update check**, IINA 1.4.x | `raw.githubusercontent.com/<ghRepo>/master/Info.json` | `Info.json` **committed at the repo root of `master`** |
| **Update check**, IINA 1.5.0 and later | `raw.githubusercontent.com/<ghRepo>/main/Info.json` | the same file on `main`, which mirrors `master` |
| **Update download** | back to `releases/latest` | the same asset |

The update check never looks at releases. It reads `ghVersion` out of the
manifest sitting at the **root of a branch IINA names itself** — `master` or
`main`, depending on the IINA — and only if that number is higher does it then
go fetch the `.iinaplgz`.

This is why `Info.json` lives at the repository root rather than under
`plugin/`, and why `packaging/pack.sh` copies it from there into the package.
While it lived under `plugin/` that URL 404'd, and IINA 1.4.4 folds a failed
fetch and "no newer version" into the same branch
(`JavascriptPlugin.swift`, `checkForUpdates`) — so `v0.2.0` published perfectly,
passed every check, and still reported **"No update found."** to anyone running
`v0.1.0`. Nothing in the release was wrong; the manifest was simply not where
IINA looks.

Two consequences worth keeping in mind:

- **The update beacon is branch state, not release state.** A manifest fix
  reaches existing users as soon as it lands on `master` — no new tag, no
  rebuild — and reaches 1.5 installs seconds later, when the mirror carries it
  to `main`. `master` is protected, so "lands on `master`" means a one-commit
  PR that passes CI, not a direct push.
- **`master` must carry the bumped `ghVersion`.** Tagging a release whose
  manifest never lands on `master` leaves the update check reading the old
  number, however correct the release page looks.

### Two branches, because two IINAs

The branch name in that URL is hardcoded in IINA, and IINA 1.5.0 changed it:
1.4.x reads `master`, 1.5.0 and later read `main` (`JavascriptPlugin.swift`,
`checkNewVersion`; the 1.5.0 release notes say "Use main branch to check for
updates"). Both generations are installed, so both URLs have to answer, with
the same manifest.

So `master` is the trunk, exactly as before, and **`main` is a mirror of it**.
`.github/workflows/mirror-main.yml` fast-forwards `main` on every push to
`master`. Nothing is ever committed to `main` directly, and a PR opened against
it is a mistake.

1.5.0 also changed what a missing manifest looks like, and it is no longer
quiet. A failed fetch is thrown instead of folded into "no update", and the
Settings page checks every installed plugin in one loop that the first throw
aborts. This repository had no `main` when 1.5.0 shipped on 2026-10-03, and
until one was pushed the next day every 1.5 user who had the plugin was shown
**"Error checking for updates."** for *all* of their plugins — plus a download
error when updating this one on its own.

Renaming `master` to `main` is not the fix, even though GitHub would make it
nearly work. `raw.githubusercontent.com` answers a `master` URL from the default
branch when a repository has no `master` at all, so `main`-only plugins satisfy
both IINAs by accident — which is why the move went unnoticed upstream: on
2026-10-04, this plugin and one other were the only GitHub-hosted entries in
IINA's list answering on `master` but not on `main`. But that fallback is
undocumented and runs one way only: a `main` URL never falls back to `master`.
A rename would stake every 1.4 install on it. Two real branches depend on
nothing.

What can go wrong, and what it looks like:

- **The mirror run fails.** `main` keeps the previous manifest, so 1.5 installs
  are not offered the release. `check-published.sh` reads both branches and
  fails on the stale one. Re-run the mirror: `gh workflow run mirror-main.yml`.
- **`main` has diverged** — someone committed to it. The workflow refuses to
  force-push and fails. Once the stray commit is understood:
  `git push --force origin origin/master:main`.
- **`main` is deleted.** 1.5 installs are back to "Error checking for updates."
  `gh workflow run mirror-main.yml` recreates it.

### The skew has a direction

The number IINA compares `master`'s `ghVersion` against is **the installed
package's own** `ghVersion`, read from the manifest inside the `.iinaplgz` the
user already has. In 1.4.4 (`JavascriptPlugin.swift`, `checkForUpdates`):

```swift
if let ghVersion = githubVersion, let ghRepo = githubRepo {
  Just.get("https://raw.githubusercontent.com/\(ghRepo)/master/Info.json", ...) { result in
    if let json = result.json as? [String: Any],
       let newGHVersion = json["ghVersion"] as? Int,
       let newVersion = json["version"] as? String,
       newGHVersion > ghVersion {          // ghVersion == githubVersion, this install's own
      handler(newVersion)
```

Two things follow.

**The shipped package must carry the same `ghVersion` as `master`.** Bumping
only `master` — letting the release workflow rewrite the manifest as a last
step, say — is worse than any window it closes: every install would keep
reporting the old number, `master` would compare greater forever, and IINA
would offer an update on every check that re-downloads the same package.

**So the bump must be in the tagged tree, and only its arrival on `master` can
move.** That arrival is the switch, and the skew it creates has a safe
direction and a dangerous one:

| State | Existing installs | New installs |
| --- | --- | --- |
| `master` ahead of `releases/latest` | offered an update, handed the **old** asset | fine |
| `releases/latest` ahead of `master` | no update offered yet; they wait | get the new version, correctly told they are current |

Publishing before merging keeps the skew in the bottom row for the few minutes
it exists, and in no row at all the rest of the time. Cutting `v0.3.0` the other
way round left about ten minutes of the top row. `main` trails `master` by the
seconds the mirror takes, which only keeps 1.5 installs in the bottom row a
little longer — the safe direction.

> IINA 1.5.0 replaced this with `checkNewVersion()`. The comparison itself is
> unchanged; what moved is the branch it reads (`main`, not `master`) and the
> failure mode (a failed fetch is thrown rather than folded into `nil`). See
> "Two branches, because two IINAs" above.

`plugin/Info.json` is a gitignored symlink created by `make dev`, because a
plugin directory must carry its own manifest for IINA to load it. Never commit
it: `raw.githubusercontent.com` serves a symlink's target path as text, so the
update check would parse `../Info.json` instead of JSON.

## If the `release` job fails

Its first-ever run is the real `v0.1.0` tag — neither a PR nor a
`workflow_dispatch` run ever reaches it, so a failure here is not a surprise,
it is simply the first time this code has run at all.

The `.iinaplgz` is safe regardless: `build` uploads it as a workflow artifact
before `release` ever starts, and that artifact survives `release` failing.

Try re-running just the failed job first: `gh run rerun <run-id> --failed`
(or the Actions UI's "Re-run failed jobs"). `build` and `verify-intel`
already succeeded, so this replays only `release`.

If that does not resolve it, publish by hand from the artifact:

```sh
gh run download <run-id> --name iina-airplay-package --dir dist
./packaging/release-notes.sh dist/iina-airplay.iinaplgz > notes.md
gh release create v0.1.0 \
  --draft \
  --title v0.1.0 \
  --notes-file notes.md \
  --repo ozykhan/iina-airplay \
  dist/iina-airplay.iinaplgz \
  dist/iina-airplay.iinaplgz.sha256
```

That lands in the same draft state the automated job aims for — pick up at
step 5 above.

Re-running the workflow on a tag whose release already exists fails with
"release already exists". That is expected, not a new problem: `release`
makes no attempt to replace a prior attempt. Either delete the existing
release (or draft) first, or attach the missing asset to it with
`gh release upload`.

## Rebuilding without releasing

`workflow_dispatch` on `package.yml` rehearses the chain and warms the ffmpeg
cache before a tag push: it runs `build` and `verify-intel` and creates no
release, leaving the `.iinaplgz` as a workflow artifact.

**Dispatch it on `master`, and do not expect a pull request to have warmed
anything.** GitHub scopes Actions caches by ref: a cache written on
`refs/pull/N/merge` is invisible to `refs/tags/v*`, and only caches written on
the **default branch** are readable from every other ref. This is not a
theory — the `v0.1.0` tag build logged `Cache not found for input keys:
ffmpeg-universal-macos15-…` and paid the full ~6-minute ffmpeg build, even
though two PR runs had already built and cached that exact key minutes
earlier. A dispatch on `master` writes a cache every later tag can read; a PR
run does not.

The `release` job never runs on a dispatch, so preview its notes by hand against
the artifact:

```sh
gh run download <run-id> --name iina-airplay-package --dir /tmp/dryrun
./packaging/release-notes.sh /tmp/dryrun/iina-airplay.iinaplgz
```

## The landing page is not a release step

`https://ozykhan.github.io/iina-airplay/` is GitHub Pages serving the `docs/`
folder of `master` (`docs/index.html`, plus `docs/.nojekyll` so Jekyll never
tries to build the markdown under `docs/`, which contains `{{ }}` from Actions
YAML, this file included). It deploys on every push to `master` and carries no
version number, so nothing in the release choreography touches it and a fix to
the page ships like a manifest fix: merge to `master`, wait a minute, reload.
