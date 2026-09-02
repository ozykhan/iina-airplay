# GitHub Pages landing page — design

**Date:** 2026-09-02
**Status:** approved

## Goal

A single landing page at `https://ozykhan.github.io/iina-airplay/` for someone
who found the plugin and wants to know "does this do what I want, and how do I
get it?" It is not a docs site. The design docs stay on GitHub.

## Decisions taken

| Question          | Decision                                                | Why                                                                                                                                      |
| ----------------- | ------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------- |
| Scope             | Landing page only                                       | One audience action (type the slug into IINA); the design docs already read fine on GitHub                                               |
| Pages source      | `master` branch, `/docs` folder                         | No workflow, no extra branch. A fix to the page ships like a manifest fix: a plain push to `master`                                       |
| URL               | Default `ozykhan.github.io/iina-airplay`                | Nothing to configure beyond enabling Pages                                                                                               |
| Look              | Light macOS: off-white, system font, text left/demo right | Matches IINA itself; slug and demo both above the fold on a laptop                                                                     |
| Jekyll            | Off, via `docs/.nojekyll`                               | Two files under `docs/superpowers/` contain `{{ }}` from Actions YAML; Liquid would fail the Pages build. Static serving sidesteps it     |
| Demo asset        | MP4 + poster committed under `docs/`, not the GIF       | The README's GIF is 9.5 MB; the same clip as H.264 is 1.1 MB. `<video autoplay muted loop playsinline>` behaves like a GIF, iPhone included |
| Build step        | None                                                    | Hand-written HTML, inline CSS, no JS, no external fonts or scripts                                                                        |

## Files

```
docs/
  index.html         the whole page
  demo.mp4           800×365, H.264 yuv420p, faststart, ~1.1 MB, 12.7 s loop
  demo-poster.jpg    first frame of demo.mp4, shown until the video plays
  .nojekyll          empty; disables Jekyll for the /docs source
```

The MP4 is produced once from the README's GIF with ffmpeg
(`-c:v libx264 -crf 23 -preset slow -pix_fmt yuv420p -movflags +faststart`,
dimensions rounded down to even). It is not part of any build; it is a committed
asset. Regenerate by hand if the demo footage changes.

## Page content, top to bottom

1. **Hero.** Headline "Cast what IINA is playing to your Apple TV." Two-sentence
   pitch: no screen mirroring, no re-encode; the plugin hands the file to the TV
   and IINA stays the remote. An "Install through IINA" button that anchors to
   the install section. The slug `ozykhan/iina-airplay` in a code chip. The demo
   video on the right; on narrow screens it stacks above the text.
2. **Four feature bullets**, taken from the README: the picture is the file;
   IINA is the remote, both ways; your subtitles come along; nothing to install
   first.
3. **Limits, up front.** The README's three-row table unchanged: macOS 15+ Local
   Network permission, image subtitles dropped, playback starts at the beginning.
4. **Install.** The Settings path (IINA → Settings → Plugins → Install → slug),
   why to install through IINA and not by browser download (quarantine), and the
   macOS 15+ Local Network note.
5. **How it works.** Three or four sentences: handoff, not a mirror; remux to
   HLS; hidden `<video>` hands the URL to the TV through WebKit's AirPlay picker;
   muted mirror playback is what makes two-way control work. Links to
   `docs/feasibility.md` on GitHub.
6. **Footer.** Links: GitHub repo, Releases, `CONTRIBUTING.md`, Issues. Sintel
   attribution (© Blender Foundation, CC BY 3.0). MIT.

Not on the page: a version number (so it never goes stale on a release),
analytics, a theme toggle, the dev quickstart, the test section.

## Presentation

- System font stack (`-apple-system, BlinkMacSystemFont, "SF Pro Text",
  "Helvetica Neue", Helvetica, Arial, sans-serif`); monospace via `"SF Mono",
  Menlo, Consolas, monospace`.
- Light palette by default; dark palette under `prefers-color-scheme: dark`.
  Both defined as CSS custom properties on `:root`.
- Single column under about 720 px. Nothing scrolls horizontally.
- `<meta name="viewport">`, `<title>`, `<meta name="description">`, and Open
  Graph title/description/image (the poster) so a pasted link previews well.
- Video: `autoplay muted loop playsinline preload="metadata"` with the poster,
  `width`/`height` set to avoid layout shift, `aria-label` describing the demo.

## Repo settings and cross-references

- Pages enabled with source `master` / `/docs`, via `gh api`.
- Repo "Website" field set to the Pages URL.
- README "Docs" section gains a line pointing at the page.
- `docs/releasing.md` gains one line: the page deploys on any push to `master`;
  it is not a release step and carries no version.
- `.gitignore` gains `.superpowers/` (brainstorming companion sessions).

## Verification

- Render `docs/index.html` locally in the in-app browser at desktop and phone
  widths, light and dark; confirm no horizontal scroll and the video plays.
- After merge, `curl -sI` the live page and `demo.mp4`; both return 200 and the
  MP4 is served as `video/mp4`.
- `make test` still passes; nothing under `plugin/`, `helper/`, or `packaging/`
  changes.

## Out of scope

Rendering the design docs as pages, a custom domain, a blog, screenshots beyond
the one demo clip, a Pages deploy workflow.
