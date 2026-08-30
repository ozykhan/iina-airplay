// plugin/main.js — orchestration. HARD RULE (docs/prototype.md): never call
// sidebar.* from utils.exec callbacks/promises; IINA runs those off the main
// thread and sidebar.show() SIGABRTs the app. Sidebar calls live only in the
// menu callback and onMessage handlers; the page polls "getState".

// ---- pure functions (node-testable) ----

// Sub codecs ffmpeg can convert to WebVTT. Image codecs (hdmv_pgs_subtitle,
// dvd_subtitle) and anything unknown can't pass through to the TV without a
// burn-in re-encode — out of scope, see docs/superpowers/specs/.
var TEXT_SUB_CODECS = { subrip: 1, srt: 1, ass: 1, ssa: 1, mov_text: 1, webvtt: 1, text: 1 };

function selectTracks(trackList) {
  var video = null, audio = null, firstAudio = null, sub = null;
  for (var i = 0; i < trackList.length; i++) {
    var t = trackList[i];
    if (t.type === "video" && (!video || t.selected)) video = t;
    if (t.type === "audio") {
      if (firstAudio === null) firstAudio = t;
      if (t.selected) audio = t;
    }
    if (t.type === "sub" && t.selected) sub = t;
  }
  if (!video) return null;
  if (!audio) audio = firstAudio;
  if (!audio) return null;
  var r = {
    vcodec: video.codec,
    acodec: audio.codec,
    achannels: audio["demux-channel-count"] || 2,
    vmap: video["ff-index"],
    amap: audio["ff-index"],
    sub: null,
    subDropped: false,
  };
  if (sub) {
    var path = sub.external ? (sub["external-filename"] || null) : null;
    if (!TEXT_SUB_CODECS[sub.codec] || (sub.external && !path)) {
      r.subDropped = true;
    } else {
      r.sub = { path: path, smap: sub["ff-index"], lang: sub.lang || "", title: sub.title || "" };
    }
  }
  return r;
}

// selectTracks already decided what happens to subtitles; this just names the
// decision for the sidebar. Before the redesign that answer existed only as a
// one-shot OSD flash, which vanished before it could be read.
function subtitleLabel(tracks) {
  if (!tracks) return { label: "", warn: false };
  if (tracks.subDropped) return { label: "Subtitles not supported", warn: true };
  if (!tracks.sub) return { label: "No subtitles", warn: false };
  return { label: "Subtitles: " + (tracks.sub.lang || tracks.sub.title || "On"),
           warn: false };
}

function parseHelperEvents(buffer, chunk) {
  var data = buffer + chunk;
  var lines = data.split("\n");
  var rest = lines.pop(); // trailing partial line (or "")
  var events = [];
  for (var i = 0; i < lines.length; i++) {
    var line = lines[i].trim();
    if (!line) continue;
    try {
      var obj = JSON.parse(line);
      if (obj && obj.event) events.push(obj);
    } catch (e) { /* stray non-JSON output; ignore */ }
  }
  return { events: events, rest: rest };
}

// utils.resolvePath("@data/") resolves to an absolute path of the form
// <...>/plugins/.data/<identifier> (verified on-disk against a running IINA
// 1.4.4 install — there is no "plugins-data" directory). The plugins
// directory (where the plugin bundle — and thus bin/ — actually lives) is
// the parent of the ".data" component. Returns null if the path doesn't
// have the expected shape.
function pluginsDirFromDataDir(dataPath) {
  if (!dataPath) return null;
  var normalized = dataPath.replace(/\/+$/, ""); // strip trailing slash(es)
  var idx = normalized.lastIndexOf("/.data/");
  if (idx === -1) return null;
  return normalized.slice(0, idx);
}

function isValidPid(pid) {
  return typeof pid === "number" && isFinite(pid) && pid > 0;
}

// mpv's "path" is a URL whenever IINA is playing a network stream. The bundled
// ffmpeg is built --disable-network and has no protocol handler for those, so
// the plugin declines up front instead of letting ffmpeg fail obscurely.
function hasURLScheme(p) {
  return !!p && /^[a-zA-Z][a-zA-Z0-9+.-]*:\/\//.test(p);
}

// mpv usually reports a bare filesystem path, but can hand back a file:// URI.
// That names a local file we can cast, so strip the scheme and percent-decode
// rather than declining it — and do this before the URL-scheme test, which
// would otherwise call it a network stream.
function normalizeSource(p) {
  if (!p || p.indexOf("file://") !== 0) return p;
  var rest = p.slice("file://".length);
  if (rest.indexOf("localhost/") === 0) rest = rest.slice("localhost".length);
  try { return decodeURIComponent(rest); } catch (e) { return rest; }
}

// ---- muted-mirror sync core (spec: docs/superpowers/specs/2026-08-30-native-controls-mirror-design.md) ----
// The TV is the clock; mpv is the mirror. AirPlay HLS runs seconds behind any
// local clock, so mpv's position is never authoritative during a cast. These
// functions are pure — they take the mirror bookkeeping object plus
// observations and return new state/actions without touching IINA APIs — so
// node tests can drive every branch.

var DRIFT_TOLERANCE = 1.5; // seconds of mpv/TV divergence tolerated before correcting
var ECHO_WINDOW_MS = 2000; // how long our own time-pos set may echo back as an mpv seek event

function newMirror(startPos, savedMute, paused) {
  return {
    seq: 0,               // last issued command sequence number
    paused: paused,       // desired TV pause state (follows mpv)
    seekTo: null,         // pending user seek {seq, pos}; cleared when the page acks it
    startPos: startPos,   // where IINA was at cast start; the page best-effort seeks the TV here
    savedMute: savedMute, // mpv mute flag to restore on teardown
    expectMpvPause: null, // swallow the next pause.changed that echoes our own mpv.set
    lastSetPos: null,     // {pos, at} of our last programmatic time-pos set (echo suppression)
  };
}

// mpv's pause flag changed. Bumps seq only for user-initiated changes; an
// expected echo of our own mpv.set(pause) must not become a command back to
// the TV, or TV-remote pauses would ping-pong.
function mirrorOnMpvPause(m, mpvPaused) {
  var n = Object.assign({}, m);
  if (m.expectMpvPause !== null && mpvPaused === m.expectMpvPause) {
    n.expectMpvPause = null;
    return n;
  }
  n.expectMpvPause = null;
  if (mpvPaused !== m.paused) {
    n.paused = mpvPaused;
    n.seq = m.seq + 1;
  }
  return n;
}

// mpv seeked. A seek near our own recent time-pos set is drift correction
// echoing back, not the user; anything else becomes a TV seek command.
// seekTo carries its own seq so the page never re-applies a seek it has
// already performed when a later pause command bumps the outer seq.
function mirrorOnMpvSeek(m, mpvPos, now) {
  var n = Object.assign({}, m);
  if (m.lastSetPos !== null &&
      now - m.lastSetPos.at < ECHO_WINDOW_MS &&
      Math.abs(mpvPos - m.lastSetPos.pos) <= DRIFT_TOLERANCE) {
    n.lastSetPos = null;
    return n;
  }
  n.seq = m.seq + 1;
  n.seekTo = { seq: n.seq, pos: mpvPos };
  return n;
}

// The page reported TV state. Decides what (if anything) to push into mpv.
// While commands are in flight (appliedSeq < seq) the TV state is stale, so
// nothing is reconciled against it — not even drift. ended only tears the
// cast down once the TV is actually the one playing: a short file's LOCAL
// (muted, hidden) playback ending before the user ever picks a TV must not
// kill the cast. An acked seekTo is cleared as soon as it's acked, even
// before wireless playback starts, so a pre-wireless user seek doesn't leave
// a stale command sitting in the sync block.
function mirrorOnTvState(m, tvState, mpvPos, mpvPaused, now) {
  var out = { m: m, setMpvPos: null, setMpvPaused: null, teardown: false };
  if (tvState.ended && tvState.wireless) { out.teardown = true; return out; }
  var n = Object.assign({}, m);
  var acked = tvState.appliedSeq >= m.seq;
  if (acked) n.seekTo = null;
  out.m = n;
  if (!tvState.wireless || !acked) return out; // nothing on the TV yet, or a command is in flight: no clock to follow
  if (tvState.paused !== n.paused) {      // TV-remote initiated: mirror into mpv
    n.paused = tvState.paused;
    n.expectMpvPause = tvState.paused;
    out.setMpvPaused = tvState.paused;
  }
  if (Math.abs(mpvPos - tvState.pos) > DRIFT_TOLERANCE) {
    out.setMpvPos = tvState.pos;
    n.lastSetPos = { pos: tvState.pos, at: now };
  }
  return out;
}

if (typeof module !== "undefined") {
  module.exports = {
    selectTracks: selectTracks,
    subtitleLabel: subtitleLabel,
    parseHelperEvents: parseHelperEvents,
    pluginsDirFromDataDir: pluginsDirFromDataDir,
    isValidPid: isValidPid,
    hasURLScheme: hasURLScheme,
    normalizeSource: normalizeSource,
    newMirror: newMirror,
    mirrorOnMpvPause: mirrorOnMpvPause,
    mirrorOnMpvSeek: mirrorOnMpvSeek,
    mirrorOnTvState: mirrorOnTvState,
    DRIFT_TOLERANCE: DRIFT_TOLERANCE,
    ECHO_WINDOW_MS: ECHO_WINDOW_MS,
  };
}

// ---- IINA runtime ----

if (typeof iina !== "undefined") {
  var core = iina.core, mpv = iina.mpv, menu = iina.menu, sidebar = iina.sidebar,
      utils = iina.utils, file = iina.file, console = iina.console, event = iina.event;

  var state = { phase: "idle", url: null, pct: 0, msg: null };
  var stdoutRest = "";

  // Bumped every time a new cast starts or the cast is stopped. Async
  // callbacks (bin-dir resolution, helper stdout, exec .then/.catch) capture
  // the generation they belong to and no-op if it no longer matches — this
  // stops a superseded cast's stale callbacks from clobbering the state of
  // whatever cast (or idle state) came after it.
  var castGen = 0;

  // Active muted mirror (spec 2026-08-30), or null when not casting. Event
  // callbacks may ONLY swap this object via the pure mirrorOn* functions;
  // everything that touches mpv runs in onMessage/menu context.
  var mirror = null;

  // Every post to the page goes through this so the page always sees the live
  // sync block alongside the pipeline state.
  function stateForPage() {
    return {
      phase: state.phase, url: state.url, pct: state.pct, msg: state.msg,
      sync: mirror === null ? null : {
        seq: mirror.seq, paused: mirror.paused,
        seekTo: mirror.seekTo, startPos: mirror.startPos,
      },
    };
  }

  function endMirror() {
    if (mirror === null) return;
    mpv.set("mute", mirror.savedMute);
    mirror = null;
  }

  // Helper-initiated teardowns (stopped/error events, exec settling) happen in
  // exec callbacks, which must not touch mpv from off-main. They only mutate
  // `state`; the page's next poll lands here (onMessage: safe) and sweeps up.
  function reapMirror() {
    if (mirror !== null && (state.phase === "idle" || state.phase === "error")) endMirror();
  }

  event.on("mpv.pause.changed", function () {
    if (mirror === null) return;
    mirror = mirrorOnMpvPause(mirror, !!mpv.getFlag("pause"));
  });
  event.on("mpv.seek", function () {
    if (mirror === null) return;
    mirror = mirrorOnMpvSeek(mirror, mpv.getNumber("time-pos") || 0, Date.now());
  });
  // A new file invalidates the whole cast: the stream on the TV is the old
  // file. file-loaded is an app event (main thread), so stopCast is safe here.
  event.on("iina.file-loaded", function () {
    if (mirror === null) return;
    stopCast();
  });

  // Resolve our own install dir: @data is <plugins>/.data/<identifier>; the
  // plugin itself lives directly under that <.../plugins> directory. Locate
  // bin/ relative to the data dir's parent plugins folder at runtime via a
  // glob through /bin/sh once, then cache it in @data so later launches skip
  // the lookup.
  // utils.exec wipes the environment (only LC_ALL survives — no HOME), so the
  // plugins dir is derived in JS via utils.resolvePath and passed to the
  // shell as an argument rather than relied on via $HOME.
  function resolveBinDir(cb) {
    var cached = null;
    try { cached = file.read("@data/bindir.txt"); } catch (e) {}
    if (cached && cached.trim()) { cb(cached.trim(), null); return; }
    var pluginsDir = pluginsDirFromDataDir(utils.resolvePath("@data/"));
    if (!pluginsDir) { cb(null, "cannot resolve plugins directory"); return; }
    // com.apple.quarantine does NOT clear the executable bit (verified: a
    // quarantined file still passes `[ -x ]`), so the `-x` test alone cannot
    // catch the browser-download case docs/distribution.md and README.md
    // promise is detected. Worse, exec'ing a quarantined binary blocks on a
    // Gatekeeper dialog instead of failing fast, so check for the xattr with
    // `/usr/bin/xattr -p` (absolute path — /bin/sh via utils.exec runs with
    // an empty PATH) BEFORE anything ever execs the binary. `xattr -p`
    // succeeding (exit 0) means the attribute is present, i.e. quarantined.
    var script =
      'for d in "$1"/*/; do' +
      '  if /usr/bin/grep -qs \'"identifier": *"dev.faruk.iina-airplay"\' "$d/Info.json"; then' +
      '    h="$d"bin/airplay-helper; f="$d"bin/ffmpeg;' +
      '    if [ -x "$h" ] && [ -x "$f" ] ' +
      '        && ! /usr/bin/xattr -p com.apple.quarantine "$h" >/dev/null 2>&1 ' +
      '        && ! /usr/bin/xattr -p com.apple.quarantine "$f" >/dev/null 2>&1; then' +
      '      printf %s "$d"bin; exit 0;' +
      '    fi;' +
      '    exit 3;' +           // found the plugin, but its binaries won't run
      '  fi;' +
      'done; exit 1';
    utils.exec("/bin/sh", ["-c", script, "sh", pluginsDir]).then(function (r) {
      if (r.status === 0 && r.stdout) {
        file.write("@data/bindir.txt", r.stdout);
        cb(r.stdout, null);
      } else if (r.status === 3) {
        // Never offer to download anything — docs/distribution.md non-goals.
        cb(null, "the bundled helper or ffmpeg is missing, not executable, or quarantined; " +
                 "reinstall the plugin through IINA (Settings → Plugins → Install)");
      } else {
        cb(null, "cannot locate plugin bin directory");
      }
    }).catch(function (e) {
      cb(null, "cannot locate plugin bin directory: " + String(e));
    });
  }

  // Drop the cached bin dir so the next resolveBinDir call re-derives it
  // instead of reusing a directory that just failed to produce a runnable
  // helper (e.g. the plugin was reinstalled to a new path).
  function invalidateBinDirCache() {
    try { file.write("@data/bindir.txt", ""); } catch (e) {}
  }

  function startCast() {
    reapMirror(); // a helper-initiated teardown may have left a stale mirror
                  // un-reaped (only the getState poll normally does that); a
                  // restart must not let newMirror() capture that stale
                  // savedMute instead of the user's real pre-cast value.
    if (state.phase === "starting" || state.phase === "ready" || state.phase === "packaged") return;
    var src = normalizeSource(mpv.getString("path"));
    if (!src) {
      var nothingMsg = "nothing is playing";
      core.osd("AirPlay: " + nothingMsg);
      state = { phase: "error", url: null, pct: 0, msg: nothingMsg };
      return;
    }
    if (hasURLScheme(src)) {
      // The bundled ffmpeg is built --disable-network and has no protocol
      // handler for these at all, so say so plainly rather than letting
      // ffmpeg fail obscurely.
      var streamMsg = "can only cast local files, not network streams";
      core.osd("AirPlay: " + streamMsg);
      state = { phase: "error", url: null, pct: 0, msg: streamMsg };
      return;
    }
    if (src.charAt(0) !== "/") {
      // A relative path: the pipeline hands src straight to ffmpeg as -i,
      // which wants a plain absolute path. (file:// URIs no longer reach
      // this branch — normalizeSource above turns them into absolute paths
      // before either guard runs.)
      var pathMsg = "needs a local file path";
      core.osd("AirPlay: " + pathMsg);
      state = { phase: "error", url: null, pct: 0, msg: pathMsg };
      return;
    }
    var tracks = selectTracks(mpv.getNative("track-list") || []);
    if (!tracks) {
      var tracksMsg = "no castable video/audio tracks";
      core.osd("AirPlay: " + tracksMsg);
      state = { phase: "error", url: null, pct: 0, msg: tracksMsg };
      return;
    }
    if (tracks.subDropped) {
      core.osd("AirPlay: selected subtitles can't be cast (image-based, unsupported, or file not found); casting without them");
    }
    var pid = getIINAPid();
    if (!isValidPid(pid)) {
      core.osd("AirPlay: cannot determine IINA process id");
      state = { phase: "error", url: null, pct: 0, msg: "cannot determine IINA process id" };
      return;
    }
    var duration = mpv.getNumber("duration") || 0;
    var outDir = utils.resolvePath("@tmp/hls");
    var gen = ++castGen;
    stdoutRest = "";
    state = { phase: "starting", url: null, pct: 0, msg: null };
    // Muted mirror: keep mpv playing, silenced, instead of pausing. The page
    // will best-effort seek the TV to startPos once wireless playback begins.
    mirror = newMirror(mpv.getNumber("time-pos") || 0, !!mpv.getFlag("mute"), !!mpv.getFlag("pause"));
    mpv.set("mute", true);

    resolveBinDir(function (binDir, resolveErr) {
      if (gen !== castGen) return; // superseded by a newer cast or a stop
      if (resolveErr) {
        state = { phase: "error", url: null, pct: 0, msg: resolveErr };
        return;
      }
      var helper = binDir + "/airplay-helper";
      var ffmpeg = binDir + "/ffmpeg";
      var serveArgs = [
        "serve",
        "-source", src, "-out", outDir,
        "-ffmpeg", ffmpeg,
        "-parent", String(pid),
        "-duration", String(duration),
        "-vcodec", tracks.vcodec, "-acodec", tracks.acodec,
        "-achannels", String(tracks.achannels),
        "-vmap", String(tracks.vmap), "-amap", String(tracks.amap),
      ];
      if (tracks.sub) {
        if (tracks.sub.path) serveArgs.push("-subpath", tracks.sub.path);
        else serveArgs.push("-smap", String(tracks.sub.smap));
        serveArgs.push("-sublang", tracks.sub.lang, "-subname", tracks.sub.title);
      }
      utils.exec(helper, serveArgs, undefined, function (chunk) {
        if (gen !== castGen) return;
        var parsed = parseHelperEvents(stdoutRest, chunk);
        stdoutRest = parsed.rest;
        for (var i = 0; i < parsed.events.length; i++) {
          var ev = parsed.events[i];
          if (ev.event === "ready") { state.phase = "ready"; state.url = ev.url; }
          else if (ev.event === "progress") { state.pct = ev.pct; }
          else if (ev.event === "packaged") { state.phase = state.phase === "ready" ? "packaged" : state.phase; state.pct = 100; }
          else if (ev.event === "error") { state = { phase: "error", url: null, pct: 0, msg: ev.msg }; }
          else if (ev.event === "stopped") { state = { phase: "idle", url: null, pct: 0, msg: null }; }
        }
      }, function (errChunk) {
        console.log("[helper:stderr] " + errChunk.trim());
      }).then(function () {
        if (gen !== castGen) return;
        if (state.phase !== "error") state = { phase: "idle", url: null, pct: 0, msg: null };
      }).catch(function (e) {
        invalidateBinDirCache(); // helper failed to start; don't trust the cached dir next time
        if (gen !== castGen) return;
        state = { phase: "error", url: null, pct: 0, msg: String(e) };
      });
    });
  }

  function getIINAPid() {
    // mpv exposes the process pid of IINA's mpv core, which lives inside IINA
    // itself (libmpv), so it IS IINA's pid.
    return mpv.getNumber("pid");
  }

  function stopCast() {
    endMirror();
    castGen++; // invalidate any in-flight cast's callbacks
    resolveBinDir(function (binDir, resolveErr) {
      if (resolveErr) { console.log("[stopCast] " + resolveErr); return; }
      utils.exec(binDir + "/airplay-helper", ["stop", "-out", utils.resolvePath("@tmp/hls")]);
    });
    state = { phase: "idle", url: null, pct: 0, msg: null };
  }

  function openSidebar() {
    sidebar.loadFile("sidebar.html"); // clears message listeners — register after
    sidebar.onMessage("getState", function () {
      reapMirror();
      sidebar.postMessage("state", stateForPage()); // onMessage handlers are main-thread safe
    });
    // The page owns starting too, not just stopping: stopCast() tears the whole
    // pipeline down, so without this the sidebar would be a dead end and the
    // only way back to a cast would be re-running the menu item.
    sidebar.onMessage("start", function () {
      startCast();
      sidebar.postMessage("state", stateForPage());
    });
    sidebar.onMessage("stop", function () {
      stopCast();
      sidebar.postMessage("state", stateForPage());
    });
    sidebar.onMessage("tvState", function (tv) {
      reapMirror(); // a helper-initiated teardown may have left a stale mirror
                    // un-reaped; don't mpv.set against a dead stream.
      if (mirror === null || !tv) return;
      var r = mirrorOnTvState(mirror, tv, mpv.getNumber("time-pos") || 0,
                              !!mpv.getFlag("pause"), Date.now());
      mirror = r.m;
      if (r.teardown) { stopCast(); return; }
      if (r.setMpvPaused !== null) mpv.set("pause", r.setMpvPaused);
      if (r.setMpvPos !== null) mpv.set("time-pos", r.setMpvPos);
    });
    sidebar.show();
  }

  menu.addItem(menu.item("Cast to TV", function () {
    openSidebar();  // main-thread menu callback: safe
    startCast();    // helper events only mutate `state`; the page polls
  }));
}
