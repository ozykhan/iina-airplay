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

6. **Publish it.** Drafts are invisible to `api.github.com/.../releases/latest`,
   so nothing reaches users until this click.

7. **Install it the way a stranger would.** IINA → Settings → Plugins →
   Install → `ozykhan/iina-airplay`. Not the local-package path — the point is
   to exercise the download-from-release path, which is the one thing local
   packaging can never test. Cast one real file, then confirm nothing picked up
   quarantine:

   ```sh
   xattr -r ~/Library/Application\ Support/com.colliderli.iina/plugins/*/bin/
   ```

   Expect `com.apple.provenance` at most, and never `com.apple.quarantine`.

## Rebuilding without releasing

`workflow_dispatch` on `package.yml` is the intended way to rehearse the chain
and warm the ffmpeg cache before a tag push: it runs `build` and
`verify-intel` and creates no release, leaving the `.iinaplgz` as a workflow
artifact. Dispatching a workflow requires it to exist on the default branch,
so this becomes available once `package.yml` merges to `master` — it has not
been exercised yet; the two runs measured so far were both `pull_request`
runs on PR #1.

The `release` job never runs on a dispatch, so preview its notes by hand against
the artifact:

```sh
gh run download <run-id> --name iina-airplay-package --dir /tmp/dryrun
./packaging/release-notes.sh /tmp/dryrun/iina-airplay.iinaplgz
```
