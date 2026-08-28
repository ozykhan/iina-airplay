# Next steps

## 0. De-risk (before writing anything real)

Run both experiments in `docs/prototype.md`. Everything below assumes the answer
to "does the AirPlay picker show up in the plugin window" — do not skip it.

## 1. Skeleton

- `iina-plugin create` for the official TypeScript template, or hand-roll
- `Info.json`: permissions `file-system`, `network-request`, `show-osd`
- Menu item + a keybinding
- Read `path`, `time-pos`, `track-list` from `iina.mpv`

## 2. The packaging engine

The real work. Decide per file:

- probe with `ffprobe` (video codec, profile, pixel format, audio codec, channels,
  subtitle codecs, HDR metadata)
- remux when the codecs already qualify; transcode only the stream that fails
- audio: DTS / TrueHD / PCM → E-AC-3 5.1 or AAC stereo
- video: anything not H.264 / HEVC Main / Main 10 → `hevc_videotoolbox`
- Dolby Vision Profile 7 → ship the HDR10 base layer
- subtitles: text tracks → WebVTT in the HLS playlist; PGS → burn-in (and warn the
  user what that costs) or drop
- package as fMP4/HLS; keep segments in `@tmp` and clean up

Streaming rather than pre-packaging the whole file is the difference between "click
and it plays" and "click and wait" — worth designing for early, probably via an
HLS event playlist that grows as ffmpeg writes.

## 3. Playback control

- Pause IINA on handoff, resume at the TV's position on stop
- Transport controls in the plugin window (or IINA's own, proxied)
- Position polling; accept drift
- Handle: TV goes to sleep, network drops, user quits IINA mid-stream

## 4. Distribution

- IINA installs plugins from GitHub via `ghRepo` / `ghVersion`
- **Verify:** does a downloaded helper binary pick up `com.apple.quarantine` and
  get refused by Gatekeeper? May need ad-hoc signing, notarization, or stripping
  the xattr on first run. This is the one unknown that could force a redesign of
  how the helper ships.
- If `ffmpeg` stays a user prerequisite, detect it and say so clearly rather than
  failing at exec time
