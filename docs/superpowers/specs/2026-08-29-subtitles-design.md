# Subtitle support: selected text track → WebVTT rendition

Date: 2026-08-29. Status: approved by user (design conversation), pending
implementation.

## Goal

When the user casts, the subtitle track currently selected in IINA appears on
the Apple TV. Text subtitles only; delivered as a WebVTT rendition in the HLS
stream, picked up by the TV's own player.

## Decisions (settled with the user — do not reopen)

- **Text subs only this round.** Image subs (PGS, DVD/VOBSUB) cannot pass
  through to Apple TV; burning them in forces a full video re-encode. Burn-in
  is deferred — a possible future round, not rejected. When the selected track
  is image-based, cast **without** subtitles and tell the user why via OSD.
- **Exactly IINA's selected subtitle track.** No multi-language renditions.
  Subs off in IINA → no subs in the cast. To change subtitle track, re-cast.
- **External subtitle files are supported** (sidecar `.srt`, IINA's online
  subtitle downloads). They appear in mpv's track-list as external tracks with
  a file path.
- **Mid-cast track changes are ignored.** Tracks are snapshotted at cast
  start, same as video/audio today.
- **ASS/SSA degrades to plain WebVTT silently.** Styling is lost, text kept.
  Documented, not warned about at cast time.
- **Approach A** (single ffmpeg process, native HLS subtitle muxing) over a
  separate extraction job with a hand-authored master playlist. Rationale:
  reuses the hardened single-job supervision (watchdog, progress, lifecycle);
  smallest diff. The known wrinkle (ffmpeg's hardcoded rendition attributes)
  gets a contained fix at serve time (§4). If ffmpeg's subtitle muxing proves
  flaky, the fallback is helper-authored playlists + a demux-only extraction
  job; the plugin-side work carries over unchanged.

## 1. Track selection (plugin/main.js, pure function)

`selectTracks(trackList)` additionally finds the sub track with
`selected: true`. No fallback track.

Codec classification:

- Castable text codecs: `subrip`, `srt`, `ass`, `ssa`, `mov_text`, `webvtt`,
  `text`.
- Image codecs (not castable): `hdmv_pgs_subtitle`, `dvd_subtitle`.
- Unknown sub codecs: treat as not castable (safe default).

Result object gains:

- `subPath` — `external-filename` for external tracks, `null` for embedded
- `smap` — the track's `ff-index` (meaningful for embedded tracks)
- `subLang`, `subTitle` — from the track's `lang` / `title`, may be empty;
  feed the TV's subtitle-menu label
- a flag distinguishing "no sub selected" from "selected but image-based", so
  `startCast` can OSD: subtitles are image-based and can't be cast; casting
  without subtitles.

`startCast` passes new helper flags when a castable sub exists:
`-smap`, `-subpath`, `-sublang`, `-subname`.

## 2. Helper ffmpeg args (helper/job.go)

`JobConfig` gains `SubPath string`, `SubMap int` (−1 = no subs),
`SubLang string`, `SubName string`.

`BuildArgs` with a sub present:

- external (`SubPath != ""`): add second input `-i SubPath`, map `1:0`
- embedded: map `0:<SubMap>`
- drop `-sn`; add `-c:s webvtt` and `-master_pl_name master.m3u8`

ffmpeg's hls muxer then writes a WebVTT rendition playlist and `.vtt`
segments alongside the AV playlist, plus `master.m3u8` referencing both.

With no sub (`SubMap == -1`), the produced args are **byte-identical to
today's** — the no-subs path must not change behavior.

## 3. Ready/URL and cleanup (helper/main.go)

With subs in play:

- ready-check also waits for `master.m3u8`
- the `ready` event advertises `http://<ip>:<port>/master.m3u8`
- stale-file cleanup globs add `master.m3u8`, the VTT rendition playlist, and
  `*.vtt` segments

Without subs: unchanged — no master playlist exists, `index.m3u8` is
advertised as today.

## 4. Master playlist rewrite (helper/server.go)

ffmpeg hardcodes `DEFAULT=NO` and a generic `NAME` on the
`#EXT-X-MEDIA:TYPE=SUBTITLES` line, which leaves subtitles disabled on the TV
until the user digs through the TV's menu. Fix at serve time:

- pure function `RewriteMasterPlaylist(content, name, lang string) string`:
  on the `TYPE=SUBTITLES` line, set `DEFAULT=YES,AUTOSELECT=YES` and, when
  non-empty, `NAME="<name>"` / `LANGUAGE="<lang>"`. Fallback name when both
  are empty: `"Subtitles"`. All other lines pass through untouched.
- the HTTP handler applies it to responses for `master.m3u8` only.

## 5. Error handling

No new machinery. A missing or unparsable subtitle file surfaces through the
existing ffmpeg-failure → `error` event path; the user's remedy is deselect
subs and re-cast. Auto-retry-without-subs needs job-restart machinery and is
deliberately out of scope.

## 6. Testing

- **Node (`plugin/tests`):** `selectTracks` — selected embedded text sub,
  selected external sub, no sub selected, selected PGS sub (image flag set),
  unknown codec.
- **Go (`helper`):** `BuildArgs` — none / embedded / external / no-subs
  byte-identical check; `RewriteMasterPlaylist` against a real ffmpeg-emitted
  master playlist sample (attributes rewritten, other lines untouched, empty
  name/lang fallback); ready-glob and cleanup updates.
- **e2e (`helper/e2e_test.go`):** fixture synthesizing an SRT track; assert
  `master.m3u8` and the VTT playlist appear, and the served `master.m3u8`
  carries `DEFAULT=YES` and the expected `NAME`/`LANGUAGE`.

## Out of scope (this round)

- Burn-in for PGS / styled ASS (future round; needs re-encode UX)
- Multiple subtitle renditions / language switching on the TV
- Reacting to mid-cast track changes
- Subtitle timing offset adjustments
