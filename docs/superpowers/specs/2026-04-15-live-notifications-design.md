# Live Notifications Design

Send notifications from within running jobs in real time, rather than waiting for job completion.

## Problem

Currently, notifications are only sent after all jobs complete in a dispatch cycle. Long-running jobs have no way to report progress or status updates mid-execution. Users want to send messages from their scripts (e.g., Python) that flow through the dispatcher's existing notification channels (Discord, ntfy).

## Design

### New `notify` Subcommand

```
dispatch notify "Backup 50% complete"
dispatch notify --job mybackup "Backup 50% complete"
```

**Behavior:**
- Loads `Dispatcher.yaml` to get notification config (webhook URLs, ntfy settings)
- Reads `DISPATCH_JOB` env var for job context (set automatically by the runner)
- `--job` flag overrides the env var
- Sends to all configured channels (Discord + ntfy) immediately
- No lock, no DB access — pure fire-and-forget
- Exit 0 on success, exit 1 if no notification channels configured or all sends fail
- Multiple positional args joined with spaces (quoting optional)

### Environment Variable Injection

When the dispatcher runs a job, it sets `DISPATCH_JOB=<jobname>` in the child process environment. This is added to the `extraEnv` slice in `main.go` before calling `RunJob()` or `RunOnce()`, flowing through the existing `extraEnv` mechanism in `internal/runner/runner.go` (line 74).

Every child process — shell scripts, Python scripts, nested subprocesses — inherits it automatically.

### Notification Formatting

Live notifications are visually distinct from post-run summaries.

**Discord:**
- Embed color: `0x7289DA` (Discord blurple)
- Title: `[backup] Live Update` (or `Live Update` if no job context)
- Description: the message text
- Timestamp: current UTC time

**ntfy:**
- Title: `[backup] Live Update` (or `Live Update`)
- Body: the message text
- Priority: `default`
- Tags: `speech_balloon`

### Usage from Python

```python
import subprocess
subprocess.run(["dispatch", "notify", "Backup 50% complete"])
```

The `DISPATCH_JOB` env var is inherited automatically, so the notification includes the job name with no extra work.

## Implementation Scope

### Files modified

1. **`main.go`** — Add `notify` subcommand handler (~30 lines). Add `DISPATCH_JOB` env var to `extraEnv` in the dispatch/run/run-all paths.
2. **`internal/notify/notify.go`** — Add `SendLiveNotification(message, jobName string, cfg NotifyConfig)` function that sends to Discord + ntfy with the live update formatting.
3. **`README.md`** — Document the `notify` subcommand, `DISPATCH_JOB` env var, and Python usage example.

### Files not changed

- `internal/runner/` — no changes, env var flows through existing `extraEnv` mechanism
- `internal/db/` — no DB involvement
- `internal/config/` — no new config fields, reuses existing notification config
- `Dispatcher.yaml` — no schema changes

### No new dependencies, no new packages, no new config options.

## Testing

- Unit test for `SendLiveNotification` formatting (verify payload construction)
- Test that `notify` subcommand reads `DISPATCH_JOB` env var correctly
- Test fallback when env var is absent
- Manual test: run a job that calls `dispatch notify "hello"` and verify Discord/ntfy
