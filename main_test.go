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
