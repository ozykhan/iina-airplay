# Selected embedded image-subtitle burn-in

Date: 2026-11-14. Status: implementation authorized; test seam approved.

## EARS requirements

- **SUB-BURN-001 (Ubiquitous):** The cast pipeline shall preserve its existing behavior when no embedded image subtitle is selected; its FFmpeg arguments shall remain byte-identical to the pinned no-subtitle command.
- **SUB-BURN-002 (Event-driven):** When IINA reports a selected embedded PGS (`hdmv_pgs_subtitle`) or VobSub (`dvd_subtitle`) track, the plugin shall pass that track's FFmpeg stream index to the helper for video burn-in.
- **SUB-BURN-003 (Event-driven):** When an embedded PGS or VobSub track is selected, the helper shall composite that subtitle stream over the selected video stream and encode the resulting video as HEVC using the existing VideoToolbox bitrate, bit-depth, and HLS settings.
- **SUB-BURN-004 (Ubiquitous):** The image-subtitle burn-in path shall not emit a subtitle rendition; the chosen image subtitle is part of the video pixels.
- **SUB-BURN-005 (Ubiquitous):** Selected text subtitles shall continue using the current WebVTT HLS rendition without being burned into the video.
- **SUB-BURN-006 (Unwanted):** The plugin shall not silently burn external image subtitles or unknown subtitle codecs; it shall retain the existing cast-without-subtitles warning behavior for unsupported selections.
- **SUB-BURN-007 (Event-driven):** If FFmpeg cannot decode or composite the selected image subtitle, the helper shall report its existing cast error; it shall not silently restart without subtitles.
- **SUB-BURN-008 (Ubiquitous):** A cast with image subtitles shall retain the selected audio stream and existing audio copy/transcode policy.

## Low-level design

### Flow and interface

`selectTracks()` classifies the selected subtitle as text, embedded image, or unsupported. It returns an image track using the existing subtitle object shape plus `burnIn: true`; only embedded PGS and DVD/VobSub codecs receive this flag. The track's `ff-index` is passed to the helper as `-burninsmap <index>`. Existing `-smap` and `-subpath` remain exclusively for text WebVTT subtitles.

The helper adds `BurnInSubMap string` to `JobConfig`. When set, `BuildArgs()` constructs a filter graph from the selected input video and subtitle streams (`overlay`), maps the filtered video and selected audio, and disables subtitle output. `Transcodes()` returns true in this mode so H.264 and HEVC sources use the existing VideoToolbox HEVC encoder rather than stream copy. Audio decisions and the HLS event playlist remain unchanged. No `master.m3u8` or WebVTT subtitle playlist is produced for the burn-in mode.

The graph's input labels use the original input's absolute FFmpeg stream indexes, not output-relative indexes. Do not add a second media-processing process. Keep the existing text-subtitle and no-subtitle branches unchanged.

### UX and errors

The selected subtitle remains named in the cast state/label. The existing unsupported-subtitle OSD remains for external image subtitles and unknown codecs. FFmpeg failures follow the existing helper `error` event; retrying without subtitles is out of scope.

### Test seams (approved)

1. Node unit tests cover selected embedded PGS and VobSub classification and confirm text/unsupported cases retain their current classification.
2. Node runtime tests confirm an embedded image selection sends `-burninsmap` (not `-smap`/`-subpath`) and still starts the cast.
3. Go argument tests confirm the chosen streams enter the overlay graph, output video uses the existing HEVC encoder settings, no subtitle rendition is mapped, and the pinned no-subtitle args are unchanged.
4. Real-media tests use deterministic PGS and VobSub fixtures, prove the selected subtitle changes decoded video pixels, and prove the burn-in path succeeds with the packaged binary. The VobSub fixture is derived from the synthetic PGS bitmap using FFmpeg's bitmap-to-bitmap subtitle conversion. Do not substitute text-subtitle behavior for bitmap-subtitle proof. `packaging/test-package.sh` sets `IINA_AIRPLAY_FFMPEG` to the packaged binary and runs these tests on both architectures.

### Known architectural exception

`CLAUDE.md` says “Prefer remuxing over re-encoding, always.” This approved feature is an explicit exception: selected image-subtitle casts necessarily re-encode the video and may take longer, particularly for UHD sources. Ordinary casts and selected text-subtitle casts do not take this path.

### Risks / implementation gates

The official FFmpeg documentation describes a compatibility exception that converts bitmap subtitle streams into video inputs for filters such as `overlay` ([FFmpeg filtergraph documentation](https://ffmpeg.org/ffmpeg.html#Complex-filtergraphs)). Host FFmpeg 9.0.2 has passed deterministic PGS and VobSub pixel tests. The pinned 9.0.1 packaged macOS binary still needs to pass the package test gate on both architectures before release; if its filtergraph cannot consume either stream, stop rather than shipping a misleading cast option.
