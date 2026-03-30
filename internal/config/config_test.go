package config

import (
	"os"
	"path/filepath"
	"testing"
)

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

func TestLoad_DbPath(t *testing.T) {
	yaml := `
db_path: custom/data.db
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
	if cfg.DbPath != "custom/data.db" {
		t.Errorf("db_path = %q, want custom/data.db", cfg.DbPath)
	}
}

func TestLoad_LegacyDiscordWebhook(t *testing.T) {
	yaml := `
discord_webhook: https://discord.com/old-style
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
	if cfg.Notify.Discord.Webhook != "https://discord.com/old-style" {
		t.Errorf("webhook = %q, want old-style URL", cfg.Notify.Discord.Webhook)
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

func TestLoad_CustomTimeout(t *testing.T) {
	yaml := `
jobs:
  j1:
    command: echo hi
    interval: 1h
    timeout: 2m
`
	dir := t.TempDir()
	path := filepath.Join(dir, "dispatcher.yaml")
	os.WriteFile(path, []byte(yaml), 0644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	job := cfg.Jobs["j1"]
	if job.Timeout != 120 {
		t.Errorf("timeout = %d, want 120", job.Timeout)
	}
}

func TestLoad_DefaultTimeout(t *testing.T) {
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
	job := cfg.Jobs["j1"]
	if job.Timeout != 600 {
		t.Errorf("timeout = %d, want 600", job.Timeout)
	}
}
