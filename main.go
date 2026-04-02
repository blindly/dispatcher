package main

import (
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/blindly/dispatcher/internal/config"
	"github.com/blindly/dispatcher/internal/db"
	"github.com/blindly/dispatcher/internal/display"
	"github.com/blindly/dispatcher/internal/notify"
	"github.com/blindly/dispatcher/internal/runner"
	"github.com/blindly/dispatcher/internal/updater"
)

var version = "dev"

const usage = `Usage: dispatch [command] [options]

Commands:
  (default)    Run due jobs
  init         Create a default dispatcher.yaml
  list         Show job status table
  status       Quick summary (last run, due count)
  analytics    Job success rates and run history
  history      Show last 20 runs for a job
  run          Force-run a specific job
  run-once     Run a job without DB tracking
  run-all      Force-run all jobs
  reset        Reset a job's next_run to now
  retry-failed Reset all failed jobs to run next cycle
  purge        Delete old run history (default: retention config)
  validate     Check config syntax
  logs         Show recent log output for a job
  watch        Live tail of job logs (all or specific job)
  install      Install crontab entry
  uninstall    Remove crontab entry
  update       Self-update to latest release
  version      Show current version
  docs         Show full documentation

Options:
  --config     Config file path (default: dispatcher.yaml)
`

func detectConfig() string {
	candidates := []string{
		"dispatcher.yaml",
		"dispatcher.yml",
		"Dispatcher.yaml",
		"Dispatcher.yml",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return "dispatcher.yaml" // default if none found
}

func initConfig() {
	// Check if any config already exists
	candidates := []string{
		"dispatcher.yaml",
		"dispatcher.yml",
		"Dispatcher.yaml",
		"Dispatcher.yml",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			fmt.Fprintf(os.Stderr, "Config already exists: %s\n", c)
			os.Exit(1)
		}
	}

	defaultConfig := `timezone: America/New_York
# schedule: "*/5 * * * *"

# notify:
#   discord:
#     webhook: ${DISCORD_WEBHOOK_URL}

jobs:
  hello:
    command: echo "Hello from dispatcher"
    interval: 5m
    description: Example job
    # adhoc: true
    # timeout: 5m
    # active_hours: [9, 17]
    # depends_on: other_job
    # retries: 2
    # retry_delay: 5s
`
	if err := os.WriteFile("dispatcher.yaml", []byte(defaultConfig), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create config: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Created dispatcher.yaml")
}

func main() {
	args := os.Args[1:]
	configPath := ""

	// Extract --config flag from anywhere in args
	configExplicit := false
	filtered := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if args[i] == "--config" && i+1 < len(args) {
			configPath = args[i+1]
			configExplicit = true
			i++
			continue
		}
		filtered = append(filtered, args[i])
	}
	if !configExplicit {
		configPath = detectConfig()
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

	if cmd == "version" {
		fmt.Printf("dispatch %s\n", version)
		os.Exit(0)
	}

	if cmd == "docs" {
		showDocs()
		return
	}

	if cmd == "update" {
		targetVersion := ""
		if len(args) > 0 {
			targetVersion = args[0]
		}
		if err := selfUpdate(targetVersion); err != nil {
			fmt.Fprintf(os.Stderr, "Update failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if cmd == "init" {
		initConfig()
		return
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

	if cmd == "validate" {
		fmt.Printf("Config OK: %s (%d jobs)\n", configPath, len(cfg.Jobs))
		for name, job := range cfg.Jobs {
			issues := validateJob(name, job, cfg.Jobs)
			for _, issue := range issues {
				fmt.Printf("  WARNING: %s\n", issue)
			}
		}
		return
	}

	if cmd == "logs" {
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: dispatch logs <job>")
			os.Exit(1)
		}
		jobName := args[0]
		if _, ok := cfg.Jobs[jobName]; !ok {
			fmt.Fprintf(os.Stderr, "Unknown job: %s\n", jobName)
			os.Exit(1)
		}
		logPath := filepath.Join(configDir, "logs", jobName+".log")
		content, err := os.ReadFile(logPath)
		if err != nil {
			if os.IsNotExist(err) {
				fmt.Printf("No logs found for %s\n", jobName)
				return
			}
			fmt.Fprintf(os.Stderr, "Error reading log: %v\n", err)
			os.Exit(1)
		}
		// Show last 50 lines
		lines := strings.Split(string(content), "\n")
		start := 0
		if len(lines) > 50 {
			start = len(lines) - 50
		}
		fmt.Print(strings.Join(lines[start:], "\n"))
		return
	}

	if cmd == "watch" {
		jobName := ""
		if len(args) > 0 {
			jobName = args[0]
			if _, ok := cfg.Jobs[jobName]; !ok {
				fmt.Fprintf(os.Stderr, "Unknown job: %s\n", jobName)
				os.Exit(1)
			}
		}
		watchLogs(configDir, jobName, cfg.Jobs)
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
		extraEnv, extraArgs := parseJobArgs(args[1:])
		rc, output := runner.RunOnce(job, extraArgs, extraEnv)
		if strings.TrimSpace(output) != "" {
			fmt.Print(output)
		}
		if rc != 0 {
			os.Exit(1)
		}
		return
	}

	dbPath := filepath.Join(configDir, "data.db")
	if cfg.DbPath != "" {
		dbPath = cfg.DbPath
		if !filepath.IsAbs(dbPath) {
			dbPath = filepath.Join(configDir, dbPath)
		}
	}
	conn, err := db.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening database: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	db.EnsureJobs(conn, cfg.Jobs)
	display.SetTimezone(cfg.Timezone)
	runner.SetLogDir(configDir)

	notifyCfg := notify.NotifyConfig{
		DiscordWebhook: cfg.Notify.Discord.Webhook,
		NtfyURL:        cfg.Notify.Ntfy.URL,
		NtfyTopic:      cfg.Notify.Ntfy.Topic,
		NtfyPriority:   cfg.Notify.Ntfy.Priority,
	}

	// Read-only: no lock needed
	if cmd == "list" {
		display.PrintStatus(conn, cfg.Jobs, cfg.Timezone)
		return
	}

	if cmd == "analytics" {
		display.PrintAnalytics(conn)
		return
	}

	if cmd == "history" {
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: dispatch history <job>")
			os.Exit(1)
		}
		if _, ok := cfg.Jobs[args[0]]; !ok {
			fmt.Fprintf(os.Stderr, "Unknown job: %s\n", args[0])
			os.Exit(1)
		}
		display.PrintHistory(conn, args[0], 20)
		return
	}

	if cmd == "status" {
		display.PrintQuickStatus(conn, cfg.Jobs, cfg.Timezone, configDir)
		return
	}

	if cmd == "retry-failed" {
		now := db.NowUTC().Format(time.RFC3339)
		rows, err := conn.Query("SELECT name FROM cron_jobs WHERE last_status LIKE 'failed%'")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		var reset []string
		for rows.Next() {
			var name string
			rows.Scan(&name)
			if _, ok := cfg.Jobs[name]; ok {
				reset = append(reset, name)
			}
		}
		rows.Close()
		if len(reset) == 0 {
			fmt.Println("No failed jobs to retry")
			return
		}
		for _, name := range reset {
			conn.Exec("UPDATE cron_jobs SET next_run_at = ? WHERE name = ?", now, name)
		}
		fmt.Printf("Reset %d failed jobs: %s\n", len(reset), strings.Join(reset, ", "))
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

	// dispatch run for adhoc jobs — no lock needed
	if cmd == "run" {
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: dispatch run <job> [KEY=VALUE...] [-- args...]")
			os.Exit(1)
		}
		job, ok := cfg.Jobs[args[0]]
		if !ok {
			fmt.Fprintf(os.Stderr, "Unknown job: %s\n", args[0])
			os.Exit(1)
		}
		if job.Adhoc {
			extraEnv, extraArgs := parseJobArgs(args[1:])
			rc, _, _ := runner.RunJob(conn, job, extraArgs, extraEnv)
			if rc != 0 {
				os.Exit(1)
			}
			return
		}
	}

	// Execution commands — acquire lock
	lockFd := acquireLock(configDir)
	if lockFd == -1 {
		fmt.Println("Another dispatcher is already running — skipping")
		return
	}
	defer releaseLock(lockFd, configDir)

	// We hold the lock — any non-adhoc running_since is stale from a crashed run
	db.ClearStaleRunning(conn, cfg.Jobs)

	switch cmd {
	case "run":
		// Non-adhoc jobs (adhoc handled above)
		job := cfg.Jobs[args[0]]
		extraEnv, extraArgs := parseJobArgs(args[1:])
		rc, elapsed, output := runner.RunJob(conn, job, extraArgs, extraEnv)
		results := []notify.JobResult{{Name: args[0], ExitCode: rc, Elapsed: elapsed, Output: output}}
		notify.SendAll(results, notifyCfg)
		if rc != 0 {
			os.Exit(1)
		}

	case "run-all":
		var results []notify.JobResult
		for name, job := range cfg.Jobs {
			if job.Adhoc {
				continue
			}
			rc, elapsed, output := runner.RunJob(conn, job, nil, nil)
			results = append(results, notify.JobResult{Name: name, ExitCode: rc, Elapsed: elapsed, Output: output})
		}
		notify.SendAll(results, notifyCfg)
		for _, r := range results {
			if r.ExitCode != 0 {
				os.Exit(1)
			}
		}

	case "purge":
		days := cfg.Retention
		if len(args) > 0 {
			purgeInterval, err := config.ParseInterval(args[0])
			if err != nil {
				fmt.Fprintf(os.Stderr, "Invalid duration: %v\n", err)
				os.Exit(1)
			}
			days = purgeInterval / 86400
			if days < 1 {
				days = 1
			}
		}
		deleted, err := db.PurgeHistory(conn, days)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Purge failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Purged %d history entries older than %dd\n", deleted, days)

	case "":
		dispatch(conn, cfg, notifyCfg)

	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}
}

func dispatch(conn *sql.DB, cfg *config.DispatcherConfig, notifyCfg notify.NotifyConfig) {
	due := db.GetDueJobs(conn, cfg.Jobs, cfg.Timezone)
	if len(due) == 0 {
		fmt.Printf("No jobs due (%d jobs configured)\n", len(cfg.Jobs))
		return
	}

	ts := display.FormatTimestamp(db.NowUTC())
	sep := strings.Repeat("=", 70)
	fmt.Printf("%s\n  Dispatcher — %s\n  Due: %s\n%s\n\n", sep, ts, strings.Join(due, ", "), sep)

	ordered := runner.ResolveOrder(due, cfg.Jobs)

	var results []notify.JobResult
	for _, name := range ordered {
		job := cfg.Jobs[name]
		if job.DependsOn != "" {
			skip := false
			for _, r := range results {
				if r.Name == job.DependsOn && r.ExitCode != 0 {
					fmt.Printf("  SKIP %s — dependency %s failed\n\n", name, job.DependsOn)
					skip = true
					break
				}
			}
			if skip {
				continue
			}
		}
		rc, elapsed, output := runner.RunJob(conn, job, nil, nil)
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

	notify.SendAll(results, notifyCfg)

	// Auto-purge old history
	db.PurgeHistory(conn, cfg.Retention)
}

func showDocs() {
	url := "https://raw.githubusercontent.com/blindly/dispatcher/main/README.md"
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to fetch docs: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		fmt.Fprintf(os.Stderr, "Failed to fetch docs: HTTP %d\n", resp.StatusCode)
		os.Exit(1)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to read docs: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(string(body))
}

func watchLogs(configDir string, jobName string, jobs map[string]*config.JobConfig) {
	logDir := filepath.Join(configDir, "logs")

	// Determine which log files to watch
	var logFiles []string
	if jobName != "" {
		logFiles = []string{filepath.Join(logDir, jobName+".log")}
	} else {
		for name := range jobs {
			logFiles = append(logFiles, filepath.Join(logDir, name+".log"))
		}
	}

	// Track file sizes
	offsets := make(map[string]int64)
	for _, f := range logFiles {
		info, err := os.Stat(f)
		if err == nil {
			// Start from end of file
			offsets[f] = info.Size()
		}
	}

	if jobName != "" {
		fmt.Printf("Watching %s (ctrl+c to stop)\n\n", jobName)
	} else {
		fmt.Printf("Watching %d jobs (ctrl+c to stop)\n\n", len(jobs))
	}

	for {
		for _, f := range logFiles {
			info, err := os.Stat(f)
			if err != nil {
				continue
			}

			offset, seen := offsets[f]
			if !seen {
				// New file appeared
				offset = 0
				offsets[f] = 0
			}

			if info.Size() <= offset {
				continue
			}

			file, err := os.Open(f)
			if err != nil {
				continue
			}
			file.Seek(offset, 0)
			buf := make([]byte, info.Size()-offset)
			n, _ := file.Read(buf)
			file.Close()

			if n > 0 {
				fmt.Print(string(buf[:n]))
			}
			offsets[f] = info.Size()
		}
		time.Sleep(1 * time.Second)
	}
}

// parseJobArgs splits args after the job name into env vars (KEY=VALUE) and extra args (after --).
func parseJobArgs(args []string) (extraEnv []string, extraArgs []string) {
	dashDash := false
	for _, arg := range args {
		if arg == "--" {
			dashDash = true
			continue
		}
		if dashDash {
			extraArgs = append(extraArgs, arg)
		} else if strings.Contains(arg, "=") {
			extraEnv = append(extraEnv, arg)
		} else {
			extraArgs = append(extraArgs, arg)
		}
	}
	return
}

// acquireLock and releaseLock are in lock_unix.go / lock_windows.go

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

	for _, line := range strings.Split(existing, "\n") {
		if strings.Contains(line, "dispatch") && strings.Contains(line, projectDir) {
			fmt.Printf("Cron already installed for %s\n", projectDir)
			fmt.Printf("  %s\n", line)
			return
		}
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

func selfUpdate(targetVersion string) error {
	return updater.Update(version, targetVersion)
}

func validateJob(name string, job *config.JobConfig, allJobs map[string]*config.JobConfig) []string {
	var issues []string
	if len(job.Commands) == 0 {
		issues = append(issues, fmt.Sprintf("%s: empty command", name))
	}
	if job.DependsOn != "" {
		if _, ok := allJobs[job.DependsOn]; !ok {
			issues = append(issues, fmt.Sprintf("%s: depends_on %q not found", name, job.DependsOn))
		}
	}
	if job.ActiveHours != nil {
		if job.ActiveHours[0] < 0 || job.ActiveHours[0] > 23 || job.ActiveHours[1] < 0 || job.ActiveHours[1] > 23 {
			issues = append(issues, fmt.Sprintf("%s: active_hours values must be 0-23", name))
		}
	}
	return issues
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
		if !(strings.Contains(line, "dispatch") && strings.Contains(line, projectDir)) {
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
