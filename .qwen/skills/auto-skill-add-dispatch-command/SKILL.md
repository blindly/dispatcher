---
name: add-dispatch-command
description: How to add a new CLI command to the dispatcher project, including DB queries, display formatting, wiring into main.go, and testing patterns.
source: auto-skill
extracted_at: '2026-07-05T16:20:17.156Z'
---

# Adding a New `dispatch` CLI Command

This project is a Go CLI scheduler with a specific architecture. Here's how to add a new command end-to-end.

## Architecture Overview

- **`main.go`** — CLI entry point: arg parsing, command routing, lock acquisition. Read-only commands (list, status, next, info) do NOT acquire the lock. Write commands (run, dispatch, purge) do.
- **`internal/config/config.go`** — YAML config parsing, `JobConfig` struct with fields like `IntervalSeconds`, `ActiveHours`, `ActiveDays`, `AtMinute`, `DependsOn`, `Paused`, `Adhoc`.
- **`internal/db/db.go`** — SQLite schema, job state queries, scheduling helpers (`computeNextAligned`, `GetDueJobs`, `IsInActiveHours`, `IsOnActiveDay`).
- **`internal/display/display.go`** — All formatted output (tables, banners). Commands should NOT print directly from main.go for non-trivial output — delegate to display functions.
- **`internal/runner/runner.go`** — Job execution, dependency resolution.
- **`internal/notify/notify.go`** — Discord/ntfy notifications.

## Step-by-Step

### 1. Add the command to `main.go` usage string
Edit the `usage` const to include the new command name and brief description.

### 2. Add command routing in `main.go`
- Read-only commands go in the section after `display.SetTimezone(cfg.Timezone)` and `runner.SetLogDir(dispDir)`, before the lock acquisition block. They need `conn` (DB) and `cfg` but NOT the lock.
- Write/mutation commands go in the `switch cmd` block after lock acquisition.
- For aliases, add them to the `aliases` map.

### 3. Implement logic in the appropriate internal package
- **DB queries** → `internal/db/db.go`
- **Display/formatting** → `internal/display/display.go`
- **Config parsing** → `internal/config/config.go`

Key patterns in `db.go`:
- Time values in the DB are RFC3339 UTC strings. Parse with `time.Parse(time.RFC3339, str)`.
- `IsInActiveHours` / `IsOnActiveDay` check the current time — for computing future times use the `*At` variants (`IsInActiveHoursAt`, `IsOnActiveDayAt`) that accept a `time.Time` and `*time.Location`.
- `computeNextAligned` handles `at_minute` alignment. For forward-looking next-run computation that also respects `active_hours` and `days`, see `ComputeNextRun`.
- `NowUTC()` is the project's clock — use it instead of `time.Now()` for testability.

Key patterns in `display.go`:
- `SetTimezone(cfg.Timezone)` must be called once before display functions.
- `FormatDt()` formats RFC3339 timestamps for display.
- `FormatInterval()` converts seconds to human-readable ("5m", "2h", "1d").
- Column widths are computed dynamically based on actual data lengths.

### 4. Testing
- **Unit tests** live alongside the code: `internal/db/db_test.go`, `internal/display/display_test.go`.
- **CLI integration tests** live in `main_test.go`. Pattern: `buildBinary(t)` compiles the binary, `writeTestConfig(t, dir)` creates a minimal YAML config, then run commands via `exec.Command`.
- Go binary is at `/usr/local/go/bin/go` on this machine.
- Run tests with: `/usr/local/go/bin/go test ./...`

### 5. Cross-platform shell defaults

When a job has no explicit `shell` configured, the runner falls back to a platform-specific default:
- **`internal/runner/shell_unix.go`** (`//go:build !windows`) → `DefaultShell() string { return "/bin/bash" }`
- **`internal/runner/shell_windows.go`** (`//go:build windows`) → `DefaultShell() string { return "powershell" }`

This pattern uses Go build tags to select the right implementation at compile time — no runtime `runtime.GOOS` checks needed. Use this same approach for any platform-specific default.

The runner always executes commands through a shell (`shell -c <cmd>` or `powershell -Command <cmd>`). PowerShell detection uses `strings.EqualFold` to match `"powershell"` or `"pwsh"`, switching the flag from `-c` to `-Command`. Extra CLI args are appended to the command string before passing to the shell.

### 6. Important constraints
- Jobs with `Paused: true` or `Adhoc: true` should generally be excluded from scheduled command output.
- The `active_hours` end hour is exclusive (9-17 means active 9:00 through 16:59).
- Overnight `active_hours` (e.g., [22, 6]) are supported — the end wraps past midnight.
- The `days` field uses `time.Weekday` ordering: Sun=0, Mon=1, ..., Sat=6.
- Config file detection searches for `Dispatcher.yaml`, `Dispatcher.yml`, `dispatcher.yaml`, `dispatcher.yml` in order.
- The `.dispatcher/` directory holds runtime data (data.db, logs, paused state) and is auto-migrated from older project-root locations.