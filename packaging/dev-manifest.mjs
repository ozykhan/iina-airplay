#!/usr/bin/env node
// Writes the manifest `make dev` puts in plugin/, so IINA can load the plugin
// directory as a development plugin next to an installed copy.
//
//     node packaging/dev-manifest.mjs Info.json plugin/Info.json
//
// A plugin directory must carry its own Info.json for IINA to load it, and the
// real one lives at the repository root, where IINA's update check reads it.
// Copying it verbatim gives the dev link the installed copy's identifier, and
// IINA allows only one: at launch it alerts "duplicated plugin identifiers"
// and appends a random UUID to the second copy's identifier
// (JavascriptPlugin.swift, loadPlugins). The identifier keys the plugin's
// enabled flag, preferences and data directory, so the renamed copy comes up
// disabled, with empty settings, on every launch.
//
// So the dev copy gets an identifier and a name of its own, and loses ghRepo
// and ghVersion. Without them IINA skips its update check for the dev copy
// (checkNewVersion returns nil before fetching). With them it would compare
// the dev copy against master's manifest, and an "update" would try to install
// the release over the dev link and fail on the identifier mismatch.
//
// pack.sh never reads this file: the package ships the root Info.json, and
// verify.sh compares it byte for byte against that one.
import { readFileSync, writeFileSync, renameSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";

export const DEV_ID_SUFFIX = ".dev";
export const DEV_NAME_SUFFIX = " (dev)";

export function devManifest(info) {
  const dev = { ...info };
  dev.identifier = info.identifier + DEV_ID_SUFFIX;
  dev.name = info.name + DEV_NAME_SUFFIX;
  // The sidebar tab is labelled separately from the plugin, and with both
  // copies enabled the player shows two tabs.
  if (info.sidebarTab && typeof info.sidebarTab.name === "string") {
    dev.sidebarTab = { ...info.sidebarTab, name: info.sidebarTab.name + DEV_NAME_SUFFIX };
  }
  delete dev.ghRepo;
  delete dev.ghVersion;
  return dev;
}

// Write through a temporary file and rename it into place. Before this script
// existed, plugin/Info.json was a symlink to ../Info.json, and an older
// checkout still has that link. Writing to the link would follow it and
// overwrite the real manifest with the dev one. rename(2) replaces the link
// itself.
export function writeDevManifest(srcPath, outPath) {
  const dev = devManifest(JSON.parse(readFileSync(srcPath, "utf8")));
  const tmp = outPath + ".tmp";
  writeFileSync(tmp, JSON.stringify(dev, null, 2) + "\n");
  renameSync(tmp, outPath);
  return dev;
}

const invokedDirectly = process.argv[1]
  && import.meta.url === pathToFileURL(realpathSync(process.argv[1])).href;
if (invokedDirectly) {
  const [src, out] = process.argv.slice(2);
  if (!src || !out) {
    console.error("usage: dev-manifest.mjs <root Info.json> <plugin Info.json>");
    process.exit(2);
  }
  const dev = writeDevManifest(src, out);
  console.log(`dev-manifest: wrote ${out} as ${dev.identifier} ("${dev.name}")`);
}
