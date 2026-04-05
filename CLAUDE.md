# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

YAML-configured cron dispatcher written in Go. Single static binary that runs shell commands on intervals, tracks state in SQLite, and sends Discord webhook notifications.

## Commands

```bash
# Build
go build -o dispatch .

# Run tests
go test ./...

# Run a single test
go test ./internal/config/ -run TestParseInterval_Minutes -v

# Run the dispatcher
./dispatch                           # normal dispatch (runs due jobs)
./dispatch list                      # show job status table
./dispatch run <job>                 # force-run a specific job
./dispatch run-once <job>            # run without DB tracking
./dispatch reset <job>               # reset next_run to now
./dispatch install "*/5 * * * *"     # install crontab entry
```

## Architecture

Single Go module with CLI entry point in `main.go` and five internal packages:

1. **internal/config** -- Loads `Dispatcher.yaml`, expands `${ENV_VAR}` references, parses intervals (`5m`, `2h`, `1d`) to seconds. Produces `DispatcherConfig` + `JobConfig` structs.
2. **internal/db** -- SQLite `cron_jobs` table via `modernc.org/sqlite` (pure Go). Determines due jobs, respects `active_hours`, updates state after runs.
3. **internal/runner** -- Executes commands via `os/exec` with 600s timeout. Per-job retry with configurable count and delay. Writes per-job logs to `logs/<name>.log`.
4. **internal/notify** -- Posts dispatch summaries to Discord via webhook embeds.
5. **internal/display** -- Formats the `list` status table.

## Key Design Decisions

- **Two external dependencies**: `gopkg.in/yaml.v3` and `modernc.org/sqlite` (pure Go, no CGo).
- **File lock** (`.dispatch.lock`) prevents concurrent dispatch runs; read-only operations (`list`) skip the lock.
- **`run-once`** bypasses both DB and lock for manual testing.
- All runtime state stored in `.dispatcher/` directory (data.db, logs/, lock file). All times UTC RFC3339.
