// The muted-mirror sync core (spec 2026-08-30): the TV is the clock, mpv the
// mirror. These are the only functions where sync correctness lives, so every
// branch — echo suppression both ways, ack handling, drift — is pinned here.
import { test } from "node:test";
import assert from "node:assert/strict";
import { createRequire } from "node:module";
const require = createRequire(import.meta.url);
const { newMirror, mirrorOnMpvPause, mirrorOnMpvSeek, mirrorOnTvState,
        DRIFT_TOLERANCE, ECHO_WINDOW_MS } = require("../main.js");

const tv = (over = {}) => ({
  appliedSeq: 0, pos: 0, paused: false, wireless: true, ended: false, ...over,
});

test("newMirror snapshots start position, saved mute, and current pause", () => {
  const m = newMirror(42, true, false);
  assert.deepEqual(m, {
    seq: 0, paused: false, seekTo: null, startPos: 42, savedMute: true,
    expectMpvPause: null, lastSetPos: null,
  });
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

test("the mirror functions never mutate their inputs", () => {
  const m0 = newMirror(0, false, false);
  const frozen = JSON.stringify(m0);
  mirrorOnMpvPause(m0, true);
  mirrorOnMpvSeek(m0, 10, 0);
  mirrorOnTvState(m0, tv({ pos: 99, paused: true }), 0, false, 0);
  assert.equal(JSON.stringify(m0), frozen);
});
