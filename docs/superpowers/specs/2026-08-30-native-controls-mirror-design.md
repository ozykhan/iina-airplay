# Native-control casting: the muted mirror

**Date:** 2026-08-30
**Status:** approved by user (sync model, plumbing, start/stop behavior all confirmed)

## Problem

When a cast is running, IINA shows a frozen paused frame and offers no playback
control; the only remote is the Apple TV's. The user wants IINA's own controls
(space bar, scrubber) to drive the TV, and the video area to stop looking stuck.

## Decisions already made (with the user)

- **IINA's native controls drive the TV.** Not a sidebar remote, not just a
  cosmetic overlay.
- **Muted mirror model**, not a pause-state hack: while casting, mpv keeps
  playing — muted — and stays position-synced to the TV. The pause icon,
  space bar, and scrubber all behave truthfully, and the video area becomes a
  live silent preview of what the TV shows.
- **Volume is out of scope.** `v.volume` propagation over AirPlay is
  unreliable across receivers; the user chose to skip it entirely. Volume
  stays a TV-remote concern.

## Sync model

**The TV is the clock; mpv is the mirror.** AirPlay HLS playback runs seconds
behind any local clock, so mpv's position is never authoritative during a cast.

- **Commands flow IINA → TV:** play, pause, seek. The hidden `<video>` element
  in the sidebar page owns the AirPlay session; while
  `webkitCurrentPlaybackTargetIsWireless` is true, `v.play()`, `v.pause()`,
  and `v.currentTime = x` control the TV.
- **Truth flows TV → IINA:** the page reports `v.currentTime`, `v.paused`,
  wireless status, and `ended`. If mpv's `time-pos` drifts more than **1.5s**
  from the TV's reported position and no user seek explains it, `main.js`
  sets mpv `time-pos` to the TV position.
- **TV-initiated changes** (pause from the TV remote) are mirrored back into
  mpv so IINA's UI stays truthful.

### Echo suppression

Two feedback loops must be broken:

1. Every command carries a **sequence number**; the page acks the highest seq
   it has applied (`appliedSeq`). `main.js` ignores TV-state deltas that are
   just its own commands being applied.
2. When `main.js` drift-corrects mpv's `time-pos`, it records the value it
   set; the next `mpv.seek` event matching that value (within tolerance,
   within a short window) is **not** treated as a user seek.

A user seek is thus: an `mpv.seek` event whose new position was not just set
programmatically. It becomes `sync.seekTo` with a bumped seq.

## Plumbing

No new channels. The sidebar page already polls `getState` every 500ms; that
poll becomes the transport in both directions. Up to ~500ms of command latency
was explicitly accepted in exchange for zero new threading risk.

- `state` grows a `sync` block: `{ seq, paused, seekTo }`.
- The page applies `sync` to the `<video>` and posts a new message,
  `tvState: { appliedSeq, pos, paused, wireless, ended }`.
- `main.js` handles `tvState` in a `sidebar.onMessage` handler (main-thread
  safe per the SIGABRT rule) — this is where drift correction and
  TV-initiated pause mirroring touch mpv.
- `main.js` subscribes to `event.on("mpv.pause.changed")` and
  `event.on("mpv.seek")`. **Those callbacks only mutate the state object** —
  never call `sidebar.*` — same discipline the codebase already follows for
  exec callbacks.
- If IINA turns out not to forward the mpv `seek` event to plugins, the
  fallback is jump detection: each poll compares mpv `time-pos` against the
  position extrapolated from the last poll; a jump beyond tolerance that
  wasn't a programmatic set is a user seek. `mirrorTick` is written against
  positions, not event names, so both wirings feed it identically.

## Cast start / stop changes (`main.js`)

**Start** (`startCast`):
- Remove `core.pause()`. Instead: save the current mute flag, set mute on,
  and let mpv keep playing.
- Initialize the sync block from mpv's current state.

**Stop** (`stopCast` and helper-reported teardown):
- Restore the saved mute flag.
- No position handling needed: mpv has been mirroring the TV, so playback
  continues locally where the TV left off — seamless handback with sound.

**Guards while casting:**
- TV stream reports `ended` → tear the cast down (same path as stop).
- mpv `path` changes (user loads a different file) → tear the cast down.

**Initial position (best-effort):** once wireless playback starts, the page
seeks `v.currentTime` to the position IINA was at when the cast began. If the
HLS packaging hasn't reached that point yet, the seek is retried as packaging
progresses; if the TV ends up somewhere else, the mirror simply follows the
TV. No hard guarantee.

## Latent bug fixed in passing

The hidden `<video>` currently plays **unmuted on the Mac** between "ready"
and the user picking a TV. The page must keep `v.muted = true` until
`webkitCurrentPlaybackTargetIsWireless` flips true, then unmute — a muted
element routed to AirPlay would cast silence. Unmute on wireless, re-mute when
wireless drops.

## Testing

- All decision logic lives in a pure function exported like `selectTracks`:
  `mirrorTick(input) → actions`, where `input` captures
  `{ mpvPos, mpvPaused, tv: {pos, paused, wireless, appliedSeq}, pending, lastSet }`
  and `actions` names what to do (`correctMpvTo`, `mirrorPauseToMpv`,
  `sendSeek`, `sendPause`, nothing).
- Node tests in `plugin/tests/` cover: drift vs user-seek discrimination,
  echo suppression both ways, seq/ack handling, TV-initiated pause, `ended`
  and path-change teardown decisions, mute save/restore bookkeeping.
- The IINA-runtime glue stays thin and untested-by-node; final acceptance is
  manual on the user's Apple TV: space bar, scrubber drag, TV-remote pause,
  stop-cast handback, start-cast initial position.

## Playback speed (issue #48, added 2026-10-29)

The first version never forwarded mpv's `speed`, so a user at 1.25x saw the
TV play at 1x. It was worse than that: mpv kept running at 1.25x, pulled
ahead of the TV by 0.25 s every second, and drift correction dragged it back
about every 6 s.

- **IINA → TV.** `mpv.speed.changed` sets `mirror.rate`; the sync block
  carries `rate`, and the page sets `v.playbackRate` (and
  `defaultPlaybackRate`, which a reload resets it to) on every poll. Speed is
  level-applied, not seq'd, so it never holds back pause or drift handling.
- **The TV may not obey.** AirPlay receivers are only reliable at rate 0
  and 1; some always play at 1x. `v.playbackRate` reads back what was asked
  for either way, so `main.js` measures the TV instead: `tvState` adds
  `rate`, `seeking` and `at` (the page's clock when it read `pos`), and
  `mirrorOnTvState` sums position against time over 10 s of TV playback at
  the requested rate. Paused, seeking, unacked and not-yet-applied polls
  reset the measurement; position jumps between polls (a TV-remote skip) are
  left out.
- **A TV found at 1x sets the clock**, as it does for position: mpv drops to
  1x (the echo is swallowed like a pause echo), the OSD and the sidebar's TV
  row say the TV ignored the speed, and the asked-for speed is kept in
  `ignoredRate`. Stopping the cast restores it to mpv, the same way mute is
  restored. A new speed from the user supersedes it and is measured again.

Not verified on hardware: whether WebKit forwards `playbackRate` to the
receiver over AirPlay at all (if not, every TV takes the 1x fallback), and
whether `v.currentTime` follows the TV closely enough for the measurement.

## Out of scope

- Volume/mute forwarding to the TV (user decision).
- Any change to packaging, the helper, subtitles, or the sidebar's visual UI.
- Multi-file/playlist transitions beyond the path-change teardown guard.
