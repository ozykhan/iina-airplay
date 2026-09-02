# GitHub Pages Landing Page Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Publish a one-screen landing page for the plugin at `https://ozykhan.github.io/iina-airplay/`, served by GitHub Pages from the `docs/` folder of `master`.

**Architecture:** One hand-written `docs/index.html` with inline CSS and no JavaScript, plus a committed MP4 demo and poster. A `.nojekyll` marker turns Jekyll off so the design docs (which contain `{{ }}`) cannot fail the Pages build. Pages is enabled through the GitHub API after the branch merges, because `docs/` already exists on `master` and enabling it earlier would make Jekyll attempt (and fail) a build of the markdown there.

**Tech Stack:** Static HTML/CSS, ffmpeg (Homebrew, for the one-time encode of the demo recording), `gh` CLI for repo settings.

Spec: `docs/superpowers/specs/2026-09-02-github-pages-landing-design.md`

## Global Constraints

- Pages source is `master` / `/docs`. No deploy workflow, no `gh-pages` branch, no custom domain.
- `docs/index.html` loads nothing from outside the repo: no external fonts, scripts, stylesheets or images. The only outbound links are anchors to GitHub.
- The page carries no version number.
- Demo asset is `docs/demo.mp4` (1200×546, H.264 yuv420p, 30 fps, faststart, ~2.2 MB) with `docs/demo-poster.jpg` (1200×546), both encoded from the maintainer's original screen recording `~/Desktop/sintel.mov` (2150×980, 13.7 s). Neither the 43 MB recording nor the README's 9.5 MB GIF is ever committed.
- Palette: light by default, dark under `prefers-color-scheme: dark`, both as custom properties on `:root`. Single column under 720 px. No horizontal scroll at any width.
- System font stack: `-apple-system, BlinkMacSystemFont, "SF Pro Text", "Helvetica Neue", Helvetica, Arial, sans-serif`; monospace `"SF Mono", Menlo, Consolas, monospace`.
- Commit messages end with `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>`.
- Branch: work on the current feature branch (`claude/iina-plugin-github-pages-3b6d35`), never directly on `master`. `master` is what IINA's update beacon reads.
- There is no automated test for a static HTML file in this repo; verification is the browser render and `curl` checks written into each task. `make test` must still pass at the end (nothing it covers is touched).

---

### Task 1: Demo assets and the Jekyll switch

**Files:**
- Create: `docs/demo.mp4`
- Create: `docs/demo-poster.jpg`
- Create: `docs/.nojekyll`

**Interfaces:**
- Produces: `docs/demo.mp4` (1200×546) and `docs/demo-poster.jpg` (1200×546), referenced by relative path from `docs/index.html` in Task 2.

- [ ] **Step 1: Locate the source recording**

The demo is encoded from the maintainer's original screen recording, not from
the README's GIF (which is 256-colour and dithered). It lives outside the
repo and is never committed:

```bash
ls -la ~/Desktop/sintel.mov
ffprobe -v error -select_streams v:0 -show_entries stream=codec_name,width,height -show_entries format=duration -of default=noprint_wrappers=1 ~/Desktop/sintel.mov
```

Expected: about 43 MB, `h264`, `2150`×`980`, duration `13.680000`. If the file
is missing, stop and ask the maintainer for it; do not fall back to the GIF.

- [ ] **Step 2: Encode the MP4 and extract the poster**

```bash
ffmpeg -v error -y -i ~/Desktop/sintel.mov \
  -vf "scale=1200:-2,fps=30" -an \
  -c:v libx264 -crf 24 -preset slow -pix_fmt yuv420p -movflags +faststart \
  docs/demo.mp4
ffmpeg -v error -y -i ~/Desktop/sintel.mov -vf "scale=1200:-2" -frames:v 1 -q:v 3 docs/demo-poster.jpg
```

`ffmpeg` is Homebrew's (`brew install ffmpeg`, already required by `make dev`).
`scale=1200:-2` keeps the aspect ratio and rounds the height to an even
number, which `yuv420p` requires. `fps=30` drops the recording's variable
frame rate to a steady 30. `-an` drops the (empty) audio track.

- [ ] **Step 3: Check the results**

```bash
ls -la docs/demo.mp4 docs/demo-poster.jpg
ffprobe -v error -select_streams v:0 \
  -show_entries stream=codec_name,width,height,pix_fmt,r_frame_rate -show_entries format=duration \
  -of default=noprint_wrappers=1 docs/demo.mp4
file docs/demo-poster.jpg
```

Expected: `demo.mp4` about 2.2 MB, `demo-poster.jpg` about 140 KB, and

```
codec_name=h264
width=1200
height=546
pix_fmt=yuv420p
r_frame_rate=30/1
duration=13.700000
```

Task 2's `<video>` tag uses `width="1200" height="546"`.

- [ ] **Step 4: Create the Jekyll marker**

```bash
touch docs/.nojekyll
```

- [ ] **Step 5: Commit**

```bash
git add docs/demo.mp4 docs/demo-poster.jpg docs/.nojekyll
git commit -m "pages: demo video, poster, and .nojekyll for the /docs source

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 2: The landing page

**Files:**
- Create: `docs/index.html`

**Interfaces:**
- Consumes: `docs/demo.mp4`, `docs/demo-poster.jpg` from Task 1.
- Produces: `docs/index.html`, the Pages entry point. Anchor `#install` is the button target.

- [ ] **Step 1: Write the page**

Write this to `docs/index.html`. The `width`/`height` attributes on the video are 1200×546, matching Task 1's MP4; they only fix the aspect ratio for layout, the CSS scales it.

```html
<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>iina-airplay — cast what IINA is playing to your Apple TV</title>
<meta name="description" content="An IINA plugin that hands the file you're watching to an AirPlay receiver. Remux-first, subtitles included, IINA stays the remote.">
<meta property="og:title" content="iina-airplay">
<meta property="og:description" content="Cast what IINA is playing to your Apple TV. No mirroring, no re-encode.">
<meta property="og:type" content="website">
<meta property="og:url" content="https://ozykhan.github.io/iina-airplay/">
<meta property="og:image" content="https://ozykhan.github.io/iina-airplay/demo-poster.jpg">
<meta property="og:image:width" content="1200">
<meta property="og:image:height" content="546">
<meta property="og:image:alt" content="The iina-airplay sidebar casting a film from IINA to an Apple TV">
<meta name="twitter:card" content="summary_large_image">
<meta name="color-scheme" content="light dark">
<style>
  :root {
    --bg: #f5f5f7;
    --surface: #ffffff;
    --text: #1d1d1f;
    --muted: #6e6e73;
    --rule: #e5e5ea;
    --accent: #0066cc;
    --accent-text: #ffffff;
    --code-bg: #e8e8ed;
    --shadow: 0 12px 32px rgba(0, 0, 0, 0.12);
  }
  @media (prefers-color-scheme: dark) {
    :root {
      --bg: #000000;
      --surface: #1c1c1e;
      --text: #f5f5f7;
      --muted: #a1a1a6;
      --rule: #2c2c2e;
      --accent: #0a84ff;
      --accent-text: #ffffff;
      --code-bg: #2c2c2e;
      --shadow: 0 12px 32px rgba(0, 0, 0, 0.6);
    }
  }

  * { box-sizing: border-box; }
  html { -webkit-text-size-adjust: 100%; }
  body {
    margin: 0;
    background: var(--bg);
    color: var(--text);
    font-family: -apple-system, BlinkMacSystemFont, "SF Pro Text", "Helvetica Neue", Helvetica, Arial, sans-serif;
    font-size: 17px;
    line-height: 1.5;
    -webkit-font-smoothing: antialiased;
  }
  code {
    font-family: "SF Mono", Menlo, Consolas, monospace;
    font-size: 0.9em;
    background: var(--code-bg);
    padding: 0.1em 0.4em;
    border-radius: 6px;
  }
  a { color: var(--accent); text-decoration: none; }
  a:hover { text-decoration: underline; }
  h1, h2 { letter-spacing: -0.02em; line-height: 1.15; margin: 0 0 0.5em; }
  h1 { font-size: 2.6rem; font-weight: 700; }
  h2 { font-size: 1.6rem; font-weight: 600; margin-top: 0; }
  p { margin: 0 0 1em; }
  main, .wrap { max-width: 1040px; margin: 0 auto; padding: 0 24px; }
  section { padding: 56px 0; border-top: 1px solid var(--rule); }
  section:first-of-type { border-top: 0; padding-top: 72px; }

  /* Hero */
  .hero { display: grid; grid-template-columns: 1fr 1.15fr; gap: 40px; align-items: center; }
  .hero .lead { font-size: 1.2rem; color: var(--muted); }
  .cta { display: flex; flex-wrap: wrap; align-items: center; gap: 16px; margin-top: 24px; }
  .button {
    display: inline-block;
    background: var(--accent);
    color: var(--accent-text);
    font-weight: 600;
    padding: 12px 22px;
    border-radius: 10px;
  }
  .button:hover { text-decoration: none; filter: brightness(1.08); }
  .slug { color: var(--muted); font-size: 0.95rem; }
  .demo { border-radius: 12px; overflow: hidden; box-shadow: var(--shadow); background: #000; }
  .demo video { display: block; width: 100%; height: auto; }
  .credit { font-size: 0.8rem; color: var(--muted); margin: 10px 0 0; }
  .facts { font-size: 0.9rem; color: var(--muted); margin: 18px 0 0; }

  /* Feature bullets */
  .features { display: grid; grid-template-columns: 1fr 1fr; gap: 28px 40px; }
  .features h2 { font-size: 1.05rem; font-weight: 600; margin: 0 0 0.3em; }
  .features p { color: var(--muted); margin: 0; }

  /* Limits table */
  table { border-collapse: collapse; width: 100%; background: var(--surface); border: 1px solid var(--rule); border-radius: 12px; overflow: hidden; }
  th, td { text-align: left; padding: 14px 18px; vertical-align: top; border-top: 1px solid var(--rule); }
  tr:first-child th, tr:first-child td { border-top: 0; }
  th { width: 11em; font-weight: 600; white-space: nowrap; }
  .table-wrap { overflow-x: auto; }

  /* Install */
  .path {
    display: block;
    background: var(--surface);
    border: 1px solid var(--rule);
    border-radius: 12px;
    padding: 16px 20px;
    font-family: "SF Mono", Menlo, Consolas, monospace;
    font-size: 0.95rem;
    margin: 0 0 1.2em;
    overflow-x: auto;
    white-space: nowrap;
  }

  /* Footer */
  footer { border-top: 1px solid var(--rule); padding: 32px 0 48px; color: var(--muted); font-size: 0.9rem; }
  footer nav { display: flex; flex-wrap: wrap; gap: 8px 24px; margin-bottom: 12px; }

  @media (max-width: 720px) {
    body { font-size: 16px; }
    h1 { font-size: 2rem; }
    section { padding: 40px 0; }
    section:first-of-type { padding-top: 40px; }
    .hero { grid-template-columns: 1fr; gap: 28px; }
    .hero .demo-col { order: -1; }
    .features { grid-template-columns: 1fr; }
    th { width: auto; white-space: normal; }
  }

  @media (prefers-reduced-motion: reduce) {
    .demo video { display: none; }
    .demo { background: #000 url(demo-poster.jpg) center / cover no-repeat; aspect-ratio: 1200 / 546; }
  }
</style>
</head>
<body>
<main>

  <section class="hero" aria-labelledby="title">
    <div>
      <h1 id="title">Cast what IINA is playing to your Apple&nbsp;TV.</h1>
      <p class="lead">No screen mirroring, no re-encode. The plugin hands the file to the TV, and IINA stays the remote.</p>
      <div class="cta">
        <a class="button" href="#install">Install through IINA</a>
        <span class="slug">Settings → Plugins → Install → <code>ozykhan/iina-airplay</code></span>
      </div>
      <p class="facts">macOS 12+ · any AirPlay 2 receiver · MIT · everything bundled, nothing downloads at runtime</p>
    </div>
    <div class="demo-col">
      <div class="demo">
        <video autoplay muted loop playsinline preload="metadata"
               width="1200" height="546" poster="demo-poster.jpg"
               aria-label="The iina-airplay sidebar casting a film from IINA to an Apple TV">
          <source src="demo.mp4" type="video/mp4">
          <img src="demo-poster.jpg" width="1200" height="546" alt="The iina-airplay sidebar casting a film from IINA to an Apple TV">
        </video>
      </div>
      <p class="credit">Demo footage: <em>Sintel</em> © <a href="https://durian.blender.org">Blender Foundation</a>, <a href="https://creativecommons.org/licenses/by/3.0/">CC BY 3.0</a>.</p>
    </div>
  </section>

  <section aria-label="What it does">
    <div class="features">
      <div>
        <h2>The picture is the file.</h2>
        <p>Remux-first, so in the normal case the TV plays the original bitstream bit-for-bit. Only unusual codecs get re-encoded, through VideoToolbox.</p>
      </div>
      <div>
        <h2>IINA is the remote, both ways.</h2>
        <p>Play, pause and seek in IINA drive the Apple TV; the Siri Remote comes back the other way.</p>
      </div>
      <div>
        <h2>Your subtitles come along.</h2>
        <p>The text track you selected in IINA, embedded or external SRT/ASS, is carried as a WebVTT rendition the TV shows through its own menu.</p>
      </div>
      <div>
        <h2>Nothing to install first.</h2>
        <p>A pinned LGPL ffmpeg build and a small Go helper ship inside the package. No Homebrew, no runtime downloads, works offline.</p>
      </div>
    </div>
  </section>

  <section aria-labelledby="limits">
    <h2 id="limits">Limits, up front</h2>
    <div class="table-wrap">
      <table>
        <tr><th scope="row">macOS 15+</th><td>Grant IINA the <strong>Local Network</strong> permission on first cast, or the TV can't reach the stream.</td></tr>
        <tr><th scope="row">Image subtitles</th><td>PGS/VOBSUB are dropped with a notice rather than burned in. SRT/ASS work.</td></tr>
        <tr><th scope="row">Start position</th><td>Playback starts at the beginning of the file, not your current position. That is the trade for a fully seekable timeline on the TV.</td></tr>
      </table>
    </div>
  </section>

  <section id="install" aria-labelledby="install-title">
    <h2 id="install-title">Install</h2>
    <p>Install <strong>through IINA</strong>, not by downloading the package in a browser:</p>
    <code class="path">IINA → Settings → Plugins → Install → ozykhan/iina-airplay</code>
    <p>IINA fetches the package from the latest GitHub release and extracts it without applying the quarantine flag, so the bundled binaries run under Gatekeeper with their ad-hoc signatures. A package downloaded in a browser and opened by hand is quarantined all the way down, and the plugin will ask you to reinstall through IINA if that happens.</p>
    <p>On macOS 15 and later, grant IINA the <strong>Local Network</strong> permission the first time it casts, or the Apple TV cannot reach the stream.</p>
    <p>Everything the plugin needs ships inside the package. There are no prerequisites and nothing is downloaded at runtime.</p>
  </section>

  <section aria-labelledby="how">
    <h2 id="how">How it works</h2>
    <p>IINA cannot AirPlay its own video output: it renders through mpv into a Metal/OpenGL layer, and macOS only exposes AirPlay video sending through AVFoundation. So this is a <strong>handoff, not a mirror</strong>. The plugin remuxes the current file to a live HLS stream (transcoding only when the codec demands it), serves it on your LAN, and a hidden video element in IINA's sidebar hands the stream URL to the TV through WebKit's own AirPlay picker. IINA keeps playing muted alongside, which is what makes the two-way remote control work.</p>
    <p>The full reasoning and the codec and subtitle handling matrix are in <a href="https://github.com/ozykhan/iina-airplay/blob/master/docs/feasibility.md">docs/feasibility.md</a>.</p>
  </section>

</main>

<footer>
  <div class="wrap">
    <nav aria-label="Project links">
      <a href="https://github.com/ozykhan/iina-airplay">GitHub</a>
      <a href="https://github.com/ozykhan/iina-airplay/releases">Releases</a>
      <a href="https://github.com/ozykhan/iina-airplay/blob/master/CONTRIBUTING.md">Contributing</a>
      <a href="https://github.com/ozykhan/iina-airplay/issues">Issues</a>
    </nav>
    <p>MIT licensed. Not affiliated with IINA or Apple. AirPlay and Apple TV are trademarks of Apple Inc.</p>
  </div>
</footer>
</body>
</html>
```

- [ ] **Step 2: Confirm the page references nothing outside the repo**

```bash
grep -nE 'src=|href=' docs/index.html | grep -vE 'github\.com|durian\.blender\.org|creativecommons\.org|#install|demo\.mp4|demo-poster\.jpg'
```

Expected: no output. Every `src` is a local asset and every `href` is an anchor or one of the three allowed hosts.

- [ ] **Step 3: Confirm the referenced assets exist**

```bash
ls docs/demo.mp4 docs/demo-poster.jpg docs/.nojekyll
```

Expected: all three listed.

- [ ] **Step 4: Render locally and check both widths and both themes**

Open the file in the in-app browser (or Safari):

```bash
open "file://$PWD/docs/index.html"
```

Check, and fix in `index.html` before moving on:
- Desktop (≥1040 px): headline, button, slug and the demo are all visible without scrolling; the video autoplays and loops.
- Phone width (375 px): the demo sits above the headline, one column throughout, and `document.documentElement.scrollWidth === window.innerWidth` (no horizontal scroll). The limits table scrolls inside its own wrapper if it overflows.
- Toggle the system appearance (or emulate `prefers-color-scheme: dark`): background goes black, text goes light, the table and install path use the dark surface colour.
- The "Install through IINA" button scrolls to the Install heading.

- [ ] **Step 5: Commit**

```bash
git add docs/index.html
git commit -m "pages: landing page

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 3: Cross-references in the README and release runbook

**Files:**
- Modify: `README.md:126-136` (the `## Docs` section)
- Modify: `docs/releasing.md:247-` (the `## Rebuilding without releasing` section is the last one; append after it)

**Interfaces:**
- Consumes: the Pages URL `https://ozykhan.github.io/iina-airplay/`.

- [ ] **Step 1: Add the page to the README's Docs list**

In `README.md`, the `## Docs` section currently ends with the `docs/releasing.md` bullet. Add one bullet above `docs/feasibility.md`:

```markdown
- [ozykhan.github.io/iina-airplay](https://ozykhan.github.io/iina-airplay/) — the
  landing page, served by GitHub Pages from `docs/` on `master`
```

- [ ] **Step 2: Add the deployment note to the release runbook**

Append to the end of `docs/releasing.md`:

```markdown

## The landing page is not a release step

`https://ozykhan.github.io/iina-airplay/` is GitHub Pages serving the `docs/`
folder of `master` (`docs/index.html`, plus `docs/.nojekyll` so Jekyll never
tries to build the design docs, which contain `{{ }}` from Actions YAML). It
deploys on every push to `master` and carries no version number, so nothing in
the release choreography touches it and a fix to the page ships like a
manifest fix: merge to `master`, wait a minute, reload.
```

- [ ] **Step 3: Check the edits read correctly**

```bash
sed -n '/^## Docs/,/^$/p' README.md | head -20
tail -12 docs/releasing.md
```

Expected: the new bullet is first in the Docs list; the new section is the last in the runbook and its heading level matches the others (`## `).

- [ ] **Step 4: Commit**

```bash
git add README.md docs/releasing.md
git commit -m "docs: point at the landing page, note it is not a release step

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 4: Ship it: PR, Pages settings, live check

**Files:** none in the repo. Repo settings via `gh`.

**Interfaces:**
- Consumes: all of the above on the feature branch.
- Produces: the live URL.

- [ ] **Step 1: Run the full test suite**

```bash
make test
```

Expected: everything passes. Nothing under `plugin/`, `helper/` or `packaging/` changed, so any failure here is pre-existing; note it and continue.

- [ ] **Step 2: Push and open the PR**

```bash
git push -u origin HEAD
gh pr create --base master --title "Add a GitHub Pages landing page" --body "$(cat <<'EOF'
One-screen landing page at https://ozykhan.github.io/iina-airplay/, served from `docs/` on `master`.

- `docs/index.html`: hand-written, inline CSS, no JS, no external assets, light/dark, single column under 720 px.
- `docs/demo.mp4` + poster: the demo as a 2.2 MB 1200×546 H.264 clip encoded from the original recording (the README's GIF is 9.5 MB and 256-colour).
- `docs/.nojekyll`: Jekyll off. Two design docs contain `{{ }}` from Actions YAML and would fail a Jekyll build.
- README Docs list and the release runbook point at the page.

Pages itself gets enabled on the repo (source `master` / `/docs`) after this merges, since `docs/` already exists on `master` and enabling it earlier would trigger a Jekyll build of the markdown.

Spec: `docs/superpowers/specs/2026-09-02-github-pages-landing-design.md`

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

- [ ] **Step 3: Wait for CI, then squash-merge (repo convention)**

The merge is the maintainer's call. If you are the maintainer:

```bash
gh pr checks --watch
gh pr merge --squash --delete-branch
```

- [ ] **Step 4: Enable Pages from `master` / `/docs`**

Only after the merge, so `docs/.nojekyll` is on `master` before the first build:

```bash
gh api -X POST repos/ozykhan/iina-airplay/pages \
  -f 'source[branch]=master' -f 'source[path]=/docs'
```

Expected: HTTP 201 with a JSON body whose `html_url` is `https://ozykhan.github.io/iina-airplay/`. If it returns 409, Pages was already enabled; use `-X PUT` with the same fields instead.

- [ ] **Step 5: Set the repo's Website field**

```bash
gh api -X PATCH repos/ozykhan/iina-airplay -f homepage=https://ozykhan.github.io/iina-airplay/
gh api repos/ozykhan/iina-airplay --jq '{has_pages, homepage}'
```

Expected: `{"has_pages":true,"homepage":"https://ozykhan.github.io/iina-airplay/"}`.

- [ ] **Step 6: Wait for the first Pages build and check the live site**

```bash
gh api repos/ozykhan/iina-airplay/pages/builds/latest --jq '{status, error}'
```

Poll until `status` is `built` (usually under two minutes). Then:

```bash
curl -sI https://ozykhan.github.io/iina-airplay/ | grep -iE '^(HTTP|content-type)'
curl -sI https://ozykhan.github.io/iina-airplay/demo.mp4 | grep -iE '^(HTTP|content-type|content-length)'
curl -sI https://ozykhan.github.io/iina-airplay/demo-poster.jpg | grep -iE '^(HTTP|content-type)'
```

Expected: all three `HTTP/2 200`; `text/html`, `video/mp4` (length about 2.2 MB), `image/jpeg`.

- [ ] **Step 7: Open the live page once in a browser**

```bash
open https://ozykhan.github.io/iina-airplay/
```

Expected: identical to the local render from Task 2, video autoplaying. Also check `https://ozykhan.github.io/iina-airplay/feasibility.md` returns raw markdown as `text/plain` or `text/markdown`, not a Jekyll-rendered page and not a 404 build error, which confirms `.nojekyll` took effect.
