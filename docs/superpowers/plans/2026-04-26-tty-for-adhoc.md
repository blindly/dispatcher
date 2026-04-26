# TTY support for ad-hoc runs Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let `dispatch run` and `dispatch run-once` allocate a real PTY when invoked from an interactive terminal, so commands like `tail -f`, `vim`, `htop`, programs that color via `isatty`, and Ctrl+C work as a user expects. The scheduled dispatch loop (cron path) remains buffered/logged exactly as today.

**Architecture:** Two gates decide whether a child gets a PTY:
1. **Call-site gate** — only `run`, `run-once`, and `run-all` opt in by passing `interactive=true` into the runner. The dispatch loop and any other call site stays at `interactive=false`.
2. **Runtime gate** — even when `interactive=true`, only allocate a PTY if `isatty(os.Stdin.Fd())` reports a real terminal. If stdin is `/dev/null` (cron) or a pipe (CI, scripted invocation), fall back to the existing buffered path.

When both gates pass, the runner uses `github.com/creack/pty` to spawn the child with a controlling TTY, puts the parent stdin into raw mode, forwards SIGWINCH for window resizes, and skips the per-job log file capture (raw PTY output contains ANSI escape codes that pollute logs). The default 600s timeout is also disabled for PTY runs so `tail -f` can stay open until the user hits Ctrl+C.

**Tech Stack:** Go 1.26, `github.com/creack/pty` (new direct dep, pure Go on Unix), `github.com/mattn/go-isatty` (promote from transitive to direct), `golang.org/x/term` (for raw-mode + SIGWINCH; pure Go).

---

## File Structure

- **Modify** `go.mod` / `go.sum` — add `github.com/creack/pty`, promote `github.com/mattn/go-isatty` to direct, add `golang.org/x/term`.
- **Create** `internal/runner/tty_unix.go` (build tag `//go:build !windows`) — PTY allocation, raw-mode setup/teardown, SIGWINCH forwarding, signal handling. Single function `runCommandTTY`.
- **Create** `internal/runner/tty_windows.go` (build tag `//go:build windows`) — stub that returns an error so the build still works on Windows; runner falls back to buffered path.
- **Modify** `internal/runner/runner.go` — thread an `interactive bool` parameter through `runOnceWithLog` → `runCommand`. Add new public entry points `RunOnceInteractive` and `RunJobInteractive`. Keep existing `RunOnce` and `RunJob` signatures untouched (they forward with `interactive=false`).
- **Modify** `main.go` — switch the `run-once`, `run` (both adhoc and non-adhoc branches), and `run-all` call sites to use the `*Interactive` variants.
- **Modify** `internal/runner/runner_test.go` — add a regression test that `interactive=true` with a non-TTY stdin (test environment) still returns identical results to `interactive=false`.

Each task below produces a self-contained, testable change. Commit between tasks.

---

## Task 1: Dependencies (added in-place by Tasks 3 and 4)

**Status:** Originally this task pre-added deps as a separate commit. Code review found that satisfying "all three direct in `go.mod`" before any code imports them required scaffolding (blank imports) that polluted `main.go` with no enforced cleanup. The cleaner approach is to add each dep alongside its first real import.

**Resulting plan:**
- Task 3 will `go get github.com/mattn/go-isatty@latest` immediately before adding its real `isatty.IsTerminal(...)` call in `internal/runner/runner.go`.
- Task 4 will `go get github.com/creack/pty@latest golang.org/x/term@latest` immediately before writing `internal/runner/tty_unix.go`.
- After each `go get`, run `go mod tidy` and confirm the dep stays in the direct `require` block (it will, because the import is real).

No work to do in Task 1. Move directly to Task 2.

---

## Task 2: Write the failing regression test for the new entry point signatures

We're about to add `RunOnceInteractive` and `RunJobInteractive`. Before implementing them, write a test that calls them — this proves the public API is wired up and gives us a regression harness. In a `go test` environment stdin is not a TTY, so `interactive=true` falls back to the buffered path and behaves identically to today.

**Files:**
- Modify: `internal/runner/runner_test.go`

- [ ] **Step 1: Add the test cases at the bottom of the file**

```go
func TestRunOnceInteractive_FallsBackWhenNoTTY(t *testing.T) {
	// In a `go test` process, stdin is not a TTY, so interactive mode
	// must transparently fall back to the buffered path.
	job := &config.JobConfig{
		Name:     "interactive_fallback",
		Commands: []string{"echo hello"},
	}
	rc, output := RunOnceInteractive(job, nil, nil)
	if rc != 0 {
		t.Errorf("rc = %d, want 0", rc)
	}
	if !strings.Contains(output, "hello") {
		t.Errorf("output = %q, want to contain 'hello'", output)
	}
}

func TestRunJobInteractive_FallsBackWhenNoTTY(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	conn, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	job := &config.JobConfig{
		Name:            "interactive_job_fallback",
		Commands:        []string{"echo hello"},
		IntervalSeconds: 300,
	}
	jobs := map[string]*config.JobConfig{"interactive_job_fallback": job}
	db.EnsureJobs(conn, jobs)

	rc, _, output := RunJobInteractive(conn, job, nil, nil)
	if rc != 0 {
		t.Errorf("rc = %d, want 0", rc)
	}
	if !strings.Contains(output, "hello") {
		t.Errorf("output = %q, want to contain 'hello'", output)
	}
}
```

- [ ] **Step 2: Run the tests to confirm they fail**

Run: `go test ./internal/runner/ -run Interactive -v`

Expected: FAIL — `undefined: RunOnceInteractive`, `undefined: RunJobInteractive`.

---

## Task 3: Thread the `interactive` flag through the runner internals

This task adds the new entry points and plumbing without yet implementing the actual PTY path. With `interactive=true` we still go through the buffered code, so behavior is unchanged. It also pulls in the `mattn/go-isatty` dependency that's used by the new `stdinIsTTY` helper.

**Files:**
- Modify: `internal/runner/runner.go`
- Modify: `go.mod`, `go.sum` (via `go get`)

- [ ] **Step 0: Add the go-isatty dependency**

Run from the repo root:

```bash
go get github.com/mattn/go-isatty@latest
```

(`go mod tidy` at the end of this task will keep `go-isatty` in the direct `require` block once `runner.go` imports it.)

- [ ] **Step 1: Add the new public entry points and update internals**

Replace the entire block from `func runCommand(...)` through `func RunJob(...)` (currently lines 48–203) with:

```go
func runCommand(command string, job *config.JobConfig, timeout int, extraArgs []string, extraEnv []string, logFile *os.File, interactive bool) (int, string) {
	var cmd *exec.Cmd

	// Under TTY mode (interactive + real TTY on stdin), don't apply the
	// dispatch timeout — ad-hoc commands like `tail -f` should run until
	// the user interrupts them.
	useTTY := interactive && stdinIsTTY()

	var ctx context.Context
	var cancel context.CancelFunc
	if useTTY {
		ctx, cancel = context.WithCancel(context.Background())
	} else {
		ctx, cancel = context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	}
	defer cancel()

	if job.Shell != "" {
		fullCmd := command
		if len(extraArgs) > 0 {
			fullCmd += " " + strings.Join(extraArgs, " ")
		}
		cmd = exec.CommandContext(ctx, job.Shell, "-c", fullCmd)
	} else {
		parts := strings.Fields(command)
		if len(parts) == 0 {
			return -2, "empty command"
		}
		parts = append(parts, extraArgs...)
		cmd = exec.CommandContext(ctx, parts[0], parts[1:]...)
	}

	// Build environment: os env + job env + extra env
	env := os.Environ()
	for k, v := range job.Env {
		env = append(env, k+"="+v)
	}
	env = append(env, extraEnv...)
	cmd.Env = env

	if job.Dir != "" {
		cmd.Dir = job.Dir
	}

	if useTTY {
		// Allocate a PTY, put parent stdin into raw mode, forward
		// SIGWINCH, and stream both directions. Skip log capture
		// because the PTY stream contains ANSI escape codes.
		return runCommandTTY(cmd, timeout)
	}

	// Stream output to both a buffer and the log file
	var buf bytes.Buffer
	if logFile != nil {
		cmd.Stdout = io.MultiWriter(&buf, logFile)
		cmd.Stderr = io.MultiWriter(&buf, logFile)
	} else {
		cmd.Stdout = &buf
		cmd.Stderr = &buf
	}

	err := cmd.Run()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return -1, fmt.Sprintf("TIMEOUT after %ds", timeout)
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), buf.String()
		}
		return -2, err.Error()
	}
	return 0, buf.String()
}

func RunOnce(job *config.JobConfig, extraArgs []string, extraEnv []string) (int, string) {
	return runOnceWithLog(job, extraArgs, extraEnv, nil, false)
}

func RunOnceInteractive(job *config.JobConfig, extraArgs []string, extraEnv []string) (int, string) {
	return runOnceWithLog(job, extraArgs, extraEnv, nil, true)
}

func runOnceWithLog(job *config.JobConfig, extraArgs []string, extraEnv []string, logFile *os.File, interactive bool) (int, string) {
	if len(job.Commands) == 0 {
		return -2, "empty command"
	}
	timeout := job.Timeout
	if timeout <= 0 {
		timeout = 600
	}

	cliArgs := strings.Join(extraArgs, " ")

	var allOutput strings.Builder
	for _, command := range job.Commands {
		if strings.Contains(command, "{{.CLI_ARGS}}") {
			command = strings.ReplaceAll(command, "{{.CLI_ARGS}}", cliArgs)
			rc, output := runCommand(command, job, timeout, nil, extraEnv, logFile, interactive)
			allOutput.WriteString(output)
			if rc != 0 {
				return rc, allOutput.String()
			}
		} else {
			rc, output := runCommand(command, job, timeout, extraArgs, extraEnv, logFile, interactive)
			allOutput.WriteString(output)
			if rc != 0 {
				return rc, allOutput.String()
			}
		}
	}
	return 0, allOutput.String()
}

func RunJob(conn *sql.DB, job *config.JobConfig, extraArgs []string, extraEnv []string) (int, float64, string) {
	return runJob(conn, job, extraArgs, extraEnv, false)
}

func RunJobInteractive(conn *sql.DB, job *config.JobConfig, extraArgs []string, extraEnv []string) (int, float64, string) {
	return runJob(conn, job, extraArgs, extraEnv, true)
}

func runJob(conn *sql.DB, job *config.JobConfig, extraArgs []string, extraEnv []string, interactive bool) (int, float64, string) {
	now := db.NowUTC()
	ts := display.FormatTimestamp(now)
	header := fmt.Sprintf("[%s] START %s — %s\n", ts, job.Name, job.Description)
	fmt.Print(header)

	// Skip log capture entirely when running under a real TTY — the
	// PTY stream contains ANSI escape codes and would pollute logs.
	var logFile *os.File
	if !(interactive && stdinIsTTY()) {
		logFile = openJobLog(job.Name)
		if logFile != nil {
			defer logFile.Close()
		}
		writeLog(logFile, header)
	}

	db.MarkRunning(conn, job.Name)
	defer db.ClearRunning(conn, job.Name)

	// Handle Ctrl+C — clear running state before exit.
	// In TTY mode the child process owns the terminal and gets SIGINT
	// directly via the controlling TTY, so we still want to clean up DB
	// state if the user interrupts the parent dispatch process.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		db.ClearRunning(conn, job.Name)
		os.Exit(130)
	}()
	defer signal.Stop(sigCh)

	start := time.Now()
	rc, output := runOnceWithLog(job, extraArgs, extraEnv, logFile, interactive)

	attempt := 1
	for rc != 0 && rc != -1 && attempt <= job.Retries {
		retryMsg := fmt.Sprintf("  [RETRY %d/%d] %s failed (rc=%d), retrying in %ds...\n",
			attempt, job.Retries, job.Name, rc, job.RetryDelay)
		fmt.Print(retryMsg)
		writeLog(logFile, retryMsg)
		time.Sleep(time.Duration(job.RetryDelay) * time.Second)
		rc, output = runOnceWithLog(job, extraArgs, extraEnv, logFile, interactive)
		attempt++
	}

	if strings.TrimSpace(output) != "" {
		fmt.Println(output)
	}

	elapsed := time.Since(start).Seconds()
	status := "ok"
	if rc != 0 {
		status = fmt.Sprintf("failed:%d", rc)
	} else if attempt > 1 {
		status = fmt.Sprintf("ok:retry%d", attempt-1)
	}

	db.UpdateAfterRun(conn, job.Name, job.IntervalSeconds, rc, elapsed, status)

	icon := "OK"
	if rc != 0 {
		icon = "FAIL"
	} else if attempt > 1 {
		icon = fmt.Sprintf("OK (retry %d)", attempt-1)
	}
	footer := fmt.Sprintf("  [%s] %s (%.1fs)\n\n", icon, job.Name, elapsed)
	fmt.Print(footer)
	writeLog(logFile, footer)

	return rc, elapsed, output
}
```

- [ ] **Step 2: Add a temporary stub for `runCommandTTY` and `stdinIsTTY` so the package compiles**

Append this to the bottom of `internal/runner/runner.go`:

```go
// stdinIsTTY reports whether the parent process's stdin is connected to
// a terminal. Implemented as a var so tests can override it if needed.
var stdinIsTTY = func() bool {
	return isatty.IsTerminal(os.Stdin.Fd())
}
```

Add `"github.com/mattn/go-isatty"` to the import block.

The `runCommandTTY` function will live in `tty_unix.go` / `tty_windows.go` (added in Tasks 4 and 5). For now, add a placeholder so this task's tests pass. Append to `runner.go`:

```go
// runCommandTTY is implemented per-platform in tty_unix.go and tty_windows.go.
```

(No stub function — the per-platform files added next will provide it.)

- [ ] **Step 3: Verify the package does NOT yet compile**

Run: `go build ./internal/runner/`

Expected: FAIL with `undefined: runCommandTTY`. This is intentional — Tasks 4 and 5 add the implementations. We're committing in stages, but each commit must build, so we'll commit at the end of Task 5 (not yet).

---

## Task 4: Implement `runCommandTTY` for Unix

**Files:**
- Create: `internal/runner/tty_unix.go`
- Modify: `go.mod`, `go.sum` (via `go get`)

- [ ] **Step 0: Add the PTY dependencies**

Run from the repo root:

```bash
go get github.com/creack/pty@latest
go get golang.org/x/term@latest
```

- [ ] **Step 1: Create the file with the PTY implementation**

```go
//go:build !windows

package runner

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/creack/pty"
	"golang.org/x/term"
)

// runCommandTTY runs cmd attached to a pseudo-terminal, with the parent's
// stdin in raw mode and SIGWINCH forwarded so the child sees window resizes.
// Output is streamed live to os.Stdout — nothing is captured into a buffer
// or log file (PTY output contains ANSI escape codes that pollute logs).
//
// The timeout argument is accepted for signature symmetry with the buffered
// path but is ignored: ad-hoc TTY commands run until they exit on their own
// or the user interrupts them.
func runCommandTTY(cmd *exec.Cmd, _ int) (int, string) {
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return -2, fmt.Sprintf("pty start: %v", err)
	}
	defer func() { _ = ptmx.Close() }()

	// Forward SIGWINCH so the child sees terminal resizes.
	winchCh := make(chan os.Signal, 1)
	signal.Notify(winchCh, syscall.SIGWINCH)
	go func() {
		for range winchCh {
			_ = pty.InheritSize(os.Stdin, ptmx)
		}
	}()
	winchCh <- syscall.SIGWINCH // trigger initial size sync
	defer signal.Stop(winchCh)

	// Put parent stdin into raw mode so keystrokes (including Ctrl+C)
	// pass through to the child unmodified.
	stdinFd := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(stdinFd)
	if err != nil {
		return -2, fmt.Sprintf("raw mode: %v", err)
	}
	defer func() { _ = term.Restore(stdinFd, oldState) }()

	// Bidirectional copy: parent stdin → PTY, PTY → parent stdout.
	// stdin→pty in a goroutine; pty→stdout blocks here until the
	// child closes the PTY.
	go func() { _, _ = io.Copy(ptmx, os.Stdin) }()
	_, _ = io.Copy(os.Stdout, ptmx)

	err = cmd.Wait()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), ""
		}
		return -2, err.Error()
	}
	return 0, ""
}
```

- [ ] **Step 2: Verify the package compiles on Linux**

Run: `go build ./internal/runner/`

Expected: still FAIL with `undefined: runCommandTTY` when targeting Windows, but on the current platform (Linux per the env) it should now compile. Run again to confirm:

Run: `GOOS=linux go build ./internal/runner/` → Expected: PASS.

Run: `GOOS=windows go build ./internal/runner/` → Expected: FAIL — Task 5 adds the Windows stub.

---

## Task 5: Add the Windows stub and verify build

**Files:**
- Create: `internal/runner/tty_windows.go`

- [ ] **Step 1: Create the Windows stub**

```go
//go:build windows

package runner

import (
	"fmt"
	"os/exec"
)

// runCommandTTY is not implemented on Windows. The runtime gate
// `stdinIsTTY` will normally prevent us from getting here on Windows
// (Windows consoles are detected differently and creack/pty has limited
// Windows support), but if we do, return a clear error.
func runCommandTTY(_ *exec.Cmd, _ int) (int, string) {
	return -2, fmt.Sprintf("interactive TTY mode is not supported on Windows")
}
```

- [ ] **Step 2: Verify both platforms build**

Run: `GOOS=linux go build ./internal/runner/ && GOOS=windows go build ./internal/runner/`

Expected: both succeed.

- [ ] **Step 3: Run the full test suite**

Run: `go test ./...`

Expected: all tests pass, including the two new `*_FallsBackWhenNoTTY` tests from Task 2 (because in `go test`, `os.Stdin` is not a terminal, so the runtime gate diverts interactive calls to the buffered path).

- [ ] **Step 4: Commit Tasks 2–5 together**

These tasks all depend on each other (the package only compiles after the stub exists). Commit them as one unit:

```bash
git add internal/runner/runner.go internal/runner/tty_unix.go internal/runner/tty_windows.go internal/runner/runner_test.go
git commit -m "feat(runner): add interactive TTY mode for ad-hoc runs"
```

---

## Task 6: Wire the CLI to the new interactive entry points

**Files:**
- Modify: `main.go:438` (run-once)
- Modify: `main.go:580` (run, adhoc branch)
- Modify: `main.go:605` (run, non-adhoc branch)
- Modify: `main.go:619` (run-all)

The dispatch loop call site at `main.go:695` is intentionally NOT changed — the cron-triggered dispatcher must stay buffered/logged.

- [ ] **Step 1: Switch `run-once` to the interactive variant**

Find this line in `main.go` (around line 438):

```go
		rc, output := runner.RunOnce(job, extraArgs, extraEnv)
```

Replace with:

```go
		rc, output := runner.RunOnceInteractive(job, extraArgs, extraEnv)
```

- [ ] **Step 2: Switch the adhoc `run` branch**

Find this line in `main.go` (around line 580, inside `if job.Adhoc { ... }`):

```go
			rc, _, _ := runner.RunJob(conn, job, extraArgs, extraEnv)
```

Replace with:

```go
			rc, _, _ := runner.RunJobInteractive(conn, job, extraArgs, extraEnv)
```

- [ ] **Step 3: Switch the non-adhoc `run` branch**

Find this line in `main.go` (around line 605, inside `case "run":`):

```go
		rc, elapsed, output := runner.RunJob(conn, job, extraArgs, extraEnv)
```

Replace with:

```go
		rc, elapsed, output := runner.RunJobInteractive(conn, job, extraArgs, extraEnv)
```

Note: under interactive+TTY, `output` will be empty (PTY output streams direct to terminal, not captured), so the notify body for this run will be empty. That's the documented tradeoff — the user is watching live and doesn't need a notification echo.

- [ ] **Step 4: Switch the `run-all` branch**

Find this block in `main.go` (around line 619):

```go
			rc, elapsed, output := runner.RunJob(conn, job, nil, jobEnv)
```

Replace with:

```go
			rc, elapsed, output := runner.RunJobInteractive(conn, job, nil, jobEnv)
```

- [ ] **Step 5: Confirm the dispatch loop call site is UNCHANGED**

Find `main.go:695` (inside `func dispatch`). It should still read:

```go
		rc, elapsed, output := runner.RunJob(conn, job, nil, jobEnv)
```

Do NOT modify this line. This is the cron path — it must stay buffered and logged.

- [ ] **Step 6: Build and run all tests**

Run: `go build -o /tmp/dispatch . && go test ./...`

Expected: build succeeds, all tests pass.

- [ ] **Step 7: Commit**

```bash
git add main.go
git commit -m "feat(cli): use interactive runner for run/run-once/run-all"
```

---

## Task 7: Manual verification

Automated tests can't exercise the PTY path because `go test` runs without a TTY on stdin. This task is a manual smoke test against a real terminal. The plan reviewer / executor must run these by hand and record results.

**Setup:** From the dispatcher project root, build a fresh binary and ensure there's a sample config:

```bash
go build -o /tmp/dispatch .
cat > /tmp/test-tty.yaml <<'EOF'
timezone: UTC
jobs:
  colors:
    command: ls --color=always /tmp
    interval: 1d
    adhoc: true
    description: Should show ANSI colors when run from a TTY
  follow:
    command: tail -f /tmp/test-tty.yaml
    interval: 1d
    adhoc: true
    description: Should stream live and exit on Ctrl+C
  hello:
    command: echo hello
    interval: 1d
    adhoc: true
    description: Sanity check
EOF
```

- [ ] **Step 1: Sanity — non-TTY-sensitive command via `run-once`**

```bash
cd /tmp && /tmp/dispatch --config test-tty.yaml run-once hello
```

Expected: `hello` printed, exit 0.

- [ ] **Step 2: Colors should appear under TTY**

```bash
cd /tmp && /tmp/dispatch --config test-tty.yaml run-once colors
```

Expected: directory listing with ANSI color codes rendered as colors (not raw `\033[` sequences). If you see raw escape sequences, the PTY isn't being allocated — debug `stdinIsTTY()`.

- [ ] **Step 3: Confirm fallback when stdin is piped**

```bash
cd /tmp && echo "" | /tmp/dispatch --config test-tty.yaml run-once colors
```

Expected: directory listing without colors (because `--color=always` would still emit them, but the PTY path didn't engage; this shows the fallback works — output is captured and printed normally rather than streamed).

- [ ] **Step 4: `tail -f` streams live and exits cleanly**

```bash
cd /tmp && /tmp/dispatch --config test-tty.yaml run-once follow
```

Expected: `tail -f` runs, no output until you append to the file in another shell. Hit Ctrl+C — it should exit promptly with rc 130 (or similar) and the parent dispatch process should exit. Crucially, the terminal should be in a sane state afterward (try typing — if your keystrokes don't echo, the raw-mode restore failed).

- [ ] **Step 5: `dispatch run` (non-adhoc) under TTY**

Use any non-adhoc job from your real `Dispatcher.yaml`:

```bash
cd /path/to/your/dispatcher/project && ./dispatch run <some-non-adhoc-job>
```

Expected: live streaming output, exit code propagates. Notify body for the Discord/ntfy hook will be empty — that's the documented tradeoff.

- [ ] **Step 6: Confirm the dispatch loop is unchanged**

```bash
cd /path/to/your/dispatcher/project && ./dispatch
```

(Or wait for cron to run it.) Expected: identical behavior to before this change — buffered output, log files in `.dispatcher/logs/<job>.log` are written, no PTY allocated even though you invoked it from a terminal (because `dispatch` with no args doesn't go through the interactive entry point).

- [ ] **Step 7: Confirm logs ARE written for the dispatch path and NOT for interactive runs**

```bash
ls -la .dispatcher/logs/
cat .dispatcher/logs/<some-job>.log | tail -20
```

Expected: log file for jobs invoked via the dispatch loop has the usual `[timestamp] START ...` and command output. If you ran the same job via `dispatch run`, it should NOT have appended to the log (that run was interactive).

---

## Self-Review

**Spec coverage:**
- Auto-detect TTY via `isatty(stdin)` — ✅ Task 3 (`stdinIsTTY` var) + runtime gate in `runCommand`
- Call-site gate so dispatch loop never gets TTY — ✅ Task 6 (loop call site unchanged)
- Real PTY allocation — ✅ Task 4 (`pty.Start`)
- Raw-mode parent stdin with restore — ✅ Task 4 (`term.MakeRaw` + deferred `term.Restore`)
- SIGWINCH forwarding — ✅ Task 4
- Skip log capture under TTY — ✅ Task 3 (`runJob` skips `openJobLog` when `interactive && stdinIsTTY()`)
- Disable timeout under TTY — ✅ Task 3 (`runCommand` uses `WithCancel` instead of `WithTimeout` when `useTTY`)
- Windows fallback — ✅ Task 5
- Existing `RunOnce` / `RunJob` signatures preserved (no churn) — ✅ Task 3

**Placeholder scan:** No "TBD"/"add validation"/"implement later". Every step has exact code.

**Type consistency:** `runCommandTTY(cmd *exec.Cmd, _ int) (int, string)` declared in `tty_unix.go` and `tty_windows.go`, called from `runCommand` in `runner.go` — signatures match. `stdinIsTTY` is a `var` of type `func() bool`, called the same way in `runCommand` and `runJob`. `RunOnceInteractive` and `RunJobInteractive` referenced in `main.go` (Task 6) match the signatures defined in Task 3.

**Deferred items (out of scope for this plan):**
- `tty: false` YAML opt-out for jobs that misbehave under PTY. Not adding now — wait for a real case to design against.
- Capturing PTY output for the notify body on `dispatch run`. Acceptable tradeoff; revisit if users complain.
