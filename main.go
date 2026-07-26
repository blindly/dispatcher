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
  init         Create a default Dispatcher.yaml
  list, ls     Show job status table (-a to include paused)
  next         Show upcoming scheduled jobs sorted by next run
  status, ps   Quick summary (-a to include paused)
  analytics    Job success rates and run history
  history      Show last 20 runs for a job
  run, exec    Force-run a specific job
  run-once     Run a job without DB tracking
  run-all      Force-run all jobs
  pause        Pause scheduled dispatch [duration] [reason]
  resume       Resume scheduled dispatch
  info         Show dispatcher configuration and state
  reset        Reset a job's next_run to now
  retry        Reset all failed jobs to run next cycle
  purge        Delete old run history (default: retention config)
  validate     Check config syntax
  reload       Reload scheduler config from Dispatcher.yaml
  logs         Show recent log output for a job
  watch        Live tail of job logs (all or specific job)
  notify       Send a live notification (for use inside jobs)
  enable       Enable the scheduler (install crontab entry from config)
  disable      Disable the scheduler (remove crontab entry)
  update       Self-update to latest release (or 'update beta')
  version      Show current version
  docs         Show full documentation

Options:
  --config     Config file path (default: Dispatcher.yaml)
`

// configCandidates are the config file names probed in the working directory,
// in priority order.
var configCandidates = []string{
	"Dispatcher.yaml",
	"Dispatcher.yml",
	"dispatcher.yaml",
	"dispatcher.yml",
}

// existingConfig returns the first candidate config present in the working
// directory, or "" if none exists.
func existingConfig() string {
	for _, c := range configCandidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

func detectConfig() string {
	if c := existingConfig(); c != "" {
		return c
	}
	return "Dispatcher.yaml" // default if none found
}

// fatalf prints a message to stderr and exits with status 1.
func fatalf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

// requireJob looks up a configured job, exiting with an error if it's unknown.
func requireJob(jobs map[string]*config.JobConfig, name string) *config.JobConfig {
	job, ok := jobs[name]
	if !ok {
		fatalf("Unknown job: %s", name)
	}
	return job
}

// requireArgs exits with the given usage line when args are missing.
func requireArgs(args []string, n int, usageLine string) {
	if len(args) < n {
		fatalf("%s", usageLine)
	}
}

// hasFlag reports whether any of the given flag spellings appear in args.
func hasFlag(args []string, flags ...string) bool {
	for _, a := range args {
		for _, f := range flags {
			if a == f {
				return true
			}
		}
	}
	return false
}

// jobEnv builds the extra environment injected into every job subprocess.
func jobEnv(name string, extra ...string) []string {
	return append(extra, "DISPATCH_JOB="+name)
}

// readCrontab returns the current user's crontab. ok is false when there is
// no crontab (or crontab is unavailable).
func readCrontab() (content string, ok bool) {
	out, err := exec.Command("crontab", "-l").Output()
	if err != nil {
		return "", false
	}
	return string(out), true
}

// writeCrontab replaces the current user's crontab with content.
func writeCrontab(content string) error {
	cmd := exec.Command("crontab", "-")
	cmd.Stdin = strings.NewReader(content)
	return cmd.Run()
}

// applyScheduler installs (enable=true) or removes the configured scheduler entry.
func applyScheduler(cfg *config.DispatcherConfig, configDir string, enable bool) {
	systemd := cfg.EffectiveScheduler() == "systemd"
	switch {
	case enable && systemd:
		enableSystemd(cfg, configDir)
	case enable:
		enableCron(cfg.Schedule, configDir)
	case systemd:
		disableSystemd(configDir)
	default:
		disableCron(configDir)
	}
}

// ensureDispatcherDir creates the .dispatcher directory and migrates old files from the project root.
func ensureDispatcherDir(configDir string) string {
	dispDir := filepath.Join(configDir, ".dispatcher")
	os.MkdirAll(dispDir, 0755)

	// Migrate old data.db (and WAL/SHM files) from project root into .dispatcher/
	oldDb := filepath.Join(configDir, "data.db")
	newDb := filepath.Join(dispDir, "data.db")
	if _, err := os.Stat(oldDb); err == nil {
		if _, err := os.Stat(newDb); os.IsNotExist(err) {
			if err := os.Rename(oldDb, newDb); err == nil {
				fmt.Println("Migrated data.db → .dispatcher/data.db")
			}
			for _, ext := range []string{"-wal", "-shm"} {
				old := filepath.Join(configDir, "data.db"+ext)
				if _, err := os.Stat(old); err == nil {
					os.Rename(old, filepath.Join(dispDir, "data.db"+ext))
				}
			}
		}
	}

	// Migrate logs directory
	oldLogs := filepath.Join(configDir, "logs")
	newLogs := filepath.Join(dispDir, "logs")
	if info, err := os.Stat(oldLogs); err == nil && info.IsDir() {
		if _, err := os.Stat(newLogs); os.IsNotExist(err) {
			if err := os.Rename(oldLogs, newLogs); err == nil {
				fmt.Println("Migrated logs/ → .dispatcher/logs/")
			}
		}
	}

	// Clean up old lock file
	oldLock := filepath.Join(configDir, ".dispatch.lock")
	if _, err := os.Stat(oldLock); err == nil {
		os.Remove(oldLock)
	}

	// Migrate crontab entry if needed
	migrateCron(configDir)

	return dispDir
}

// migrateCron updates an existing crontab entry to use the new .dispatcher/logs/ path.
func migrateCron(projectDir string) {
	out, ok := readCrontab()
	if !ok {
		return
	}

	lines := strings.Split(out, "\n")
	changed := false
	for i, line := range lines {
		if isDispatchLine(line, projectDir) {
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

	if err := writeCrontab(strings.Join(lines, "\n")); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not update crontab: %v\n", err)
		return
	}
	fmt.Println("Migrated crontab: logs/ → .dispatcher/logs/")
}


func initConfig() {
	if c := existingConfig(); c != "" {
		fatalf("Config already exists: %s", c)
	}

	defaultConfig := `timezone: America/New_York
# scheduler: systemd     # or "cron" — auto-detected if omitted
# schedule: "*/5 * * * *"
# update: false          # set to false in air-gapped environments
# retention: 90d
# pause_timeout: 1h

# notify:
#   discord:
#     webhook: ${DISCORD_WEBHOOK_URL}
#   ntfy:
#     url: https://ntfy.sh
#     topic: my-dispatch

jobs:
  hello:
    command: echo "Hello from dispatcher"
    interval: 5m
    description: Example job
    # adhoc: true
    # timeout: 5m
    # active_hours: [9, 17]
    # at_minute: 0           # run on the minute (0-59), prevents drift
    # days: weekdays   # or weekends, all, or [mon, wed, fri]
    # depends_on: other_job
    # retries: 2
    # retry_delay: 5s
`
	if err := os.WriteFile("Dispatcher.yaml", []byte(defaultConfig), 0644); err != nil {
		fatalf("Failed to create config: %v", err)
	}
	fmt.Println("Created Dispatcher.yaml")
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

	// Command aliases
	aliases := map[string]string{
		"ps":   "status",
		"ls":   "list",
		"exec": "run-once",
	}

	cmd := ""
	if len(args) > 0 {
		cmd = args[0]
		args = args[1:]
	}
	if alias, ok := aliases[cmd]; ok {
		cmd = alias
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

	if cmd == "init" {
		initConfig()
		return
	}

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		fatalf("Config not found: %s", configPath)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		if notifyCfg := config.ExtractNotifySettings(configPath); notifyCfg != nil {
			notify.SendLiveNotification(
				fmt.Sprintf("Config error in `%s`: %v", configPath, err),
				"",
				notify.FromConfig(*notifyCfg),
			)
		}
		os.Exit(1)
	}

	configDir := filepath.Dir(configPath)
	if configDir == "" || configDir == "." {
		configDir, _ = os.Getwd()
	} else {
		configDir, _ = filepath.Abs(configDir)
	}

	// update doesn't need DB or lock
	if cmd == "update" {
		if !cfg.AllowUpdate {
			fmt.Println("Update disabled (air-gapped environment)")
			return
		}
		targetVersion := ""
		if len(args) > 0 {
			targetVersion = args[0]
		}
		if err := selfUpdate(targetVersion); err != nil {
			fatalf("Update failed: %v", err)
		}
		return
	}

	// enable/disable don't need DB
	if cmd == "enable" {
		applyScheduler(cfg, configDir, true)
		return
	}
	if cmd == "disable" {
		applyScheduler(cfg, configDir, false)
		return
	}

	if cmd == "validate" {
		fmt.Printf("Config OK: %s (%d jobs)\n", configPath, len(cfg.Jobs))
		if cfg.Scheduler != "" {
			fmt.Printf("  Scheduler:   %s (explicit)\n", cfg.Scheduler)
		} else {
			fmt.Printf("  Scheduler:   auto-detect\n")
		}
		for name, job := range cfg.Jobs {
			issues := validateJob(name, job, cfg.Jobs)
			for _, issue := range issues {
				fmt.Printf("  WARNING: %s\n", issue)
			}
		}
		return
	}

	if cmd == "reload" {
		applyScheduler(cfg, configDir, true)
		return
	}

	if cmd == "notify" {
		requireArgs(args, 1, "usage: dispatch notify [--job NAME] <message...>")
		jobName := os.Getenv("DISPATCH_JOB")
		var msgParts []string
		for i := 0; i < len(args); i++ {
			if args[i] == "--job" && i+1 < len(args) {
				jobName = args[i+1]
				i++
				continue
			}
			msgParts = append(msgParts, args[i])
		}
		if len(msgParts) == 0 {
			fatalf("usage: dispatch notify [--job NAME] <message...>")
		}
		message := strings.Join(msgParts, " ")

		if err := notify.SendLiveNotification(message, jobName, notify.FromConfig(cfg.Notify)); err != nil {
			fatalf("notify: %v", err)
		}
		return
	}

	dispDir := ensureDispatcherDir(configDir)

	if cmd == "pause" {
		duration := time.Duration(cfg.PauseTimeout) * time.Second
		reason := ""
		// Parse args: [duration] [reason]
		if len(args) > 0 {
			if parsed, err := config.ParseInterval(args[0]); err == nil {
				duration = time.Duration(parsed) * time.Second
				if len(args) > 1 {
					reason = strings.Join(args[1:], " ")
				}
			} else {
				reason = strings.Join(args, " ")
			}
		}
		expiresAt := time.Now().UTC().Add(duration)
		writePauseFile(dispDir, expiresAt, reason)
		durationStr := FormatDuration(duration)
		msg := fmt.Sprintf("Dispatcher paused for %s (until %s)", durationStr, expiresAt.Local().Format("15:04"))
		if reason != "" {
			msg += fmt.Sprintf(" — %s", reason)
		}
		fmt.Println(msg)
		return
	}

	if cmd == "resume" {
		info := readPauseFile(dispDir)
		if info == nil {
			fmt.Println("Dispatcher is not paused")
			return
		}
		removePauseFile(dispDir)
		fmt.Println("Dispatcher resumed")
		return
	}

	if cmd == "logs" {
		requireArgs(args, 1, "usage: dispatch logs <job>")
		jobName := args[0]
		requireJob(cfg.Jobs, jobName)
		logPath := filepath.Join(dispDir, "logs", jobName+".log")
		content, err := os.ReadFile(logPath)
		if err != nil {
			if os.IsNotExist(err) {
				fmt.Printf("No logs found for %s\n", jobName)
				return
			}
			fatalf("Error reading log: %v", err)
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
			requireJob(cfg.Jobs, jobName)
		}
		watchLogs(dispDir, jobName, cfg.Jobs)
		return
	}

	// run-once doesn't need DB or lock
	if cmd == "run-once" {
		requireArgs(args, 1, "usage: dispatch run-once <job>")
		job := requireJob(cfg.Jobs, args[0])
		extraEnv, extraArgs := parseJobArgs(args[1:])
		rc, output := runner.RunOnceInteractive(job, extraArgs, jobEnv(args[0], extraEnv...))
		if strings.TrimSpace(output) != "" {
			fmt.Print(output)
		}
		if rc != 0 {
			os.Exit(1)
		}
		return
	}

	dbPath := filepath.Join(dispDir, "data.db")
	conn, err := db.Open(dbPath)
	if err != nil {
		fatalf("Error opening database: %v", err)
	}
	defer conn.Close()

	db.EnsureJobs(conn, cfg.Jobs)
	display.SetTimezone(cfg.Timezone)
	runner.SetLogDir(dispDir)

	notifyCfg := notify.FromConfig(cfg.Notify)

	// Check pause state for display commands
	_, pauseMsg := checkPause(dispDir)

	// Read-only: no lock needed
	if cmd == "list" {
		display.PrintPauseBanner(pauseMsg)
		display.PrintStatus(conn, cfg.Jobs, cfg.Timezone, hasFlag(args, "-a", "--all"))
		return
	}

	if cmd == "next" {
		display.PrintNextRuns(conn, cfg.Jobs, cfg.Timezone)
		return
	}

	if cmd == "analytics" {
		display.PrintAnalytics(conn)
		return
	}

	if cmd == "history" {
		requireArgs(args, 1, "usage: dispatch history <job>")
		requireJob(cfg.Jobs, args[0])
		display.PrintHistory(conn, args[0], 20)
		return
	}

	if cmd == "status" {
		display.PrintPauseBanner(pauseMsg)
		sched := cfg.EffectiveScheduler()
		schedStatus := resolveSchedulerStatus(sched, configDir)
		display.PrintQuickStatus(conn, cfg.Jobs, cfg.Timezone, configDir, hasFlag(args, "-a", "--all"), sched, schedStatus)
		return
	}

	if cmd == "info" {
		printInfo(cfg, configPath, configDir, dispDir, conn)
		return
	}

	if cmd == "retry-failed" || cmd == "retry" {
		now := db.NowUTC().Format(time.RFC3339)
		rows, err := conn.Query("SELECT name FROM cron_jobs WHERE last_status LIKE 'failed%'")
		if err != nil {
			fatalf("Error: %v", err)
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
			conn.Exec("UPDATE cron_jobs SET next_run_at = ?, last_status = NULL, force_next = 1 WHERE name = ?", now, name)
		}
		fmt.Printf("Reset %d failed jobs: %s\n", len(reset), strings.Join(reset, ", "))
		return
	}

	if cmd == "reset" {
		requireArgs(args, 1, "usage: dispatch reset <job>")
		requireJob(cfg.Jobs, args[0])
		conn.Exec("UPDATE cron_jobs SET next_run_at = ? WHERE name = ?",
			db.NowUTC().Format(time.RFC3339), args[0])
		fmt.Printf("Reset %s — will run on next dispatch\n", args[0])
		return
	}

	// dispatch run for adhoc jobs — no lock needed
	if cmd == "run" {
		requireArgs(args, 1, "usage: dispatch run <job> [KEY=VALUE...] [-- args...]")
		job := requireJob(cfg.Jobs, args[0])
		if job.Adhoc {
			extraEnv, extraArgs := parseJobArgs(args[1:])
			rc, _, _ := runner.RunJobInteractive(conn, job, extraArgs, jobEnv(args[0], extraEnv...))
			if rc != 0 {
				os.Exit(1)
			}
			return
		}
	}

	// Execution commands — acquire lock
	lockFd := acquireLock(dispDir)
	if lockFd == -1 {
		fmt.Println("Another dispatcher is already running — skipping")
		return
	}
	defer releaseLock(lockFd, dispDir)

	// We hold the lock — any non-adhoc running_since is stale from a crashed run
	db.ClearStaleRunning(conn, cfg.Jobs)

	switch cmd {
	case "run":
		// Non-adhoc jobs (adhoc handled above)
		job := cfg.Jobs[args[0]]
		extraEnv, extraArgs := parseJobArgs(args[1:])
		rc, elapsed, output := runner.RunJobInteractive(conn, job, extraArgs, jobEnv(args[0], extraEnv...))
		results := []notify.JobResult{{Name: args[0], ExitCode: rc, Elapsed: elapsed, Output: output, Notify: job.Notify}}
		notify.SendAll(results, notifyCfg)
		if rc != 0 {
			os.Exit(1)
		}

	case "run-all":
		var results []notify.JobResult
		for name, job := range cfg.Jobs {
			if job.Adhoc || job.Paused {
				continue
			}
			rc, elapsed, output := runner.RunJobInteractive(conn, job, nil, jobEnv(name))
			results = append(results, notify.JobResult{Name: name, ExitCode: rc, Elapsed: elapsed, Output: output, Notify: job.Notify})
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
				fatalf("Invalid duration: %v", err)
			}
			days = purgeInterval / 86400
			if days < 1 {
				days = 1
			}
		}
		deleted, err := db.PurgeHistory(conn, days)
		if err != nil {
			fatalf("Purge failed: %v", err)
		}
		fmt.Printf("Purged %d history entries older than %dd\n", deleted, days)

	case "":
		if paused, msg := checkPause(dispDir); paused {
			fmt.Println(msg)
			return
		}
		dispatch(conn, cfg, notifyCfg)

	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}
}

func dispatch(conn *sql.DB, cfg *config.DispatcherConfig, notifyCfg notify.NotifyConfig) {
	db.SetMeta(conn, "last_dispatch_at", db.NowUTC().Format(time.RFC3339))

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
		rc, elapsed, output := runner.RunJob(conn, job, nil, jobEnv(name))
		results = append(results, notify.JobResult{Name: name, ExitCode: rc, Elapsed: elapsed, Output: output, Notify: job.Notify})
	}

	passed, failed, totalTime := notify.Tally(results)
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
		fatalf("Failed to fetch docs: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		fatalf("Failed to fetch docs: HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fatalf("Failed to read docs: %v", err)
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

func resolveSchedulerStatus(schedulerType string, configDir string) string {
	if schedulerType == "systemd" {
		if installed, detail := isSystemdInstalled(configDir); installed {
			return detail
		}
		return "disabled"
	}
	// cron
	if installed, schedule := display.IsCronInstalled(configDir); installed {
		return "enabled (" + schedule + ")"
	}
	return "disabled"
}

func enableCron(schedule string, projectDir string) {
	dispatchPath, err := os.Executable()
	if err != nil {
		dispatchPath = "dispatch"
	}
	cronLine := fmt.Sprintf("%s cd %s && %s >> .dispatcher/logs/dispatcher.log 2>&1", schedule, projectDir, dispatchPath)

	existing, _ := readCrontab()

	newContent, status := buildCrontab(existing, cronLine, projectDir)

	if status == cronUnchanged {
		fmt.Printf("Already enabled (unchanged): %s\n", cronLine)
		return
	}

	if err := writeCrontab(newContent); err != nil {
		fatalf("Failed to write crontab: %v", err)
	}

	switch status {
	case cronAdded:
		fmt.Printf("Cron enabled: %s\n", cronLine)
	case cronUpdated:
		fmt.Printf("Cron updated: %s\n", cronLine)
	}
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
		start, end := job.ActiveHours[0], job.ActiveHours[1]
		if start < 0 || start > 23 || end < 0 || end > 24 || start == end {
			issues = append(issues, fmt.Sprintf("%s: active_hours must be 0-23 start with end strictly greater, or wrapping (e.g. [22, 2]); 24 means midnight", name))
		}
	}
	return issues
}

func disableCron(projectDir string) {
	out, ok := readCrontab()
	if !ok {
		fmt.Println("No crontab found")
		return
	}

	lines := strings.Split(out, "\n")
	var filtered []string
	for _, line := range lines {
		if !isDispatchLine(line, projectDir) {
			filtered = append(filtered, line)
		}
	}

	if len(filtered) == len(lines) {
		fmt.Printf("No cron entry found for %s\n", projectDir)
		return
	}

	if err := writeCrontab(strings.Join(filtered, "\n")); err != nil {
		fatalf("Failed to update crontab: %v", err)
	}
	fmt.Printf("Cron disabled for %s\n", projectDir)
}
