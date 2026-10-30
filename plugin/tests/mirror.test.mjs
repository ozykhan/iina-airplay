// The muted-mirror sync core (spec 2026-08-30): the TV is the clock, mpv the
// mirror. These are the only functions where sync correctness lives, so every
// branch — echo suppression both ways, ack handling, drift — is pinned here.
import { test } from "node:test";
import assert from "node:assert/strict";
import { createRequire } from "node:module";
const require = createRequire(import.meta.url);
const { newMirror, mirrorOnMpvPause, mirrorOnMpvSeek, mirrorOnMpvSpeed, mirrorOnTvState,
        DRIFT_TOLERANCE, ECHO_WINDOW_MS, RATE_WINDOW_S } = require("../main.js");

const tv = (over = {}) => ({
  appliedSeq: 0, pos: 0, paused: false, wireless: true, ended: false,
  rate: 1, seeking: false, at: 0, ...over,
});

// The TV playing at tvRate for `secs` seconds, sampled every 500 ms the way
// the page polls, with the page's playbackRate following the requested rate.
// Returns the last result and every speed main.js was told to set in mpv.
function play(m, tvRate, secs, over = {}) {
  let r = { m };
  const speedSets = [];
  for (let ms = 0; ms <= secs * 1000; ms += 500) {
    const pos = 100 + tvRate * ms / 1000;
    r = mirrorOnTvState(r.m, tv({ pos, at: ms, rate: r.m.rate, ...over }), pos, false, ms);
    if (r.setMpvSpeed !== null) speedSets.push(r.setMpvSpeed);
  }
  return { r, speedSets };
}

test("newMirror snapshots start position, saved mute, current pause and speed", () => {
  const m = newMirror(42, true, false, 1.25);
  assert.deepEqual(m, {
    seq: 0, paused: false, seekTo: null, startPos: 42, savedMute: true,
    expectMpvPause: null, lastSetPos: null,
    rate: 1.25, expectMpvSpeed: null, ignoredRate: null, rateProbe: null,
  });
});

test("newMirror without a usable speed requests 1x", () => {
  assert.equal(newMirror(0, false, false).rate, 1);
  assert.equal(newMirror(0, false, false, 0).rate, 1);
});

// ---- mirrorOnMpvPause ----

test("a user pause bumps seq and records the desired state", () => {
  const m = mirrorOnMpvPause(newMirror(0, false, false), true);
  assert.equal(m.seq, 1);
  assert.equal(m.paused, true);
});

test("an expected echo of our own mpv.set(pause) is swallowed", () => {
  const m0 = { ...newMirror(0, false, false), expectMpvPause: true };
  const m = mirrorOnMpvPause(m0, true);
  assert.equal(m.seq, 0, "an echo must not become a command back to the TV");
  assert.equal(m.expectMpvPause, null, "the echo flag is single-use");
});

test("a user change that contradicts the expected echo still wins", () => {
  // We set mpv paused (expect echo true), but the user un-paused first.
  const m0 = { ...newMirror(0, false, false), paused: true, expectMpvPause: true };
  const m = mirrorOnMpvPause(m0, false);
  assert.equal(m.seq, 1);
  assert.equal(m.paused, false);
  assert.equal(m.expectMpvPause, null);
});

test("a pause event matching the desired state is a no-op", () => {
  const m0 = { ...newMirror(0, false, false), paused: true, seq: 3 };
  const m = mirrorOnMpvPause(m0, true);
  assert.equal(m.seq, 3);
});

// ---- mirrorOnMpvSeek ----

test("a user seek bumps seq and sets seekTo with its own seq", () => {
  const m = mirrorOnMpvSeek(newMirror(0, false, false), 300, 1000);
  assert.equal(m.seq, 1);
  assert.deepEqual(m.seekTo, { seq: 1, pos: 300 });
});

test("a seek echoing our own drift correction is swallowed", () => {
  const m0 = { ...newMirror(0, false, false), lastSetPos: { pos: 100, at: 1000 } };
  const m = mirrorOnMpvSeek(m0, 100.5, 1000 + ECHO_WINDOW_MS - 1);
  assert.equal(m.seq, 0);
  assert.equal(m.seekTo, null);
  assert.equal(m.lastSetPos, null, "the echo record is single-use");
});

test("an old lastSetPos outside the echo window does not mask a user seek", () => {
  const m0 = { ...newMirror(0, false, false), lastSetPos: { pos: 100, at: 1000 } };
  const m = mirrorOnMpvSeek(m0, 100.5, 1000 + ECHO_WINDOW_MS + 1);
  assert.deepEqual(m.seekTo, { seq: 1, pos: 100.5 });
});

test("a seek far from lastSetPos is a user seek even inside the window", () => {
  const m0 = { ...newMirror(0, false, false), lastSetPos: { pos: 100, at: 1000 } };
  const m = mirrorOnMpvSeek(m0, 100 + DRIFT_TOLERANCE + 0.1, 1100);
  assert.deepEqual(m.seekTo, { seq: 1, pos: 100 + DRIFT_TOLERANCE + 0.1 });
});

// ---- mirrorOnTvState ----

test("ended tears the cast down", () => {
  const r = mirrorOnTvState(newMirror(0, false, false), tv({ ended: true }), 0, false, 0);
  assert.equal(r.teardown, true);
});

test("ended without a wireless target does not tear down", () => {
  // A short file's LOCAL (muted, hidden) playback ending before the user
  // ever picks a TV must not kill the cast.
  const r = mirrorOnTvState(newMirror(0, false, false), tv({ ended: true, wireless: false }), 0, false, 0);
  assert.equal(r.teardown, false);
  assert.equal(r.setMpvPos, null);
  assert.equal(r.setMpvPaused, null);
});

test("no wireless target means no clock to follow: no actions", () => {
  const r = mirrorOnTvState(newMirror(0, false, false), tv({ wireless: false, pos: 90 }), 10, false, 0);
  assert.equal(r.setMpvPos, null);
  assert.equal(r.setMpvPaused, null);
  assert.equal(r.teardown, false);
});

test("commands in flight (appliedSeq < seq) suspend all reconciliation", () => {
  const m0 = { ...newMirror(0, false, false), seq: 2, paused: true };
  const r = mirrorOnTvState(m0, tv({ appliedSeq: 1, pos: 90, paused: false }), 10, true, 0);
  assert.equal(r.setMpvPos, null, "drift must not be corrected against a stale TV state");
  assert.equal(r.setMpvPaused, null);
});

test("an ack clears the pending seekTo", () => {
  const m0 = { ...newMirror(0, false, false), seq: 1, seekTo: { seq: 1, pos: 300 } };
  const r = mirrorOnTvState(m0, tv({ appliedSeq: 1, pos: 300 }), 300, false, 0);
  assert.equal(r.m.seekTo, null);
});

test("an ack clears seekTo even before wireless playback starts", () => {
  const m0 = { ...newMirror(0, false, false), seq: 1, seekTo: { seq: 1, pos: 300 } };
  const r = mirrorOnTvState(m0, tv({ appliedSeq: 1, wireless: false }), 300, false, 0);
  assert.equal(r.m.seekTo, null);
  assert.equal(r.setMpvPos, null);
  assert.equal(r.setMpvPaused, null);
  assert.equal(r.teardown, false);
});

test("a TV-remote pause is mirrored into mpv with the echo flag armed", () => {
  const r = mirrorOnTvState(newMirror(0, false, false), tv({ paused: true, pos: 10 }), 10, false, 0);
  assert.equal(r.setMpvPaused, true);
  assert.equal(r.m.paused, true, "desired state follows the TV so no command echoes back");
  assert.equal(r.m.expectMpvPause, true);
});

test("drift beyond tolerance corrects mpv to the TV clock and records the set", () => {
  const r = mirrorOnTvState(newMirror(0, false, false), tv({ pos: 50 }), 42, false, 7777);
  assert.equal(r.setMpvPos, 50);
  assert.deepEqual(r.m.lastSetPos, { pos: 50, at: 7777 });
});

test("drift within tolerance is left alone", () => {
  const r = mirrorOnTvState(newMirror(0, false, false), tv({ pos: 43 }), 42, false, 0);
  assert.equal(r.setMpvPos, null);
});

// ---- playback speed (issue #48) ----

test("a user speed change becomes the requested TV rate without a seq bump", () => {
  // The page applies the rate on every poll, not through the seq/ack path,
  // so a speed change must not hold back pause and drift reconciliation.
  const m = mirrorOnMpvSpeed(newMirror(0, false, false, 1), 1.25);
  assert.equal(m.rate, 1.25);
  assert.equal(m.seq, 0);
});

test("an expected echo of our own speed set is swallowed", () => {
  const m0 = { ...newMirror(0, false, false, 1), expectMpvSpeed: 1, ignoredRate: 1.25 };
  const m = mirrorOnMpvSpeed(m0, 1);
  assert.equal(m.expectMpvSpeed, null, "the echo flag is single-use");
  assert.equal(m.ignoredRate, 1.25, "an echo is not a new request");
});

test("a new speed supersedes one the TV ignored", () => {
  const m0 = { ...newMirror(0, false, false, 1), ignoredRate: 1.25 };
  const m = mirrorOnMpvSpeed(m0, 1.5);
  assert.equal(m.rate, 1.5);
  assert.equal(m.ignoredRate, null, "stopping the cast must not undo the user's newer speed");
});

test("a TV that plays at 1x when asked for 1.25x drops mpv to 1x", () => {
  const { r, speedSets } = play(newMirror(0, false, false, 1.25), 1, RATE_WINDOW_S + 1);
  assert.deepEqual(speedSets, [1], "mpv follows the TV's clock, once");
  assert.equal(r.m.rate, 1);
  assert.equal(r.m.ignoredRate, 1.25, "kept for the sidebar note and for restoring on stop");
  assert.equal(r.m.expectMpvSpeed, 1);
});

test("a TV that follows the requested speed is left alone", () => {
  const { r, speedSets } = play(newMirror(0, false, false, 1.25), 1.25, RATE_WINDOW_S * 3);
  assert.deepEqual(speedSets, []);
  assert.equal(r.m.rate, 1.25);
  assert.equal(r.m.ignoredRate, null);
});

test("a slower requested speed the TV ignores is caught too", () => {
  const { speedSets } = play(newMirror(0, false, false, 0.5), 1, RATE_WINDOW_S + 1);
  assert.deepEqual(speedSets, [1]);
});

test("no verdict before a full window of playback", () => {
  const { speedSets } = play(newMirror(0, false, false, 1.25), 1, RATE_WINDOW_S - 1);
  assert.deepEqual(speedSets, []);
});

test("samples before the page applied the rate are not measured", () => {
  // The TV is still at the old rate until the page sets playbackRate.
  const { speedSets } = play(newMirror(0, false, false, 1.25), 1, RATE_WINDOW_S * 2, { rate: 1 });
  assert.deepEqual(speedSets, []);
});

test("a pause inside the window is not measured as slow playback", () => {
  // 8 s at 1.25x then 2 s paused averages exactly 1x over 10 s.
  let r = { m: newMirror(0, false, false, 1.25) };
  const speedSets = [];
  for (let ms = 0; ms <= 14000; ms += 500) {
    const paused = ms > 8000 && ms <= 10000;
    const pos = 100 + 1.25 * (Math.min(ms, 8000) + Math.max(0, ms - 10000)) / 1000;
    r = mirrorOnTvState(r.m, tv({ pos, at: ms, paused, rate: r.m.rate }), pos, paused, ms);
    if (r.setMpvSpeed !== null) speedSets.push(r.setMpvSpeed);
  }
  assert.deepEqual(speedSets, []);
});

test("a seeking TV is not measured", () => {
  const { speedSets } = play(newMirror(0, false, false, 1.25), 1, RATE_WINDOW_S * 2, { seeking: true });
  assert.deepEqual(speedSets, []);
});

test("at 1x there is nothing to measure", () => {
  const m = play(newMirror(0, false, false, 1), 1, RATE_WINDOW_S * 2).r.m;
  assert.equal(m.rateProbe, null);
});

test("a jump in the TV's position does not count as playback", () => {
  // A TV-remote skip of 30 s inside the window would otherwise read as the
  // TV racing ahead (here: 4x over the window, which is no verdict at all).
  let r = { m: newMirror(0, false, false, 1.25) };
  const speedSets = [];
  for (let ms = 0; ms <= (RATE_WINDOW_S + 0.5) * 1000; ms += 500) {
    const pos = 100 + ms / 1000 + (ms >= 3000 ? 30 : 0);
    r = mirrorOnTvState(r.m, tv({ pos, at: ms, rate: r.m.rate }), pos, false, ms);
    if (r.setMpvSpeed !== null) speedSets.push(r.setMpvSpeed);
  }
  assert.deepEqual(speedSets, [1], "with the jump left out the TV is still at 1x");
});

test("losing the TV or a command in flight restarts the measurement", () => {
  const m0 = { ...newMirror(0, false, false, 1.25),
               rateProbe: { pos: 100, at: 0, dpos: 9, dt: 9 } };
  assert.equal(mirrorOnTvState(m0, tv({ wireless: false, rate: 1.25, at: 500 }), 0, false, 0).m.rateProbe, null);
  const inFlight = { ...m0, seq: 1 };
  assert.equal(mirrorOnTvState(inFlight, tv({ rate: 1.25, at: 500 }), 0, false, 0).m.rateProbe, null);
});

test("the mirror functions never mutate their inputs", () => {
  const m0 = { ...newMirror(0, false, false, 1.25), rateProbe: { pos: 0, at: 0, dpos: 9.9, dt: 9.9 } };
  const frozen = JSON.stringify(m0);
  mirrorOnMpvPause(m0, true);
  mirrorOnMpvSeek(m0, 10, 0);
  mirrorOnMpvSpeed(m0, 2);
  mirrorOnTvState(m0, tv({ pos: 99, paused: true }), 0, false, 0);
  mirrorOnTvState(m0, tv({ pos: 0.5, rate: 1.25, at: 500 }), 0, false, 0);
  assert.equal(JSON.stringify(m0), frozen);
});
