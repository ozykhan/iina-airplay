// Info.json is the one file IINA parses before any of our code runs: a bad
// ghRepo makes IINA refuse to load the plugin outright, and a ghVersion of the
// wrong JSON type silently disables update checks. Both are cheap to assert.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const info = JSON.parse(readFileSync(new URL("../Info.json", import.meta.url), "utf8"));

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
