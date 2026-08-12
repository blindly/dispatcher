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
dispatch init              # creates a Dispatcher.yaml with an example job
dispatch validate          # check config syntax
dispatch run-once hello    # test run without tracking
dispatch enable            # set up cron to run every 5 minutes
dispatch status            # see summary + cron state
```

## Configuration

Create a `Dispatcher.yaml` (or run `dispatch init`):

```yaml
timezone: America/New_York
schedule: "*/5 * * * *"   # how often cron checks for due jobs (default: */5)
retention: 90d              # how long to keep run history (default: 90d)
pause_timeout: 1h           # default pause duration (default: 1h)
timeout: 10m                # default job timeout (default: 10m)

vars:
  BACKUP_DIR: /var/backups/myapp
  S3_BUCKET: s3://myapp-backups

notify:
  on: failure                       # "always" (default) or "failure"
  discord:
    webhook: ${DISCORD_WEBHOOK_URL}
  # ntfy:
  #   url: https://ntfy.sh
  #   topic: my-dispatch
  #   token: ${NTFY_TOKEN}

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
    days: weekdays
    retries: 3
    retry_delay: 10s
    timeout: 30s

  restore:
    command: "gunzip -c {{.CLI_ARGS}} | psql myapp"
    adhoc: true
    description: Restore a backup (run manually)
```

This config sets up a daily backup pipeline: `db-backup` runs once a day, `upload-backup` waits for it to finish, `cleanup-old` runs weekly, and `health-check` pings every 5 minutes (only on weekdays, between 6am-10pm). The `restore` job is adhoc -- it only runs when you trigger it manually:

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
| `at_minute` | no | | Minute (0–59) to run at; prevents schedule drift from execution time |
| `days` | no | | Days of week: `weekdays`, `weekends`, `all`, or list like `[mon, wed, fri]` |
| `depends_on` | no | | Name of another job that must succeed first |
| `retries` | no | `2` | Number of retry attempts on failure |
| `retry_delay` | no | `5s` | Delay between retries |
| `timeout` | no | global `timeout` (default: `10m`) | Max time before killing the job |
| `adhoc` | no | `false` | If true, only runs manually (skipped by scheduler) |
| `dir` | no | | Working directory for the command |
| `env` | no | | Environment variables (key-value map) |
| `shell` | no | `/bin/bash` (Unix), `powershell` (Windows) | Shell to use for running commands |
| `notify` | no | | Set to `output` to forward job stdout as the notification body |
| `paused` | no | | Set to `true` to temporarily disable scheduling |

\* `interval` is not required when `adhoc: true`.

### Preventing schedule drift with `at_minute`

Without `at_minute`, a job that takes 3 minutes to run on a 1-hour interval will progressively drift later each cycle: 9:00, 10:03, 11:06, 12:09, etc. Setting `at_minute` snaps the next run to a specific clock minute:

```yaml
jobs:
  db-backup:
    command: pg_dump myapp > backup.sql
    interval: 1h
    at_minute: 0           # runs at 9:00, 10:00, 11:00 — no drift
```

For sub-hour intervals, `at_minute` is the anchor — valid minutes are derived from the interval automatically. With `interval: 15m` and `at_minute: 0`, the scheduler derives `[0, 15, 30, 45]` and picks whichever one comes next:

```yaml
jobs:
  metrics:
    command: python3 scripts/metrics.py
    interval: 15m
    at_minute: 0           # runs at 9:00, 9:15, 9:30, 9:45
```

An offset shifts the whole pattern:

```yaml
jobs:
  heartbeat:
    command: curl -sf https://myapp.com/ping
    interval: 10m
    at_minute: 5           # runs at 9:05, 9:15, 9:25, 9:35, 9:45, 9:55
```

For multi-hour intervals, extra hours are added on top so the minimum spacing is still respected:

```yaml
jobs:
  daily-report:
    command: python3 scripts/report.py
    interval: 24h
    at_minute: 30          # runs at 00:30 each day, regardless of runtime
```

Environment variables in the form `${VAR_NAME}` are expanded throughout the config. Variables are loaded from `.dispatcher/.env` first, then from a `.env` in the config directory (project root), then from the shell environment. Use this for secrets like webhook URLs:

```
# .dispatcher/.env
DISCORD_WEBHOOK_URL=https://discord.com/api/webhooks/123456/abcdef
API_KEY=your-secret-key
```

Config-level variables use `{{.VAR_NAME}}` syntax from the `vars` section — use these for paths, binaries, and other reusable non-secret values.

Config file auto-detection checks: `Dispatcher.yaml`, `Dispatcher.yml`, `dispatcher.yaml`, `dispatcher.yml`.

### Directory, environment, and shell

Jobs can specify a working directory, environment variables, and shell:

```yaml
jobs:
  deploy:
    command: ./deploy.sh
    dir: /opt/myapp
    shell: /bin/bash
    env:
      NODE_ENV: production
      LOG_LEVEL: info
    adhoc: true
```

Without `shell`, commands run through the system default shell (`/bin/bash` on Linux/macOS, `powershell` on Windows). With an explicit `shell`, the full command string is passed to that shell via `-c` (or `-Command` for PowerShell), which enables pipes, redirects, and shell syntax.

Job-level `env` is merged with the process environment and any `.env` file. CLI `KEY=VALUE` args take highest priority.

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
dispatch next            # show upcoming jobs sorted by next run
dispatch status          # quick summary + cron state
dispatch run <job>       # force-run with DB tracking (alias: exec)
dispatch run-once <job>  # run without DB tracking
dispatch run-all         # force-run all scheduled jobs
dispatch pause           # pause scheduled dispatch (default: 1h)
dispatch pause 2h        # pause for a specific duration
dispatch pause "reason"  # pause with a reason
dispatch resume          # resume scheduled dispatch
dispatch reset <job>     # reset schedule to run now
dispatch logs <job>      # show recent job output
dispatch watch           # live tail all job logs
dispatch watch <job>     # live tail a specific job
dispatch notify "msg"    # send a live notification (from inside a job)
dispatch history <job>   # show last 20 runs for a job
dispatch info            # show full config, state, and job summary
dispatch analytics       # job success rates and run history
dispatch purge           # delete old run history (uses retention config)
dispatch purge 30d       # delete history older than 30 days
dispatch validate        # check config syntax
dispatch init            # create default config
dispatch enable          # enable scheduler (systemd timer or crontab)
dispatch disable         # disable scheduler (remove timer or crontab entry)
dispatch update          # self-update to latest stable release
dispatch update beta     # update to latest beta/pre-release
dispatch update v1.11.0  # update to a specific version
dispatch version         # show current version
dispatch docs            # show full documentation
```

## Scheduling

`dispatch enable` installs the scheduler to run the dispatcher on the interval defined in `Dispatcher.yaml`. By default it auto-detects whether your system has systemd or cron, preferring systemd:

```yaml
scheduler: systemd         # or "cron" — auto-detected if omitted
schedule: "*/5 * * * *"    # defaults to every 5 minutes
```

### Systemd timer (preferred)

On systems with systemd, `dispatch enable` installs a `.service` + `.timer` unit. When run as root, units are installed system-wide (`/etc/systemd/system/`). As a regular user, they're installed per-user (`~/.config/systemd/user/`).

The `schedule:` cron expression is converted to systemd `OnCalendar` format. For example, `*/5 * * * *` becomes `*-*-* 00:00/5`.

```bash
dispatch enable            # installs systemd timer
dispatch disable           # removes systemd timer
```

### Cron (legacy)

If systemd isn't available or you explicitly set `scheduler: cron`, `dispatch enable` installs a crontab entry. The `schedule:` field is the single source of truth:

```bash
dispatch enable            # installs crontab entry
dispatch disable           # removes crontab entry
```

The `enable` command is idempotent — it updates the existing entry if `schedule:` changed, or prints "Already enabled" if nothing changed.

```bash
# Check current status
dispatch status
```

## Notifications

Dispatch can send summaries to Discord and/or ntfy after each run.

### Global settings

```yaml
notify:
  on: failure              # "always" (default) or "failure"
  discord:
    webhook: ${DISCORD_WEBHOOK_URL}
  ntfy:
    url: https://ntfy.sh   # default if omitted
    topic: my-dispatch
    token: ${NTFY_TOKEN}   # optional, for private topics
    priority: high          # optional ntfy priority
```

| Field | Default | Description |
|---|---|---|
| `notify.on` | `always` | When to notify: `always` sends after every dispatch, `failure` only when a job fails |
| `notify.discord.webhook` | | Discord webhook URL |
| `notify.ntfy.url` | `https://ntfy.sh` | ntfy server URL |
| `notify.ntfy.topic` | | ntfy topic name |
| `notify.ntfy.token` | | Auth token for private topics |
| `notify.ntfy.priority` | | ntfy message priority |

### Per-job overrides

Individual jobs can set `notify: output` to forward their stdout as the notification body instead of the default summary:

```yaml
jobs:
  status-report:
    command: ./generate-report.sh
    interval: 1d
    notify: output          # sends job output as the notification
```

### Live notifications

Jobs can send notifications in real time while they're running, using the `dispatch notify` command:

```bash
dispatch notify "Backup 50% complete"
dispatch notify --job mybackup "Step 3 done"
```

When a job is launched by the dispatcher, the `DISPATCH_JOB` environment variable is set automatically. The `notify` command reads it to tag notifications with the job name — no `--job` flag needed:

```yaml
jobs:
  etl-pipeline:
    command: python3 scripts/etl.py
    interval: 1d
    shell: /bin/bash
```

```python
# scripts/etl.py
import subprocess

subprocess.run(["dispatch", "notify", "Starting extraction..."])
# ... do work ...
subprocess.run(["dispatch", "notify", "Extraction complete, loading 50k rows"])
# ... do more work ...
subprocess.run(["dispatch", "notify", "Pipeline finished"])
```

Live notifications appear on Discord with a blurple embed and on ntfy with a speech balloon tag, visually distinct from the post-run summaries.

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

## System info

`dispatch info` shows the full effective configuration and runtime state — what the dispatcher actually sees after parsing, env expansion, and defaults:

```
Dispatcher
  Version:       v1.14.3
  Config:        /srv/myapp/Dispatcher.yaml
  Data dir:      /srv/myapp/.dispatcher
  Timezone:      America/New_York
  Schedule:      */5 * * * *
  Retention:     90d
  Pause timeout: 1h

Cron
  Status:        enabled
  Entry:         */5 * * * * cd /srv/myapp && dispatch >> .dispatcher/logs/dispatcher.log 2>&1

State
  Paused:        no
  Last dispatch: 2026-04-07 21:30:00
  Database:      /srv/myapp/.dispatcher/data.db (248.0 KB)

Jobs: 4 scheduled, 1 adhoc, 0 paused
  Scheduled:
    cleanup-old      1w     Delete backups older than 30 days
    db-backup        1d     Dump and compress the database
    health-check     5m     Ping the app endpoint
    upload-backup    1d     Push backup to S3
  Adhoc:
    restore                 Restore a backup

Notifications
  Mode:          failure
  Discord:       configured
  Ntfy:          not configured
```

Useful for debugging config issues — catches empty `${VAR}` expansions, forgotten defaults, and verifying the scheduler is enabled.

## Pausing

Pause the dispatcher to prevent scheduled jobs from running while you're working on something:

```bash
dispatch pause                    # pause for default duration (1h)
dispatch pause 2h                 # pause for 2 hours
dispatch pause "fixing the ETL"   # pause with a reason
dispatch pause 2h "deploying"     # duration + reason
dispatch resume                   # resume early
```

While paused, manual runs (`dispatch run`, `dispatch exec`, `dispatch run-once`) still work. Only the cron-triggered default dispatch is blocked. The `list` and `status` commands show a banner when paused.

Pauses auto-expire after the configured duration (default: 1h, set via `pause_timeout` in config). You can also pause manually with `touch .dispatcher/paused` — this has no expiry and must be resumed with `dispatch resume` or `rm .dispatcher/paused`.

## How it works

1. Reads `Dispatcher.yaml` and checks SQLite for jobs where `next_run_at <= now`.
2. Due jobs are ordered so dependencies run first. Adhoc jobs are skipped.
3. Each job runs as a subprocess. Failed jobs retry per their config.
4. Dependent jobs are skipped if their dependency failed.
5. After each run, the DB is updated with the result and next scheduled time.
6. A summary is posted to Discord (if configured).

Runtime files (SQLite DB, job logs, lock file, .env) are stored in `.dispatcher/` next to the config file. Existing files are auto-migrated on first run.

```
.dispatcher/
  data.db          # SQLite state
  .env              # Dispatcher secrets (optional; root .env also works)
  logs/             # Per-job log files
```

## Windows

Dispatch runs on Windows. Most features work the same, with a few differences:

| Feature | Windows | Linux/macOS |
|---|---|---|
| Job execution | Works | Works |
| SQLite tracking | Works | Works |
| Discord notifications | Works | Works |
| File locking | Works (LockFileEx) | Works (flock) |
| Config, variables, analytics | Works | Works |
| `dispatch enable` / `disable` | Not supported (no crontab) | Works |

For scheduled execution on Windows, use Task Scheduler manually:

```
schtasks /create /tn "dispatch" /tr "C:\path\to\dispatch.exe" /sc minute /mo 5
```

## Development

```bash
go test ./...
go build -o dispatch .
```
