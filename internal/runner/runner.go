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

func runCommand(command string, timeout int) (int, string) {
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return -2, "empty command"
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, parts[0], parts[1:]...)
	cmd.Env = os.Environ()

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

func RunOnce(job *config.JobConfig) (int, string) {
	if len(job.Commands) == 0 {
		return -2, "empty command"
	}
	timeout := job.Timeout
	if timeout <= 0 {
		timeout = 600
	}

	var allOutput strings.Builder
	for _, command := range job.Commands {
		rc, output := runCommand(command, timeout)
		allOutput.WriteString(output)
		if rc != 0 {
			return rc, allOutput.String()
		}
	}
	return 0, allOutput.String()
}

func RunJob(conn *sql.DB, job *config.JobConfig) (int, float64, string) {
	now := db.NowUTC()
	ts := now.Format("2006-01-02 15:04:05 UTC")
	header := fmt.Sprintf("[%s] START %s — %s\n", ts, job.Name, job.Description)
	fmt.Print(header)

	start := time.Now()
	rc, output := RunOnce(job)

	attempt := 1
	for rc != 0 && rc != -1 && attempt <= job.Retries {
		retryMsg := fmt.Sprintf("  [RETRY %d/%d] %s failed (rc=%d), retrying in %ds...\n",
			attempt, job.Retries, job.Name, rc, job.RetryDelay)
		fmt.Print(retryMsg)
		time.Sleep(time.Duration(job.RetryDelay) * time.Second)
		rc, output = RunOnce(job)
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
