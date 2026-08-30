// Info.json is the one file IINA parses before any of our code runs: a bad
// ghRepo makes IINA refuse to load the plugin outright, and a ghVersion of the
// wrong JSON type silently disables update checks. Both are cheap to assert.
//
// Its LOCATION is equally load-bearing, and was the harder lesson. IINA's
// update check (JavascriptPlugin.swift, checkForUpdates at v1.4.4) fetches
//
//     https://raw.githubusercontent.com/<ghRepo>/master/Info.json
//
// — the REPOSITORY root of master, not the release, and not the package. While
// this file lived at plugin/Info.json that URL 404'd, and IINA 1.4.4 folds a
// failed fetch and "no newer version" into the same branch, so the failure
// showed up as "No update found." with no error. The release was fine; the
// manifest was simply not where IINA looks. Hence the root path below.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

// Two levels up from plugin/tests/ — the repository root, the one location
// IINA's update check can see.
const infoURL = new URL("../../Info.json", import.meta.url);
const info = JSON.parse(readFileSync(infoURL, "utf8"));

test("the manifest sits at the repository root, where IINA's update check reads it", () => {
  // Reading it above would already have thrown; this states the requirement
  // so a future move fails with the reason rather than with ENOENT.
  assert.ok(info, "Info.json must be at the repository root, not under plugin/");
});

test("ghRepo matches IINA's githubRepoRegex", () => {
  assert.equal(typeof info.ghRepo, "string");
  assert.match(info.ghRepo, /^[\w-]+\/[\w-]+$/);
});

test("ghVersion is an integer, not the semver string", () => {
  assert.equal(typeof info.ghVersion, "number");
  assert.ok(Number.isInteger(info.ghVersion), "IINA casts ghVersion as? Int");
});

test("version is a semver string, kept separate from ghVersion", () => {
  assert.match(info.version, /^\d+\.\d+\.\d+$/);
});

test("the entry file named by the manifest is packaged from plugin/", () => {
  // pack.sh copies the manifest to the package root alongside plugin/main.js,
  // so entry is resolved inside the PACKAGE, not next to this file.
  assert.equal(typeof info.entry, "string");
  assert.ok(info.entry.length > 0, "IINA needs an entry to load anything");
});
