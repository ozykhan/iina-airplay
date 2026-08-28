// AirPlay de-risk prototype for IINA.
//
// What this proves (or disproves) in one click:
//   1. utils.exec can drive ffmpeg + a local HTTP server from a plugin
//   2. the packaged HLS stream is something an Apple TV will accept
//   3. the plugin's standaloneWindow (a WKWebView, built with a default
//      WKWebViewConfiguration where allowsAirPlayForMediaPlayback is true)
//      surfaces WebKit's own AirPlay picker
//
// If 3 holds, the real plugin needs no native AirPlay UI at all.

const { core, mpv, menu, standaloneWindow, sidebar, utils, file, console } = iina;

const PORT = 8919;
const CLIP_SECONDS = "180";   // "0" for the whole file

// serve.sh, embedded so the plugin is self-contained. exec() can only run
// binaries under @data or @tmp (or an absolute path), so it is written out
// on demand; IINA chmod 755s anything it runs from its own data directory.
const SERVE_SH = "#!/bin/bash\n# AirPlay de-risk helper for IINA.\n#\n# Packages a media file into an AirPlay-compatible fMP4/HLS stream and serves it\n# on the LAN, then prints \"READY <url>\".  The Apple TV fetches the stream itself,\n# which is why this binds 0.0.0.0 and advertises the Mac's LAN IP rather than\n# 127.0.0.1.\n#\n# Usage: serve.sh <source-file> <output-dir> [port] [seconds]\n#   seconds: length of the test clip, 0 = whole file (default 180)\n\nset -uo pipefail\n\n# IINA's utils.exec replaces the process environment (it sets only LC_ALL),\n# so PATH has to be rebuilt here or nothing will be found.\nexport PATH=\"/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin\"\n\nSRC=\"${1:-}\"\nOUT=\"${2:-}\"\nPORT=\"${3:-8919}\"\nDUR=\"${4:-180}\"\n\ndie() { echo \"ERROR $*\" >&2; exit 1; }\n\n[ -n \"$SRC\" ] && [ -f \"$SRC\" ] || die \"source file not found: '$SRC'\"\n[ -n \"$OUT\" ] || die \"no output directory given\"\n\nfor bin in ffmpeg ffprobe python3; do\n  command -v \"$bin\" >/dev/null 2>&1 || die \"$bin not found (PATH=$PATH)\"\ndone\n\nprobe() {\n  ffprobe -v error -select_streams \"$1\" -show_entries \"stream=$2\" -of csv=p=0 -- \"$SRC\" 2>/dev/null | head -1\n}\n\nVCODEC=$(probe v:0 codec_name)\nACODEC=$(probe a:0 codec_name)\nACHAN=$(probe a:0 channels)\n[ -n \"$ACHAN\" ] || ACHAN=2\n\necho \"PROBE video=${VCODEC:-none} audio=${ACODEC:-none} channels=$ACHAN\"\n\n# --- video: Apple TV takes H.264 and HEVC Main/Main10, nothing else ---\ncase \"$VCODEC\" in\n  h264) VARGS=(-c:v copy) ;;\n  hevc) VARGS=(-c:v copy -tag:v hvc1) ;;\n  *)    VARGS=(-c:v hevc_videotoolbox -b:v 12M -tag:v hvc1)\n        echo \"NOTE re-encoding video: '$VCODEC' is not AirPlay-compatible\" ;;\nesac\n\n# --- audio: AAC / AC-3 / E-AC-3 pass through, DTS and TrueHD do not ---\ncase \"$ACODEC\" in\n  aac|ac3|eac3)\n    AARGS=(-c:a copy) ;;\n  *)\n    if [ \"$ACHAN\" -gt 2 ]; then\n      AARGS=(-c:a eac3 -b:a 640k -ac 6)\n    else\n      AARGS=(-c:a aac -b:a 256k)\n    fi\n    echo \"NOTE re-encoding audio: '$ACODEC' is not AirPlay-compatible\" ;;\nesac\n\nTARGS=()\n[ \"$DUR\" != \"0\" ] && TARGS=(-t \"$DUR\")\n\nmkdir -p \"$OUT\" || die \"cannot create $OUT\"\nrm -f \"$OUT\"/index.m3u8 \"$OUT\"/init.mp4 \"$OUT\"/seg_*.m4s\n\necho \"PACKAGING (clip=${DUR}s, 0 means whole file)\"\nffmpeg -hide_banner -loglevel warning -y \\\n  -i \"$SRC\" \\\n  -map 0:v:0 -map 0:a:0 -sn -dn \\\n  \"${VARGS[@]}\" \"${AARGS[@]}\" \"${TARGS[@]}\" \\\n  -f hls -hls_time 6 -hls_playlist_type vod \\\n  -hls_segment_type fmp4 -hls_flags independent_segments \\\n  -hls_fmp4_init_filename init.mp4 \\\n  -hls_segment_filename \"$OUT/seg_%04d.m4s\" \\\n  \"$OUT/index.m3u8\" || die \"ffmpeg failed\"\n\nIP=\"\"\nfor IFACE in en0 en1 en2 en3 en4; do\n  CANDIDATE=$(ipconfig getifaddr \"$IFACE\" 2>/dev/null || true)\n  if [ -n \"$CANDIDATE\" ]; then IP=\"$CANDIDATE\"; break; fi\ndone\n[ -n \"$IP\" ] || die \"no LAN IP found; the Apple TV pulls the stream itself so 127.0.0.1 will not do\"\n\ncat > \"$OUT/index.html\" <<'HTML'\n<!doctype html>\n<meta charset=\"utf-8\">\n<title>IINA AirPlay test</title>\n<style>\n  html,body{margin:0;background:#101014;color:#e8e8ee;\n    font:14px/1.5 -apple-system,BlinkMacSystemFont,sans-serif}\n  video{display:block;width:100%;max-height:78vh;background:#000}\n  p{padding:12px 16px;margin:0}\n  code{color:#9ad}\n</style>\n<video controls autoplay playsinline x-webkit-airplay=\"allow\" src=\"index.m3u8\"></video>\n<p>Click the AirPlay button in the player controls and pick your Apple TV.</p>\nHTML\n\necho \"READY http://$IP:$PORT/\"\n\nexec python3 - \"$PORT\" \"$OUT\" <<'PY'\nimport functools, http.server, mimetypes, socketserver, sys\n\n# python's stock table does not know these, and Safari refuses HLS served as\n# application/octet-stream\nmimetypes.add_type(\"application/vnd.apple.mpegurl\", \".m3u8\")\nmimetypes.add_type(\"video/iso.segment\", \".m4s\")\nmimetypes.add_type(\"video/mp4\", \".mp4\")\n\nport, root = int(sys.argv[1]), sys.argv[2]\n\nclass Server(socketserver.ThreadingTCPServer):\n    allow_reuse_address = True\n    daemon_threads = True\n\nhandler = functools.partial(http.server.SimpleHTTPRequestHandler, directory=root)\nwith Server((\"0.0.0.0\", port), handler) as httpd:\n    httpd.serve_forever()\nPY\n";

let serverRunning = false;
let streamURL = null;

function showWindow(url) {
  streamURL = url;
  standaloneWindow.loadFile("window.html");        // clears message listeners
  standaloneWindow.setProperty({ title: "AirPlay Test", resizable: true });
  standaloneWindow.setFrame(960, 620);
  standaloneWindow.onMessage("ready", () => {
    standaloneWindow.postMessage("url", { url: streamURL });
  });
  standaloneWindow.open();
}

// IINA's sidebar.show() runs AppKit layout on the *calling* thread and
// SIGABRTs (AutoLayout thread assertion) when invoked from a utils.exec
// callback, which runs off-main. Only call sidebar.* from menu callbacks;
// the page polls "ready" until streamURL exists.
function openSidebar() {
  sidebar.loadFile("sidebar.html");                // clears message listeners
  sidebar.onMessage("ready", () => {
    if (streamURL) sidebar.postMessage("url", { url: streamURL });
  });
  sidebar.show();
}

function cast(show) {
  if (serverRunning) {
    core.osd("Already streaming - quit IINA to stop the helper");
    if (streamURL) show(streamURL);
    return;
  }

  const src = mpv.getString("path");
  if (!src) {
    core.osd("Nothing is playing");
    return;
  }
  if (src.charAt(0) !== "/") {
    core.osd("Only local files for now: " + src);
    return;
  }

  file.write("@data/serve.sh", SERVE_SH);
  const outDir = utils.resolvePath("@tmp/hls");

  core.pause();
  core.osd("Packaging for AirPlay...");
  serverRunning = true;

  let opened = false;

  utils.exec(
    "@data/serve.sh",
    [src, outDir, String(PORT), CLIP_SECONDS],
    undefined,
    (out) => {
      console.log("[serve] " + out.trim());
      const m = out.match(/READY (http:\S+)/);
      if (m && !opened) {
        opened = true;
        core.osd("Ready - look for the AirPlay button");
        show(m[1]);
      }
    },
    (err) => console.log("[serve:stderr] " + err.trim())
  )
    .then((r) => {
      serverRunning = false;
      console.log("[serve] helper exited with status " + r.status);
    })
    .catch((e) => {
      serverRunning = false;
      console.log("[serve] failed: " + e);
      core.osd("AirPlay helper failed - see the plugin console");
    });
}

menu.addItem(menu.item("Cast current file to Apple TV (test)", () => cast(showWindow)));
menu.addItem(menu.item("Cast via sidebar (test)", () => {
  openSidebar();                          // main-thread menu callback: safe
  cast((url) => { streamURL = url; });    // READY handler only records the URL
}));
