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

Self-update to the latest release:

```bash
dispatch update
```

## Quick start

```bash
dispatch init            # creates a dispatcher.yaml with an example job
dispatch validate        # check config syntax
dispatch run-once hello  # test run without tracking
dispatch install         # set up cron to run every 5 minutes
dispatch status          # see summary + cron state
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
| `command` | yes | | Shell command or list of commands |
| `interval` | yes* | | Run frequency: `30s`, `5m`, `2h`, `1d`, `1w` |
| `description` | no | | Shown in logs and notifications |
| `active_hours` | no | | `[start, end]` hours when the job is allowed to run |
| `depends_on` | no | | Name of another job that must succeed first |
| `retries` | no | `2` | Number of retry attempts on failure |
| `retry_delay` | no | `5s` | Delay between retries |
| `timeout` | no | `600s` | Max time before killing the job |
| `adhoc` | no | `false` | If true, only runs manually (skipped by scheduler) |

\* `interval` is not required when `adhoc: true`.

Environment variables in the form `${VAR_NAME}` are expanded throughout the config.

Config file auto-detection checks: `dispatcher.yaml`, `dispatcher.yml`, `Dispatcher.yaml`, `Dispatcher.yml`.

### Multiple commands

A job can run a single command or a sequence of commands. Commands run in order and stop on the first failure:

```yaml
jobs:
  deploy:
    command:
      - git pull
      - npm install
      - npm run build
    interval: 1h
```

### Variables

Define reusable variables in a `vars` section. Use `{{.VAR_NAME}}` to reference them in commands:

```yaml
vars:
  PYTHON: /usr/bin/python3
  DATA_DIR: /opt/data

jobs:
  fetch:
    command: "{{.PYTHON}} scripts/fetch.py --output {{.DATA_DIR}}"
    interval: 30m

  calibrate:
    command: "{{.PYTHON}} scripts/calibrate.py {{.CLI_ARGS}}"
    adhoc: true
```

`{{.CLI_ARGS}}` is a special built-in that expands to the extra arguments passed after `--`:

```bash
dispatch run calibrate -- weather crypto --parallel
# runs: /usr/bin/python3 scripts/calibrate.py weather crypto --parallel
```

Variables are expanded at config load time (except `{{.CLI_ARGS}}` which is expanded at runtime). Use `${ENV_VAR}` for secrets from the environment, `{{.VAR}}` for config-level values.

### Adhoc jobs

Jobs marked `adhoc: true` are never run by the scheduler -- they only run when you explicitly trigger them with `dispatch run` or `dispatch run-once`. Useful for manual tasks you want to keep in the config:

```yaml
jobs:
  migrate:
    command: python manage.py migrate
    adhoc: true
```

### Passing parameters to jobs

You can pass environment variables and extra arguments when manually running a job:

```bash
# Environment variables (KEY=VALUE before --)
dispatch run deploy ENV=production VERSION=1.2.3

# Extra args (after --) appended to the command
dispatch run backup -- /data/important

# Both
dispatch run deploy ENV=production -- --force

# Works with run-once too
dispatch run-once migrate DB_HOST=localhost -- --dry-run
```

`KEY=VALUE` pairs are set as **environment variables** (`os.environ` in Python, `os.Getenv` in Go). Args after `--` are **appended to the command** (`sys.argv` in Python).

```python
# scripts/deploy.py
import argparse, os

# Environment variables (KEY=VALUE)
api_key = os.environ["API_KEY"]

# Extra args (after --)
parser = argparse.ArgumentParser()
parser.add_argument("--env", required=True)
args = parser.parse_args()
```

```bash
dispatch run deploy API_KEY=secret -- --env production
```

## Usage

```bash
dispatch                 # run due jobs (the cron use case)
dispatch list            # full job status table
dispatch status          # quick summary + cron state
dispatch run <job>       # force-run with DB tracking
dispatch run-once <job>  # run without DB tracking
dispatch run-all         # force-run all scheduled jobs
dispatch reset <job>     # reset schedule to run now
dispatch logs <job>      # show recent job output
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
2. Due jobs are ordered so dependencies run first. Adhoc jobs are skipped.
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
