# Rename `install`/`uninstall` to `enable`/`disable` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Hard-rename the `install`/`uninstall` CLI commands to `enable`/`disable`, make re-running `enable` idempotently update the crontab when `schedule:` changes, and drop the optional cron-expression CLI argument so `Dispatcher.yaml` is the single source of truth.

**Architecture:** The existing `installCron` function in `main.go` both builds the new crontab line and decides whether to write it. We split this: a pure helper `buildCrontab(existing, newLine, projectDir) (content, status)` in a new file `cron.go` is unit-tested, and `enableCron` wraps it with the crontab read/write + user-facing print. `uninstallCron` is a straight rename to `disableCron`. The rest is renames and string substitutions.

**Tech Stack:** Go 1.22+, `os/exec` for `crontab -l` / `crontab -`, standard library only.

**Spec:** `docs/superpowers/specs/2026-04-11-enable-disable-rename-design.md`

---

## File Structure

- **Create:** `cron.go` — pure helpers `buildCrontab` and `cronStatus` constants.
- **Create:** `cron_test.go` — unit tests for `buildCrontab`.
- **Modify:** `main.go` — switch cases, help text, `installCron` → `enableCron` (reworked), `uninstallCron` → `disableCron` (rename only).
- **Modify:** `info.go` — status strings (`installed` → `enabled`).
- **Modify:** `internal/display/display.go` — status strings.
- **Modify:** `README.md` — replace occurrences, add note.

---

### Task 1: Add `buildCrontab` helper with unit tests

**Files:**
- Create: `cron.go`
- Create: `cron_test.go`

- [ ] **Step 1: Write failing tests**

Create `cron_test.go` with:

```go
package main

import "testing"

func TestBuildCrontab_AddsToEmpty(t *testing.T) {
	newLine := "*/5 * * * * cd /proj && /bin/dispatch >> .dispatcher/logs/dispatcher.log 2>&1"
	content, status := buildCrontab("", newLine, "/proj")
	if status != cronAdded {
		t.Errorf("status = %v, want cronAdded", status)
	}
	if content != newLine+"\n" {
		t.Errorf("content = %q", content)
	}
}

func TestBuildCrontab_AppendsAlongsideUnrelated(t *testing.T) {
	existing := "0 0 * * * /usr/bin/backup\n"
	newLine := "*/5 * * * * cd /proj && /bin/dispatch >> .dispatcher/logs/dispatcher.log 2>&1"
	content, status := buildCrontab(existing, newLine, "/proj")
	if status != cronAdded {
		t.Errorf("status = %v, want cronAdded", status)
	}
	want := "0 0 * * * /usr/bin/backup\n" + newLine + "\n"
	if content != want {
		t.Errorf("content = %q, want %q", content, want)
	}
}

func TestBuildCrontab_UnchangedWhenIdentical(t *testing.T) {
	line := "*/5 * * * * cd /proj && /bin/dispatch >> .dispatcher/logs/dispatcher.log 2>&1"
	existing := line + "\n"
	content, status := buildCrontab(existing, line, "/proj")
	if status != cronUnchanged {
		t.Errorf("status = %v, want cronUnchanged", status)
	}
	if content != existing {
		t.Errorf("content = %q", content)
	}
}

func TestBuildCrontab_UpdatesWhenScheduleDiffers(t *testing.T) {
	oldLine := "*/5 * * * * cd /proj && /bin/dispatch >> .dispatcher/logs/dispatcher.log 2>&1"
	newLine := "*/10 * * * * cd /proj && /bin/dispatch >> .dispatcher/logs/dispatcher.log 2>&1"
	existing := "0 0 * * * /usr/bin/backup\n" + oldLine + "\n"
	content, status := buildCrontab(existing, newLine, "/proj")
	if status != cronUpdated {
		t.Errorf("status = %v, want cronUpdated", status)
	}
	want := "0 0 * * * /usr/bin/backup\n" + newLine + "\n"
	if content != want {
		t.Errorf("content = %q, want %q", content, want)
	}
}

func TestBuildCrontab_OnlyMatchesThisProject(t *testing.T) {
	otherLine := "*/5 * * * * cd /other && /bin/dispatch >> .dispatcher/logs/dispatcher.log 2>&1"
	newLine := "*/5 * * * * cd /proj && /bin/dispatch >> .dispatcher/logs/dispatcher.log 2>&1"
	existing := otherLine + "\n"
	content, status := buildCrontab(existing, newLine, "/proj")
	if status != cronAdded {
		t.Errorf("status = %v, want cronAdded (other project's line must not match)", status)
	}
	want := otherLine + "\n" + newLine + "\n"
	if content != want {
		t.Errorf("content = %q, want %q", content, want)
	}
}
```

- [ ] **Step 2: Run tests, verify they fail**

Run: `go test ./... -run TestBuildCrontab -v`
Expected: FAIL — `buildCrontab`, `cronAdded`, `cronUnchanged`, `cronUpdated` undefined.

- [ ] **Step 3: Implement `buildCrontab`**

Create `cron.go`:

```go
package main

import "strings"

type cronStatus int

const (
	cronAdded cronStatus = iota
	cronUpdated
	cronUnchanged
)

// buildCrontab returns the new crontab content and a status describing what
// changed. It replaces any existing dispatch line for projectDir with newLine,
// or appends newLine if none exists.
func buildCrontab(existing, newLine, projectDir string) (string, cronStatus) {
	trimmed := strings.TrimRight(existing, "\n")
	var lines []string
	if trimmed != "" {
		lines = strings.Split(trimmed, "\n")
	}

	var out []string
	matched := false
	changed := false
	for _, line := range lines {
		if strings.Contains(line, "dispatch") && strings.Contains(line, projectDir) {
			matched = true
			if line != newLine {
				changed = true
				out = append(out, newLine)
			} else {
				out = append(out, line)
			}
		} else {
			out = append(out, line)
		}
	}

	if !matched {
		out = append(out, newLine)
		return strings.Join(out, "\n") + "\n", cronAdded
	}
	content := strings.Join(out, "\n") + "\n"
	if changed {
		return content, cronUpdated
	}
	return content, cronUnchanged
}
```

- [ ] **Step 4: Run tests, verify they pass**

Run: `go test ./... -run TestBuildCrontab -v`
Expected: all five PASS.

- [ ] **Step 5: Commit**

```bash
git add cron.go cron_test.go
git commit -m "feat: add buildCrontab helper for idempotent crontab updates"
```

---

### Task 2: Rewrite `installCron` → `enableCron` using the helper

**Files:**
- Modify: `main.go:784-813`

- [ ] **Step 1: Replace `installCron` with `enableCron`**

In `main.go`, replace the entire `installCron` function (lines 784–813) with:

```go
func enableCron(schedule string, projectDir string) {
	dispatchPath, err := os.Executable()
	if err != nil {
		dispatchPath = "dispatch"
	}
	cronLine := fmt.Sprintf("%s cd %s && %s >> .dispatcher/logs/dispatcher.log 2>&1", schedule, projectDir, dispatchPath)

	out, _ := exec.Command("crontab", "-l").Output()
	existing := string(out)

	newContent, status := buildCrontab(existing, cronLine, projectDir)

	if status == cronUnchanged {
		fmt.Printf("Already enabled (unchanged): %s\n", cronLine)
		return
	}

	cmd := exec.Command("crontab", "-")
	cmd.Stdin = strings.NewReader(newContent)
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to write crontab: %v\n", err)
		os.Exit(1)
	}

	switch status {
	case cronAdded:
		fmt.Printf("Cron enabled: %s\n", cronLine)
	case cronUpdated:
		fmt.Printf("Cron updated: %s\n", cronLine)
	}
}
```

- [ ] **Step 2: Build**

Run: `go build -o dispatch .`
Expected: succeeds (the old `installCron` callsite still references the old name — that gets fixed in Task 4, but this task may leave `enableCron` unused temporarily. Build will still succeed because Go allows unused package-level functions.)

Note: If the build fails because `installCron` is still referenced, proceed to Task 4 immediately — Tasks 2, 3, 4 form a unit that must land together to compile. In that case commit the three tasks as one; see Task 4, Step 6.

- [ ] **Step 3: Commit (hold if Task 4 merges)**

```bash
git add main.go
git commit -m "refactor: replace installCron with enableCron using buildCrontab"
```

---

### Task 3: Rename `uninstallCron` → `disableCron`

**Files:**
- Modify: `main.go:837`

- [ ] **Step 1: Rename the function and update its success message**

In `main.go`:

1. Change the signature at line 837: `func uninstallCron(projectDir string) {` → `func disableCron(projectDir string) {`.
2. Change the success print at line 864: `fmt.Printf("Cron removed for %s\n", projectDir)` → `fmt.Printf("Cron disabled for %s\n", projectDir)`.
3. Leave `"No crontab found"` and `"No cron entry found for %s"` alone — they are still accurate.

- [ ] **Step 2: Build**

Run: `go build -o dispatch .`
Expected: succeeds unless callsite still uses old name (fixed in Task 4).

- [ ] **Step 3: Commit (hold if Task 4 merges)**

```bash
git add main.go
git commit -m "refactor: rename uninstallCron to disableCron"
```

---

### Task 4: Update CLI switch cases, help text, and drop args override

**Files:**
- Modify: `main.go:45-46` (help text)
- Modify: `main.go:282-294` (switch cases)

- [ ] **Step 1: Update the help text block**

In `main.go`, lines 45–46, change:

```
  install      Install crontab entry
  uninstall    Remove crontab entry
```

to:

```
  enable       Enable the scheduler (install crontab entry from config)
  disable      Disable the scheduler (remove crontab entry)
```

- [ ] **Step 2: Update the switch cases**

In `main.go`, replace lines 282–294:

```go
	// install/uninstall don't need DB
	if cmd == "install" {
		schedule := cfg.Schedule
		if len(args) > 0 {
			schedule = args[0] // CLI arg overrides config
		}
		installCron(schedule, configDir)
		return
	}
	if cmd == "uninstall" {
		uninstallCron(configDir)
		return
	}
```

with:

```go
	// enable/disable don't need DB
	if cmd == "enable" {
		enableCron(cfg.Schedule, configDir)
		return
	}
	if cmd == "disable" {
		disableCron(configDir)
		return
	}
```

- [ ] **Step 3: Build**

Run: `go build -o dispatch .`
Expected: succeeds cleanly.

- [ ] **Step 4: Run full test suite**

Run: `go test ./...`
Expected: all tests pass (including the new `buildCrontab` tests from Task 1).

- [ ] **Step 5: Smoke-test the new commands**

Run: `./dispatch --help 2>&1 | grep -E 'enable|disable'`
Expected: lines showing `enable` and `disable` with their descriptions; no lines containing `install` or `uninstall`.

Run: `./dispatch install 2>&1 | head -5`
Expected: some "unknown command" error path (exact wording depends on current default case — may just print help). The point is `install` no longer works.

- [ ] **Step 6: Commit**

```bash
git add main.go
git commit -m "feat: rename install/uninstall to enable/disable"
```

If Tasks 2 and 3 were not separately committed (because the build required all three together), stage `cron.go` and `main.go` together here with the same message.

---

### Task 5: Update user-facing status vocabulary

**Files:**
- Modify: `info.go:33,36`
- Modify: `internal/display/display.go:168-170`

- [ ] **Step 1: Update `info.go`**

In `info.go`, change:

```go
		fmt.Println("  Status:        installed")
```

to:

```go
		fmt.Println("  Status:        enabled")
```

And change:

```go
		fmt.Println("  Status:        not installed")
```

to:

```go
		fmt.Println("  Status:        disabled")
```

- [ ] **Step 2: Update `internal/display/display.go`**

In `internal/display/display.go`, change:

```go
	cronStatus := "not installed"
	if installed, schedule := IsCronInstalled(projectDir); installed {
		cronStatus = "installed (" + schedule + ")"
	}
```

to:

```go
	cronStatus := "disabled"
	if installed, schedule := IsCronInstalled(projectDir); installed {
		cronStatus = "enabled (" + schedule + ")"
	}
```

Note: the function name `IsCronInstalled` is internal — leave it alone. Only user-visible strings change.

- [ ] **Step 3: Build and test**

Run: `go build -o dispatch . && go test ./...`
Expected: build succeeds, all tests pass.

- [ ] **Step 4: Commit**

```bash
git add info.go internal/display/display.go
git commit -m "feat: use enabled/disabled vocabulary in status output"
```

---

### Task 6: Update README

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Update the example on line 58**

Change:

```
dispatch install           # set up cron to run every 5 minutes
```

to:

```
dispatch enable            # set up cron to run every 5 minutes
```

- [ ] **Step 2: Update the command list (lines 290–291)**

Change:

```
dispatch install         # add crontab entry (default: */5 * * * *)
dispatch uninstall       # remove crontab entry
```

to:

```
dispatch enable          # enable scheduler (install crontab entry from config)
dispatch disable         # disable scheduler (remove crontab entry)
```

- [ ] **Step 3: Update the Crontab integration section (lines 301–321)**

Replace the whole section starting at line 299 (heading `## Crontab integration`) through line 321 (closing code block) with:

```markdown
## Crontab integration

`dispatch enable` installs a crontab entry that runs the dispatcher on the schedule defined in `Dispatcher.yaml`:

```yaml
schedule: "*/1 * * * *"   # check for due jobs every minute
```

If omitted, defaults to `*/5 * * * *` (every 5 minutes). The `schedule:` field in the config is the single source of truth — there is no CLI override.

```bash
# Enable scheduler using schedule from config
dispatch enable

# To change the schedule: edit Dispatcher.yaml and re-run enable
# (enable is idempotent: it updates the existing crontab line if the schedule changed)
dispatch enable

# Check if enabled
dispatch status

# Disable
dispatch disable
```
```

(Note: the inner code fences must be escaped correctly when editing — this plan shows them unescaped for readability. In the actual file, use backticks normally.)

- [ ] **Step 4: Update the `dispatch info` sample output (line 394)**

Change `  Status:        installed` to `  Status:        enabled`.

- [ ] **Step 5: Update line 417**

Change:

```
Useful for debugging config issues — catches empty `${VAR}` expansions, forgotten defaults, and verifying cron is installed.
```

to:

```
Useful for debugging config issues — catches empty `${VAR}` expansions, forgotten defaults, and verifying the scheduler is enabled.
```

- [ ] **Step 6: Update the compatibility table (line 464)**

Change:

```
| `dispatch install` / `uninstall` | Not supported (no crontab) | Works |
```

to:

```
| `dispatch enable` / `disable` | Not supported (no crontab) | Works |
```

- [ ] **Step 7: Verify no stragglers**

Run: `grep -nE 'dispatch (install|uninstall)' README.md`
Expected: no output (all occurrences replaced). If any remain, fix them.

- [ ] **Step 8: Commit**

```bash
git add README.md
git commit -m "docs: update README for enable/disable rename"
```

---

### Task 7: Final verification

**Files:** none (verification only)

- [ ] **Step 1: Full build + test**

Run: `go build -o dispatch . && go test ./...`
Expected: build succeeds, all tests pass.

- [ ] **Step 2: Manual round-trip against a real crontab**

Run each command in order and confirm the expected output:

```bash
./dispatch enable
```
Expected: `Cron enabled: */5 * * * * cd <dir> && <dispatch> >> .dispatcher/logs/dispatcher.log 2>&1`

```bash
./dispatch enable
```
Expected: `Already enabled (unchanged): …`

Now edit `Dispatcher.yaml` and set `schedule: "*/10 * * * *"`. Then:

```bash
./dispatch enable
```
Expected: `Cron updated: */10 * * * * cd <dir> && …`

```bash
crontab -l | grep dispatch
```
Expected: exactly one line, showing `*/10 * * * *`.

```bash
./dispatch info
```
Expected: `Status:        enabled`

```bash
./dispatch disable
```
Expected: `Cron disabled` (or existing disable message) and the line is gone.

```bash
crontab -l | grep dispatch
```
Expected: no output.

```bash
./dispatch info
```
Expected: `Status:        disabled`

Revert `Dispatcher.yaml` if you want to restore the original schedule.

- [ ] **Step 3: Confirm no remaining references to old names**

Run: `grep -rnE '\b(installCron|uninstallCron)\b' --include='*.go' .`
Expected: no output.

Run: `grep -rnE 'dispatch (install|uninstall)' --include='*.md' .`
Expected: only occurrences in `docs/superpowers/plans/` historical docs (those are out of scope). `README.md` must be clean.

- [ ] **Step 4: No commit needed for this task** — verification only. If any fix was needed in Steps 1–3, commit it separately.

---

## Notes

- **Why no test for `enableCron` itself?** It shells out to `crontab`, which is not unit-testable without mocking `exec.Command`. The testable logic lives in `buildCrontab`; `enableCron` is a thin wrapper that we verify by manual round-trip in Task 7.
- **Why not extract `disableCron`'s logic into `buildCrontab` too?** The spec scope is enable/disable rename + the new idempotent-update behavior. `disableCron`'s logic isn't changing — only its name — so refactoring it is out of scope. If a future change touches it, extract then.
- **Historical plans under `docs/superpowers/plans/`** reference `installCron`/`uninstallCron`. Leave them alone — they describe past work and should not be rewritten.
