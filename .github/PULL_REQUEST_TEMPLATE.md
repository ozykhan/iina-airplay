<!--
The PR title and body become the squashed commit on master. Write them as the
changelog entry you'd want to read a year from now.
-->

## What and why

<!-- What changes, and what problem it solves. -->

Part of #

## How it was tested

<!--
`make test` is the baseline. Say what else you exercised.

Casting to a real Apple TV can't be automated — if you couldn't verify that
last step, say so. It's an expected gap, not a problem; it just tells the
maintainer what to check before merging.
-->

- [ ] `make test` passes locally
- [ ] Cast end-to-end to a real AirPlay device — or: not verified (no device)

## Checklist

- [ ] Conventional-commit style title (`feat:`, `fix:`, `docs:`, …)
- [ ] `plugin/Info.json` is not committed (it's a `make dev` symlink)
- [ ] Docs updated if behaviour or the build changed
