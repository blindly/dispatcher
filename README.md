# dispatch

YAML-configured cron dispatcher with SQLite job tracking and Discord notifications. Single static binary -- no runtime dependencies.

## Install

```bash
go build -o dispatch .
```

Or download a binary from [Releases](https://github.com/blindly/dispatcher/releases).

## Configuration

Create a `dispatcher.yaml`:

```yaml
timezone: America/New_York

notify:
  discord:
    webhook: ${DISCORD_WEBHOOK_URL}

jobs:
  fetch_data:
    command: python scripts/fetch.py
    interval: 30m
    description: Fetch latest data
    active_hours: [9, 17]
    retries: 3
    retry_delay: 10s

  process_data:
    command: python scripts/process.py
    interval: 1h
    description: Process fetched data
    depends_on: fetch_data
```

### Job options

| Field | Required | Default | Description |
|---|---|---|---|
| `command` | yes | | Shell command to execute |
| `interval` | yes | | Run frequency: `30s`, `5m`, `2h`, `1d`, `1w` |
| `description` | no | | Shown in logs and notifications |
| `active_hours` | no | | `[start, end]` hours when the job is allowed to run |
| `depends_on` | no | | Name of another job that must succeed first |
| `retries` | no | `2` | Number of retry attempts on failure |
| `retry_delay` | no | `5s` | Delay between retries |

Environment variables in the form `${VAR_NAME}` are expanded throughout the config.

## Usage

```bash
# Run the dispatcher (executes all due jobs)
dispatch

# Show job status table
dispatch list

# Force-run a specific job
dispatch run fetch_data

# Run a job without DB tracking
dispatch run-once fetch_data

# Reset a job so it runs on the next dispatch
dispatch reset fetch_data

# Force-run all jobs
dispatch run-all
```

## Crontab integration

```bash
# Install (default: every 5 minutes)
dispatch install

# Custom schedule
dispatch install "*/10 * * * *"

# Remove
dispatch uninstall
```

## How it works

1. Reads `dispatcher.yaml` and checks SQLite for jobs where `next_run_at <= now`.
2. Due jobs are ordered so dependencies run first.
3. Each job runs as a subprocess with a 600s timeout. Failed jobs retry per their config.
4. After each run, the DB is updated with the result and next scheduled time.
5. A summary is posted to Discord (if configured).

SQLite state is stored as `data.db` next to the config file. A file lock prevents concurrent dispatch runs. Per-job output is logged to `logs/<name>.log`.

## Development

```bash
go test ./...
go build -o dispatch .
```
