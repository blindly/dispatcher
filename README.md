# dispatch

YAML-configured cron dispatcher with SQLite job tracking and Discord notifications. Single static binary -- no runtime dependencies.

## Why dispatch?

Cron is great at scheduling, but terrible at everything else. If you've ever:

- Written a cron job and had no idea if it actually ran, or when it last succeeded
- Had two instances of the same job overlap and corrupt shared state
- Needed job B to wait for job A, but cron doesn't know about dependencies
- Wanted a notification when a nightly job fails instead of discovering it days later
- Wished you could see all your scheduled jobs and their status in one place

Then dispatch is for you. It's a single binary that wraps your existing scripts and commands with scheduling, state tracking, retries, dependency ordering, and notifications -- all configured in one YAML file.

**Good for:**
- Data pipelines (fetch -> transform -> load) with dependency chains
- Periodic API calls, health checks, and monitoring scripts
- Backup jobs with retry on failure
- Any recurring task where you need visibility into what ran, when, and whether it worked

**Not for:**
- Sub-second scheduling (minimum interval is 1s, but designed for minutes+)
- Distributed job orchestration across multiple machines
- Long-running daemons or services

## Install

Download a binary from [Releases](https://github.com/blindly/dispatcher/releases) (Linux, macOS, Windows).

Or build from source:

```bash
go build -o dispatch .
```

## Quick start

```bash
dispatch init          # creates a dispatcher.yaml with an example job
dispatch validate      # check config syntax
dispatch run-once hello  # test run without tracking
dispatch install       # set up cron to run every 5 minutes
dispatch status        # see summary + cron state
```

## Configuration

Create a `dispatcher.yaml` (or run `dispatch init`):

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
    timeout: 5m

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
| `timeout` | no | `600s` | Max time before killing the job |

Environment variables in the form `${VAR_NAME}` are expanded throughout the config.

Config file auto-detection checks: `dispatcher.yaml`, `dispatcher.yml`, `Dispatcher.yaml`, `Dispatcher.yml`.

## Usage

```bash
dispatch                 # run due jobs (the cron use case)
dispatch list            # full job status table
dispatch status          # quick summary + cron state
dispatch run <job>       # force-run with DB tracking
dispatch run-once <job>  # run without DB tracking
dispatch run-all         # force-run everything
dispatch reset <job>     # reset schedule to run now
dispatch logs <job>      # tail recent job output
dispatch validate        # check config syntax
dispatch init            # create default config
dispatch install         # add crontab entry (default: */5 * * * *)
dispatch uninstall       # remove crontab entry
dispatch update          # self-update to latest release
dispatch version         # show current version
```

## Crontab integration

```bash
# Install (default: every 5 minutes)
dispatch install

# Custom schedule
dispatch install "*/10 * * * *"

# Check if installed
dispatch status

# Remove
dispatch uninstall
```

## How it works

1. Reads `dispatcher.yaml` and checks SQLite for jobs where `next_run_at <= now`.
2. Due jobs are ordered so dependencies run first.
3. Each job runs as a subprocess. Failed jobs retry per their config.
4. Dependent jobs are skipped if their dependency failed.
5. After each run, the DB is updated with the result and next scheduled time.
6. A summary is posted to Discord (if configured).

SQLite state is stored as `data.db` next to the config file. A file lock prevents concurrent dispatch runs. Per-job output is logged to `logs/<name>.log`.

## Development

```bash
go test ./...
go build -o dispatch .
```
