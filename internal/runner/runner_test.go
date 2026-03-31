package runner

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/blindly/dispatcher/internal/config"
	"github.com/blindly/dispatcher/internal/db"
)

func TestRunOnce_Success(t *testing.T) {
	job := &config.JobConfig{
		Name:    "echo_test",
		Commands: []string{"echo hello"},
	}
	rc, output := RunOnce(job, nil, nil)
	if rc != 0 {
		t.Errorf("rc = %d, want 0", rc)
	}
	if !strings.Contains(output, "hello") {
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
		Commands: []string{cmd},
	}
	rc, _ := RunOnce(job, nil, nil)
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
		Commands:        []string{"echo hello"},
		IntervalSeconds: 300,
		Retries:         0,
		RetryDelay:      1,
	}
	jobs := map[string]*config.JobConfig{"test_echo": job}
	db.EnsureJobs(conn, jobs)

	rc, _, output := RunJob(conn, job, nil, nil)
	if rc != 0 {
		t.Errorf("rc = %d, want 0", rc)
	}
	if !strings.Contains(output, "hello") {
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

func TestRunOnce_MultipleCommands(t *testing.T) {
	job := &config.JobConfig{
		Name:     "multi_test",
		Commands: []string{"echo hello", "echo world"},
	}
	rc, output := RunOnce(job, nil, nil)
	if rc != 0 {
		t.Errorf("rc = %d, want 0", rc)
	}
	if !strings.Contains(output, "hello") || !strings.Contains(output, "world") {
		t.Errorf("output = %q, want both hello and world", output)
	}
}

func TestRunOnce_MultipleCommandsStopsOnFailure(t *testing.T) {
	cmd := "false"
	if runtime.GOOS == "windows" {
		cmd = "cmd /c exit 1"
	}
	job := &config.JobConfig{
		Name:     "multi_fail_test",
		Commands: []string{cmd, "echo should-not-run"},
	}
	rc, output := RunOnce(job, nil, nil)
	if rc == 0 {
		t.Error("expected non-zero exit code")
	}
	if strings.Contains(output, "should-not-run") {
		t.Error("second command should not have run")
	}
}

func TestRunOnce_ExtraArgs(t *testing.T) {
	job := &config.JobConfig{
		Name:     "args_test",
		Commands: []string{"echo"},
	}
	rc, output := RunOnce(job, []string{"extra", "args"}, nil)
	if rc != 0 {
		t.Errorf("rc = %d, want 0", rc)
	}
	if !strings.Contains(output, "extra") || !strings.Contains(output, "args") {
		t.Errorf("output = %q, want to contain extra args", output)
	}
}

func TestRunOnce_ExtraEnv(t *testing.T) {
	job := &config.JobConfig{
		Name:     "env_test",
		Commands: []string{"env"},
	}
	rc, output := RunOnce(job, nil, []string{"MY_TEST_VAR=hello123"})
	if rc != 0 {
		t.Errorf("rc = %d, want 0", rc)
	}
	if !strings.Contains(output, "MY_TEST_VAR=hello123") {
		t.Errorf("output missing env var: %q", output)
	}
}

func TestRunOnce_CLIArgsTemplate(t *testing.T) {
	job := &config.JobConfig{
		Name:     "cli_args_test",
		Commands: []string{"echo prefix {{.CLI_ARGS}} suffix"},
	}
	rc, output := RunOnce(job, []string{"hello", "world"}, nil)
	if rc != 0 {
		t.Errorf("rc = %d, want 0", rc)
	}
	if !strings.Contains(output, "prefix hello world suffix") {
		t.Errorf("output = %q, want 'prefix hello world suffix'", output)
	}
}

func TestRunOnce_CLIArgsEmpty(t *testing.T) {
	job := &config.JobConfig{
		Name:     "cli_args_empty",
		Commands: []string{"echo before {{.CLI_ARGS}} after"},
	}
	rc, output := RunOnce(job, nil, nil)
	if rc != 0 {
		t.Errorf("rc = %d, want 0", rc)
	}
	if !strings.Contains(output, "before") || !strings.Contains(output, "after") {
		t.Errorf("output = %q", output)
	}
}

func TestResolveOrder_RespectsDependencies(t *testing.T) {
	jobs := map[string]*config.JobConfig{
		"fetch": {Name: "fetch", Commands: []string{"echo"}, IntervalSeconds: 300},
		"scan":  {Name: "scan", Commands: []string{"echo"}, IntervalSeconds: 300, DependsOn: "fetch"},
		"other": {Name: "other", Commands: []string{"echo"}, IntervalSeconds: 300},
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
		"fetch": {Name: "fetch", Commands: []string{"echo"}, IntervalSeconds: 300},
		"scan":  {Name: "scan", Commands: []string{"echo"}, IntervalSeconds: 300, DependsOn: "fetch"},
	}
	due := []string{"scan"}
	ordered := ResolveOrder(due, jobs)
	if len(ordered) != 1 || ordered[0] != "scan" {
		t.Errorf("got %v, want [scan]", ordered)
	}
}
