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
function isLocalSource(p) {
  return !!p && !/^[a-zA-Z][a-zA-Z0-9+.-]*:\/\//.test(p);
}

if (typeof module !== "undefined") {
  module.exports = {
    selectTracks: selectTracks,
    parseHelperEvents: parseHelperEvents,
    pluginsDirFromDataDir: pluginsDirFromDataDir,
    isValidPid: isValidPid,
    isLocalSource: isLocalSource,
  };
}

// ---- IINA runtime ----

if (typeof iina !== "undefined") {
  var core = iina.core, mpv = iina.mpv, menu = iina.menu, sidebar = iina.sidebar,
      utils = iina.utils, file = iina.file, console = iina.console;

  var state = { phase: "idle", url: null, pct: 0, msg: null };
  var stdoutRest = "";

  // Bumped every time a new cast starts or the cast is stopped. Async
  // callbacks (bin-dir resolution, helper stdout, exec .then/.catch) capture
  // the generation they belong to and no-op if it no longer matches — this
  // stops a superseded cast's stale callbacks from clobbering the state of
  // whatever cast (or idle state) came after it.
  var castGen = 0;

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
    var script =
      'for d in "$1"/*/; do' +
      '  if /usr/bin/grep -qs \'"identifier": *"dev.faruk.iina-airplay"\' "$d/Info.json"; then' +
      '    if [ -x "$d"bin/airplay-helper ] && [ -x "$d"bin/ffmpeg ]; then' +
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
        cb(null, "the bundled helper or ffmpeg is missing or not executable; " +
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
    if (state.phase === "starting" || state.phase === "ready" || state.phase === "packaged") return;
    var src = mpv.getString("path");
    if (!src || src.charAt(0) !== "/") {
      core.osd("AirPlay: only local files can be cast");
      return;
    }
    if (!isLocalSource(src)) {
      var streamMsg = "AirPlay can only cast local files, not network streams";
      core.osd("AirPlay: " + streamMsg);
      state = { phase: "error", url: null, pct: 0, msg: streamMsg };
      return;
    }
    var tracks = selectTracks(mpv.getNative("track-list") || []);
    if (!tracks) {
      core.osd("AirPlay: no castable video/audio tracks");
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
    core.pause();

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
      sidebar.postMessage("state", state); // onMessage handlers are main-thread safe
    });
    // The page owns starting too, not just stopping: stopCast() tears the whole
    // pipeline down, so without this the sidebar would be a dead end and the
    // only way back to a cast would be re-running the menu item.
    sidebar.onMessage("start", function () {
      startCast();
      sidebar.postMessage("state", state);
    });
    sidebar.onMessage("stop", function () {
      stopCast();
      sidebar.postMessage("state", state);
    });
    sidebar.show();
  }

  menu.addItem(menu.item("Cast to TV", function () {
    openSidebar();  // main-thread menu callback: safe
    startCast();    // helper events only mutate `state`; the page polls
  }));
}
