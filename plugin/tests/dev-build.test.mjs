// `make dev` loads plugin/ into IINA next to the installed plugin. That only
// works if the two never share an identifier: IINA renames a duplicate with a
// random UUID on every launch, which resets its settings and disables it. So
// the dev build gets its own manifest (packaging/dev-manifest.mjs), and each
// copy finds its own bin/ by its own identifier (main.js, resolveBinDir).
import { test } from "node:test";
import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { execFileSync } from "node:child_process";
import {
  mkdtempSync, mkdirSync, readFileSync, writeFileSync, copyFileSync,
  symlinkSync, lstatSync, chmodSync, rmSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { devManifest, writeDevManifest } from "../../packaging/dev-manifest.mjs";

const require = createRequire(import.meta.url);
const { BIN_DIR_LOOKUP_SCRIPT, identifierPattern } = require("../main.js");

const ROOT_INFO = fileURLToPath(new URL("../../Info.json", import.meta.url));
const info = JSON.parse(readFileSync(ROOT_INFO, "utf8"));

function scratch(t) {
  const d = mkdtempSync(join(tmpdir(), "iina-airplay-dev-"));
  t.after(() => rmSync(d, { recursive: true, force: true }));
  return d;
}

test("the dev manifest has its own identifier, and IINA accepts it", () => {
  const dev = devManifest(info);
  assert.notEqual(dev.identifier, info.identifier);
  // IINA's idRegex (JavascriptPlugin.swift); a non-matching identifier makes
  // IINA refuse to load the plugin at all.
  assert.match(dev.identifier, /^([\w-]+\.)+[\w-]+$/);
});

test("the dev build is labelled as such in Settings and in the sidebar", () => {
  const dev = devManifest(info);
  assert.notEqual(dev.name, info.name);
  assert.ok(dev.name.startsWith(info.name));
  assert.notEqual(dev.sidebarTab.name, info.sidebarTab.name);
});

test("the dev manifest has no ghRepo or ghVersion, so IINA never update-checks it", () => {
  const dev = devManifest(info);
  assert.equal("ghRepo" in dev, false);
  assert.equal("ghVersion" in dev, false);
});

test("everything else in the dev manifest is the root manifest's", () => {
  const dev = devManifest(info);
  const changed = new Set(["identifier", "name", "sidebarTab", "ghRepo", "ghVersion"]);
  for (const key of Object.keys(info)) {
    if (changed.has(key)) continue;
    assert.deepEqual(dev[key], info[key], `dev manifest's ${key} differs from the root manifest's`);
  }
});

test("devManifest leaves the root manifest object alone", () => {
  const before = JSON.stringify(info);
  devManifest(info);
  assert.equal(JSON.stringify(info), before);
});

// Before dev-manifest.mjs, `make dev` made plugin/Info.json a symlink to
// ../Info.json, and existing checkouts still have it. A plain write would
// follow the link and put the dev identifier into the real manifest.
test("writing over the old plugin/Info.json symlink replaces the link, not the root manifest", (t) => {
  const d = scratch(t);
  mkdirSync(join(d, "plugin"));
  copyFileSync(ROOT_INFO, join(d, "Info.json"));
  symlinkSync("../Info.json", join(d, "plugin", "Info.json"));

  writeDevManifest(join(d, "Info.json"), join(d, "plugin", "Info.json"));

  assert.equal(readFileSync(join(d, "Info.json"), "utf8"), readFileSync(ROOT_INFO, "utf8"),
    "the root manifest was modified");
  assert.equal(lstatSync(join(d, "plugin", "Info.json")).isSymbolicLink(), false);
  const written = JSON.parse(readFileSync(join(d, "plugin", "Info.json"), "utf8"));
  assert.equal(written.identifier, devManifest(info).identifier);
});

// The real shell script, against a plugins folder laid out the way IINA's is
// with both copies present: the installed plugin as a directory, the dev
// build as the symlink `iina-plugin link` creates. The installed copy sorts
// first in the glob, so the dev lookup has to skip it.
function pluginsFolder(t) {
  const d = scratch(t);
  const plugins = join(d, "plugins");
  const checkout = join(d, "checkout");

  const installed = join(plugins, `${info.identifier}.iinaplugin`);
  mkdirSync(join(installed, "bin"), { recursive: true });
  copyFileSync(ROOT_INFO, join(installed, "Info.json"));

  mkdirSync(join(checkout, "plugin", "bin"), { recursive: true });
  copyFileSync(ROOT_INFO, join(checkout, "Info.json"));
  writeDevManifest(join(checkout, "Info.json"), join(checkout, "plugin", "Info.json"));
  symlinkSync(join(checkout, "plugin"), join(plugins, "iina-airplay.iinaplugin-dev.iinaplugin-dev"));

  for (const bin of [join(installed, "bin"), join(checkout, "plugin", "bin")]) {
    for (const name of ["airplay-helper", "ffmpeg"]) {
      writeFileSync(join(bin, name), "#!/bin/sh\n");
      chmodSync(join(bin, name), 0o755);
    }
  }
  return { plugins, installed, devLink: join(plugins, "iina-airplay.iinaplugin-dev.iinaplugin-dev") };
}

function lookup(plugins, identifier) {
  try {
    const out = execFileSync("/bin/sh", ["-c", BIN_DIR_LOOKUP_SCRIPT, "sh", plugins, identifierPattern(identifier)],
      { encoding: "utf8", env: {} });
    return { status: 0, stdout: out };
  } catch (e) {
    return { status: e.status, stdout: e.stdout };
  }
}

test("with both copies installed, each finds its own bin dir", (t) => {
  const { plugins, installed, devLink } = pluginsFolder(t);
  assert.deepEqual(lookup(plugins, info.identifier), { status: 0, stdout: `${installed}/bin` });
  assert.deepEqual(lookup(plugins, devManifest(info).identifier), { status: 0, stdout: `${devLink}/bin` });
});

test("the installed identifier does not match the dev manifest", (t) => {
  const { plugins, installed } = pluginsFolder(t);
  rmSync(installed, { recursive: true });
  assert.equal(lookup(plugins, info.identifier).status, 1);
});
