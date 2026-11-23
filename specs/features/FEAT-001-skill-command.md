---
created: 2026-11-23
id: FEAT-001
status: implemented
title: skill command
type: feature
---

# skill command

## Summary

`dispatch skill` prints an agent skill file (`SKILL.md`, embedded in the
binary via `go:embed`) to stdout. `dispatch skill --install` writes it to
detected agent skill directories so the skill can be installed without a
checkout of the repo. `dispatch update` refreshes already-installed copies
so the skill stays in sync with the binary version.

## Motivation

Agents configuring `Dispatcher.yaml` need accurate field/option docs. A
hand-maintained skill file drifts from the code, and installed copies drift
from each other. Embedding the skill in the binary makes the binary the
single source of truth: the released artifact always carries the matching
documentation.

## Requirements

Addresses: REQ-001

## Design

- `SKILL.md` lives at the repo root and is embedded with `//go:embed`.
- `dispatch skill` prints it verbatim to stdout (no config file needed,
  like `docs`/`init`/`version`). Enables `dispatch skill > SKILL.md`.
- `dispatch skill --install` writes it under a `dispatcher-configuration/`
  dir in each agent skill root whose anchor directory already exists:
  - `~/.config/spec/skills/` (anchor `~/.config/spec`) — the spec skill
    library; repo installs are symlinks to it
  - `~/.claude/skills/` (anchor `~/.claude`)
  - `~/.config/devin/skills/` (anchor `~/.config/devin`)
- After `dispatch update` succeeds, already-installed SKILL.md copies are
  overwritten in place (never created) so skill versions track the binary.
- Anchors keep install additive-only: the command never creates agent
  config dirs on machines that don't use those agents.

## Acceptance Criteria

- [x] `dispatch skill` prints embedded SKILL.md without requiring a config
- [x] `dispatch skill --install` writes to all existing agent skill roots
- [x] `dispatch update` refreshes previously installed copies
