package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
	if !strings.Contains(string(out), "Created dispatcher.yaml") {
		t.Errorf("unexpected output: %s", out)
	}

	// Verify file exists
	content, err := os.ReadFile(filepath.Join(dir, "dispatcher.yaml"))
	if err != nil {
		t.Fatal("dispatcher.yaml not created")
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
