// plugin/main.js — orchestration. HARD RULE (docs/prototype.md): never call
// sidebar.* from utils.exec callbacks/promises; IINA runs those off the main
// thread and sidebar.show() SIGABRTs the app. Sidebar calls live only in the
// menu callback and onMessage handlers; the page polls "getState".

// ---- pure functions (node-testable) ----

function selectTracks(trackList) {
  var video = null, audio = null, firstAudio = null;
  for (var i = 0; i < trackList.length; i++) {
    var t = trackList[i];
    if (t.type === "video" && (!video || t.selected)) video = t;
    if (t.type === "audio") {
      if (firstAudio === null) firstAudio = t;
      if (t.selected) audio = t;
    }
  }
  if (!video) return null;
  if (!audio) audio = firstAudio;
  if (!audio) return null;
  return {
    vcodec: video.codec,
    acodec: audio.codec,
    achannels: audio["demux-channel-count"] || 2,
    vmap: video["ff-index"],
    amap: audio["ff-index"],
  };
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

if (typeof module !== "undefined") {
  module.exports = { selectTracks: selectTracks, parseHelperEvents: parseHelperEvents };
}

// ---- IINA runtime ----

if (typeof iina !== "undefined") {
  var core = iina.core, mpv = iina.mpv, menu = iina.menu, sidebar = iina.sidebar,
      utils = iina.utils, file = iina.file, console = iina.console;

  var state = { phase: "idle", url: null, pct: 0, msg: null };
  var stdoutRest = "";

  // Resolve our own install dir: @data is <plugins-data>/<identifier>; the
  // plugin itself lives in <.../plugins>/<something>. Locate bin/ relative to
  // the data dir's sibling plugins folder at runtime via a glob through /bin/sh
  // once, then cache it in @data so later launches skip the lookup.
  function resolveBinDir(cb) {
    var cached = null;
    try { cached = file.read("@data/bindir.txt"); } catch (e) {}
    if (cached && cached.trim()) { cb(cached.trim()); return; }
    var script =
      'for d in "$HOME/Library/Application Support/com.colliderli.iina/plugins/"*/; do' +
      '  if grep -qs \'"identifier": *"dev.faruk.iina-airplay"\' "$d/Info.json"; then' +
      '    printf %s "$d"bin; exit 0; fi; done; exit 1';
    utils.exec("/bin/sh", ["-c", script]).then(function (r) {
      if (r.status === 0 && r.stdout) {
        file.write("@data/bindir.txt", r.stdout);
        cb(r.stdout);
      } else {
        state = { phase: "error", url: null, pct: 0, msg: "cannot locate plugin bin directory" };
      }
    });
  }

  function startCast() {
    if (state.phase === "starting" || state.phase === "ready") return;
    var src = mpv.getString("path");
    if (!src || src.charAt(0) !== "/") {
      core.osd("AirPlay: only local files can be cast");
      return;
    }
    var tracks = selectTracks(mpv.getNative("track-list") || []);
    if (!tracks) {
      core.osd("AirPlay: no castable video/audio tracks");
      return;
    }
    var duration = mpv.getNumber("duration") || 0;
    var outDir = utils.resolvePath("@tmp/hls");
    state = { phase: "starting", url: null, pct: 0, msg: null };
    core.pause();

    resolveBinDir(function (binDir) {
      var helper = binDir + "/airplay-helper";
      var ffmpeg = binDir + "/ffmpeg";
      utils.exec(helper, [
        "serve",
        "-source", src, "-out", outDir,
        "-ffmpeg", ffmpeg,
        "-parent", String(getIINAPid()),
        "-duration", String(duration),
        "-vcodec", tracks.vcodec, "-acodec", tracks.acodec,
        "-achannels", String(tracks.achannels),
        "-vmap", String(tracks.vmap), "-amap", String(tracks.amap),
      ], undefined, function (chunk) {
        var parsed = parseHelperEvents(stdoutRest, chunk);
        stdoutRest = parsed.rest;
        for (var i = 0; i < parsed.events.length; i++) {
          var ev = parsed.events[i];
          if (ev.event === "ready") { state.phase = "ready"; state.url = ev.url; }
          else if (ev.event === "progress") { state.pct = ev.pct; }
          else if (ev.event === "packaged") { state.phase = state.phase === "ready" ? "packaged" : state.phase; state.pct = 100; }
          else if (ev.event === "error") { state = { phase: "error", url: null, pct: 0, msg: ev.msg }; }
        }
      }, function (errChunk) {
        console.log("[helper:stderr] " + errChunk.trim());
      }).then(function () {
        if (state.phase !== "error") state = { phase: "idle", url: null, pct: 0, msg: null };
      }).catch(function (e) {
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
    resolveBinDir(function (binDir) {
      utils.exec(binDir + "/airplay-helper", ["stop", "-out", utils.resolvePath("@tmp/hls")]);
    });
    state = { phase: "idle", url: null, pct: 0, msg: null };
  }

  function openSidebar() {
    sidebar.loadFile("sidebar.html"); // clears message listeners — register after
    sidebar.onMessage("getState", function () {
      sidebar.postMessage("state", state); // onMessage handlers are main-thread safe
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
