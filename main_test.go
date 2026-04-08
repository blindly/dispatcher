package main

import (
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func buildBinary(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "dispatch")
	cmd := exec.Command("go", "build", "-o", binary, ".")
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
	if !strings.Contains(string(out), "Usage:") {
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
	if !strings.Contains(string(out), "echo_test") {
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
	if !strings.Contains(string(out), "hello") {
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

func TestCLI_Status(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()
	cfgPath := writeTestConfig(t, dir)

	out, err := exec.Command(binary, "--config", cfgPath, "status").CombinedOutput()
	if err != nil {
		t.Fatalf("exit error: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "jobs") {
		t.Errorf("status output missing 'jobs': %s", out)
	}
}

func TestCLI_Version(t *testing.T) {
	binary := buildBinary(t)
	out, err := exec.Command(binary, "version").CombinedOutput()
	if err != nil {
		t.Fatalf("exit error: %v", err)
	}
	if !strings.Contains(string(out), "dispatch") {
		t.Errorf("version output missing 'dispatch': %s", out)
	}
}

func TestCLI_Init(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()

	cmd := exec.Command(binary, "init")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("exit error: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "Created Dispatcher.yaml") {
		t.Errorf("unexpected output: %s", out)
	}

	// Verify file exists
	content, err := os.ReadFile(filepath.Join(dir, "Dispatcher.yaml"))
	if err != nil {
		t.Fatal("Dispatcher.yaml not created")
	}
	if !strings.Contains(string(content), "timezone") {
		t.Error("config missing timezone")
	}
	if !strings.Contains(string(content), "jobs:") {
		t.Error("config missing jobs section")
	}
}

func TestCLI_InitAlreadyExists(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()

	// Create existing config
	os.WriteFile(filepath.Join(dir, "dispatcher.yaml"), []byte("existing"), 0644)

	cmd := exec.Command(binary, "init")
	cmd.Dir = dir
	err := cmd.Run()
	if err == nil {
		t.Error("expected error when config already exists")
	}
}

func TestCLI_Validate(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()
	cfgPath := writeTestConfig(t, dir)

	out, err := exec.Command(binary, "--config", cfgPath, "validate").CombinedOutput()
	if err != nil {
		t.Fatalf("exit error: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "Config OK") {
		t.Errorf("validate output missing 'Config OK': %s", out)
	}
}

func TestCLI_ValidateBadDep(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()
	cfg := `
jobs:
  broken:
    command: echo hi
    interval: 5m
    depends_on: nonexistent
`
	path := filepath.Join(dir, "dispatcher.yaml")
	os.WriteFile(path, []byte(cfg), 0644)

	out, err := exec.Command(binary, "--config", path, "validate").CombinedOutput()
	if err != nil {
		t.Fatalf("exit error: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "WARNING") {
		t.Errorf("expected WARNING for bad dependency: %s", out)
	}
}

func TestCLI_LogsNoFile(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()
	cfgPath := writeTestConfig(t, dir)

	out, err := exec.Command(binary, "--config", cfgPath, "logs", "echo_test").CombinedOutput()
	if err != nil {
		t.Fatalf("exit error: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "No logs found") {
		t.Errorf("expected 'No logs found': %s", out)
	}
}

func TestCLI_DetectsYml(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()

	// Create a .yml config (not .yaml)
	cfg := `
timezone: America/New_York
jobs:
  yml_test:
    command: echo yml
    interval: 5m
`
	os.WriteFile(filepath.Join(dir, "dispatcher.yml"), []byte(cfg), 0644)

	// Should auto-detect dispatcher.yml without --config
	cmd := exec.Command(binary, "list")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("exit error: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "yml_test") {
		t.Errorf("list output missing yml_test (auto-detect failed): %s", out)
	}
}

func TestCLI_Pause(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()
	cfgPath := writeTestConfig(t, dir)

	out, err := exec.Command(binary, "--config", cfgPath, "pause").CombinedOutput()
	if err != nil {
		t.Fatalf("exit error: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "Dispatcher paused") {
		t.Errorf("unexpected output: %s", out)
	}

	// Verify pause file exists
	pausePath := filepath.Join(dir, ".dispatcher", "paused")
	if _, err := os.Stat(pausePath); os.IsNotExist(err) {
		t.Error("pause file should exist")
	}
}

func TestCLI_PauseWithDurationAndReason(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()
	cfgPath := writeTestConfig(t, dir)

	out, err := exec.Command(binary, "--config", cfgPath, "pause", "2h", "deploying changes").CombinedOutput()
	if err != nil {
		t.Fatalf("exit error: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "2h") {
		t.Errorf("output should mention duration: %s", out)
	}
	if !strings.Contains(string(out), "deploying changes") {
		t.Errorf("output should mention reason: %s", out)
	}
}

func TestCLI_Resume(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()
	cfgPath := writeTestConfig(t, dir)

	// Pause first
	exec.Command(binary, "--config", cfgPath, "pause").CombinedOutput()

	// Then resume
	out, err := exec.Command(binary, "--config", cfgPath, "resume").CombinedOutput()
	if err != nil {
		t.Fatalf("exit error: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "resumed") {
		t.Errorf("unexpected output: %s", out)
	}

	// Verify pause file is gone
	pausePath := filepath.Join(dir, ".dispatcher", "paused")
	if _, err := os.Stat(pausePath); !os.IsNotExist(err) {
		t.Error("pause file should be removed after resume")
	}
}

func TestCLI_ResumeWhenNotPaused(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()
	cfgPath := writeTestConfig(t, dir)

	out, err := exec.Command(binary, "--config", cfgPath, "resume").CombinedOutput()
	if err != nil {
		t.Fatalf("exit error: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "not paused") {
		t.Errorf("expected 'not paused' message: %s", out)
	}
}

func TestCLI_DispatchWhilePaused(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()
	cfgPath := writeTestConfig(t, dir)

	// Pause
	exec.Command(binary, "--config", cfgPath, "pause").CombinedOutput()

	// Default dispatch should be blocked
	out, err := exec.Command(binary, "--config", cfgPath).CombinedOutput()
	if err != nil {
		t.Fatalf("exit error: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "paused") {
		t.Errorf("dispatch should mention paused: %s", out)
	}
}

func TestCLI_RunWhilePaused(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()
	cfgPath := writeTestConfig(t, dir)

	// Pause
	exec.Command(binary, "--config", cfgPath, "pause").CombinedOutput()

	// Manual run should still work
	out, err := exec.Command(binary, "--config", cfgPath, "run-once", "echo_test").CombinedOutput()
	if err != nil {
		t.Fatalf("exit error: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "hello") {
		t.Errorf("run-once should still work while paused: %s", out)
	}
}

func TestCLI_ExecAlias(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()
	cfgPath := writeTestConfig(t, dir)

	out, err := exec.Command(binary, "--config", cfgPath, "exec", "echo_test").CombinedOutput()
	if err != nil {
		t.Fatalf("exit error: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "hello") {
		t.Errorf("exec should work like run-once: %s", out)
	}
}

func TestMigration_PreservesData(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()
	cfgPath := writeTestConfig(t, dir)

	// Create a data.db in the project root (old location) with WAL mode and run history
	dbPath := filepath.Join(dir, "data.db")
	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	conn.Exec("PRAGMA journal_mode=WAL")
	conn.Exec("PRAGMA busy_timeout=10000")
	conn.Exec(`CREATE TABLE IF NOT EXISTS cron_jobs (
		name TEXT PRIMARY KEY, last_run_at TEXT, next_run_at TEXT NOT NULL,
		last_status TEXT, last_duration_s REAL, run_count INTEGER DEFAULT 0,
		fail_count INTEGER DEFAULT 0, running_since TEXT)`)
	conn.Exec(`CREATE TABLE IF NOT EXISTS job_runs (
		id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL,
		run_at TEXT NOT NULL, status TEXT NOT NULL, exit_code INTEGER, duration_s REAL)`)
	conn.Exec("INSERT INTO cron_jobs (name, next_run_at, last_run_at, last_status, run_count) VALUES (?, ?, ?, ?, ?)",
		"echo_test", "2026-01-01T00:00:00Z", "2026-04-04T12:00:00Z", "ok", 50)
	for i := 0; i < 25; i++ {
		conn.Exec("INSERT INTO job_runs (name, run_at, status, exit_code, duration_s) VALUES (?, ?, ?, ?, ?)",
			"echo_test", "2026-04-04T12:00:00Z", "ok", 0, 1.5)
	}
	// Verify WAL file exists while DB is open (proves WAL mode is active)
	if _, err := os.Stat(filepath.Join(dir, "data.db-wal")); os.IsNotExist(err) {
		t.Fatal("WAL file should exist before migration")
	}
	conn.Close()

	// Run any command to trigger migration
	out, err := exec.Command(binary, "--config", cfgPath, "list").CombinedOutput()
	if err != nil {
		t.Fatalf("exit error: %v\n%s", err, out)
	}

	// Verify old data.db is gone
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Error("old data.db should be gone after migration")
	}

	// Verify new data.db exists
	newDbPath := filepath.Join(dir, ".dispatcher", "data.db")
	if _, err := os.Stat(newDbPath); os.IsNotExist(err) {
		t.Fatal(".dispatcher/data.db should exist after migration")
	}

	// Verify data survived
	conn2, err := sql.Open("sqlite", newDbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn2.Close()

	var runCount int
	conn2.QueryRow("SELECT run_count FROM cron_jobs WHERE name = ?", "echo_test").Scan(&runCount)
	if runCount != 50 {
		t.Errorf("run_count = %d, want 50 (data lost during migration)", runCount)
	}

	var histCount int
	conn2.QueryRow("SELECT COUNT(*) FROM job_runs WHERE name = ?", "echo_test").Scan(&histCount)
	if histCount != 25 {
		t.Errorf("job_runs count = %d, want 25 (history lost during migration)", histCount)
	}
}
