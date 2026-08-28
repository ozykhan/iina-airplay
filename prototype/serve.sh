#!/bin/bash
# AirPlay de-risk helper for IINA.
#
# Packages a media file into an AirPlay-compatible fMP4/HLS stream and serves it
# on the LAN, then prints "READY <url>".  The Apple TV fetches the stream itself,
# which is why this binds 0.0.0.0 and advertises the Mac's LAN IP rather than
# 127.0.0.1.
#
# Usage: serve.sh <source-file> <output-dir> [port] [seconds]
#   seconds: length of the test clip, 0 = whole file (default 180)

set -uo pipefail

# IINA's utils.exec replaces the process environment (it sets only LC_ALL),
# so PATH has to be rebuilt here or nothing will be found.
export PATH="/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"

SRC="${1:-}"
OUT="${2:-}"
PORT="${3:-8919}"
DUR="${4:-180}"

die() { echo "ERROR $*" >&2; exit 1; }

[ -n "$SRC" ] && [ -f "$SRC" ] || die "source file not found: '$SRC'"
[ -n "$OUT" ] || die "no output directory given"

for bin in ffmpeg ffprobe python3; do
  command -v "$bin" >/dev/null 2>&1 || die "$bin not found (PATH=$PATH)"
done

probe() {
  ffprobe -v error -select_streams "$1" -show_entries "stream=$2" -of csv=p=0 -- "$SRC" 2>/dev/null | head -1
}

VCODEC=$(probe v:0 codec_name)
ACODEC=$(probe a:0 codec_name)
ACHAN=$(probe a:0 channels)
[ -n "$ACHAN" ] || ACHAN=2

echo "PROBE video=${VCODEC:-none} audio=${ACODEC:-none} channels=$ACHAN"

# --- video: Apple TV takes H.264 and HEVC Main/Main10, nothing else ---
case "$VCODEC" in
  h264) VARGS=(-c:v copy) ;;
  hevc) VARGS=(-c:v copy -tag:v hvc1) ;;
  *)    VARGS=(-c:v hevc_videotoolbox -b:v 12M -tag:v hvc1)
        echo "NOTE re-encoding video: '$VCODEC' is not AirPlay-compatible" ;;
esac

# --- audio: AAC / AC-3 / E-AC-3 pass through, DTS and TrueHD do not ---
case "$ACODEC" in
  aac|ac3|eac3)
    AARGS=(-c:a copy) ;;
  *)
    if [ "$ACHAN" -gt 2 ]; then
      AARGS=(-c:a eac3 -b:a 640k -ac 6)
    else
      AARGS=(-c:a aac -b:a 256k)
    fi
    echo "NOTE re-encoding audio: '$ACODEC' is not AirPlay-compatible" ;;
esac

TARGS=()
[ "$DUR" != "0" ] && TARGS=(-t "$DUR")

mkdir -p "$OUT" || die "cannot create $OUT"
rm -f "$OUT"/index.m3u8 "$OUT"/init.mp4 "$OUT"/seg_*.m4s

echo "PACKAGING (clip=${DUR}s, 0 means whole file)"
ffmpeg -hide_banner -loglevel warning -y \
  -i "$SRC" \
  -map 0:v:0 -map 0:a:0 -sn -dn \
  "${VARGS[@]}" "${AARGS[@]}" "${TARGS[@]}" \
  -f hls -hls_time 6 -hls_playlist_type vod \
  -hls_segment_type fmp4 -hls_flags independent_segments \
  -hls_fmp4_init_filename init.mp4 \
  -hls_segment_filename "$OUT/seg_%04d.m4s" \
  "$OUT/index.m3u8" || die "ffmpeg failed"

IP=""
for IFACE in en0 en1 en2 en3 en4; do
  CANDIDATE=$(ipconfig getifaddr "$IFACE" 2>/dev/null || true)
  if [ -n "$CANDIDATE" ]; then IP="$CANDIDATE"; break; fi
done
[ -n "$IP" ] || die "no LAN IP found; the Apple TV pulls the stream itself so 127.0.0.1 will not do"

cat > "$OUT/index.html" <<'HTML'
<!doctype html>
<meta charset="utf-8">
<title>IINA AirPlay test</title>
<style>
  html,body{margin:0;background:#101014;color:#e8e8ee;
    font:14px/1.5 -apple-system,BlinkMacSystemFont,sans-serif}
  video{display:block;width:100%;max-height:78vh;background:#000}
  p{padding:12px 16px;margin:0}
  code{color:#9ad}
</style>
<video controls autoplay playsinline x-webkit-airplay="allow" src="index.m3u8"></video>
<p>Click the AirPlay button in the player controls and pick your Apple TV.</p>
HTML

echo "READY http://$IP:$PORT/"

exec python3 - "$PORT" "$OUT" <<'PY'
import functools, http.server, mimetypes, socketserver, sys

# python's stock table does not know these, and Safari refuses HLS served as
# application/octet-stream
mimetypes.add_type("application/vnd.apple.mpegurl", ".m3u8")
mimetypes.add_type("video/iso.segment", ".m4s")
mimetypes.add_type("video/mp4", ".mp4")

port, root = int(sys.argv[1]), sys.argv[2]

class Server(socketserver.ThreadingTCPServer):
    allow_reuse_address = True
    daemon_threads = True

handler = functools.partial(http.server.SimpleHTTPRequestHandler, directory=root)
with Server(("0.0.0.0", port), handler) as httpd:
    httpd.serve_forever()
PY
