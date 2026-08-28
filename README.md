# iina-airplay

An [IINA](https://github.com/iina/iina) plugin that plays the current file on an
Apple TV, by packaging it into an AirPlay-compatible stream and handing it off —
IINA itself cannot AirPlay its video output.

**Status:** research complete, prototype not yet run on hardware.

- `docs/feasibility.md` — why the direct route is impossible, what is possible,
  and the format-handling matrix
- `docs/prototype.md` — the two experiments that de-risk the design
- `prototype/` — a throwaway plugin and shell helper that answer the one open
  question

Start with `docs/prototype.md`.
