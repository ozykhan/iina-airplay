# Listing the plugin in IINA's Community Plugins list

Date: 2026-09-12

## Goal

Get `ozykhan/iina-airplay` into IINA's own plugin list, so it shows up in the
IINA README and in the in-app plugin browser that reads `plugins.json`. An IINA
maintainer asked for a pull request modelled on iina/iina#6304.

## What the upstream PR contains

Two files on the `develop` branch of `iina/iina`, both kept in alphabetical
order. "AirPlay" sorts before "Anime4K", so both entries go first.

`README.md`, under `### Community Plugins`, before the Anime4K line:

```
- **[AirPlay](https://github.com/ozykhan/iina-airplay)** (`ozykhan/iina-airplay`) - Cast the current file to an Apple TV over AirPlay; IINA stays the remote.
```

`plugins.json`, first object in the array:

```json
{
  "name": "AirPlay",
  "url": "https://github.com/ozykhan/iina-airplay",
  "desc": "Cast the current file to an Apple TV over AirPlay; IINA stays the remote.",
  "id": "dev.faruk.iina-airplay"
}
```

The `id` must equal `identifier` in this repo's `Info.json`. The `name` matches
`Info.json`'s `name`.

## PR body

Follows IINA's template exactly as #6304 did:

- CONTRIBUTING.md read: checked.
- Related issue: none. Design proposal: not applicable.
- AI disclosure: checked. The plugin was built with AI assistance and this PR
  text was drafted with AI; the list entries were checked by hand.
- Description: one paragraph naming the plugin, its install slug, that v0.3.1
  ships an `.iinaplgz` asset, that `Info.json` carries `ghRepo` and `ghVersion`,
  and that the list `id` matches the identifier.

## Mechanics

1. Fork `iina/iina` under `ozykhan` (no fork exists yet).
2. Branch `add-airplay-plugin` from upstream `develop`.
3. Apply the two edits, verify `plugins.json` still parses and both lists stay
   sorted, commit, push to the fork.
4. Open the PR against `iina/iina:develop`.

Nothing in this repo changes except this spec.

## Out of scope

- Any change to the plugin itself or its release.
- Mentioning macOS / ffmpeg requirements in the list entry; no other entry does.
