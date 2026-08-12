---
name: dispatcher-configuration
description: Use when configuring Dispatcher.yaml jobs, troubleshooting job behavior, or answering questions about dispatcher configuration options
---

# Dispatcher Configuration

## Overview
Dispatcher is a YAML-configured cron replacement with SQLite job tracking and notifications. Jobs are defined in `Dispatcher.yaml` with scheduling, retries, dependencies, and notification options.

## Core Configuration Structure

```yaml
timezone: America/New_York
schedule: "*/5 * * * *"   # cron check interval
retention: 90d              # run history retention
pause_timeout: 1h           # default pause duration
timeout: 10m                # default job timeout

vars:
  KEY: value

notify:
  on: failure              # "always" or "failure"
  discord:
    webhook: ${WEBHOOK_URL}
  ntfy:
    url: https://ntfy.sh
    topic: my-topic
    token: ${NTFY_TOKEN}

jobs:
  job-name:
    command: echo "hello"
    interval: 1h
```

## Job Configuration Options

| Field | Default | Description |
|-------|---------|-------------|
| `command` | required | Shell command or list of commands |
| `interval` | required* | Run frequency: `30s`, `5m`, `2h`, `1d`, `1w` |
| `description` | | Shown in logs and notifications |
| `timeout` | global `timeout` (10m) | Max time before killing job |
| `retries` | 2 | Retry attempts on failure |
| `retry_delay` | 5s | Delay between retries |
| `depends_on` | | Job that must succeed first |
| `active_hours` | | `[start, end]` hours when allowed to run |
| `at_minute` | | Minute (0-59) to anchor schedule (prevents drift) |
| `days` | | Days: `weekdays`, `weekends`, `all`, or `[mon, wed, fri]` |
| `adhoc` | false | If true, only runs manually |
| `dir` | | Working directory |
| `env` | | Environment variables (key-value map) |
| `shell` | | `/bin/bash` (Unix) or `powershell` (Windows) |
| `notify` | | `output` to send stdout as notification body |
| `paused` | false | Temporarily disable scheduling |

*`interval` not required when `adhoc: true`

## Exit Code Handling

**Exit codes 0** = success, **non-zero** = failure (triggers retries/failure notifications)

**To handle non-zero exit codes as success:**

```yaml
# Option 1: Shell wrapper with conditional logic
jobs:
  check-restart:
    command: |
      if needs-restarting; then
        echo "No reboot needed"
        exit 0
      else
        echo "Reboot required"
        exit 0  # succeed but output indicates action needed
      fi
    interval: 1h
    notify: output  # see output in notification

# Option 2: Always succeed, check output
jobs:
  check-restart:
    command: |
      needs-restarting
      rc=$?
      if [ $rc -eq 1 ]; then
        echo "REBOOT_NEEDED"
      else
        echo "NO_REBOOT_NEEDED"
      fi
      exit 0
    interval: 1h
    notify: output

# Option 3: Simple OR wrapper (treat any exit code as success)
jobs:
  check-restart:
    command: needs-restarting || true
    interval: 1h
```

**Key principle:** Dispatcher doesn't have built-in exit code mapping. Use shell wrappers to normalize exit codes while preserving information in output.

## Notification Behavior

**Global `notify.on` policy:**
- `always` (default): notify on all job completions
- `failure`: only notify on failures

**Job-level override:**
```yaml
jobs:
  critical-job:
    notify: failure  # override global policy
```

**Output mode:**
```yaml
jobs:
  verbose-job:
    notify: output  # sends stdout as notification body (skipped if empty)
```

**Combined with failure policy:**
Currently `notify: output` sends regardless of exit code. To only send output on failure, use shell wrapper:
```yaml
jobs:
  job-with-output-on-fail:
    command: |
      output=$(my-command)
      rc=$?
      if [ $rc -ne 0 ]; then
        echo "$output"
      fi
      exit $rc
    interval: 1h
```

## Schedule Drift Prevention

Without `at_minute`, jobs drift based on execution time. Use `at_minute` to anchor:

```yaml
jobs:
  daily-backup:
    command: ./backup.sh
    interval: 24h
    at_minute: 0  # runs at 00:00 each day
```

For sub-hour intervals, `at_minute` is the anchor - valid minutes derived automatically:
```yaml
jobs:
  metrics:
    command: ./metrics.py
    interval: 15m
    at_minute: 0  # runs at :00, :15, :30, :45
```

## Dependencies

Jobs wait for dependency to succeed:
```yaml
jobs:
  extract:
    command: tar -xf data.tar.gz
    interval: 1d

  process:
    command: ./process.sh
    interval: 1d
    depends_on: extract  # waits for extract to succeed
```

**Dependency behavior:**
- Dependency fails → dependent job skipped
- Dependency succeeds → dependent job runs on next check
- Circular dependencies → config validation error

## Environment Variables

**Three sources (priority order):**
1. CLI args: `dispatch run job KEY=VALUE`
2. `.env` file: `.dispatcher/.env` or project `.env`
3. Job-level `env` in config

**Config variables (vars section):**
```yaml
vars:
  BACKUP_DIR: /var/backups
  S3_BUCKET: s3://backups

jobs:
  backup:
    command: aws s3 cp {{.BACKUP_DIR}}/data {{.S3_BUCKET}}/
```

**Special variable:**
- `{{.CLI_ARGS}}` expands to args after `--`

## Multiple Commands

Commands run sequentially, stop on first failure:
```yaml
jobs:
  deploy:
    command:
      - git pull
      - npm install
      - npm run build
      - systemctl restart myapp
```

## Common Patterns

**Conditional execution based on command output:**
```yaml
jobs:
  health-check:
    command: |
      if curl -sf https://api.example.com/health; then
        echo "HEALTHY"
      else
        echo "UNHEALTHY"
        exit 1
      fi
    interval: 5m
```

**Long-running job with timeout:**
```yaml
jobs:
  data-import:
    command: python import_large_dataset.py
    interval: 1d
    timeout: 2h  # override global 10m default
```

**Job that only runs manually:**
```yaml
jobs:
  manual-cleanup:
    command: ./cleanup.sh
    adhoc: true  # no interval needed
```

**Time-restricted job:**
```yaml
jobs:
  business-hours-check:
    command: ./check.sh
    interval: 30m
    active_hours: [9, 17]  # 9am-5pm only
    days: weekdays
```

## Config File Locations

Auto-detection order:
1. `Dispatcher.yaml`
2. `Dispatcher.yml`
3. `dispatcher.yaml`
4. `dispatcher.yml`

## Common Mistakes

**Using wrong interval syntax:**
- ❌ `interval: 60` (missing unit)
- ✅ `interval: 60s` or `interval: 1m`

**Forgetting that adhoc jobs need no interval:**
- ❌ `adhoc: true` with `interval: 1h` (interval ignored)
- ✅ `adhoc: true` alone (no interval needed)

**Expecting notify: output to respect failure policy:**
- ❌ `notify: output` sends on success too
- ✅ Use shell wrapper to only output on failure

**Circular dependencies:**
- ❌ A depends on B, B depends on A
- ✅ Linear chain: A → B → C

**Not anchoring schedules:**
- ❌ `interval: 1h` without `at_minute` (drifts over time)
- ✅ `interval: 1h` with `at_minute: 0` (runs at :00 each hour)

## Quick Reference

**Scheduling:**
- Minimum interval: 1s (designed for minutes+)
- Cron check: `schedule: "*/5 * * * *"` (default)
- Timezone: `timezone: America/New_York`

**Retries:**
- Default: 2 retries with 5s delay
- Override: `retries: 3, retry_delay: 30s`

**Notifications:**
- Discord webhook + ntfy supported
- Global policy: `notify.on: always|failure`
- Job override: `notify: output|failure`

**Data:**
- SQLite database: `.dispatcher/data.db`
- History retention: `retention: 90d` (default)
