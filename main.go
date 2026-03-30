package main

import (
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/blindly/dispatcher/internal/config"
	"github.com/blindly/dispatcher/internal/db"
	"github.com/blindly/dispatcher/internal/display"
	"github.com/blindly/dispatcher/internal/notify"
	"github.com/blindly/dispatcher/internal/runner"
)

const usage = `Usage: dispatch [command] [options]

Commands:
  (default)    Run due jobs
  list         Show job status table
  run          Force-run a specific job
  run-once     Run a job without DB tracking
  run-all      Force-run all jobs
  reset        Reset a job's next_run to now
  install      Install crontab entry
  uninstall    Remove crontab entry

Options:
  --config     Config file path (default: dispatcher.yaml)
`

func main() {
	args := os.Args[1:]
	configPath := "dispatcher.yaml"

	// Extract --config flag from anywhere in args
	filtered := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if args[i] == "--config" && i+1 < len(args) {
			configPath = args[i+1]
			i++
			continue
		}
		filtered = append(filtered, args[i])
	}
	args = filtered

	cmd := ""
	if len(args) > 0 {
		cmd = args[0]
		args = args[1:]
	}

	if cmd == "-h" || cmd == "--help" || cmd == "help" {
		fmt.Print(usage)
		os.Exit(0)
	}

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Config not found: %s\n", configPath)
		os.Exit(1)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	configDir := filepath.Dir(configPath)
	if configDir == "" || configDir == "." {
		configDir, _ = os.Getwd()
	} else {
		configDir, _ = filepath.Abs(configDir)
	}

	// install/uninstall don't need DB
	if cmd == "install" {
		schedule := "*/5 * * * *"
		if len(args) > 0 {
			schedule = args[0]
		}
		installCron(schedule, configDir)
		return
	}
	if cmd == "uninstall" {
		uninstallCron(configDir)
		return
	}

	// run-once doesn't need DB or lock
	if cmd == "run-once" {
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: dispatch run-once <job>")
			os.Exit(1)
		}
		job, ok := cfg.Jobs[args[0]]
		if !ok {
			fmt.Fprintf(os.Stderr, "Unknown job: %s\n", args[0])
			os.Exit(1)
		}
		rc, output := runner.RunOnce(job)
		if strings.TrimSpace(output) != "" {
			fmt.Print(output)
		}
		if rc != 0 {
			os.Exit(1)
		}
		return
	}

	dbPath := filepath.Join(configDir, "data.db")
	conn, err := db.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening database: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	db.EnsureJobs(conn, cfg.Jobs)

	// Read-only: no lock needed
	if cmd == "list" {
		display.PrintStatus(conn, cfg.Jobs, cfg.Timezone)
		return
	}

	if cmd == "reset" {
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: dispatch reset <job>")
			os.Exit(1)
		}
		if _, ok := cfg.Jobs[args[0]]; !ok {
			fmt.Fprintf(os.Stderr, "Unknown job: %s\n", args[0])
			os.Exit(1)
		}
		conn.Exec("UPDATE cron_jobs SET next_run_at = ? WHERE name = ?",
			db.NowUTC().Format(time.RFC3339), args[0])
		fmt.Printf("Reset %s — will run on next dispatch\n", args[0])
		return
	}

	// Execution commands — acquire lock
	lockFd := acquireLock(configDir)
	if lockFd == -1 {
		fmt.Println("Another dispatcher is already running — skipping")
		return
	}
	defer releaseLock(lockFd, configDir)

	switch cmd {
	case "run":
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: dispatch run <job>")
			os.Exit(1)
		}
		job, ok := cfg.Jobs[args[0]]
		if !ok {
			fmt.Fprintf(os.Stderr, "Unknown job: %s\n", args[0])
			os.Exit(1)
		}
		rc, elapsed, output := runner.RunJob(conn, job)
		results := []notify.JobResult{{Name: args[0], ExitCode: rc, Elapsed: elapsed, Output: output}}
		notify.SendDiscordSummary(results, cfg.Notify.Discord.Webhook)

	case "run-all":
		var results []notify.JobResult
		for name, job := range cfg.Jobs {
			rc, elapsed, output := runner.RunJob(conn, job)
			results = append(results, notify.JobResult{Name: name, ExitCode: rc, Elapsed: elapsed, Output: output})
		}
		notify.SendDiscordSummary(results, cfg.Notify.Discord.Webhook)

	case "":
		dispatch(conn, cfg)

	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}
}

func dispatch(conn *sql.DB, cfg *config.DispatcherConfig) {
	due := db.GetDueJobs(conn, cfg.Jobs, cfg.Timezone)
	if len(due) == 0 {
		return
	}

	ts := db.NowUTC().Format("2006-01-02 15:04:05 UTC")
	sep := strings.Repeat("=", 70)
	fmt.Printf("%s\n  Dispatcher — %s\n  Due: %s\n%s\n\n", sep, ts, strings.Join(due, ", "), sep)

	ordered := runner.ResolveOrder(due, cfg.Jobs)

	var results []notify.JobResult
	for _, name := range ordered {
		job := cfg.Jobs[name]
		if job.DependsOn != "" {
			for _, r := range results {
				if r.Name == job.DependsOn && r.ExitCode != 0 {
					fmt.Printf("  SKIP %s — dependency %s failed\n\n", name, job.DependsOn)
					continue
				}
			}
		}
		rc, elapsed, output := runner.RunJob(conn, job)
		results = append(results, notify.JobResult{Name: name, ExitCode: rc, Elapsed: elapsed, Output: output})
	}

	totalTime := 0.0
	passed, failed := 0, 0
	for _, r := range results {
		totalTime += r.Elapsed
		if r.ExitCode == 0 {
			passed++
		} else {
			failed++
		}
	}
	fmt.Printf("%s\n  Done: %d ok, %d failed, %.1fs total\n%s\n", sep, passed, failed, totalTime, sep)

	notify.SendDiscordSummary(results, cfg.Notify.Discord.Webhook)
}

func acquireLock(dir string) int {
	lockPath := filepath.Join(dir, ".dispatch.lock")
	fd, err := syscall.Open(lockPath, syscall.O_CREAT|syscall.O_WRONLY, 0644)
	if err != nil {
		return -1
	}
	err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		syscall.Close(fd)
		return -1
	}
	return fd
}

func releaseLock(fd int, dir string) {
	syscall.Flock(fd, syscall.LOCK_UN)
	syscall.Close(fd)
	os.Remove(filepath.Join(dir, ".dispatch.lock"))
}

func installCron(schedule string, projectDir string) {
	dispatchPath, err := os.Executable()
	if err != nil {
		dispatchPath = "dispatch"
	}
	cronLine := fmt.Sprintf("%s cd %s && %s >> logs/dispatcher.log 2>&1", schedule, projectDir, dispatchPath)

	out, err := exec.Command("crontab", "-l").Output()
	existing := ""
	if err == nil {
		existing = string(out)
	}

	if strings.Contains(existing, projectDir) {
		fmt.Printf("Cron already installed for %s\n", projectDir)
		for _, line := range strings.Split(existing, "\n") {
			if strings.Contains(line, projectDir) {
				fmt.Printf("  %s\n", line)
			}
		}
		return
	}

	newCrontab := strings.TrimRight(existing, "\n") + "\n" + cronLine + "\n"
	cmd := exec.Command("crontab", "-")
	cmd.Stdin = strings.NewReader(newCrontab)
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to install crontab: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Cron installed: %s\n", cronLine)
}

func uninstallCron(projectDir string) {
	out, err := exec.Command("crontab", "-l").Output()
	if err != nil {
		fmt.Println("No crontab found")
		return
	}

	lines := strings.Split(string(out), "\n")
	var filtered []string
	for _, line := range lines {
		if !strings.Contains(line, projectDir) {
			filtered = append(filtered, line)
		}
	}

	if len(filtered) == len(lines) {
		fmt.Printf("No cron entry found for %s\n", projectDir)
		return
	}

	newCrontab := strings.Join(filtered, "\n")
	cmd := exec.Command("crontab", "-")
	cmd.Stdin = strings.NewReader(newCrontab)
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to update crontab: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Cron removed for %s\n", projectDir)
}
