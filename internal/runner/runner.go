package runner

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/blindly/dispatcher/internal/config"
	"github.com/blindly/dispatcher/internal/db"
	"github.com/blindly/dispatcher/internal/display"
	"github.com/mattn/go-isatty"
)

var logBaseDir string

func SetLogDir(dir string) {
	logBaseDir = dir
}

func openJobLog(name string) *os.File {
	logDir := "logs"
	if logBaseDir != "" {
		logDir = filepath.Join(logBaseDir, "logs")
	}
	os.MkdirAll(logDir, 0755)
	logPath := filepath.Join(logDir, name+".log")
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil
	}
	return f
}

func writeLog(f *os.File, content string) {
	if f != nil {
		f.WriteString(content)
	}
}

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
			if rc := signalRC(exitErr); rc != 0 {
				return rc, buf.String()
			}
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
	for rc != 0 && rc != -1 && !isInterrupted(rc) && attempt <= job.Retries {
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
	if isInterrupted(rc) {
		status = "interrupted"
	} else if rc != 0 {
		status = fmt.Sprintf("failed:%d", rc)
	} else if attempt > 1 {
		status = fmt.Sprintf("ok:retry%d", attempt-1)
	}

	db.UpdateAfterRun(conn, job.Name, job.IntervalSeconds, rc, elapsed, status, job.AtMinutes)

	icon := "OK"
	if isInterrupted(rc) {
		icon = "INTERRUPTED"
	} else if rc != 0 {
		icon = "FAIL"
	} else if attempt > 1 {
		icon = fmt.Sprintf("OK (retry %d)", attempt-1)
	}
	footer := fmt.Sprintf("  [%s] %s (%.1fs)\n\n", icon, job.Name, elapsed)
	fmt.Print(footer)
	writeLog(logFile, footer)

	return rc, elapsed, output
}

func ResolveOrder(due []string, jobs map[string]*config.JobConfig) []string {
	ordered := make([]string, 0, len(due))
	added := make(map[string]bool)

	dueSet := make(map[string]bool)
	for _, name := range due {
		dueSet[name] = true
	}

	for _, name := range due {
		job, ok := jobs[name]
		if !ok {
			continue
		}
		if job.DependsOn != "" && dueSet[job.DependsOn] && !added[job.DependsOn] {
			ordered = append(ordered, job.DependsOn)
			added[job.DependsOn] = true
		}
		if !added[name] {
			ordered = append(ordered, name)
			added[name] = true
		}
	}
	return ordered
}

// stdinIsTTY reports whether the parent process's stdin is connected to
// a terminal. Implemented as a var so tests can override it if needed.
var stdinIsTTY = func() bool {
	return isatty.IsTerminal(os.Stdin.Fd())
}

// isInterrupted reports whether rc indicates the child was killed by
// a user-meaningful signal (SIGHUP, SIGINT, SIGTERM) — bash convention
// of 128+sig. Used to classify TTY Ctrl+C as "interrupted" rather than
// "failed" and to suppress the retry loop on those exits.
func isInterrupted(rc int) bool {
	return rc == 129 || rc == 130 || rc == 143
}

// runCommandTTY is implemented per-platform in tty_unix.go and tty_windows.go.
