# Pause Feature Design

Stop scheduled jobs from running while allowing manual runs. Backed by a sentinel file with configurable auto-expiry.

## Behavior

### What gets paused

Only the default dispatch path (cron-triggered `dispatch` with no args). These commands still work normally while paused:

- `dispatch run <job>` — manual run with DB tracking
- `dispatch run-once <job>` — manual run without DB tracking
- `dispatch run-all` — explicit run-all
- `dispatch list`, `dispatch status`, etc. — read-only commands

### Pause flow

1. User runs `dispatch pause [duration] [reason]`
2. Creates `.dispatcher/paused` containing JSON: `{"expires_at": "...", "reason": "...", "paused_at": "..."}`
3. Cron fires `dispatch` — sees the file, checks expiry:
   - **Not expired:** prints "Dispatcher paused until HH:MM (reason)" and exits 0
   - **Expired:** deletes the file, prints "Pause expired — resuming", continues with normal dispatch
4. User runs `dispatch resume` to cancel early

### CLI commands

```
dispatch pause                    # pause for default duration (from config), no reason
dispatch pause 2h                 # pause for 2 hours
dispatch pause "deploying"        # pause for default duration with reason
dispatch pause 2h "deploying"     # pause for 2 hours with reason
dispatch resume                   # remove pause immediately
```

Duration parsing reuses the existing `config.ParseInterval` function (supports `30m`, `1h`, `2h`, `1d`, etc.).

Argument disambiguation: if the first arg after `pause` parses as a valid interval, it's the duration. Otherwise it's the reason.

### Config

New top-level field in `Dispatcher.yaml`:

```yaml
pause_timeout: 1h
```

Default: `1h` if not specified in config.

### Sentinel file

Path: `.dispatcher/paused`

Format (JSON):
```json
{
  "expires_at": "2026-04-07T15:30:00Z",
  "reason": "deploying auth changes",
  "paused_at": "2026-04-07T14:30:00Z"
}
```

The file can also be created/removed manually (`touch`/`rm`). If the file exists but can't be parsed as JSON (e.g., empty from `touch`), treat it as paused with no expiry — must be manually resumed.

### Status visibility

`dispatch status` and `dispatch list` show a banner when paused:

```
PAUSED until 15:30 EDT — deploying auth changes
```

If no expiry (manual touch): `PAUSED (no expiry — run 'dispatch resume' to unpause)`

## Implementation scope

### Files to modify

1. **internal/config/config.go** — add `PauseTimeout` field to `DispatcherConfig`, parse it alongside retention
2. **main.go** — add `pause` and `resume` command handling, add pause check at top of `dispatch()` function
3. **internal/display/display.go** — add pause banner to `PrintStatus` and `PrintQuickStatus`

### New files

None. A `pause.go` helper in the root package would be overkill — the logic is small enough to live in `main.go` alongside the other command handlers.

### Pause file operations (in main.go)

- `writePauseFile(dispDir, expiresAt, reason)` — writes JSON to `.dispatcher/paused`
- `readPauseFile(dispDir) (*PauseInfo, error)` — reads and parses, returns nil if not paused
- `removePauseFile(dispDir)` — deletes the file
- `checkPause(dispDir) (paused bool, msg string)` — reads file, checks expiry, auto-removes if expired

### Pause check location

In `main.go`, inside the `case ""` block (default dispatch), before calling `dispatch()`:

```go
case "":
    if paused, msg := checkPause(dispDir); paused {
        fmt.Println(msg)
        return
    }
    dispatch(conn, cfg, notifyCfg)
```

This keeps it out of `run`, `run-all`, and other commands.

### Config change

```go
type DispatcherConfig struct {
    // ... existing fields
    PauseTimeout int `yaml:"pause_timeout"` // seconds, parsed from interval string
}
```

Default to 3600 (1h) when not set, same pattern as `Retention`.

### Display changes

Both `PrintStatus` and `PrintQuickStatus` accept the dispatcher dir path. Before printing the table, call `readPauseFile` and if paused, print the banner line.

Alternatively, pass the pause info from main.go to avoid the display package needing to know about file paths. Cleaner: main.go reads pause state once and passes a `*PauseInfo` to display functions.

### Error handling

- File doesn't exist: not paused
- File exists but empty/unparseable: paused, no expiry (manual touch case)
- File exists, valid JSON, expired: auto-remove and proceed
- `dispatch resume` when not paused: "Dispatcher is not paused" message

### Tests

- `config.ParseInterval` already tested — no new tests needed there
- Test `checkPause` with: no file, valid file not expired, valid file expired, empty file (manual touch)
- Test pause/resume CLI flow via integration test pattern if one exists
