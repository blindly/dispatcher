# .dispatcher Directory — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move all dispatcher runtime files (data.db, logs/, .dispatch.lock) into a `.dispatcher/` hidden directory to avoid polluting the project root.

**Architecture:** All runtime state moves under `<configDir>/.dispatcher/`. On startup, if old files exist in the project root and `.dispatcher/` doesn't have them yet, auto-migrate. Remove the `db_path` config option — `.dispatcher/data.db` is always the answer. The cron install command also updates its log path.

**Tech Stack:** Go, SQLite

---

## File Map

| File | Action | Responsibility |
|------|--------|----------------|
| `internal/config/config.go` | Modify | Remove `DbPath` from config structs |
| `internal/config/config_test.go` | Modify | Remove `TestLoad_DbPath` test |
| `main.go` | Modify | Add migration logic, use `.dispatcher/` for DB path, pass `.dispatcher/` to runner/lock |
| `internal/runner/runner.go` | Modify (minor) | `openJobLog` already uses `logBaseDir` — just needs `.dispatcher/logs` passed in |
| `lock_unix.go` | No change | Already takes `dir` param — just pass `.dispatcher/` |
| `lock_windows.go` | No change | Same — already takes `dir` param |
| `internal/display/display.go` | No change | Doesn't reference file paths directly |
| `.gitignore` | Modify | Replace individual entries with `.dispatcher/` |
| `README.md` | Modify | Update file layout docs |
| `CLAUDE.md` | Modify | Update architecture description |

---

### Task 1: Remove `db_path` config option

**Files:**
- Modify: `internal/config/config.go:48-56` (DispatcherConfig struct)
- Modify: `internal/config/config.go:92-101` (rawConfig struct)
- Modify: `internal/config/config.go:202-210` (Load function)
- Modify: `internal/config/config_test.go:160-179` (TestLoad_DbPath)

- [ ] **Step 1: Remove `DbPath` from DispatcherConfig struct**

In `internal/config/config.go`, remove the `DbPath` field from `DispatcherConfig`:

```go
type DispatcherConfig struct {
	Timezone  string            `yaml:"timezone"`
	Notify    NotifyConfig      `yaml:"notify"`
	Jobs      map[string]*JobConfig
	Schedule  string            `yaml:"schedule"`
	Retention int               `yaml:"-"`
	Vars      map[string]string `yaml:"vars"`
}
```

- [ ] **Step 2: Remove `DbPath` from rawConfig struct**

In `internal/config/config.go`, remove the `DbPath` field from `rawConfig`:

```go
type rawConfig struct {
	Timezone       string            `yaml:"timezone"`
	Notify         NotifyConfig      `yaml:"notify"`
	Jobs           map[string]rawJob `yaml:"jobs"`
	Schedule       string            `yaml:"schedule"`
	Retention      string            `yaml:"retention"`
	DiscordWebhook string            `yaml:"discord_webhook"`
	Vars           map[string]string `yaml:"vars"`
}
```

- [ ] **Step 3: Remove `DbPath` assignment in Load()**

In `internal/config/config.go`, remove `DbPath: raw.DbPath,` from the `cfg := &DispatcherConfig{...}` block.

- [ ] **Step 4: Remove `TestLoad_DbPath` test**

Delete the entire `TestLoad_DbPath` function from `internal/config/config_test.go` (lines 160-179).

- [ ] **Step 5: Build and run config tests**

Run: `go build ./... && go test ./internal/config/ -v`
Expected: all tests pass, no compile errors

- [ ] **Step 6: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "refactor: remove db_path config option"
```

---

### Task 2: Add migration helper and use `.dispatcher/` for all runtime files

**Files:**
- Modify: `main.go:279-296` (DB path logic)
- Modify: `main.go:403-408` (lock path)
- Modify: `main.go:224` (logs command)
- Modify: `main.go:548` (watchLogs)
- Modify: `main.go:638` (installCron)

- [ ] **Step 1: Add `ensureDispatcherDir` and `migrateFiles` functions to main.go**

Add these functions after the `detectConfig()` function:

```go
// ensureDispatcherDir creates the .dispatcher directory and migrates old files from the project root.
func ensureDispatcherDir(configDir string) string {
	dispDir := filepath.Join(configDir, ".dispatcher")
	os.MkdirAll(dispDir, 0755)

	// Migrate old files from project root into .dispatcher/
	migrations := []string{"data.db", "data.db-shm", "data.db-wal"}
	for _, name := range migrations {
		oldPath := filepath.Join(configDir, name)
		newPath := filepath.Join(dispDir, name)
		if _, err := os.Stat(oldPath); err == nil {
			if _, err := os.Stat(newPath); os.IsNotExist(err) {
				os.Rename(oldPath, newPath)
			}
		}
	}

	// Migrate logs directory
	oldLogs := filepath.Join(configDir, "logs")
	newLogs := filepath.Join(dispDir, "logs")
	if info, err := os.Stat(oldLogs); err == nil && info.IsDir() {
		if _, err := os.Stat(newLogs); os.IsNotExist(err) {
			os.Rename(oldLogs, newLogs)
		}
	}

	// Migrate lock file
	oldLock := filepath.Join(configDir, ".dispatch.lock")
	if _, err := os.Stat(oldLock); err == nil {
		os.Remove(oldLock)
	}

	// Migrate crontab entry (update logs/ path to .dispatcher/logs/)
	migrateCron(configDir)

	return dispDir
}

// migrateCron updates an existing crontab entry to use the new .dispatcher/logs/ path.
func migrateCron(projectDir string) {
	out, err := exec.Command("crontab", "-l").Output()
	if err != nil {
		return
	}

	lines := strings.Split(string(out), "\n")
	changed := false
	for i, line := range lines {
		if strings.Contains(line, "dispatch") && strings.Contains(line, projectDir) {
			oldLog := ">> logs/dispatcher.log"
			newLog := ">> .dispatcher/logs/dispatcher.log"
			if strings.Contains(line, oldLog) && !strings.Contains(line, newLog) {
				lines[i] = strings.Replace(line, oldLog, newLog, 1)
				changed = true
			}
		}
	}

	if !changed {
		return
	}

	cmd := exec.Command("crontab", "-")
	cmd.Stdin = strings.NewReader(strings.Join(lines, "\n"))
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not update crontab: %v\n", err)
		return
	}
	fmt.Println("Migrated crontab entry: logs/ → .dispatcher/logs/")
}
```

- [ ] **Step 2: Replace DB path logic in main()**

Replace the current `dbPath` block (lines ~279-288) with:

```go
dispDir := ensureDispatcherDir(configDir)
dbPath := filepath.Join(dispDir, "data.db")
```

This replaces the old code that checked `cfg.DbPath`.

- [ ] **Step 3: Update `runner.SetLogDir` to use `.dispatcher/`**

Change the `runner.SetLogDir(configDir)` call to:

```go
runner.SetLogDir(dispDir)
```

And in `internal/runner/runner.go`, `openJobLog` already builds `filepath.Join(logBaseDir, "logs")`, so passing `dispDir` will produce `.dispatcher/logs/`.

- [ ] **Step 4: Update lock calls to use `.dispatcher/`**

Change the lock acquisition/release calls from `configDir` to `dispDir`. The `acquireLock` and `releaseLock` functions already accept `dir` as a parameter.

```go
lockFd := acquireLock(dispDir)
if lockFd == -1 {
    fmt.Println("Another dispatcher is already running — skipping")
    return
}
defer releaseLock(lockFd, dispDir)
```

- [ ] **Step 5: Update `logs` command path**

Change the log path in the `logs` command handler from:

```go
logPath := filepath.Join(configDir, "logs", jobName+".log")
```

to:

```go
logPath := filepath.Join(dispDir, "logs", jobName+".log")
```

Note: `dispDir` needs to be accessible here. Since `ensureDispatcherDir` is called before this point in main(), this is fine — just move the `dispDir` variable declaration up so it's in scope for all commands that need it.

- [ ] **Step 6: Update `watchLogs` function**

Change the `watchLogs` call from:

```go
watchLogs(configDir, jobName, cfg.Jobs)
```

to:

```go
watchLogs(dispDir, jobName, cfg.Jobs)
```

The `watchLogs` function already uses its first parameter as the base dir for `filepath.Join(configDir, "logs")`.

- [ ] **Step 7: Update `installCron` log path**

In the `installCron` function, change the cron line from:

```go
cronLine := fmt.Sprintf("%s cd %s && %s >> logs/dispatcher.log 2>&1", schedule, projectDir, dispatchPath)
```

to:

```go
cronLine := fmt.Sprintf("%s cd %s && %s >> .dispatcher/logs/dispatcher.log 2>&1", schedule, projectDir, dispatchPath)
```

- [ ] **Step 8: Build and run all tests**

Run: `go build ./... && go test ./...`
Expected: all tests pass

- [ ] **Step 9: Commit**

```bash
git add main.go internal/runner/runner.go
git commit -m "feat: move runtime files into .dispatcher/ directory with auto-migration"
```

---

### Task 3: Update `.gitignore`

**Files:**
- Modify: `.gitignore`

- [ ] **Step 1: Replace individual entries with `.dispatcher/`**

Replace the current `.gitignore`:

```
dispatch
data.db
logs/
.dispatch.lock
.env
CLAUDE.md
data.db-shm
data.db-wal
```

with:

```
dispatch
.dispatcher/
.env
CLAUDE.md
```

- [ ] **Step 2: Commit**

```bash
git add .gitignore
git commit -m "chore: simplify .gitignore — .dispatcher/ covers all runtime files"
```

---

### Task 4: Update docs

**Files:**
- Modify: `README.md:341`
- Modify: `CLAUDE.md`

- [ ] **Step 1: Update README "How it works" section**

Replace line 341:

> SQLite state is stored as `data.db` next to the config file. A file lock prevents concurrent dispatch runs. Per-job output is logged to `logs/<name>.log`.

with:

> Runtime files (SQLite DB, job logs, lock file) are stored in `.dispatcher/` next to the config file. Existing files are auto-migrated on first run.

- [ ] **Step 2: Update CLAUDE.md architecture description**

In the `db` package description, update:

> SQLite stored next to config as `data.db`. All times UTC RFC3339.

to:

> All runtime state stored in `.dispatcher/` directory (data.db, logs/, lock file). All times UTC RFC3339.

- [ ] **Step 3: Commit**

```bash
git add README.md CLAUDE.md
git commit -m "docs: update file layout for .dispatcher/ directory"
```

---

### Task 5: Manual integration test

- [ ] **Step 1: Build and verify migration works**

```bash
go build -o dispatch .
```

On an existing project with `data.db` and `logs/` in the root:

```bash
./dispatch status
```

Expected: `.dispatcher/` directory created. `data.db` moved into it. `logs/` moved into it. Status output works correctly using migrated data.

- [ ] **Step 2: Verify `list`, `logs`, `watch` commands work**

```bash
./dispatch list
./dispatch logs <some-job>
```

Expected: all commands read from `.dispatcher/` paths.

- [ ] **Step 3: Verify cron install uses new path**

```bash
./dispatch install "*/5 * * * *"
```

Expected: cron line references `.dispatcher/logs/dispatcher.log`.
