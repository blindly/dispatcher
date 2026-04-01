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
- Data pipelines with dependency chains
- Periodic health checks and monitoring scripts
- Backup jobs with retry on failure
- Any recurring task where you need visibility into what ran, when, and whether it worked

**Not for:**
- Sub-second scheduling (minimum interval is 1s, but designed for minutes+)
- Distributed job orchestration across multiple machines
- Long-running daemons or services

## Install

**Linux/macOS:**

```bash
curl -sL https://github.com/blindly/dispatcher/releases/latest/download/dispatch-$(uname -s | tr '[:upper:]' '[:lower:]')-$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/') -o dispatch
chmod +x dispatch
sudo mv dispatch /usr/local/bin/
```

**Or with Go:**

```bash
go install github.com/blindly/dispatcher@latest
```

**Or download manually** from [Releases](https://github.com/blindly/dispatcher/releases) (Linux, macOS, Windows).

Once installed, keep it up to date:

```bash
dispatch update
```

## Quick start

```bash
dispatch init              # creates a dispatcher.yaml with an example job
dispatch validate          # check config syntax
dispatch run-once hello    # test run without tracking
dispatch install           # set up cron to run every 5 minutes
dispatch status            # see summary + cron state
```

## Configuration

Create a `dispatcher.yaml` (or run `dispatch init`):

```yaml
timezone: America/New_York
schedule: "*/5 * * * *"   # how often cron checks for due jobs (default: */5)
retention: 90d              # how long to keep run history (default: 90d)

vars:
  BACKUP_DIR: /var/backups/myapp
  S3_BUCKET: s3://myapp-backups

notify:
  discord:
    webhook: ${DISCORD_WEBHOOK_URL}

jobs:
  db-backup:
    command:
      - pg_dump myapp > {{.BACKUP_DIR}}/dump-$(date +%Y%m%d).sql
      - gzip {{.BACKUP_DIR}}/dump-$(date +%Y%m%d).sql
    interval: 1d
    description: Dump and compress the database
    timeout: 10m

  upload-backup:
    command: aws s3 cp {{.BACKUP_DIR}}/dump-$(date +%Y%m%d).sql.gz {{.S3_BUCKET}}/
    interval: 1d
    description: Push backup to S3
    depends_on: db-backup
    retries: 3
    retry_delay: 30s

  cleanup-old:
    command: find {{.BACKUP_DIR}} -name "*.sql.gz" -mtime +30 -delete
    interval: 1w
    description: Delete backups older than 30 days

  health-check:
    command: curl -sf https://myapp.com/health
    interval: 5m
    description: Ping the app endpoint
    active_hours: [6, 22]
    retries: 3
    retry_delay: 10s
    timeout: 30s

  restore:
    command: "gunzip -c {{.CLI_ARGS}} | psql myapp"
    adhoc: true
    description: Restore a backup (run manually)
```

This config sets up a daily backup pipeline: `db-backup` runs once a day, `upload-backup` waits for it to finish, `cleanup-old` runs weekly, and `health-check` pings every 5 minutes (only between 6am-10pm). The `restore` job is adhoc -- it only runs when you trigger it manually:

```bash
dispatch run restore -- /var/backups/myapp/dump-20260315.sql.gz
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

Environment variables in the form `${VAR_NAME}` are expanded throughout the config. Variables are loaded from a `.env` file in the config directory (if present), then from the shell environment. Use this for secrets like webhook URLs:

```
# .env (same directory as dispatcher.yaml)
DISCORD_WEBHOOK_URL=https://discord.com/api/webhooks/123456/abcdef
API_KEY=your-secret-key
```

Config-level variables use `{{.VAR_NAME}}` syntax from the `vars` section — use these for paths, binaries, and other reusable non-secret values.

Config file auto-detection checks: `dispatcher.yaml`, `dispatcher.yml`, `Dispatcher.yaml`, `Dispatcher.yml`.

### Multiple commands

A job can run a sequence of commands. They run in order and stop on the first failure:

```yaml
jobs:
  deploy:
    command:
      - git pull
      - npm install
      - npm run build
      - systemctl restart myapp
    adhoc: true
```

### Variables

Define reusable variables in a `vars` section:

```yaml
vars:
  PY: /usr/bin/python3
  APP_DIR: /opt/myapp

jobs:
  migrate:
    command: "{{.PY}} {{.APP_DIR}}/manage.py migrate"
    adhoc: true

  collect-metrics:
    command: "{{.PY}} {{.APP_DIR}}/scripts/metrics.py"
    interval: 15m
```

`{{.CLI_ARGS}}` is a special built-in that expands to the extra arguments passed after `--`:

```bash
dispatch run migrate -- --fake-initial
# runs: /usr/bin/python3 /opt/myapp/manage.py migrate --fake-initial
```

### Adhoc jobs

Jobs marked `adhoc: true` are never run by the scheduler -- they only run when you explicitly trigger them with `dispatch run` or `dispatch run-once`. No `interval` required:

```yaml
jobs:
  seed-db:
    command: psql myapp < seed.sql
    adhoc: true
```

### Passing parameters to jobs

Pass environment variables and extra arguments when manually running a job:

```bash
# Environment variables (KEY=VALUE)
dispatch run deploy ENV=production VERSION=1.2.3

# Extra args (after --) appended to the command
dispatch run restore -- /var/backups/dump-20260315.sql.gz

# Both
dispatch run deploy ENV=production -- --no-cache
```

`KEY=VALUE` pairs are set as **environment variables** in the subprocess. Args after `--` are **appended to the command** (or placed where `{{.CLI_ARGS}}` appears).

```python
# scripts/deploy.py
import argparse, os

env = os.environ["ENV"]           # from KEY=VALUE
version = os.environ["VERSION"]   # from KEY=VALUE

parser = argparse.ArgumentParser()
parser.add_argument("--no-cache", action="store_true")
args = parser.parse_args()        # from -- args
```

```bash
dispatch run deploy ENV=production VERSION=1.2.3 -- --no-cache
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
dispatch watch           # live tail all job logs
dispatch watch <job>     # live tail a specific job
dispatch history <job>   # show last 20 runs for a job
dispatch analytics       # job success rates and run history
dispatch purge           # delete old run history (uses retention config)
dispatch purge 30d       # delete history older than 30 days
dispatch validate        # check config syntax
dispatch init            # create default config
dispatch install         # add crontab entry (default: */5 * * * *)
dispatch uninstall       # remove crontab entry
dispatch update          # self-update to latest release
dispatch version         # show current version
dispatch docs            # show full documentation
```

## Crontab integration

The `schedule` field in `dispatcher.yaml` controls how often cron fires the dispatcher. `dispatch install` reads it from the config:

```yaml
schedule: "*/1 * * * *"   # check for due jobs every minute
```

If omitted, defaults to `*/5 * * * *` (every 5 minutes).

```bash
# Install using schedule from config
dispatch install

# Override with a custom schedule
dispatch install "*/10 * * * *"

# Check if installed
dispatch status

# Remove
dispatch uninstall
```

## Analytics

Every run is logged to a history table. Use `dispatch analytics` to see success rates and trends:

```
Job                  Runs    Pass    Fail     Rate    Avg Time     Last 7d
----------------------------------------------------------------------------------------------------
db-backup              30      29       1    96.7%       45.2s      7/7
upload-backup          29      28       1    96.6%       12.8s      7/7
cleanup-old             4       4       0   100.0%        0.3s      1/1
health-check          840     839       1    99.9%        1.2s    168/168

Overall: 903 runs, 99.7% success rate, 4 jobs
Most reliable: health-check (99.9%)
Least reliable: upload-backup (96.6%)
```

## How it works

1. Reads `dispatcher.yaml` and checks SQLite for jobs where `next_run_at <= now`.
2. Due jobs are ordered so dependencies run first. Adhoc jobs are skipped.
3. Each job runs as a subprocess. Failed jobs retry per their config.
4. Dependent jobs are skipped if their dependency failed.
5. After each run, the DB is updated with the result and next scheduled time.
6. A summary is posted to Discord (if configured).

SQLite state is stored as `data.db` next to the config file. A file lock prevents concurrent dispatch runs. Per-job output is logged to `logs/<name>.log`.

## Windows

Dispatch runs on Windows. Most features work the same, with a few differences:

| Feature | Windows | Linux/macOS |
|---|---|---|
| Job execution | Works | Works |
| SQLite tracking | Works | Works |
| Discord notifications | Works | Works |
| File locking | Works (LockFileEx) | Works (flock) |
| Config, variables, analytics | Works | Works |
| `dispatch install` / `uninstall` | Not supported (no crontab) | Works |

For scheduled execution on Windows, use Task Scheduler manually:

```
schtasks /create /tn "dispatch" /tr "C:\path\to\dispatch.exe" /sc minute /mo 5
```

## Development

```bash
go test ./...
go build -o dispatch .
```
