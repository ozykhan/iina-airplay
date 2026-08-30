# Releasing

Users install by typing `ozykhan/iina-airplay` into IINA, which pulls the
`.iinaplgz` from the repository's **latest GitHub release**. A release without
that asset is a broken install path, so every release carries it — CI refuses to
publish one that does not.

## Cutting a release

1. **Bump both numbers in `plugin/Info.json`.**

   - `version` — the semver string, e.g. `0.2.0`. The tag must be `v` + this.
   - `ghVersion` — an **Int**, a monotonic counter. **Not** the semver string.

   `ghVersion` is the step most likely to be forgotten and the only one whose
   omission is silent: the release ships fine, installs fine for new users, and
   simply never reaches anyone who already has the plugin, because IINA's update
   check compares this number. `packaging/check-release.sh` refuses a tag that
   does not increase it — but only once a previous tag exists.

2. **Commit, then tag and push.**

   ```sh
   git tag v0.2.0
   git push origin master v0.2.0
   ```

3. **Watch the chain.** `gh run watch`. `build` gates the tag first, so a
   mismatch fails in seconds. On GitHub's `macos-15` runner a cold ffmpeg
   cache costs about 5 minutes to build and about 9 minutes for the whole
   `build` → `verify-intel` chain; warm, `build` finishes in under 2 minutes.
   A local `make pack` is considerably slower on a developer machine — see
   `README.md`.

4. **Read the `verify-intel` log**, not just its checkmark. Three lines are the
   point of the job:

   - `runner architecture: x86_64`
   - `verify: note — the native slice is x86_64, so its assertions run natively`
   - `test-package: driving iina-airplay.iinaplgz through the helper suite on x86_64`

5. **Review the draft release.** Confirm exactly one `.iinaplgz` asset, its
   `.sha256` sidecar, and notes naming the FFmpeg version, upstream source URL
   and source SHA-256 — that last part is the LGPL obligation, not decoration.

6. **Publish it.** The publish dialog offers two checkboxes — leave **Set as
   a pre-release** unchecked, and confirm **Set as the latest release** is
   checked. Either one wrong and `api.github.com/.../releases/latest` skips
   this release exactly as it skips a draft: every check stays green, the
   release page looks fine, and nothing reaches users.

7. **Install it the way a stranger would.** IINA → Settings → Plugins →
   Install → `ozykhan/iina-airplay`. Not the local-package path — the point is
   to exercise the download-from-release path, which is the one thing local
   packaging can never test. Cast one real file, then confirm nothing picked up
   quarantine:

   ```sh
   xattr -r ~/Library/Application\ Support/com.colliderli.iina/plugins/*/bin/
   ```

   Expect `com.apple.provenance` at most, and never `com.apple.quarantine`.

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
