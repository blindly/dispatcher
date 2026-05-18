# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

YAML-configured cron dispatcher in Go. Single static binary that runs shell commands on intervals, tracks state in SQLite, and sends notifications to Discord and/or ntfy.

## Commands

```bash
# Build
go build -o dispatch .

# All tests
go test ./...

# Single test
go test ./internal/config/ -run TestParseInterval_Minutes -v

# Race detector (use when touching internal/runner)
go test -race ./internal/runner/

# Cross-compile sanity check
GOOS=windows go build ./...
```

On this machine the Go toolchain is at `/usr/local/go/bin/go`, NOT on PATH — bash subshells need the full path.

CLI commands: `./dispatch help`. Common ones include `list`, `run <job>`, `run-once <job>`, `enable` (installs a crontab entry using `schedule:` from config), `disable`, `update [beta]`.

## Architecture

CLI entry point in `main.go`. Six internal packages:

- **config** — Loads `Dispatcher.yaml`, expands `${ENV_VAR}`, parses intervals (`5m`, `2h`, `1d`).
- **db** — SQLite `cron_jobs` table via `modernc.org/sqlite` (pure Go). Determines due jobs, respects `active_hours`, updates state after runs.
- **runner** — Job execution. Two modes (see below).
- **notify** — Discord webhooks + ntfy. Live notifications via the `dispatch notify` subcommand inside scripts (uses the `DISPATCH_JOB` env var injected into job subprocesses).
- **display** — Formats `list`/`status`/`analytics`/`history` tables.
- **updater** — `dispatch update` self-update from GitHub releases.

### Runner modes (important)

Two execution paths, gated by call site:

- **Buffered (cron path):** `RunOnce` / `RunJob`. Captures stdout/stderr to a buffer + `.dispatcher/logs/<name>.log`. 600s default timeout. Used by the dispatch loop.
- **Interactive (ad-hoc path):** `RunOnceInteractive` / `RunJobInteractive`. Used by `dispatch run`, `run-once`, `run-all`. Falls back to the buffered path UNLESS `isatty(stdin)` reports a real terminal — then allocates a real PTY (`creack/pty`), puts parent stdin in raw mode, forwards SIGWINCH, skips the log file, and skips the timeout.

The dispatch loop call site (`func dispatch` in `main.go`) deliberately uses the non-`Interactive` variant. Don't change it — that's the safety boundary that keeps cron from ever upgrading to PTY.

PTY implementation: `internal/runner/tty_unix.go` (build tag `!windows`); `tty_windows.go` is a "not supported" stub.

Tests that invoke `RunJob`/`RunJobInteractive` must call the package-private helpers `withTempLogDir(t)` and `pinNoTTY(t)` (in `runner_test.go`). Without `withTempLogDir`, log files leak to `internal/runner/logs/` (gitignored but still grows on disk).

## Key Design Decisions

- **Pure Go, no CGo**: `modernc.org/sqlite` (not `mattn/go-sqlite3`), `creack/pty` for the PTY path.
- **File lock** (`.dispatcher/.dispatch.lock`) prevents concurrent dispatch runs; read-only commands (`list`, `status`, `analytics`, `history`, `info`) and `run-once` skip the lock.
- All runtime state under `.dispatcher/`. All persisted timestamps are UTC RFC3339.

## Releases

Tag-driven via `.github/workflows/release.yml`: push a `v*` tag → CI cross-compiles 6 platform binaries (linux/darwin/windows × amd64/arm64) and creates a GitHub release. Tags containing `-beta`/`-alpha`/`-rc` are auto-marked prerelease and become available to `dispatch update beta`.

CI's `go-version` (in both `ci.yml` and `release.yml`) must match `go.mod`'s `go` directive.
