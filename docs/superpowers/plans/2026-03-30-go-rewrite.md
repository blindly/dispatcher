# Go Dispatcher Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rewrite the Python cron-dispatcher as a static Go binary with the same YAML config, SQLite state tracking, and Discord notifications.

**Architecture:** Single Go module with `main.go` handling CLI subcommand routing and five `internal/` packages: `config` (YAML + env expansion), `db` (SQLite state), `runner` (subprocess exec + retry), `notify` (Discord webhooks), `display` (status table). Pure-Go SQLite via `modernc.org/sqlite` for a truly static binary.

**Tech Stack:** Go 1.22+, `gopkg.in/yaml.v3`, `modernc.org/sqlite`

---

## File Structure

```
dispatcher/
├── main.go                        # CLI entry point, subcommand routing, file lock
├── go.mod
├── go.sum
├── dispatcher.yaml                # example config
├── internal/
│   ├── config/
│   │   ├── config.go              # DispatcherConfig, JobConfig, YAML loading, env expansion, interval parsing
│   │   └── config_test.go         # interval parsing, env expansion, full YAML load tests
│   ├── db/
│   │   ├── db.go                  # Open, EnsureJobs, GetDueJobs, UpdateAfterRun, NowUTC
│   │   └── db_test.go             # registration, due queries, active hours filtering
│   ├── runner/
│   │   ├── runner.go              # RunOnce, RunJob, ResolveOrder
│   │   └── runner_test.go         # success, failure, retry, dependency ordering
│   ├── notify/
│   │   ├── notify.go              # SendDiscordSummary
│   │   └── notify_test.go         # embed formatting, truncation
│   └── display/
│       ├── display.go             # PrintStatus
│       └── display_test.go        # interval formatting, date formatting
├── main_test.go                   # integration: subcommand smoke tests
└── docs/
    └── superpowers/
        └── specs/
            └── 2026-03-30-go-rewrite-design.md
```

---

### Task 1: Project scaffold — go.mod, main.go, example config

**Files:**
- Create: `go.mod`
- Create: `main.go`
- Create: `dispatcher.yaml`

- [ ] **Step 1: Initialize go module**

```bash
cd /home/wk/finance/dispatcher
go mod init github.com/blindly/dispatcher
```

- [ ] **Step 2: Write main.go with subcommand routing skeleton**

```go
package main

import (
	"fmt"
	"os"
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

	_ = configPath // used in later tasks

	switch cmd {
	case "":
		fmt.Println("dispatch: no due jobs") // placeholder
	case "list":
		fmt.Println("list: not yet implemented")
	case "run":
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: dispatch run <job>")
			os.Exit(1)
		}
		fmt.Printf("run %s: not yet implemented\n", args[0])
	case "run-once":
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: dispatch run-once <job>")
			os.Exit(1)
		}
		fmt.Printf("run-once %s: not yet implemented\n", args[0])
	case "run-all":
		fmt.Println("run-all: not yet implemented")
	case "reset":
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: dispatch reset <job>")
			os.Exit(1)
		}
		fmt.Printf("reset %s: not yet implemented\n", args[0])
	case "install":
		schedule := "*/5 * * * *"
		if len(args) > 0 {
			schedule = args[0]
		}
		fmt.Printf("install %s: not yet implemented\n", schedule)
	case "uninstall":
		fmt.Println("uninstall: not yet implemented")
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}
}
```

- [ ] **Step 3: Write example dispatcher.yaml**

```yaml
timezone: America/New_York

notify:
  discord:
    webhook: ${DISCORD_WEBHOOK_URL}

jobs:
  hello:
    command: echo "Hello from dispatcher"
    interval: 5m
    description: Test job

  world:
    command: echo "World"
    interval: 10m
    description: Depends on hello
    depends_on: hello
```

- [ ] **Step 4: Verify it builds and runs**

```bash
go build -o dispatch .
./dispatch --help
./dispatch list
```

Expected: help text prints, "list: not yet implemented" prints.

- [ ] **Step 5: Commit**

```bash
git add go.mod main.go dispatcher.yaml
git commit -m "feat: project scaffold with CLI subcommand routing"
```

---

### Task 2: Config package — YAML loading, env expansion, interval parsing

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`

- [ ] **Step 1: Write failing tests for interval parsing**

```go
package config

import "testing"

func TestParseInterval_Minutes(t *testing.T) {
	got, err := ParseInterval("5m")
	if err != nil {
		t.Fatal(err)
	}
	if got != 300 {
		t.Errorf("got %d, want 300", got)
	}
}

func TestParseInterval_Hours(t *testing.T) {
	got, err := ParseInterval("2h")
	if err != nil {
		t.Fatal(err)
	}
	if got != 7200 {
		t.Errorf("got %d, want 7200", got)
	}
}

func TestParseInterval_Days(t *testing.T) {
	got, err := ParseInterval("1d")
	if err != nil {
		t.Fatal(err)
	}
	if got != 86400 {
		t.Errorf("got %d, want 86400", got)
	}
}

func TestParseInterval_Seconds(t *testing.T) {
	got, err := ParseInterval("30s")
	if err != nil {
		t.Fatal(err)
	}
	if got != 30 {
		t.Errorf("got %d, want 30", got)
	}
}

func TestParseInterval_Weeks(t *testing.T) {
	got, err := ParseInterval("1w")
	if err != nil {
		t.Fatal(err)
	}
	if got != 604800 {
		t.Errorf("got %d, want 604800", got)
	}
}

func TestParseInterval_Invalid(t *testing.T) {
	_, err := ParseInterval("abc")
	if err == nil {
		t.Error("expected error for invalid interval")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
cd /home/wk/finance/dispatcher
go test ./internal/config/ -v
```

Expected: compilation error, `ParseInterval` not defined.

- [ ] **Step 3: Write failing tests for env expansion**

Add to `config_test.go`:

```go
func TestExpandEnv_Substitutes(t *testing.T) {
	t.Setenv("TEST_URL", "https://example.com")
	got := ExpandEnv("webhook: ${TEST_URL}")
	want := "webhook: https://example.com"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestExpandEnv_MissingVar(t *testing.T) {
	got := ExpandEnv("${NONEXISTENT_VAR_12345}")
	if got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}
```

- [ ] **Step 4: Write failing tests for YAML loading**

Add to `config_test.go`:

```go
import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_FullConfig(t *testing.T) {
	yaml := `
timezone: America/New_York

notify:
  discord:
    webhook: https://discord.com/hook

jobs:
  test_job:
    command: echo hello
    interval: 5m
    active_hours: [9, 17]
    description: A test job
  dependent_job:
    command: echo world
    interval: 10m
    depends_on: test_job
    description: Depends on test_job
`
	dir := t.TempDir()
	path := filepath.Join(dir, "dispatcher.yaml")
	os.WriteFile(path, []byte(yaml), 0644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Timezone != "America/New_York" {
		t.Errorf("timezone = %q, want America/New_York", cfg.Timezone)
	}
	if cfg.Notify.Discord.Webhook != "https://discord.com/hook" {
		t.Errorf("webhook = %q", cfg.Notify.Discord.Webhook)
	}
	if len(cfg.Jobs) != 2 {
		t.Fatalf("got %d jobs, want 2", len(cfg.Jobs))
	}
	job := cfg.Jobs["test_job"]
	if job.Command != "echo hello" {
		t.Errorf("command = %q", job.Command)
	}
	if job.IntervalSeconds != 300 {
		t.Errorf("interval = %d, want 300", job.IntervalSeconds)
	}
	if job.ActiveHours == nil || job.ActiveHours[0] != 9 || job.ActiveHours[1] != 17 {
		t.Errorf("active_hours = %v", job.ActiveHours)
	}
	dep := cfg.Jobs["dependent_job"]
	if dep.DependsOn != "test_job" {
		t.Errorf("depends_on = %q", dep.DependsOn)
	}
}

func TestLoad_EnvExpansion(t *testing.T) {
	t.Setenv("MY_WEBHOOK", "https://discord.com/hook")
	yaml := `
notify:
  discord:
    webhook: ${MY_WEBHOOK}

jobs:
  j1:
    command: echo hi
    interval: 1h
`
	dir := t.TempDir()
	path := filepath.Join(dir, "dispatcher.yaml")
	os.WriteFile(path, []byte(yaml), 0644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Notify.Discord.Webhook != "https://discord.com/hook" {
		t.Errorf("webhook = %q", cfg.Notify.Discord.Webhook)
	}
}

func TestLoad_Defaults(t *testing.T) {
	yaml := `
jobs:
  j1:
    command: echo hi
    interval: 1h
`
	dir := t.TempDir()
	path := filepath.Join(dir, "dispatcher.yaml")
	os.WriteFile(path, []byte(yaml), 0644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Timezone != "America/New_York" {
		t.Errorf("timezone = %q, want America/New_York", cfg.Timezone)
	}
	job := cfg.Jobs["j1"]
	if job.Retries != 2 {
		t.Errorf("retries = %d, want 2", job.Retries)
	}
	if job.RetryDelay != 5 {
		t.Errorf("retry_delay = %d, want 5", job.RetryDelay)
	}
}
```

- [ ] **Step 5: Implement config.go**

```go
package config

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type JobConfig struct {
	Name            string
	Command         string `yaml:"command"`
	IntervalSeconds int
	Description     string  `yaml:"description"`
	ActiveHours     *[2]int `yaml:"-"`
	DependsOn       string  `yaml:"depends_on"`
	Retries         int     `yaml:"retries"`
	RetryDelay      int     `yaml:"-"` // seconds, parsed from retry_delay
}

type DiscordConfig struct {
	Webhook string `yaml:"webhook"`
}

type NotifyConfig struct {
	Discord DiscordConfig `yaml:"discord"`
}

type DispatcherConfig struct {
	Timezone string       `yaml:"timezone"`
	Notify   NotifyConfig `yaml:"notify"`
	Jobs     map[string]*JobConfig
}

// rawJob is the intermediate YAML structure before parsing.
type rawJob struct {
	Command     string `yaml:"command"`
	Interval    string `yaml:"interval"`
	Description string `yaml:"description"`
	ActiveHours []int  `yaml:"active_hours"`
	DependsOn   string `yaml:"depends_on"`
	Retries     *int   `yaml:"retries"`
	RetryDelay  string `yaml:"retry_delay"`
}

type rawConfig struct {
	Timezone string            `yaml:"timezone"`
	Notify   NotifyConfig      `yaml:"notify"`
	Jobs     map[string]rawJob `yaml:"jobs"`
}

var intervalRe = regexp.MustCompile(`^(\d+)([smhdw])$`)

func ParseInterval(s string) (int, error) {
	s = strings.TrimSpace(s)
	m := intervalRe.FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("invalid interval: %q (expected e.g. 5m, 2h, 1d)", s)
	}
	value, _ := strconv.Atoi(m[1])
	multipliers := map[string]int{"s": 1, "m": 60, "h": 3600, "d": 86400, "w": 604800}
	return value * multipliers[m[2]], nil
}

func ExpandEnv(text string) string {
	re := regexp.MustCompile(`\$\{(\w+)\}`)
	return re.ReplaceAllStringFunc(text, func(match string) string {
		varName := re.FindStringSubmatch(match)[1]
		return os.Getenv(varName)
	})
}

func Load(path string) (*DispatcherConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	expanded := ExpandEnv(string(data))

	var raw rawConfig
	if err := yaml.Unmarshal([]byte(expanded), &raw); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	cfg := &DispatcherConfig{
		Timezone: raw.Timezone,
		Notify:   raw.Notify,
		Jobs:     make(map[string]*JobConfig),
	}

	if cfg.Timezone == "" {
		cfg.Timezone = "America/New_York"
	}

	for name, rj := range raw.Jobs {
		intervalSec, err := ParseInterval(rj.Interval)
		if err != nil {
			return nil, fmt.Errorf("job %q: %w", name, err)
		}

		job := &JobConfig{
			Name:            name,
			Command:         rj.Command,
			IntervalSeconds: intervalSec,
			Description:     rj.Description,
			DependsOn:       rj.DependsOn,
			Retries:         2, // default
			RetryDelay:      5, // default 5s
		}

		if rj.Retries != nil {
			job.Retries = *rj.Retries
		}

		if rj.RetryDelay != "" {
			delay, err := ParseInterval(rj.RetryDelay)
			if err != nil {
				return nil, fmt.Errorf("job %q retry_delay: %w", name, err)
			}
			job.RetryDelay = delay
		}

		if len(rj.ActiveHours) == 2 {
			job.ActiveHours = &[2]int{rj.ActiveHours[0], rj.ActiveHours[1]}
		}

		cfg.Jobs[name] = job
	}

	return cfg, nil
}
```

- [ ] **Step 6: Add yaml dependency**

```bash
cd /home/wk/finance/dispatcher
go get gopkg.in/yaml.v3
```

- [ ] **Step 7: Run tests to verify they pass**

```bash
go test ./internal/config/ -v
```

Expected: all tests PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/config/ go.mod go.sum
git commit -m "feat: config package — YAML loading, env expansion, interval parsing"
```

---

### Task 3: DB package — SQLite schema, due jobs, update after run

**Files:**
- Create: `internal/db/db.go`
- Create: `internal/db/db_test.go`

- [ ] **Step 1: Write failing tests**

```go
package db

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/blindly/dispatcher/internal/config"
)

func TestOpen_CreatesTable(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	conn, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	var count int
	err = conn.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='cron_jobs'").Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("expected cron_jobs table, got count=%d", count)
	}
}

func TestEnsureJobs_RegistersNew(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	conn, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	jobs := map[string]*config.JobConfig{
		"job_a": {Name: "job_a", Command: "echo a", IntervalSeconds: 300},
		"job_b": {Name: "job_b", Command: "echo b", IntervalSeconds: 600},
	}
	EnsureJobs(conn, jobs)

	rows, err := conn.Query("SELECT name FROM cron_jobs ORDER BY name")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		rows.Scan(&name)
		names = append(names, name)
	}
	if len(names) != 2 || names[0] != "job_a" || names[1] != "job_b" {
		t.Errorf("got names=%v, want [job_a, job_b]", names)
	}
}

func TestGetDueJobs_ReturnsOverdue(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	conn, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	past := NowUTC().Add(-1 * time.Hour).Format(time.RFC3339)
	future := NowUTC().Add(1 * time.Hour).Format(time.RFC3339)
	conn.Exec("INSERT INTO cron_jobs (name, next_run_at) VALUES (?, ?)", "overdue", past)
	conn.Exec("INSERT INTO cron_jobs (name, next_run_at) VALUES (?, ?)", "future", future)

	jobs := map[string]*config.JobConfig{
		"overdue": {Name: "overdue", Command: "echo", IntervalSeconds: 300},
		"future":  {Name: "future", Command: "echo", IntervalSeconds: 300},
	}
	due := GetDueJobs(conn, jobs, "America/New_York")

	found := false
	for _, name := range due {
		if name == "overdue" {
			found = true
		}
		if name == "future" {
			t.Error("future job should not be due")
		}
	}
	if !found {
		t.Error("overdue job should be due")
	}
}

func TestGetDueJobs_FiltersActiveHours(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	conn, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	past := NowUTC().Add(-1 * time.Hour).Format(time.RFC3339)
	conn.Exec("INSERT INTO cron_jobs (name, next_run_at) VALUES (?, ?)", "narrow", past)

	// active_hours 3-4 AM — almost certainly not the current hour
	hours := [2]int{3, 4}
	jobs := map[string]*config.JobConfig{
		"narrow": {Name: "narrow", Command: "echo", IntervalSeconds: 300, ActiveHours: &hours},
	}
	due := GetDueJobs(conn, jobs, "America/New_York")
	for _, name := range due {
		if name == "narrow" {
			t.Error("narrow job should be filtered by active_hours")
		}
	}
}

func TestUpdateAfterRun(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	conn, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	now := NowUTC().Format(time.RFC3339)
	conn.Exec("INSERT INTO cron_jobs (name, next_run_at) VALUES (?, ?)", "test", now)

	UpdateAfterRun(conn, "test", 300, 0, 1.5, "ok")

	var runCount int
	var status string
	conn.QueryRow("SELECT run_count, last_status FROM cron_jobs WHERE name = ?", "test").Scan(&runCount, &status)
	if runCount != 1 {
		t.Errorf("run_count = %d, want 1", runCount)
	}
	if status != "ok" {
		t.Errorf("status = %q, want ok", status)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/db/ -v
```

Expected: compilation error, `Open` not defined.

- [ ] **Step 3: Implement db.go**

```go
package db

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/blindly/dispatcher/internal/config"
	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS cron_jobs (
    name          TEXT PRIMARY KEY,
    last_run_at   TEXT,
    next_run_at   TEXT NOT NULL,
    last_status   TEXT,
    last_duration_s REAL,
    run_count     INTEGER DEFAULT 0,
    fail_count    INTEGER DEFAULT 0
);`

func Open(dbPath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	db.Exec("PRAGMA journal_mode=WAL")
	db.Exec("PRAGMA busy_timeout=10000")
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("creating schema: %w", err)
	}
	return db, nil
}

func NowUTC() time.Time {
	return time.Now().UTC()
}

func EnsureJobs(db *sql.DB, jobs map[string]*config.JobConfig) {
	now := NowUTC().Format(time.RFC3339)
	for name := range jobs {
		db.Exec("INSERT OR IGNORE INTO cron_jobs (name, next_run_at) VALUES (?, ?)", name, now)
	}
}

func IsInActiveHours(hours *[2]int, tzName string) bool {
	if hours == nil {
		return true
	}
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		return true
	}
	nowHour := time.Now().In(loc).Hour()
	start, end := hours[0], hours[1]
	if start <= end {
		return nowHour >= start && nowHour < end
	}
	return nowHour >= start || nowHour < end
}

func GetDueJobs(db *sql.DB, jobs map[string]*config.JobConfig, tzName string) []string {
	now := NowUTC().Format(time.RFC3339)
	rows, err := db.Query("SELECT name FROM cron_jobs WHERE next_run_at <= ? ORDER BY next_run_at", now)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var due []string
	for rows.Next() {
		var name string
		rows.Scan(&name)
		job, ok := jobs[name]
		if !ok {
			continue
		}
		if !IsInActiveHours(job.ActiveHours, tzName) {
			continue
		}
		due = append(due, name)
	}
	return due
}

func UpdateAfterRun(db *sql.DB, name string, intervalSeconds int, rc int, elapsed float64, status string) {
	now := NowUTC()
	nextRun := now.Add(time.Duration(intervalSeconds) * time.Second).Format(time.RFC3339)
	failInc := 0
	if rc != 0 {
		failInc = 1
	}
	db.Exec(
		`UPDATE cron_jobs SET last_run_at = ?, next_run_at = ?, last_status = ?,
		 last_duration_s = ?, run_count = run_count + 1, fail_count = fail_count + ?
		 WHERE name = ?`,
		now.Format(time.RFC3339), nextRun, status, elapsed, failInc, name,
	)
}
```

- [ ] **Step 4: Add sqlite dependency**

```bash
cd /home/wk/finance/dispatcher
go get modernc.org/sqlite
```

- [ ] **Step 5: Run tests to verify they pass**

```bash
go test ./internal/db/ -v
```

Expected: all tests PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/db/ go.mod go.sum
git commit -m "feat: db package — SQLite schema, due jobs, update after run"
```

---

### Task 4: Runner package — subprocess exec, retry, dependency ordering

**Files:**
- Create: `internal/runner/runner.go`
- Create: `internal/runner/runner_test.go`

- [ ] **Step 1: Write failing tests**

```go
package runner

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/blindly/dispatcher/internal/config"
	"github.com/blindly/dispatcher/internal/db"
)

func TestRunOnce_Success(t *testing.T) {
	job := &config.JobConfig{
		Name:    "echo_test",
		Command: "echo hello",
	}
	rc, output := RunOnce(job)
	if rc != 0 {
		t.Errorf("rc = %d, want 0", rc)
	}
	if output == "" || !contains(output, "hello") {
		t.Errorf("output = %q, want to contain 'hello'", output)
	}
}

func TestRunOnce_Failure(t *testing.T) {
	cmd := "false"
	if runtime.GOOS == "windows" {
		cmd = "cmd /c exit 1"
	}
	job := &config.JobConfig{
		Name:    "fail_test",
		Command: cmd,
	}
	rc, _ := RunOnce(job)
	if rc == 0 {
		t.Error("expected non-zero exit code")
	}
}

func TestRunJob_UpdatesDB(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	conn, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	job := &config.JobConfig{
		Name:            "test_echo",
		Command:         "echo hello",
		IntervalSeconds: 300,
		Retries:         0,
		RetryDelay:      1,
	}
	jobs := map[string]*config.JobConfig{"test_echo": job}
	db.EnsureJobs(conn, jobs)

	rc, _, output := RunJob(conn, job)
	if rc != 0 {
		t.Errorf("rc = %d, want 0", rc)
	}
	if !contains(output, "hello") {
		t.Errorf("output = %q, want to contain 'hello'", output)
	}

	var runCount int
	var status string
	conn.QueryRow("SELECT run_count, last_status FROM cron_jobs WHERE name = ?", "test_echo").Scan(&runCount, &status)
	if runCount != 1 {
		t.Errorf("run_count = %d, want 1", runCount)
	}
	if status != "ok" {
		t.Errorf("status = %q, want ok", status)
	}
}

func TestResolveOrder_RespectsDependencies(t *testing.T) {
	jobs := map[string]*config.JobConfig{
		"fetch": {Name: "fetch", Command: "echo", IntervalSeconds: 300},
		"scan":  {Name: "scan", Command: "echo", IntervalSeconds: 300, DependsOn: "fetch"},
		"other": {Name: "other", Command: "echo", IntervalSeconds: 300},
	}
	due := []string{"scan", "fetch", "other"}
	ordered := ResolveOrder(due, jobs)

	fetchIdx, scanIdx := -1, -1
	for i, name := range ordered {
		if name == "fetch" {
			fetchIdx = i
		}
		if name == "scan" {
			scanIdx = i
		}
	}
	if fetchIdx >= scanIdx {
		t.Errorf("fetch (idx=%d) should come before scan (idx=%d)", fetchIdx, scanIdx)
	}
	if len(ordered) != 3 {
		t.Errorf("got %d items, want 3", len(ordered))
	}
}

func TestResolveOrder_DepNotDue(t *testing.T) {
	jobs := map[string]*config.JobConfig{
		"fetch": {Name: "fetch", Command: "echo", IntervalSeconds: 300},
		"scan":  {Name: "scan", Command: "echo", IntervalSeconds: 300, DependsOn: "fetch"},
	}
	due := []string{"scan"}
	ordered := ResolveOrder(due, jobs)
	if len(ordered) != 1 || ordered[0] != "scan" {
		t.Errorf("got %v, want [scan]", ordered)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/runner/ -v
```

Expected: compilation error, `RunOnce` not defined.

- [ ] **Step 3: Implement runner.go**

```go
package runner

import (
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

func RunOnce(job *config.JobConfig) (int, string) {
	parts := strings.Fields(job.Command)
	if len(parts) == 0 {
		return -2, "empty command"
	}
	cmd := exec.Command(parts[0], parts[1:]...)
	cmd.Env = os.Environ()

	out, err := cmd.CombinedOutput()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), string(out)
		}
		return -2, err.Error()
	}
	return 0, string(out)
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
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/runner/ -v
```

Expected: all tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/runner/
git commit -m "feat: runner package — subprocess exec, retry, dependency ordering"
```

---

### Task 5: Notify package — Discord webhook

**Files:**
- Create: `internal/notify/notify.go`
- Create: `internal/notify/notify_test.go`

- [ ] **Step 1: Write failing tests**

```go
package notify

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSendDiscordSummary_AllPass(t *testing.T) {
	var gotPayload map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotPayload)
		w.WriteHeader(204)
	}))
	defer server.Close()

	results := []JobResult{
		{Name: "job1", ExitCode: 0, Elapsed: 1.5, Output: "ok"},
		{Name: "job2", ExitCode: 0, Elapsed: 2.0, Output: "done"},
	}
	SendDiscordSummary(results, server.URL)

	embeds := gotPayload["embeds"].([]interface{})
	embed := embeds[0].(map[string]interface{})
	color := int(embed["color"].(float64))
	if color != 0x00FF00 {
		t.Errorf("color = %x, want green (00FF00)", color)
	}
}

func TestSendDiscordSummary_MixedResults(t *testing.T) {
	var gotPayload map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotPayload)
		w.WriteHeader(204)
	}))
	defer server.Close()

	results := []JobResult{
		{Name: "job1", ExitCode: 0, Elapsed: 1.5, Output: "ok"},
		{Name: "job2", ExitCode: 1, Elapsed: 2.0, Output: "error line"},
	}
	SendDiscordSummary(results, server.URL)

	embeds := gotPayload["embeds"].([]interface{})
	embed := embeds[0].(map[string]interface{})
	color := int(embed["color"].(float64))
	if color != 0xFF9900 {
		t.Errorf("color = %x, want yellow (FF9900)", color)
	}
}

func TestSendDiscordSummary_NoWebhook(t *testing.T) {
	// Should not panic or error
	results := []JobResult{{Name: "job1", ExitCode: 0, Elapsed: 1.0, Output: "ok"}}
	SendDiscordSummary(results, "")
}

func TestSendDiscordSummary_NoResults(t *testing.T) {
	// Should not send anything
	SendDiscordSummary(nil, "https://example.com")
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/notify/ -v
```

Expected: compilation error, `JobResult` not defined.

- [ ] **Step 3: Implement notify.go**

```go
package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type JobResult struct {
	Name     string
	ExitCode int
	Elapsed  float64
	Output   string
}

func extractSummary(rc int, output string) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	var nonEmpty []string
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			nonEmpty = append(nonEmpty, strings.TrimSpace(l))
		}
	}
	if rc != 0 {
		tail := nonEmpty
		if len(tail) > 2 {
			tail = tail[len(tail)-2:]
		}
		if len(tail) > 0 {
			s := "```\n" + strings.Join(tail, "\n") + "\n```"
			if len(s) > 300 {
				s = s[:300]
			}
			return s
		}
		return ""
	}
	if len(nonEmpty) > 0 {
		s := nonEmpty[len(nonEmpty)-1]
		if len(s) > 200 {
			s = s[:200]
		}
		return s
	}
	return ""
}

func SendDiscordSummary(results []JobResult, webhookURL string) {
	if len(results) == 0 || webhookURL == "" {
		return
	}

	passed := 0
	failed := 0
	totalTime := 0.0
	for _, r := range results {
		if r.ExitCode == 0 {
			passed++
		} else {
			failed++
		}
		totalTime += r.Elapsed
	}

	var lines []string
	for _, r := range results {
		icon := "\u2705"
		if r.ExitCode != 0 {
			icon = "\u274c"
		}
		line := fmt.Sprintf("%s **%s** (%.1fs)", icon, r.Name, r.Elapsed)
		summary := extractSummary(r.ExitCode, r.Output)
		if summary != "" {
			line += "\n" + summary
		}
		lines = append(lines, line)
	}

	description := strings.Join(lines, "\n")
	if len(description) > 3900 {
		description = description[:3900] + "\n..."
	}

	color := 0x00FF00
	if failed > 0 && passed > 0 {
		color = 0xFF9900
	} else if failed > 0 {
		color = 0xFF0000
	}

	title := fmt.Sprintf("Dispatcher: %d ok, %d failed (%.0fs)", passed, failed, totalTime)

	payload := map[string]interface{}{
		"embeds": []map[string]interface{}{
			{
				"title":       title,
				"color":       color,
				"description": description,
				"timestamp":   time.Now().UTC().Format(time.RFC3339),
			},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		fmt.Printf("  Discord notification marshal failed: %v\n", err)
		return
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(webhookURL, "application/json", bytes.NewReader(body))
	if err != nil {
		fmt.Printf("  Discord notification failed: %v\n", err)
		return
	}
	resp.Body.Close()
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/notify/ -v
```

Expected: all tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/notify/
git commit -m "feat: notify package — Discord webhook summaries"
```

---

### Task 6: Display package — status table formatting

**Files:**
- Create: `internal/display/display.go`
- Create: `internal/display/display_test.go`

- [ ] **Step 1: Write failing tests**

```go
package display

import "testing"

func TestFormatInterval_Minutes(t *testing.T) {
	if got := FormatInterval(300); got != "5m" {
		t.Errorf("got %q, want 5m", got)
	}
}

func TestFormatInterval_Hours(t *testing.T) {
	if got := FormatInterval(7200); got != "2h" {
		t.Errorf("got %q, want 2h", got)
	}
}

func TestFormatInterval_Days(t *testing.T) {
	if got := FormatInterval(86400); got != "1d" {
		t.Errorf("got %q, want 1d", got)
	}
}

func TestFormatInterval_Weeks(t *testing.T) {
	if got := FormatInterval(604800); got != "1w" {
		t.Errorf("got %q, want 1w", got)
	}
}

func TestFormatDt_Valid(t *testing.T) {
	got := FormatDt("2025-01-15T10:30:00Z")
	if got != "2025-01-15 10:30" {
		t.Errorf("got %q", got)
	}
}

func TestFormatDt_Empty(t *testing.T) {
	if got := FormatDt(""); got != "-" {
		t.Errorf("got %q, want -", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/display/ -v
```

Expected: compilation error, `FormatInterval` not defined.

- [ ] **Step 3: Implement display.go**

```go
package display

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/blindly/dispatcher/internal/config"
	"github.com/blindly/dispatcher/internal/db"
)

func FormatInterval(seconds int) string {
	switch {
	case seconds >= 604800:
		return fmt.Sprintf("%dw", seconds/604800)
	case seconds >= 86400:
		return fmt.Sprintf("%dd", seconds/86400)
	case seconds >= 3600:
		return fmt.Sprintf("%dh", seconds/3600)
	default:
		return fmt.Sprintf("%dm", seconds/60)
	}
}

func FormatDt(iso string) string {
	if iso == "" {
		return "-"
	}
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		if len(iso) >= 19 {
			return iso[:19]
		}
		return iso
	}
	return t.Format("2006-01-02 15:04")
}

func PrintStatus(conn *sql.DB, jobs map[string]*config.JobConfig, tzName string) {
	now := db.NowUTC()

	rows, err := conn.Query("SELECT name, last_run_at, next_run_at, last_status, last_duration_s, run_count, fail_count FROM cron_jobs ORDER BY next_run_at")
	if err != nil {
		fmt.Printf("Error querying jobs: %v\n", err)
		return
	}
	defer rows.Close()

	fmt.Printf("\n%-30s  %8s  %10s  %-19s  %10s  %-19s  %4s  %5s  %5s\n",
		"Name", "Interval", "Active", "Last Run", "Status", "Next Run", "Due", "Runs", "Fails")
	fmt.Println(strings.Repeat("-", 140))

	for rows.Next() {
		var name string
		var lastRun, nextRun, status sql.NullString
		var duration sql.NullFloat64
		var runCount, failCount int

		rows.Scan(&name, &lastRun, &nextRun, &status, &duration, &runCount, &failCount)

		job, ok := jobs[name]
		if !ok {
			continue
		}

		interval := FormatInterval(job.IntervalSeconds)
		lr := FormatDt(lastRun.String)
		if !lastRun.Valid {
			lr = "-"
		}
		nr := FormatDt(nextRun.String)
		st := "-"
		if status.Valid {
			st = status.String
		}

		isDue := ""
		if nextRun.Valid && nextRun.String <= now.Format(time.RFC3339) {
			isDue = "YES"
		}

		active := "always"
		if job.ActiveHours != nil {
			active = fmt.Sprintf("%02d-%02d", job.ActiveHours[0], job.ActiveHours[1])
			if !db.IsInActiveHours(job.ActiveHours, tzName) {
				isDue = ""
			}
		}

		fmt.Printf("%-30s  %8s  %10s  %-19s  %10s  %-19s  %4s  %5d  %5d\n",
			name, interval, active, lr, st, nr, isDue, runCount, failCount)
	}
	fmt.Println()
}
```

**Note:** Add `"strings"` to the import block.

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/display/ -v
```

Expected: all tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/display/
git commit -m "feat: display package — status table formatting"
```

---

### Task 7: Wire up main.go — full CLI integration

**Files:**
- Modify: `main.go`

- [ ] **Step 1: Replace main.go with full implementation**

```go
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
```

- [ ] **Step 2: Build and verify**

```bash
cd /home/wk/finance/dispatcher
go build -o dispatch .
./dispatch --help
./dispatch --config dispatcher.yaml list
```

Expected: help text prints, list shows empty table.

- [ ] **Step 3: Commit**

```bash
git add main.go
git commit -m "feat: wire up main.go with full CLI integration"
```

---

### Task 8: Integration smoke test

**Files:**
- Create: `main_test.go`

- [ ] **Step 1: Write integration tests**

```go
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func buildBinary(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "dispatch")
	cmd := exec.Command("go", "build", "-o", binary, ".")
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}
	return binary
}

func writeTestConfig(t *testing.T, dir string) string {
	t.Helper()
	cfg := `
timezone: America/New_York

jobs:
  echo_test:
    command: echo hello
    interval: 5m
    description: Test job
`
	path := filepath.Join(dir, "dispatcher.yaml")
	os.WriteFile(path, []byte(cfg), 0644)
	return path
}

func TestCLI_Help(t *testing.T) {
	binary := buildBinary(t)
	out, err := exec.Command(binary, "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("exit error: %v", err)
	}
	if !containsStr(string(out), "Usage:") {
		t.Errorf("help output missing Usage: %s", out)
	}
}

func TestCLI_List(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()
	cfgPath := writeTestConfig(t, dir)

	out, err := exec.Command(binary, "--config", cfgPath, "list").CombinedOutput()
	if err != nil {
		t.Fatalf("exit error: %v\n%s", err, out)
	}
	if !containsStr(string(out), "echo_test") {
		t.Errorf("list output missing echo_test: %s", out)
	}
}

func TestCLI_RunOnce(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()
	cfgPath := writeTestConfig(t, dir)

	out, err := exec.Command(binary, "--config", cfgPath, "run-once", "echo_test").CombinedOutput()
	if err != nil {
		t.Fatalf("exit error: %v\n%s", err, out)
	}
	if !containsStr(string(out), "hello") {
		t.Errorf("output missing hello: %s", out)
	}
}

func TestCLI_UnknownJob(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()
	cfgPath := writeTestConfig(t, dir)

	cmd := exec.Command(binary, "--config", cfgPath, "run-once", "nonexistent")
	err := cmd.Run()
	if err == nil {
		t.Error("expected error for unknown job")
	}
}

func TestCLI_UnknownCommand(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()
	cfgPath := writeTestConfig(t, dir)

	cmd := exec.Command(binary, "--config", cfgPath, "bogus")
	err := cmd.Run()
	if err == nil {
		t.Error("expected error for unknown command")
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && len(sub) > 0 && indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
```

- [ ] **Step 2: Run integration tests**

```bash
cd /home/wk/finance/dispatcher
go test -v -run TestCLI
```

Expected: all tests PASS.

- [ ] **Step 3: Run full test suite**

```bash
go test ./... -v
```

Expected: all tests across all packages PASS.

- [ ] **Step 4: Commit**

```bash
git add main_test.go
git commit -m "test: integration smoke tests for CLI subcommands"
```

---

### Task 9: CLAUDE.md and README.md

**Files:**
- Create: `CLAUDE.md`
- Create: `README.md`

- [ ] **Step 1: Write CLAUDE.md**

```markdown
# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

YAML-configured cron dispatcher written in Go. Single static binary that runs shell commands on intervals, tracks state in SQLite, and sends Discord webhook notifications.

## Commands

` `` bash
# Build
go build -o dispatch .

# Run tests
go test ./...

# Run a single test
go test ./internal/config/ -run TestParseInterval_Minutes -v

# Run the dispatcher
./dispatch                           # normal dispatch (runs due jobs)
./dispatch list                      # show job status table
./dispatch run <job>                 # force-run a specific job
./dispatch run-once <job>            # run without DB tracking
./dispatch reset <job>               # reset next_run to now
./dispatch install "*/5 * * * *"     # install crontab entry
` ``

## Architecture

Single Go module with CLI entry point in `main.go` and five internal packages:

1. **internal/config** — Loads `dispatcher.yaml`, expands `${ENV_VAR}` references, parses intervals (`5m`, `2h`, `1d`) to seconds. Produces `DispatcherConfig` + `JobConfig` structs.
2. **internal/db** — SQLite `cron_jobs` table via `modernc.org/sqlite` (pure Go). Determines due jobs, respects `active_hours`, updates state after runs.
3. **internal/runner** — Executes commands via `os/exec` with 600s timeout. Per-job retry with configurable count and delay. Writes per-job logs to `logs/<name>.log`.
4. **internal/notify** — Posts dispatch summaries to Discord via webhook embeds.
5. **internal/display** — Formats the `list` status table.

## Key Design Decisions

- **Two external dependencies**: `gopkg.in/yaml.v3` and `modernc.org/sqlite` (pure Go, no CGo).
- **File lock** (`.dispatch.lock`) prevents concurrent dispatch runs; read-only operations (`list`) skip the lock.
- **`run-once`** bypasses both DB and lock for manual testing.
- SQLite stored next to config as `data.db`. All times UTC ISO 8601.
```

- [ ] **Step 2: Write README.md**

(Content mirrors the Python version's README adapted for Go — build from source, subcommand usage, config format, how it works.)

```markdown
# dispatch

YAML-configured cron dispatcher with SQLite job tracking and Discord notifications. Single static binary — no runtime dependencies.

## Install

` ``bash
go build -o dispatch .
` ``

Or download a binary from [Releases](https://github.com/blindly/dispatcher/releases).

## Configuration

Create a `dispatcher.yaml`:

` ``yaml
timezone: America/New_York

notify:
  discord:
    webhook: ${DISCORD_WEBHOOK_URL}

jobs:
  fetch_data:
    command: python scripts/fetch.py
    interval: 30m
    description: Fetch latest data
    active_hours: [9, 17]
    retries: 3
    retry_delay: 10s

  process_data:
    command: python scripts/process.py
    interval: 1h
    description: Process fetched data
    depends_on: fetch_data
` ``

### Job options

| Field | Required | Default | Description |
|---|---|---|---|
| `command` | yes | | Shell command to execute |
| `interval` | yes | | Run frequency: `30s`, `5m`, `2h`, `1d`, `1w` |
| `description` | no | | Shown in logs and notifications |
| `active_hours` | no | | `[start, end]` hours when the job is allowed to run |
| `depends_on` | no | | Name of another job that must succeed first |
| `retries` | no | `2` | Number of retry attempts on failure |
| `retry_delay` | no | `5s` | Delay between retries |

Environment variables in the form `${VAR_NAME}` are expanded throughout the config.

## Usage

` ``bash
# Run the dispatcher (executes all due jobs)
dispatch

# Show job status table
dispatch list

# Force-run a specific job
dispatch run fetch_data

# Run a job without DB tracking
dispatch run-once fetch_data

# Reset a job so it runs on the next dispatch
dispatch reset fetch_data

# Force-run all jobs
dispatch run-all
` ``

## Crontab integration

` ``bash
# Install (default: every 5 minutes)
dispatch install

# Custom schedule
dispatch install "*/10 * * * *"

# Remove
dispatch uninstall
` ``

## How it works

1. Reads `dispatcher.yaml` and checks SQLite for jobs where `next_run_at <= now`.
2. Due jobs are ordered so dependencies run first.
3. Each job runs as a subprocess with a 600s timeout. Failed jobs retry per their config.
4. After each run, the DB is updated with the result and next scheduled time.
5. A summary is posted to Discord (if configured).

SQLite state is stored as `data.db` next to the config file. A file lock prevents concurrent dispatch runs. Per-job output is logged to `logs/<name>.log`.

## Development

` ``bash
go test ./...
go build -o dispatch .
` ``
```

- [ ] **Step 3: Commit**

```bash
git add CLAUDE.md README.md
git commit -m "docs: add CLAUDE.md and README.md"
```

---

## Self-Review

**Spec coverage check:**
- Config format with nested notify, per-job retries/retry_delay — Task 2
- SQLite schema, due jobs, active hours, update after run — Task 3
- Subprocess exec, retry, dependency ordering, job logging — Task 4
- Discord webhook embeds with color logic — Task 5
- Status table formatting — Task 6
- CLI subcommands (all 8) — Task 1 scaffold, Task 7 full wiring
- File lock — Task 7
- Build/distribution — Task 9 README
- Testing — all tasks include tests, Task 8 integration

**Placeholder scan:** No TBDs, TODOs, or "implement later" found.

**Type consistency check:**
- `config.JobConfig` — consistent fields: `Name`, `Command`, `IntervalSeconds`, `Description`, `ActiveHours`, `DependsOn`, `Retries`, `RetryDelay`
- `config.Load()` returns `*DispatcherConfig` — used consistently in main.go
- `db.Open()` returns `*sql.DB` — used consistently across runner, display, main
- `notify.JobResult` struct — used in main.go when building results
- `runner.RunOnce` returns `(int, string)`, `runner.RunJob` returns `(int, float64, string)` — consistent across all call sites
