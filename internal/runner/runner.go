package runner

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/blindly/dispatcher/internal/config"
	"github.com/blindly/dispatcher/internal/db"
	"github.com/blindly/dispatcher/internal/display"
)

func writeJobLog(name string, content string) {
	logDir := "logs"
	os.MkdirAll(logDir, 0755)
	logPath := filepath.Join(logDir, name+".log")
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	f.WriteString(content)
}

func runCommand(command string, job *config.JobConfig, timeout int, extraArgs []string, extraEnv []string) (int, string) {
	var cmd *exec.Cmd

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()

	if job.Shell != "" {
		// Use specified shell
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

	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return -1, fmt.Sprintf("TIMEOUT after %ds", timeout)
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), string(out)
		}
		return -2, err.Error()
	}
	return 0, string(out)
}

func RunOnce(job *config.JobConfig, extraArgs []string, extraEnv []string) (int, string) {
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
		// If command uses {{.CLI_ARGS}}, substitute inline; otherwise append extraArgs
		if strings.Contains(command, "{{.CLI_ARGS}}") {
			command = strings.ReplaceAll(command, "{{.CLI_ARGS}}", cliArgs)
			rc, output := runCommand(command, job, timeout, nil, extraEnv)
			allOutput.WriteString(output)
			if rc != 0 {
				return rc, allOutput.String()
			}
		} else {
			rc, output := runCommand(command, job, timeout, extraArgs, extraEnv)
			allOutput.WriteString(output)
			if rc != 0 {
				return rc, allOutput.String()
			}
		}
	}
	return 0, allOutput.String()
}

func RunJob(conn *sql.DB, job *config.JobConfig, extraArgs []string, extraEnv []string) (int, float64, string) {
	now := db.NowUTC()
	ts := display.FormatTimestamp(now)
	header := fmt.Sprintf("[%s] START %s — %s\n", ts, job.Name, job.Description)
	fmt.Print(header)

	db.MarkRunning(conn, job.Name)
	defer db.ClearRunning(conn, job.Name)

	start := time.Now()
	rc, output := RunOnce(job, extraArgs, extraEnv)

	attempt := 1
	for rc != 0 && rc != -1 && attempt <= job.Retries {
		retryMsg := fmt.Sprintf("  [RETRY %d/%d] %s failed (rc=%d), retrying in %ds...\n",
			attempt, job.Retries, job.Name, rc, job.RetryDelay)
		fmt.Print(retryMsg)
		time.Sleep(time.Duration(job.RetryDelay) * time.Second)
		rc, output = RunOnce(job, extraArgs, extraEnv)
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
	footer := fmt.Sprintf("  [%s] %s (%.1fs)\n", icon, job.Name, elapsed)
	fmt.Print(footer)

	writeJobLog(job.Name, header+output+footer+"\n")

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
