# Rename `install`/`uninstall` to `enable`/`disable`

## Motivation

`dispatch install` and `dispatch uninstall` sound like package-manager verbs — they
imply installing or removing the `dispatch` binary, when in fact they only
manage a crontab entry that invokes dispatch. Rename them to `enable` and
`disable`, which describe what actually happens: the scheduler is turned on or
off.

## Scope

Hard rename. No backwards-compatible aliases. The audience is small and
scripted use is unlikely; a clean break is preferable to carrying a
deprecation surface.

While renaming, also remove the optional cron-expression argument to `enable`
— the schedule should be read from `Dispatcher.yaml` (`schedule:` key) as the
single source of truth.

## Changes

### 1. CLI commands (`main.go`)

- Rename switch case `"install"` → `"enable"` (main.go:283).
- Rename switch case `"uninstall"` → `"disable"` (main.go:291).
- Drop the `args[0]` override in the `enable` branch. `enable` takes no
  arguments; it always uses `cfg.Schedule`.
- Update the help text block (main.go:45–46):
  - `enable       Enable the scheduler (install crontab entry from config)`
  - `disable      Disable the scheduler (remove crontab entry)`
- Rename `uninstallCron` → `disableCron` for vocabulary consistency. The
  inline install logic may stay inline or be pulled into an `enableCron`
  helper — minor cleanup, not required.

### 2. Re-enable semantics (idempotent update)

Current behavior (main.go:797–803): if a crontab line mentioning `dispatch`
plus this project dir already exists, print "Cron already installed" and
exit without modifying the crontab. This means editing `schedule:` in the
config and re-running `enable` is silently a no-op — a footgun.

New behavior:

1. Read the existing crontab.
2. Build the new cron line from `cfg.Schedule`.
3. If a matching dispatch line already exists:
   - If it is byte-identical to the new line, print
     `Already enabled (unchanged): <line>` and exit 0.
   - Otherwise, remove the old line and insert the new one in its place.
     Print `Cron updated: <new line>`.
4. If no matching line exists, append the new line and print
   `Cron enabled: <new line>`.

This makes `enable` the canonical way to push config changes to the crontab:
edit `schedule:`, run `dispatch enable`, done.

### 3. Status vocabulary

Change the user-visible status strings to match the new commands:

- `info.go:33,36` — `"installed"` / `"not installed"` → `"enabled"` /
  `"disabled"`.
- `internal/display/display.go:168–170` — same substitution.

Internal identifiers (`IsCronInstalled`, etc.) can stay; they are not
user-visible.

### 4. README

- Replace every `dispatch install` / `dispatch uninstall` occurrence with
  `dispatch enable` / `dispatch disable`.
- Add a short note: "These commands manage the crontab entry that runs
  `dispatch`. The schedule is taken from the `schedule:` key in
  `Dispatcher.yaml` (default `*/5 * * * *`). To change the schedule, edit
  the config and re-run `dispatch enable`."

### 5. Tests

Grep for any tests that invoke `install`/`uninstall` as subcommands or
assert on the old status strings, and update them. No new test coverage is
required beyond updating existing references — the re-enable update path
can be verified by a small unit test around the crontab-mutation function
if one exists, otherwise by manual verification.

## Out of scope

- Historical design docs under `docs/superpowers/plans/` — leave as-is.
- `active_days` — a separate discussion; not part of this change.
- Any change to the cron expression syntax or parsing.

## Verification

1. `go build -o dispatch .`
2. `go test ./...`
3. Manual round-trip:
   - `./dispatch enable` → expect `Cron enabled: …`
   - `./dispatch enable` → expect `Already enabled (unchanged): …`
   - Edit `Dispatcher.yaml` `schedule:` to a new value.
   - `./dispatch enable` → expect `Cron updated: …`
   - `./dispatch disable` → expect crontab line removed.
   - `crontab -l` to confirm at each step.
4. `./dispatch info` shows `Status: enabled` / `Status: disabled`.
