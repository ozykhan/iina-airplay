# Sidebar redesign: one glass card

**Date:** 2026-08-30
**Status:** approved by user (study A, light mode support, accent reserved for the affirmative action)

## Problem

The cast panel looks unfinished, and the reasons are specific:

- **Two competing primaries.** `Send to TV` and `Stop casting` are both
  full-width `#2f6fed` slabs, so nothing indicates which one is the action.
- **Hardcoded web blue.** `#2f6fed` ignores the macOS accent, and the page
  hardcodes dark text (`#e8e8ee`) on a transparent body, so it washes out
  when IINA runs in light appearance.
- **The status line truncates.** "Ready — click Send to TV." runs out of room
  in a narrow sidebar; it is a label doing a button's job.
- **The panel is all chrome.** Nothing shows elapsed time, and subtitle status
  exists only as a one-shot OSD flash (`main.js:358`) that vanishes before it
  can be read.

## Constraint: the glass is painted, not sampled

`backdrop-filter` inside a `WKWebView` blurs only the page's own compositing
tree. Behind the page's transparent `body` sits IINA's native sidebar
material, which WebKit cannot reach. Depth therefore comes from layered
translucent fills, a specular top edge, and an inner hairline. The look holds;
it does not shift with the video behind it. Do not spend effort trying to make
real vibrancy work — it cannot.

## Constraint: no AirPlay device name

WebKit exposes only the boolean `webkitCurrentPlaybackTargetIsWireless`. There
is no API for the selected target's name in a `WKWebView`. "Playing on TV" is
the ceiling; "Playing on Bedroom" is not achievable.

## Design: one glass card

A single `.glass` container, 14px padding, 16px radius, **fluid width** — the
IINA sidebar is user-resizable, so nothing is pinned to a fixed pixel width.

Top to bottom:

1. Header row — AirPlay glyph, `AIRPLAY` label, state dot
2. Headline plus a secondary sub line
3. Progress track, with an elapsed / duration row beneath it
4. Action stack — one primary, one quiet secondary

Every text line takes `text-overflow: ellipsis`. The times row uses
`font-variant-numeric: tabular-nums` so digits do not jitter as they tick.

### Tokens

Six tokens, defined for both appearances, switched on `prefers-color-scheme`
with the light palette on bare `:root`.

| Token | Dark | Light |
| --- | --- | --- |
| `--glass-fill` | white 13% → 3.5% gradient | black 6% → 2% |
| `--glass-edge` | white 10% | black 8% |
| `--glass-spec` | white 22% inset top | white 70% inset top |
| `--ink` / `--ink-2` | `#F2F3F5` / white 58% | `#1C1C1E` / black 55% |
| `--accent` | `#0A84FF` | `#007AFF` |
| `--warn` | `#F0C169` | `#8A5B06` |

### Accent discipline

**Accent is reserved for the affirmative action** — `Start casting`,
`Send to TV`, `Try again`. While casting to a TV, `Stop casting` is a neutral
glass button, not a blue one: a teardown should not be the loudest thing on
screen. The accent stays present in the progress bar.

### State to UI

| Phase | Dot | Headline | Sub | Bar | Primary | Secondary |
| --- | --- | --- | --- | --- | --- | --- |
| `idle` | none | Not casting | subtitle label | hidden | Start casting (accent) | none |
| `starting` | amber | Packaging | subtitle label | `pct` | Send to TV (disabled) | Cancel |
| `ready`/`packaged`, no devices | none | Ready to send | Waiting for AirPlay devices | full, neutral | Send to TV (disabled) | Stop casting |
| `ready`/`packaged` | none | Ready to send | subtitle label | full, neutral | Send to TV (accent) | Stop casting |
| on TV (`onTV` true) | green | Playing on TV | subtitle label | elapsed / duration | Stop casting (neutral) | Send to another TV |
| `error` | red | Couldn't cast | `state.msg` | hidden | Try again (accent) | none |

"On TV" is a page-local condition (`webkitCurrentPlaybackTargetIsWireless`),
not a helper phase; it overrides the `ready`/`packaged` rows when true.

## Plumbing

Two changes in `plugin/main.js`. **No helper protocol change** — the Go helper
and its event stream are untouched.

### `subtitleLabel(tracks)`

A pure function beside `selectTracks`, taking a `selectTracks` result (or
`null`) and returning `{ label, warn }`. `label` is the finished display
string, so the page does no string building:

- `tracks.sub` present → `"Subtitles: "` + `sub.lang`, else `sub.title`, else `"On"`
- `tracks.subDropped` → `{ label: "Subtitles not supported", warn: true }`
- `tracks` null (no castable tracks yet) → `{ label: "", warn: false }`
- neither → `{ label: "No subtitles", warn: false }`

Unit-testable the way `selectTracks` and the `mirrorOn*` functions already are.

### `stateForPage()` gains two derived fields

`duration` and `subs` are **computed live in `stateForPage()`**, not stored on
`state`:

```js
duration: mpv.getNumber("duration") || 0,
subs: subtitleLabel(selectTracks(mpv.getNative("track-list") || [])),
```

This is safe: all three `stateForPage()` call sites (`main.js:444`, `451`,
`455`) are inside `sidebar.onMessage` handlers, which run on the main thread.

Deriving rather than storing matters for the `idle` row of the state table.
`selectTracks` otherwise runs only at cast start (`main.js:350`), so a stored
`subs` would be empty before the first cast — and the panel would have nothing
to say about subtitles exactly when the user is deciding whether to cast.
Deriving also keeps the label correct when the user switches subtitle track
mid-session.

Because neither field lands on `state`, the ten `state = { ... }` literals
(`main.js:328`, `337`, `347`, `354`, `363`, `370`, `408`, `415`, `419`, `437`)
are left alone. No factory, no refactor.

The page reads elapsed from the `<video>` element it already owns — no field
for it.

The `subDropped` OSD at `main.js:358` stays — it is still right at the moment
of the decision. The panel just stops being the only place that information
briefly lived.

## Edge cases

- **Narrow sidebar** — text ellipsizes; action buttons stay full-width.
- **Long subtitle titles** — truncate, with a `title` attribute for the tooltip.
- **First-load media flake** — the existing retry-once behavior
  (`docs/prototype.md`) is unchanged; failure surfaces only after the second
  attempt.
- **`prefers-reduced-motion`** — drops the progress bar transition.
- **Sidebar threading** — unchanged. The page continues to poll `getState`;
  nothing here calls `sidebar.*` from an `exec` callback.

## Testing

- `subtitleLabel` gets unit tests in `plugin/tests/main.test.mjs`, alongside
  the existing pure-function tests: a text track with `lang`, one with only
  `title`, one with neither, a `subDropped` result, and `null`.
- The render path stays manual — it is DOM in a webview with no harness, as
  today. Verify by driving all six rows of the state table against a real cast,
  in both light and dark appearance.

## Out of scope

- File title in the panel (considered, not wanted).
- Packaging detail beyond a percentage — would require new helper event fields.
- Subtitle *picking* from the sidebar. The panel reports subtitle status only.
  The tile grammar that would make a picker natural was study B, which was not
  chosen.
