# LAN address setting

Date: 2026-10-08. Closes issue #33.

## Goal

Let a user choose the LAN address the helper advertises to the Apple TV,
from IINA's own plugin preferences, instead of only from a terminal.

#32 gave the helper a `serve -ip <addr>` flag, but nothing in the plugin
passes it, and the plugin has no settings at all. Auto-detection still picks
the wrong address in two known cases: a VPN whose tunnel interface is not
named `utun*` (`ipsec*`, `ppp*`), and a Mac with private addresses on more
than one `en*` interface on different subnets. Both look the same from the
sidebar: the helper reports `ready`, the hidden `<video>` never loads, and
the AirPlay picker refuses to open, with nothing pointing at the address.

## Non-goals

- No validation in the plugin. The helper already rejects malformed, IPv6,
  loopback, link-local and unassigned addresses and addresses on down
  interfaces, and its messages name the override ("invalid LAN IPv4
  override", "not assigned to this Mac", "interface en1 is down"). They
  arrive as `error` events, which the sidebar already shows.
- No field in the sidebar. The sidebar webview has no preferences bridge,
  so a field there would need its own message plumbing, for a setting most
  users never touch. Considered and dropped; see the issue.
- No hint in the sidebar's error state. The failure this fixes produces no
  error event (the helper says `ready`), so there is nothing to hang it on.
- No version or `ghVersion` bump. That is release work.

## How IINA plugin preferences work

Verified against `iina/iina` at `v1.4.4` and `develop`
(`JavascriptPlugin.swift`, `JavascriptAPIPreferences.swift`,
`PrefPluginViewController.swift`), so both installed IINA lines behave the
same:

- `Info.json` names the page with `"preferencesPage": "<file>"`, resolved
  relative to the plugin root, and gives defaults in `"preferenceDefaults"`,
  a plain object. A missing or malformed `preferenceDefaults` logs a warning
  and yields no defaults.
- The page shows under Settings → Plugins → AirPlay → Preferences. IINA
  injects a script at document end that finds every `input[data-pref-key]`,
  fills it from the stored value (or the default), and stores `input.value`
  on every `change` event. A text input needs no `data-type`. IINA also
  injects a stylesheet with `.pref-section`, `.pref-help`, `.small` and
  `.secondary`, and dark mode.
- Values live in an in-memory dictionary on the plugin object and are
  written to disk when the Preferences tab disappears. The plugin's own
  `iina.preferences.get(key)` is synchronous and reads that same dictionary,
  falling back to the default, so a change in Settings reaches the next cast
  without restarting IINA.

## Design

### Manifest

`Info.json` gains two keys:

```json
"preferencesPage": "preferences.html",
"preferenceDefaults": { "lanIP": "" }
```

Empty string means automatic.

### Preferences page

New file `plugin/preferences.html`. One section, one text input, help text,
no script of our own:

- A label "LAN address" and `<input type="text" data-pref-key="lanIP"
  placeholder="Automatic">`.
- Help text, in `.pref-help`: the IPv4 address the Apple TV uses to reach
  this Mac; leave it empty to detect it automatically; set it when a cast
  says ready but the TV never starts playing, which happens with a VPN
  whose tunnel is not named `utun` or with more than one network interface;
  it must be an address assigned to this Mac, or the next cast refuses it.

Markup uses IINA's injected classes only. No custom CSS beyond what keeps
the input a sensible width.

### Plugin

`plugin/main.js`:

- A new pure, exported function `lanIPOverride(value)`: returns
  `value.trim()` when `value` is a string, and `""` for anything else
  (`null`, `undefined`, a number, any stale non-string that may come back
  from the preferences store).
- `startCast` reads `lanIPOverride(preferences.get("lanIP"))` at the top,
  after the early-return guards and before the asynchronous bin-dir lookup,
  and captures it in a local. `startCast` runs only in menu and onMessage
  contexts (main thread); the capture keeps the preferences call out of the
  exec callback.
- When building `serveArgs`, a non-empty value appends `"-ip", value`. An
  empty value appends nothing, so the helper keeps auto-detecting.

A rejected value makes the helper emit an `error` event; the existing
handler sets `state.phase = "error"` with the helper's message and the
sidebar shows it. Nothing new is needed on that path.

### Packaging

- `packaging/pack.sh`: a `PLUGIN_PREFS="$ROOT/plugin/preferences.html"`
  declaration next to `PLUGIN_SIDEBAR`, included in the source existence
  loop and in the `cp` into the stage root.
- `packaging/verify.sh`: assert `preferences.html` is present and non-empty
  in the package root, and add it to the staleness loop that compares each
  payload file byte-for-byte against `plugin/`.
- `packaging/tests/pack-paths.test.sh`: extend the declaration regex and the
  loop to `PLUGIN_PREFS`, so a moved or renamed page fails `make test`
  instead of `make pack` after the ffmpeg build.
- `packaging/tests/verify.test.sh`: add `preferences.html` to every fixture
  source tree and package, and a "missing preferences.html" failure case
  alongside the existing "missing sidebar.html" one.

### Tests

- `plugin/tests/main.test.mjs`: `lanIPOverride` returns the trimmed string
  for a string, and `""` for `null`, `undefined`, a number and an object.
- `plugin/tests/runtime.test.mjs`: the fake `iina` gains
  `preferences: { get: (k) => (k === "lanIP" ? opts.lanIP : undefined) }`
  with `opts.lanIP` defaulting to `""`. Three cases on the serve args of a
  started cast: no `-ip` when the setting is empty; `-ip` followed by
  `192.168.1.20` when set; the value is trimmed when set with surrounding
  whitespace.
- `plugin/tests/manifest.test.mjs`: `preferencesPage` is a string naming a
  file that exists under `plugin/`, and `preferenceDefaults.lanIP` is the
  empty string.

### Docs

- `docs/distribution.md`, LAN address selection: replace "The plugin has no
  UI setting for it" with a sentence saying the same override is the "LAN
  address" field under Settings → Plugins → AirPlay → Preferences, and that
  the terminal flag remains for manual runs.
- `README.md`: the VPN pointer line mentions the setting as the first thing
  to try.
- `CONTRIBUTING.md`: the `plugin/` row of the layout table lists
  `preferences.html`.

## Acceptance

- With the field empty, `serve` is spawned without `-ip` (unchanged
  behaviour).
- With `192.168.1.20` in the field, `serve` is spawned with
  `-ip 192.168.1.20`, and the ready URL the sidebar loads carries that host.
- With an address not assigned to the Mac, the sidebar shows the helper's
  rejection and the cast ends in the error state.
- `make test` passes, and `make pack` ships `preferences.html` in the
  package root next to `sidebar.html`.
